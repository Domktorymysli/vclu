package web

import (
	"github.com/gin-gonic/gin"
	"net/http"
)

// handleTools shows the tools page
func (s *Server) handleTools(c *gin.Context) {
	trans := s.getTranslations(c)

	s.renderHTML(c, http.StatusOK, "tools.html", gin.H{
		"title":           "Developer Tools",
		"currentPage":     "tools",
		"configured":      s.isConfigured,
		"supportsModules": s.supportsModules,
		"lang":            trans["lang"],
		"t":               trans["t"],
	})
}
