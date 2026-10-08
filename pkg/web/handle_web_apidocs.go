package web

import (
	"net/http"
	"os"
	"path/filepath"

	"github.com/gin-gonic/gin"
)

// handleAPIDocs renders the interactive API documentation (Redoc) page.
func (s *Server) handleAPIDocs(c *gin.Context) {
	trans := s.getTranslations(c)
	s.renderHTML(c, http.StatusOK, "api-docs.html", gin.H{
		"lang": trans["lang"],
	})
}

// handleOpenAPISpec serves the generated OpenAPI (Swagger 2.0) spec.
// The file is generated from handler annotations — see pkg/web/doc.go.
func (s *Server) handleOpenAPISpec(c *gin.Context) {
	specPath := filepath.Join(s.webDir, "api", "swagger.json")
	if _, err := os.Stat(specPath); err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "OpenAPI spec not found — regenerate with go generate ./pkg/web"})
		return
	}
	c.File(specPath)
}
