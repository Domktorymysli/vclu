package omparser

import (
	"encoding/json"
	"os"
	"testing"
)

// TestGenerateFiles generates actual output files for manual inspection.
// Output goes to testoutput/ folder (gitignored).
func TestGenerateFiles(t *testing.T) {
	// Read real om.lua
	content, err := os.ReadFile("testdata/CLU212345678_om.lua")
	if err != nil {
		t.Fatalf("Failed to read test fixture: %v", err)
	}

	parsed, err := Parse(string(content))
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	// Generate Lua proxy
	luaConfig := DefaultConfig()
	luaConfig.Namespace = "dom"
	lua := GenerateProxy(parsed, luaConfig)

	err = os.WriteFile("testoutput/proxy.lua", []byte(lua), 0644)
	if err != nil {
		t.Fatalf("Failed to write proxy.lua: %v", err)
	}
	t.Logf("Generated testoutput/proxy.lua (%d bytes)", len(lua))

	// Generate HA Discovery JSON
	haConfig := HADiscoveryConfig{
		TopicPrefix:     "grenton",
		DiscoveryPrefix: "homeassistant",
		DeviceName:      "DOM20",
	}
	messages := GenerateHADiscovery(parsed, haConfig)

	jsonBytes, err := json.MarshalIndent(messages, "", "  ")
	if err != nil {
		t.Fatalf("Failed to marshal JSON: %v", err)
	}

	err = os.WriteFile("testoutput/discovery.json", jsonBytes, 0644)
	if err != nil {
		t.Fatalf("Failed to write discovery.json: %v", err)
	}
	t.Logf("Generated testoutput/discovery.json (%d bytes, %d messages)", len(jsonBytes), len(messages))
}
