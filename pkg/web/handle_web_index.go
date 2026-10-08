package web

import (
	"github.com/Domktorymysli/vclu/pkg/config"
	"github.com/gin-gonic/gin"
	"net/http"
)

// handleIndex redirects to setup or status based on configuration
func (s *Server) handleIndex(c *gin.Context) {
	// Check if setup is complete using local config
	if config.IsSetupComplete(s.serverDir) {
		c.Redirect(http.StatusFound, "/stats")
	} else {
		c.Redirect(http.StatusFound, "/wizard")
	}
}
