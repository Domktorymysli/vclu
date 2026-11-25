package web

import (
	"encoding/json"
	"io"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/Domktorymysli/vclu/pkg/plugins"
)

// maxActionBody bounds the params of one action call.
const maxActionBody = 64 << 10

// handlePluginsAPIActions lists the actions of running plugins (?plugin= for one).
func (s *Server) handlePluginsAPIActions(c *gin.Context) {
	if s.luaExecutor == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "Lua engine not ready"})
		return
	}
	filter := c.Query("plugin")
	if filter != "" && !plugins.ValidPluginID(filter) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid plugin id"})
		return
	}
	out, err := s.luaExecutor.Execute(plugins.ListActionsScript(filter))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	actions, err := plugins.ParseActions(out)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, actions)
}

// handlePluginsAPICallAction runs a plugin's action with the JSON body as its params, as the
// request's initiator: {result}, or {error} with 404 (no such action), 400 (params) or 422.
func (s *Server) handlePluginsAPICallAction(c *gin.Context) {
	if s.luaExecutor == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "Lua engine not ready"})
		return
	}
	id, name := c.Param("id"), c.Param("name")
	if !plugins.ValidPluginID(id) || !plugins.ValidActionName(name) {
		c.JSON(http.StatusNotFound, gin.H{"error": "no such action"})
		return
	}
	body, err := io.ReadAll(io.LimitReader(c.Request.Body, maxActionBody+1))
	if err != nil || len(body) > maxActionBody {
		c.JSON(http.StatusBadRequest, gin.H{"error": "params too large"})
		return
	}
	params := map[string]any{}
	if len(body) > 0 {
		if err := json.Unmarshal(body, &params); err != nil || params == nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "params are a JSON object"})
			return
		}
	}
	script, err := plugins.CallActionScript(id, name, params)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	out, err := s.executeAs(c, "plugin-action:"+id, script)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	result, err := plugins.ParseActionResult(out)
	if err != nil {
		status := http.StatusInternalServerError
		switch plugins.ActionErrorCode(err) {
		case "not_found":
			status = http.StatusNotFound
		case "invalid":
			status = http.StatusBadRequest
		case "failed":
			status = http.StatusUnprocessableEntity
		}
		c.JSON(status, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"result": result})
}
