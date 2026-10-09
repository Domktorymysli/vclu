package web

import (
	"github.com/Domktorymysli/vclu/pkg/homekit"
	"log"
	"net/http"
	"strings"

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

	// Same rules as the API: a wrong PIN is refused, not replaced
	if pin == "" {
		pin = "00102003"
	}
	if err := homekit.ValidatePin(pin); err != nil {
		s.renderHTML(c, http.StatusBadRequest, "homekit.html", gin.H{"error": err.Error()})
		return
	}

	masterConfig, err := config.ReadMasterConfig(s.serverDir)
	if err != nil || masterConfig == nil {
		masterConfig = &config.VCLUConfig{}
	}
	masterConfig.HomeKit = homeKitConfig(masterConfig.HomeKit, enabled, pin, portNum, bridgeName, serialNumber)

	if err := config.WriteMasterConfig(s.serverDir, masterConfig); err != nil {
		s.renderHTML(c, http.StatusInternalServerError, "homekit.html", gin.H{
			"error": "Failed to save configuration",
		})
		return
	}
	if warning := s.applyHomeKitConfig(masterConfig.HomeKit); warning != "" {
		log.Printf("[HomeKit] %s", warning)
	}

	// Redirect back to HomeKit page
	c.Redirect(http.StatusSeeOther, "/homekit")
}

// handleAPIHomeKitSave saves HomeKit configuration via JSON API
//
//	@Summary		Save HomeKit configuration
//	@Description	Stores the HomeKit section of .vclu.json and reloads the running HomeKit server so changes apply immediately. An empty PIN means the default 00102003; a PIN that is not 8 digits or that HomeKit refuses as insecure is rejected with 400; port 0 means 5123. Disabling stops the bridge; enabling a bridge that was off at startup needs a restart of vCLU. If the reload fails the configuration is still saved and a warning is returned.
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

	// An empty PIN keeps the default; a wrong one is refused, not replaced
	// (the Home app would ask for a code the user never set)
	if req.Pin == "" {
		req.Pin = "00102003"
	}
	if err := homekit.ValidatePin(req.Pin); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if req.Port == 0 {
		req.Port = 5123
	}

	// Load and update master config
	masterConfig, err := config.ReadMasterConfig(s.serverDir)
	if err != nil || masterConfig == nil {
		masterConfig = &config.VCLUConfig{}
	}

	masterConfig.HomeKit = homeKitConfig(masterConfig.HomeKit, req.Enabled, req.Pin, req.Port, req.BridgeName, req.SerialNumber)

	if err := config.WriteMasterConfig(s.serverDir, masterConfig); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to save configuration"})
		return
	}

	if warning := s.applyHomeKitConfig(masterConfig.HomeKit); warning != "" {
		c.JSON(http.StatusOK, gin.H{"success": true, "warning": warning})
		return
	}

	c.JSON(http.StatusOK, gin.H{"success": true})
}

// homeKitConfig builds the saved HomeKit section: refresh interval kept, an
// empty bridge name gets the default (hap refuses an accessory without a name).
func homeKitConfig(old *config.VCLUHomeKitConfig, enabled bool, pin string, port int, name, serial string) *config.VCLUHomeKitConfig {
	refresh := 300
	if old != nil && old.RefreshInterval > 0 {
		refresh = old.RefreshInterval
	}
	if strings.TrimSpace(name) == "" {
		name = "vCLU Bridge"
	}
	return &config.VCLUHomeKitConfig{
		Enabled:         enabled,
		Pin:             pin,
		Port:            port,
		BridgeName:      name,
		SerialNumber:    serial,
		RefreshInterval: refresh,
	}
}

// applyHomeKitConfig applies saved settings to the running bridge; returns a
// warning for the user, or "".
func (s *Server) applyHomeKitConfig(cfg *config.VCLUHomeKitConfig) string {
	if s.homekitServer == nil {
		if cfg.Enabled {
			return "Saved; restart vCLU to start HomeKit"
		}
		return ""
	}
	// The running server keeps its own config pointer: hand it the new one
	s.homekitServer.SetConfig(cfg)
	if !cfg.Enabled {
		s.homekitServer.Stop()
		return ""
	}
	if err := s.homekitServer.Reload(); err != nil {
		return "Saved but restart failed: " + err.Error()
	}
	return ""
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
