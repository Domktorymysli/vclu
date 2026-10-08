package web

import (
	"net/http"

	"github.com/Domktorymysli/vclu/pkg/config"
	"github.com/gin-gonic/gin"
)

// handleAPIDevice returns device information as JSON
//
//	@Summary		Device configuration
//	@Description	Returns the current CLU device configuration. Responds 404 when the device has not been configured yet.
//	@Tags			settings
//	@Produce		json
//	@Success		200	{object}	config.CLUDeviceConfig
//	@Failure		404	{object}	web.APIError
//	@Security		ApiKeyAuth
//	@Router			/api/device [get]
func (s *Server) handleAPIDevice(c *gin.Context) {
	if s.deviceConfig == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Device not configured"})
		return
	}

	c.JSON(http.StatusOK, s.deviceConfig)
}

// handleAPIKeys returns key status (not actual keys for security)
//
//	@Summary		Key status
//	@Description	Reports whether a project key and IV are configured (booleans only — key material is never returned) plus the device PIN. Responds 404 when keys are not configured.
//	@Tags			settings
//	@Produce		json
//	@Success		200	{object}	map[string]interface{}	"hasProjectKey: bool, hasIV: bool, pin: string"
//	@Failure		404	{object}	web.APIError
//	@Security		ApiKeyAuth
//	@Router			/api/keys [get]
func (s *Server) handleAPIKeys(c *gin.Context) {
	if s.keys == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Keys not configured"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"hasProjectKey": len(s.keys.Key) > 0,
		"hasIV":         len(s.keys.IV) > 0,
		"pin":           string(s.keys.PIN),
	})
}

// handleAPIDevices returns list of predefined device types
//
//	@Summary		Predefined device types
//	@Description	Returns the static list of device types vCLU can emulate.
//	@Tags			settings
//	@Produce		json
//	@Success		200	{array}		config.DeviceType
//	@Security		ApiKeyAuth
//	@Router			/api/devices [get]
func (s *Server) handleAPIDevices(c *gin.Context) {
	c.JSON(http.StatusOK, config.PredefinedDevices)
}

// handleAPISettingsSave saves settings via API
//
//	@Summary		Save device settings
//	@Description	Replaces the in-memory CLU device configuration with the posted one and persists it to disk. Takes effect immediately for the web server; no restart is triggered.
//	@Tags			settings
//	@Accept			json
//	@Produce		json
//	@Param			settings	body		config.CLUDeviceConfig	true	"Device configuration"
//	@Success		200			{object}	map[string]interface{}	"message: \"Settings saved successfully\""
//	@Failure		400			{object}	web.APIError
//	@Failure		500			{object}	web.APIError
//	@Security		ApiKeyAuth
//	@Router			/api/settings/save [post]
func (s *Server) handleAPISettingsSave(c *gin.Context) {
	var req config.CLUDeviceConfig

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request"})
		return
	}

	// Update config
	s.deviceConfig = &req

	// Save to disk
	if err := config.WriteDeviceConfig(s.serverDir, s.deviceConfig); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to save config"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "Settings saved successfully"})
}
