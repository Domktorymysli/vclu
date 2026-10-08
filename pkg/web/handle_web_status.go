package web

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// handleStatus shows the status page
func (s *Server) handleStatus(c *gin.Context) {
	// Get translations
	trans := s.getTranslations(c)

	data := gin.H{
		"title":           "Virtual CLU Status",
		"currentPage":     "status",
		"configured":      s.isConfigured,
		"supportsModules": s.supportsModules,
		"setupComplete":   c.Query("setup") == "complete",
		"lang":            trans["lang"],
		"t":               trans["t"],
		"langs":           trans["langs"],
	}

	// Device info, encryption keys, and pairing status are on the Grenton page

	// Add MQTT client statistics
	if mqttStats := s.getMQTTStats(); mqttStats != nil {
		data["mqtt"] = mqttStats
	}

	// Add MQTT broker statistics
	if s.brokerController != nil {
		if broker := s.brokerController.GetMQTTBroker(); broker != nil {
			data["mqttBroker"] = broker.Stats()
		}
	}

	s.renderHTML(c, http.StatusOK, "status.html", data)
}
