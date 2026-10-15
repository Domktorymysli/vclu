package omparser

import (
	"os"
	"strings"
	"testing"
)

func TestGenerateProxy(t *testing.T) {
	parsed, err := Parse(sampleOM)
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	config := DefaultConfig()
	config.Namespace = "testns"
	config.IncludeBaseClasses = true
	config.IncludeRegistry = true

	lua := GenerateProxy(parsed, config)

	// Check header
	if !strings.Contains(lua, "-- Proxy generated from CLU212345678") {
		t.Error("Missing header")
	}

	// Check base classes (only present when IncludeBaseClasses=true)
	if !strings.Contains(lua, "RemoteObject = {}") {
		t.Error("Missing RemoteObject class")
	}
	if !strings.Contains(lua, "RemoteSwitch = setmetatable") {
		t.Error("Missing RemoteSwitch class")
	}
	if !strings.Contains(lua, "RemoteSensor = setmetatable") {
		t.Error("Missing RemoteSensor class")
	}

	// Check namespace
	if !strings.Contains(lua, "testns = {}") {
		t.Error("Missing namespace table")
	}

	// Check objects (format: ClassName:new(_clu, "objId", "friendlyName"))
	if !strings.Contains(lua, "testns.P1_DACHOWE_BOK = RemoteSwitch:new(_clu, \"DOU5048\"") {
		t.Error("Missing DOU5048 object with friendly name")
	}
	if !strings.Contains(lua, "RemoteSensor:new(_clu, \"DIN7929\"") {
		t.Error("Missing DIN7929 object")
	}

	// Check registry
	if !strings.Contains(lua, "_registry = {}") {
		t.Error("Missing registry")
	}
	// Check registry methods
	if !strings.Contains(lua, "sync = function(self") {
		t.Error("Missing sync function")
	}
	if !strings.Contains(lua, "event = function(self") {
		t.Error("Missing event function")
	}
}

func TestGenerateProxyWithoutBaseClasses(t *testing.T) {
	parsed, err := Parse(sampleOM)
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	config := GeneratorConfig{
		Namespace:          "test",
		IncludeBaseClasses: false,
		IncludeRegistry:    false,
	}

	lua := GenerateProxy(parsed, config)

	// Should NOT have base classes
	if strings.Contains(lua, "RemoteObject = {}") {
		t.Error("Should not include RemoteObject class")
	}

	// Should NOT have registry
	if strings.Contains(lua, "_registry = {}") {
		t.Error("Should not include registry")
	}

	// Should still have objects
	if !strings.Contains(lua, "test.P1_DACHOWE_BOK = RemoteSwitch:new") {
		t.Error("Missing objects")
	}
}

func TestGenerateProxyRealFile(t *testing.T) {
	content, err := os.ReadFile("testdata/CLU212345678_om.lua")
	if err != nil {
		t.Fatalf("Failed to read test fixture: %v", err)
	}

	parsed, err := Parse(string(content))
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	config := DefaultConfig()
	lua := GenerateProxy(parsed, config)

	t.Logf("Generated %d bytes of Lua code", len(lua))

	// Count objects in generated code (exclude class definitions)
	doutCount := strings.Count(lua, "= RemoteSwitch:new")
	dinCount := strings.Count(lua, "= RemoteSensor:new")

	t.Logf("Generated switches: %d", doutCount)
	t.Logf("Generated sensors: %d", dinCount)

	// Verify counts match parsed
	expectedDout := len(parsed.GetObjectsByClass(ClassDOUT))
	expectedDin := len(parsed.GetObjectsByClass(ClassDIN)) + len(parsed.GetObjectsByClass(ClassAnalogIN))

	if doutCount != expectedDout {
		t.Errorf("Switch count = %d, want %d", doutCount, expectedDout)
	}
	if dinCount != expectedDin {
		t.Errorf("Sensor count = %d, want %d", dinCount, expectedDin)
	}

	// Print first 100 lines for inspection
	lines := strings.Split(lua, "\n")
	t.Log("\n=== Generated Lua (first 100 lines) ===")
	for i, line := range lines {
		if i >= 100 {
			t.Logf("... and %d more lines", len(lines)-100)
			break
		}
		t.Log(line)
	}
}

func TestBaseClassesMethods(t *testing.T) {
	config := DefaultConfig()
	config.IncludeBaseClasses = true
	parsed, _ := Parse(sampleOM)
	lua := GenerateProxy(parsed, config)

	// Test RemoteSwitch methods
	switchMethods := []string{
		"function RemoteSwitch:getValue()",
		"function RemoteSwitch:setValue(value)",
		"function RemoteSwitch:switchOn(time)",
		"function RemoteSwitch:switchOff(time)",
		"function RemoteSwitch:on()",
		"function RemoteSwitch:off()",
		"function RemoteSwitch:toggle()",
		"function RemoteSwitch:onSwitchOn(callback)",
		"function RemoteSwitch:onSwitchOff(callback)",
	}

	for _, method := range switchMethods {
		if !strings.Contains(lua, method) {
			t.Errorf("Missing method: %s", method)
		}
	}

	// Test RemoteSensor methods
	sensorMethods := []string{
		"function RemoteSensor:getValue()",
		"function RemoteSensor:onClick(callback)",
		"function RemoteSensor:onLongPress(callback)",
		"function RemoteSensor:onHold(callback)",
	}

	for _, method := range sensorMethods {
		if !strings.Contains(lua, method) {
			t.Errorf("Missing method: %s", method)
		}
	}

	// Test RemoteLight methods
	lightMethods := []string{
		"function RemoteLight:getValue()",
		"function RemoteLight:setValue(value, ramp)",
		"function RemoteLight:getBrightness()",
		"function RemoteLight:setBrightness(percent, ramp)",
		"function RemoteLight:dim(ramp)",
	}

	for _, method := range lightMethods {
		if !strings.Contains(lua, method) {
			t.Errorf("Missing method: %s", method)
		}
	}

	// Test RemoteCover methods
	coverMethods := []string{
		"function RemoteCover:getPosition()",
		"function RemoteCover:setPosition(position, lamel)",
		"function RemoteCover:open(time)",
		"function RemoteCover:close(time)",
		"function RemoteCover:stop()",
		"function RemoteCover:up(time)",
		"function RemoteCover:down(time)",
	}

	for _, method := range coverMethods {
		if !strings.Contains(lua, method) {
			t.Errorf("Missing method: %s", method)
		}
	}

	// Test RemoteRGBLight methods
	rgbMethods := []string{
		"function RemoteRGBLight:getHue()",
		"function RemoteRGBLight:setHue(hue, ramp)",
		"function RemoteRGBLight:getRGB()",
		"function RemoteRGBLight:setRGB(hex, ramp)",
		"function RemoteRGBLight:setColor(r, g, b, ramp)",
	}

	for _, method := range rgbMethods {
		if !strings.Contains(lua, method) {
			t.Errorf("Missing method: %s", method)
		}
	}
}

func TestEventHandlingInBaseClasses(t *testing.T) {
	config := DefaultConfig()
	config.IncludeBaseClasses = true
	parsed, _ := Parse(sampleOM)
	lua := GenerateProxy(parsed, config)

	// Check event handling infrastructure
	eventMethods := []string{
		"function RemoteObject:onEvent(eventName, callback)",
		"function RemoteObject:emit(eventName, ...)",
		"function RemoteObject:onChange(callback)",
		"function RemoteObject:updateState(newState)",
	}

	for _, method := range eventMethods {
		if !strings.Contains(lua, method) {
			t.Errorf("Missing event method: %s", method)
		}
	}

	// Check that updateState emits events
	if !strings.Contains(lua, `self:emit("OnChange"`) {
		t.Error("updateState should emit OnChange")
	}
	if !strings.Contains(lua, `self:emit("OnSwitchOn")`) {
		t.Error("updateState should emit OnSwitchOn")
	}
	if !strings.Contains(lua, `self:emit("OnSwitchOff")`) {
		t.Error("updateState should emit OnSwitchOff")
	}
}

func TestGenerateLocalProxy(t *testing.T) {
	parsed, err := Parse(sampleOM)
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	lua := GenerateLocalProxy(parsed)

	// Should register existing objects
	if !strings.Contains(lua, `registerObject("CLU212345678.DOU5048", DOU5048)`) {
		t.Error("Missing registerObject for DOU5048")
	}
	if !strings.Contains(lua, `registerObject("CLU212345678.DIN7929", DIN7929)`) {
		t.Error("Missing registerObject for DIN7929")
	}

	// Should set _type
	if !strings.Contains(lua, `DOU5048._type = "DOUT"`) {
		t.Error("Missing _type for DOU5048")
	}
	if !strings.Contains(lua, `DIN7929._type = "DIN"`) {
		t.Error("Missing _type for DIN7929")
	}

	// Should set _registryPath for StateBus compatibility
	if !strings.Contains(lua, `DOU5048._registryPath = "CLU212345678.DOU5048"`) {
		t.Error("Missing _registryPath for DOU5048")
	}
	if !strings.Contains(lua, `DIN7929._registryPath = "CLU212345678.DIN7929"`) {
		t.Error("Missing _registryPath for DIN7929")
	}

	// Should NOT contain remote proxy code
	if strings.Contains(lua, "CLU_REMOTE:new") {
		t.Error("Should not create CLU_REMOTE")
	}
	if strings.Contains(lua, "RemoteSwitch:new") {
		t.Error("Should not create RemoteSwitch")
	}
	if strings.Contains(lua, "RemoteSensor:new") {
		t.Error("Should not create RemoteSensor")
	}
	// Should NOT create namespace table
	if strings.Contains(lua, "dom20 = {}") || strings.Contains(lua, "= {}") {
		t.Error("Should not create namespace table")
	}
}

func TestGenerateLocalProxyNames(t *testing.T) {
	parsed, err := Parse(sampleOM)
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	lua := GenerateLocalProxy(parsed)

	// Objects with real NAME_IO should get _name
	if !strings.Contains(lua, `DOU5048._name = "P1_DACHOWE_BOK"`) {
		t.Error("Missing _name for DOU5048")
	}
	if !strings.Contains(lua, `DOU4458._name = "P1_DACHOWE_WEJSCIE"`) {
		t.Error("Missing _name for DOU4458")
	}
	if !strings.Contains(lua, `DIN7929._name = "P1_23_IN_GARAZ_SRODKOWY_PRAWY"`) {
		t.Error("Missing _name for DIN7929")
	}

	// Objects with x-prefix names should still get _name (it's from NAME_IO)
	if !strings.Contains(lua, `DIN9518._name = "x330000027_DIN1"`) {
		t.Error("Missing _name for DIN9518")
	}
}

func TestGenerateLocalProxyNoObjects(t *testing.T) {
	omWithNoIO := `-- FwType 00000003
CLU123456789 = OBJECT:new(0, 0xC0A80001)

function OnInit()
end
CLU123456789:add_event(0, OnInit)
`
	parsed, err := Parse(omWithNoIO)
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	lua := GenerateLocalProxy(parsed)

	if lua != "" {
		t.Errorf("Expected empty output for om.lua without IO objects, got:\n%s", lua)
	}
}

func TestGenerateLocalProxyRealFile(t *testing.T) {
	content, err := os.ReadFile("testdata/CLU212345678_om.lua")
	if err != nil {
		t.Fatalf("Failed to read test fixture: %v", err)
	}

	parsed, err := Parse(string(content))
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	lua := GenerateLocalProxy(parsed)

	t.Logf("Generated %d bytes of local proxy code", len(lua))

	// Count registerObject calls
	regCount := strings.Count(lua, "registerObject(")
	expectedCount := len(parsed.GetExposableObjects())

	if regCount != expectedCount {
		t.Errorf("registerObject count = %d, want %d", regCount, expectedCount)
	}

	// Every exposable object should have _type set
	typeCount := strings.Count(lua, "._type = ")
	if typeCount != expectedCount {
		t.Errorf("_type count = %d, want %d", typeCount, expectedCount)
	}

	// Every exposable object should have _registryPath set
	pathCount := strings.Count(lua, "._registryPath = ")
	if pathCount != expectedCount {
		t.Errorf("_registryPath count = %d, want %d", pathCount, expectedCount)
	}

	t.Logf("Registered %d local objects", regCount)
}

// A ROLLER_SH (class 11) has no Position feature, so its cover is marked to not subscribe
// to it; a ROLLER_SHUTTER (class 24) keeps the default.
func TestGenerateProxyRollerPosition(t *testing.T) {
	parsed := NewParsedOM()
	parsed.CLU = CLUInfo{ID: "CLU1", Name: "dom"}
	parsed.Objects = []GrentonObject{
		{ID: "ROL1", Name: "SALON", Class: ClassROLLER},
		{ID: "ROL2", Name: "STRYCH", Class: ClassROLLER_SH},
	}

	lua := GenerateProxy(parsed, DefaultConfig())

	if !strings.Contains(lua, "dom.STRYCH._hasPosition = false") {
		t.Errorf("ROLLER_SH not marked without position:\n%s", lua)
	}
	if strings.Contains(lua, "dom.SALON._hasPosition") {
		t.Errorf("ROLLER_SHUTTER must keep its position:\n%s", lua)
	}
}
