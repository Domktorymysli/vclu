package web

import (
	"encoding/base64"
	"errors"
	"log"
	"net"
	"net/http"

	"github.com/Domktorymysli/vclu/pkg/config"
	"github.com/Domktorymysli/vclu/pkg/i18n"
	"github.com/gin-gonic/gin"
)

// handleSettingsPost processes the settings form
func (s *Server) handleSettingsPost(c *gin.Context) {
	trans := s.getTranslations(c)

	// Parse form data
	pin := c.PostForm("pin")
	broadcastEnabled := c.PostForm("broadcastEnabled") == "on"
	broadcastKeyB64 := c.PostForm("broadcastKey")
	broadcastIVB64 := c.PostForm("broadcastIV")

	// Helper to render settings with error
	renderError := func(errMsg string) {
		data := s.getSettingsData(trans)
		data["error"] = errMsg
		s.renderHTML(c, http.StatusOK, "settings.html", data)
	}

	// Update PIN
	s.keys.PIN = []byte(pin)

	// Update broadcast keys
	s.keys.BroadcastEnabled = broadcastEnabled
	if broadcastEnabled && broadcastKeyB64 != "" && broadcastIVB64 != "" {
		broadcastKey, err1 := base64.StdEncoding.DecodeString(broadcastKeyB64)
		broadcastIV, err2 := base64.StdEncoding.DecodeString(broadcastIVB64)

		if err1 != nil || err2 != nil || len(broadcastKey) != 16 || len(broadcastIV) != 16 {
			renderError("Invalid broadcast keys - must be 16-byte values encoded in Base64")
			return
		}

		s.keys.BroadcastKey = broadcastKey
		s.keys.BroadcastIV = broadcastIV
	}

	// Save to master config (.vclu.json) - this is the source of truth
	if err := config.SaveToMasterConfig(s.serverDir, s.deviceConfig, s.keys); err != nil {
		log.Printf("Failed to save master config: %v", err)
	}

	// Propagate config to create/update derived files
	localIP := net.ParseIP(s.getLocalIP())
	if err := config.PropagateConfig(s.serverDir, localIP, s.deviceConfig, s.keys); err != nil {
		log.Printf("Warning: Failed to propagate config: %v", err)
	}

	// Show success message
	lang, _ := trans["lang"].(string)
	data := s.getSettingsData(trans)
	data["success"] = i18n.Get(lang, "settings.saved")
	s.renderHTML(c, http.StatusOK, "settings.html", data)
}

// handleSecurityPost processes the security form (auth settings only)
func (s *Server) handleSecurityPost(c *gin.Context) {
	trans := s.getTranslations(c)

	// Auth fields
	authEnabled := c.PostForm("authEnabled") == "on"
	authUsername := c.PostForm("authUsername")
	authPassword := c.PostForm("authPassword")
	authPasswordConfirm := c.PostForm("authPasswordConfirm")

	// Helper to render settings with error
	renderError := func(errMsg string) {
		data := s.getSettingsData(trans)
		data["error"] = errMsg
		s.renderHTML(c, http.StatusOK, "settings.html", data)
	}

	lang, _ := trans["lang"].(string)

	// Handle auth settings
	if authEnabled {
		// Validate username
		if len(authUsername) < 3 {
			renderError(i18n.Get(lang, "settings.err_username_min"))
			return
		}

		// If password provided, validate and update
		if authPassword != "" {
			if len(authPassword) < 8 {
				renderError(i18n.Get(lang, "settings.err_password_min"))
				return
			}
			if authPassword != authPasswordConfirm {
				renderError(i18n.Get(lang, "settings.err_password_mismatch"))
				return
			}
			// Save new password
			if err := config.SaveAuth(s.serverDir, authUsername, authPassword); err != nil {
				log.Printf("Failed to save auth: %v", err)
				renderError(i18n.Get(lang, "settings.err_save_auth"))
				return
			}
			log.Printf("Authentication updated for user: %s", authUsername)
		} else {
			// Password not changed, just update username if needed
			errNoAuth := errors.New("auth not configured")
			renamed := false
			err := updateMasterConfig(s.serverDir, func(mc *config.VCLUConfig) error {
				if mc.Auth == nil {
					return errNoAuth
				}
				if mc.Auth.Username == authUsername {
					return errUnchanged
				}
				mc.Auth.Username = authUsername
				renamed = true
				return nil
			})
			switch {
			case errors.Is(err, errNoAuth):
				// Auth not configured yet but enabled without password
				renderError(i18n.Get(lang, "settings.err_password_required"))
				return
			case err != nil:
				log.Printf("Failed to save auth: %v", err)
				renderError(i18n.Get(lang, "settings.err_save_auth"))
				return
			case renamed:
				log.Printf("Username updated to: %s", authUsername)
			}
		}
	} else {
		// Disable auth - remove auth config
		disabled := false
		err := updateMasterConfig(s.serverDir, func(mc *config.VCLUConfig) error {
			if mc.Auth == nil {
				return errUnchanged
			}
			mc.Auth = nil
			disabled = true
			return nil
		})
		if err != nil {
			log.Printf("Failed to save auth: %v", err)
			renderError(i18n.Get(lang, "settings.err_save_auth"))
			return
		}
		if disabled {
			log.Printf("Authentication disabled")
		}
	}

	// Redirect with success
	data := s.getSettingsData(trans)
	data["success"] = i18n.Get(lang, "settings.security_saved")
	s.renderHTML(c, http.StatusOK, "settings.html", data)
}

// handleAPIGenerateAPIKey generates a new API key and returns the plaintext key
//
//	@Summary		Generate API key
//	@Description	Generates a new API key, stores its hash in the auth configuration and returns the plaintext key once — it cannot be retrieved later. Replaces any previous key. Requires session authentication; requests authenticated with an API key are rejected with 403. Authentication must already be enabled.
//	@Tags			settings
//	@Produce		json
//	@Success		200	{object}	map[string]interface{}	"apiKey: the plaintext key (shown only once)"
//	@Failure		400	{object}	web.APIError
//	@Failure		403	{object}	web.APIError
//	@Failure		500	{object}	web.APIError
//	@Security		ApiKeyAuth
//	@Router			/api/settings/generate-api-key [post]
func (s *Server) handleAPIGenerateAPIKey(c *gin.Context) {
	// Only allow session-based auth (not API key) to generate new API keys
	if c.GetString("auth_method") == "apikey" {
		c.JSON(http.StatusForbidden, gin.H{"error": "API key generation requires session authentication"})
		return
	}

	plaintext, hash, err := config.GenerateAPIKey()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to generate API key"})
		return
	}

	errNoAuth := errors.New("auth not configured")
	err = config.UpdateMasterConfig(s.serverDir, func(mc *config.VCLUConfig) error {
		if mc.Auth == nil {
			return errNoAuth
		}
		mc.Auth.APIKeyHash = hash
		return nil
	})
	if errors.Is(err, errNoAuth) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Authentication must be enabled first"})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to save API key"})
		return
	}

	log.Printf("API key generated")
	c.JSON(http.StatusOK, gin.H{"apiKey": plaintext})
}

// handleAPIRevokeAPIKey revokes the current API key
//
//	@Summary		Revoke API key
//	@Description	Clears the stored API key hash so the current API key stops working immediately. Responds 400 when no auth configuration exists.
//	@Tags			settings
//	@Produce		json
//	@Success		200	{object}	web.APIOK
//	@Failure		400	{object}	web.APIError
//	@Failure		500	{object}	web.APIError
//	@Security		ApiKeyAuth
//	@Router			/api/settings/api-key [delete]
func (s *Server) handleAPIRevokeAPIKey(c *gin.Context) {
	errNoAuth := errors.New("auth not configured")
	err := config.UpdateMasterConfig(s.serverDir, func(mc *config.VCLUConfig) error {
		if mc.Auth == nil {
			return errNoAuth
		}
		mc.Auth.APIKeyHash = ""
		return nil
	})
	if errors.Is(err, errNoAuth) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "No auth config found"})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to revoke API key"})
		return
	}

	log.Printf("API key revoked")
	c.JSON(http.StatusOK, gin.H{"success": true})
}
