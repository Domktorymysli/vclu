package config

import (
	"strings"
	"testing"
)

func TestOMGeneratorGate(t *testing.T) {
	device := &VCLUDeviceConfig{
		Name:            "TestGate",
		SerialNumber:    520000123,
		Category:        DeviceCategoryGate,
		HardwareType:    0x12,
		HardwareVersion: 0x01,
		FirmwareType:    0x03,
		FirmwareVersion: 0x000003e8,
	}

	localIP := IPToUint32("192.168.1.100")
	generator := NewOMGenerator(device, localIP)

	content, err := generator.Generate()
	if err != nil {
		t.Fatalf("Failed to generate om.lua: %v", err)
	}

	// Check FW/HW header values (Gate: HW 0x12, FW 0x000003e8)
	if !strings.Contains(content, "-- FwType 00000003") {
		t.Error("Expected FwType 00000003 in header")
	}
	if !strings.Contains(content, "-- FwVersion 000003e8") {
		t.Error("Expected FwVersion 000003e8 in header")
	}
	if !strings.Contains(content, "-- HwType 00000012") {
		t.Error("Expected HwType 00000012 in header")
	}
	if !strings.Contains(content, "-- HwVersion 00000001") {
		t.Error("Expected HwVersion 00000001 in header")
	}

	// Check for GATE device signature
	if !strings.Contains(content, "GATE:new") {
		t.Error("Expected GATE:new in output")
	}

	// Check for serial number
	if !strings.Contains(content, "CLU520000123") {
		t.Error("Expected CLU520000123 in output")
	}

	// Check for IP address (hex format)
	if !strings.Contains(content, "0xC0A80164") { // 192.168.1.100 in hex
		t.Error("Expected IP address 0xC0A80164 in output")
	}

	// Check for gateway (last octet = 1)
	if !strings.Contains(content, "0xC0A80101") { // 192.168.1.1 in hex
		t.Error("Expected gateway 0xC0A80101 in output")
	}

	// Should NOT contain OBJECT:new (that's for CLU)
	if strings.Contains(content, "OBJECT:new(0,") {
		t.Error("Gate device should not have OBJECT:new(0,)")
	}
}

func TestOMGeneratorCLU(t *testing.T) {
	// CLU om.lua contains only the base CLU object.
	// Modules/IO are in config.txt and CONFIG.JSON — OM reads those
	// and generates the full om.lua with module entries itself.
	device := &VCLUDeviceConfig{
		Name:            "TestCLU",
		SerialNumber:    221000456,
		Category:        DeviceCategoryCLU,
		HardwareType:    0x13,
		HardwareVersion: 0x01,
		FirmwareType:    0x03,
		FirmwareVersion: 0x000001f6,
		Modules: []ModuleInstance{
			{ID: "module-1", TypeID: "dout8t", Serial: "330000027", Name: "IOModule", HardwareType: 0x1e},
		},
		IOObjects: []IOInstance{
			{ID: "DOU5048", ModuleID: "module-1", TypeID: "dout", Index: 0, Name: "Light1", LuaType: 4},
		},
	}

	localIP := IPToUint32("192.168.0.50")
	generator := NewOMGenerator(device, localIP)

	content, err := generator.Generate()
	if err != nil {
		t.Fatalf("Failed to generate om.lua: %v", err)
	}

	// FW/HW header
	for _, expected := range []string{
		"-- FwType 00000003",
		"-- FwVersion 000001f6",
		"-- HwType 00000013",
		"-- HwVersion 00000001",
	} {
		if !strings.Contains(content, expected) {
			t.Errorf("Missing header: %s", expected)
		}
	}

	// Base CLU object
	if !strings.Contains(content, "OBJECT:new(0,") {
		t.Error("Expected OBJECT:new(0, for CLU device")
	}
	if !strings.Contains(content, "CLU221000456") {
		t.Error("Expected CLU221000456 in output")
	}
	if !strings.Contains(content, "CLU221000456:set(18, 0)") {
		t.Error("Expected CLU init set(18, 0)")
	}

	// Section markers present (OM uses these)
	if !strings.Contains(content, "-- MODULES") {
		t.Error("Expected -- MODULES marker")
	}
	if !strings.Contains(content, "-- IO_MODULES") {
		t.Error("Expected -- IO_MODULES marker")
	}

	// Modules/IO should NOT be generated — OM handles those via config.txt
	if strings.Contains(content, "mm_330000027") {
		t.Error("om.lua should NOT contain module objects (OM manages those)")
	}
	if strings.Contains(content, "DOU5048") {
		t.Error("om.lua should NOT contain I/O objects (OM manages those)")
	}

	// Should NOT contain GATE:new
	if strings.Contains(content, "GATE:new") {
		t.Error("CLU device should not have GATE:new")
	}
}

func TestIPConversion(t *testing.T) {
	tests := []struct {
		ip       string
		expected uint32
	}{
		{"192.168.0.1", 0xC0A80001},
		{"192.168.1.100", 0xC0A80164},
		{"10.0.0.1", 0x0A000001},
		{"255.255.255.255", 0xFFFFFFFF},
		{"0.0.0.0", 0x00000000},
	}

	for _, test := range tests {
		result := IPToUint32(test.ip)
		if result != test.expected {
			t.Errorf("IPToUint32(%s) = 0x%08X, expected 0x%08X", test.ip, result, test.expected)
		}

		// Test reverse conversion
		backToIP := Uint32ToIP(result)
		if backToIP != test.ip {
			t.Errorf("Uint32ToIP(0x%08X) = %s, expected %s", result, backToIP, test.ip)
		}
	}
}

func TestModuleSerialGeneration(t *testing.T) {
	moduleType := GetModuleByID("dout8t")
	if moduleType == nil {
		t.Fatal("Module type dout8t not found")
	}

	serial := GenerateModuleSerial(moduleType)

	// Should start with 33 (prefix for DOUT8T)
	if !strings.HasPrefix(serial, "33") {
		t.Errorf("Serial %s should start with 33", serial)
	}

	// Should be 9 digits total
	if len(serial) != 9 {
		t.Errorf("Serial %s should be 9 digits, got %d", serial, len(serial))
	}
}

func TestIOIDGeneration(t *testing.T) {
	id := GenerateIOID("DOU")

	// Should start with prefix
	if !strings.HasPrefix(id, "DOU") {
		t.Errorf("ID %s should start with DOU", id)
	}

	// Should be 7 characters total (3 prefix + 4 digits)
	if len(id) != 7 {
		t.Errorf("ID %s should be 7 characters, got %d", id, len(id))
	}
}
