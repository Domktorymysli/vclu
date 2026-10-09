package web

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"net/http/pprof"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/Domktorymysli/vclu/pkg/accesscontrol"
	"github.com/Domktorymysli/vclu/pkg/broker"
	"github.com/Domktorymysli/vclu/pkg/camera"
	"github.com/Domktorymysli/vclu/pkg/config"
	"github.com/Domktorymysli/vclu/pkg/history"
	"github.com/Domktorymysli/vclu/pkg/homekit"
	"github.com/Domktorymysli/vclu/pkg/i18n"
	"github.com/Domktorymysli/vclu/pkg/logstream"
	"github.com/Domktorymysli/vclu/pkg/lua"
	mcppkg "github.com/Domktorymysli/vclu/pkg/mcp"
	"github.com/Domktorymysli/vclu/pkg/metrics"
	"github.com/Domktorymysli/vclu/pkg/server"
	"github.com/Domktorymysli/vclu/pkg/stats"
	"github.com/gin-gonic/gin"
)

const (
	sessionCookieName = "vclu_session"
	sessionMaxAge     = 43200 // 12 hours
)

// LuaExecutor interface for executing Lua scripts
type LuaExecutor interface {
	Execute(script string) (string, error)
}

// LuaReloader interface for reloading Lua runtime
type LuaReloader interface {
	Reload() error
}

// MQTTStatsProvider interface for getting MQTT statistics
// Optional - not all LuaExecutor implementations may have MQTT support
type MQTTStatsProvider interface {
	GetMQTTStats() lua.MQTTStats
}

// CLUCommander interface for sending commands to CLUs
type CLUCommander interface {
	SendCommand(targetIP string, script string) (string, error)
	GetRegisteredCLUs() ([]server.CLUInfo, error)
}

// BrokerController interface for managing MQTT broker
type BrokerController interface {
	GetMQTTBroker() *broker.Broker
}

// PluginController interface for managing plugins
type PluginController interface {
	LoadPlugin(pluginID string, config map[string]interface{}) error
	UnloadPlugin(pluginID string) error
	IsLoaded(pluginID string) bool
}

// sessionStore manages user sessions and CSRF tokens
type sessionStore struct {
	sessions   map[string]time.Time // session token -> expiry time
	csrfTokens map[string]string    // session token -> CSRF token
	mu         sync.RWMutex
}

func newSessionStore() *sessionStore {
	return &sessionStore{
		sessions:   make(map[string]time.Time),
		csrfTokens: make(map[string]string),
	}
}

func (s *sessionStore) create() string {
	token := make([]byte, 32)
	if _, err := rand.Read(token); err != nil {
		panic("crypto/rand failed: " + err.Error())
	}
	sessionToken := hex.EncodeToString(token)

	csrfBytes := make([]byte, 32)
	if _, err := rand.Read(csrfBytes); err != nil {
		panic("crypto/rand failed: " + err.Error())
	}
	csrfToken := hex.EncodeToString(csrfBytes)

	s.mu.Lock()
	defer s.mu.Unlock()
	s.sessions[sessionToken] = time.Now().Add(time.Duration(sessionMaxAge) * time.Second)
	s.csrfTokens[sessionToken] = csrfToken
	return sessionToken
}

func (s *sessionStore) validate(token string) bool {
	s.mu.RLock()
	expiry, exists := s.sessions[token]
	if !exists {
		s.mu.RUnlock()
		return false
	}
	if time.Now().After(expiry) {
		s.mu.RUnlock()
		// Need write lock to delete - upgrade from RLock
		s.mu.Lock()
		delete(s.sessions, token)
		delete(s.csrfTokens, token)
		s.mu.Unlock()
		return false
	}
	s.mu.RUnlock()
	return true
}

func (s *sessionStore) getCSRF(sessionToken string) string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.csrfTokens[sessionToken]
}

func (s *sessionStore) validateCSRF(sessionToken, csrfToken string) bool {
	s.mu.RLock()
	stored, exists := s.csrfTokens[sessionToken]
	s.mu.RUnlock()
	if !exists || stored == "" || csrfToken == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(stored), []byte(csrfToken)) == 1
}

func (s *sessionStore) delete(token string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.sessions, token)
	delete(s.csrfTokens, token)
}

// isHTTPS returns true if the request arrived over HTTPS (directly or via reverse proxy)
func isHTTPS(c *gin.Context) bool {
	return c.GetHeader("X-Forwarded-Proto") == "https" || c.Request.TLS != nil
}

// Server represents the web interface server
type Server struct {
	port             int
	router           *gin.Engine
	httpServer       *http.Server
	deviceConfig     *config.CLUDeviceConfig
	keys             *config.CLUKeys
	serverDir        string
	webDir           string
	luaExecutor      LuaExecutor
	luaReloader      LuaReloader
	cluCommander     CLUCommander
	brokerController BrokerController
	pluginController PluginController
	accessManager    *accesscontrol.Manager
	homekitServer    *homekit.Server
	mcpServer        *mcppkg.MCPServer
	logStream        *logstream.LogStream
	metrics          *stats.AppMetrics
	metricsCollector *metrics.Collector
	historyStore     *history.Store
	cameraManager    *camera.Manager
	mqttManager      *lua.MQTTManager
	satelManager     *lua.SatelManager
	isConfigured     bool
	supportsModules  bool
	sessions         *sessionStore
	backupState      backupSchedulerState
}

// NewServer creates a new web server
func NewServer(port int, webDir string, serverDir string, deviceConfig *config.CLUDeviceConfig, keys *config.CLUKeys, luaExecutor LuaExecutor) *Server {
	// Set Gin to release mode for production
	gin.SetMode(gin.ReleaseMode)

	router := gin.Default()

	// Trust all proxies — vCLU is designed for local network behind reverse proxy (nginx/caddy)
	// This enables correct handling of X-Forwarded-For, X-Forwarded-Proto, X-Real-IP
	router.SetTrustedProxies(nil)
	router.ForwardedByClientIP = true

	// Enable proper handling of URL-encoded path parameters (e.g., @vclu/weather -> %40vclu%2Fweather)
	router.UseRawPath = true
	router.UnescapePathValues = true

	// Load translations
	localesPath := filepath.Join(webDir, "locales")
	if err := i18n.Load(localesPath); err != nil {
		log.Printf("Warning: Failed to load translations: %v", err)
	}

	// Setup templates with translation function
	templatesPath := filepath.Join(webDir, "templates", "*.html")
	tmpl := template.New("").Funcs(template.FuncMap{
		"t": func(lang, key string) string {
			return i18n.Get(lang, key)
		},
		"json": func(v interface{}) template.JS {
			b, _ := json.Marshal(v)
			return template.JS(b)
		},
	})
	tmpl = template.Must(tmpl.ParseGlob(templatesPath))
	router.SetHTMLTemplate(tmpl)

	// Setup static files
	staticPath := filepath.Join(webDir, "static")
	router.Static("/static", staticPath)

	// Initialize unified access control manager
	accessMgr := accesscontrol.NewManager(serverDir)
	if err := accessMgr.Load(); err != nil {
		// Not recoverable at runtime: every integration now denies until the file is fixed.
		log.Printf("ERROR: access control config unusable - DENYING ALL integration access until fixed: %v", err)
	}

	s := &Server{
		port:            port,
		router:          router,
		deviceConfig:    deviceConfig,
		keys:            keys,
		serverDir:       serverDir,
		webDir:          webDir,
		luaExecutor:     luaExecutor,
		accessManager:   accessMgr,
		isConfigured:    keys != nil && deviceConfig != nil,
		supportsModules: checkSupportsModules(serverDir),
		sessions:        newSessionStore(),
	}

	// Add metrics middleware (must be first to capture all requests)
	router.Use(s.metricsMiddleware())

	// Add language middleware
	router.Use(s.languageMiddleware())

	// Add auth middleware
	router.Use(s.authMiddleware())

	// Add CSRF middleware (after auth, so auth_method is set)
	router.Use(s.csrfMiddleware())

	// Setup routes
	s.setupRoutes()

	// Start auto-backup scheduler if enabled
	s.startBackupScheduler()

	// Note: access control sync to Lua happens via SetOnLuaReset callback,
	// not here, to avoid blocking on Lua mutex during startup.

	return s
}

// syncAccessControlToLua syncs profile-based access control to Lua.
func (s *Server) syncAccessControlToLua() {
	if s.luaExecutor == nil || s.accessManager == nil {
		return
	}

	s.syncProfilesToLua()

	// Re-evaluate HA discovery so access changes (e.g. Hidden) apply live: hidden
	// objects get their discovery cleared, visible ones (re)published. No-op before
	// the HA publisher is set up (startup sync runs before HAIntegration.setup).
	if _, err := s.luaExecutor.Execute("if HAStatePublisher and HAStatePublisher.refresh then HAStatePublisher.refresh() end"); err != nil {
		log.Printf("[Access] HA discovery refresh failed: %v", err)
	}

	log.Println("Synced access control profiles to Lua")
}

// applyAccessChange applies an access change made in the panel: Lua, HA and
// the HomeKit bridge (a hidden object leaves it, a visible one joins).
func (s *Server) applyAccessChange() {
	s.syncAccessControlToLua()
	if s.homekitServer != nil {
		go func() {
			if err := s.homekitServer.Sync(); err != nil {
				log.Printf("[HomeKit] bridge not updated after access change: %v", err)
			}
		}()
	}
}

// syncProfilesToLua sends profile and assignment data to the Lua AccessControl module.
func (s *Server) syncProfilesToLua() {
	if s.luaExecutor == nil || s.accessManager == nil {
		return
	}

	profiles := s.accessManager.GetProfilesForSync()
	assignments := s.accessManager.GetAssignmentsForSync()

	// Build profiles Lua table
	// Format: { ["name"] = { ["default"] = "full", objects = { ["CLU.DOU1"] = "hidden" } }, ... }
	profilesLua := "{"
	firstP := true
	for name, pm := range profiles {
		if !firstP {
			profilesLua += ", "
		}
		firstP = false
		profilesLua += fmt.Sprintf(`["%s"] = {`, name)
		profilesLua += fmt.Sprintf(`["default"] = "%s"`, pm["default"])

		if objects, ok := pm["objects"].(map[string]string); ok && len(objects) > 0 {
			profilesLua += ", objects = {"
			firstO := true
			for path, level := range objects {
				if !firstO {
					profilesLua += ", "
				}
				firstO = false
				profilesLua += fmt.Sprintf(`["%s"] = "%s"`, path, level)
			}
			profilesLua += "}"
		}
		profilesLua += "}"
	}
	profilesLua += "}"

	// Build assignments Lua table
	// Format: { mqtt = {"profile1", "profile2"}, ... }
	assignmentsLua := "{"
	firstA := true
	for intg, names := range assignments {
		if !firstA {
			assignmentsLua += ", "
		}
		firstA = false
		assignmentsLua += fmt.Sprintf(`%s = {`, intg)
		for i, name := range names {
			if i > 0 {
				assignmentsLua += ", "
			}
			assignmentsLua += fmt.Sprintf(`"%s"`, name)
		}
		assignmentsLua += "}"
	}
	assignmentsLua += "}"

	script := fmt.Sprintf(`LuaAPI.syncProfiles(%s, %s)`, profilesLua, assignmentsLua)
	if _, err := s.luaExecutor.Execute(script); err != nil {
		log.Printf("Warning: Failed to sync profiles to Lua: %v", err)
	}
}

// GetAccessSyncFunc returns a function that syncs access control to Lua
// This can be registered as a callback in the main server to be called after Lua reset
func (s *Server) GetAccessSyncFunc() func() {
	return s.syncAccessControlToLua
}

// GetAccessManager returns the access control manager
func (s *Server) GetAccessManager() *accesscontrol.Manager {
	return s.accessManager
}

// authMiddleware checks if user is authenticated when auth is enabled
func (s *Server) authMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		path := c.Request.URL.Path

		// Always allow these paths without auth
		publicPaths := []string{"/login", "/static/", "/wizard", "/setup", "/api/wizard/", "/api/health", "/mcp", "/d/", "/api/dashboard/"}
		for _, p := range publicPaths {
			if strings.HasPrefix(path, p) {
				c.Next()
				return
			}
		}

		// Check if auth is enabled
		if !config.IsAuthEnabled(s.serverDir) {
			// No auth configured, allow all
			c.Next()
			return
		}

		// Check API key header first
		if apiKey := c.GetHeader("X-API-Key"); apiKey != "" {
			if s.validateAPIKey(apiKey) {
				c.Set("auth_method", "apikey")
				c.Next()
				return
			}
			// Invalid API key — for API paths return 401 JSON
			if strings.HasPrefix(path, "/api/") {
				c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "Invalid API key"})
			} else {
				c.Redirect(http.StatusFound, "/login")
				c.Abort()
			}
			return
		}

		// Check session cookie
		sessionToken, err := c.Cookie(sessionCookieName)
		if err != nil || !s.sessions.validate(sessionToken) {
			// For API paths, return 401 JSON instead of redirect
			if strings.HasPrefix(path, "/api/") {
				c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "Authentication required"})
			} else {
				c.Redirect(http.StatusFound, "/login")
				c.Abort()
			}
			return
		}

		c.Set("auth_method", "session")
		c.Next()
	}
}

// csrfMiddleware validates CSRF tokens on state-changing requests
func (s *Server) csrfMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		// Skip safe methods
		method := c.Request.Method
		if method == "GET" || method == "HEAD" || method == "OPTIONS" {
			c.Next()
			return
		}

		path := c.Request.URL.Path

		// Skip public paths (wizard, login, health, mcp)
		publicPaths := []string{"/api/wizard/", "/login", "/api/health", "/mcp", "/api/dashboard/"}
		for _, p := range publicPaths {
			if strings.HasPrefix(path, p) {
				c.Next()
				return
			}
		}

		// Skip if auth not enabled
		if !config.IsAuthEnabled(s.serverDir) {
			c.Next()
			return
		}

		// Skip if authenticated via API key
		if c.GetString("auth_method") == "apikey" {
			c.Next()
			return
		}

		sessionToken, _ := c.Cookie(sessionCookieName)
		// Check X-CSRF-Token header first, then fall back to form field
		csrfToken := c.GetHeader("X-CSRF-Token")
		if csrfToken == "" {
			csrfToken = c.PostForm("_csrf")
		}

		if !s.sessions.validateCSRF(sessionToken, csrfToken) {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "Invalid CSRF token"})
			return
		}
		c.Next()
	}
}

// getCSRFToken returns the CSRF token for the current session
func (s *Server) getCSRFToken(c *gin.Context) string {
	sessionToken, err := c.Cookie(sessionCookieName)
	if err != nil {
		return ""
	}
	return s.sessions.getCSRF(sessionToken)
}

// validateAPIKey checks if the given API key is valid against stored hash
func (s *Server) validateAPIKey(key string) bool {
	masterConfig, err := config.ReadMasterConfig(s.serverDir)
	if err != nil || masterConfig == nil || masterConfig.Auth == nil {
		return false
	}
	if masterConfig.Auth.APIKeyHash == "" {
		return false
	}
	return config.ValidateAPIKey(key, masterConfig.Auth.APIKeyHash)
}

// isAuthenticated checks if current request has valid session
func (s *Server) isAuthenticated(c *gin.Context) bool {
	if !config.IsAuthEnabled(s.serverDir) {
		return true // No auth required
	}
	sessionToken, err := c.Cookie(sessionCookieName)
	if err != nil {
		return false
	}
	return s.sessions.validate(sessionToken)
}

// SetCLUCommander sets the CLU commander for sending commands to CLUs
func (s *Server) SetCLUCommander(commander CLUCommander) {
	s.cluCommander = commander
}

// SetBrokerController sets the MQTT broker controller
func (s *Server) SetBrokerController(controller BrokerController) {
	s.brokerController = controller
}

// SetPluginController sets the plugin controller for loading/unloading plugins
func (s *Server) SetPluginController(controller PluginController) {
	s.pluginController = controller
}

// SetLuaReloader sets the Lua reloader for runtime reloading
func (s *Server) SetLuaReloader(reloader LuaReloader) {
	s.luaReloader = reloader
}

// SetHomeKitServer sets the HomeKit server
func (s *Server) SetHomeKitServer(server *homekit.Server) {
	s.homekitServer = server
}

// SetMCPServer sets the MCP server and registers the /mcp endpoint.
// MCP endpoint is registered WITHOUT gin's auth/CSRF middleware — clean pass-through.
func (s *Server) SetMCPServer(mcpSrv *mcppkg.MCPServer) {
	s.mcpServer = mcpSrv
	// Register /mcp endpoint outside of middleware stack.
	// gin.WrapH passes directly to mcp-go's HTTP handler.
	// Auth is handled inside MCPServer.Handler() (Bearer token).
	s.router.Any("/mcp", gin.WrapH(mcpSrv.Handler()))

	// MCP clients (Claude Code) probe OAuth discovery before connecting.
	// Return 404 for auth-server (we don't support OAuth), but return proper
	// resource metadata per RFC 9470 so clients know to use Bearer tokens.
	oauthNotSupported := func(c *gin.Context) {
		c.JSON(http.StatusNotFound, gin.H{"error": "OAuth not supported, use Bearer token auth"})
	}
	// RFC 9470 Protected Resource Metadata — tells clients we accept Bearer tokens
	oauthResourceMetadata := func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{
			"resource":                 c.Request.Host,
			"bearer_methods_supported": []string{"header"},
		})
	}
	// RFC 8414: /.well-known/oauth-authorization-server[/path]
	// RFC 9470: /.well-known/oauth-protected-resource[/path]
	// Clients probe root-relative, path-relative, or with suffix.
	s.router.GET("/.well-known/oauth-authorization-server", oauthNotSupported)
	s.router.GET("/.well-known/oauth-authorization-server/*any", oauthNotSupported)
	s.router.GET("/.well-known/oauth-protected-resource", oauthResourceMetadata)
	s.router.GET("/.well-known/oauth-protected-resource/*any", oauthResourceMetadata)
	s.router.GET("/mcp/.well-known/oauth-authorization-server", oauthNotSupported)
	s.router.GET("/mcp/.well-known/oauth-protected-resource", oauthResourceMetadata)
}

// SetLogStream sets the log stream for debug console
func (s *Server) SetLogStream(ls *logstream.LogStream) {
	s.logStream = ls
}

// SetMetrics sets the metrics collector for statistics
func (s *Server) SetMetrics(m *stats.AppMetrics) {
	s.metrics = m
}

// SetMetricsCollector sets the dashboard metrics collector
func (s *Server) SetMetricsCollector(mc *metrics.Collector) {
	s.metricsCollector = mc
}

// SetCameraManager sets the camera snapshot manager
func (s *Server) SetCameraManager(cm *camera.Manager) {
	s.cameraManager = cm
}

// SetMQTTManager sets the MQTT manager for client reconnect and test connection
func (s *Server) SetMQTTManager(mm *lua.MQTTManager) {
	s.mqttManager = mm
}

// metricsMiddleware records HTTP request metrics
func (s *Server) metricsMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		// Process request first
		c.Next()

		// Record metrics (metrics may be set after server creation)
		if s.metrics != nil {
			path := c.Request.URL.Path
			isAPI := strings.HasPrefix(path, "/api/")
			statusCode := c.Writer.Status()
			s.metrics.RecordHTTPRequest(isAPI, statusCode)
		}
	}
}

// languageMiddleware detects and sets the language for each request
func (s *Server) languageMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		lang := i18n.DefaultLang

		// 1. Check query parameter
		if q := c.Query("lang"); q != "" {
			lang = q
			// Set cookie for future requests
			c.SetCookie(i18n.CookieName, lang, 86400*30, "/", "", isHTTPS(c), false)
		} else if cookie, err := c.Cookie(i18n.CookieName); err == nil && cookie != "" {
			// 2. Check cookie
			lang = cookie
		} else {
			// 3. Check Accept-Language header
			acceptLang := c.GetHeader("Accept-Language")
			if strings.Contains(acceptLang, "pl") {
				lang = "pl"
			} else if strings.Contains(acceptLang, "en") {
				lang = "en"
			}
		}

		// Store language in context
		c.Set("lang", lang)
		c.Next()
	}
}

// getLang returns the current language from context
func (s *Server) getLang(c *gin.Context) string {
	if lang, exists := c.Get("lang"); exists {
		return lang.(string)
	}
	return i18n.DefaultLang
}

// getTranslations returns translations for templates
func (s *Server) getTranslations(c *gin.Context) gin.H {
	lang := s.getLang(c)
	return gin.H{
		"lang":  lang,
		"t":     i18n.GetAll(lang),
		"langs": i18n.AvailableLanguages(),
	}
}

// getMQTTStats returns MQTT statistics if available
func (s *Server) getMQTTStats() *lua.MQTTStats {
	if s.luaExecutor == nil {
		return nil
	}

	// Try to get MQTT stats via type assertion
	if provider, ok := s.luaExecutor.(MQTTStatsProvider); ok {
		stats := provider.GetMQTTStats()
		return &stats
	}
	return nil
}

// setupRoutes configures all web routes
func (s *Server) setupRoutes() {
	// Main page - redirects based on configuration state
	s.router.GET("/", s.handleIndex)

	// Login/logout pages
	s.router.GET("/login", s.handleLogin)
	s.router.POST("/login", s.handleLoginPost)
	s.router.GET("/logout", s.handleLogout)

	// Setup redirects to wizard
	s.router.GET("/setup", func(c *gin.Context) {
		c.Redirect(http.StatusFound, "/wizard")
	})

	// Prometheus metrics endpoint (public, no auth required for scraping)
	s.router.GET("/metrics", s.handleMetrics)

	// Legacy /status redirect → /stats
	s.router.GET("/status", func(c *gin.Context) {
		c.Redirect(http.StatusFound, "/stats")
	})

	// Settings page
	s.router.GET("/settings", s.handleSettings)
	s.router.POST("/settings", s.handleSettingsPost)
	s.router.POST("/settings/security", s.handleSecurityPost)

	// Stats page
	s.router.GET("/stats", s.handleStatsPage)

	// Tools page
	s.router.GET("/tools", s.handleTools)

	// Backup page
	s.router.GET("/backup", s.handleBackup)
	s.router.GET("/history", s.handleHistory)

	// Grenton page
	s.router.GET("/grenton", s.handleGrenton)

	// MQTT page
	s.router.GET("/mqtt", s.handleMQTT)

	// Satel page
	s.router.GET("/satel", s.handleSatel)

	// API documentation (Redoc UI; spec served inside the /api group below)
	s.router.GET("/api-docs", s.handleAPIDocs)

	// HomeKit page
	s.router.GET("/homekit", s.handleHomeKit)
	s.router.POST("/homekit", s.handleHomeKitPost)

	// Access Control pages
	s.router.GET("/access", s.handleAccess)
	s.router.GET("/access/profiles", s.handleAccessProfiles)
	s.router.GET("/access/profiles/edit", s.handleAccessProfileEdit)

	// MCP dashboard page
	s.router.GET("/mcp-dashboard", s.handleMCPDashboard)

	// Plugins page
	s.router.GET("/plugins", s.handlePlugins)

	// Editor page
	s.router.GET("/editor", s.handleEditor)

	// Dashboard pages
	s.router.GET("/d/:id", s.handleDashboardPage)
	s.router.GET("/d/:id/custom.css", s.handleDashboardCustomCSS)
	s.router.GET("/dashboards", s.handleDashboards)

	// Camera snapshot proxy (public — used by dashboard image widgets)
	s.router.GET("/api/camera/:id/snapshot", s.handleAPICameraSnapshot)

	// Debug page
	s.router.GET("/debug", s.handleDebug)

	// pprof endpoints (behind auth middleware like other pages)
	pprofGroup := s.router.Group("/debug/pprof")
	{
		pprofGroup.GET("/", gin.WrapF(pprof.Index))
		pprofGroup.GET("/cmdline", gin.WrapF(pprof.Cmdline))
		pprofGroup.GET("/profile", gin.WrapF(pprof.Profile))
		pprofGroup.GET("/symbol", gin.WrapF(pprof.Symbol))
		pprofGroup.GET("/trace", gin.WrapF(pprof.Trace))
		pprofGroup.GET("/allocs", gin.WrapH(pprof.Handler("allocs")))
		pprofGroup.GET("/block", gin.WrapH(pprof.Handler("block")))
		pprofGroup.GET("/goroutine", gin.WrapH(pprof.Handler("goroutine")))
		pprofGroup.GET("/heap", gin.WrapH(pprof.Handler("heap")))
		pprofGroup.GET("/mutex", gin.WrapH(pprof.Handler("mutex")))
		pprofGroup.GET("/threadcreate", gin.WrapH(pprof.Handler("threadcreate")))
	}

	// Wizard pages
	s.router.GET("/wizard", s.handleWizard)

	// API endpoints
	api := s.router.Group("/api")
	{
		api.GET("/device", s.handleAPIDevice)
		api.GET("/keys", s.handleAPIKeys)
		api.GET("/devices", s.handleAPIDevices)
		api.POST("/settings/save", s.handleAPISettingsSave)
		api.POST("/settings/generate-api-key", s.handleAPIGenerateAPIKey)
		api.DELETE("/settings/api-key", s.handleAPIRevokeAPIKey)
		api.POST("/reset", s.handleAPIReset)
		api.GET("/health", s.handleAPIHealth)
		api.POST("/restart", s.handleAPIRestart)

		// Editor endpoints
		api.GET("/editor/files", s.handleAPIEditorFiles) // Legacy endpoint
		api.GET("/editor/tree", s.handleAPIEditorTree)
		api.GET("/editor/file/*path", s.handleAPIEditorFile)
		api.POST("/editor/file", s.handleAPIEditorCreateFile)
		api.PUT("/editor/file/*path", s.handleAPIEditorSaveFile)
		api.DELETE("/editor/file/*path", s.handleAPIEditorDeleteFile)
		api.POST("/editor/folder", s.handleAPIEditorCreateFolder)
		api.DELETE("/editor/folder/*path", s.handleAPIEditorDeleteFolder)
		api.POST("/editor/validate", s.handleAPIEditorValidate)
		api.GET("/editor/search", s.handleAPIEditorSearch)
		api.POST("/editor/rename", s.handleAPIEditorRename)
		api.POST("/editor/run", s.handleAPIEditorRun)

		// Lua runtime endpoints
		api.POST("/lua/reload", s.handleAPILuaReload)

		// MQTT endpoints
		api.GET("/mqtt/broker/stats", s.handleAPIMQTTBrokerStats)
		api.GET("/mqtt/broker/clients", s.handleAPIMQTTBrokerClients)
		api.POST("/mqtt/broker/save", s.handleAPIMQTTBrokerSave)
		api.GET("/mqtt/clients/stats", s.handleAPIMQTTClientsStats)
		api.POST("/mqtt/test/publish", s.handleAPIMQTTTestPublish)
		api.GET("/mqtt/client/config", s.handleAPIMQTTClientConfig)
		api.POST("/mqtt/client/save", s.handleAPIMQTTClientSave)
		api.POST("/mqtt/client/test", s.handleAPIMQTTClientTest)

		// Home Assistant Discovery
		api.POST("/ha/discovery/publish", s.handleAPIHADiscoveryPublish)
		api.POST("/ha/commands/enable", s.handleAPIHACommandsEnable)

		// Grenton endpoints
		api.GET("/openapi.json", s.handleOpenAPISpec)

		api.GET("/satel/status", s.handleAPISatelStatus)
		api.POST("/satel/config", s.handleAPISatelSave)
		api.POST("/satel/test", s.handleAPISatelTest)
		api.POST("/satel/scan", s.handleAPISatelScan)
		api.GET("/satel/scan", s.handleAPISatelLastScan)
		api.GET("/satel/state", s.handleAPISatelState)

		api.GET("/grenton/status", s.handleAPIGrentonStatus)
		api.POST("/grenton/save", s.handleAPIGrentonSave)

		// Grenton Sync endpoints
		api.GET("/grenton/sync", s.handleAPIGrentonSyncGet)
		api.POST("/grenton/sync", s.handleAPIGrentonSyncSave)
		api.POST("/grenton/sync/now", s.handleAPIGrentonSyncNow)
		api.GET("/grenton/sync/status", s.handleAPIGrentonSyncStatus)

		// HomeKit endpoints
		api.GET("/homekit/status", s.handleAPIHomeKitStatus)
		api.POST("/homekit/save", s.handleAPIHomeKitSave)
		api.POST("/homekit/restart", s.handleAPIHomeKitRestart)

		// MCP endpoints
		api.GET("/mcp/status", s.handleAPIMCPStatus)
		api.POST("/mcp/save", s.handleAPIMCPSave)
		api.GET("/mcp/stats", s.handleAPIMCPStats)
		api.GET("/mcp/activity", s.handleAPIMCPActivity)

		// MCP Bot endpoints
		api.GET("/mcp/bots", s.handleAPIMCPBots)
		api.POST("/mcp/bots", s.handleAPIMCPBotCreate)
		api.PUT("/mcp/bots/:id", s.handleAPIMCPBotUpdate)
		api.DELETE("/mcp/bots/:id", s.handleAPIMCPBotDelete)
		api.POST("/mcp/bots/:id/regenerate", s.handleAPIMCPBotRegenerate)

		// MCP Workspace endpoints (Layer 2 dashboard)
		api.GET("/mcp/workspace/drafts", s.handleAPIMCPDrafts)
		api.GET("/mcp/workspace/drafts/:path", s.handleAPIMCPDraftGet)
		api.DELETE("/mcp/workspace/drafts/:path", s.handleAPIMCPDraftDelete)
		api.GET("/mcp/workspace/diff/:path", s.handleAPIMCPDraftDiff)
		api.POST("/mcp/workspace/promote", s.handleAPIMCPDraftPromoteWeb)
		api.GET("/mcp/workspace/backups", s.handleAPIMCPBackups)
		api.DELETE("/mcp/workspace/backups", s.handleAPIMCPBackupsClear)
		api.POST("/mcp/workspace/rollback", s.handleAPIMCPRollback)
		api.GET("/mcp/workspace/audit", s.handleAPIMCPAuditLog)
		api.GET("/mcp/workspace/history", s.handleAPIMCPHistory)
		api.GET("/mcp/workspace/backup/:name", s.handleAPIMCPBackupGet)
		api.POST("/mcp/workspace/validate", s.handleAPIMCPDraftValidateWeb)
		api.POST("/mcp/workspace/test", s.handleAPIMCPDraftTestWeb)

		// MCP Incidents + Agent Context (Layer 2 Phase 5)
		api.GET("/mcp/incidents", s.handleAPIMCPIncidents)
		api.GET("/mcp/incidents/:id", s.handleAPIMCPIncidentGet)
		api.GET("/mcp/agent-context", s.handleAPIMCPAgentContext)
		api.DELETE("/mcp/agent-context", s.handleAPIMCPAgentContextClear)

		// Access Control endpoints
		api.GET("/access", s.handleAPIAccessList)
		api.POST("/access", s.handleAPIAccessSet)
		api.POST("/access/bot", s.handleAPIAccessBotSet)
		api.GET("/access/integrations", s.handleAPIAccessIntegrations)

		// Access Control Profile endpoints
		api.GET("/access/profiles", s.handleAPIAccessProfiles)
		api.POST("/access/profiles", s.handleAPIAccessProfileSave)
		api.DELETE("/access/profiles/:name", s.handleAPIAccessProfileDelete)
		api.POST("/access/reset", s.handleAPIAccessReset)
		api.GET("/access/assignments", s.handleAPIAccessAssignments)
		api.POST("/access/assignments", s.handleAPIAccessAssignmentsSave)

		// Dashboard endpoints
		api.GET("/dashboards", s.handleAPIDashboards)
		api.GET("/dashboard/:id", s.handleAPIDashboardDef)
		api.GET("/dashboard/:id/values", s.handleAPIDashboardValues)
		api.POST("/dashboard/:id/action", s.handleAPIDashboardAction)
		api.POST("/dashboard/:id/auth", s.handleAPIDashboardAuth)

		// CLU Function Tester endpoints
		api.GET("/clus", s.handleAPIGetCLUs)
		api.POST("/clu/execute", s.handleAPICLUExecute)

		// Backup endpoints
		api.POST("/backup/create", s.handleAPIBackupCreate)
		api.GET("/backup/list", s.handleAPIBackupList)
		api.GET("/backup/download/:filename", s.handleAPIBackupDownload)
		api.DELETE("/backup/:filename", s.handleAPIBackupDelete)
		api.POST("/backup/restore", s.handleAPIBackupRestore)
		api.GET("/backup/settings", s.handleAPIBackupSettings)

		// State change history (Tools -> History and audit)
		api.GET("/history", s.handleAPIHistory)
		api.GET("/history/status", s.handleAPIHistoryStatus)
		api.GET("/history/settings", s.handleAPIHistorySettings)
		api.PUT("/history/settings", s.handleAPIHistorySettingsSave)
		api.POST("/history/flush", s.handleAPIHistoryFlush)
		api.POST("/history/clear-memory", s.handleAPIHistoryClearMemory)
		api.POST("/history/clear-disk", s.handleAPIHistoryClearDisk)
		api.POST("/backup/settings", s.handleAPIBackupSettingsSave)

		// OM Files endpoints
		api.POST("/om/upload", s.handleAPIOMUpload)
		api.POST("/om/regenerate", s.handleAPIOMRegenerate)
		api.GET("/om/list", s.handleAPIOMList)
		api.GET("/om/:serial", s.handleAPIOMDetails)
		api.DELETE("/om/:serial", s.handleAPIOMDelete)

		// Registry endpoints
		api.GET("/registry/list", s.handleAPIRegistryList)
		api.POST("/registry/subscribe", s.handleAPIRegistrySubscribe)

		// Plugin endpoints
		api.GET("/plugins/repositories", s.handlePluginsAPIRepositories)
		api.POST("/plugins/repositories", s.handlePluginsAPIAddRepository)
		api.DELETE("/plugins/repositories/:name", s.handlePluginsAPIRemoveRepository)
		api.GET("/plugins/available", s.handlePluginsAPIAvailable)
		api.GET("/plugins/installed", s.handlePluginsAPIInstalled)
		api.POST("/plugins/install", s.handlePluginsAPIInstall)
		api.DELETE("/plugins/:id", s.handlePluginsAPIUninstall)
		api.POST("/plugins/:id/toggle", s.handlePluginsAPIToggle)
		api.POST("/plugins/:id/config", s.handlePluginsAPIConfig)
		api.GET("/plugins/:id/schema", s.handlePluginsAPIGetSchema)
		api.GET("/plugins/:id/doc", s.handlePluginsAPIGetDoc)

		// Debug log endpoints
		api.GET("/debug/stream", s.handleAPIDebugStream)
		api.GET("/debug/sources", s.handleAPIDebugSources)
		api.GET("/debug/history", s.handleAPIDebugHistory)
		api.GET("/debug/stats", s.handleAPIDebugStats)

		// Stats/metrics endpoints
		api.GET("/stats", s.handleAPIStats)
		api.GET("/stats/timeseries", s.handleAPIStatsTimeSeries)

		// Diagnostic endpoint
		api.POST("/diagnostic", s.handleAPIDiagnosticCreate)

		// Dashboard metrics endpoints (time-series for charts)
		api.GET("/metrics/data", s.handleAPIMetricsData)
		api.GET("/metrics/list", s.handleAPIMetricsList)

		// Wizard endpoints
		api.GET("/wizard/state", s.handleAPIWizardState)
		api.POST("/wizard/device-type", s.handleAPIWizardSetDeviceType)
		api.POST("/wizard/module", s.handleAPIWizardAddModule)
		api.DELETE("/wizard/module/:id", s.handleAPIWizardRemoveModule)
		api.POST("/wizard/keys", s.handleAPIWizardSetKeys)
		api.POST("/wizard/security", s.handleAPIWizardSetSecurity)
		api.POST("/wizard/finish", s.handleAPIWizardFinish)
		api.GET("/wizard/om-preview", s.handleAPIWizardGenerateOMPreview)
	}
}

// Start starts the web server
func (s *Server) Start() error {
	s.httpServer = &http.Server{
		Addr:    fmt.Sprintf(":%d", s.port),
		Handler: s.router,
	}

	go func() {
		log.Printf("Web interface starting on http://0.0.0.0:%d", s.port)
		if err := s.httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Printf("Web server error: %v", err)
		}
	}()

	return nil
}

// Stop stops the web server
func (s *Server) Stop() error {
	s.stopBackupScheduler()

	if s.httpServer == nil {
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	return s.httpServer.Shutdown(ctx)
}

// checkSupportsModules reads master config to determine if the device supports I/O modules
func checkSupportsModules(serverDir string) bool {
	vcluConfig, err := config.ReadMasterConfig(serverDir)
	if err != nil || vcluConfig == nil || vcluConfig.Device == nil {
		return false
	}
	return vcluConfig.Device.SupportsModules()
}

// renderHTML renders an HTML template with automatic CSRF token injection
func (s *Server) renderHTML(c *gin.Context, code int, name string, data gin.H) {
	data["csrfToken"] = s.getCSRFToken(c)
	c.HTML(code, name, data)
}
