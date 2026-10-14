package lua

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/Domktorymysli/vclu/pkg/gpio"
)

// ==================== ExposedObjects Module Tests ====================

func TestExposedObjectsExists(t *testing.T) {
	engine := NewLuaEngine()
	defer engine.Close()

	// Test ExposedObjects exists and is a table
	result, err := engine.Execute("type(ExposedObjects)")
	if err != nil {
		t.Fatalf("ExposedObjects check failed: %v", err)
	}
	if result != "table" {
		t.Errorf("ExposedObjects type is %q, expected 'table'", result)
	}

	// Test expose global function exists
	result, err = engine.Execute("type(expose)")
	if err != nil {
		t.Fatalf("expose function check failed: %v", err)
	}
	if result != "function" {
		t.Errorf("expose type is %q, expected 'function'", result)
	}

	// Test exposeRegistry global function exists
	result, err = engine.Execute("type(exposeRegistry)")
	if err != nil {
		t.Fatalf("exposeRegistry function check failed: %v", err)
	}
	if result != "function" {
		t.Errorf("exposeRegistry type is %q, expected 'function'", result)
	}
}

func TestExposeBasic(t *testing.T) {
	engine := NewLuaEngine()
	defer engine.Close()

	// Create a mock object and expose it
	setup := `
		mockObj = {
			_lastState = 0,
			get = function(self, idx) return self._lastState end,
			set = function(self, idx, val) self._lastState = val end,
		}
		handle = expose(mockObj, "switch", { name = "Test Switch", path = "test:switch1" })
	`
	_, err := engine.Execute(setup)
	if err != nil {
		t.Fatalf("Setup failed: %v", err)
	}

	// Check handle is returned
	result, err := engine.Execute("handle ~= nil")
	if err != nil {
		t.Fatalf("handle check failed: %v", err)
	}
	if result != "true" {
		t.Errorf("handle is nil")
	}

	// Check path
	result, err = engine.Execute("handle:getPath()")
	if err != nil {
		t.Fatalf("getPath failed: %v", err)
	}
	if result != "test:switch1" {
		t.Errorf("path is %q, expected 'test:switch1'", result)
	}

	// Check isExposed
	result, err = engine.Execute("handle:isExposed()")
	if err != nil {
		t.Fatalf("isExposed failed: %v", err)
	}
	if result != "true" {
		t.Errorf("isExposed is %q, expected 'true'", result)
	}

	// Check count
	result, err = engine.Execute("ExposedObjects:count()")
	if err != nil {
		t.Fatalf("count failed: %v", err)
	}
	if result != "1" {
		t.Errorf("count is %q, expected '1'", result)
	}
}

func TestExposeFluentMethods(t *testing.T) {
	engine := NewLuaEngine()
	defer engine.Close()

	// Create object and use fluent methods
	setup := `
		mockObj = {
			get = function(self, idx) return 0 end,
			set = function(self, idx, val) end,
		}
		handle = expose(mockObj, "switch", { path = "test:fluent" })
			:name("My Light")
			:readonly()
			:group("Living Room")
	`
	_, err := engine.Execute(setup)
	if err != nil {
		t.Fatalf("Setup failed: %v", err)
	}

	// Check name
	result, err := engine.Execute("handle.options.name")
	if err != nil {
		t.Fatalf("name check failed: %v", err)
	}
	if result != "My Light" {
		t.Errorf("name is %q, expected 'My Light'", result)
	}

	// Check readonly
	result, err = engine.Execute("handle.options.readonly")
	if err != nil {
		t.Fatalf("readonly check failed: %v", err)
	}
	if result != "true" {
		t.Errorf("readonly is %q, expected 'true'", result)
	}

	// Check group
	result, err = engine.Execute("handle.options.group")
	if err != nil {
		t.Fatalf("group check failed: %v", err)
	}
	if result != "Living Room" {
		t.Errorf("group is %q, expected 'Living Room'", result)
	}
}

func TestExposeGetSetValue(t *testing.T) {
	engine := NewLuaEngine()
	defer engine.Close()

	// Create object with get/set
	setup := `
		mockObj = {
			_value = 50,
			get = function(self, idx) return self._value end,
			set = function(self, idx, val) self._value = val end,
		}
		handle = expose(mockObj, "dimmer", { path = "test:dimmer1" })
	`
	_, err := engine.Execute(setup)
	if err != nil {
		t.Fatalf("Setup failed: %v", err)
	}

	// Get initial value
	result, err := engine.Execute("handle:getValue()")
	if err != nil {
		t.Fatalf("getValue failed: %v", err)
	}
	if result != "50" {
		t.Errorf("initial value is %q, expected '50'", result)
	}

	// Set value
	_, err = engine.Execute("handle:setValue(75)")
	if err != nil {
		t.Fatalf("setValue failed: %v", err)
	}

	// Check new value
	result, err = engine.Execute("handle:getValue()")
	if err != nil {
		t.Fatalf("getValue after set failed: %v", err)
	}
	if result != "75" {
		t.Errorf("value after set is %q, expected '75'", result)
	}

	// Verify object was updated
	result, err = engine.Execute("mockObj._value")
	if err != nil {
		t.Fatalf("mockObj._value check failed: %v", err)
	}
	if result != "75" {
		t.Errorf("mockObj._value is %q, expected '75'", result)
	}
}

func TestExposeReadonlyPreventsSetValue(t *testing.T) {
	engine := NewLuaEngine()
	defer engine.Close()

	// Create readonly object
	setup := `
		mockObj = {
			_value = 100,
			get = function(self, idx) return self._value end,
			set = function(self, idx, val) self._value = val end,
		}
		handle = expose(mockObj, "sensor", { path = "test:sensor1" }):readonly()
	`
	_, err := engine.Execute(setup)
	if err != nil {
		t.Fatalf("Setup failed: %v", err)
	}

	// Try to set value (should fail)
	result, err := engine.Execute("handle:setValue(200)")
	if err != nil {
		t.Fatalf("setValue failed: %v", err)
	}
	if result != "false" {
		t.Errorf("setValue on readonly returned %q, expected 'false'", result)
	}

	// Value should be unchanged
	result, err = engine.Execute("mockObj._value")
	if err != nil {
		t.Fatalf("mockObj._value check failed: %v", err)
	}
	if result != "100" {
		t.Errorf("mockObj._value is %q, expected '100' (unchanged)", result)
	}
}

func TestExposeUnexpose(t *testing.T) {
	engine := NewLuaEngine()
	defer engine.Close()

	// Create and expose object
	setup := `
		mockObj = {
			get = function(self, idx) return 0 end,
			set = function(self, idx, val) end,
		}
		handle = expose(mockObj, "switch", { path = "test:unexpose" })
	`
	_, err := engine.Execute(setup)
	if err != nil {
		t.Fatalf("Setup failed: %v", err)
	}

	// Verify exposed
	result, err := engine.Execute("handle:isExposed()")
	if err != nil {
		t.Fatalf("isExposed check failed: %v", err)
	}
	if result != "true" {
		t.Errorf("isExposed should be true before unexpose")
	}

	// Count before unexpose
	result, err = engine.Execute("ExposedObjects:count()")
	if err != nil {
		t.Fatalf("count failed: %v", err)
	}
	if result != "1" {
		t.Errorf("count is %q, expected '1'", result)
	}

	// Unexpose
	_, err = engine.Execute("handle:unexpose()")
	if err != nil {
		t.Fatalf("unexpose failed: %v", err)
	}

	// Verify unexposed
	result, err = engine.Execute("handle:isExposed()")
	if err != nil {
		t.Fatalf("isExposed after unexpose failed: %v", err)
	}
	if result != "false" {
		t.Errorf("isExposed should be false after unexpose")
	}

	// Count after unexpose
	result, err = engine.Execute("ExposedObjects:count()")
	if err != nil {
		t.Fatalf("count after unexpose failed: %v", err)
	}
	if result != "0" {
		t.Errorf("count is %q after unexpose, expected '0'", result)
	}

	// Operations should fail on unexposed handle
	result, err = engine.Execute("handle:getValue()")
	if err != nil {
		t.Fatalf("getValue on unexposed failed: %v", err)
	}
	if result != "nil" {
		t.Errorf("getValue on unexposed should return nil, got %q", result)
	}
}

func TestExposeUnexposeIdempotent(t *testing.T) {
	engine := NewLuaEngine()
	defer engine.Close()

	// Create, expose, and unexpose multiple times
	setup := `
		mockObj = {
			get = function(self, idx) return 0 end,
		}
		handle = expose(mockObj, "switch", { path = "test:idempotent" })
		handle:unexpose()
		handle:unexpose()  -- Should not error
		handle:unexpose()  -- Should not error
	`
	_, err := engine.Execute(setup)
	if err != nil {
		t.Fatalf("Multiple unexpose failed: %v", err)
	}

	// Verify still unexposed
	result, err := engine.Execute("handle:isExposed()")
	if err != nil {
		t.Fatalf("isExposed check failed: %v", err)
	}
	if result != "false" {
		t.Errorf("isExposed should be false")
	}
}

func TestExposePathFromRegistry(t *testing.T) {
	engine := NewLuaEngine()
	defer engine.Close()

	// Setup: create object in registry
	setup := `
		local mockCLU = { execute = function() end }
		testObj = RemoteSwitch:new(mockCLU, "DOU1234", "test_switch")
		registerObject("CLU123.DOU1234", testObj)

		-- Expose the registry object
		handle = expose(testObj, "switch", { name = "My Switch" })
	`
	_, err := engine.Execute(setup)
	if err != nil {
		t.Fatalf("Setup failed: %v", err)
	}

	// Path should be the registry path (CLU123.DOU1234), not generated hash
	result, err := engine.Execute("handle:getPath()")
	if err != nil {
		t.Fatalf("getPath failed: %v", err)
	}
	if result != "CLU123.DOU1234" {
		t.Errorf("path is %q, expected 'CLU123.DOU1234' (registry path)", result)
	}
}

func TestExposePathWithPluginContext(t *testing.T) {
	engine := NewLuaEngine()
	defer engine.Close()

	// Set plugin context
	setup := `
		ExposedObjects:setPluginContext("@my/plugin")
		mockObj = {
			_id = "sensor1",
			get = function(self, idx) return 0 end,
		}
		handle = expose(mockObj, "sensor")
		ExposedObjects:setPluginContext(nil)  -- Clear context
	`
	_, err := engine.Execute(setup)
	if err != nil {
		t.Fatalf("Setup failed: %v", err)
	}

	// Path should include plugin context
	result, err := engine.Execute("handle:getPath()")
	if err != nil {
		t.Fatalf("getPath failed: %v", err)
	}
	if result != "@my/plugin:sensor1" {
		t.Errorf("path is %q, expected '@my/plugin:sensor1'", result)
	}
}

func TestExposeIdempotent(t *testing.T) {
	engine := NewLuaEngine()
	defer engine.Close()

	// Expose same object twice
	setup := `
		mockObj = {
			get = function(self, idx) return 0 end,
		}
		handle1 = expose(mockObj, "switch", { path = "test:idempotent", name = "First" })
		handle2 = expose(mockObj, "switch", { name = "Second" })  -- Same object, different options
	`
	_, err := engine.Execute(setup)
	if err != nil {
		t.Fatalf("Setup failed: %v", err)
	}

	// Should return same handle
	result, err := engine.Execute("handle1 == handle2")
	if err != nil {
		t.Fatalf("handle comparison failed: %v", err)
	}
	if result != "true" {
		t.Errorf("Re-exposing same object should return same handle")
	}

	// Name should be updated to "Second"
	result, err = engine.Execute("handle1.options.name")
	if err != nil {
		t.Fatalf("name check failed: %v", err)
	}
	if result != "Second" {
		t.Errorf("name is %q, expected 'Second' (updated by second expose)", result)
	}

	// Count should be 1
	result, err = engine.Execute("ExposedObjects:count()")
	if err != nil {
		t.Fatalf("count failed: %v", err)
	}
	if result != "1" {
		t.Errorf("count is %q, expected '1'", result)
	}
}

func TestExposePathCollision(t *testing.T) {
	engine := NewLuaEngine()
	defer engine.Close()

	// Expose two different objects with same path - should error
	setup := `
		obj1 = { get = function(self, idx) return 1 end }
		obj2 = { get = function(self, idx) return 2 end }
		expose(obj1, "switch", { path = "test:collision" })

		local ok, err = pcall(function()
			expose(obj2, "switch", { path = "test:collision" })
		end)
		collisionError = err
		collisionOk = ok
	`
	_, err := engine.Execute(setup)
	if err != nil {
		t.Fatalf("Setup failed: %v", err)
	}

	// Should have failed
	result, err := engine.Execute("collisionOk")
	if err != nil {
		t.Fatalf("collisionOk check failed: %v", err)
	}
	if result != "false" {
		t.Errorf("Path collision should have failed, but ok=%s", result)
	}

	// Error should mention collision
	result, err = engine.Execute("tostring(collisionError)")
	if err != nil {
		t.Fatalf("collisionError check failed: %v", err)
	}
	if !strings.Contains(result, "already in use") {
		t.Errorf("Error should mention path collision: %s", result)
	}
}

func TestExposeHidden(t *testing.T) {
	engine := NewLuaEngine()
	defer engine.Close()

	// Expose with hidden=true
	setup := `
		mockObj = {
			get = function(self, idx) return 0 end,
		}
		handle = expose(mockObj, "switch", { hidden = true })
	`
	_, err := engine.Execute(setup)
	if err != nil {
		t.Fatalf("Setup failed: %v", err)
	}

	// Handle should exist but not be exposed
	result, err := engine.Execute("handle ~= nil")
	if err != nil {
		t.Fatalf("handle check failed: %v", err)
	}
	if result != "true" {
		t.Errorf("handle should not be nil even when hidden")
	}

	// isExposed should be false
	result, err = engine.Execute("handle:isExposed()")
	if err != nil {
		t.Fatalf("isExposed check failed: %v", err)
	}
	if result != "false" {
		t.Errorf("isExposed should be false for hidden objects")
	}

	// Count should be 0
	result, err = engine.Execute("ExposedObjects:count()")
	if err != nil {
		t.Fatalf("count failed: %v", err)
	}
	if result != "0" {
		t.Errorf("count is %q, expected '0' (hidden objects not counted)", result)
	}
}

func TestExposeInvalidType(t *testing.T) {
	engine := NewLuaEngine()
	defer engine.Close()

	// Expose with invalid type
	setup := `
		mockObj = { get = function(self, idx) return 0 end }
		local ok, err = pcall(function()
			expose(mockObj, "invalid_type")
		end)
		typeError = err
		typeOk = ok
	`
	_, err := engine.Execute(setup)
	if err != nil {
		t.Fatalf("Setup failed: %v", err)
	}

	// Should have failed
	result, err := engine.Execute("typeOk")
	if err != nil {
		t.Fatalf("typeOk check failed: %v", err)
	}
	if result != "false" {
		t.Errorf("Invalid type should have failed")
	}

	// Error should mention unknown type
	result, err = engine.Execute("tostring(typeError)")
	if err != nil {
		t.Fatalf("typeError check failed: %v", err)
	}
	if !strings.Contains(result, "unknown type") {
		t.Errorf("Error should mention unknown type: %s", result)
	}
}

func TestExposeNilObject(t *testing.T) {
	engine := NewLuaEngine()
	defer engine.Close()

	// Expose nil object
	setup := `
		local ok, err = pcall(function()
			expose(nil, "switch")
		end)
		nilError = err
		nilOk = ok
	`
	_, err := engine.Execute(setup)
	if err != nil {
		t.Fatalf("Setup failed: %v", err)
	}

	// Should have failed
	result, err := engine.Execute("nilOk")
	if err != nil {
		t.Fatalf("nilOk check failed: %v", err)
	}
	if result != "false" {
		t.Errorf("Exposing nil should have failed")
	}

	// Error should mention nil
	result, err = engine.Execute("tostring(nilError)")
	if err != nil {
		t.Fatalf("nilError check failed: %v", err)
	}
	if !strings.Contains(result, "nil") {
		t.Errorf("Error should mention nil object: %s", result)
	}
}

func TestExposeToJSON(t *testing.T) {
	engine := NewLuaEngine()
	defer engine.Close()

	// Create and expose objects
	setup := `
		obj1 = {
			_value = 1,
			get = function(self, idx) return self._value end,
			set = function(self, idx, val) self._value = val end,
		}
		obj2 = {
			_value = 50,
			get = function(self, idx) return self._value end,
			set = function(self, idx, val) self._value = val end,
		}
		expose(obj1, "switch", { path = "test:json1", name = "Switch 1" })
		expose(obj2, "dimmer", { path = "test:json2", name = "Dimmer 1" })
	`
	_, err := engine.Execute(setup)
	if err != nil {
		t.Fatalf("Setup failed: %v", err)
	}

	// Get JSON
	result, err := engine.Execute("ExposedObjects:toJSON()")
	if err != nil {
		t.Fatalf("toJSON failed: %v", err)
	}

	// Parse JSON
	var objects []map[string]interface{}
	if err := json.Unmarshal([]byte(result), &objects); err != nil {
		t.Fatalf("Failed to parse JSON: %v - %s", err, result)
	}

	if len(objects) != 2 {
		t.Errorf("Expected 2 objects in JSON, got %d", len(objects))
	}

	// Check that both paths are present
	foundPaths := make(map[string]bool)
	for _, obj := range objects {
		if path, ok := obj["path"].(string); ok {
			foundPaths[path] = true
		}
	}

	if !foundPaths["test:json1"] {
		t.Errorf("Missing test:json1 in JSON output")
	}
	if !foundPaths["test:json2"] {
		t.Errorf("Missing test:json2 in JSON output")
	}
}

func TestExposeToJSONFilter(t *testing.T) {
	engine := NewLuaEngine()
	defer engine.Close()

	// Create objects with different mqtt/homekit settings
	setup := `
		obj1 = { get = function(self) return 0 end }
		obj2 = { get = function(self) return 0 end }
		obj3 = { get = function(self) return 0 end }

		expose(obj1, "switch", { path = "test:mqtt_only" }):mqttOnly()
		expose(obj2, "switch", { path = "test:homekit_only" }):homekitOnly()
		expose(obj3, "switch", { path = "test:both" })
	`
	_, err := engine.Execute(setup)
	if err != nil {
		t.Fatalf("Setup failed: %v", err)
	}

	// Filter for homekit only
	result, err := engine.Execute("ExposedObjects:toJSON({ homekit = true })")
	if err != nil {
		t.Fatalf("toJSON with homekit filter failed: %v", err)
	}

	var objects []map[string]interface{}
	if err := json.Unmarshal([]byte(result), &objects); err != nil {
		t.Fatalf("Failed to parse JSON: %v", err)
	}

	// Should have homekit_only and both, but not mqtt_only
	if len(objects) != 2 {
		t.Errorf("Expected 2 objects with homekit=true, got %d", len(objects))
	}

	for _, obj := range objects {
		path := obj["path"].(string)
		if path == "test:mqtt_only" {
			t.Errorf("mqtt_only should not be in homekit filter results")
		}
	}
}

func TestExposeCapabilities(t *testing.T) {
	engine := NewLuaEngine()
	defer engine.Close()

	// Test getCapabilities for various types
	testCases := []struct {
		objType string
		hasCap  string
	}{
		{"switch", "binary"},
		{"light", "write"}, // brightness comes from the object, see getHandleCapabilities
		{"dimmer", "brightness"},
		{"cover", "position"},
		{"scene", "execute"},
		{"sensor", "binary"},
	}

	for _, tc := range testCases {
		result, err := engine.Execute("ExposedObjects:getCapabilities('" + tc.objType + "')." + tc.hasCap)
		if err != nil {
			t.Fatalf("getCapabilities(%s).%s failed: %v", tc.objType, tc.hasCap, err)
		}
		if result != "true" {
			t.Errorf("Expected %s to have %s capability, got %s", tc.objType, tc.hasCap, result)
		}
	}
}

func TestExposeRename(t *testing.T) {
	engine := NewLuaEngine()
	defer engine.Close()

	// Create and expose object
	setup := `
		mockObj = { get = function(self) return 0 end }
		handle = expose(mockObj, "switch", { path = "test:rename", name = "Original" })
	`
	_, err := engine.Execute(setup)
	if err != nil {
		t.Fatalf("Setup failed: %v", err)
	}

	// Rename
	result, err := engine.Execute("handle:rename('New Name')")
	if err != nil {
		t.Fatalf("rename failed: %v", err)
	}
	if result != "true" {
		t.Errorf("rename should return true")
	}

	// Check new name
	result, err = engine.Execute("handle.options.name")
	if err != nil {
		t.Fatalf("name check failed: %v", err)
	}
	if result != "New Name" {
		t.Errorf("name is %q, expected 'New Name'", result)
	}
}

func TestExposeUpdate(t *testing.T) {
	engine := NewLuaEngine()
	defer engine.Close()

	// Create and expose object
	setup := `
		mockObj = { get = function(self) return 0 end }
		handle = expose(mockObj, "switch", { path = "test:update", name = "Original" })
	`
	_, err := engine.Execute(setup)
	if err != nil {
		t.Fatalf("Setup failed: %v", err)
	}

	// Update multiple options
	result, err := engine.Execute(`handle:update({ name = "Updated", group = "Kitchen" })`)
	if err != nil {
		t.Fatalf("update failed: %v", err)
	}
	if result != "true" {
		t.Errorf("update should return true")
	}

	// Check updated values
	result, err = engine.Execute("handle.options.name")
	if err != nil {
		t.Fatalf("name check failed: %v", err)
	}
	if result != "Updated" {
		t.Errorf("name is %q, expected 'Updated'", result)
	}

	result, err = engine.Execute("handle.options.group")
	if err != nil {
		t.Fatalf("group check failed: %v", err)
	}
	if result != "Kitchen" {
		t.Errorf("group is %q, expected 'Kitchen'", result)
	}
}

func TestExposeGetByPath(t *testing.T) {
	engine := NewLuaEngine()
	defer engine.Close()

	// Create and expose object
	setup := `
		mockObj = { get = function(self) return 42 end }
		expose(mockObj, "number", { path = "test:getbypath" })
	`
	_, err := engine.Execute(setup)
	if err != nil {
		t.Fatalf("Setup failed: %v", err)
	}

	// Get by path
	result, err := engine.Execute("ExposedObjects:get('test:getbypath') ~= nil")
	if err != nil {
		t.Fatalf("get failed: %v", err)
	}
	if result != "true" {
		t.Errorf("get should return handle")
	}

	// Get non-existent path
	result, err = engine.Execute("ExposedObjects:get('nonexistent') == nil")
	if err != nil {
		t.Fatalf("get nonexistent failed: %v", err)
	}
	if result != "true" {
		t.Errorf("get nonexistent should return nil")
	}
}

func TestExposeUnexposeByPlugin(t *testing.T) {
	engine := NewLuaEngine()
	defer engine.Close()

	// Create objects from different plugins
	setup := `
		obj1 = { get = function(self) return 0 end }
		obj2 = { get = function(self) return 0 end }
		obj3 = { get = function(self) return 0 end }

		ExposedObjects:setPluginContext("plugin-a")
		expose(obj1, "switch", { id = "sw1" })
		ExposedObjects:setPluginContext("plugin-b")
		expose(obj2, "switch", { id = "sw2" })
		ExposedObjects:setPluginContext(nil)
		expose(obj3, "switch", { path = "other:sw3" })
	`
	_, err := engine.Execute(setup)
	if err != nil {
		t.Fatalf("Setup failed: %v", err)
	}

	// Verify count before
	result, err := engine.Execute("ExposedObjects:count()")
	if err != nil {
		t.Fatalf("count failed: %v", err)
	}
	if result != "3" {
		t.Errorf("count is %q, expected '3'", result)
	}

	// Unexpose plugin-a
	_, err = engine.Execute("ExposedObjects:unexposeByPlugin('plugin-a')")
	if err != nil {
		t.Fatalf("unexposeByPlugin failed: %v", err)
	}

	// Count after
	result, err = engine.Execute("ExposedObjects:count()")
	if err != nil {
		t.Fatalf("count after unexposeByPlugin failed: %v", err)
	}
	if result != "2" {
		t.Errorf("count is %q after unexposeByPlugin, expected '2'", result)
	}

	// Check specific objects
	result, err = engine.Execute("ExposedObjects:get('plugin-a:sw1') == nil")
	if err != nil {
		t.Fatalf("get plugin-a:sw1 failed: %v", err)
	}
	if result != "true" {
		t.Errorf("plugin-a:sw1 should be removed")
	}

	result, err = engine.Execute("ExposedObjects:get('plugin-b:sw2') ~= nil")
	if err != nil {
		t.Fatalf("get plugin-b:sw2 failed: %v", err)
	}
	if result != "true" {
		t.Errorf("plugin-b:sw2 should still exist")
	}
}

func TestExposeList(t *testing.T) {
	engine := NewLuaEngine()
	defer engine.Close()

	// Create multiple objects
	setup := `
		obj1 = { get = function(self) return 0 end }
		obj2 = { get = function(self) return 0 end }
		expose(obj1, "switch", { path = "test:list1" })
		expose(obj2, "switch", { path = "test:list2" })
	`
	_, err := engine.Execute(setup)
	if err != nil {
		t.Fatalf("Setup failed: %v", err)
	}

	// List should return table
	result, err := engine.Execute("type(ExposedObjects:list())")
	if err != nil {
		t.Fatalf("list failed: %v", err)
	}
	if result != "table" {
		t.Errorf("list should return table, got %s", result)
	}

	// Check specific paths exist in list
	result, err = engine.Execute("ExposedObjects:list()['test:list1'] ~= nil")
	if err != nil {
		t.Fatalf("list check failed: %v", err)
	}
	if result != "true" {
		t.Errorf("test:list1 should be in list")
	}
}

func TestExposeSceneWithCooldown(t *testing.T) {
	engine := NewLuaEngine()
	defer engine.Close()

	// Create scene object
	setup := `
		executeCount = 0
		sceneObj = {
			execute = function(self, cmd)
				executeCount = executeCount + 1
			end,
		}
		handle = expose(sceneObj, "scene", { path = "test:scene" })
		handle._cooldownMs = 100  -- Short cooldown for testing
	`
	_, err := engine.Execute(setup)
	if err != nil {
		t.Fatalf("Setup failed: %v", err)
	}

	// Execute scene once
	_, err = engine.Execute("handle:executeScene()")
	if err != nil {
		t.Fatalf("executeScene failed: %v", err)
	}

	result, err := engine.Execute("executeCount")
	if err != nil {
		t.Fatalf("executeCount check failed: %v", err)
	}
	if result != "1" {
		t.Errorf("executeCount is %q, expected '1'", result)
	}

	// Try to execute immediately (should be blocked by cooldown)
	result, err = engine.Execute("handle:executeScene()")
	if err != nil {
		t.Fatalf("second executeScene failed: %v", err)
	}
	if result != "false" {
		t.Errorf("second executeScene should return false (cooldown)")
	}

	// Count should still be 1
	result, err = engine.Execute("executeCount")
	if err != nil {
		t.Fatalf("executeCount check failed: %v", err)
	}
	if result != "1" {
		t.Errorf("executeCount is %q after cooldown block, expected '1'", result)
	}
}

func TestExposeRegistryAutoExpose(t *testing.T) {
	engine := NewLuaEngine()
	defer engine.Close()

	// Setup: create plain objects in registry (without expose() method)
	// This tests the fallback path in exposeRegistry for objects that don't have expose()
	setup := `
		obj1 = {
			_type = "DOUT",
			_name = "switch1",
			get = function(self, idx) return 0 end,
			set = function(self, idx, val) end,
		}
		obj2 = {
			_type = "DOUT",
			_name = "switch2",
			get = function(self, idx) return 0 end,
			set = function(self, idx, val) end,
		}
		obj3 = {
			_type = "DIN",
			_name = "sensor1",
			get = function(self, idx) return 0 end,
		}
		_registry["CLU123.DOU1"] = obj1
		_registry["CLU123.DOU2"] = obj2
		_registry["CLU123.DIN1"] = obj3
	`
	_, err := engine.Execute(setup)
	if err != nil {
		t.Fatalf("Setup failed: %v", err)
	}

	// Before exposeRegistry
	result, err := engine.Execute("ExposedObjects:count()")
	if err != nil {
		t.Fatalf("count before failed: %v", err)
	}
	if result != "0" {
		t.Errorf("count before exposeRegistry is %q, expected '0'", result)
	}

	// Call exposeRegistry
	result, err = engine.Execute("exposeRegistry()")
	if err != nil {
		t.Fatalf("exposeRegistry failed: %v", err)
	}

	// Should have exposed 3 objects
	if result != "3" {
		t.Errorf("exposeRegistry returned %q, expected '3'", result)
	}

	// Verify count
	result, err = engine.Execute("ExposedObjects:count()")
	if err != nil {
		t.Fatalf("count after failed: %v", err)
	}
	if result != "3" {
		t.Errorf("count after exposeRegistry is %q, expected '3'", result)
	}

	// Verify paths match registry paths
	result, err = engine.Execute("ExposedObjects:get('CLU123.DOU1') ~= nil")
	if err != nil {
		t.Fatalf("get CLU123.DOU1 failed: %v", err)
	}
	if result != "true" {
		t.Errorf("CLU123.DOU1 should be exposed")
	}
}

func TestRemoteObjectExpose(t *testing.T) {
	engine := NewLuaEngine()
	defer engine.Close()

	// Setup: create RemoteSwitch and register it
	setup := `
		local mockCLU = { execute = function() end }
		testSwitch = RemoteSwitch:new(mockCLU, "DOU1234", "my_switch")
		registerObject("CLU999.DOU1234", testSwitch)
	`
	_, err := engine.Execute(setup)
	if err != nil {
		t.Fatalf("Setup failed: %v", err)
	}

	// Call expose() on the RemoteObject
	_, err = engine.Execute("handle = testSwitch:expose({ name = 'Kitchen Light' })")
	if err != nil {
		t.Fatalf("expose failed: %v", err)
	}

	// Verify handle is returned
	result, err := engine.Execute("handle ~= nil")
	if err != nil {
		t.Fatalf("handle check failed: %v", err)
	}
	if result != "true" {
		t.Errorf("handle should not be nil")
	}

	// Path should be registry path
	result, err = engine.Execute("handle:getPath()")
	if err != nil {
		t.Fatalf("getPath failed: %v", err)
	}
	if result != "CLU999.DOU1234" {
		t.Errorf("path is %q, expected 'CLU999.DOU1234'", result)
	}

	// Name should be set
	result, err = engine.Execute("handle.options.name")
	if err != nil {
		t.Fatalf("name check failed: %v", err)
	}
	if result != "Kitchen Light" {
		t.Errorf("name is %q, expected 'Kitchen Light'", result)
	}

	// isExposed() should work on RemoteObject
	result, err = engine.Execute("testSwitch:isExposed()")
	if err != nil {
		t.Fatalf("isExposed failed: %v", err)
	}
	if result != "true" {
		t.Errorf("testSwitch:isExposed() should be true")
	}

	// Re-expose should be idempotent
	_, err = engine.Execute("handle2 = testSwitch:expose()")
	if err != nil {
		t.Fatalf("re-expose failed: %v", err)
	}
	result, err = engine.Execute("handle == handle2")
	if err != nil {
		t.Fatalf("handle comparison failed: %v", err)
	}
	if result != "true" {
		t.Errorf("re-expose should return same handle")
	}

	// Unexpose via RemoteObject
	_, err = engine.Execute("testSwitch:unexpose()")
	if err != nil {
		t.Fatalf("unexpose failed: %v", err)
	}

	result, err = engine.Execute("testSwitch:isExposed()")
	if err != nil {
		t.Fatalf("isExposed after unexpose failed: %v", err)
	}
	if result != "false" {
		t.Errorf("testSwitch:isExposed() should be false after unexpose")
	}
}

func TestExposeToJSONCaps(t *testing.T) {
	engine := NewLuaEngine()
	defer engine.Close()

	// Create objects with different capabilities
	setup := `
		obj1 = { get = function(self) return 1 end }
		obj2 = { get = function(self) return 50 end }
		obj3 = { get = function(self) return 0 end }

		expose(obj1, "switch", { path = "test:caps_switch" })
		expose(obj2, "dimmer", { path = "test:caps_dimmer" })
		expose(obj3, "cover", { path = "test:caps_cover" })
	`
	_, err := engine.Execute(setup)
	if err != nil {
		t.Fatalf("Setup failed: %v", err)
	}

	// Get JSON
	result, err := engine.Execute("ExposedObjects:toJSON()")
	if err != nil {
		t.Fatalf("toJSON failed: %v", err)
	}

	// Parse and verify caps
	var objects []map[string]interface{}
	if err := json.Unmarshal([]byte(result), &objects); err != nil {
		t.Fatalf("Failed to parse JSON: %v - %s", err, result)
	}

	// Find each object and check caps
	for _, obj := range objects {
		path := obj["path"].(string)
		caps := obj["caps"].(map[string]interface{})

		switch path {
		case "test:caps_switch":
			if _, ok := caps["binary"]; !ok {
				t.Errorf("switch should have binary cap")
			}
		case "test:caps_dimmer":
			if _, ok := caps["brightness"]; !ok {
				t.Errorf("dimmer should have brightness cap")
			}
		case "test:caps_cover":
			if _, ok := caps["position"]; !ok {
				t.Errorf("cover should have position cap")
			}
		}
	}
}

// ==================== GPIO + Registry/Expose Integration Tests ====================

func newGPIOExposeEngine(t *testing.T) *LuaEngine {
	t.Helper()
	ctrl := gpio.NewMockController(gpio.Config{BCMValidation: true})
	gm := NewGPIOManager(ctrl)
	engine := NewLuaEngine()
	engine.SetGPIOManager(gm)
	return engine
}

func TestGPIO_DOUT_RegistersInRegistry(t *testing.T) {
	engine := newGPIOExposeEngine(t)
	defer engine.Close()

	_, err := engine.Execute(`GPIO_DOUT:new("RELAY1", 17, {activeLow = true})`)
	if err != nil {
		t.Fatalf("GPIO_DOUT:new failed: %v", err)
	}

	result, err := engine.Execute(`_registry["gpio.RELAY1"] ~= nil`)
	if err != nil {
		t.Fatalf("registry check failed: %v", err)
	}
	if result != "true" {
		t.Fatal("expected GPIO_DOUT to be registered in _registry as gpio.RELAY1")
	}

	result, _ = engine.Execute(`_registry["gpio.RELAY1"]._type`)
	if result != "DOUT" {
		t.Fatalf("expected _type=DOUT, got %s", result)
	}

	result, _ = engine.Execute(`_registry["gpio.RELAY1"]._name`)
	if result != "RELAY1" {
		t.Fatalf("expected _name=RELAY1, got %s", result)
	}
}

func TestGPIO_DIN_RegistersInRegistry(t *testing.T) {
	engine := newGPIOExposeEngine(t)
	defer engine.Close()

	_, err := engine.Execute(`GPIO_DIN:new("BTN1", 27, "up")`)
	if err != nil {
		t.Fatalf("GPIO_DIN:new failed: %v", err)
	}

	result, err := engine.Execute(`_registry["gpio.BTN1"] ~= nil`)
	if err != nil {
		t.Fatalf("registry check failed: %v", err)
	}
	if result != "true" {
		t.Fatal("expected GPIO_DIN to be registered in _registry as gpio.BTN1")
	}

	result, _ = engine.Execute(`_registry["gpio.BTN1"]._type`)
	if result != "DIN" {
		t.Fatalf("expected _type=DIN, got %s", result)
	}

	result, _ = engine.Execute(`_registry["gpio.BTN1"]._name`)
	if result != "BTN1" {
		t.Fatalf("expected _name=BTN1, got %s", result)
	}
}

func TestGPIO_DOUT_RegistryToJSON(t *testing.T) {
	engine := newGPIOExposeEngine(t)
	defer engine.Close()

	_, err := engine.Execute(`GPIO_DOUT:new("RELAY1", 17)`)
	if err != nil {
		t.Fatalf("GPIO_DOUT:new failed: %v", err)
	}

	result, err := engine.Execute(`_registry:toJSON()`)
	if err != nil {
		t.Fatalf("toJSON failed: %v", err)
	}

	var entries []map[string]interface{}
	if err := json.Unmarshal([]byte(result), &entries); err != nil {
		t.Fatalf("JSON parse failed: %v - %s", err, result)
	}

	found := false
	for _, entry := range entries {
		if entry["path"] == "gpio.RELAY1" {
			found = true
			if entry["type"] != "DOUT" {
				t.Fatalf("expected type=DOUT, got %v", entry["type"])
			}
			break
		}
	}
	if !found {
		t.Fatalf("gpio.RELAY1 not found in registry JSON: %s", result)
	}
}

func TestGPIO_DOUT_ExposeRegistryAutoExposes(t *testing.T) {
	engine := newGPIOExposeEngine(t)
	defer engine.Close()

	setup := `
		GPIO_DOUT:new("RELAY1", 17)
		GPIO_DIN:new("BTN1", 27, "up")
		ExposedObjects:exposeRegistry()
	`
	_, err := engine.Execute(setup)
	if err != nil {
		t.Fatalf("setup failed: %v", err)
	}

	result, err := engine.Execute(`ExposedObjects:toJSON()`)
	if err != nil {
		t.Fatalf("toJSON failed: %v", err)
	}

	var objects []map[string]interface{}
	if err := json.Unmarshal([]byte(result), &objects); err != nil {
		t.Fatalf("JSON parse failed: %v - %s", err, result)
	}

	foundRelay := false
	foundButton := false
	for _, obj := range objects {
		path, _ := obj["path"].(string)
		objType, _ := obj["type"].(string)
		if strings.Contains(path, "RELAY1") {
			foundRelay = true
			if objType != "switch" {
				t.Errorf("expected RELAY1 type=switch, got %s", objType)
			}
		}
		if strings.Contains(path, "BTN1") {
			foundButton = true
			if objType != "sensor" {
				t.Errorf("expected BTN1 type=sensor, got %s", objType)
			}
		}
	}

	if !foundRelay {
		t.Errorf("RELAY1 not found in exposed objects after exposeRegistry()")
	}
	if !foundButton {
		t.Errorf("BTN1 not found in exposed objects after exposeRegistry()")
	}
}

func TestGPIO_DOUT_ManualExposeOverridesRegistry(t *testing.T) {
	engine := newGPIOExposeEngine(t)
	defer engine.Close()

	setup := `
		GPIO_DOUT:new("RELAY1", 17)
		ExposedObjects:expose(RELAY1, "switch", {name = "My Custom Relay"})
		ExposedObjects:exposeRegistry()
	`
	_, err := engine.Execute(setup)
	if err != nil {
		t.Fatalf("setup failed: %v", err)
	}

	result, err := engine.Execute(`ExposedObjects:toJSON()`)
	if err != nil {
		t.Fatalf("toJSON failed: %v", err)
	}

	var objects []map[string]interface{}
	if err := json.Unmarshal([]byte(result), &objects); err != nil {
		t.Fatalf("JSON parse failed: %v - %s", err, result)
	}

	// Should appear exactly once (manual expose, not duplicated by exposeRegistry)
	count := 0
	for _, obj := range objects {
		path, _ := obj["path"].(string)
		if strings.Contains(path, "RELAY1") {
			count++
			name, _ := obj["name"].(string)
			if name != "My Custom Relay" {
				t.Errorf("expected custom name 'My Custom Relay', got '%s'", name)
			}
		}
	}
	if count != 1 {
		t.Errorf("expected RELAY1 exactly once, found %d times", count)
	}
}

func TestGPIO_DOUT_CustomNameInRegistry(t *testing.T) {
	engine := newGPIOExposeEngine(t)
	defer engine.Close()

	_, err := engine.Execute(`GPIO_DOUT:new("RELAY1", 17, {name = "Kitchen Light"})`)
	if err != nil {
		t.Fatalf("GPIO_DOUT:new failed: %v", err)
	}

	result, _ := engine.Execute(`_registry["gpio.RELAY1"]._name`)
	if result != "Kitchen Light" {
		t.Fatalf("expected _name='Kitchen Light', got '%s'", result)
	}
}

// ==================== RemoteSwitch on() Method Collision Tests ====================
// Regression tests for critical bug: RemoteSwitch:on() overrode RemoteObject:on(eventName, callback),
// causing expose() to call switchOn() on every DOUT instead of registering onChange handlers.
// This turned on all relays in the house during Reload().

func TestRemoteSwitchOnWithNoArgs_SwitchesOn(t *testing.T) {
	engine := NewLuaEngine()
	defer engine.Close()

	// RemoteSwitch:on() with no args should call switchOn
	setup := `
		executeCalls = {}
		local mockCLU = {
			execute = function(self, mode, script)
				table.insert(executeCalls, script)
			end,
			executeNoReply = function(self, mode, script)
				table.insert(executeCalls, script)
			end
		}
		sw = RemoteSwitch:new(mockCLU, "DOU1234", "test")
		sw:on()
	`
	_, err := engine.Execute(setup)
	if err != nil {
		t.Fatalf("Setup failed: %v", err)
	}

	result, err := engine.Execute("#executeCalls")
	if err != nil {
		t.Fatalf("count check failed: %v", err)
	}
	if result != "1" {
		t.Errorf("on() with no args should have triggered 1 execute call, got %s", result)
	}

	result, err = engine.Execute("executeCalls[1]")
	if err != nil {
		t.Fatalf("script check failed: %v", err)
	}
	if !strings.Contains(result, "execute(1") {
		t.Errorf("on() should call execute(1, ...) for switchOn, got: %s", result)
	}
}

func TestRemoteSwitchOnEvent_RegistersEventHandler(t *testing.T) {
	engine := NewLuaEngine()
	defer engine.Close()

	// RemoteSwitch:onEvent(eventName, callback) should register event handler, NOT switch on
	setup := `
		executeCalls = {}
		callbackFired = false
		local mockCLU = {
			execute = function(self, mode, script)
				table.insert(executeCalls, script)
			end
		}
		sw = RemoteSwitch:new(mockCLU, "DOU1234", "test")
		sw:onEvent("OnChange", function() callbackFired = true end)
	`
	_, err := engine.Execute(setup)
	if err != nil {
		t.Fatalf("Setup failed: %v", err)
	}

	// onEvent() must NOT trigger any remote execute calls
	result, err := engine.Execute("#executeCalls")
	if err != nil {
		t.Fatalf("count check failed: %v", err)
	}
	if result != "0" {
		t.Errorf("onEvent(eventName, cb) must NOT trigger execute calls, but got %s calls", result)
	}

	// Verify handler was registered
	result, err = engine.Execute("sw._eventHandlers['OnChange'] ~= nil")
	if err != nil {
		t.Fatalf("handler check failed: %v", err)
	}
	if result != "true" {
		t.Errorf("onEvent('OnChange', cb) should register the handler")
	}
}

func TestRemoteSwitchOnChange_RegistersHandler_NotSwitchOn(t *testing.T) {
	engine := NewLuaEngine()
	defer engine.Close()

	// onChange(cb) calls self:on("OnChange", cb) — must NOT switchOn
	setup := `
		executeCalls = {}
		local mockCLU = {
			execute = function(self, mode, script)
				table.insert(executeCalls, script)
			end
		}
		sw = RemoteSwitch:new(mockCLU, "DOU1234", "test")
		sw:onChange(function() end)
	`
	_, err := engine.Execute(setup)
	if err != nil {
		t.Fatalf("Setup failed: %v", err)
	}

	// CRITICAL: onChange must NOT trigger switchOn
	result, err := engine.Execute("#executeCalls")
	if err != nil {
		t.Fatalf("count check failed: %v", err)
	}
	if result != "0" {
		script, _ := engine.Execute("executeCalls[1]")
		t.Fatalf("onChange() must NOT trigger execute calls, but got %s calls (script: %s)", result, script)
	}

	// Handler should be registered
	result, err = engine.Execute("#sw._eventHandlers['OnChange']")
	if err != nil {
		t.Fatalf("handler count check failed: %v", err)
	}
	if result != "1" {
		t.Errorf("onChange should register 1 handler, got %s", result)
	}
}

func TestRemoteSwitchOnSwitchOn_RegistersHandler_NotSwitchOn(t *testing.T) {
	engine := NewLuaEngine()
	defer engine.Close()

	// onSwitchOn(cb) calls self:on("OnSwitchOn", cb) — must NOT switchOn
	setup := `
		executeCalls = {}
		local mockCLU = {
			execute = function(self, mode, script)
				table.insert(executeCalls, script)
			end
		}
		sw = RemoteSwitch:new(mockCLU, "DOU1234", "test")
		sw:onSwitchOn(function() end)
	`
	_, err := engine.Execute(setup)
	if err != nil {
		t.Fatalf("Setup failed: %v", err)
	}

	// CRITICAL: onSwitchOn(cb) must NOT trigger switchOn
	result, err := engine.Execute("#executeCalls")
	if err != nil {
		t.Fatalf("count check failed: %v", err)
	}
	if result != "0" {
		t.Fatalf("onSwitchOn(cb) must NOT trigger execute calls, but got %s calls", result)
	}
}

func TestRemoteSwitchOnSwitchOff_RegistersHandler_NotSwitchOn(t *testing.T) {
	engine := NewLuaEngine()
	defer engine.Close()

	// onSwitchOff(cb) calls self:on("OnSwitchOff", cb) — must NOT switchOn
	setup := `
		executeCalls = {}
		local mockCLU = {
			execute = function(self, mode, script)
				table.insert(executeCalls, script)
			end
		}
		sw = RemoteSwitch:new(mockCLU, "DOU1234", "test")
		sw:onSwitchOff(function() end)
	`
	_, err := engine.Execute(setup)
	if err != nil {
		t.Fatalf("Setup failed: %v", err)
	}

	result, err := engine.Execute("#executeCalls")
	if err != nil {
		t.Fatalf("count check failed: %v", err)
	}
	if result != "0" {
		t.Fatalf("onSwitchOff(cb) must NOT trigger execute calls, but got %s calls", result)
	}
}

func TestRemoteSwitchExpose_DoesNotSwitchOn(t *testing.T) {
	engine := NewLuaEngine()
	defer engine.Close()

	// THE CRITICAL REGRESSION TEST: expose() on RemoteSwitch must NOT call switchOn
	// This is the exact scenario that turned on all relays in the house.
	setup := `
		executeCalls = {}
		local mockCLU = {
			execute = function(self, mode, script)
				table.insert(executeCalls, script)
			end
		}
		sw = RemoteSwitch:new(mockCLU, "DOU1234", "test")
		registerObject("CLU999.DOU1234", sw)
		sw:expose({ name = "Test Light" })
	`
	_, err := engine.Execute(setup)
	if err != nil {
		t.Fatalf("Setup failed: %v", err)
	}

	// CRITICAL: expose() must NOT trigger any switchOn (execute(1, ...)) calls
	result, err := engine.Execute("#executeCalls")
	if err != nil {
		t.Fatalf("count check failed: %v", err)
	}
	if result != "0" {
		// Show what was called to help debug
		script, _ := engine.Execute("executeCalls[1]")
		t.Fatalf("expose() on RemoteSwitch must NOT trigger remote execute calls!\n"+
			"Got %s call(s), first: %s\n"+
			"This bug caused all relays to turn on during Reload().", result, script)
	}
}

func TestRemoteLightExpose_DoesNotSwitchOn(t *testing.T) {
	engine := NewLuaEngine()
	defer engine.Close()

	// RemoteLight had the same on() collision as RemoteSwitch
	setup := `
		executeCalls = {}
		local mockCLU = {
			execute = function(self, mode, script)
				table.insert(executeCalls, script)
			end
		}
		light = RemoteLight:new(mockCLU, "DIM1234", "test")
		registerObject("CLU999.DIM1234", light)
		light:expose({ name = "Test Dimmer" })
	`
	_, err := engine.Execute(setup)
	if err != nil {
		t.Fatalf("Setup failed: %v", err)
	}

	// expose() must NOT trigger any switchOn calls
	result, err := engine.Execute("#executeCalls")
	if err != nil {
		t.Fatalf("count check failed: %v", err)
	}
	if result != "0" {
		script, _ := engine.Execute("executeCalls[1]")
		t.Fatalf("expose() on RemoteLight must NOT trigger remote execute calls!\n"+
			"Got %s call(s), first: %s", result, script)
	}
}

func TestRemoteLightOnChange_RegistersHandler_NotSwitchOn(t *testing.T) {
	engine := NewLuaEngine()
	defer engine.Close()

	// onChange(cb) on RemoteLight must register handler, NOT switchOn
	setup := `
		executeCalls = {}
		local mockCLU = {
			execute = function(self, mode, script)
				table.insert(executeCalls, script)
			end
		}
		light = RemoteLight:new(mockCLU, "DIM1234", "test")
		light:onChange(function() end)
	`
	_, err := engine.Execute(setup)
	if err != nil {
		t.Fatalf("Setup failed: %v", err)
	}

	result, err := engine.Execute("#executeCalls")
	if err != nil {
		t.Fatalf("count check failed: %v", err)
	}
	if result != "0" {
		script, _ := engine.Execute("executeCalls[1]")
		t.Fatalf("onChange() on RemoteLight must NOT trigger execute calls, got %s call(s): %s", result, script)
	}

	// Handler should be registered
	result, err = engine.Execute("#light._eventHandlers['OnChange']")
	if err != nil {
		t.Fatalf("handler count check failed: %v", err)
	}
	if result != "1" {
		t.Errorf("onChange should register 1 handler, got %s", result)
	}
}

func TestRemoteSwitchExposeRegistry_DoesNotSwitchOn(t *testing.T) {
	engine := NewLuaEngine()
	defer engine.Close()

	// exposeRegistry() with RemoteSwitch objects must NOT call switchOn
	setup := `
		executeCalls = {}
		local mockCLU = {
			execute = function(self, mode, script)
				table.insert(executeCalls, script)
			end
		}
		sw1 = RemoteSwitch:new(mockCLU, "DOU1111", "switch1")
		sw2 = RemoteSwitch:new(mockCLU, "DOU2222", "switch2")
		registerObject("CLU999.DOU1111", sw1)
		registerObject("CLU999.DOU2222", sw2)
		exposeRegistry()
	`
	_, err := engine.Execute(setup)
	if err != nil {
		t.Fatalf("Setup failed: %v", err)
	}

	result, err := engine.Execute("#executeCalls")
	if err != nil {
		t.Fatalf("count check failed: %v", err)
	}
	if result != "0" {
		script, _ := engine.Execute("table.concat(executeCalls, '; ')")
		t.Fatalf("exposeRegistry() must NOT trigger remote execute calls!\n"+
			"Got %s call(s): %s", result, script)
	}

	// Verify objects were actually exposed
	result, err = engine.Execute("ExposedObjects:count()")
	if err != nil {
		t.Fatalf("count check failed: %v", err)
	}
	if result != "2" {
		t.Errorf("Expected 2 exposed objects, got %s", result)
	}
}

func TestGPIO_DOUT_OnChangePropagatesToExpose(t *testing.T) {
	engine := newGPIOExposeEngine(t)
	defer engine.Close()

	setup := `
		_test_state_changes = {}
		GPIO_DOUT:new("RELAY1", 17)
		ExposedObjects:expose(RELAY1, "switch", {name = "Test Relay"})
		StateBus:getShared():on("state_changed", function(data)
			table.insert(_test_state_changes, data.path .. ":" .. tostring(data.value))
		end)
		RELAY1:execute(DOUT.METHOD_SWITCH_ON)
	`
	_, err := engine.Execute(setup)
	if err != nil {
		t.Fatalf("setup failed: %v", err)
	}

	result, _ := engine.Execute(`#_test_state_changes`)
	if result == "0" {
		t.Fatal("expected at least one state_changed event after SWITCH_ON")
	}
}

// captureOnChangeWarns collects formatted Logger warnings that mention onChange into _warns.
const captureOnChangeWarns = `
	_warns = {}
	Logger.warn = function(self, msg, ...)
		local s = self:format(msg, ...)
		if s:find("onChange", 1, true) then _warns[#_warns + 1] = s end
	end
`

// An object whose onChange returns no unsubscribe function keeps its callback after unexpose;
// expose() logs one warning with the path, once per object.
func TestExposeWarnsWhenOnChangeReturnsNoUnsubscribe(t *testing.T) {
	engine := NewLuaEngine()
	defer engine.Close()

	got, err := engine.Execute(captureOnChangeWarns + `
		local function custom(ret)
			return {
				get = function() return 0 end,
				onChange = function(self, cb) return ret end,
			}
		end
		local a = custom(nil)
		local h = expose(a, "switch", { path = "custom:a" })
		expose(a, "switch", { path = "custom:a" })
		h:unexpose()
		expose(a, "switch", { path = "custom:a" })
		expose(custom({}), "sensor", { path = "custom:b" })
		expose({ get = function() return 0 end }, "sensor", { path = "custom:no-onchange" })
		expose(custom(function() end), "sensor", { path = "custom:ok" })
		return table.concat(_warns, "\n")
	`)
	if err != nil {
		t.Fatalf("lua: %v", err)
	}
	warns := strings.Split(got, "\n")
	if got == "" || len(warns) != 2 {
		t.Fatalf("want one warning per object (custom:a, custom:b), got:\n%s", got)
	}
	if !strings.Contains(warns[0], "custom:a") || !strings.Contains(warns[1], "custom:b") {
		t.Errorf("warnings should name the paths custom:a and custom:b:\n%s", got)
	}
}

// Every kind of object the runtime provides returns an unsubscribe function: no warning.
func TestExposeCoreObjectsNoOnChangeWarning(t *testing.T) {
	engine := newGPIOExposeEngine(t)
	defer engine.Close()

	got, err := engine.Execute(captureOnChangeWarns + `
		local clu = { execute = function() end }
		local bus, mod = EventBus:getShared(), { address = "MOD1" }
		local p = Plugin:new("warn-test", { name = "Warn test" })
		local objs = {
			{ RemoteObject:new(clu, "OBJ0001", "remote_object"), "switch" },
			{ RemoteSwitch:new(clu, "DOU0001", "remote_switch"), "switch" },
			{ RemoteLight:new(clu, "DIM0001", "remote_light"), "dimmer" },
			{ RemoteRGBLight:new(clu, "LED0001", "remote_rgb"), "light" },
			{ RemoteCover:new(clu, "ROL0001", "remote_cover"), "cover" },
			{ RemoteNumber:new(clu, "NUM0001", "remote_number"), "number" },
			{ RemoteSensor:new(clu, "DIN0001", "remote_sensor"), "binary_sensor" },
			{ RemoteThermometer:new(clu, "TMP0001", "remote_thermo"), "temperature" },
			{ RemoteValueSensor:new(clu, "AIN0001", "remote_value", "humidity"), "humidity" },
			{ VirtualObject:new("virtual_object"), "sensor" },
			{ VirtualSwitch:new("virtual_switch"), "switch" },
			{ VirtualDimmer:new("virtual_dimmer"), "dimmer" },
			{ VirtualNumber:new("virtual_number"), "number" },
			{ VirtualSensor:new("virtual_sensor"), "sensor" },
			{ VirtualText:new("virtual_text"), "sensor" },
			{ VirtualCover:new("virtual_cover"), "cover" },
			{ DOUT:new("DOU0002", mod, 1, bus), "switch" },
			{ DIN:new("DIN0002", mod, 1, bus), "binary_sensor" },
			{ ANALOG_OUT:new("AOU0001", mod, 1, bus), "number" },
			{ GPIO_DOUT:new("RELAY1", 17), "switch" },
			{ GPIO_DIN:new("BTN1", 27, "up"), "binary_sensor" },
			{ p:sensor("temp", function() return 21 end), "temperature" },
			{ p:control("speed", function() return 1 end, function() end), "number" },
		}
		local c = { n = 0 }
		for i, o in ipairs(objs) do
			assert(type(o[1].onChange) == "function", "object " .. i .. " has no onChange")
			expose(o[1], o[2], { path = "core:" .. i })
			c.n = c.n + 1
		end
		return c.n .. "|" .. table.concat(_warns, "\n")
	`)
	if err != nil {
		t.Fatalf("lua: %v", err)
	}
	if got != "23|" {
		t.Errorf("core objects should expose without an onChange warning, got %q", got)
	}
}

// A plugin sensor's onChange unsubscribe removes the callback: after it, and after
// unexpose(), a value change no longer reaches it.
func TestPluginSensorOnChangeUnsubscribe(t *testing.T) {
	engine := NewLuaEngine()
	defer engine.Close()

	got := mustExec(t, engine, `(function()
		local p = Plugin:new("unsub-test", { name = "Unsub test" })
		local state = { v = 1 }
		local s = p:sensor("temp", function() return state.v end)
		local c = { a = 0, b = 0 }
		local unsubA = s:onChange(function() c.a = c.a + 1 end)
		s:onChange(function() c.b = c.b + 1 end)
		state.v = 2; s:_notify()
		unsubA()
		state.v = 3; s:_notify()

		local h = expose(s, "temperature", { path = "unsub:temp" })
		local exposed = #s._eventHandlers["OnChange"]
		h:unexpose()
		return c.a .. "," .. c.b .. "|" .. exposed .. "," .. #s._eventHandlers["OnChange"]
	end)()`)
	// a stops after unsubscribe, b keeps running; expose adds one handler, unexpose removes it.
	if got != "1,2|2,1" {
		t.Errorf("calls a,b | handlers exposed,after unexpose = %s, want 1,2|2,1", got)
	}
}
