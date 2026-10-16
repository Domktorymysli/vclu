package omparser

import (
	"encoding/json"
	"fmt"
	"strings"
)

// HADiscoveryConfig holds configuration for HA Discovery generation
type HADiscoveryConfig struct {
	// TopicPrefix for all MQTT topics (default: "grenton")
	TopicPrefix string

	// DiscoveryPrefix for HA discovery (default: "homeassistant")
	DiscoveryPrefix string

	// DeviceIdentifier for grouping entities (default: CLU ID)
	DeviceIdentifier string

	// DeviceName for display in HA (default: CLU Name or ID)
	DeviceName string
}

// DefaultHAConfig returns default HA Discovery configuration
func DefaultHAConfig() HADiscoveryConfig {
	return HADiscoveryConfig{
		TopicPrefix:     "vclu",
		DiscoveryPrefix: "homeassistant",
	}
}

// HADiscoveryMessage represents a single HA Discovery message
type HADiscoveryMessage struct {
	Topic      string                 `json:"topic"`
	Payload    map[string]interface{} `json:"payload"`
	Retain     bool                   `json:"retain"`
	ObjectPath string                 `json:"-"` // e.g., "CLU123.DOU456" for access control
}

// HADevice represents device info for HA Discovery
type HADevice struct {
	Identifiers  []string `json:"identifiers"`
	Name         string   `json:"name"`
	Manufacturer string   `json:"manufacturer"`
	Model        string   `json:"model,omitempty"`
	SwVersion    string   `json:"sw_version,omitempty"`
}

// GenerateHADiscovery generates Home Assistant MQTT Discovery messages
func GenerateHADiscovery(parsed *ParsedOM, config HADiscoveryConfig) []HADiscoveryMessage {
	// Apply defaults
	if config.TopicPrefix == "" {
		config.TopicPrefix = "vclu"
	}
	if config.DiscoveryPrefix == "" {
		config.DiscoveryPrefix = "homeassistant"
	}
	if config.DeviceIdentifier == "" {
		config.DeviceIdentifier = strings.ToLower(parsed.CLU.ID)
	}
	if config.DeviceName == "" {
		config.DeviceName = parsed.CLU.Name
		if config.DeviceName == "" {
			config.DeviceName = parsed.CLU.ID
		}
	}

	device := HADevice{
		Identifiers:  []string{config.DeviceIdentifier},
		Name:         config.DeviceName,
		Manufacturer: "VCLU",
		Model:        "vclu",
		SwVersion:    parsed.CLU.FwVersion,
	}

	var messages []HADiscoveryMessage

	for _, obj := range parsed.Objects {
		msg := generateDiscoveryForObject(parsed, obj, device, config)
		if msg != nil {
			messages = append(messages, *msg)
		}
	}

	return messages
}

// GenerateHADiscoveryJSON generates HA Discovery as JSON array (for debugging/export)
func GenerateHADiscoveryJSON(parsed *ParsedOM, config HADiscoveryConfig) (string, error) {
	messages := GenerateHADiscovery(parsed, config)
	data, err := json.MarshalIndent(messages, "", "  ")
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func generateDiscoveryForObject(parsed *ParsedOM, obj GrentonObject, device HADevice, config HADiscoveryConfig) *HADiscoveryMessage {
	cluID := parsed.CLU.ID
	objectPath := strings.ToUpper(cluID) + "." + strings.ToUpper(obj.ID)

	var msg *HADiscoveryMessage
	switch obj.Class {
	case ClassDOUT:
		msg = generateSwitch(obj, cluID, device, config)
	case ClassDIN:
		msg = generateBinarySensor(obj, cluID, device, config)
	case ClassDIMMER, ClassLEDChannel:
		msg = generateLight(obj, cluID, device, config)
	case ClassLEDRGB:
		msg = generateRGBLight(obj, cluID, device, config, parsed.HasWhite(obj))
	case ClassROLLER, ClassROLLER_SH:
		msg = generateCover(obj, cluID, device, config)
	case ClassAnalogIN:
		msg = generateSensor(obj, cluID, device, config)
	case ClassAnalogOUT:
		msg = generateNumber(obj, cluID, device, config)
	default:
		return nil
	}

	if msg != nil {
		msg.ObjectPath = objectPath
	}
	return msg
}

func getObjectName(obj GrentonObject) string {
	if obj.Name != "" {
		return obj.Name
	}
	return obj.ID
}

func getUniqueID(cluID string, obj GrentonObject) string {
	return fmt.Sprintf("vclu_%s_%s", strings.ToLower(cluID), strings.ToLower(obj.ID))
}

func getBaseTopic(config HADiscoveryConfig, component, cluID, objID string) string {
	return fmt.Sprintf("%s/%s/%s/%s", config.TopicPrefix, strings.ToLower(cluID), component, strings.ToLower(objID))
}

func generateSwitch(obj GrentonObject, cluID string, device HADevice, config HADiscoveryConfig) *HADiscoveryMessage {
	uniqueID := getUniqueID(cluID, obj)
	baseTopic := getBaseTopic(config, "switch", cluID, obj.ID)

	payload := map[string]interface{}{
		"name":          getObjectName(obj),
		"unique_id":     uniqueID,
		"object_id":     uniqueID,
		"command_topic": baseTopic + "/set",
		"state_topic":   baseTopic + "/state",
		"payload_on":    "ON",
		"payload_off":   "OFF",
		"state_on":      "ON",
		"state_off":     "OFF",
		"device":        device,
	}

	return &HADiscoveryMessage{
		Topic:   fmt.Sprintf("%s/switch/%s/config", config.DiscoveryPrefix, uniqueID),
		Payload: payload,
		Retain:  true,
	}
}

func generateBinarySensor(obj GrentonObject, cluID string, device HADevice, config HADiscoveryConfig) *HADiscoveryMessage {
	uniqueID := getUniqueID(cluID, obj)
	baseTopic := getBaseTopic(config, "binary_sensor", cluID, obj.ID)

	payload := map[string]interface{}{
		"name":        getObjectName(obj),
		"unique_id":   uniqueID,
		"object_id":   uniqueID,
		"state_topic": baseTopic + "/state",
		"payload_on":  "ON",
		"payload_off": "OFF",
		"device":      device,
	}

	return &HADiscoveryMessage{
		Topic:   fmt.Sprintf("%s/binary_sensor/%s/config", config.DiscoveryPrefix, uniqueID),
		Payload: payload,
		Retain:  true,
	}
}

func generateLight(obj GrentonObject, cluID string, device HADevice, config HADiscoveryConfig) *HADiscoveryMessage {
	uniqueID := getUniqueID(cluID, obj)
	baseTopic := getBaseTopic(config, "light", cluID, obj.ID)

	payload := map[string]interface{}{
		"name":                     getObjectName(obj),
		"unique_id":                uniqueID,
		"object_id":                uniqueID,
		"command_topic":            baseTopic + "/set",
		"state_topic":              baseTopic + "/state",
		"brightness_command_topic": baseTopic + "/brightness/set",
		"brightness_state_topic":   baseTopic + "/brightness",
		"brightness_scale":         100,
		"payload_on":               "ON",
		"payload_off":              "OFF",
		"state_on":                 "ON",
		"state_off":                "OFF",
		"device":                   device,
	}

	return &HADiscoveryMessage{
		Topic:   fmt.Sprintf("%s/light/%s/config", config.DiscoveryPrefix, uniqueID),
		Payload: payload,
		Retain:  true,
	}
}

func generateRGBLight(obj GrentonObject, cluID string, device HADevice, config HADiscoveryConfig, white bool) *HADiscoveryMessage {
	uniqueID := getUniqueID(cluID, obj)
	baseTopic := getBaseTopic(config, "light", cluID, obj.ID)

	payload := map[string]interface{}{
		"name":                  getObjectName(obj),
		"unique_id":             uniqueID,
		"object_id":             uniqueID,
		"schema":                "json",
		"command_topic":         baseTopic + "/set",
		"state_topic":           baseTopic + "/state",
		"brightness":            true,
		"brightness_scale":      100,
		"supported_color_modes": []string{"rgb", "hs"},
		"effect":                false,
		"device":                device,
	}

	if white {
		// HA sends the white channel in color.w (0..255)
		payload["supported_color_modes"] = []string{"rgbw"}
	}

	return &HADiscoveryMessage{
		Topic:   fmt.Sprintf("%s/light/%s/config", config.DiscoveryPrefix, uniqueID),
		Payload: payload,
		Retain:  true,
	}
}

func generateCover(obj GrentonObject, cluID string, device HADevice, config HADiscoveryConfig) *HADiscoveryMessage {
	uniqueID := getUniqueID(cluID, obj)
	baseTopic := getBaseTopic(config, "cover", cluID, obj.ID)

	payload := map[string]interface{}{
		"name":               getObjectName(obj),
		"unique_id":          uniqueID,
		"object_id":          uniqueID,
		"command_topic":      baseTopic + "/set",
		"state_topic":        baseTopic + "/state",
		"position_topic":     baseTopic + "/position",
		"set_position_topic": baseTopic + "/position/set",
		"payload_open":       "OPEN",
		"payload_close":      "CLOSE",
		"payload_stop":       "STOP",
		"state_open":         "open",
		"state_closed":       "closed",
		"position_open":      100,
		"position_closed":    0,
		"device":             device,
	}

	// ROLLER_SH (class 11) doesn't have position feedback
	if obj.Class == ClassROLLER_SH {
		delete(payload, "position_topic")
		delete(payload, "set_position_topic")
		delete(payload, "position_open")
		delete(payload, "position_closed")
	}

	return &HADiscoveryMessage{
		Topic:   fmt.Sprintf("%s/cover/%s/config", config.DiscoveryPrefix, uniqueID),
		Payload: payload,
		Retain:  true,
	}
}

func generateSensor(obj GrentonObject, cluID string, device HADevice, config HADiscoveryConfig) *HADiscoveryMessage {
	uniqueID := getUniqueID(cluID, obj)
	baseTopic := getBaseTopic(config, "sensor", cluID, obj.ID)

	payload := map[string]interface{}{
		"name":        getObjectName(obj),
		"unique_id":   uniqueID,
		"object_id":   uniqueID,
		"state_topic": baseTopic + "/state",
		"device":      device,
	}

	return &HADiscoveryMessage{
		Topic:   fmt.Sprintf("%s/sensor/%s/config", config.DiscoveryPrefix, uniqueID),
		Payload: payload,
		Retain:  true,
	}
}

func generateNumber(obj GrentonObject, cluID string, device HADevice, config HADiscoveryConfig) *HADiscoveryMessage {
	uniqueID := getUniqueID(cluID, obj)
	baseTopic := getBaseTopic(config, "number", cluID, obj.ID)

	payload := map[string]interface{}{
		"name":          getObjectName(obj),
		"unique_id":     uniqueID,
		"object_id":     uniqueID,
		"command_topic": baseTopic + "/set",
		"state_topic":   baseTopic + "/state",
		"min":           0,
		"max":           100,
		"step":          1,
		"device":        device,
	}

	return &HADiscoveryMessage{
		Topic:   fmt.Sprintf("%s/number/%s/config", config.DiscoveryPrefix, uniqueID),
		Payload: payload,
		Retain:  true,
	}
}

// HAStateMessage represents initial state message
type HAStateMessage struct {
	Topic      string
	Payload    string
	Retain     bool
	ObjectPath string // e.g., "CLU123.DOU456" for access control
}

// GenerateInitialStates generates initial state messages for all objects
func GenerateInitialStates(parsed *ParsedOM, config HADiscoveryConfig) []HAStateMessage {
	if config.TopicPrefix == "" {
		config.TopicPrefix = "vclu"
	}

	var states []HAStateMessage

	for _, obj := range parsed.Objects {
		objectPath := strings.ToUpper(parsed.CLU.ID) + "." + strings.ToUpper(obj.ID)

		switch obj.Class {
		case ClassDOUT:
			baseTopic := getBaseTopic(config, "switch", parsed.CLU.ID, obj.ID)
			states = append(states, HAStateMessage{
				Topic:      baseTopic + "/state",
				Payload:    "OFF",
				Retain:     true,
				ObjectPath: objectPath,
			})
		case ClassDIN:
			baseTopic := getBaseTopic(config, "binary_sensor", parsed.CLU.ID, obj.ID)
			states = append(states, HAStateMessage{
				Topic:      baseTopic + "/state",
				Payload:    "OFF",
				Retain:     true,
				ObjectPath: objectPath,
			})
		case ClassDIMMER, ClassLEDChannel:
			baseTopic := getBaseTopic(config, "light", parsed.CLU.ID, obj.ID)
			states = append(states, HAStateMessage{
				Topic:      baseTopic + "/state",
				Payload:    "OFF",
				Retain:     true,
				ObjectPath: objectPath,
			})
			states = append(states, HAStateMessage{
				Topic:      baseTopic + "/brightness",
				Payload:    "0",
				Retain:     true,
				ObjectPath: objectPath,
			})
		case ClassLEDRGB:
			baseTopic := getBaseTopic(config, "light", parsed.CLU.ID, obj.ID)
			// JSON schema state
			states = append(states, HAStateMessage{
				Topic:      baseTopic + "/state",
				Payload:    `{"state":"OFF","brightness":0,"color":{"r":255,"g":255,"b":255}}`,
				Retain:     true,
				ObjectPath: objectPath,
			})
		case ClassROLLER, ClassROLLER_SH:
			baseTopic := getBaseTopic(config, "cover", parsed.CLU.ID, obj.ID)
			states = append(states, HAStateMessage{
				Topic:      baseTopic + "/state",
				Payload:    "closed",
				Retain:     true,
				ObjectPath: objectPath,
			})
			if obj.Class == ClassROLLER {
				states = append(states, HAStateMessage{
					Topic:      baseTopic + "/position",
					Payload:    "0",
					Retain:     true,
					ObjectPath: objectPath,
				})
			}
		case ClassAnalogIN:
			baseTopic := getBaseTopic(config, "sensor", parsed.CLU.ID, obj.ID)
			states = append(states, HAStateMessage{
				Topic:      baseTopic + "/state",
				Payload:    "0",
				Retain:     true,
				ObjectPath: objectPath,
			})
		case ClassAnalogOUT:
			baseTopic := getBaseTopic(config, "number", parsed.CLU.ID, obj.ID)
			states = append(states, HAStateMessage{
				Topic:      baseTopic + "/state",
				Payload:    "0",
				Retain:     true,
				ObjectPath: objectPath,
			})
		}
	}

	return states
}
