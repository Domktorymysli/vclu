package web

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/Domktorymysli/vclu/pkg/broker"
	"github.com/Domktorymysli/vclu/pkg/config"
	"github.com/gin-gonic/gin"
)

// handleMQTT shows the MQTT configuration page with Broker/Client/Access tabs
func (s *Server) handleMQTT(c *gin.Context) {
	activeTab := c.DefaultQuery("tab", "broker")
	if activeTab != "broker" && activeTab != "client" {
		activeTab = "broker"
	}

	data := gin.H{
		"title":           "MQTT",
		"currentPage":     "mqtt",
		"configured":      s.isConfigured,
		"supportsModules": s.supportsModules,
		"activeTab":       activeTab,
	}

	// Load broker config from master config
	masterConfig, _ := config.ReadMasterConfig(s.serverDir)
	if masterConfig != nil && masterConfig.MQTTBroker != nil {
		data["broker"] = gin.H{
			"enabled":  masterConfig.MQTTBroker.Enabled,
			"port":     masterConfig.MQTTBroker.Port,
			"wsPort":   masterConfig.MQTTBroker.WSPort,
			"username": masterConfig.MQTTBroker.Username,
			"password": masterConfig.MQTTBroker.Password,
		}
	} else {
		data["broker"] = gin.H{
			"enabled": false,
			"port":    1883,
			"wsPort":  0,
		}
	}

	// Load client config
	if masterConfig != nil && masterConfig.MQTTClient != nil {
		data["client"] = gin.H{
			"enabled":       masterConfig.MQTTClient.Enabled,
			"host":          masterConfig.MQTTClient.Host,
			"port":          masterConfig.MQTTClient.Port,
			"clientId":      masterConfig.MQTTClient.ClientID,
			"username":      masterConfig.MQTTClient.Username,
			"haIntegration": masterConfig.MQTTClient.HAIntegration,
		}
	} else {
		data["client"] = gin.H{
			"enabled":       false,
			"host":          "localhost",
			"port":          1883,
			"clientId":      "vclu_internal",
			"haIntegration": true,
		}
	}

	// Add translations
	trans := s.getTranslations(c)
	data["lang"] = trans["lang"]
	data["t"] = trans["t"]
	data["langs"] = trans["langs"]

	s.renderHTML(c, http.StatusOK, "mqtt.html", data)
}

// handleAPIMQTTBrokerStats returns broker statistics
//
//	@Summary		Embedded MQTT broker statistics
//	@Description	Live statistics of the embedded MQTT broker (clients, messages, bytes, subscriptions, memory). When the broker is not running, only {"running": false} is returned.
//	@Tags			mqtt
//	@Produce		json
//	@Success		200	{object}	broker.Stats
//	@Security		ApiKeyAuth
//	@Router			/api/mqtt/broker/stats [get]
func (s *Server) handleAPIMQTTBrokerStats(c *gin.Context) {
	if s.brokerController == nil {
		c.JSON(http.StatusOK, gin.H{
			"running": false,
		})
		return
	}

	broker := s.brokerController.GetMQTTBroker()
	if broker == nil {
		c.JSON(http.StatusOK, gin.H{
			"running": false,
		})
		return
	}

	stats := broker.Stats()
	c.JSON(http.StatusOK, stats)
}

// handleAPIMQTTBrokerClients returns list of clients connected to the broker
//
//	@Summary		Connected broker clients
//	@Description	Lists clients currently known to the embedded MQTT broker. Returns an empty list when the broker is not running.
//	@Tags			mqtt
//	@Produce		json
//	@Success		200	{object}	map[string]interface{}	"clients: []broker.ClientInfo"
//	@Security		ApiKeyAuth
//	@Router			/api/mqtt/broker/clients [get]
func (s *Server) handleAPIMQTTBrokerClients(c *gin.Context) {
	if s.brokerController == nil {
		c.JSON(http.StatusOK, gin.H{"clients": []interface{}{}})
		return
	}

	b := s.brokerController.GetMQTTBroker()
	if b == nil {
		c.JSON(http.StatusOK, gin.H{"clients": []interface{}{}})
		return
	}

	clients := b.GetClients()
	if clients == nil {
		clients = []broker.ClientInfo{}
	}
	c.JSON(http.StatusOK, gin.H{"clients": clients})
}

// handleAPIMQTTBrokerSave saves broker configuration
//
//	@Summary		Save broker configuration
//	@Description	Stores the embedded MQTT broker section of .vclu.json and immediately reconfigures the running broker — no vCLU restart required.
//	@Tags			mqtt
//	@Accept			json
//	@Produce		json
//	@Param			config	body		object	true	"Broker configuration: {enabled, port, wsPort, username, password}"
//	@Success		200		{object}	web.APIOK
//	@Failure		400		{object}	web.APIError
//	@Failure		500		{object}	web.APIError
//	@Security		ApiKeyAuth
//	@Router			/api/mqtt/broker/save [post]
func (s *Server) handleAPIMQTTBrokerSave(c *gin.Context) {
	var req struct {
		Enabled  bool   `json:"enabled"`
		Port     int    `json:"port"`
		WSPort   int    `json:"wsPort"`
		Username string `json:"username"`
		Password string `json:"password"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if err := config.UpdateMasterConfig(s.serverDir, func(mc *config.VCLUConfig) error {
		mc.MQTTBroker = &config.VCLUMQTTBrokerConfig{
			Enabled:  req.Enabled,
			Port:     req.Port,
			WSPort:   req.WSPort,
			Username: req.Username,
			Password: req.Password,
		}
		return nil
	}); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	// Reconfigure broker if available
	if s.brokerController != nil {
		b := s.brokerController.GetMQTTBroker()
		if b != nil {
			brokerCfg := &broker.Config{
				Enabled:  req.Enabled,
				Port:     req.Port,
				WSPort:   req.WSPort,
				Username: req.Username,
				Password: req.Password,
			}
			if err := b.Reconfigure(brokerCfg); err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
				return
			}
		}
	}

	c.JSON(http.StatusOK, gin.H{"success": true})
}

// handleAPIMQTTClientsStats returns MQTT client statistics from Lua
//
//	@Summary		MQTT client statistics
//	@Description	Aggregated statistics of all MQTT client instances managed by the Go MQTT manager. Returns zeroed counters when the manager is not available.
//	@Tags			mqtt
//	@Produce		json
//	@Success		200	{object}	lua.MQTTStats
//	@Security		ApiKeyAuth
//	@Router			/api/mqtt/clients/stats [get]
func (s *Server) handleAPIMQTTClientsStats(c *gin.Context) {
	stats := s.getMQTTStats()
	if stats == nil {
		c.JSON(http.StatusOK, gin.H{
			"totalClients":     0,
			"connectedClients": 0,
			"droppedMessages":  0,
			"clients":          []interface{}{},
		})
		return
	}

	c.JSON(http.StatusOK, stats)
}

// handleAPIMQTTTestPublish tests MQTT publish via Go MQTTManager
//
//	@Summary		Publish a test MQTT message
//	@Description	Publishes a message on the default MQTT client instance (QoS 0, not retained) to verify connectivity.
//	@Tags			mqtt
//	@Accept			json
//	@Produce		json
//	@Param			message	body		object	true	"Message: {topic, payload}"
//	@Success		200		{object}	web.APIOK
//	@Failure		400		{object}	web.APIError
//	@Failure		500		{object}	web.APIError
//	@Failure		503		{object}	web.APIError
//	@Security		ApiKeyAuth
//	@Router			/api/mqtt/test/publish [post]
func (s *Server) handleAPIMQTTTestPublish(c *gin.Context) {
	var req struct {
		Topic   string `json:"topic"`
		Payload string `json:"payload"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if s.mqttManager == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "MQTT manager not available"})
		return
	}

	if err := s.mqttManager.Publish(config.DefaultMQTTInstance, req.Topic, req.Payload, 0, false); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"success": true})
}

// handleAPIHACommandsEnable enables HA command handling and state publishing in Lua
//
//	@Summary		Enable Home Assistant integration
//	@Description	Runs LuaAPI.setupHAIntegration() in the Lua engine, enabling Home Assistant command handling and state publishing over MQTT.
//	@Tags			mqtt
//	@Produce		json
//	@Success		200	{object}	map[string]interface{}	"success bool, message string"
//	@Failure		500	{object}	web.APIError
//	@Failure		503	{object}	web.APIError
//	@Security		ApiKeyAuth
//	@Router			/api/ha/commands/enable [post]
func (s *Server) handleAPIHACommandsEnable(c *gin.Context) {
	if s.luaExecutor == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "Lua executor not available"})
		return
	}

	result, err := s.luaExecutor.Execute(`LuaAPI.setupHAIntegration()`)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	if result != "OK" {
		c.JSON(http.StatusInternalServerError, gin.H{"error": result})
		return
	}

	c.JSON(http.StatusOK, gin.H{"success": true, "message": "HA integration enabled"})
}

// handleAPIMQTTClientConfig returns MQTT client configuration (password masked)
//
//	@Summary		MQTT client configuration
//	@Description	Returns the stored MQTT client configuration (password masked as hasPassword) together with the live connection status of the default client instance.
//	@Tags			mqtt
//	@Produce		json
//	@Success		200	{object}	map[string]interface{}	"enabled, host, port, clientId, username, hasPassword, haIntegration, defaultAccess, status {connected, lastError, host, port}"
//	@Security		ApiKeyAuth
//	@Router			/api/mqtt/client/config [get]
func (s *Server) handleAPIMQTTClientConfig(c *gin.Context) {
	clientConfig := config.GetMQTTClientConfig(s.serverDir)
	if clientConfig == nil {
		clientConfig = config.DefaultMQTTClientConfig()
	}

	// Get connection status from Go MQTTManager
	var connected bool
	var lastError string
	var connHost string
	var connPort int
	if s.mqttManager != nil {
		stats := s.mqttManager.Stats()
		for _, cl := range stats.Clients {
			if cl.InstanceID == config.DefaultMQTTInstance {
				connected = cl.Connected
				lastError = cl.LastError
				connHost = cl.URL
				connPort = cl.Port
				break
			}
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"enabled":       clientConfig.Enabled,
		"host":          clientConfig.Host,
		"port":          clientConfig.Port,
		"clientId":      clientConfig.ClientID,
		"username":      clientConfig.Username,
		"hasPassword":   clientConfig.Password != "",
		"haIntegration": clientConfig.HAIntegration,
		"defaultAccess": clientConfig.DefaultAccess,
		"status": gin.H{
			"connected": connected,
			"lastError": lastError,
			"host":      connHost,
			"port":      connPort,
		},
	})
}

// handleAPIMQTTClientSave saves MQTT client config, reconnects, and optionally sets up HA integration
//
//	@Summary		Save MQTT client configuration
//	@Description	Stores the MQTT client section of .vclu.json and applies it immediately: reconnects the default client instance (or disconnects it when disabled) and sets up Home Assistant integration when enabled. An empty password keeps the stored one.
//	@Tags			mqtt
//	@Accept			json
//	@Produce		json
//	@Param			config	body		object	true	"Client configuration: {enabled, host, port, clientId, username, password, haIntegration}"
//	@Success		200		{object}	map[string]interface{}	"success bool, connected bool, error string (connect error, empty on success)"
//	@Failure		400		{object}	web.APIError
//	@Failure		500		{object}	web.APIError
//	@Security		ApiKeyAuth
//	@Router			/api/mqtt/client/save [post]
func (s *Server) handleAPIMQTTClientSave(c *gin.Context) {
	var req struct {
		Enabled       bool   `json:"enabled"`
		Host          string `json:"host"`
		Port          int    `json:"port"`
		ClientID      string `json:"clientId"`
		Username      string `json:"username"`
		Password      string `json:"password"`
		HAIntegration bool   `json:"haIntegration"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Defaults
	if req.Port == 0 {
		req.Port = 1883
	}
	if req.Host == "" {
		req.Host = "localhost"
	}
	if req.ClientID == "" {
		req.ClientID = "vclu_internal"
	}

	// Preserve existing password and defaultAccess when not provided
	if err := config.UpdateMasterConfig(s.serverDir, func(mc *config.VCLUConfig) error {
		existing := mc.MQTTClient
		if req.Password == "" && existing != nil {
			req.Password = existing.Password
		}
		defaultAccess := "full"
		if existing != nil && existing.DefaultAccess != "" {
			defaultAccess = existing.DefaultAccess
		}

		mc.MQTTClient = &config.VCLUMQTTClientConfig{
			Enabled:       req.Enabled,
			Host:          req.Host,
			Port:          req.Port,
			ClientID:      req.ClientID,
			Username:      req.Username,
			Password:      req.Password,
			HAIntegration: req.HAIntegration,
			DefaultAccess: defaultAccess,
		}
		return nil
	}); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	connected := false
	var connError string

	if req.Enabled && s.mqttManager != nil {
		// Tell Lua about the connection params (always)
		if s.luaExecutor != nil {
			connectScript := fmt.Sprintf(`MQTT:setHost(%q):setPort(%d):setClientId(%q):setCredentials(%q, %q)`,
				req.Host, req.Port, req.ClientID, req.Username, req.Password)
			s.luaExecutor.Execute(connectScript)
		}
		// Reconnect via Go MQTTManager
		if err := s.mqttManager.Reconnect(config.DefaultMQTTInstance, req.Host, req.Port, req.ClientID, req.Username, req.Password); err != nil {
			connError = err.Error()
		} else {
			connected = true
		}
		// Setup HA integration regardless of initial connect result —
		// subscriptions are tracked and re-applied on auto-reconnect
		if req.HAIntegration && s.luaExecutor != nil {
			s.luaExecutor.Execute("HAIntegration.setup(MQTT)")
		}
	} else if !req.Enabled && s.mqttManager != nil {
		// Disconnect
		s.mqttManager.Disconnect(config.DefaultMQTTInstance)
	}

	c.JSON(http.StatusOK, gin.H{
		"success":   true,
		"connected": connected,
		"error":     connError,
	})
}

// handleAPIMQTTClientTest tests connectivity to an MQTT broker without side effects
//
//	@Summary		Test MQTT broker connection
//	@Description	Connects to the given broker and disconnects, without touching the running client. An empty password falls back to the stored one. Connection failures are reported with HTTP 200 and success=false.
//	@Tags			mqtt
//	@Accept			json
//	@Produce		json
//	@Param			target	body		object	true	"Connection target: {host, port, username, password}"
//	@Success		200		{object}	map[string]interface{}	"success bool, error string (on failure)"
//	@Failure		400		{object}	web.APIError
//	@Failure		503		{object}	web.APIError
//	@Security		ApiKeyAuth
//	@Router			/api/mqtt/client/test [post]
func (s *Server) handleAPIMQTTClientTest(c *gin.Context) {
	var req struct {
		Host     string `json:"host"`
		Port     int    `json:"port"`
		Username string `json:"username"`
		Password string `json:"password"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if req.Port == 0 {
		req.Port = 1883
	}
	if req.Host == "" {
		req.Host = "localhost"
	}

	// Preserve existing password when not provided (form shows empty for security)
	if req.Password == "" {
		existing := config.GetMQTTClientConfig(s.serverDir)
		if existing != nil {
			req.Password = existing.Password
		}
	}

	if s.mqttManager == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "MQTT manager not available"})
		return
	}

	if err := s.mqttManager.TestConnection(req.Host, req.Port, req.Username, req.Password); err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"success": true})
}

// haDiscoveryRefresh republishes discovery from the runtime; -1 when the HA
// integration is off, -2 when its MQTT client cannot publish (MQTT:publish
// drops the Go "not connected" error, so refresh() would report nothing)
const haDiscoveryRefresh = `
	if not (HAStatePublisher and HAStatePublisher.isEnabled()) then return -1 end
	local m = HAStatePublisher.mqttInstance
	if not m or (m.isConnected and not m:isConnected()) then return -2 end
	HAStatePublisher.refresh()
	local n = 0
	for _ in pairs(ExposedObjects:list()) do n = n + 1 end
	return n`

// handleAPIHADiscoveryPublish republishes Home Assistant discovery
//
//	@Summary		Publish Home Assistant discovery
//	@Description	Republishes Home Assistant discovery and state of every exposed object (CLU objects are exposed automatically), as vCLU does on start, and clears stale discovery. Access control applies: hidden objects are removed, read-only ones get no command topic. Needs the Home Assistant integration of the MQTT client.
//	@Tags			mqtt
//	@Produce		json
//	@Success		200	{object}	map[string]interface{}	"success bool, published int (exposed objects)"
//	@Failure		409	{object}	web.APIError	"Home Assistant integration is off"
//	@Failure		503	{object}	web.APIError
//	@Security		ApiKeyAuth
//	@Router			/api/ha/discovery/publish [post]
func (s *Server) handleAPIHADiscoveryPublish(c *gin.Context) {
	if s.luaExecutor == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "Lua runtime not available"})
		return
	}
	out, err := s.luaExecutor.Execute(haDiscoveryRefresh)
	if err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": err.Error()})
		return
	}
	n, err := strconv.Atoi(strings.TrimSpace(out))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "unexpected result: " + out})
		return
	}
	switch n {
	case -1:
		c.JSON(http.StatusConflict, gin.H{"error": "Home Assistant integration is off: enable it in the MQTT client settings"})
		return
	case -2:
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "MQTT client not connected"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "published": n})
}
