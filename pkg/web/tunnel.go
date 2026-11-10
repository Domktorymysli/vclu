package web

import (
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/Domktorymysli/vclu-cloud/proto"
	"github.com/gin-gonic/gin"
)

// What vclu cloud asks of this vCLU comes through the tunnel (vclu-cloud docs/protocol.md,
// sections 7 and 8) and is served by TunnelHandler, never by the LAN router: only there are the
// cloud's actor headers believed. It serves the remote dashboards and nothing of the panel.

const (
	ctxRemoteRole  = "remote_role"
	ctxRemoteActor = "remote_actor"
)

var (
	remoteActorRe = regexp.MustCompile(`^cloud:[0-9]{1,18}$`)
	remoteRoles   = map[string]bool{"owner": true, "admin": true, "member": true}
)

// TunnelHandler serves the requests vclu cloud opens through the tunnel: the dashboards marked
// :remote() in Lua, as the role the cloud names, and the static files their page needs.
func (s *Server) TunnelHandler() http.Handler {
	r := gin.New()
	if s.router != nil {
		r.HTMLRender = s.router.HTMLRender // the dashboard and PIN pages
	}
	r.UseRawPath, r.UnescapePathValues = true, true
	r.Use(gin.Recovery(), s.tunnelViewer, staticCache)
	r.POST(proto.PathLocalToken, s.handleLocalToken)
	s.remoteRoutes(r)
	return r
}

// remoteRoutes are what a person away from the house gets, through the tunnel or with a local
// token on the LAN: the remote dashboards as their role, and the static files their page needs.
func (s *Server) remoteRoutes(r *gin.Engine) {
	r.GET("/d/:id", s.handleDashboardPage)
	r.GET("/d/:id/custom.css", s.handleDashboardCustomCSS)
	r.GET("/d/:id/manifest.webmanifest", s.handleDashboardManifest)
	r.GET("/api/dashboard/:id", s.handleAPIDashboardDef)
	r.GET("/api/dashboard/:id/values", s.handleAPIDashboardValues)
	r.GET("/api/dashboard/:id/ws", s.handleAPIDashboardWS)
	r.GET("/api/dashboard/:id/metrics", s.handleAPIDashboardMetrics)
	r.GET("/api/dashboard/:id/bars", s.handleAPIDashboardBars)
	r.GET("/api/dashboard/:id/calendar", s.handleAPIDashboardCalendar)
	r.POST("/api/dashboard/:id/action", s.handleAPIDashboardAction)
	r.POST("/api/dashboard/:id/auth", s.handleAPIDashboardAuth)
	r.GET("/api/camera/:id/snapshot", s.handleTunnelCameraSnapshot)
	r.Static("/static", filepath.Join(s.webDir, "static"))
}

// tunnelViewer takes the person from the cloud's headers; a request without a valid one is
// not the cloud's and gets nothing. A dashboard's :roles() are checked here too, with the
// person's roles from the members list, as on the LAN: the cloud checked them already, but the
// house does not take its word for it. Without a members list (no LAN access) the cloud's
// check is the only one.
func (s *Server) tunnelViewer(c *gin.Context) {
	actor, role := c.GetHeader(proto.HeaderActor), c.GetHeader(proto.HeaderRole)
	if !remoteActorRe.MatchString(actor) || !remoteRoles[role] {
		c.AbortWithStatus(http.StatusNotFound)
		return
	}
	check := func(path string) int {
		if s.local == nil {
			return 0
		}
		roles, member, known := s.local.MemberRoles(actor)
		if known && !member && viewerPath(path) {
			return http.StatusNotFound
		}
		return s.mayView(path, roles, known)
	}
	if st := check(c.Request.URL.Path); st != 0 {
		c.AbortWithStatusJSON(st, gin.H{"error": "Dashboard not found"})
		return
	}
	if s.local != nil {
		if roles, _, known := s.local.MemberRoles(actor); known {
			c.Set(ctxViewerRoles, roles) // a non-member never gets here for a viewerPath
		}
	}
	c.Set(ctxRemoteActor, actor)
	c.Set(ctxRemoteRole, role)
	c.Set(ctxRecheck, check)
	c.Next()
}

// ctxViewerRoles holds a remote viewer's home roles ([]string) when the house knows them;
// ctxRecheck a func(path string) int telling whether the viewer may still have path now (0, or
// the status a new request would get), for a live socket that outlives a request.
const (
	ctxViewerRoles = "viewer_roles"
	ctxRecheck     = "viewer_recheck"
)

// viewerPath is a path whose answer depends on the dashboards the viewer sees.
func viewerPath(p string) bool {
	return dashboardOfPath(p) != "" || strings.HasPrefix(p, "/api/camera/")
}

// mayView is the status a remote viewer with roles gets for path: 0 to pass, 404 for a
// dashboard their roles do not reach or that the cloud's list leaves out. known false (the
// cloud alone knows the roles) lets every path pass.
func (s *Server) mayView(path string, roles []string, known bool) int {
	if id := dashboardOfPath(path); id != "" && known {
		if dr, listed := s.remoteDashboardRoles(id); !listed || !rolesAllow(dr, roles) {
			return http.StatusNotFound
		}
	}
	return 0
}

// viewerRecheck is whether the viewer of c may still have path (see ctxRecheck): 0 for the
// house, which its own auth checks.
func viewerRecheck(c *gin.Context, path string) int {
	if f, ok := c.Get(ctxRecheck); ok {
		return f.(func(string) int)(path)
	}
	return 0
}

// remoteRole is the cloud role of a request through the tunnel, "" for the house.
func remoteRole(c *gin.Context) string { return c.GetString(ctxRemoteRole) }

// roleArg is the role as the last argument of a Dashboard.API call, nothing for the house.
// The role is one of remoteRoles, so it needs no escaping.
func roleArg(c *gin.Context) string {
	if r := remoteRole(c); r != "" {
		return `, "` + r + `"`
	}
	return ""
}

// roleKey separates cached answers per remote role. Every remote viewer shares one answer: Lua
// tells only the house (role nil) from a remote viewer, and which dashboards a person sees is
// checked before the cache. An argument per person in Dashboard.API would have to be in the key.
func roleKey(c *gin.Context) string {
	if r := remoteRole(c); r != "" {
		return "\x00remote:" + r
	}
	return ""
}

// limitKey is who a rate limit counts: the address on the LAN, the person through the cloud
// (all of whom come from one address).
func limitKey(c *gin.Context) string {
	if a := c.GetString(ctxRemoteActor); a != "" {
		return a
	}
	return c.ClientIP()
}

// handleTunnelCameraSnapshot serves a camera only when an image widget on a dashboard this
// viewer sees shows it.
func (s *Server) handleTunnelCameraSnapshot(c *gin.Context) {
	if s.luaExecutor == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "Lua engine not ready"})
		return
	}
	// only on a dashboard the viewer's roles reach, when the house knows them
	var roles []string
	v, known := c.Get(ctxViewerRoles)
	if known {
		roles = v.([]string)
	}
	// a client of the device API sees no dashboard naming no roles (clientMayView)
	client := strings.HasPrefix(c.GetString(ctxRemoteActor), "client:")
	only, _ := json.Marshal(s.remoteDashboardsFor(roles, known, client))
	out, err := s.luaExecutor.Execute(fmt.Sprintf(`Dashboard.API.remoteCamera("%s"%s, "%s")`,
		luaEscape(c.Param("id")), roleArg(c), luaEscape(string(only))))
	var r struct {
		Used bool `json:"used"`
	}
	if err != nil || json.Unmarshal([]byte(out), &r) != nil || !r.Used {
		c.JSON(http.StatusNotFound, gin.H{"error": "camera not found"})
		return
	}
	s.handleAPICameraSnapshot(c)
}
