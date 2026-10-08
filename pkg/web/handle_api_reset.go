package web

import (
	"log"
	"net/http"
	"os"
	"time"

	"github.com/gin-gonic/gin"
)

// handleAPIReset resets the CLU to factory defaults.
// Removes everything in serverDir and restarts the process.
//
//	@Summary		Factory reset
//	@Description	Irreversibly wipes the entire server data directory (configuration, keys, scripts), recreates it empty, then exits the process after 500 ms so the service manager restarts vCLU into the setup wizard.
//	@Tags			system
//	@Produce		json
//	@Success		200	{object}	map[string]interface{}	"message: confirmation text, redirect: \"/wizard\""
//	@Failure		500	{object}	web.APIError
//	@Security		ApiKeyAuth
//	@Router			/api/reset [post]
func (s *Server) handleAPIReset(c *gin.Context) {
	log.Printf("CLU reset requested - wiping %s", s.serverDir)

	// Remove entire serverDir contents
	if err := os.RemoveAll(s.serverDir); err != nil {
		log.Printf("ERROR: Failed to remove serverDir: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": "Failed to reset: " + err.Error(),
		})
		return
	}

	// Recreate empty serverDir
	if err := os.MkdirAll(s.serverDir, 0755); err != nil {
		log.Printf("ERROR: Failed to recreate serverDir: %v", err)
	}

	log.Printf("CLU reset complete - all data cleared")

	c.JSON(http.StatusOK, gin.H{
		"message":  "CLU reset successfully. Redirecting to setup...",
		"redirect": "/wizard",
	})

	// Exit process so systemd restarts vCLU with clean state
	go func() {
		time.Sleep(500 * time.Millisecond)
		log.Printf("Restarting vCLU after reset...")
		os.Exit(0)
	}()
}
