package lua

import (
	"strings"
	"testing"
)

func TestRegistryExists(t *testing.T) {
	engine := NewLuaEngine()
	defer engine.Close()

	// Test _registry exists and is a table
	result, err := engine.Execute("type(_registry)")
	if err != nil {
		t.Fatalf("_registry check failed: %v", err)
	}
	if result != "table" {
		t.Errorf("_registry type is %q, expected 'table'", result)
	}

	// Test _ alias exists
	result, err = engine.Execute("_ == _registry")
	if err != nil {
		t.Fatalf("_ alias check failed: %v", err)
	}
	if result != "true" {
		t.Errorf("_ is not alias for _registry")
	}

	// Test _nameIndex exists
	result, err = engine.Execute("type(_nameIndex)")
	if err != nil {
		t.Fatalf("_nameIndex check failed: %v", err)
	}
	if result != "table" {
		t.Errorf("_nameIndex type is %q, expected 'table'", result)
	}
}

func TestRegisterObject(t *testing.T) {
	engine := NewLuaEngine()
	defer engine.Close()

	// Setup: create mock CLU and register object
	setup := `
		mockCLU = { execute = function() end }
		testObj = RemoteSwitch:new(mockCLU, "DOU1234", "test_switch")
		registerObject("CLU123.DOU1234", testObj)
	`
	_, err := engine.Execute(setup)
	if err != nil {
		t.Fatalf("Setup failed: %v", err)
	}

	// Verify object in registry
	result, err := engine.Execute(`_registry["CLU123.DOU1234"] ~= nil`)
	if err != nil {
		t.Fatalf("registry check failed: %v", err)
	}
	if result != "true" {
		t.Errorf("Object not registered in _registry")
	}

	// Verify name index
	result, err = engine.Execute(`_nameIndex["test_switch"] == "CLU123.DOU1234"`)
	if err != nil {
		t.Fatalf("_nameIndex check failed: %v", err)
	}
	if result != "true" {
		t.Errorf("Object not indexed by name")
	}
}

func TestRegistrySync(t *testing.T) {
	engine := NewLuaEngine()
	defer engine.Close()

	// Setup: create and register object
	setup := `
		local mockCLU = { execute = function() end }
		local obj = RemoteSwitch:new(mockCLU, "DOU1234", "test_switch")
		registerObject("CLU123.DOU1234", obj)
	`
	_, err := engine.Execute(setup)
	if err != nil {
		t.Fatalf("Setup failed: %v", err)
	}

	// Test sync by path
	_, err = engine.Execute(`_:sync("CLU123.DOU1234", 1)`)
	if err != nil {
		t.Fatalf("sync by path failed: %v", err)
	}

	result, err := engine.Execute(`_registry["CLU123.DOU1234"]._lastState`)
	if err != nil {
		t.Fatalf("state check failed: %v", err)
	}
	if result != "1" {
		t.Errorf("State after sync is %q, expected '1'", result)
	}

	// Test sync by name
	_, err = engine.Execute(`_:sync("test_switch", 0)`)
	if err != nil {
		t.Fatalf("sync by name failed: %v", err)
	}

	result, err = engine.Execute(`_registry["CLU123.DOU1234"]._lastState`)
	if err != nil {
		t.Fatalf("state check failed: %v", err)
	}
	if result != "0" {
		t.Errorf("State after sync by name is %q, expected '0'", result)
	}
}

func TestRegistrySyncStringConversion(t *testing.T) {
	engine := NewLuaEngine()
	defer engine.Close()

	// Setup
	setup := `
		local mockCLU = { execute = function() end }
		local obj = RemoteSwitch:new(mockCLU, "DOU1234", "test_switch")
		registerObject("CLU123.DOU1234", obj)
	`
	_, err := engine.Execute(setup)
	if err != nil {
		t.Fatalf("Setup failed: %v", err)
	}

	// Test sync with string "1" - should convert to number 1
	_, err = engine.Execute(`_:sync("test_switch", "1")`)
	if err != nil {
		t.Fatalf("sync with string failed: %v", err)
	}

	// Check state is number, not string
	result, err := engine.Execute(`type(_registry["CLU123.DOU1234"]._lastState)`)
	if err != nil {
		t.Fatalf("type check failed: %v", err)
	}
	if result != "number" {
		t.Errorf("State type is %q, expected 'number'", result)
	}

	result, err = engine.Execute(`_registry["CLU123.DOU1234"]._lastState`)
	if err != nil {
		t.Fatalf("state check failed: %v", err)
	}
	if result != "1" {
		t.Errorf("State is %q, expected '1'", result)
	}

	// Test with string "0"
	_, err = engine.Execute(`_:sync("test_switch", "0")`)
	if err != nil {
		t.Fatalf("sync with string 0 failed: %v", err)
	}

	result, err = engine.Execute(`_registry["CLU123.DOU1234"]._lastState`)
	if err != nil {
		t.Fatalf("state check failed: %v", err)
	}
	if result != "0" {
		t.Errorf("State is %q, expected '0'", result)
	}
}

func TestRegistryGet(t *testing.T) {
	engine := NewLuaEngine()
	defer engine.Close()

	// Setup
	setup := `
		local mockCLU = { execute = function() end }
		local obj = RemoteSwitch:new(mockCLU, "DOU1234", "test_switch")
		registerObject("CLU123.DOU1234", obj)
	`
	_, err := engine.Execute(setup)
	if err != nil {
		t.Fatalf("Setup failed: %v", err)
	}

	// Test get by path
	result, err := engine.Execute(`_:get("CLU123.DOU1234")._objId`)
	if err != nil {
		t.Fatalf("get by path failed: %v", err)
	}
	if result != "DOU1234" {
		t.Errorf("Object ID is %q, expected 'DOU1234'", result)
	}

	// Test get by name
	result, err = engine.Execute(`_:get("test_switch")._objId`)
	if err != nil {
		t.Fatalf("get by name failed: %v", err)
	}
	if result != "DOU1234" {
		t.Errorf("Object ID is %q, expected 'DOU1234'", result)
	}
}

func TestRegistryInfo(t *testing.T) {
	engine := NewLuaEngine()
	defer engine.Close()

	// Setup: register a few objects
	setup := `
		local mockCLU = { execute = function() end }
		registerObject("CLU123.DOU1", RemoteSwitch:new(mockCLU, "DOU1", "switch1"))
		registerObject("CLU123.DOU2", RemoteSwitch:new(mockCLU, "DOU2", "switch2"))
		registerObject("CLU123.DIN1", RemoteSensor:new(mockCLU, "DIN1", "sensor1"))
	`
	_, err := engine.Execute(setup)
	if err != nil {
		t.Fatalf("Setup failed: %v", err)
	}

	// Test info
	result, err := engine.Execute(`_:info()`)
	if err != nil {
		t.Fatalf("info failed: %v", err)
	}

	if !strings.Contains(result, "CLU123") {
		t.Errorf("Info doesn't contain CLU123: %s", result)
	}
	if !strings.Contains(result, "3 objects") {
		t.Errorf("Info doesn't show 3 objects: %s", result)
	}
}

func TestRegistryList(t *testing.T) {
	engine := NewLuaEngine()
	defer engine.Close()

	// Setup
	setup := `
		local mockCLU = { execute = function() end }
		registerObject("CLU123.DOU1", RemoteSwitch:new(mockCLU, "DOU1", "switch1"))
		registerObject("CLU123.DOU2", RemoteSwitch:new(mockCLU, "DOU2", "switch2"))
	`
	_, err := engine.Execute(setup)
	if err != nil {
		t.Fatalf("Setup failed: %v", err)
	}

	// Test list returns correct count
	result, err := engine.Execute(`#_:list()`)
	if err != nil {
		t.Fatalf("list failed: %v", err)
	}
	if result != "2" {
		t.Errorf("List count is %q, expected '2'", result)
	}
}

func TestRegistryDirectAccess(t *testing.T) {
	engine := NewLuaEngine()
	defer engine.Close()

	// Setup: create objects in _registry
	_, err := engine.Execute(`
		_registry["CLU.DOUT1"] = { Value = 0, Name = "Light", nested = { deep = 42 } }
		_registry["CLU.DIN1"] = { Value = 1, Name = "Button" }
	`)
	if err != nil {
		t.Fatalf("Setup failed: %v", err)
	}

	// -------------------------------------------------------
	// Test 1: _:get() returns a direct reference (same object)
	// -------------------------------------------------------
	result, err := engine.Execute(`_:get("CLU.DOUT1") == _registry["CLU.DOUT1"]`)
	if err != nil {
		t.Fatalf("Reference check failed: %v", err)
	}
	if result != "true" {
		t.Errorf("Test 1: _:get() should return the same object reference as _registry[]")
	}

	// -------------------------------------------------------
	// Test 2: Can read fields via _:get()
	// -------------------------------------------------------
	result, _ = engine.Execute(`tostring((_:get("CLU.DOUT1")).Value)`)
	if result != "0" {
		t.Errorf("Test 2: Expected Value=0, got '%s'", result)
	}
	result, _ = engine.Execute(`(_:get("CLU.DOUT1")).Name`)
	if result != "Light" {
		t.Errorf("Test 2: Expected Name='Light', got '%s'", result)
	}

	// -------------------------------------------------------
	// Test 3: Can MODIFY fields via _:get() reference
	// -------------------------------------------------------
	_, err = engine.Execute(`
		local lamp = _:get("CLU.DOUT1")
		lamp.Value = 1
	`)
	if err != nil {
		t.Fatalf("Test 3: Modify via _:get() failed: %v", err)
	}
	result, _ = engine.Execute(`tostring(_registry["CLU.DOUT1"].Value)`)
	if result != "1" {
		t.Errorf("Test 3: Expected Value=1 after modification, got '%s'", result)
	}

	// -------------------------------------------------------
	// Test 4: Can MODIFY fields via direct _registry[] access
	// -------------------------------------------------------
	_, err = engine.Execute(`_registry["CLU.DIN1"].Value = 0`)
	if err != nil {
		t.Fatalf("Test 4: Direct modify failed: %v", err)
	}
	result, _ = engine.Execute(`tostring(_registry["CLU.DIN1"].Value)`)
	if result != "0" {
		t.Errorf("Test 4: Expected Value=0 after modification, got '%s'", result)
	}

	// -------------------------------------------------------
	// Test 5: Can MODIFY nested fields
	// -------------------------------------------------------
	_, err = engine.Execute(`
		local lamp = _:get("CLU.DOUT1")
		lamp.nested.deep = 999
	`)
	if err != nil {
		t.Fatalf("Test 5: Nested modify failed: %v", err)
	}
	result, _ = engine.Execute(`tostring(_registry["CLU.DOUT1"].nested.deep)`)
	if result != "999" {
		t.Errorf("Test 5: Expected nested.deep=999, got '%s'", result)
	}

	// -------------------------------------------------------
	// Test 6: Can ADD new fields to registry objects
	// -------------------------------------------------------
	_, err = engine.Execute(`
		local lamp = _:get("CLU.DOUT1")
		lamp.NewField = "added"
	`)
	if err != nil {
		t.Fatalf("Test 6: Add field failed: %v", err)
	}
	result, _ = engine.Execute(`_registry["CLU.DOUT1"].NewField`)
	if result != "added" {
		t.Errorf("Test 6: Expected NewField='added', got '%s'", result)
	}

	// -------------------------------------------------------
	// Test 7: Can ASSIGN new keys to _registry
	// -------------------------------------------------------
	_, err = engine.Execute(`_registry["CLU.DOUT2"] = { Value = 0, Name = "New" }`)
	if err != nil {
		t.Fatalf("Test 7: Assign new key failed: %v", err)
	}
	result, _ = engine.Execute(`(_:get("CLU.DOUT2")).Name`)
	if result != "New" {
		t.Errorf("Test 7: Expected Name='New', got '%s'", result)
	}

	// -------------------------------------------------------
	// Test 8: pairs() works on registry objects
	// -------------------------------------------------------
	_, err = engine.Execute(`
		__test_count = 0
		local obj = _:get("CLU.DIN1")
		for k, v in pairs(obj) do
			__test_count = __test_count + 1
		end
	`)
	if err != nil {
		t.Fatalf("Test 8: pairs() iteration failed: %v", err)
	}
	result, _ = engine.Execute(`tostring(__test_count)`)
	if result != "2" {
		t.Errorf("Test 8: Expected 2 fields from pairs(), got '%s'", result)
	}

	// -------------------------------------------------------
	// Test 9: Can DELETE fields from registry objects
	// -------------------------------------------------------
	_, err = engine.Execute(`
		local lamp = _:get("CLU.DOUT1")
		lamp.NewField = nil
	`)
	if err != nil {
		t.Fatalf("Test 9: Delete field failed: %v", err)
	}
	result, _ = engine.Execute(`tostring(_registry["CLU.DOUT1"].NewField)`)
	if result != "nil" {
		t.Errorf("Test 9: Expected NewField=nil after delete, got '%s'", result)
	}
}

func TestRemoveByPrefix(t *testing.T) {
	engine := NewLuaEngine()
	defer engine.Close()

	// Setup: register objects from two CLUs
	setup := `
		local mockCLU = { execute = function() end }
		registerObject("CLU111.DOU1", RemoteSwitch:new(mockCLU, "DOU1", "sw1"))
		registerObject("CLU111.DOU2", RemoteSwitch:new(mockCLU, "DOU2", "sw2"))
		registerObject("CLU111.DIN1", RemoteSensor:new(mockCLU, "DIN1", "sensor1"))
		registerObject("CLU222.DOU1", RemoteSwitch:new(mockCLU, "DOU1", "sw3"))
		registerObject("CLU222.DOU2", RemoteSwitch:new(mockCLU, "DOU2", "sw4"))
	`
	_, err := engine.Execute(setup)
	if err != nil {
		t.Fatalf("Setup failed: %v", err)
	}

	// Verify initial count
	result, _ := engine.Execute(`#_:list()`)
	if result != "5" {
		t.Fatalf("Initial count is %q, expected '5'", result)
	}

	// Remove CLU111 objects
	result, err = engine.Execute(`removeByPrefix("CLU111.")`)
	if err != nil {
		t.Fatalf("removeByPrefix failed: %v", err)
	}
	if result != "3" {
		t.Errorf("removeByPrefix returned %q, expected '3' removed", result)
	}

	// Only CLU222 objects should remain
	result, _ = engine.Execute(`#_:list()`)
	if result != "2" {
		t.Errorf("After removal count is %q, expected '2'", result)
	}

	// CLU111 objects gone
	result, _ = engine.Execute(`tostring(_registry["CLU111.DOU1"])`)
	if result != "nil" {
		t.Errorf("CLU111.DOU1 should be nil, got %q", result)
	}

	// CLU222 objects still there
	result, _ = engine.Execute(`_registry["CLU222.DOU1"] ~= nil`)
	if result != "true" {
		t.Errorf("CLU222.DOU1 should still exist")
	}

	// Name index cleaned for removed objects
	result, _ = engine.Execute(`tostring(_nameIndex["sw1"])`)
	if result != "nil" {
		t.Errorf("_nameIndex['sw1'] should be nil after removal, got %q", result)
	}
	result, _ = engine.Execute(`tostring(_nameIndex["sensor1"])`)
	if result != "nil" {
		t.Errorf("_nameIndex['sensor1'] should be nil after removal, got %q", result)
	}

	// Name index preserved for remaining objects
	result, _ = engine.Execute(`_nameIndex["sw3"]`)
	if result != "CLU222.DOU1" {
		t.Errorf("_nameIndex['sw3'] should be 'CLU222.DOU1', got %q", result)
	}
}

func TestRemoveByPrefix_NoMatch(t *testing.T) {
	engine := NewLuaEngine()
	defer engine.Close()

	setup := `
		local mockCLU = { execute = function() end }
		registerObject("CLU111.DOU1", RemoteSwitch:new(mockCLU, "DOU1", "sw1"))
	`
	_, err := engine.Execute(setup)
	if err != nil {
		t.Fatalf("Setup failed: %v", err)
	}

	// Remove non-existent prefix
	result, err := engine.Execute(`removeByPrefix("CLU999.")`)
	if err != nil {
		t.Fatalf("removeByPrefix failed: %v", err)
	}
	if result != "0" {
		t.Errorf("removeByPrefix returned %q, expected '0'", result)
	}

	// Original object still there
	result, _ = engine.Execute(`#_:list()`)
	if result != "1" {
		t.Errorf("Count should still be 1, got %q", result)
	}
}

func TestRegistryTags(t *testing.T) {
	engine := NewLuaEngine()
	defer engine.Close()

	// Setup with tags
	setup := `
		local mockCLU = { execute = function() end }
		local obj1 = RemoteSwitch:new(mockCLU, "DOU1", "switch1")
		local obj2 = RemoteSwitch:new(mockCLU, "DOU2", "switch2")
		registerObject("CLU123.DOU1", obj1)
		registerObject("CLU123.DOU2", obj2)
		obj1:addTag("lights", "floor1")
		obj2:addTag("lights", "floor2")
	`
	_, err := engine.Execute(setup)
	if err != nil {
		t.Fatalf("Setup failed: %v", err)
	}

	// Test byTag
	result, err := engine.Execute(`_:byTag("lights"):count()`)
	if err != nil {
		t.Fatalf("byTag failed: %v", err)
	}
	if result != "2" {
		t.Errorf("byTag('lights') count is %q, expected '2'", result)
	}

	// Test byTag with specific tag
	result, err = engine.Execute(`_:byTag("floor1"):count()`)
	if err != nil {
		t.Fatalf("byTag floor1 failed: %v", err)
	}
	if result != "1" {
		t.Errorf("byTag('floor1') count is %q, expected '1'", result)
	}

	// Test tags list
	result, err = engine.Execute(`#_:tags()`)
	if err != nil {
		t.Fatalf("tags failed: %v", err)
	}
	if result != "3" {
		t.Errorf("tags count is %q, expected '3' (lights, floor1, floor2)", result)
	}
}
