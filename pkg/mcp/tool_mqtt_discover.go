package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	pahomqtt "github.com/eclipse/paho.mqtt.golang"
	mcplib "github.com/mark3labs/mcp-go/mcp"
)

// DiscoveredDevice represents a device found via MQTT discovery protocols.
type DiscoveredDevice struct {
	Source       string            `json:"source"`
	Confidence   float64           `json:"confidence"`
	ID           string            `json:"id"`
	Name         string            `json:"name"`
	Type         string            `json:"type"`
	Model        string            `json:"model,omitempty"`
	Vendor       string            `json:"vendor,omitempty"`
	IP           string            `json:"ip,omitempty"`
	Firmware     string            `json:"firmware,omitempty"`
	Features     []string          `json:"features,omitempty"`
	Topics       map[string]string `json:"topics,omitempty"`
	Exposed      bool              `json:"exposed"`
	SuggestedLua string            `json:"suggestedLua,omitempty"`
}

// BrokerResult holds discovery results from a single MQTT broker.
type BrokerResult struct {
	ID      string             `json:"id"`
	URL     string             `json:"url"`
	Devices []DiscoveredDevice `json:"devices"`
}

// MQTTDiscoveryResult is the top-level result returned by the tool.
type MQTTDiscoveryResult struct {
	Brokers []BrokerResult `json:"brokers"`
}

// mqttDiscoveryCache provides a short-lived cache to avoid repeated scanning.
type mqttDiscoveryCache struct {
	mu       sync.RWMutex
	result   *MQTTDiscoveryResult
	cachedAt time.Time
	ttl      time.Duration
}

func (c *mqttDiscoveryCache) get() *MQTTDiscoveryResult {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.result != nil && time.Since(c.cachedAt) < c.ttl {
		return c.result
	}
	return nil
}

func (c *mqttDiscoveryCache) set(r *MQTTDiscoveryResult) {
	c.mu.Lock()
	c.result, c.cachedAt = r, time.Now()
	c.mu.Unlock()
}

// Discovery topic patterns used for both embedded and external brokers.
var discoveryTopics = []string{
	"zigbee2mqtt/bridge/devices",
	"tasmota/discovery/+/config",
	"esphome/discover/#",
	"shellies/+/info",
	"homeassistant/+/+/config",
}

func (s *MCPServer) registerMQTTDiscoverTool() {
	s.addTool("vclu_mqtt_discover",
		"Discover smart home devices on MQTT brokers (Zigbee2MQTT, ESPHome, Tasmota, Shelly, HA Discovery)",
		s.handleMQTTDiscover,
		map[string]any{
			"source": map[string]any{
				"type":        "string",
				"description": "Filter: 'broker' (embedded only), 'all' (broker + external clients), or specific client instance ID. Default: 'all'",
			},
		}, nil)
}

func (s *MCPServer) handleMQTTDiscover(ctx context.Context, req mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
	start := time.Now()
	if err := s.requireDevMode(ctx); err != nil {
		return err, nil
	}

	// Check cache
	if cached := s.mqttCache.get(); cached != nil {
		s.recordActivity("vclu_mqtt_discover", "cached", true, start, "", ctx)
		return toolJSON(cached)
	}

	source := "all"
	if v, ok := req.GetArguments()["source"].(string); ok && v != "" {
		source = v
	}

	result := &MQTTDiscoveryResult{}

	// --- Embedded broker ---
	if (source == "all" || source == "broker") && s.broker != nil && s.broker.IsRunning() {
		devices := s.discoverFromBroker()
		s.markExposedDevices(ctx, devices)
		for i := range devices {
			devices[i].SuggestedLua = generateSnippet(devices[i])
		}
		brokerCfg := s.broker.GetConfig()
		result.Brokers = append(result.Brokers, BrokerResult{
			ID:      "embedded",
			URL:     fmt.Sprintf("tcp://localhost:%d", brokerCfg.Port),
			Devices: devices,
		})
	}

	// --- External MQTT clients ---
	if source != "broker" && s.mqttCreds != nil {
		stats := s.mqttCreds.Stats()
		for _, client := range stats.Clients {
			if !client.Connected {
				continue
			}
			// If a specific instance ID is requested, filter
			if source != "all" && client.InstanceID != source {
				continue
			}
			creds := s.mqttCreds.GetClientCredentials(client.InstanceID)
			if creds == nil {
				continue
			}
			devices := s.discoverFromExternalBroker(creds.URL, creds.Port, creds.Username, creds.Password)
			s.markExposedDevices(ctx, devices)
			for i := range devices {
				devices[i].SuggestedLua = generateSnippet(devices[i])
			}
			result.Brokers = append(result.Brokers, BrokerResult{
				ID:      client.InstanceID,
				URL:     fmt.Sprintf("%s:%d", creds.URL, creds.Port),
				Devices: devices,
			})
		}
	}

	s.mqttCache.set(result)
	s.recordActivity("vclu_mqtt_discover", source, true, start, "", ctx)
	return toolJSON(result)
}

// discoverFromBroker reads retained messages from the embedded mochi-mqtt broker.
func (s *MCPServer) discoverFromBroker() []DiscoveredDevice {
	var devices []DiscoveredDevice

	// Zigbee2MQTT — single retained topic with full device list
	for _, msg := range s.broker.GetRetainedByFilter("zigbee2mqtt/bridge/devices") {
		devices = append(devices, parseZigbee2MQTTDevices(string(msg.Payload))...)
	}

	// Tasmota — one config message per device
	for _, msg := range s.broker.GetRetainedByFilter("tasmota/discovery/+/config") {
		if d := parseTasmotaDevice(msg.Topic, string(msg.Payload)); d != nil {
			devices = append(devices, *d)
		}
	}

	// ESPHome — one discovery message per device
	for _, msg := range s.broker.GetRetainedByFilter("esphome/discover/#") {
		if d := parseESPHomeDevice(msg.Topic, string(msg.Payload)); d != nil {
			devices = append(devices, *d)
		}
	}

	// Shelly — info endpoint per device
	for _, msg := range s.broker.GetRetainedByFilter("shellies/+/info") {
		if d := parseShellyDevice(msg.Topic, string(msg.Payload)); d != nil {
			devices = append(devices, *d)
		}
	}

	// HA Discovery — generic, any device type
	for _, msg := range s.broker.GetRetainedByFilter("homeassistant/+/+/config") {
		if d := parseHADiscoveryDevice(msg.Topic, string(msg.Payload)); d != nil {
			devices = append(devices, *d)
		}
	}

	return devices
}

// discoverFromExternalBroker creates a temporary Paho client, subscribes to
// discovery topics, collects retained messages for 2s, then disconnects.
func (s *MCPServer) discoverFromExternalBroker(url string, port int, username, password string) []DiscoveredDevice {
	brokerURL := url
	if !strings.HasPrefix(brokerURL, "tcp://") && !strings.HasPrefix(brokerURL, "ssl://") &&
		!strings.HasPrefix(brokerURL, "ws://") && !strings.HasPrefix(brokerURL, "wss://") {
		brokerURL = "tcp://" + brokerURL
	}
	brokerAddr := fmt.Sprintf("%s:%d", brokerURL, port)

	opts := pahomqtt.NewClientOptions().
		AddBroker(brokerAddr).
		SetClientID(fmt.Sprintf("vclu-discover-%d", time.Now().UnixMilli())).
		SetCleanSession(true).
		SetAutoReconnect(false)

	if username != "" {
		opts.SetUsername(username)
		opts.SetPassword(password)
	}

	type retainedMsg struct {
		topic   string
		payload string
	}
	var mu sync.Mutex
	var messages []retainedMsg

	opts.SetDefaultPublishHandler(func(c pahomqtt.Client, m pahomqtt.Message) {
		if m.Retained() {
			mu.Lock()
			messages = append(messages, retainedMsg{topic: m.Topic(), payload: string(m.Payload())})
			mu.Unlock()
		}
	})

	client := pahomqtt.NewClient(opts)
	token := client.Connect()
	if !token.WaitTimeout(5*time.Second) || token.Error() != nil {
		return nil
	}
	defer client.Disconnect(500)

	// Subscribe to all discovery topics
	topicMap := make(map[string]byte, len(discoveryTopics))
	for _, t := range discoveryTopics {
		topicMap[t] = 0
	}
	subToken := client.SubscribeMultiple(topicMap, nil)
	subToken.WaitTimeout(5 * time.Second)

	// Wait for retained messages
	time.Sleep(2 * time.Second)

	// Parse collected messages
	mu.Lock()
	defer mu.Unlock()

	var devices []DiscoveredDevice
	for _, msg := range messages {
		// Try each parser based on topic prefix
		switch {
		case msg.topic == "zigbee2mqtt/bridge/devices":
			devices = append(devices, parseZigbee2MQTTDevices(msg.payload)...)
		case strings.HasPrefix(msg.topic, "tasmota/discovery/"):
			if d := parseTasmotaDevice(msg.topic, msg.payload); d != nil {
				devices = append(devices, *d)
			}
		case strings.HasPrefix(msg.topic, "esphome/discover/"):
			if d := parseESPHomeDevice(msg.topic, msg.payload); d != nil {
				devices = append(devices, *d)
			}
		case strings.HasPrefix(msg.topic, "shellies/"):
			if d := parseShellyDevice(msg.topic, msg.payload); d != nil {
				devices = append(devices, *d)
			}
		case strings.HasPrefix(msg.topic, "homeassistant/"):
			if d := parseHADiscoveryDevice(msg.topic, msg.payload); d != nil {
				devices = append(devices, *d)
			}
		}
	}

	return devices
}

// --- Parsers ---

func parseZigbee2MQTTDevices(payload string) []DiscoveredDevice {
	var z2mDevices []struct {
		IEEEAddress  string `json:"ieee_address"`
		FriendlyName string `json:"friendly_name"`
		Type         string `json:"type"`
		ModelID      string `json:"model_id"`
		Vendor       string `json:"vendor"`
		Definition   *struct {
			Model       string `json:"model"`
			Description string `json:"description"`
			Exposes     []struct {
				Type     string `json:"type"`
				Name     string `json:"name,omitempty"`
				Features []struct {
					Name string `json:"name,omitempty"`
					Type string `json:"type,omitempty"`
				} `json:"features,omitempty"`
			} `json:"exposes"`
		} `json:"definition"`
	}
	if err := json.Unmarshal([]byte(payload), &z2mDevices); err != nil {
		return nil
	}

	var result []DiscoveredDevice
	for _, d := range z2mDevices {
		if d.Type == "Coordinator" {
			continue
		}

		dev := DiscoveredDevice{
			Source:     "zigbee2mqtt",
			Confidence: 1.0,
			ID:         d.IEEEAddress,
			Name:       d.FriendlyName,
			Vendor:     d.Vendor,
		}

		if d.Definition != nil {
			dev.Model = d.Definition.Model
			for _, e := range d.Definition.Exposes {
				if e.Name != "" {
					dev.Features = append(dev.Features, e.Name)
				}
				for _, f := range e.Features {
					if f.Name != "" {
						dev.Features = append(dev.Features, f.Name)
					}
				}
			}
			dev.Type = inferZ2MDeviceType(d.Definition.Exposes)
		}

		if dev.Type == "" {
			dev.Type = "sensor"
		}

		dev.Topics = map[string]string{
			"state": "zigbee2mqtt/" + d.FriendlyName,
		}
		result = append(result, dev)
	}
	return result
}

func inferZ2MDeviceType(exposes []struct {
	Type     string `json:"type"`
	Name     string `json:"name,omitempty"`
	Features []struct {
		Name string `json:"name,omitempty"`
		Type string `json:"type,omitempty"`
	} `json:"features,omitempty"`
}) string {
	for _, e := range exposes {
		switch e.Type {
		case "light":
			return "light"
		case "switch":
			return "switch"
		case "cover":
			return "cover"
		case "lock":
			return "lock"
		case "fan":
			return "fan"
		case "climate":
			return "climate"
		}
	}
	return "sensor"
}

func parseTasmotaDevice(topic, payload string) *DiscoveredDevice {
	if payload == "" {
		return nil
	}
	var cfg struct {
		DeviceName string `json:"dn"`
		Hostname   string `json:"hn"`
		IP         string `json:"ip"`
		Module     string `json:"md"`
		Firmware   string `json:"sw"`
		Topic      string `json:"t"`
		Relays     int    `json:"rl"`
	}
	if err := json.Unmarshal([]byte(payload), &cfg); err != nil {
		return nil
	}
	if cfg.Hostname == "" && cfg.Topic == "" {
		return nil
	}

	id := cfg.Hostname
	if id == "" {
		id = cfg.Topic
	}
	name := cfg.DeviceName
	if name == "" {
		name = id
	}

	cmdTopic := cfg.Topic
	if cmdTopic == "" {
		cmdTopic = id
	}

	return &DiscoveredDevice{
		Source:     "tasmota",
		Confidence: 0.9,
		ID:         id,
		Name:       name,
		Type:       "switch",
		Model:      cfg.Module,
		IP:         cfg.IP,
		Firmware:   cfg.Firmware,
		Features:   []string{"power"},
		Topics: map[string]string{
			"state":   "stat/" + cmdTopic + "/POWER",
			"command": "cmnd/" + cmdTopic + "/POWER",
		},
	}
}

func parseESPHomeDevice(topic, payload string) *DiscoveredDevice {
	if payload == "" {
		return nil
	}
	var cfg struct {
		Name     string `json:"name"`
		Platform string `json:"platform"`
		Network  struct {
			IP string `json:"ip"`
		} `json:"network"`
		Version    string   `json:"esphome_version"`
		Components []string `json:"components,omitempty"`
	}
	if err := json.Unmarshal([]byte(payload), &cfg); err != nil {
		return nil
	}
	if cfg.Name == "" {
		return nil
	}

	parts := strings.Split(topic, "/")
	id := cfg.Name
	if len(parts) >= 3 {
		id = parts[2]
	}

	return &DiscoveredDevice{
		Source:     "esphome",
		Confidence: 0.95,
		ID:         id,
		Name:       cfg.Name,
		Type:       "sensor",
		IP:         cfg.Network.IP,
		Firmware:   cfg.Version,
		Model:      cfg.Platform,
		Features:   cfg.Components,
		Topics: map[string]string{
			"state": "esphome/" + cfg.Name + "/sensor/+/state",
		},
	}
}

func parseShellyDevice(topic, payload string) *DiscoveredDevice {
	if payload == "" {
		return nil
	}
	parts := strings.Split(topic, "/")
	if len(parts) < 2 {
		return nil
	}
	shellyID := parts[1]

	var info struct {
		Type string `json:"type"`
		ID   string `json:"id"`
		FW   string `json:"fw"`
		WiFi struct {
			IP string `json:"ip"`
		} `json:"wifi_sta"`
	}
	if err := json.Unmarshal([]byte(payload), &info); err != nil {
		return nil
	}

	devType := mapShellyType(info.Type)

	return &DiscoveredDevice{
		Source:     "shelly",
		Confidence: 0.8,
		ID:         shellyID,
		Name:       shellyID,
		Type:       devType,
		IP:         info.WiFi.IP,
		Firmware:   info.FW,
	}
}

func mapShellyType(shellyType string) string {
	t := strings.ToLower(shellyType)
	switch {
	case strings.Contains(t, "dimmer"):
		return "light"
	case strings.Contains(t, "roller"), strings.Contains(t, "shutter"):
		return "cover"
	case strings.Contains(t, "rgbw"), strings.Contains(t, "bulb"), strings.Contains(t, "duo"):
		return "light"
	case strings.Contains(t, "ht"), strings.Contains(t, "flood"), strings.Contains(t, "gas"),
		strings.Contains(t, "motion"), strings.Contains(t, "door"):
		return "sensor"
	default:
		return "switch"
	}
}

func parseHADiscoveryDevice(topic, payload string) *DiscoveredDevice {
	if payload == "" {
		return nil
	}
	// Topic format: homeassistant/<component>/<object_id>/config
	parts := strings.Split(topic, "/")
	if len(parts) < 4 {
		return nil
	}
	component := parts[1]
	objectID := parts[2]

	var cfg struct {
		Name     string `json:"name"`
		UniqueID string `json:"unique_id"`
		Device   *struct {
			Name         string `json:"name"`
			Model        string `json:"model"`
			Manufacturer string `json:"manufacturer"`
			SWVersion    string `json:"sw_version"`
		} `json:"device"`
		StateTopic   string `json:"state_topic"`
		CommandTopic string `json:"command_topic,omitempty"`
	}
	if err := json.Unmarshal([]byte(payload), &cfg); err != nil {
		return nil
	}

	id := cfg.UniqueID
	if id == "" {
		id = objectID
	}
	name := cfg.Name
	if name == "" {
		name = objectID
	}

	dev := &DiscoveredDevice{
		Source:     "homeassistant",
		Confidence: 0.5,
		ID:         id,
		Name:       name,
		Type:       mapHAComponent(component),
	}

	if cfg.Device != nil {
		dev.Model = cfg.Device.Model
		dev.Vendor = cfg.Device.Manufacturer
		dev.Firmware = cfg.Device.SWVersion
	}

	dev.Topics = make(map[string]string)
	if cfg.StateTopic != "" {
		dev.Topics["state"] = cfg.StateTopic
	}
	if cfg.CommandTopic != "" {
		dev.Topics["command"] = cfg.CommandTopic
	}

	return dev
}

func mapHAComponent(component string) string {
	switch component {
	case "light":
		return "light"
	case "switch":
		return "switch"
	case "cover":
		return "cover"
	case "fan":
		return "fan"
	case "climate":
		return "climate"
	case "lock":
		return "lock"
	case "binary_sensor", "sensor":
		return "sensor"
	default:
		return component
	}
}

// markExposedDevices checks which discovered devices already have expose() in vCLU.
func (s *MCPServer) markExposedDevices(ctx context.Context, devices []DiscoveredDevice) {
	if len(devices) == 0 {
		return
	}

	exposed, err := s.devices.ListDevices(ctx, "{}")
	if err != nil {
		return
	}

	// Build lookup set of known names and paths
	known := make(map[string]bool, len(exposed)*2)
	for _, d := range exposed {
		known[strings.ToLower(d.Name)] = true
		known[strings.ToLower(d.Path)] = true
	}

	for i := range devices {
		if known[strings.ToLower(devices[i].Name)] || known[strings.ToLower(devices[i].ID)] {
			devices[i].Exposed = true
		}
		// Check if path contains device ID (e.g., "z2m:0x001234")
		for _, d := range exposed {
			if strings.Contains(strings.ToLower(d.Path), strings.ToLower(devices[i].ID)) {
				devices[i].Exposed = true
				break
			}
		}
	}
}

// --- Snippet generators ---

func generateSnippet(d DiscoveredDevice) string {
	switch d.Source {
	case "zigbee2mqtt":
		return generateZigbee2MQTTSnippet(d)
	case "tasmota":
		return generateTasmotaSnippet(d)
	case "esphome":
		return generateESPHomeSnippet(d)
	default:
		return ""
	}
}

func generateZigbee2MQTTSnippet(d DiscoveredDevice) string {
	stateTopic := d.Topics["state"]
	if stateTopic == "" {
		stateTopic = "zigbee2mqtt/" + d.Name
	}

	if d.Type == "switch" || d.Type == "light" {
		return fmt.Sprintf(`-- Auto-generated: %s (%s)
local dev = VirtualSwitch()
MQTT_CLIENT:subscribe("%s", function(topic, payload)
    local data = JSON.parse(payload)
    if data and data.state then
        if data.state == "ON" then dev:switchOn()
        else dev:switchOff() end
    end
end)
dev:onChange(function()
    MQTT_CLIENT:publish("%s/set", JSON.stringify({state = dev:getValue() == 1 and "ON" or "OFF"}))
end)
expose(dev, "%s", { name = "%s", path = "z2m:%s" })`,
			d.Name, d.Model, stateTopic, stateTopic,
			d.Type, d.Name, d.ID)
	}

	// sensor (read-only)
	return fmt.Sprintf(`-- Auto-generated: %s (%s)
local dev = VirtualSensor()
MQTT_CLIENT:subscribe("%s", function(topic, payload)
    local data = JSON.parse(payload)
    if data then dev:setValue(data) end
end)
expose(dev, "sensor", { name = "%s", path = "z2m:%s" })`,
		d.Name, d.Model, stateTopic, d.Name, d.ID)
}

func generateTasmotaSnippet(d DiscoveredDevice) string {
	stateTopic := d.Topics["state"]
	cmdTopic := d.Topics["command"]
	if stateTopic == "" {
		stateTopic = "stat/" + d.ID + "/POWER"
	}
	if cmdTopic == "" {
		cmdTopic = "cmnd/" + d.ID + "/POWER"
	}

	return fmt.Sprintf(`-- Auto-generated: %s (%s, IP: %s)
local dev = VirtualSwitch()
MQTT_CLIENT:subscribe("%s", function(topic, payload)
    if payload == "ON" then dev:switchOn()
    else dev:switchOff() end
end)
dev:onChange(function()
    MQTT_CLIENT:publish("%s", dev:getValue() == 1 and "ON" or "OFF")
end)
expose(dev, "switch", { name = "%s", path = "tasmota:%s" })`,
		d.Name, d.Model, d.IP, stateTopic, cmdTopic,
		d.Name, d.ID)
}

func generateESPHomeSnippet(d DiscoveredDevice) string {
	return fmt.Sprintf(`-- Auto-generated: %s (ESPHome, IP: %s)
local dev = VirtualSensor()
MQTT_CLIENT:subscribe("esphome/%s/sensor/+/state", function(topic, payload)
    dev:setValue(tonumber(payload) or payload)
end)
expose(dev, "sensor", { name = "%s", path = "esphome:%s" })`,
		d.Name, d.IP, d.ID, d.Name, d.ID)
}
