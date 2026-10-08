package lua

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func init() {
	// Change to project root for tests
	// Find the project root by looking for go.mod
	dir, _ := os.Getwd()
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			os.Chdir(dir)
			break
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
}

func TestLuaEngineBootstrap(t *testing.T) {
	// Test that bootstrap_go.lua loads successfully
	engine := NewLuaEngine()
	defer engine.Close()

	// Test checkAlive
	result, err := engine.Execute("checkAlive()")
	if err != nil {
		t.Fatalf("checkAlive failed: %v", err)
	}
	if result != "true" {
		t.Errorf("checkAlive returned %q, expected 'true'", result)
	}

	// Test SYSTEM exists
	result, err = engine.Execute("SYSTEM ~= nil")
	if err != nil {
		t.Fatalf("SYSTEM check failed: %v", err)
	}
	if result != "true" {
		t.Errorf("SYSTEM is nil, expected not nil")
	}

	// Test GATE exists
	result, err = engine.Execute("GATE ~= nil")
	if err != nil {
		t.Fatalf("GATE check failed: %v", err)
	}
	if result != "true" {
		t.Errorf("GATE is nil, expected not nil")
	}

	// Test OBJECT exists
	result, err = engine.Execute("OBJECT ~= nil")
	if err != nil {
		t.Fatalf("OBJECT check failed: %v", err)
	}
	if result != "true" {
		t.Errorf("OBJECT is nil, expected not nil")
	}

	t.Log("Bootstrap loaded successfully!")
}

func TestLoadCLUData(t *testing.T) {
	engine := NewLuaEngine()
	defer engine.Close()

	// Load CLU data from the test directory
	dataPath := "./cmd/vclu/data/clu/a"
	err := engine.LoadCLUData(dataPath)
	if err != nil {
		t.Fatalf("LoadCLUData failed: %v", err)
	}

	// Check that a CLU was created (name depends on om.lua content)
	// We just verify the load succeeded and SYSTEM is still valid
	result, err := engine.Execute("SYSTEM ~= nil")
	if err != nil {
		t.Fatalf("SYSTEM check failed: %v", err)
	}
	if result != "true" {
		t.Errorf("SYSTEM is nil after loading CLU data")
	}

	t.Log("CLU data loaded successfully!")
}

func TestCLUInit(t *testing.T) {
	engine := NewLuaEngine()
	defer engine.Close()

	// Load CLU data
	dataPath := "./cmd/vclu/data/clu/a"
	err := engine.LoadCLUData(dataPath)
	if err != nil {
		t.Fatalf("LoadCLUData failed: %v", err)
	}

	// Initialize CLU
	err = engine.Init()
	if err != nil {
		t.Fatalf("Init failed: %v", err)
	}

	// After init, OnInit should have been called
	// This sets various properties on CLU520000006
	t.Log("CLU initialized successfully!")
}

// ==================== CallLuaFunction Tests ====================

func TestCallLuaFunction(t *testing.T) {
	engine := NewLuaEngine()
	defer engine.Close()

	// Setup: define test functions in Lua
	_, err := engine.Execute(`
		TestModule = {}
		function TestModule.add(a, b)
			return tostring(tonumber(a) + tonumber(b))
		end
		function TestModule.echo(msg)
			return msg
		end
		function TestModule.noReturn()
			-- does nothing
		end
	`)
	if err != nil {
		t.Fatalf("Setup failed: %v", err)
	}

	// Test 1: Call dotted path function with args
	result, err := engine.CallLuaFunction("TestModule.add", "3", "7")
	if err != nil {
		t.Fatalf("CallLuaFunction failed: %v", err)
	}
	if result != "10" {
		t.Errorf("Expected '10', got '%s'", result)
	}

	// Test 2: String arg passed through
	result, err = engine.CallLuaFunction("TestModule.echo", "hello world")
	if err != nil {
		t.Fatalf("echo failed: %v", err)
	}
	if result != "hello world" {
		t.Errorf("Expected 'hello world', got '%s'", result)
	}

	// Test 3: Non-existent root table returns ("", nil) — not an error
	result, err = engine.CallLuaFunction("NonExistent.foo", "x")
	if err != nil {
		t.Errorf("Expected nil error for missing root table, got: %v", err)
	}
	if result != "" {
		t.Errorf("Expected empty string for missing root table, got '%s'", result)
	}

	// Test 4: Existing table but missing function → error
	_, err = engine.CallLuaFunction("TestModule.missing")
	if err == nil {
		t.Error("Expected error for missing function, got nil")
	}

	// Test 5: Function with no return value
	result, err = engine.CallLuaFunction("TestModule.noReturn")
	if err != nil {
		t.Fatalf("noReturn failed: %v", err)
	}
	if result != "nil" {
		t.Errorf("Expected 'nil', got '%s'", result)
	}
}

func TestExecuteCached(t *testing.T) {
	engine := NewLuaEngine()
	defer engine.Close()

	// Setup state
	_, err := engine.Execute(`_testCounter = 0`)
	if err != nil {
		t.Fatalf("Setup failed: %v", err)
	}

	script := "_testCounter = _testCounter + 1; return _testCounter"

	// First call compiles and executes
	result, err := engine.ExecuteCached(script)
	if err != nil {
		t.Fatalf("First call failed: %v", err)
	}
	if result != "1" {
		t.Errorf("Expected '1', got '%s'", result)
	}

	// Second call uses cached function
	result, err = engine.ExecuteCached(script)
	if err != nil {
		t.Fatalf("Second call failed: %v", err)
	}
	if result != "2" {
		t.Errorf("Expected '2', got '%s'", result)
	}

	// Expression style (auto-wraps with return)
	result, err = engine.ExecuteCached("1 + 2")
	if err != nil {
		t.Fatalf("Expr failed: %v", err)
	}
	if result != "3" {
		t.Errorf("Expected '3', got '%s'", result)
	}

	// Method call on SYSTEM
	result, err = engine.ExecuteCached("if SYSTEM then return 'ok' else return 'no' end")
	if err != nil {
		t.Fatalf("SYSTEM check failed: %v", err)
	}
	if result != "ok" {
		t.Errorf("Expected 'ok', got '%s'", result)
	}
}

// ==================== MQTT Module Tests ====================

func TestMqttModule(t *testing.T) {
	engine := NewLuaEngine()
	defer engine.Close()

	// Test 1: MQTT module exists
	result, err := engine.Execute("type(MQTT)")
	if err != nil {
		t.Fatalf("Failed to check MQTT type: %v", err)
	}
	if result != "table" {
		t.Errorf("Expected MQTT to be table, got %s", result)
	}

	// Test 2: Named methods with chaining
	_, err = engine.Execute(`
		MQTT:setUrl("tcp://broker.local")
			:setPort(8883)
			:setClientId("vclu_test")
			:setTopicPrefix("grenton")
			:setQoS(1)
	`)
	if err != nil {
		t.Fatalf("Failed to use named methods: %v", err)
	}

	result, _ = engine.Execute("MQTT:getUrl()")
	if result != "tcp://broker.local" {
		t.Errorf("Expected URL 'tcp://broker.local', got '%s'", result)
	}

	// Test 4: Publish (without Go backend, should queue)
	_, err = engine.Execute(`
		MQTT:publish("test/topic", "hello")
		MQTT:publish("switch/dou123/state", "ON", 1, true)
	`)
	if err != nil {
		t.Fatalf("Failed to publish: %v", err)
	}

	result, _ = engine.Execute("#MQTT:getOutbox()")
	if result != "2" {
		t.Errorf("Expected 2 messages in outbox, got %s", result)
	}

	// Test 5: Subscribe and simulate message
	_, err = engine.Execute(`
		receivedTopic = nil
		receivedPayload = nil
		MQTT:subscribe("+/+/set", function(topic, payload)
			receivedTopic = topic
			receivedPayload = payload
		end)
		MQTT:simulateMessage("grenton/switch/dou123/set", "ON")
	`)
	if err != nil {
		t.Fatalf("Failed to subscribe/simulate: %v", err)
	}

	result, _ = engine.Execute("receivedPayload")
	if result != "ON" {
		t.Errorf("Expected received payload 'ON', got '%s'", result)
	}

	t.Log("MQTT module test passed!")
}

func TestMqttMultiInstance(t *testing.T) {
	engine := NewLuaEngine()
	defer engine.Close()

	// Create multiple MQTT instances
	_, err := engine.Execute(`
		-- Local broker (HA)
		mqtt_ha = MQTT:new("ha_broker")
		mqtt_ha:setUrl("tcp://192.168.1.100")
		       :setPort(1883)
		       :setTopicPrefix("homeassistant")

		-- Cloud broker
		mqtt_cloud = MQTT:new("cloud_broker")
		mqtt_cloud:setUrl("ssl://cloud.example.com")
		          :setPort(8883)
		          :setCredentials("user", "token")
		          :setTopicPrefix("grenton/remote")

		-- Connect both
		mqtt_ha:connect()
		mqtt_cloud:connect()
	`)
	if err != nil {
		t.Fatalf("Failed to create instances: %v", err)
	}

	// Verify different IDs
	result, _ := engine.Execute("mqtt_ha:getId()")
	if result != "ha_broker" {
		t.Errorf("Expected ID 'ha_broker', got '%s'", result)
	}

	result, _ = engine.Execute("mqtt_cloud:getId()")
	if result != "cloud_broker" {
		t.Errorf("Expected ID 'cloud_broker', got '%s'", result)
	}

	// Verify different URLs
	result, _ = engine.Execute("mqtt_ha:getUrl()")
	if result != "tcp://192.168.1.100" {
		t.Errorf("Expected HA URL, got '%s'", result)
	}

	result, _ = engine.Execute("mqtt_cloud:getUrl()")
	if result != "ssl://cloud.example.com" {
		t.Errorf("Expected cloud URL, got '%s'", result)
	}

	// Publish to different brokers
	_, err = engine.Execute(`
		mqtt_ha:publish("switch/light1/state", "ON")
		mqtt_cloud:publish("device/status", "online")
	`)
	if err != nil {
		t.Fatalf("Failed to publish: %v", err)
	}

	// Check outbox has correct instanceIds
	result, _ = engine.Execute("mqtt_ha:getOutbox()[1].instanceId")
	if result != "ha_broker" {
		t.Errorf("Expected instanceId 'ha_broker' in outbox, got '%s'", result)
	}

	result, _ = engine.Execute("mqtt_cloud:getOutbox()[1].instanceId")
	if result != "cloud_broker" {
		t.Errorf("Expected instanceId 'cloud_broker' in outbox, got '%s'", result)
	}

	// Get all instance IDs
	result, _ = engine.Execute("#MQTT.getInstanceIds()")
	// Should be at least 3: default MQTT + ha_broker + cloud_broker
	t.Logf("Total MQTT instances: %s", result)

	t.Log("MQTT multi-instance test passed!")
}

func TestMqttDiscovery(t *testing.T) {
	engine := NewLuaEngine()
	defer engine.Close()

	// Setup MQTT with pre-generated discovery messages (like from Go omparser)
	_, err := engine.Execute(`
		-- Configure MQTT for HA
		mqtt_ha = MQTT:new("ha")
		mqtt_ha:setUrl("tcp://192.168.1.100")
		       :configureDiscovery({autoPublish = false})

		-- Load pre-generated discovery messages (simulating Go omparser output)
		mqtt_ha:loadDiscoveryMessages({
			{
				topic = "homeassistant/switch/grenton_clu123_dou5048/config",
				payload = '{"name":"Lampka Salon","unique_id":"grenton_clu123_dou5048","command_topic":"grenton/clu123/switch/dou5048/set","state_topic":"grenton/clu123/switch/dou5048/state"}',
				retain = true
			},
			{
				topic = "homeassistant/light/grenton_clu123_dim1234/config",
				payload = '{"name":"Dimmer Kuchnia","unique_id":"grenton_clu123_dim1234"}',
				retain = true
			},
			{
				topic = "homeassistant/cover/grenton_clu123_rol5678/config",
				payload = '{"name":"Roleta Okno","unique_id":"grenton_clu123_rol5678"}',
				retain = true
			}
		})
	`)
	if err != nil {
		t.Fatalf("Failed to setup: %v", err)
	}

	// Publish all discovery
	_, err = engine.Execute(`count = mqtt_ha:publishAllDiscovery()`)
	if err != nil {
		t.Fatalf("Failed to publish discovery: %v", err)
	}

	result, _ := engine.Execute("count")
	if result != "3" {
		t.Errorf("Expected 3 discovery messages, got %s", result)
	}

	// Check outbox
	result, _ = engine.Execute("#mqtt_ha:getOutbox()")
	if result != "3" {
		t.Errorf("Expected 3 messages in outbox, got %s", result)
	}

	// Check first message topic
	result, _ = engine.Execute("mqtt_ha:getOutbox()[1].topic")
	expected := "homeassistant/switch/grenton_clu123_dou5048/config"
	if result != expected {
		t.Errorf("Expected topic '%s', got '%s'", expected, result)
	}

	// Check payload
	result, _ = engine.Execute("mqtt_ha:getOutbox()[1].payload")
	if !strings.Contains(result, "Lampka Salon") {
		t.Errorf("Payload should contain 'Lampka Salon': %s", result)
	}
	t.Logf("Discovery payload: %s", result)

	// Test remove discovery
	_, err = engine.Execute(`
		mqtt_ha:clearOutbox()
		removeCount = mqtt_ha:removeAllDiscovery()
	`)
	if err != nil {
		t.Fatalf("Failed to remove discovery: %v", err)
	}

	// Should publish 3 empty payloads
	result, _ = engine.Execute("#mqtt_ha:getOutbox()")
	if result != "3" {
		t.Errorf("Expected 3 removal messages in outbox, got %s", result)
	}

	// Empty payload removes from HA
	result, _ = engine.Execute("mqtt_ha:getOutbox()[1].payload")
	if result != "" {
		t.Errorf("Removal payload should be empty, got '%s'", result)
	}

	t.Log("MQTT discovery test passed!")
}
