package web

import (
	"github.com/Domktorymysli/vclu/pkg/config"
	"github.com/gin-gonic/gin"
)

// contextExecutor runs Lua as a given initiator (pkg/lua.LuaEngine.ExecuteAs).
type contextExecutor interface {
	ExecuteAs(origin, actor, correlationID, script string) (string, error)
}

// requestActor tells who makes a state-changing web request, from what the
// auth middleware established, never from the request body: a session is
// the single web account, an API key a service; with auth disabled the
// local user. fallback is used for public pages (a dashboard opened by PIN).
func (s *Server) requestActor(c *gin.Context, fallback string) (origin, actor string) {
	switch c.GetString("auth_method") {
	case "apikey":
		return "api", "service:web-api"
	case "session":
		return "web", "user:" + s.webUsername()
	}
	if !config.IsAuthEnabled(s.serverDir) {
		return "web", "user:local"
	}
	if token, err := c.Cookie(sessionCookieName); err == nil && s.sessions.validate(token) {
		return "web", "user:" + s.webUsername()
	}
	return "web", fallback
}

func (s *Server) webUsername() string {
	mc, _ := config.ReadMasterConfig(s.serverDir)
	if mc != nil && mc.Auth != nil && mc.Auth.Username != "" {
		return mc.Auth.Username
	}
	return "unknown"
}

// executeAs runs a state-changing script as the request's initiator.
func (s *Server) executeAs(c *gin.Context, fallbackActor, script string) (string, error) {
	if ce, ok := s.luaExecutor.(contextExecutor); ok {
		origin, actor := s.requestActor(c, fallbackActor)
		return ce.ExecuteAs(origin, actor, "", script)
	}
	return s.luaExecutor.Execute(script)
}
