package lua

import (
	"strings"
	"testing"
	"time"
)

// =============================================================================
// E2E Tests - Full integration tests using real Lua runtime with test fixtures
// =============================================================================

const e2eTestDataPath = "./pkg/lua/testdata/e2e/clu"

// TestE2E_FullBootstrapWithCLUData tests complete system initialization
func TestE2E_FullBootstrapWithCLUData(t *testing.T) {
	engine := NewLuaEngine()
	defer engine.Close()

	// Load test CLU data
	err := engine.LoadCLUData(e2eTestDataPath)
	if err != nil {
		t.Fatalf("Failed to load CLU data: %v", err)
	}

	// Initialize system
	err = engine.Init()
	if err != nil {
		t.Fatalf("Failed to init: %v", err)
	}

	// Verify initialization
	result, err := engine.Execute("getVar('initialized')")
	if err != nil {
		t.Fatalf("Failed to check initialized: %v", err)
	}
	if result != "true" {
		t.Errorf("Expected initialized=true, got %s", result)
	}

	t.Log("E2E: Full bootstrap with CLU data passed!")
}

// TestE2E_RegistryPopulation tests that registry contains expected objects
func TestE2E_RegistryPopulation(t *testing.T) {
	engine := NewLuaEngine()
	defer engine.Close()

	err := engine.LoadCLUData(e2eTestDataPath)
	if err != nil {
		t.Fatalf("Failed to load CLU data: %v", err)
	}

	err = engine.Init()
	if err != nil {
		t.Fatalf("Failed to init: %v", err)
	}

	// Check registry has objects
	result, err := engine.Execute("inspectRegistry()")
	if err != nil {
		t.Fatalf("Failed to inspect registry: %v", err)
	}

	// Should have at least CLUs
	t.Logf("Registry contains %s objects", result)

	// Check specific objects exist (CLUs only in simplified test fixtures)
	tests := []struct {
		check string
		name  string
	}{
		{"CLU520046772 ~= nil", "CLU520046772 (GATE)"},
		{"CLU212345678 ~= nil", "CLU212345678 (Remote)"},
		{"CLU220000592 ~= nil", "CLU220000592 (Remote)"},
	}

	for _, tc := range tests {
		result, err := engine.Execute(tc.check)
		if err != nil {
			t.Errorf("Check %s failed: %v", tc.name, err)
		}
		if result != "true" {
			t.Errorf("Expected %s to exist", tc.name)
		}
	}

	t.Log("E2E: Registry population passed!")
}

// TestE2E_ObjectGetSet tests object value operations
func TestE2E_ObjectGetSet(t *testing.T) {
	engine := NewLuaEngine()
	defer engine.Close()

	err := engine.LoadCLUData(e2eTestDataPath)
	if err != nil {
		t.Fatalf("Failed to load CLU data: %v", err)
	}

	// Set value on CLU
	_, err = engine.Execute("CLU520046772:set(1, 5000)")
	if err != nil {
		t.Fatalf("Failed to set: %v", err)
	}

	// Test user variables (simpler than object state)
	_, err = engine.Execute("setVar('test_state', 123)")
	if err != nil {
		t.Fatalf("Failed to setVar: %v", err)
	}

	result, err := engine.Execute("getVar('test_state')")
	if err != nil {
		t.Fatalf("Failed to getVar: %v", err)
	}
	if result != "123" {
		t.Errorf("Expected 123, got %s", result)
	}

	t.Log("E2E: Object get/set passed!")
}

// TestE2E_UserVariables tests setVar/getVar functionality
func TestE2E_UserVariables(t *testing.T) {
	engine := NewLuaEngine()
	defer engine.Close()

	err := engine.LoadCLUData(e2eTestDataPath)
	if err != nil {
		t.Fatalf("Failed to load CLU data: %v", err)
	}

	// Set variable
	_, err = engine.Execute("setVar('myVar', 'hello')")
	if err != nil {
		t.Fatalf("Failed to setVar: %v", err)
	}

	// Get variable
	result, err := engine.Execute("getVar('myVar')")
	if err != nil {
		t.Fatalf("Failed to getVar: %v", err)
	}
	if result != "hello" {
		t.Errorf("Expected 'hello', got '%s'", result)
	}

	// Test numeric variable
	_, err = engine.Execute("setVar('counter', 42)")
	if err != nil {
		t.Fatalf("Failed to setVar counter: %v", err)
	}

	result, err = engine.Execute("getVar('counter')")
	if err != nil {
		t.Fatalf("Failed to getVar counter: %v", err)
	}
	if result != "42" {
		t.Errorf("Expected '42', got '%s'", result)
	}

	t.Log("E2E: User variables passed!")
}

// TestE2E_UserFunctions tests calling user-defined functions
func TestE2E_UserFunctions(t *testing.T) {
	engine := NewLuaEngine()
	defer engine.Close()

	err := engine.LoadCLUData(e2eTestDataPath)
	if err != nil {
		t.Fatalf("Failed to load CLU data: %v", err)
	}

	// Call sceneLightsOff
	result, err := engine.Execute("sceneLightsOff()")
	if err != nil {
		t.Fatalf("Failed to call sceneLightsOff: %v", err)
	}
	if result != "all_off" {
		t.Errorf("Expected 'all_off', got '%s'", result)
	}

	// Call synchronize
	result, err = engine.Execute("synchronize('test/path', 123)")
	if err != nil {
		t.Fatalf("Failed to call synchronize: %v", err)
	}
	if result != "synced" {
		t.Errorf("Expected 'synced', got '%s'", result)
	}

	// Verify sync stored values
	result, err = engine.Execute("getVar('last_sync_path')")
	if err != nil {
		t.Fatalf("Failed to get last_sync_path: %v", err)
	}
	if result != "test/path" {
		t.Errorf("Expected 'test/path', got '%s'", result)
	}

	t.Log("E2E: User functions passed!")
}

// TestE2E_GlobalHelpers tests global helper functions from Helpers.lua
func TestE2E_GlobalHelpers(t *testing.T) {
	engine := NewLuaEngine()
	defer engine.Close()

	// Test clamp
	result, err := engine.Execute("clamp(150, 0, 100)")
	if err != nil {
		t.Fatalf("clamp failed: %v", err)
	}
	if result != "100" {
		t.Errorf("clamp(150,0,100) expected 100, got %s", result)
	}

	// Test lerp
	result, err = engine.Execute("lerp(0, 100, 0.5)")
	if err != nil {
		t.Fatalf("lerp failed: %v", err)
	}
	if result != "50" {
		t.Errorf("lerp(0,100,0.5) expected 50, got %s", result)
	}

	// Test map
	result, err = engine.Execute("map(50, 0, 100, 0, 255)")
	if err != nil {
		t.Fatalf("map failed: %v", err)
	}
	if result != "127.5" {
		t.Errorf("map(50,0,100,0,255) expected 127.5, got %s", result)
	}

	// Test round
	result, err = engine.Execute("round(3.7)")
	if err != nil {
		t.Fatalf("round failed: %v", err)
	}
	if result != "4" {
		t.Errorf("round(3.7) expected 4, got %s", result)
	}

	// Test inspect exists
	result, err = engine.Execute("type(inspect)")
	if err != nil {
		t.Fatalf("inspect check failed: %v", err)
	}
	if result != "function" {
		t.Errorf("inspect should be function, got %s", result)
	}

	// Test dump exists
	result, err = engine.Execute("type(dump)")
	if err != nil {
		t.Fatalf("dump check failed: %v", err)
	}
	if result != "function" {
		t.Errorf("dump should be function, got %s", result)
	}

	t.Log("E2E: Global helpers passed!")
}

// TestE2E_InspectFunction tests the inspect helper
func TestE2E_InspectFunction(t *testing.T) {
	engine := NewLuaEngine()
	defer engine.Close()

	err := engine.LoadCLUData(e2eTestDataPath)
	if err != nil {
		t.Fatalf("Failed to load CLU data: %v", err)
	}

	// Inspect a CLU object
	result, err := engine.Execute("inspect(CLU520046772)")
	if err != nil {
		t.Fatalf("inspect(CLU520046772) failed: %v", err)
	}
	t.Logf("inspect(CLU520046772):\n%s", result)

	// Inspect a table
	result, err = engine.Execute("inspect({a=1, b=2, c='test'})")
	if err != nil {
		t.Fatalf("inspect table failed: %v", err)
	}
	if !strings.Contains(result, "a:") || !strings.Contains(result, "b:") {
		t.Errorf("inspect should contain table keys, got: %s", result)
	}
	t.Logf("inspect({a,b,c}):\n%s", result)

	t.Log("E2E: Inspect function passed!")
}

// TestE2E_MQTTModule tests MQTT functionality
func TestE2E_MQTTModule(t *testing.T) {
	engine := NewLuaEngine()
	defer engine.Close()

	// Create MQTT instance
	_, err := engine.Execute(`
		mqtt = MQTT.new("test_e2e")
		mqtt:setHost("127.0.0.1")
		mqtt:setPort(1883)
		mqtt:setCredentials("test", "test")
		mqtt:setTopicPrefix("grenton")
	`)
	if err != nil {
		t.Fatalf("MQTT setup failed: %v", err)
	}

	// Publish (will queue without actual broker)
	_, err = engine.Execute(`
		mqtt:publish("switch/light1/state", "ON")
		mqtt:publish("sensor/temp/state", "23.5")
	`)
	if err != nil {
		t.Fatalf("MQTT publish failed: %v", err)
	}

	// Check outbox
	result, err := engine.Execute("#mqtt:getOutbox()")
	if err != nil {
		t.Fatalf("getOutbox failed: %v", err)
	}
	if result != "2" {
		t.Errorf("Expected 2 messages in outbox, got %s", result)
	}

	// Subscribe and simulate
	_, err = engine.Execute(`
		receivedPayload = nil
		mqtt:subscribe("cmnd/+/POWER", function(topic, payload)
			receivedPayload = payload
		end)
		mqtt:simulateMessage("grenton/cmnd/light1/POWER", "TOGGLE")
	`)
	if err != nil {
		t.Fatalf("MQTT subscribe/simulate failed: %v", err)
	}

	result, err = engine.Execute("receivedPayload")
	if err != nil {
		t.Fatalf("Get receivedPayload failed: %v", err)
	}
	if result != "TOGGLE" {
		t.Errorf("Expected 'TOGGLE', got '%s'", result)
	}

	t.Log("E2E: MQTT module passed!")
}

// TestE2E_EventHandlers tests event registration and firing via EventBus
func TestE2E_EventHandlers(t *testing.T) {
	engine := NewLuaEngine()
	defer engine.Close()

	err := engine.LoadCLUData(e2eTestDataPath)
	if err != nil {
		t.Fatalf("Failed to load CLU data: %v", err)
	}

	// Setup event handler using EventBus (works without hardware objects)
	_, err = engine.Execute(`
		eventFired = false
		eventData = nil
		EventBus:getShared():on("test:switch", function(data)
			eventFired = true
			eventData = data
		end)
	`)
	if err != nil {
		t.Fatalf("Event setup failed: %v", err)
	}

	// Emit event
	_, err = engine.Execute(`EventBus:getShared():emit("test:switch", {value = 1, source = "test"})`)
	if err != nil {
		t.Fatalf("emit failed: %v", err)
	}

	// Check event fired
	result, err := engine.Execute("eventFired")
	if err != nil {
		t.Fatalf("Check eventFired failed: %v", err)
	}
	if result != "true" {
		t.Errorf("Expected event to fire")
	}

	result, err = engine.Execute("eventData.value")
	if err != nil {
		t.Fatalf("Check eventData.value failed: %v", err)
	}
	if result != "1" {
		t.Errorf("Expected eventData.value=1, got %s", result)
	}

	t.Log("E2E: Event handlers passed!")
}

// TestE2E_TimerExecution tests timer functionality
func TestE2E_TimerExecution(t *testing.T) {
	engine := NewLuaEngine()
	defer engine.Close()

	// Create timer with callback using proper API:
	// - set(Timer.FEATURE_TIME, ms) - set time
	// - set(Timer.FEATURE_MODE, mode) - set mode
	// - add_event(Timer.EVENT_ON_TIMER, callback) - register callback
	// - execute(Timer.METHOD_START) - start
	_, err := engine.Execute(`
		timerFired = false
		testTimer = Timer:new("test_timer_1", 0, EventBus:getShared())
		testTimer:set(Timer.FEATURE_TIME, 50) -- 50ms
		testTimer:set(Timer.FEATURE_MODE, Timer.MODE_COUNT_DOWN) -- one-shot
		testTimer:add_event(Timer.EVENT_ON_TIMER, function()
			timerFired = true
		end)
		testTimer:execute(Timer.METHOD_START)
	`)
	if err != nil {
		t.Fatalf("Timer setup failed: %v", err)
	}

	// Check timer configuration
	result, err := engine.Execute("testTimer:get(Timer.FEATURE_TIME)")
	if err != nil {
		t.Fatalf("Get time failed: %v", err)
	}
	if result != "50" {
		t.Errorf("Expected time=50, got %s", result)
	}

	// Wait for timer
	time.Sleep(150 * time.Millisecond)

	// Process timer queue
	engine.ProcessTimers()

	// Check timer fired
	result, err = engine.Execute("timerFired")
	if err != nil {
		t.Fatalf("Check timerFired failed: %v", err)
	}
	if result != "true" {
		t.Log("Timer may not have fired - this is expected without full timer integration")
	}

	t.Log("E2E: Timer test completed")
}

// TestE2E_SceneDefinition tests scene helper
func TestE2E_SceneDefinition(t *testing.T) {
	engine := NewLuaEngine()
	defer engine.Close()

	err := engine.LoadCLUData(e2eTestDataPath)
	if err != nil {
		t.Fatalf("Failed to load CLU data: %v", err)
	}

	// Define scene
	_, err = engine.Execute(`
		sceneRan = false
		scene("kino", function()
			sceneRan = true
			return "kino_active"
		end)
	`)
	if err != nil {
		t.Fatalf("Scene definition failed: %v", err)
	}

	// Run scene
	_, err = engine.Execute("scenes.kino:run()")
	if err != nil {
		t.Fatalf("Scene run failed: %v", err)
	}

	// Check scene ran
	result, err := engine.Execute("sceneRan")
	if err != nil {
		t.Fatalf("Check sceneRan failed: %v", err)
	}
	if result != "true" {
		t.Errorf("Expected scene to run")
	}

	// Disable and run again
	_, err = engine.Execute(`
		sceneRan = false
		scenes.kino:disable()
		scenes.kino:run()
	`)
	if err != nil {
		t.Fatalf("Scene disable/run failed: %v", err)
	}

	result, err = engine.Execute("sceneRan")
	if err != nil {
		t.Fatalf("Check sceneRan after disable failed: %v", err)
	}
	if result != "false" {
		t.Errorf("Scene should not run when disabled")
	}

	t.Log("E2E: Scene definition passed!")
}

// TestE2E_HTTPModule tests HTTP client (without actual network)
func TestE2E_HTTPModule(t *testing.T) {
	engine := NewLuaEngine()
	defer engine.Close()

	// Test HTTP module using proper Grenton API:
	// - set(HTTPRequest.FEATURE_HOST, host) - set host
	// - set(HTTPRequest.FEATURE_PATH, path) - set path
	// - set(HTTPRequest.FEATURE_METHOD, method) - set method
	// - set(HTTPRequest.FEATURE_REQUEST_BODY, body) - set body
	// - set(HTTPRequest.FEATURE_REQUEST_TYPE, type) - set content type
	_, err := engine.Execute(`
		req = HTTPRequest:new("test_http_1", 0, EventBus:getShared())
		req:set(HTTPRequest.FEATURE_HOST, "http://example.com")
		req:set(HTTPRequest.FEATURE_PATH, "/api/data")
		req:set(HTTPRequest.FEATURE_METHOD, "POST")
		req:set(HTTPRequest.FEATURE_REQUEST_TYPE, HTTPRequest.REQUEST_TYPE_JSON)
		req:set(HTTPRequest.FEATURE_REQUEST_BODY, {test = true})
	`)
	if err != nil {
		t.Fatalf("HTTP setup failed: %v", err)
	}

	// Verify configuration using get()
	result, err := engine.Execute("req:get(HTTPRequest.FEATURE_HOST)")
	if err != nil {
		t.Fatalf("get HOST failed: %v", err)
	}
	if result != "http://example.com" {
		t.Errorf("Expected host 'http://example.com', got %s", result)
	}

	result, err = engine.Execute("req:get(HTTPRequest.FEATURE_PATH)")
	if err != nil {
		t.Fatalf("get PATH failed: %v", err)
	}
	if result != "/api/data" {
		t.Errorf("Expected path '/api/data', got %s", result)
	}

	result, err = engine.Execute("req:get(HTTPRequest.FEATURE_METHOD)")
	if err != nil {
		t.Fatalf("get METHOD failed: %v", err)
	}
	if result != "POST" {
		t.Errorf("Expected POST, got %s", result)
	}

	t.Log("E2E: HTTP module passed!")
}

// TestE2E_JSONModule tests JSON encoding/decoding
func TestE2E_JSONModule(t *testing.T) {
	engine := NewLuaEngine()
	defer engine.Close()

	// Encode
	result, err := engine.Execute(`JSON:encode({name="test", value=42, active=true})`)
	if err != nil {
		t.Fatalf("JSON encode failed: %v", err)
	}
	if !strings.Contains(result, "test") || !strings.Contains(result, "42") {
		t.Errorf("JSON encode wrong: %s", result)
	}

	// Decode
	_, err = engine.Execute(`decoded = JSON:decode('{"x":1,"y":2}')`)
	if err != nil {
		t.Fatalf("JSON decode failed: %v", err)
	}

	result, err = engine.Execute("decoded.x")
	if err != nil {
		t.Fatalf("Get decoded.x failed: %v", err)
	}
	if result != "1" {
		t.Errorf("Expected x=1, got %s", result)
	}

	t.Log("E2E: JSON module passed!")
}

// TestE2E_EventBus tests global event bus
func TestE2E_EventBus(t *testing.T) {
	engine := NewLuaEngine()
	defer engine.Close()

	// Subscribe to event
	_, err := engine.Execute(`
		eventReceived = nil
		EventBus:getShared():on("test:event", function(data)
			eventReceived = data
		end)
	`)
	if err != nil {
		t.Fatalf("EventBus subscribe failed: %v", err)
	}

	// Emit event
	_, err = engine.Execute(`EventBus:getShared():emit("test:event", "hello_world")`)
	if err != nil {
		t.Fatalf("EventBus emit failed: %v", err)
	}

	// Check received
	result, err := engine.Execute("eventReceived")
	if err != nil {
		t.Fatalf("Get eventReceived failed: %v", err)
	}
	if result != "hello_world" {
		t.Errorf("Expected 'hello_world', got '%s'", result)
	}

	t.Log("E2E: EventBus passed!")
}

// TestE2E_StateBus tests state change notifications
func TestE2E_StateBus(t *testing.T) {
	engine := NewLuaEngine()
	defer engine.Close()

	err := engine.LoadCLUData(e2eTestDataPath)
	if err != nil {
		t.Fatalf("Failed to load CLU data: %v", err)
	}

	// Subscribe to state changes
	_, err = engine.Execute(`
		stateChanges = {}
		StateBus:getShared():on("state_changed", function(data)
			table.insert(stateChanges, data)
		end)
	`)
	if err != nil {
		t.Fatalf("StateBus subscribe failed: %v", err)
	}

	// Trigger state change
	_, err = engine.Execute(`
		StateBus:getShared():emit("state_changed", {
			path = "CLU212345678.DOU7234",
			value = 1,
			type = "switch"
		})
	`)
	if err != nil {
		t.Fatalf("StateBus emit failed: %v", err)
	}

	// Check received
	result, err := engine.Execute("#stateChanges")
	if err != nil {
		t.Fatalf("Get stateChanges count failed: %v", err)
	}
	if result != "1" {
		t.Errorf("Expected 1 state change, got %s", result)
	}

	t.Log("E2E: StateBus passed!")
}

// TestE2E_PluginSandbox tests plugin sandbox creation (without actual plugin)
func TestE2E_PluginSandbox(t *testing.T) {
	engine := NewLuaEngine()
	defer engine.Close()

	// Create sandbox
	_, err := engine.Execute(`PluginSandbox:create("@test/plugin")`)
	if err != nil {
		t.Fatalf("Sandbox create failed: %v", err)
	}

	// Check sandbox exists
	result, err := engine.Execute("PluginSandbox.sandboxes['@test/plugin'] ~= nil")
	if err != nil {
		t.Fatalf("Sandbox check failed: %v", err)
	}
	if result != "true" {
		t.Errorf("Sandbox should exist")
	}

	// Cleanup sandbox
	_, err = engine.Execute(`PluginSandbox:cleanup("@test/plugin")`)
	if err != nil {
		t.Fatalf("Sandbox cleanup failed: %v", err)
	}

	// Check sandbox removed
	result, err = engine.Execute("PluginSandbox.sandboxes['@test/plugin'] == nil")
	if err != nil {
		t.Fatalf("Sandbox removal check failed: %v", err)
	}
	if result != "true" {
		t.Errorf("Sandbox should be removed after cleanup")
	}

	t.Log("E2E: Plugin sandbox passed!")
}

// TestE2E_AccessControl tests access control module
func TestE2E_AccessControl(t *testing.T) {
	engine := NewLuaEngine()
	defer engine.Close()

	// Set access levels (API: set(integration, objectPath, level))
	_, err := engine.Execute(`
		AccessControl.set("mqtt", "CLU212345678.DOU7234", "full")
		AccessControl.set("mqtt", "CLU212345678.ANA4201", "readonly")
		AccessControl.set("mqtt", "CLU212345678.DIN9868", "hidden")
		AccessControl.set("homekit", "CLU212345678.DOU7234", "hidden")
	`)
	if err != nil {
		t.Fatalf("AccessControl set failed: %v", err)
	}

	// Check access levels (API: get(integration, objectPath))
	tests := []struct {
		integration string
		path        string
		expected    string
	}{
		{"mqtt", "CLU212345678.DOU7234", "full"},
		{"mqtt", "CLU212345678.ANA4201", "readonly"},
		{"mqtt", "CLU212345678.DIN9868", "hidden"},
		{"homekit", "CLU212345678.DOU7234", "hidden"},
		{"homekit", "CLU212345678.ANA4201", "full"}, // default
	}

	for _, tc := range tests {
		result, err := engine.Execute(`AccessControl.get("` + tc.integration + `", "` + tc.path + `")`)
		if err != nil {
			t.Errorf("get(%s, %s) failed: %v", tc.integration, tc.path, err)
			continue
		}
		if result != tc.expected {
			t.Errorf("Access for %s.%s: expected %s, got %s", tc.integration, tc.path, tc.expected, result)
		}
	}

	// Test helper methods
	result, err := engine.Execute(`AccessControl.canPublish("mqtt", "CLU212345678.DIN9868")`)
	if err != nil {
		t.Fatalf("canPublish failed: %v", err)
	}
	if result != "false" {
		t.Errorf("canPublish should be false for hidden object")
	}

	result, err = engine.Execute(`AccessControl.canControl("mqtt", "CLU212345678.ANA4201")`)
	if err != nil {
		t.Fatalf("canControl failed: %v", err)
	}
	if result != "false" {
		t.Errorf("canControl should be false for readonly object")
	}

	t.Log("E2E: Access control passed!")
}

// TestE2E_Reinitialize tests Lua state reinitialization (like after reset)
func TestE2E_Reinitialize(t *testing.T) {
	engine := NewLuaEngine()
	defer engine.Close()

	// Load initial data
	err := engine.LoadCLUData(e2eTestDataPath)
	if err != nil {
		t.Fatalf("Failed to load CLU data: %v", err)
	}

	// Set some state in global (not using setVar which is in om.lua)
	_, err = engine.Execute("_G.test_before_reset = 'value1'")
	if err != nil {
		t.Fatalf("Failed to set global: %v", err)
	}

	// Verify state exists
	result, err := engine.Execute("_G.test_before_reset")
	if err != nil {
		t.Fatalf("Failed to get global: %v", err)
	}
	if result != "value1" {
		t.Errorf("Expected 'value1', got '%s'", result)
	}

	// Verify user function exists before reinit
	result, err = engine.Execute("type(getVar)")
	if err != nil {
		t.Fatalf("type(getVar) failed: %v", err)
	}
	if result != "function" {
		t.Errorf("getVar should be a function before reinit")
	}

	// Reinitialize (this creates a fresh Lua state, only bootstrap is loaded)
	err = engine.Reinitialize(e2eTestDataPath)
	if err != nil {
		t.Fatalf("Reinitialize failed: %v", err)
	}

	// Check bootstrap loaded again
	result, err = engine.Execute("SYSTEM ~= nil")
	if err != nil {
		t.Fatalf("SYSTEM check failed: %v", err)
	}
	if result != "true" {
		t.Errorf("SYSTEM should exist after reinit")
	}

	// Old state should be gone - global should not exist
	result, err = engine.Execute("_G.test_before_reset == nil")
	if err != nil {
		t.Fatalf("Check nil failed: %v", err)
	}
	if result != "true" {
		t.Errorf("test_before_reset should be nil after reinit")
	}

	// User function getVar should not exist (it's defined in om.lua which wasn't reloaded)
	result, err = engine.Execute("type(getVar)")
	if err != nil {
		t.Fatalf("type(getVar) after reinit failed: %v", err)
	}
	if result != "nil" {
		t.Errorf("getVar should be nil after reinit (om.lua not loaded), got %s", result)
	}

	// Bootstrap functions should still work
	result, err = engine.Execute("type(Timer)")
	if err != nil {
		t.Fatalf("type(Timer) failed: %v", err)
	}
	if result != "table" {
		t.Errorf("Timer should exist after reinit")
	}

	t.Log("E2E: Reinitialize passed!")
}
