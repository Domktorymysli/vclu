package web

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/Domktorymysli/vclu/pkg/accesscontrol"
	"github.com/Domktorymysli/vclu/pkg/config"
	"github.com/gin-gonic/gin"
)

// Clients of the device API (/api/v1/client/): programs that are not the panel, the cloud or
// an integration (a desktop app, a watch, a CLI). Each is made on the panel's API clients page,
// like an MCP bot:
//
//   - its own token (clientTokenPrefix), shown once, kept as a hash in config.json;
//   - its own access profiles: what it may read (readonly) or control (full) of the exposed
//     objects, resolved per object as for the bots; with no profile, nothing;
//   - the home's own roles: the remote dashboards naming one of them, on the LAN listener.
//
// The API is served on the panel's port and on the LAN listener (HTTPS, pinned). Lua runs its
// calls as origin "client", actor "client:<id>".

const clientTokenPrefix = "vclu_client_"

// ctxClient holds the request's *config.APIClient.
const ctxClient = "api_client"

// apiClient is the client a token is for, false when no client has it or it expired. The
// config is read each time, as for the MCP bots: a deleted client stops at once.
func (s *Server) apiClient(token string) (config.APIClient, bool) {
	if !strings.HasPrefix(token, clientTokenPrefix) {
		return config.APIClient{}, false
	}
	mc, err := config.ReadMasterConfig(s.serverDir)
	if err != nil || mc == nil {
		return config.APIClient{}, false
	}
	now := time.Now().Unix()
	for _, cl := range mc.Clients {
		if config.ValidateAPIKey(token, cl.TokenHash) {
			if cl.ExpiresAt != 0 && cl.ExpiresAt <= now {
				return config.APIClient{}, false
			}
			return cl, true
		}
	}
	return config.APIClient{}, false
}

func clientActor(cl config.APIClient) string { return "client:" + cl.ID }

func bearer(c *gin.Context) string {
	h := c.GetHeader("Authorization")
	if len(h) < 8 || !strings.EqualFold(h[:7], "bearer ") {
		return ""
	}
	return strings.TrimSpace(h[7:])
}

// clientAuth lets through only a request with a client's token.
func (s *Server) clientAuth(c *gin.Context) {
	cl, ok := s.apiClient(bearer(c))
	if !ok {
		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "unauthorized", "message": "client token not valid"})
		return
	}
	c.Set(ctxClient, cl)
	c.Set(ctxRemoteActor, clientActor(cl))
	c.Next()
}

// clientRoutes are the device API, on the panel's router and on the LAN listener.
func (s *Server) clientRoutes(r gin.IRouter) {
	g := r.Group("/api/v1/client", s.clientAuth)
	g.GET("/devices", s.handleClientDevices)
	g.GET("/device", s.handleClientDevice)
	g.POST("/set", s.handleClientSet)
	g.POST("/call", s.handleClientCall)
}

// clientAccess is what the request's client may do with the object at path.
func (s *Server) clientAccess(c *gin.Context, path string) accesscontrol.AccessLevel {
	v, ok := c.Get(ctxClient)
	if !ok || s.accessManager == nil {
		return accesscontrol.AccessHidden
	}
	cl := v.(config.APIClient)
	if len(cl.Profiles) == 0 {
		return accesscontrol.AccessHidden
	}
	return s.accessManager.ResolveAccess(cl.Profiles, path)
}

func clientError(c *gin.Context, status int, code, msg string) {
	c.JSON(status, gin.H{"error": code, "message": msg})
}

// handleClientDevices lists the exposed objects the client may read, each with its access
// (readonly or full). Query: type, area, group, tag, as for the MCP bots.
func (s *Server) handleClientDevices(c *gin.Context) {
	if s.luaExecutor == nil {
		clientError(c, http.StatusServiceUnavailable, "unavailable", "Lua engine not ready")
		return
	}
	filter := map[string]string{}
	for _, k := range []string{"type", "area", "group", "tag"} {
		if v := c.Query(k); v != "" {
			filter[k] = v
		}
	}
	fj, _ := json.Marshal(filter)
	out, err := s.luaExecutor.Execute(fmt.Sprintf(`MCP.list_devices("%s")`, luaEscape(string(fj))))
	var list []map[string]any
	if err == nil {
		err = json.Unmarshal([]byte(out), &list)
	}
	if err != nil {
		clientError(c, http.StatusInternalServerError, "error", "devices not listed")
		return
	}
	devices := []map[string]any{}
	for _, d := range list {
		path, _ := d["path"].(string)
		level := s.clientAccess(c, path)
		if level == accesscontrol.AccessHidden {
			continue
		}
		d["access"] = clientLevel(level, d["readonly"] == true)
		delete(d, "readonly")
		devices = append(devices, d)
	}
	c.JSON(http.StatusOK, gin.H{"devices": devices})
}

// clientLevel is the access a client gets: full only when its profiles and the object allow it.
func clientLevel(level accesscontrol.AccessLevel, readonly bool) string {
	if level == accesscontrol.AccessFull && !readonly {
		return "full"
	}
	return "readonly"
}

// handleClientDevice is one object with its state, methods and capabilities (?path=).
func (s *Server) handleClientDevice(c *gin.Context) {
	path := c.Query("path")
	if path == "" || s.clientAccess(c, path) == accesscontrol.AccessHidden || s.luaExecutor == nil {
		clientError(c, http.StatusNotFound, "not_found", "no such device")
		return
	}
	out, err := s.luaExecutor.Execute(fmt.Sprintf(`MCP.get_device("%s")`, luaEscape(path)))
	var d map[string]any
	if err == nil {
		err = json.Unmarshal([]byte(out), &d)
	}
	if err != nil || d["error"] != nil {
		clientError(c, http.StatusNotFound, "not_found", "no such device")
		return
	}
	d["access"] = clientLevel(s.clientAccess(c, path), d["readonly"] == true)
	delete(d, "readonly")
	c.JSON(http.StatusOK, d)
}

// clientControl checks that the client may control path and is not over the rate limit;
// false when it answered.
func (s *Server) clientControl(c *gin.Context, path string) bool {
	switch s.clientAccess(c, path) {
	case accesscontrol.AccessHidden:
		clientError(c, http.StatusNotFound, "not_found", "no such device")
		return false
	case accesscontrol.AccessReadonly:
		clientError(c, http.StatusForbidden, "forbidden", "read only for this client")
		return false
	}
	if s.luaExecutor == nil {
		clientError(c, http.StatusServiceUnavailable, "unavailable", "Lua engine not ready")
		return false
	}
	if !actionLimiter.allow(limitKey(c)) {
		clientError(c, http.StatusTooManyRequests, "rate_limited", "too many commands")
		return false
	}
	return true
}

// clientResult answers with Lua's {ok, error, ...}: 422 with its error when it refused.
func clientResult(c *gin.Context, out string, err error) {
	var r map[string]any
	if err == nil {
		err = json.Unmarshal([]byte(out), &r)
	}
	if err != nil {
		clientError(c, http.StatusInternalServerError, "error", "command failed")
		return
	}
	if r["ok"] != true {
		msg, _ := r["error"].(string)
		clientError(c, http.StatusUnprocessableEntity, "refused", msg)
		return
	}
	c.JSON(http.StatusOK, r)
}

// handleClientSet sets an object's value: {path, value}.
func (s *Server) handleClientSet(c *gin.Context) {
	var req struct {
		Path  string `json:"path"`
		Value any    `json:"value"`
	}
	if c.ShouldBindJSON(&req) != nil || req.Path == "" {
		clientError(c, http.StatusBadRequest, "invalid", "path and value required")
		return
	}
	if !s.clientControl(c, req.Path) {
		return
	}
	out, err := s.executeAs(c, "", fmt.Sprintf(`MCP.set_value("%s", %s)`, luaEscape(req.Path), actionValueLua(req.Value)))
	clientResult(c, out, err)
}

// handleClientCall calls one of an object's methods: {path, method, args?}.
func (s *Server) handleClientCall(c *gin.Context) {
	var req struct {
		Path   string `json:"path"`
		Method string `json:"method"`
		Args   []any  `json:"args"`
	}
	if c.ShouldBindJSON(&req) != nil || req.Path == "" || req.Method == "" || strings.HasPrefix(req.Method, "_") {
		clientError(c, http.StatusBadRequest, "invalid", "path and method required")
		return
	}
	if !s.clientControl(c, req.Path) {
		return
	}
	args := ""
	if len(req.Args) > 0 {
		b, _ := json.Marshal(req.Args)
		args = string(b)
	}
	out, err := s.executeAs(c, "", fmt.Sprintf(`MCP.execute_method("%s", "%s", "%s")`,
		luaEscape(req.Path), luaEscape(req.Method), luaEscape(args)))
	clientResult(c, out, err)
}

// lanClient is the LAN listener's viewer for a client token: its actor and roles. A client
// sees only the remote dashboards naming one of its roles, never those for every member.
func (s *Server) lanClient(token string) (actor string, roles []string, ok bool) {
	cl, ok := s.apiClient(token)
	if !ok {
		return "", nil, false
	}
	return clientActor(cl), cl.Roles, true
}

// clientMayView is mayView for a client: also 404 for a dashboard naming no roles.
func (s *Server) clientMayView(path string, roles []string) int {
	if id := dashboardOfPath(path); id != "" {
		if dr, _ := s.remoteDashboardRoles(id); len(dr) == 0 {
			return http.StatusNotFound
		}
	}
	return s.mayView(path, roles, true)
}

// newClientToken makes a client's token.
func newClientToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return clientTokenPrefix + base64.RawURLEncoding.EncodeToString(b), nil
}

// validClientRoles checks roles as the cloud does: 1 to 32 of a-z, 0-9, - and _, at most 30.
func validClientRoles(roles []string) bool {
	if len(roles) > 30 {
		return false
	}
	for i, r := range roles {
		if len(r) == 0 || len(r) > 32 || strings.Trim(r, "abcdefghijklmnopqrstuvwxyz0123456789-_") != "" ||
			slices.Contains(roles[:i], r) {
			return false
		}
	}
	return true
}
