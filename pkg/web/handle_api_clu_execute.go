package web

import (
	"github.com/gin-gonic/gin"
	"log"
	"net/http"
)

// CLUExecuteRequest represents a request to execute a command on a CLU
type CLUExecuteRequest struct {
	TargetIP string `json:"targetIP" binding:"required"`
	Script   string `json:"script" binding:"required"`
}

// handleAPICLUExecute sends a command to a CLU and returns the result
//
//	@Summary		Execute script on a CLU
//	@Description	Sends a Lua script over the encrypted Grenton UDP protocol to the CLU at the given IP and returns the textual response. Blocks until the CLU replies or the command times out.
//	@Tags			registry
//	@Accept			json
//	@Produce		json
//	@Param			command	body		web.CLUExecuteRequest	true	"Target CLU IP and script to execute"
//	@Success		200		{object}	map[string]interface{}	"result: CLU response string"
//	@Failure		400		{object}	web.APIError
//	@Failure		500		{object}	web.APIError
//	@Failure		503		{object}	web.APIError
//	@Security		ApiKeyAuth
//	@Router			/api/clu/execute [post]
func (s *Server) handleAPICLUExecute(c *gin.Context) {
	if s.cluCommander == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "CLU commander not available"})
		return
	}

	var req CLUExecuteRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request: " + err.Error()})
		return
	}

	log.Printf("Executing command on %s: %s", req.TargetIP, req.Script)

	result, err := s.cluCommander.SendCommand(req.TargetIP, req.Script)
	if err != nil {
		log.Printf("Command execution failed: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"result": result,
	})
}
