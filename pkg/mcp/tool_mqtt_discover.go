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

	stateKey string // z2m payload key of the on/off state ("state", "state_l1", ...)
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
		brokerCfg := s.broker.GetConfig()
		br := BrokerResult{
			ID:      "embedded",
			URL:     fmt.Sprintf("tcp://localhost:%d", brokerCfg.Port),
			Devices: devices,
		}
		for i := range devices {
			devices[i].SuggestedLua = generateSnippet(devices[i], br)
		}
		result.Brokers = append(result.Brokers, br)
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
			br := BrokerResult{
				ID:      client.InstanceID,
				URL:     fmt.Sprintf("%s:%d", creds.URL, creds.Port),
				Devices: devices,
			}
			for i := range devices {
				devices[i].SuggestedLua = generateSnippet(devices[i], br)
			}
			result.Brokers = append(result.Brokers, br)
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
				Property string `json:"property,omitempty"`
				Features []struct {
					Name     string `json:"name,omitempty"`
					Type     string `json:"type,omitempty"`
					Property string `json:"property,omitempty"`
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
			// Features are payload keys: property differs from name on
			// multi-endpoint devices (state_l1, temperature_l2)
			for _, e := range d.Definition.Exposes {
				if k := z2mKey(e.Name, e.Property); k != "" {
					dev.Features = append(dev.Features, k)
				}
				for _, f := range e.Features {
					k := z2mKey(f.Name, f.Property)
					if k != "" {
						dev.Features = append(dev.Features, k)
					}
					if f.Name == "state" && dev.stateKey == "" {
						dev.stateKey = k
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

// z2mKey is the payload key of an expose: its property, or its name.
func z2mKey(name, property string) string {
	if property != "" {
		return property
	}
	return name
}

func inferZ2MDeviceType(exposes []struct {
	Type     string `json:"type"`
	Name     string `json:"name,omitempty"`
	Property string `json:"property,omitempty"`
	Features []struct {
		Name     string `json:"name,omitempty"`
		Type     string `json:"type,omitempty"`
		Property string `json:"property,omitempty"`
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
//
// Snippets follow the Tasmota pattern from docs/ai/examples.md: updateState on
// reports, onSwitchOn/Off sends commands (skipped for the device's own reports,
// so a wall switch does not echo a command back). A type without a reliable
// mapping gets no snippet rather than code that only looks right.

func generateSnippet(d DiscoveredDevice, broker BrokerResult) string {
	var body string
	switch d.Source {
	case "zigbee2mqtt":
		body = generateZigbee2MQTTSnippet(d)
	case "tasmota":
		body = generateTasmotaSnippet(d)
	case "esphome":
		body = generateESPHomeSnippet(d)
	}
	if body == "" {
		return ""
	}
	return fmt.Sprintf("-- Auto-generated: %s (%s)\n", luaComment(d.Name), luaComment(d.Model)) + snippetClient(broker) + body
}

// snippetClient picks the Lua client for the broker the device was found on.
func snippetClient(b BrokerResult) string {
	switch b.ID {
	case "", "MQTT":
		return "-- MQTT = default client (Integrations -> MQTT -> Client)\n"
	case "embedded":
		return fmt.Sprintf("-- Found on the embedded broker (%s): the default client MQTT must connect to it\n-- (Integrations -> MQTT -> Client: localhost)\n", luaComment(b.URL))
	default:
		return fmt.Sprintf("local MQTT = assert(MQTT.getInstance(%[1]s), \"MQTT client \" .. %[1]s .. \" not found\")\n", luaQuote(b.ID))
	}
}

// luaComment keeps s on one comment line.
func luaComment(s string) string {
	return strings.NewReplacer("\n", " ", "\r", " ").Replace(s)
}

// luaQuote returns s as a Lua string literal.
func luaQuote(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`, "\r", `\r`, "\x00", `\000`)
	return `"` + r.Replace(s) + `"`
}

// z2mSensorFeature maps a Zigbee2MQTT payload key to a vCLU object and expose type.
type z2mSensorFeature struct {
	key, class, exposeType, unit string
	activeWhen                   string // binary sensors: Lua expression on v that means 1
}

// HomeKit has no matching accessory for binary_sensor and illuminance (they
// would show up as switches), so those stay out of HomeKit.
var z2mSensorFeatures = []z2mSensorFeature{
	{key: "temperature", class: "VirtualNumber", exposeType: "temperature", unit: "°C"},
	{key: "humidity", class: "VirtualNumber", exposeType: "humidity", unit: "%"},
	{key: "illuminance_lux", class: "VirtualNumber", exposeType: "illuminance", unit: "lx"},
	{key: "illuminance", class: "VirtualNumber", exposeType: "illuminance", unit: "lx"}, // z2m 2.x: already lux
	{key: "occupancy", class: "VirtualSensor", exposeType: "motion", activeWhen: "v == true"},
	{key: "contact", class: "VirtualSensor", exposeType: "binary_sensor", activeWhen: "v == false"},
	{key: "water_leak", class: "VirtualSensor", exposeType: "binary_sensor", activeWhen: "v == true"},
}

func generateZigbee2MQTTSnippet(d DiscoveredDevice) string {
	stateTopic := d.Topics["state"]
	if stateTopic == "" {
		stateTopic = "zigbee2mqtt/" + d.Name
	}

	if d.Type == "switch" || d.Type == "light" {
		if d.stateKey != "" && d.stateKey != "state" {
			return "" // multi-endpoint: one object per endpoint, write it by hand
		}
		// A light is exposed as a switch: on/off only, brightness by hand
		return fmt.Sprintf(`local dev = VirtualSwitch:new(%[1]s)
local guard = { fromDevice = false }
MQTT:subscribe(%[2]s, function(topic, payload)
    local data = JSON.decode(payload)
    if type(data) == "table" and data.state then
        guard.fromDevice = true
        local ok, err = pcall(dev.updateState, dev, data.state == "ON" and 1 or 0)
        guard.fromDevice = false
        if not ok then error(err, 0) end
    end
end)
dev:onSwitchOn(function()  if not guard.fromDevice then MQTT:publish(%[3]s, JSON.encode({ state = "ON" })) end end)
dev:onSwitchOff(function() if not guard.fromDevice then MQTT:publish(%[3]s, JSON.encode({ state = "OFF" })) end end)
expose(dev, "switch", { name = %[1]s, path = %[4]s })`,
			luaQuote(d.Name), luaQuote(stateTopic), luaQuote(stateTopic+"/set"), luaQuote("z2m:"+d.ID))
	}
	if d.Type != "sensor" {
		return "" // cover, lock, fan, climate: write it by hand
	}

	has := make(map[string]bool, len(d.Features))
	for _, f := range d.Features {
		has[f] = true
	}
	if has["illuminance_lux"] {
		delete(has, "illuminance") // older z2m: raw value next to lux
	}
	var objs, reads []string
	for _, sf := range z2mSensorFeatures {
		if !has[sf.key] {
			continue
		}
		v := "dev_" + sf.key
		name := d.Name + " " + sf.key
		opts := fmt.Sprintf("{ name = %s, path = %s, readonly = true", luaQuote(name), luaQuote("z2m:"+d.ID+":"+sf.key))
		if sf.unit != "" {
			opts += ", unit = " + luaQuote(sf.unit)
		}
		if sf.exposeType == "binary_sensor" || sf.exposeType == "illuminance" {
			opts += ", homekit = false"
		}
		opts += " }"
		objs = append(objs, fmt.Sprintf("local %s = %s:new(%s)\nexpose(%s, %s, %s)", v, sf.class, luaQuote(name), v, luaQuote(sf.exposeType), opts))
		if sf.activeWhen != "" {
			reads = append(reads, fmt.Sprintf("    v = data[%s]; if v ~= nil then %s:updateState((%s) and 1 or 0) end", luaQuote(sf.key), v, sf.activeWhen))
		} else {
			reads = append(reads, fmt.Sprintf("    v = data[%s]; if type(v) == \"number\" then %s:setValue(v) end", luaQuote(sf.key), v))
		}
	}
	if len(objs) == 0 {
		return ""
	}
	return strings.Join(objs, "\n") + fmt.Sprintf(`
MQTT:subscribe(%s, function(topic, payload)
    local data = JSON.decode(payload)
    if type(data) ~= "table" then return end
    local v
%s
end)`, luaQuote(stateTopic), strings.Join(reads, "\n"))
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

	return fmt.Sprintf(`-- Tasmota IP: %[1]s
local dev = VirtualSwitch:new(%[2]s)
local guard = { fromDevice = false }
MQTT:subscribe(%[3]s, function(topic, payload)
    guard.fromDevice = true
    local ok, err = pcall(dev.updateState, dev, payload == "ON" and 1 or 0)
    guard.fromDevice = false
    if not ok then error(err, 0) end
end)
dev:onSwitchOn(function()  if not guard.fromDevice then MQTT:publish(%[4]s, "ON") end end)
dev:onSwitchOff(function() if not guard.fromDevice then MQTT:publish(%[4]s, "OFF") end end)
expose(dev, "switch", { name = %[2]s, path = %[5]s })`,
		luaComment(d.IP), luaQuote(d.Name), luaQuote(stateTopic), luaQuote(cmdTopic), luaQuote("tasmota:"+d.ID))
}

// generateESPHomeSnippet gives a template for one sensor: the discovery
// config does not say which sensors the node publishes.
func generateESPHomeSnippet(d DiscoveredDevice) string {
	base := strings.TrimSuffix(d.Topics["state"], "/sensor/+/state")
	if base == "" {
		base = "esphome/" + d.Name
	}
	return fmt.Sprintf(`-- ESPHome IP: %[1]s. SENSOR: the sensor id from %[2]s/sensor/<id>/state;
-- set the expose type and unit to match it
local SENSOR = "temperature"
local dev = VirtualNumber:new(%[3]s .. " " .. SENSOR)
MQTT:subscribe(%[4]s .. SENSOR .. "/state", function(topic, payload)
    local v = tonumber(payload)
    if v then dev:setValue(v) end
end)
expose(dev, "temperature", { name = %[3]s .. " " .. SENSOR, path = %[5]s .. SENSOR, readonly = true, unit = "°C" })`,
		luaComment(d.IP), luaComment(base), luaQuote(d.Name), luaQuote(base+"/sensor/"), luaQuote("esphome:"+d.ID+":"))
}
