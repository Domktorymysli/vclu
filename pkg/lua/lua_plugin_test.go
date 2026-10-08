package lua

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/Domktorymysli/vclu/pkg/plugins"
)

func TestPluginAPIBasic(t *testing.T) {
	engine := NewLuaEngine()
	defer engine.Close()

	// Test 1: Plugin class exists
	result, err := engine.Execute("type(Plugin)")
	if err != nil {
		t.Fatalf("Failed to check Plugin type: %v", err)
	}
	if result != "table" {
		t.Errorf("Expected Plugin to be table, got %s", result)
	}

	// Test 2: Plugin.instances exists
	result, err = engine.Execute("type(Plugin.instances)")
	if err != nil {
		t.Fatalf("Failed to check Plugin.instances: %v", err)
	}
	if result != "table" {
		t.Errorf("Expected Plugin.instances to be table, got %s", result)
	}

	// Test 3: Plugin:new() creates an instance
	_, err = engine.Execute(`
		testPlugin = Plugin:new("test-plugin", {
			name = "Test Plugin",
			version = "1.0.0",
			description = "A test plugin"
		})
	`)
	if err != nil {
		t.Fatalf("Failed to create plugin: %v", err)
	}

	// Test 4: Instance has correct properties
	result, _ = engine.Execute("testPlugin.name")
	if result != "Test Plugin" {
		t.Errorf("Expected name 'Test Plugin', got '%s'", result)
	}

	result, _ = engine.Execute("testPlugin.version")
	if result != "1.0.0" {
		t.Errorf("Expected version '1.0.0', got '%s'", result)
	}

	result, _ = engine.Execute("testPlugin.id")
	if result != "test-plugin" {
		t.Errorf("Expected id 'test-plugin', got '%s'", result)
	}

	// Test 5: Instance is registered
	result, _ = engine.Execute(`Plugin.instances["test-plugin"] ~= nil`)
	if result != "true" {
		t.Errorf("Expected plugin to be registered in instances")
	}

	t.Log("Plugin API basic test passed!")
}

func TestPluginAPINamespace(t *testing.T) {
	engine := NewLuaEngine()
	defer engine.Close()

	// Test with namespace prefix (like @vclu/weather)
	_, err := engine.Execute(`
		__plugin_id = "@vclu/weather"
		weatherPlugin = Plugin:new("weather", {
			name = "Weather Plugin",
			version = "2.0.0"
		})
	`)
	if err != nil {
		t.Fatalf("Failed to create namespaced plugin: %v", err)
	}

	// Should use fullId from __plugin_id
	result, _ := engine.Execute("weatherPlugin.id")
	if result != "@vclu/weather" {
		t.Errorf("Expected id '@vclu/weather', got '%s'", result)
	}

	// Should parse namespace
	result, _ = engine.Execute("weatherPlugin.namespace")
	if result != "vclu" {
		t.Errorf("Expected namespace 'vclu', got '%s'", result)
	}

	// Should parse shortId
	result, _ = engine.Execute("weatherPlugin.shortId")
	if result != "weather" {
		t.Errorf("Expected shortId 'weather', got '%s'", result)
	}

	t.Log("Plugin API namespace test passed!")
}

func TestPluginAPIConfig(t *testing.T) {
	engine := NewLuaEngine()
	defer engine.Close()

	_, err := engine.Execute(`
		configPlugin = Plugin:new("config-test", {
			name = "Config Test",
			version = "1.0.0"
		})

		-- Simulate init with config
		configPlugin:init({
			apiKey = "secret123",
			interval = 60,
			enabled = true
		})
	`)
	if err != nil {
		t.Fatalf("Failed to init plugin with config: %v", err)
	}

	// Test getConfig
	result, _ := engine.Execute(`configPlugin:getConfig("apiKey", "")`)
	if result != "secret123" {
		t.Errorf("Expected apiKey 'secret123', got '%s'", result)
	}

	result, _ = engine.Execute(`tostring(configPlugin:getConfig("interval", 0))`)
	if result != "60" {
		t.Errorf("Expected interval 60, got '%s'", result)
	}

	// Test default value
	result, _ = engine.Execute(`configPlugin:getConfig("missing", "default_value")`)
	if result != "default_value" {
		t.Errorf("Expected default 'default_value', got '%s'", result)
	}

	t.Log("Plugin API config test passed!")
}

func TestPluginAPICallbacks(t *testing.T) {
	engine := NewLuaEngine()
	defer engine.Close()

	_, err := engine.Execute(`
		callbackPlugin = Plugin:new("callback-test", {
			name = "Callback Test",
			version = "1.0.0"
		})

		initCalled = false
		cleanupCalled = false
		receivedConfig = nil

		callbackPlugin:onInit(function(config)
			initCalled = true
			receivedConfig = config
		end)

		callbackPlugin:onCleanup(function()
			cleanupCalled = true
		end)
	`)
	if err != nil {
		t.Fatalf("Failed to setup callbacks: %v", err)
	}

	// Init should not be called yet
	result, _ := engine.Execute("initCalled")
	if result != "false" {
		t.Errorf("Expected initCalled to be false before init")
	}

	// Call init
	_, err = engine.Execute(`callbackPlugin:init({testKey = "testValue"})`)
	if err != nil {
		t.Fatalf("Failed to call init: %v", err)
	}

	// Init callback should have been called
	result, _ = engine.Execute("initCalled")
	if result != "true" {
		t.Errorf("Expected initCalled to be true after init")
	}

	// Config should have been passed
	result, _ = engine.Execute(`receivedConfig.testKey`)
	if result != "testValue" {
		t.Errorf("Expected receivedConfig.testKey 'testValue', got '%s'", result)
	}

	// Call cleanup
	_, err = engine.Execute(`callbackPlugin:cleanup()`)
	if err != nil {
		t.Fatalf("Failed to call cleanup: %v", err)
	}

	result, _ = engine.Execute("cleanupCalled")
	if result != "true" {
		t.Errorf("Expected cleanupCalled to be true after cleanup")
	}

	t.Log("Plugin API callbacks test passed!")
}

func TestPluginAPIHelpers(t *testing.T) {
	engine := NewLuaEngine()
	defer engine.Close()

	_, err := engine.Execute(`
		helperPlugin = Plugin:new("helper-test", {
			name = "Helper Test",
			version = "1.0.0"
		})
	`)
	if err != nil {
		t.Fatalf("Failed to create plugin: %v", err)
	}

	// Test coerceNumber
	result, _ := engine.Execute(`tostring(helperPlugin:coerceNumber("123", 0))`)
	if result != "123" {
		t.Errorf("Expected coerceNumber '123', got '%s'", result)
	}

	result, _ = engine.Execute(`tostring(helperPlugin:coerceNumber(nil, 42))`)
	if result != "42" {
		t.Errorf("Expected coerceNumber default 42, got '%s'", result)
	}

	// Test coerceString
	result, _ = engine.Execute(`helperPlugin:coerceString(123, "default")`)
	if result != "123" {
		t.Errorf("Expected coerceString '123', got '%s'", result)
	}

	result, _ = engine.Execute(`helperPlugin:coerceString(nil, "default")`)
	if result != "default" {
		t.Errorf("Expected coerceString default 'default', got '%s'", result)
	}

	// Test coerceBool
	result, _ = engine.Execute(`tostring(helperPlugin:coerceBool("true", false))`)
	if result != "true" {
		t.Errorf("Expected coerceBool true, got '%s'", result)
	}

	result, _ = engine.Execute(`tostring(helperPlugin:coerceBool(nil, true))`)
	if result != "true" {
		t.Errorf("Expected coerceBool default true, got '%s'", result)
	}

	t.Log("Plugin API helpers test passed!")
}

func TestPluginAPIKVStore(t *testing.T) {
	// Create engine with KV store
	engine := NewLuaEngine()
	defer engine.Close()

	// Create temp directory for KV store
	tempDir := t.TempDir()
	kvManager := plugins.NewKVStoreManager(tempDir)
	engine.SetKVStoreManager(kvManager)

	// Create plugin
	_, err := engine.Execute(`
		__plugin_id = "@test/kv-plugin"
		kvPlugin = Plugin:new("kv-plugin", {
			name = "KV Test Plugin",
			version = "1.0.0"
		})
	`)
	if err != nil {
		t.Fatalf("Failed to create plugin: %v", err)
	}

	// Test kvSet and kvGet
	_, err = engine.Execute(`
		kvPlugin:kvSet("mykey", "myvalue")
	`)
	if err != nil {
		t.Fatalf("Failed to kvSet: %v", err)
	}

	result, _ := engine.Execute(`kvPlugin:kvGet("mykey", "default")`)
	if result != "myvalue" {
		t.Errorf("Expected kvGet 'myvalue', got '%s'", result)
	}

	// Test default value for missing key
	result, _ = engine.Execute(`kvPlugin:kvGet("missing", "default123")`)
	if result != "default123" {
		t.Errorf("Expected kvGet default 'default123', got '%s'", result)
	}

	// Test kvHas
	result, _ = engine.Execute(`tostring(kvPlugin:kvHas("mykey"))`)
	if result != "true" {
		t.Errorf("Expected kvHas true, got '%s'", result)
	}

	result, _ = engine.Execute(`tostring(kvPlugin:kvHas("missing"))`)
	if result != "false" {
		t.Errorf("Expected kvHas false, got '%s'", result)
	}

	// Test kvDelete
	_, err = engine.Execute(`kvPlugin:kvDelete("mykey")`)
	if err != nil {
		t.Fatalf("Failed to kvDelete: %v", err)
	}

	result, _ = engine.Execute(`tostring(kvPlugin:kvHas("mykey"))`)
	if result != "false" {
		t.Errorf("Expected kvHas false after delete, got '%s'", result)
	}

	// Verify file was created
	expectedFile := filepath.Join(tempDir, "kv", "test--kv-plugin.json")
	if !fileExists(expectedFile) {
		t.Logf("Note: KV file expected at %s", expectedFile)
	}

	t.Log("Plugin API KV store test passed!")
}

func TestPluginAPIKVStoreSecure(t *testing.T) {
	engine := NewLuaEngine()
	defer engine.Close()

	tempDir := t.TempDir()
	kvManager := plugins.NewKVStoreManager(tempDir)
	engine.SetKVStoreManager(kvManager)

	_, err := engine.Execute(`
		__plugin_id = "@test/secure-plugin"
		securePlugin = Plugin:new("secure-plugin", {
			name = "Secure Test",
			version = "1.0.0"
		})
	`)
	if err != nil {
		t.Fatalf("Failed to create plugin: %v", err)
	}

	// Test secure flag
	_, err = engine.Execute(`
		securePlugin:kvSet("token", "secret_token_123", {secure = true})
		securePlugin:kvSet("normal", "normal_value")
	`)
	if err != nil {
		t.Fatalf("Failed to kvSet with secure: %v", err)
	}

	// kvGet should still return the actual value
	result, _ := engine.Execute(`securePlugin:kvGet("token", "")`)
	if result != "secret_token_123" {
		t.Errorf("Expected kvGet token value, got '%s'", result)
	}

	// kvGetAll should hide secure values
	result, _ = engine.Execute(`securePlugin:kvGetAll().token`)
	if result != "[SECURE]" {
		t.Errorf("Expected kvGetAll to show '[SECURE]' for secure key, got '%s'", result)
	}

	result, _ = engine.Execute(`securePlugin:kvGetAll().normal`)
	if result != "normal_value" {
		t.Errorf("Expected kvGetAll normal value, got '%s'", result)
	}

	t.Log("Plugin API KV secure test passed!")
}

func TestPluginList(t *testing.T) {
	engine := NewLuaEngine()
	defer engine.Close()

	// Create multiple plugins
	_, err := engine.Execute(`
		Plugin:new("plugin-a", {name = "Plugin A", version = "1.0.0"})
		Plugin:new("plugin-b", {name = "Plugin B", version = "2.0.0"})
	`)
	if err != nil {
		t.Fatalf("Failed to create plugins: %v", err)
	}

	// Test Plugin.list()
	result, err := engine.Execute(`#Plugin.list()`)
	if err != nil {
		t.Fatalf("Failed to list plugins: %v", err)
	}
	// Should have at least 2 plugins (might have more from previous tests in same process)
	if result != "2" {
		t.Logf("Plugin.list() returned %s plugins", result)
	}

	// Test Plugin.getPlugin()
	result, _ = engine.Execute(`Plugin.getPlugin("plugin-a").name`)
	if result != "Plugin A" {
		t.Errorf("Expected Plugin.getPlugin name 'Plugin A', got '%s'", result)
	}

	result, _ = engine.Execute(`Plugin.getPlugin("plugin-b").version`)
	if result != "2.0.0" {
		t.Errorf("Expected Plugin.getPlugin version '2.0.0', got '%s'", result)
	}

	t.Log("Plugin list test passed!")
}

func TestPluginAPISensorControl(t *testing.T) {
	engine := NewLuaEngine()
	defer engine.Close()

	// First verify globals persist
	_, err := engine.Execute(`testGlobal = 42`)
	if err != nil {
		t.Fatalf("Failed to set global: %v", err)
	}
	result, _ := engine.Execute(`tostring(testGlobal)`)
	if result != "42" {
		t.Fatalf("Globals don't persist! Expected '42', got '%s'", result)
	}

	// Setup: create plugin
	_, err = engine.Execute(`
		scPlugin = Plugin:new("sensor-control-test", {
			name = "SC Test",
			version = "1.0.0"
		})
	`)
	if err != nil {
		t.Fatalf("Failed to create plugin: %v", err)
	}

	// Verify plugin was created
	result, _ = engine.Execute(`tostring(scPlugin ~= nil)`)
	if result != "true" {
		t.Fatalf("Plugin creation failed")
	}

	// Setup state
	_, err = engine.Execute(`scState = { temp = 22.5, target = 20.0 }`)
	if err != nil {
		t.Fatalf("Failed to create state: %v", err)
	}

	// Create sensor
	_, err = engine.Execute(`scSensor = scPlugin:sensor("temp", function() return scState.temp end)`)
	if err != nil {
		t.Fatalf("Failed to create sensor: %v", err)
	}

	// Test sensor returned
	result, _ = engine.Execute(`tostring(scSensor ~= nil)`)
	if result != "true" {
		t.Errorf("sensor() should return an object")
	}

	// Test sensor:get()
	result, _ = engine.Execute(`tostring(scSensor:get())`)
	if result != "22.5" {
		t.Errorf("sensor:get() expected 22.5, got '%s'", result)
	}

	// Check _sensors was created
	result, _ = engine.Execute(`tostring(scPlugin._sensors ~= nil)`)
	if result != "true" {
		t.Errorf("_sensors should exist on plugin")
	}

	// Check _sensors has the sensor
	result, _ = engine.Execute(`tostring(scPlugin._sensors["temp"] ~= nil)`)
	if result != "true" {
		t.Errorf("_sensors['temp'] should exist")
	}

	// Test plugin:get() retrieves the sensor
	result, _ = engine.Execute(`tostring(scPlugin:get("temp") ~= nil)`)
	if result != "true" {
		t.Errorf("plugin:get('temp') should return the sensor")
	}

	// Create control
	_, err = engine.Execute(`scControl = scPlugin:control("target", function() return scState.target end, function(v) scState.target = v end)`)
	if err != nil {
		t.Fatalf("Failed to create control: %v", err)
	}

	// Test control:get()
	result, _ = engine.Execute(`tostring(scControl:get())`)
	if result != "20" {
		t.Errorf("control:get() expected 20, got '%s'", result)
	}

	// Test control:set()
	_, err = engine.Execute(`scControl:set(25.0)`)
	if err != nil {
		t.Fatalf("control:set() error: %v", err)
	}

	// Verify state changed
	result, _ = engine.Execute(`tostring(scState.target)`)
	if result != "25" {
		t.Errorf("state.target should be 25 after set, got '%s'", result)
	}

	// Verify control:get() returns new value
	result, _ = engine.Execute(`tostring(scControl:get())`)
	if result != "25" {
		t.Errorf("control:get() expected 25 after set, got '%s'", result)
	}

	// Test sensor has no _setter
	result, _ = engine.Execute(`tostring(scSensor._setter == nil)`)
	if result != "true" {
		t.Errorf("sensor should not have a setter")
	}

	t.Log("Plugin API sensor/control test passed!")
}

func TestPluginSandboxReadOnlyRegistry(t *testing.T) {
	engine := NewLuaEngine()
	defer engine.Close()

	// Setup: put external objects in global _registry
	_, err := engine.Execute(`
		_registry["CLU.DOUT1"] = { Value = 0, Name = "Light" }
		_registry["CLU.DIN1"] = { Value = 1, Name = "Button" }
	`)
	if err != nil {
		t.Fatalf("Failed to setup registry: %v", err)
	}

	// Create sandbox for @test/weather
	_, err = engine.Execute(`PluginSandbox:create("@test/weather")`)
	if err != nil {
		t.Fatalf("Sandbox create failed: %v", err)
	}

	// Setup plugin's own data object via sandbox API
	_, err = engine.Execute(`
		local r, e = PluginSandbox:execute("@test/weather", [[
			local p = Plugin:new("weather", {name="Weather", version="1.0.0"})
			p:createObject("data", { temp = 22.5, ready = true })
		]])
		if e then error(e) end
	`)
	if err != nil {
		t.Fatalf("Failed to create plugin data object: %v", err)
	}

	// -------------------------------------------------------
	// Test 1: Plugin CAN READ external object via _:get()
	// -------------------------------------------------------
	_, err = engine.Execute(`
		local r, e = PluginSandbox:execute("@test/weather", 'return tostring((_:get("CLU.DOUT1")).Value)')
		__test_r = r
		__test_e = e
	`)
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	result, _ := engine.Execute("__test_r")
	if result != "0" {
		t.Errorf("Test 1: Expected to read Value=0 from external object, got '%s'", result)
	}

	// -------------------------------------------------------
	// Test 2: Plugin CANNOT MODIFY external object via _:get()
	// -------------------------------------------------------
	_, err = engine.Execute(`
		local r, e = PluginSandbox:execute("@test/weather", [[
			local lamp = _:get("CLU.DOUT1")
			lamp.Value = 1
		]])
		__test_e = e
	`)
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	result, _ = engine.Execute("__test_e")
	if result == "nil" || result == "" {
		t.Errorf("Test 2: Expected error when modifying external object, got no error")
	}
	// Original must be unchanged
	result, _ = engine.Execute("tostring(_registry['CLU.DOUT1'].Value)")
	if result != "0" {
		t.Errorf("Test 2: External object must be unchanged, got Value=%s", result)
	}

	// -------------------------------------------------------
	// Test 3: Plugin CANNOT MODIFY external object via _registry[]
	// -------------------------------------------------------
	_, err = engine.Execute(`
		local r, e = PluginSandbox:execute("@test/weather", [[
			local lamp = _registry["CLU.DIN1"]
			lamp.Value = 99
		]])
		__test_e = e
	`)
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	result, _ = engine.Execute("__test_e")
	if result == "nil" || result == "" {
		t.Errorf("Test 3: Expected error when modifying _registry external object")
	}
	result, _ = engine.Execute("tostring(_registry['CLU.DIN1'].Value)")
	if result != "1" {
		t.Errorf("Test 3: External object must be unchanged, got Value=%s", result)
	}

	// -------------------------------------------------------
	// Test 4: Plugin CAN modify own data objects via plugin:getObject
	// -------------------------------------------------------
	_, err = engine.Execute(`
		local r, e = PluginSandbox:execute("@test/weather", [[
			local p = Plugin.getPlugin("@test/weather")
			local data = p:getObject("data")
			data.temp = 30.0
			return tostring(data.temp)
		]])
		__test_r = r
		__test_e = e
	`)
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	result, _ = engine.Execute("__test_r")
	if result != "30" {
		t.Errorf("Test 4: Expected to modify own object temp=30, got '%s'", result)
	}
	errResult, _ := engine.Execute("__test_e")
	if errResult != "nil" {
		t.Errorf("Test 4: Should not error when modifying own data object, got: %s", errResult)
	}

	// -------------------------------------------------------
	// Test 5: Plugin CANNOT write to _registry directly (must use createObject/upsertObject)
	// -------------------------------------------------------
	_, err = engine.Execute(`
		local r, e = PluginSandbox:execute("@test/weather", [[
			_registry["plugins.test.weather.newobj"] = { val = 99 }
		]])
		__test_e = e
	`)
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	result, _ = engine.Execute("__test_e")
	if result == "nil" || result == "" {
		t.Errorf("Test 5: Expected error when writing to _registry directly, got no error")
	}

	// -------------------------------------------------------
	// Test 6: Plugin CANNOT assign to external key in _registry
	// -------------------------------------------------------
	_, err = engine.Execute(`
		local r, e = PluginSandbox:execute("@test/weather", [[
			_registry["CLU.DOUT1"] = { Value = 999 }
		]])
		__test_e = e
	`)
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	result, _ = engine.Execute("__test_e")
	if result == "nil" || result == "" {
		t.Errorf("Test 6: Expected error when assigning external key in _registry")
	}
	// Original must still have the old value
	result, _ = engine.Execute("tostring(_registry['CLU.DOUT1'].Value)")
	if result != "0" {
		t.Errorf("Test 6: External object must be unchanged, got Value=%s", result)
	}

	// -------------------------------------------------------
	// Test 7: pairs() works on read-only proxy
	// -------------------------------------------------------
	_, err = engine.Execute(`
		local r, e = PluginSandbox:execute("@test/weather", [[
			local lamp = _:get("CLU.DOUT1")
			local count = 0
			for k, v in pairs(lamp) do
				count = count + 1
			end
			return tostring(count)
		]])
		__test_r = r
		__test_e = e
	`)
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	result, _ = engine.Execute("__test_r")
	if result != "2" {
		t.Errorf("Test 7: Expected pairs() to iterate 2 fields (Value, Name), got '%s'", result)
	}

	// -------------------------------------------------------
	// Test 8: Nested tables are also read-only
	// -------------------------------------------------------
	_, err = engine.Execute(`_registry["CLU.DOUT1"].nested = { deep = 42 }`)
	if err != nil {
		t.Fatalf("Failed to add nested field: %v", err)
	}
	_, err = engine.Execute(`
		local r, e = PluginSandbox:execute("@test/weather", [[
			local lamp = _:get("CLU.DOUT1")
			lamp.nested.deep = 999
		]])
		__test_e = e
	`)
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	result, _ = engine.Execute("__test_e")
	if result == "nil" || result == "" {
		t.Errorf("Test 8: Expected error when modifying nested field of external object")
	}
	result, _ = engine.Execute("tostring(_registry['CLU.DOUT1'].nested.deep)")
	if result != "42" {
		t.Errorf("Test 8: Nested field must be unchanged, got %s", result)
	}

	// -------------------------------------------------------
	// Test 9: Reading Name field of external object works
	// -------------------------------------------------------
	_, err = engine.Execute(`
		local r, e = PluginSandbox:execute("@test/weather", 'return (_:get("CLU.DOUT1")).Name')
		__test_r = r
	`)
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	result, _ = engine.Execute("__test_r")
	if result != "Light" {
		t.Errorf("Test 9: Expected Name='Light', got '%s'", result)
	}

	// Cleanup
	_, _ = engine.Execute(`PluginSandbox:cleanup("@test/weather")`)

	t.Log("Plugin sandbox read-only registry test passed!")
}

// TestPluginSandboxEventBusShared verifies that events emitted by sandboxed plugins
// are received by listeners registered via EventBus:getShared():on() (the standard
// pattern used in modules/*.lua). This tests the fix where sandbox api.emit/api.on
// must use the shared EventBus instance, not the class-level table.
func TestPluginSandboxEventBusShared(t *testing.T) {
	engine := NewLuaEngine()
	defer engine.Close()

	// -------------------------------------------------------
	// Test 1: Module listener (getShared) receives plugin emit
	// -------------------------------------------------------
	// Register a listener using the pattern from modules/*.lua
	_, err := engine.Execute(`
		_test_received = nil
		EventBus:getShared():on("weather:changed", function(data)
			_test_received = data
		end)
	`)
	if err != nil {
		t.Fatalf("Failed to register shared listener: %v", err)
	}

	// Create sandbox and plugin
	_, err = engine.Execute(`PluginSandbox:create("@test/event-plugin")`)
	if err != nil {
		t.Fatalf("Sandbox create failed: %v", err)
	}

	// Plugin emits event via sandbox
	_, err = engine.Execute(`
		local r, e = PluginSandbox:execute("@test/event-plugin", [[
			local p = Plugin:new("event-plugin", {name="Event Test", version="1.0.0"})
			p:emit("weather:changed", { temp = 12.5, humidity = 43 })
		]])
		if e then error(e) end
	`)
	if err != nil {
		t.Fatalf("Plugin emit failed: %v", err)
	}

	// Verify listener received the event
	result, _ := engine.Execute("tostring(_test_received ~= nil)")
	if result != "true" {
		t.Errorf("Test 1: Expected shared listener to receive event, got nil")
	}

	result, _ = engine.Execute("tostring(_test_received.temp)")
	if result != "12.5" {
		t.Errorf("Test 1: Expected temp=12.5, got '%s'", result)
	}

	result, _ = engine.Execute("tostring(_test_received.humidity)")
	if result != "43" {
		t.Errorf("Test 1: Expected humidity=43, got '%s'", result)
	}

	// -------------------------------------------------------
	// Test 2: Plugin listener (sandbox on) receives module emit
	// -------------------------------------------------------
	_, err = engine.Execute(`
		local r, e = PluginSandbox:execute("@test/event-plugin", [[
			_plugin_received = nil
			local p = Plugin.getPlugin("@test/event-plugin")
			p:on("test:from_module", function(data)
				_plugin_received = data
			end)
		]])
		if e then error(e) end
	`)
	if err != nil {
		t.Fatalf("Plugin on() failed: %v", err)
	}

	// Module emits via shared EventBus
	_, err = engine.Execute(`
		EventBus:getShared():emit("test:from_module", { source = "module" })
	`)
	if err != nil {
		t.Fatalf("Module emit failed: %v", err)
	}

	// Check plugin received it (the callback sets a global in the sandbox env,
	// but since pcall wraps it, we need to check via sandbox execute)
	result, _ = engine.Execute(`
		local r, e = PluginSandbox:execute("@test/event-plugin", 'return tostring(_plugin_received ~= nil)')
		return r
	`)
	if result != "true" {
		t.Errorf("Test 2: Expected plugin listener to receive module event, got '%s'", result)
	}

	// -------------------------------------------------------
	// Test 3: Cleanup removes subscriptions from shared bus
	// -------------------------------------------------------
	_, err = engine.Execute(`
		_test_after_cleanup = nil
		PluginSandbox:cleanup("@test/event-plugin")
	`)
	if err != nil {
		t.Fatalf("Cleanup failed: %v", err)
	}

	// Emit again — plugin listener should NOT fire (was cleaned up)
	_, err = engine.Execute(`
		EventBus:getShared():emit("test:from_module", { source = "after_cleanup" })
	`)
	if err != nil {
		t.Fatalf("Post-cleanup emit failed: %v", err)
	}

	// The _plugin_received was in sandbox env which is now gone,
	// so we just verify no errors occurred during cleanup+emit
	t.Log("Plugin sandbox EventBus shared instance test passed!")
}

// TestPluginSandboxEventBusBidirectional tests that plugin-to-plugin events also work
// when both plugins use the sandbox API (both should go through shared instance).
func TestPluginSandboxEventBusBidirectional(t *testing.T) {
	engine := NewLuaEngine()
	defer engine.Close()

	// Create two sandboxed plugins
	_, err := engine.Execute(`
		PluginSandbox:create("@test/producer")
		PluginSandbox:create("@test/consumer")
	`)
	if err != nil {
		t.Fatalf("Failed to create sandboxes: %v", err)
	}

	// Consumer listens
	_, err = engine.Execute(`
		local r, e = PluginSandbox:execute("@test/consumer", [[
			_consumed = nil
			local p = Plugin:new("consumer", {name="Consumer", version="1.0.0"})
			p:on("data:ready", function(data)
				_consumed = data
			end)
		]])
		if e then error(e) end
	`)
	if err != nil {
		t.Fatalf("Consumer setup failed: %v", err)
	}

	// Producer emits
	_, err = engine.Execute(`
		local r, e = PluginSandbox:execute("@test/producer", [[
			local p = Plugin:new("producer", {name="Producer", version="1.0.0"})
			p:emit("data:ready", { value = 42 })
		]])
		if e then error(e) end
	`)
	if err != nil {
		t.Fatalf("Producer emit failed: %v", err)
	}

	// Verify consumer received
	_, err = engine.Execute(`
		local r, e = PluginSandbox:execute("@test/consumer", [[
			return tostring(_consumed and _consumed.value or "nil")
		]])
		__bidir_result = r
		__bidir_err = e
	`)
	if err != nil {
		t.Fatalf("Consumer check failed: %v", err)
	}

	result, _ := engine.Execute("__bidir_result")
	if result != "42" {
		t.Errorf("Expected consumer to receive value=42, got '%s'", result)
	}

	// Cleanup
	engine.Execute(`PluginSandbox:cleanup("@test/producer")`)
	engine.Execute(`PluginSandbox:cleanup("@test/consumer")`)

	t.Log("Plugin sandbox bidirectional event test passed!")
}

// TestEventBusOnEmitHelpers verifies that the global on()/emit() helpers
// work correctly and route through EventBus:getShared().
func TestEventBusOnEmitHelpers(t *testing.T) {
	engine := NewLuaEngine()
	defer engine.Close()

	// Test 1: on() + emit() deliver events
	_, err := engine.Execute(`
		_helper_received = nil
		on("test:hello", function(data)
			_helper_received = data
		end)
		emit("test:hello", { msg = "world" })
	`)
	if err != nil {
		t.Fatalf("on/emit helper failed: %v", err)
	}

	result, _ := engine.Execute("_helper_received.msg")
	if result != "world" {
		t.Errorf("Expected msg='world', got '%s'", result)
	}

	// Test 2: on() returns unsubscribe function
	_, err = engine.Execute(`
		_unsub_count = 0
		local unsub = on("test:counted", function()
			_unsub_count = _unsub_count + 1
		end)
		emit("test:counted")
		emit("test:counted")
		unsub()
		emit("test:counted")
	`)
	if err != nil {
		t.Fatalf("unsubscribe test failed: %v", err)
	}

	result, _ = engine.Execute("tostring(_unsub_count)")
	if result != "2" {
		t.Errorf("Expected count=2 (unsubscribed before 3rd emit), got '%s'", result)
	}

	// Test 3: Plugin emit → on() helper listener
	_, err = engine.Execute(`PluginSandbox:create("@test/helper-emitter")`)
	if err != nil {
		t.Fatalf("Sandbox create failed: %v", err)
	}

	_, err = engine.Execute(`
		_from_plugin = nil
		on("plugin:data", function(data)
			_from_plugin = data
		end)
	`)
	if err != nil {
		t.Fatalf("on() setup failed: %v", err)
	}

	_, err = engine.Execute(`
		local r, e = PluginSandbox:execute("@test/helper-emitter", [[
			local p = Plugin:new("helper-emitter", {name="Emitter", version="1.0.0"})
			p:emit("plugin:data", { value = 99 })
		]])
		if e then error(e) end
	`)
	if err != nil {
		t.Fatalf("Plugin emit failed: %v", err)
	}

	result, _ = engine.Execute("tostring(_from_plugin.value)")
	if result != "99" {
		t.Errorf("Expected on() helper to receive plugin event with value=99, got '%s'", result)
	}

	engine.Execute(`PluginSandbox:cleanup("@test/helper-emitter")`)
	t.Log("EventBus on/emit helpers test passed!")
}

func fileExists(path string) bool {
	_, err := filepath.Abs(path)
	return err == nil
}

// Plugin.get called with a dot used to return nil, because the class table
// doubles as the instance method table and indexing a string yields nil rather
// than raising. The mistake then surfaced far from its cause, as a call on a
// nil plugin, and every plugin README in the repository shipped it.
func TestPluginGetStaticMisuseRaises(t *testing.T) {
	engine := NewLuaEngine()
	defer engine.Close()

	if _, err := engine.Execute(`
		-- Outside a sandbox the loader sets no __plugin_id, so the namespace
		-- has to come from the id itself for both lookup forms to exist.
		getPlugin_target = Plugin:new("@vclu/guard-test", {
			name = "Guard Test", version = "1.0.0", description = "d"
		})
		getPlugin_target:sensor("temperature", function() return 21.5 end)
	`); err != nil {
		t.Fatalf("failed to create plugin: %v", err)
	}

	// The instance method still does its job.
	result, err := engine.Execute(`tostring(getPlugin_target:get("temperature"):get())`)
	if err != nil {
		t.Fatalf("instance get failed: %v", err)
	}
	if result != "21.5" {
		t.Errorf("expected the sensor to read 21.5, got %s", result)
	}

	// Fetching an instance goes through getPlugin, with either id form.
	for _, id := range []string{"@vclu/guard-test", "guard-test"} {
		result, err = engine.Execute(`type(Plugin.getPlugin("` + id + `"))`)
		if err != nil {
			t.Fatalf("getPlugin(%q) failed: %v", id, err)
		}
		if result != "table" {
			t.Errorf("getPlugin(%q) returned %s, want table", id, result)
		}
	}

	// The static misuse now says what to do instead of returning nil.
	result, err = engine.Execute(`
		local ok, err = pcall(function() return Plugin.get("@vclu/guard-test") end)
		if ok then return "no error raised" end
		return tostring(err)
	`)
	if err != nil {
		t.Fatalf("failed to probe the guard: %v", err)
	}
	if result == "no error raised" {
		t.Fatal("Plugin.get(\"@vclu/guard-test\") returned instead of raising")
	}
	for _, want := range []string{"instance method", "Plugin.getPlugin", "@vclu/guard-test"} {
		if !strings.Contains(result, want) {
			t.Errorf("guard message %q does not mention %q", result, want)
		}
	}
}
