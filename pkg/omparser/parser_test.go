package omparser

import (
	"os"
	"testing"
)

// Sample om.lua content for testing
const sampleOM = `-- FwType 00000003
-- FwVersion 000001f6
-- HwType 00000013
-- HwVersion 00000001

CLU212345678 = OBJECT:new(0, 0xC0A80003)
-- NAME_CLU DOM20=CLU212345678

CLU_0d1cf150 = OBJECT:new(1, 0xC0A80002)
CLU520000006 = OBJECT:new(1, 0xC0A80006)
CLU511000022 = OBJECT:new(1, 0xC0A80005)

-- MODULES
mm_330000027 = OBJECT:new(2, 0x13ab669b, 0x1e, "13ab669b") -- 330000027 02 01
mm_330000028 = OBJECT:new(2, 0x13ab669c, 0x1e, "13ab669c") -- 330000028 02 01
mm_181000015 = OBJECT:new(2, 0x0ac9d74f, 0x14, "0ac9d74f") -- 181000015 02 01

-- IO_MODULES
 DOU5048 = OBJECT:new(4, mm_330000027, 0)
-- NAME_IO P1_DACHOWE_BOK=DOU5048
 DOU4458 = OBJECT:new(4, mm_330000027, 1)
-- NAME_IO P1_DACHOWE_WEJSCIE=DOU4458
 DIN9518 = OBJECT:new(3, mm_330000027, 0)
-- NAME_IO x330000027_DIN1=DIN9518
 ANA8466 = OBJECT:new(12, mm_330000027, 0)
-- NAME_IO x330000027_AnalogIN1=ANA8466
 DIN7929 = OBJECT:new(3, mm_181000015, 0)
-- NAME_IO P1_23_IN_GARAZ_SRODKOWY_PRAWY=DIN7929

function EventsFor_DIN7929_1()
	DOU1959:execute(1, 0)
end
DIN7929:add_event(1, EventsFor_DIN7929_1)

function EventsFor_DIN7929_2()
	DOU1959:execute(2, 0)
end
DIN7929:add_event(2, EventsFor_DIN7929_2)

function OnInit()
-- INIT_CLU_OBJECTS
CLU212345678:set(18, 0)
end

CLU212345678:add_event(0, OnInit)
`

func TestParse(t *testing.T) {
	result, err := Parse(sampleOM)
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	// Test CLU info
	if result.CLU.ID != "CLU212345678" {
		t.Errorf("CLU.ID = %q, want %q", result.CLU.ID, "CLU212345678")
	}
	if result.CLU.Name != "DOM20" {
		t.Errorf("CLU.Name = %q, want %q", result.CLU.Name, "DOM20")
	}
	if result.CLU.FwType != "00000003" {
		t.Errorf("CLU.FwType = %q, want %q", result.CLU.FwType, "00000003")
	}
	if result.CLU.IP != 0xC0A80003 {
		t.Errorf("CLU.IP = %x, want %x", result.CLU.IP, 0xC0A80003)
	}

	// Test references
	if len(result.References) != 3 {
		t.Errorf("len(References) = %d, want %d", len(result.References), 3)
	}

	// Test modules
	if len(result.Modules) != 3 {
		t.Errorf("len(Modules) = %d, want %d", len(result.Modules), 3)
	}
	if result.Modules[0].ID != "mm_330000027" {
		t.Errorf("Modules[0].ID = %q, want %q", result.Modules[0].ID, "mm_330000027")
	}
	if result.Modules[0].Serial != "13ab669b" {
		t.Errorf("Modules[0].Serial = %q, want %q", result.Modules[0].Serial, "13ab669b")
	}

	// Test objects
	if len(result.Objects) != 5 {
		t.Errorf("len(Objects) = %d, want %d", len(result.Objects), 5)
	}

	// Test DOUT object
	dou5048, ok := result.ObjectMap["DOU5048"]
	if !ok {
		t.Fatal("DOU5048 not found in ObjectMap")
	}
	if dou5048.Name != "P1_DACHOWE_BOK" {
		t.Errorf("DOU5048.Name = %q, want %q", dou5048.Name, "P1_DACHOWE_BOK")
	}
	if dou5048.Class != ClassDOUT {
		t.Errorf("DOU5048.Class = %d, want %d", dou5048.Class, ClassDOUT)
	}
	if dou5048.ModuleID != "mm_330000027" {
		t.Errorf("DOU5048.ModuleID = %q, want %q", dou5048.ModuleID, "mm_330000027")
	}
	if dou5048.Index != 0 {
		t.Errorf("DOU5048.Index = %d, want %d", dou5048.Index, 0)
	}

	// Test DIN object
	din7929, ok := result.ObjectMap["DIN7929"]
	if !ok {
		t.Fatal("DIN7929 not found in ObjectMap")
	}
	if din7929.Class != ClassDIN {
		t.Errorf("DIN7929.Class = %d, want %d", din7929.Class, ClassDIN)
	}

	// Test AnalogIN object
	ana8466, ok := result.ObjectMap["ANA8466"]
	if !ok {
		t.Fatal("ANA8466 not found in ObjectMap")
	}
	if ana8466.Class != ClassAnalogIN {
		t.Errorf("ANA8466.Class = %d, want %d", ana8466.Class, ClassAnalogIN)
	}

	// Test events
	if len(result.Events) != 3 { // 2 for DIN7929 + 1 for OnInit
		t.Errorf("len(Events) = %d, want %d", len(result.Events), 3)
	}
}

func TestGetObjectsByClass(t *testing.T) {
	result, err := Parse(sampleOM)
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	douts := result.GetObjectsByClass(ClassDOUT)
	if len(douts) != 2 {
		t.Errorf("len(DOUT objects) = %d, want %d", len(douts), 2)
	}

	dins := result.GetObjectsByClass(ClassDIN)
	if len(dins) != 2 {
		t.Errorf("len(DIN objects) = %d, want %d", len(dins), 2)
	}

	analogs := result.GetObjectsByClass(ClassAnalogIN)
	if len(analogs) != 1 {
		t.Errorf("len(AnalogIN objects) = %d, want %d", len(analogs), 1)
	}
}

func TestGetExposableObjects(t *testing.T) {
	result, err := Parse(sampleOM)
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	exposable := result.GetExposableObjects()
	// All 5 objects should be exposable (2 DOUT, 2 DIN, 1 AnalogIN)
	if len(exposable) != 5 {
		t.Errorf("len(exposable) = %d, want %d", len(exposable), 5)
	}
}

func TestObjectClassString(t *testing.T) {
	tests := []struct {
		class ObjectClass
		want  string
	}{
		{ClassDOUT, "DOUT"},
		{ClassDIN, "DIN"},
		{ClassDIMMER, "DIMMER"},
		{ClassLEDRGB, "LEDRGB"},
		{ClassROLLER_SH, "ROLLER_SH"},
		{ClassROLLER, "ROLLER_SHUTTER"},
		{ClassAnalogIN, "AnalogIN"},
		{ClassAnalogOUT, "AnalogOUT"},
	}

	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			if got := tt.class.String(); got != tt.want {
				t.Errorf("ObjectClass(%d).String() = %q, want %q", tt.class, got, tt.want)
			}
		})
	}
}

func TestObjectClassMQTTType(t *testing.T) {
	tests := []struct {
		class ObjectClass
		want  string
	}{
		{ClassDOUT, "switch"},
		{ClassDIN, "binary_sensor"},
		{ClassDIMMER, "light"},
		{ClassLEDRGB, "light"},
		{ClassROLLER_SH, "cover"},
		{ClassROLLER, "cover"},
		{ClassAnalogIN, "sensor"},
		{ClassAnalogOUT, "number"},
		{ClassCLU, ""},
		{ClassModule, ""},
	}

	for _, tt := range tests {
		t.Run(tt.class.String(), func(t *testing.T) {
			if got := tt.class.MQTTType(); got != tt.want {
				t.Errorf("ObjectClass(%d).MQTTType() = %q, want %q", tt.class, got, tt.want)
			}
		})
	}
}

func TestIPToString(t *testing.T) {
	tests := []struct {
		ip   uint32
		want string
	}{
		{0xC0A80003, "192.168.0.3"},
		{0xC0A80002, "192.168.0.2"},
		{0x0A000001, "10.0.0.1"},
	}

	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			if got := IPToString(tt.ip); got != tt.want {
				t.Errorf("IPToString(%x) = %q, want %q", tt.ip, got, tt.want)
			}
		})
	}
}

// TestParseRealFile tests parsing a real om.lua file
func TestParseRealFile(t *testing.T) {
	content, err := os.ReadFile("testdata/CLU212345678_om.lua")
	if err != nil {
		t.Fatalf("Failed to read test fixture: %v", err)
	}

	result, err := Parse(string(content))
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	t.Logf("Parsed CLU: %s (%s)", result.CLU.ID, result.CLU.Name)
	t.Logf("IP: %s", IPToString(result.CLU.IP))
	t.Logf("References: %d", len(result.References))
	t.Logf("Modules: %d", len(result.Modules))
	t.Logf("Objects: %d", len(result.Objects))
	t.Logf("Events: %d", len(result.Events))

	// Log objects by type
	for _, class := range []ObjectClass{ClassDOUT, ClassDIN, ClassDIMMER, ClassAnalogIN} {
		objs := result.GetObjectsByClass(class)
		if len(objs) > 0 {
			t.Logf("  %s: %d", class.String(), len(objs))
			for _, obj := range objs[:min(3, len(objs))] {
				t.Logf("    - %s (%s)", obj.ID, obj.Name)
			}
			if len(objs) > 3 {
				t.Logf("    ... and %d more", len(objs)-3)
			}
		}
	}
}
