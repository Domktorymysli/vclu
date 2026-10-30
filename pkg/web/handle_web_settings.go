package web

import (
	"github.com/Domktorymysli/vclu/pkg/config"
	"github.com/gin-gonic/gin"
	"net/http"
)

// getSettingsData builds the data map for settings page
func (s *Server) getSettingsData(trans map[string]interface{}) gin.H {
	data := gin.H{
		"title":           "Device Settings",
		"currentPage":     "settings",
		"configured":      s.isConfigured,
		"supportsModules": s.supportsModules,
		"lang":            trans["lang"],
		"t":               trans["t"],
		"langs":           trans["langs"],
	}

	if s.keys != nil {
		data["pin"] = string(s.keys.PIN)
		data["broadcastEnabled"] = s.keys.BroadcastEnabled
	}

	// Auth info
	masterConfig, _ := config.ReadMasterConfig(s.serverDir)
	if masterConfig != nil && masterConfig.Auth != nil && masterConfig.Auth.Username != "" {
		data["authEnabled"] = true
		data["authUsername"] = masterConfig.Auth.Username
		if masterConfig.Auth.APIKeyHash != "" {
			data["hasAPIKey"] = true
		}
	}

	// MQTT Broker config
	if masterConfig != nil && masterConfig.MQTTBroker != nil {
		data["mqttBroker"] = gin.H{
			"enabled":  masterConfig.MQTTBroker.Enabled,
			"port":     masterConfig.MQTTBroker.Port,
			"wsPort":   masterConfig.MQTTBroker.WSPort,
			"username": masterConfig.MQTTBroker.Username,
			"password": masterConfig.MQTTBroker.Password,
		}
	} else {
		data["mqttBroker"] = gin.H{
			"enabled": false,
			"port":    1883,
			"wsPort":  0,
		}
	}

	data["updateCheck"] = gin.H{
		"enabled":   masterConfig.UpdateCheckOn(),
		"forcedOff": config.UpdateCheckForcedOff(),
	}

	return data
}

// handleSettings shows the settings page
func (s *Server) handleSettings(c *gin.Context) {
	trans := s.getTranslations(c)
	data := s.getSettingsData(trans)
	s.renderHTML(c, http.StatusOK, "settings.html", data)
}
