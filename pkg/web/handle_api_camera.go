package web

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// GET /api/camera/:id/snapshot — proxy camera snapshot image
func (s *Server) handleAPICameraSnapshot(c *gin.Context) {
	if s.cameraManager == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "camera not available"})
		return
	}

	id := c.Param("id")

	data, contentType, err := s.cameraManager.GetSnapshot(id)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
		return
	}

	if contentType == "" {
		contentType = "image/jpeg"
	}

	// Allow caching for a few seconds (matches manager cache TTL)
	c.Header("Cache-Control", "public, max-age=3")
	c.Data(http.StatusOK, contentType, data)
}
