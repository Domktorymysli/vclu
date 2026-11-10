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

	"github.com/Domktorymysli/vclu-cloud/proto"
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
// The API is the cloud's device contract over HTTP (clientRoutes): one API for the cloud and
// for everyone else, versioned in its path. It is served on the panel's port and on the LAN
// listener (HTTPS, pinned). Lua runs its commands as origin "client", actor "client:<id>".

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

// clientFails counts tokens that are no client's, per address: each one reads config.json, so
// a flood of them is answered 429 until it eases (20 at once, then one a second).
var clientFails = &rateLimiter{buckets: make(map[string]*bucket), rate: 1, burst: 20}

// authClient is the client of the request's token, or the status to answer: 401 for a token
// that is no client's, 429 while its address has sent too many of those.
func (s *Server) authClient(c *gin.Context, token string) (config.APIClient, int) {
	ip := c.ClientIP()
	if clientFails.empty(ip) {
		return config.APIClient{}, http.StatusTooManyRequests
	}
	cl, ok := s.apiClient(token)
	if !ok {
		clientFails.allow(ip)
		return config.APIClient{}, http.StatusUnauthorized
	}
	return cl, 0
}

// clientAuth lets through only a request with a client's token.
func (s *Server) clientAuth(c *gin.Context) {
	cl, st := s.authClient(c, bearer(c))
	if st == http.StatusTooManyRequests {
		c.AbortWithStatusJSON(st, gin.H{"error": proto.ReasonRateLimited, "message": "too many invalid tokens"})
		return
	}
	if st != 0 {
		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "unauthorized", "message": "client token not valid"})
		return
	}
	c.Set(ctxClient, cl)
	c.Set(ctxRemoteActor, clientActor(cl))
	c.Next()
}

// clientRoutes are the device API, on the panel's router and on the LAN listener: the cloud's
// device contract (vclu-cloud docs/protocol.md, section 5, proto.Object, State, Cmd and
// CmdResult) over HTTP, as the client, with its access profiles instead of :remote()'s roles.
func (s *Server) clientRoutes(r gin.IRouter) {
	g := r.Group("/api/v1", s.clientAuth)
	g.GET("/devices", s.handleClientDevices)
	g.POST("/states", s.handleClientStates)
	g.POST("/cmd", s.handleClientCmd)
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

// handleClientDevices lists the exposed devices the client's profiles let it read
// ({"devices": [proto.Object]}): a readonly profile makes the device readonly, a full one leaves
// the device's own access (full, or with :remote()'s PIN or confirmation).
func (s *Server) handleClientDevices(c *gin.Context) {
	if s.luaExecutor == nil {
		clientError(c, http.StatusServiceUnavailable, "unavailable", "Lua engine not ready")
		return
	}
	out, err := s.luaExecutor.Execute("JSON.encode(ExposedObjects:deviceList())")
	var list []proto.Object
	if err == nil {
		err = json.Unmarshal([]byte(out), &list)
	}
	if err != nil {
		clientError(c, http.StatusInternalServerError, proto.ReasonError, "devices not listed")
		return
	}
	devices := []proto.Object{}
	for _, o := range list {
		switch s.clientAccess(c, o.Path) {
		case accesscontrol.AccessFull:
		case accesscontrol.AccessReadonly:
			o.Access = proto.AccessReadonly
		default:
			continue
		}
		devices = append(devices, o)
	}
	c.JSON(http.StatusOK, gin.H{"devices": devices})
}

// handleClientStates answers what devices are doing ({"paths": [...]} → {"states": [proto.State]}):
// those the client may not read, or that cannot be read, are left out.
func (s *Server) handleClientStates(c *gin.Context) {
	var req struct {
		Paths []string `json:"paths"`
	}
	if c.ShouldBindJSON(&req) != nil || len(req.Paths) == 0 || len(req.Paths) > proto.MaxStatePaths {
		clientError(c, http.StatusBadRequest, proto.ReasonInvalid, fmt.Sprintf("1 to %d paths", proto.MaxStatePaths))
		return
	}
	if s.luaExecutor == nil {
		clientError(c, http.StatusServiceUnavailable, "unavailable", "Lua engine not ready")
		return
	}
	paths := []string{}
	for _, p := range req.Paths {
		if l := s.clientAccess(c, p); l == accesscontrol.AccessFull || l == accesscontrol.AccessReadonly {
			paths = append(paths, p)
		}
	}
	pj, _ := json.Marshal(paths)
	out, err := s.luaExecutor.Execute(fmt.Sprintf(`Cloud._clientStates("%s")`, luaEscape(string(pj))))
	states := []proto.State{}
	if err == nil {
		err = json.Unmarshal([]byte(out), &states)
	}
	if err != nil {
		clientError(c, http.StatusInternalServerError, proto.ReasonError, "states not read")
		return
	}
	c.JSON(http.StatusOK, gin.H{"states": states})
}

// clientCmdWait is how long a client's command may wait in the Lua queue before it is not run.
const clientCmdWait = 10 * time.Second

// cmdStatus is the HTTP status of a refused command, by its reason.
var cmdStatus = map[string]int{
	proto.ReasonNotFound:    http.StatusNotFound,
	proto.ReasonForbidden:   http.StatusForbidden,
	proto.ReasonInvalid:     http.StatusUnprocessableEntity,
	proto.ReasonConfirm:     http.StatusConflict,
	proto.ReasonPinRequired: http.StatusConflict,
	proto.ReasonPinWrong:    http.StatusConflict,
	proto.ReasonRateLimited: http.StatusTooManyRequests,
	proto.ReasonExpired:     http.StatusGatewayTimeout,
}

// handleClientCmd runs a command as the client ({path, action, value?, confirmed?, pin?} →
// proto.CmdResult): the same Lua as the cloud's cmd (Cloud._clientCommand), with the PIN, the
// confirmation and the actions each type takes.
func (s *Server) handleClientCmd(c *gin.Context) {
	var req struct {
		Path      string   `json:"path"`
		Action    string   `json:"action"`
		Value     *float64 `json:"value"`
		Confirmed bool     `json:"confirmed"`
		Pin       string   `json:"pin"`
	}
	if c.ShouldBindJSON(&req) != nil || req.Path == "" || req.Action == "" {
		clientError(c, http.StatusBadRequest, proto.ReasonInvalid, "path and action required")
		return
	}
	// the level reaches Lua as one of two constants, whatever a profile file says
	level := ""
	switch s.clientAccess(c, req.Path) {
	case accesscontrol.AccessFull:
		level = "full"
	case accesscontrol.AccessReadonly:
		level = "readonly"
	}
	if level == "" {
		c.JSON(http.StatusNotFound, proto.CmdResult{Reason: proto.ReasonNotFound, Message: "Nie ma takiego urządzenia."})
		return
	}
	if s.luaExecutor == nil {
		clientError(c, http.StatusServiceUnavailable, "unavailable", "Lua engine not ready")
		return
	}
	if !actionLimiter.allow(limitKey(c)) {
		c.JSON(http.StatusTooManyRequests, proto.CmdResult{Reason: proto.ReasonRateLimited, Message: "Za dużo poleceń."})
		return
	}
	// no level is -1, which Lua only reads with set (and refuses there)
	value := -1.0
	if req.Value != nil {
		value = *req.Value
	}
	deadline := time.Now().Add(clientCmdWait).UnixMilli()
	out, err := s.executeAs(c, "", fmt.Sprintf(`Cloud._clientCommand("%s", "%s", "%s", %t, "%s", %d, %g)`,
		luaEscape(req.Path), luaEscape(req.Action), level, req.Confirmed, luaEscape(req.Pin), deadline, value))
	var res proto.CmdResult
	if err == nil {
		err = json.Unmarshal([]byte(out), &res)
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, proto.CmdResult{Reason: proto.ReasonError, Message: "Nie udało się."})
		return
	}
	status := http.StatusOK
	if !res.OK {
		status = http.StatusInternalServerError
		if st, ok := cmdStatus[res.Reason]; ok {
			status = st
		}
	}
	c.JSON(status, res)
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
