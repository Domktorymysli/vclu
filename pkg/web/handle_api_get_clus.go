package web

import (
	"github.com/gin-gonic/gin"
	"log"
	"net/http"
)

// handleAPIGetCLUs returns list of registered CLUs (local and remote)
//
//	@Summary		List registered CLUs
//	@Description	Returns all CLUs known to the CLU commander, both the local virtual CLU and discovered remote CLUs.
//	@Tags			registry
//	@Produce		json
//	@Success		200	{array}		server.CLUInfo
//	@Failure		500	{object}	web.APIError
//	@Failure		503	{object}	web.APIError
//	@Security		ApiKeyAuth
//	@Router			/api/clus [get]
func (s *Server) handleAPIGetCLUs(c *gin.Context) {
	if s.cluCommander == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "CLU commander not available"})
		return
	}

	clus, err := s.cluCommander.GetRegisteredCLUs()
	if err != nil {
		log.Printf("Failed to get CLUs: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, clus)
}
