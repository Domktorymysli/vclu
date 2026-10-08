package web

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// handleAPIRegistrySubscribe triggers auto-subscribe to get fresh states from remote CLUs
//
//	@Summary		Trigger auto-subscribe
//	@Description	Runs LuaAPI.autoSubscribe() in the Lua runtime to re-register state subscriptions on all remote CLUs and pull fresh object states.
//	@Tags			registry
//	@Produce		json
//	@Success		200	{object}	map[string]interface{}	"status: \"ok\", result: Lua return value"
//	@Failure		500	{object}	web.APIError
//	@Failure		503	{object}	web.APIError
//	@Security		ApiKeyAuth
//	@Router			/api/registry/subscribe [post]
func (s *Server) handleAPIRegistrySubscribe(c *gin.Context) {
	if s.luaExecutor == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "Lua executor not available"})
		return
	}

	result, err := s.luaExecutor.Execute("LuaAPI.autoSubscribe()")
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"status": "ok", "result": result})
}

// handleAPIRegistryList returns all objects from _registry
//
//	@Summary		List registry objects
//	@Description	Returns all objects registered in the Lua _registry as the raw JSON produced by _registry:toJSON(). Returns an empty array when the registry is empty.
//	@Tags			registry
//	@Produce		json
//	@Success		200	{array}		map[string]interface{}	"Registry objects as serialized by _registry:toJSON()"
//	@Failure		500	{object}	web.APIError
//	@Failure		503	{object}	web.APIError
//	@Security		ApiKeyAuth
//	@Router			/api/registry/list [get]
func (s *Server) handleAPIRegistryList(c *gin.Context) {
	if s.luaExecutor == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "Lua executor not available"})
		return
	}

	// Call _registry:toJSON() method defined in registry.lua
	result, err := s.luaExecutor.Execute("_registry:toJSON()")
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.Header("Content-Type", "application/json")
	if result == "" || result == "nil" {
		c.String(http.StatusOK, "[]")
	} else {
		c.String(http.StatusOK, result)
	}
}
