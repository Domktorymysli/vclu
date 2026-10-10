package web

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
)

// dashboardCache provides Go-side TTL caching to reduce Lua mutex contention.
type dashboardCache struct {
	mu      sync.RWMutex
	values  map[string]*cacheEntry // dashId → cached getValues JSON
	defs    map[string]*cacheEntry // dashId → cached getDefinition JSON
	metrics map[string]*cacheEntry // dashId\x00path → usesMetric JSON (drawn metrics only)
}

type cacheEntry struct {
	data      string
	expiresAt time.Time
}

func newDashboardCache() *dashboardCache {
	return &dashboardCache{
		values:  make(map[string]*cacheEntry),
		defs:    make(map[string]*cacheEntry),
		metrics: make(map[string]*cacheEntry),
	}
}

func (dc *dashboardCache) getMetric(key string) (string, bool) {
	dc.mu.RLock()
	defer dc.mu.RUnlock()
	e, ok := dc.metrics[key]
	if !ok || time.Now().After(e.expiresAt) {
		return "", false
	}
	return e.data, true
}

func (dc *dashboardCache) setMetric(key, data string) {
	dc.mu.Lock()
	defer dc.mu.Unlock()
	dc.metrics[key] = &cacheEntry{data: data, expiresAt: time.Now().Add(30 * time.Second)}
}

func (dc *dashboardCache) getValues(id string) (string, bool) {
	dc.mu.RLock()
	defer dc.mu.RUnlock()
	e, ok := dc.values[id]
	if !ok || time.Now().After(e.expiresAt) {
		return "", false
	}
	return e.data, true
}

func (dc *dashboardCache) setValues(id string, data string, ttl time.Duration) {
	dc.mu.Lock()
	defer dc.mu.Unlock()
	dc.values[id] = &cacheEntry{data: data, expiresAt: time.Now().Add(ttl)}
}

func (dc *dashboardCache) getDef(id string) (string, bool) {
	dc.mu.RLock()
	defer dc.mu.RUnlock()
	e, ok := dc.defs[id]
	if !ok || time.Now().After(e.expiresAt) {
		return "", false
	}
	return e.data, true
}

func (dc *dashboardCache) setDef(id string, data string) {
	dc.mu.Lock()
	defer dc.mu.Unlock()
	dc.defs[id] = &cacheEntry{data: data, expiresAt: time.Now().Add(30 * time.Second)}
}

// Invalidate clears all cached data (called on Lua module reload).
func (dc *dashboardCache) Invalidate() {
	dc.mu.Lock()
	defer dc.mu.Unlock()
	dc.values = make(map[string]*cacheEntry)
	dc.defs = make(map[string]*cacheEntry)
	dc.metrics = make(map[string]*cacheEntry)
}

// dashCache is the singleton cache instance, initialized lazily.
var dashCache = newDashboardCache()

// GetDashboardCacheInvalidator returns a function to invalidate the dashboard cache.
// Register this as a callback on Lua reloads.
func GetDashboardCacheInvalidator() func() {
	return dashCache.Invalidate
}

// --------------------------------------------------------------------------
// Rate limiter (token bucket per IP, for action endpoints)
// --------------------------------------------------------------------------

type rateLimiter struct {
	mu      sync.Mutex
	buckets map[string]*bucket
	rate    float64 // tokens per second
	burst   float64
}

type bucket struct {
	tokens    float64
	lastCheck time.Time
}

var actionLimiter = &rateLimiter{buckets: make(map[string]*bucket), rate: 10, burst: 10}

// pinLimiter: 5 PIN tries at once, then one per 6 s (a 4-digit PIN takes hours)
var pinLimiter = &rateLimiter{buckets: make(map[string]*bucket), rate: 1.0 / 6, burst: 5}

func (rl *rateLimiter) allow(ip string) bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	now := time.Now()
	b, ok := rl.buckets[ip]
	if !ok {
		b = &bucket{tokens: rl.burst, lastCheck: now}
		rl.buckets[ip] = b
	}
	elapsed := now.Sub(b.lastCheck).Seconds()
	b.lastCheck = now
	b.tokens += elapsed * rl.rate
	if b.tokens > rl.burst {
		b.tokens = rl.burst
	}
	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}

// --------------------------------------------------------------------------
// Page handlers
// --------------------------------------------------------------------------

// handleDashboardPage serves the public dashboard page at /d/:id
func (s *Server) handleDashboardPage(c *gin.Context) {
	id := c.Param("id")
	if s.luaExecutor == nil {
		c.String(http.StatusServiceUnavailable, "Lua engine not ready")
		return
	}

	// Get definition to determine auth type and theme
	script := fmt.Sprintf(`Dashboard.API.getDefinition("%s")`, luaEscape(id))
	result, err := s.luaExecutor.Execute(script)
	if err != nil {
		c.String(http.StatusInternalServerError, "Error loading dashboard")
		log.Printf("Dashboard %s error: %v", id, err)
		return
	}

	var def map[string]interface{}
	if err := json.Unmarshal([]byte(result), &def); err != nil {
		c.String(http.StatusInternalServerError, "Invalid dashboard definition")
		return
	}
	if _, hasErr := def["error"]; hasErr {
		c.String(http.StatusNotFound, "Dashboard not found")
		return
	}

	// Auth check
	auth, _ := def["auth"].(string)
	switch auth {
	case "session":
		if !s.isAuthenticated(c) {
			c.Redirect(http.StatusFound, "/login?next=/d/"+id)
			return
		}
	case "pin":
		cookieName := "vclu_dash_" + id
		cookie, err := c.Cookie(cookieName)
		if err != nil || !s.validateDashPinCookie(id, cookie) {
			// Show PIN entry page
			c.HTML(http.StatusOK, "dashboard_pin.html", gin.H{
				"id":   id,
				"name": def["name"],
				"icon": def["icon"],
			})
			return
		}
	}
	// auth == "public" → always allow

	// Parse theme for CSS vars
	themeVars := ""
	if theme, ok := def["theme"].(map[string]interface{}); ok {
		vars := ":root{"
		themeKeys := []struct{ lua, css string }{
			{"accent", "--dash-accent"},
			{"background", "--dash-bg"},
			{"cardBg", "--dash-card-bg"},
			{"text", "--dash-text"},
		}
		for _, tk := range themeKeys {
			if v, ok := theme[tk.lua].(string); ok {
				vars += tk.css + ":" + v + ";"
			}
		}
		// Wallpaper as url()
		if wp, ok := theme["wallpaper"].(string); ok && wp != "" {
			vars += "--dash-wallpaper:url(" + wp + ");"
		}
		vars += "}"
		themeVars = vars
	}

	c.HTML(http.StatusOK, "dashboard.html", gin.H{
		"id":      id,
		"name":    def["name"],
		"icon":    def["icon"],
		"theme":   themeVars,
		"columns": def["columns"],
	})
}

// handleDashboardCustomCSS serves modules/custom.css for dashboard theme overrides.
func (s *Server) handleDashboardCustomCSS(c *gin.Context) {
	cssPath := filepath.Join(s.serverDir, "modules", "custom.css")
	data, err := os.ReadFile(cssPath)
	if err != nil {
		c.Data(http.StatusOK, "text/css; charset=utf-8", []byte("/* no custom.css */\n"))
		return
	}
	c.Data(http.StatusOK, "text/css; charset=utf-8", data)
}

// handleDashboards serves the management page at /dashboards
func (s *Server) handleDashboards(c *gin.Context) {
	trans := s.getTranslations(c)

	var dashboards []map[string]interface{}
	if s.luaExecutor != nil {
		result, err := s.luaExecutor.Execute("Dashboard.API.list()")
		if err == nil {
			json.Unmarshal([]byte(result), &dashboards)
		}
	}

	s.renderHTML(c, http.StatusOK, "dashboards.html", gin.H{
		"title":       "Dashboards",
		"currentPage": "dashboards",
		"configured":  s.isConfigured,
		"dashboards":  dashboards,
		"lang":        trans["lang"],
		"t":           trans["t"],
	})
}

// --------------------------------------------------------------------------
// API handlers
// --------------------------------------------------------------------------

// handleAPIDashboards returns the list of all dashboards (JSON)
func (s *Server) handleAPIDashboards(c *gin.Context) {
	if s.luaExecutor == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "Lua engine not ready"})
		return
	}
	result, err := s.luaExecutor.Execute("Dashboard.API.list()")
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.Data(http.StatusOK, "application/json", []byte(result))
}

// handleAPIDashboardDef returns full definition + current values
func (s *Server) handleAPIDashboardDef(c *gin.Context) {
	id := c.Param("id")
	if s.luaExecutor == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "Lua engine not ready"})
		return
	}

	// Check per-dashboard auth
	if !s.checkDashboardAuth(c, id) {
		return
	}

	// Check cache
	if cached, ok := dashCache.getDef(id); ok {
		c.Data(http.StatusOK, "application/json", []byte(cached))
		return
	}

	script := fmt.Sprintf(`Dashboard.API.getDefinition("%s")`, luaEscape(id))
	result, err := s.luaExecutor.Execute(script)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	// Cache the definition
	dashCache.setDef(id, result)

	c.Data(http.StatusOK, "application/json", []byte(result))
}

// handleAPIDashboardValues returns widget values for polling
func (s *Server) handleAPIDashboardValues(c *gin.Context) {
	id := c.Param("id")
	if s.luaExecutor == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "Lua engine not ready"})
		return
	}

	if !s.checkDashboardAuth(c, id) {
		return
	}

	// Check values cache
	if cached, ok := dashCache.getValues(id); ok {
		c.Data(http.StatusOK, "application/json", []byte(cached))
		return
	}

	script := fmt.Sprintf(`Dashboard.API.getValues("%s")`, luaEscape(id))
	result, err := s.luaExecutor.Execute(script)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	// Cache with TTL = 1s (half of default poll interval)
	dashCache.setValues(id, result, 1*time.Second)

	c.Data(http.StatusOK, "application/json", []byte(result))
}

// handleAPIDashboardMetrics returns samples of a metric the dashboard draws,
// under the dashboard's own auth (a public page needs no login for its charts)
func (s *Server) handleAPIDashboardMetrics(c *gin.Context) {
	id := c.Param("id")
	if s.luaExecutor == nil || s.metricsCollector == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "metrics not available"})
		return
	}
	if !s.checkDashboardAuth(c, id) {
		return
	}
	path := c.Query("path")
	if path == "" || len(path) > 256 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "path is required (max 256 chars)"})
		return
	}
	key := id + "\x00" + path
	result, ok := dashCache.getMetric(key)
	if !ok {
		var err error
		result, err = s.luaExecutor.Execute(fmt.Sprintf(`Dashboard.API.usesMetric("%s", "%s")`, luaEscape(id), luaEscape(path)))
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
	}
	var r struct {
		Used   bool     `json:"used"`
		Ranges []string `json:"ranges"`
	}
	json.Unmarshal([]byte(result), &r)
	if !r.Used {
		c.JSON(http.StatusForbidden, gin.H{"error": "metric not on this dashboard"})
		return
	}
	if !ok {
		dashCache.setMetric(key, result)
	}
	if rg := capRange(c.DefaultQuery("range", "2h"), r.Ranges); rg != "" {
		q := c.Request.URL.Query()
		q.Set("range", rg)
		c.Request.URL.RawQuery = q.Encode()
	}
	s.writeMetricSamples(c, path)
}

// capRange returns the widest drawn range when the requested one is wider
// (no more history than the dashboard shows), "" when it fits.
func capRange(requested string, drawn []string) string {
	var widest time.Duration
	for _, rg := range drawn {
		widest = max(widest, parseRange(rg))
	}
	if parseRange(requested) <= widest {
		return ""
	}
	return widest.String()
}

// handleAPIDashboardAction executes a widget action
func (s *Server) handleAPIDashboardAction(c *gin.Context) {
	id := c.Param("id")
	if s.luaExecutor == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "Lua engine not ready"})
		return
	}

	if !s.checkDashboardAuth(c, id) {
		return
	}

	// Rate limit
	if !actionLimiter.allow(c.ClientIP()) {
		c.JSON(http.StatusTooManyRequests, gin.H{"error": "Rate limit exceeded"})
		return
	}

	var req struct {
		WidgetID string      `json:"widgetId"`
		Action   string      `json:"action"`
		Value    interface{} `json:"value"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request body"})
		return
	}

	// Build Lua call
	valueStr := "nil"
	switch v := req.Value.(type) {
	case bool:
		if v {
			valueStr = "true"
		} else {
			valueStr = "false"
		}
	case float64:
		valueStr = fmt.Sprintf("%g", v)
	case string:
		valueStr = fmt.Sprintf(`"%s"`, luaEscape(v))
	}

	script := fmt.Sprintf(`Dashboard.API.executeAction("%s", "%s", "%s", %s)`,
		luaEscape(id), luaEscape(req.WidgetID), luaEscape(req.Action), valueStr)
	result, err := s.executeAs(c, "dashboard:"+id, script)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	// Invalidate values cache after action
	dashCache.mu.Lock()
	delete(dashCache.values, id)
	dashCache.mu.Unlock()

	c.Data(http.StatusOK, "application/json", []byte(result))
}

// handleAPIDashboardAuth verifies PIN and sets cookie
func (s *Server) handleAPIDashboardAuth(c *gin.Context) {
	id := c.Param("id")
	if s.luaExecutor == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "Lua engine not ready"})
		return
	}

	if !pinLimiter.allow(c.ClientIP()) {
		c.JSON(http.StatusTooManyRequests, gin.H{"error": "Too many PIN attempts, try again later"})
		return
	}

	var req struct {
		Pin string `json:"pin"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request"})
		return
	}

	script := fmt.Sprintf(`Dashboard.API.verifyPin("%s", "%s")`, luaEscape(id), luaEscape(req.Pin))
	result, err := s.luaExecutor.Execute(script)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	var resp struct {
		Valid bool `json:"valid"`
	}
	json.Unmarshal([]byte(result), &resp)

	if !resp.Valid {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Invalid PIN"})
		return
	}

	// Set PIN cookie
	cookieName := "vclu_dash_" + id
	cookieValue := s.makeDashPinCookie(id, req.Pin, time.Now().Add(24*time.Hour).Unix())
	http.SetCookie(c.Writer, &http.Cookie{
		Name:     cookieName,
		Value:    cookieValue,
		Path:     "/",
		MaxAge:   86400, // 24h
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
		Secure:   isHTTPS(c),
	})

	c.JSON(http.StatusOK, gin.H{"success": true})
}

// --------------------------------------------------------------------------
// Auth helpers
// --------------------------------------------------------------------------

// checkDashboardAuth checks per-dashboard auth for API endpoints.
// Returns true if access is allowed, false if response was already written.
// Uses lightweight getAuth() instead of getDefinition() to avoid evaluating all widget callbacks.
func (s *Server) checkDashboardAuth(c *gin.Context, id string) bool {
	script := fmt.Sprintf(`Dashboard.API.getAuth("%s")`, luaEscape(id))
	result, err := s.luaExecutor.Execute(script)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return false
	}

	var def struct {
		Auth  string `json:"auth"`
		Error string `json:"error"`
	}
	json.Unmarshal([]byte(result), &def)
	if def.Error != "" {
		c.JSON(http.StatusNotFound, gin.H{"error": def.Error})
		return false
	}

	switch def.Auth {
	case "session":
		if !s.isAuthenticated(c) {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "Authentication required"})
			return false
		}
	case "pin":
		cookieName := "vclu_dash_" + id
		cookie, err := c.Cookie(cookieName)
		if err != nil || !s.validateDashPinCookie(id, cookie) {
			// Also allow session-authenticated users
			if !s.isAuthenticated(c) {
				c.JSON(http.StatusUnauthorized, gin.H{"error": "PIN required"})
				return false
			}
		}
	}
	return true
}

// makeDashPinCookie returns "<expiry>.<hmac>": the HMAC binds the dashboard,
// its current PIN and the expiry, so a changed PIN ends old cookies
func (s *Server) makeDashPinCookie(id, pin string, exp int64) string {
	e := strconv.FormatInt(exp, 10)
	return e + "." + hex.EncodeToString(dashPinMAC(s.getDashHMACKey(), id, pin, e))
}

func dashPinMAC(key []byte, id, pin, exp string) []byte {
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(id + "\x00" + pin + "\x00" + exp))
	return mac.Sum(nil)
}

// validateDashPinCookie checks the cookie against the dashboard's current PIN.
func (s *Server) validateDashPinCookie(id, cookieValue string) bool {
	e, sig, ok := strings.Cut(cookieValue, ".")
	if !ok {
		return false
	}
	exp, err := strconv.ParseInt(e, 10, 64)
	if err != nil || time.Now().Unix() > exp {
		return false
	}
	got, err := hex.DecodeString(sig)
	if err != nil {
		return false
	}
	result, err := s.luaExecutor.Execute(fmt.Sprintf(`Dashboard.API.getPin("%s")`, luaEscape(id)))
	if err != nil {
		return false
	}
	var r struct {
		Pin string `json:"pin"`
	}
	if json.Unmarshal([]byte(result), &r) != nil || r.Pin == "" {
		return false
	}
	return hmac.Equal(got, dashPinMAC(s.getDashHMACKey(), id, r.Pin, e))
}

var (
	dashKey     []byte
	dashKeyOnce sync.Once
)

// getDashHMACKey returns the key for dashboard PIN cookies: random, kept in
// dashboard-pin.key (not the CLU key, which every OM project file holds)
func (s *Server) getDashHMACKey() []byte {
	dashKeyOnce.Do(func() {
		dashKey = make([]byte, 32)
		if _, err := rand.Read(dashKey); err != nil {
			panic(err)
		}
		if s.serverDir == "" {
			return
		}
		file := filepath.Join(s.serverDir, "dashboard-pin.key")
		if b, err := os.ReadFile(file); err == nil && len(b) == 32 {
			dashKey = b
			return
		}
		if err := os.WriteFile(file, dashKey, 0o600); err != nil {
			log.Printf("[DASHBOARD] PIN key not saved (%v): PIN cookies end on restart", err)
		}
	})
	return dashKey
}

// luaEscape escapes a string for safe embedding in Lua string literals
func luaEscape(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, c := range s {
		switch c {
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		default:
			b.WriteRune(c)
		}
	}
	return b.String()
}
