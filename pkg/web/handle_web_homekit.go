package web

import (
	"net/http"

	"github.com/Domktorymysli/vclu/pkg/config"
	"github.com/gin-gonic/gin"
)

// handleHomeKit shows the HomeKit configuration page
func (s *Server) handleHomeKit(c *gin.Context) {
	data := gin.H{
		"title":           "HomeKit",
		"currentPage":     "homekit",
		"configured":      s.isConfigured,
		"supportsModules": s.supportsModules,
	}

	// Load HomeKit config from master config
	masterConfig, _ := config.ReadMasterConfig(s.serverDir)
	if masterConfig != nil && masterConfig.HomeKit != nil {
		data["homekit"] = gin.H{
			"enabled":      masterConfig.HomeKit.Enabled,
			"pin":          masterConfig.HomeKit.Pin,
			"port":         masterConfig.HomeKit.Port,
			"bridgeName":   masterConfig.HomeKit.BridgeName,
			"serialNumber": masterConfig.HomeKit.SerialNumber,
		}
	} else {
		defaults := config.DefaultHomeKitConfig()
		data["homekit"] = gin.H{
			"enabled":      defaults.Enabled,
			"pin":          defaults.Pin,
			"port":         defaults.Port,
			"bridgeName":   defaults.BridgeName,
			"serialNumber": defaults.SerialNumber,
		}
	}

	// Get HomeKit server status if running
	if s.homekitServer != nil {
		status := s.homekitServer.GetStatus()
		data["status"] = gin.H{
			"running":          status.Running,
			"accessoryCount":   status.AccessoryCount,
			"pairingAvailable": status.PairingAvailable,
		}
	} else {
		data["status"] = gin.H{
			"running": false,
		}
	}

	// Add translations
	trans := s.getTranslations(c)
	data["lang"] = trans["lang"]
	data["t"] = trans["t"]
	data["langs"] = trans["langs"]

	s.renderHTML(c, http.StatusOK, "homekit.html", data)
}

// handleHomeKitPost handles saving HomeKit configuration
func (s *Server) handleHomeKitPost(c *gin.Context) {
	enabled := c.PostForm("enabled") == "on"
	pin := c.PostForm("pin")
	port := c.PostForm("port")
	bridgeName := c.PostForm("bridgeName")
	serialNumber := c.PostForm("serialNumber")

	// Parse port
	var portNum int
	if port != "" {
		_, err := parseIntParam(port)
		if err != nil {
			s.renderHTML(c, http.StatusBadRequest, "homekit.html", gin.H{
				"error": "Invalid port number",
			})
			return
		}
		portNum, _ = parseIntParam(port)
	} else {
		portNum = 5123
	}

	// Validate PIN (must be 8 digits)
	if len(pin) != 8 {
		pin = "00102003" // Default PIN
	}

	// Load and update master config
	masterConfig, err := config.ReadMasterConfig(s.serverDir)
	if err != nil {
		masterConfig = &config.VCLUConfig{}
	}
	if masterConfig == nil {
		masterConfig = &config.VCLUConfig{}
	}

	masterConfig.HomeKit = &config.VCLUHomeKitConfig{
		Enabled:      enabled,
		Pin:          pin,
		Port:         portNum,
		BridgeName:   bridgeName,
		SerialNumber: serialNumber,
	}

	if err := config.WriteMasterConfig(s.serverDir, masterConfig); err != nil {
		s.renderHTML(c, http.StatusInternalServerError, "homekit.html", gin.H{
			"error": "Failed to save configuration",
		})
		return
	}

	// Redirect back to HomeKit page
	c.Redirect(http.StatusSeeOther, "/homekit")
}

// handleAPIHomeKitSave saves HomeKit configuration via JSON API
//
//	@Summary		Save HomeKit configuration
//	@Description	Stores the HomeKit section of .vclu.json and reloads the running HomeKit server so changes apply immediately. A PIN that is not 8 digits is replaced with the default, port 0 with 5123. If the reload fails the configuration is still saved and a warning is returned.
//	@Tags			homekit
//	@Accept			json
//	@Produce		json
//	@Param			config	body		object	true	"HomeKit configuration: {enabled, pin, port, bridgeName, serialNumber}"
//	@Success		200		{object}	map[string]interface{}	"success bool, warning string (when saved but reload failed)"
//	@Failure		400		{object}	web.APIError
//	@Failure		500		{object}	web.APIError
//	@Security		ApiKeyAuth
//	@Router			/api/homekit/save [post]
func (s *Server) handleAPIHomeKitSave(c *gin.Context) {
	var req struct {
		Enabled      bool   `json:"enabled"`
		Pin          string `json:"pin"`
		Port         int    `json:"port"`
		BridgeName   string `json:"bridgeName"`
		SerialNumber string `json:"serialNumber"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request"})
		return
	}

	// Validate PIN (must be 8 digits)
	if len(req.Pin) != 8 {
		req.Pin = "00102003"
	}
	if req.Port == 0 {
		req.Port = 5123
	}

	// Load and update master config
	masterConfig, err := config.ReadMasterConfig(s.serverDir)
	if err != nil || masterConfig == nil {
		masterConfig = &config.VCLUConfig{}
	}

	// Preserve RefreshInterval if it was set
	refreshInterval := 300
	if masterConfig.HomeKit != nil && masterConfig.HomeKit.RefreshInterval > 0 {
		refreshInterval = masterConfig.HomeKit.RefreshInterval
	}

	masterConfig.HomeKit = &config.VCLUHomeKitConfig{
		Enabled:         req.Enabled,
		Pin:             req.Pin,
		Port:            req.Port,
		BridgeName:      req.BridgeName,
		SerialNumber:    req.SerialNumber,
		RefreshInterval: refreshInterval,
	}

	if err := config.WriteMasterConfig(s.serverDir, masterConfig); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to save configuration"})
		return
	}

	// Restart HomeKit server if available (applies config changes immediately)
	if s.homekitServer != nil {
		if err := s.homekitServer.Reload(); err != nil {
			c.JSON(http.StatusOK, gin.H{"success": true, "warning": "Saved but restart failed: " + err.Error()})
			return
		}
	}

	c.JSON(http.StatusOK, gin.H{"success": true})
}

// handleAPIHomeKitStatus returns HomeKit server status
//
//	@Summary		HomeKit server status
//	@Description	Live status of the HomeKit bridge (accessory count, PIN, pairing availability). When the server is not initialized, only {"running": false} is returned.
//	@Tags			homekit
//	@Produce		json
//	@Success		200	{object}	homekit.ServerStatus
//	@Security		ApiKeyAuth
//	@Router			/api/homekit/status [get]
func (s *Server) handleAPIHomeKitStatus(c *gin.Context) {
	if s.homekitServer == nil {
		c.JSON(http.StatusOK, gin.H{
			"running": false,
		})
		return
	}

	status := s.homekitServer.GetStatus()
	c.JSON(http.StatusOK, status)
}

// handleAPIHomeKitRestart restarts the HomeKit server
//
//	@Summary		Restart HomeKit server
//	@Description	Reloads the HomeKit bridge with the stored configuration, re-publishing all accessories.
//	@Tags			homekit
//	@Produce		json
//	@Success		200	{object}	web.APIOK
//	@Failure		500	{object}	web.APIError
//	@Failure		503	{object}	web.APIError
//	@Security		ApiKeyAuth
//	@Router			/api/homekit/restart [post]
func (s *Server) handleAPIHomeKitRestart(c *gin.Context) {
	if s.homekitServer == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"error": "HomeKit server not initialized",
		})
		return
	}

	if err := s.homekitServer.Reload(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
	})
}

// parseIntParam parses an integer from string
func parseIntParam(s string) (int, error) {
	var val int
	_, err := stringToInt(s, &val)
	return val, err
}

// stringToInt converts string to int
func stringToInt(s string, val *int) (int, error) {
	n := 0
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0, nil
		}
		n = n*10 + int(c-'0')
	}
	*val = n
	return n, nil
}
