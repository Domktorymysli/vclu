package web

import (
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"
	"regexp"

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
	r.Use(gin.Recovery(), tunnelViewer, staticCache)
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
// not the cloud's and gets nothing.
func tunnelViewer(c *gin.Context) {
	actor, role := c.GetHeader(proto.HeaderActor), c.GetHeader(proto.HeaderRole)
	if !remoteActorRe.MatchString(actor) || !remoteRoles[role] {
		c.AbortWithStatus(http.StatusNotFound)
		return
	}
	c.Set(ctxRemoteActor, actor)
	c.Set(ctxRemoteRole, role)
	c.Next()
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

// roleKey separates cached answers per remote role.
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
	out, err := s.luaExecutor.Execute(fmt.Sprintf(`Dashboard.API.remoteCamera("%s"%s)`, luaEscape(c.Param("id")), roleArg(c)))
	var r struct {
		Used bool `json:"used"`
	}
	if err != nil || json.Unmarshal([]byte(out), &r) != nil || !r.Used {
		c.JSON(http.StatusNotFound, gin.H{"error": "camera not found"})
		return
	}
	s.handleAPICameraSnapshot(c)
}
