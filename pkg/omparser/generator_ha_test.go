package omparser

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func TestGenerateHADiscovery(t *testing.T) {
	parsed, err := Parse(sampleOM)
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	config := DefaultHAConfig()
	messages := GenerateHADiscovery(parsed, config)

	// Should have 5 objects: 2 DOUT (switch), 2 DIN (binary_sensor), 1 AnalogIN (sensor)
	if len(messages) != 5 {
		t.Errorf("Expected 5 messages, got %d", len(messages))
	}

	// Check switch discovery
	var switchMsg *HADiscoveryMessage
	for i := range messages {
		if strings.Contains(messages[i].Topic, "/switch/") {
			switchMsg = &messages[i]
			break
		}
	}

	if switchMsg == nil {
		t.Fatal("No switch discovery message found")
	}

	// Verify topic format
	if !strings.HasPrefix(switchMsg.Topic, "homeassistant/switch/") {
		t.Errorf("Wrong topic prefix: %s", switchMsg.Topic)
	}
	if !strings.HasSuffix(switchMsg.Topic, "/config") {
		t.Errorf("Wrong topic suffix: %s", switchMsg.Topic)
	}

	// Verify payload
	if switchMsg.Payload["payload_on"] != "ON" {
		t.Error("Missing payload_on")
	}
	if switchMsg.Payload["payload_off"] != "OFF" {
		t.Error("Missing payload_off")
	}
	if !switchMsg.Retain {
		t.Error("Discovery messages should be retained")
	}

	// Check device info
	device, ok := switchMsg.Payload["device"].(HADevice)
	if !ok {
		t.Fatal("Missing device info")
	}
	if device.Manufacturer != "VCLU" {
		t.Errorf("Wrong manufacturer: %s", device.Manufacturer)
	}
}

func TestGenerateHADiscoveryJSON(t *testing.T) {
	parsed, err := Parse(sampleOM)
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	config := DefaultHAConfig()
	jsonStr, err := GenerateHADiscoveryJSON(parsed, config)
	if err != nil {
		t.Fatalf("GenerateHADiscoveryJSON failed: %v", err)
	}

	// Should be valid JSON
	var messages []HADiscoveryMessage
	if err := json.Unmarshal([]byte(jsonStr), &messages); err != nil {
		t.Fatalf("Invalid JSON: %v", err)
	}

	t.Logf("Generated %d discovery messages", len(messages))
}

func TestHADiscoveryTopics(t *testing.T) {
	parsed, err := Parse(sampleOM)
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	config := DefaultHAConfig()
	messages := GenerateHADiscovery(parsed, config)

	expectedComponents := map[string]bool{
		"switch":        false,
		"binary_sensor": false,
		"sensor":        false,
	}

	for _, msg := range messages {
		for comp := range expectedComponents {
			if strings.Contains(msg.Topic, "/"+comp+"/") {
				expectedComponents[comp] = true
			}
		}
	}

	for comp, found := range expectedComponents {
		if !found {
			t.Errorf("Missing component type: %s", comp)
		}
	}
}

func TestHADiscoveryRealFile(t *testing.T) {
	content, err := os.ReadFile("testdata/CLU212345678_om.lua")
	if err != nil {
		t.Fatalf("Failed to read test fixture: %v", err)
	}

	parsed, err := Parse(string(content))
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	config := HADiscoveryConfig{
		TopicPrefix:     "grenton",
		DiscoveryPrefix: "homeassistant",
		DeviceName:      "DOM20",
	}

	messages := GenerateHADiscovery(parsed, config)

	t.Logf("Generated %d discovery messages", len(messages))

	// Count by type
	counts := make(map[string]int)
	for _, msg := range messages {
		parts := strings.Split(msg.Topic, "/")
		if len(parts) >= 2 {
			counts[parts[1]]++
		}
	}

	for comp, count := range counts {
		t.Logf("  %s: %d", comp, count)
	}

	// Should have switches (DOUT)
	if counts["switch"] != 20 {
		t.Errorf("Expected 20 switches, got %d", counts["switch"])
	}

	// Should have binary_sensors (DIN)
	if counts["binary_sensor"] != 34 {
		t.Errorf("Expected 34 binary_sensors, got %d", counts["binary_sensor"])
	}

	// Should have sensors (AnalogIN)
	if counts["sensor"] != 5 {
		t.Errorf("Expected 5 sensors, got %d", counts["sensor"])
	}

	// Print first few messages for inspection
	t.Log("\n=== Sample Discovery Messages ===")
	for i, msg := range messages {
		if i >= 3 {
			break
		}
		jsonBytes, _ := json.MarshalIndent(msg, "", "  ")
		t.Logf("\n%s", string(jsonBytes))
	}
}

func TestHADiscoveryPayloadFields(t *testing.T) {
	parsed, err := Parse(sampleOM)
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	config := DefaultHAConfig()
	messages := GenerateHADiscovery(parsed, config)

	for _, msg := range messages {
		// All messages should have these fields
		if _, ok := msg.Payload["name"]; !ok {
			t.Errorf("Missing name in %s", msg.Topic)
		}
		if _, ok := msg.Payload["unique_id"]; !ok {
			t.Errorf("Missing unique_id in %s", msg.Topic)
		}
		if _, ok := msg.Payload["device"]; !ok {
			t.Errorf("Missing device in %s", msg.Topic)
		}
		if _, ok := msg.Payload["state_topic"]; !ok {
			t.Errorf("Missing state_topic in %s", msg.Topic)
		}

		// Controllable entities should have command_topic
		if strings.Contains(msg.Topic, "/switch/") ||
			strings.Contains(msg.Topic, "/light/") ||
			strings.Contains(msg.Topic, "/cover/") ||
			strings.Contains(msg.Topic, "/number/") {
			if _, ok := msg.Payload["command_topic"]; !ok {
				t.Errorf("Missing command_topic in controllable entity %s", msg.Topic)
			}
		}
	}
}
