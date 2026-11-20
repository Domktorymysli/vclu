package server

import (
	"bytes"
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Domktorymysli/vclu/pkg/config"
	"github.com/Domktorymysli/vclu/pkg/lua"
)

// An om.lua copy kept before #214 still has a GATE Alarm's settings and Wi-Fi: the start
// (LoadInitialData) removes them once, and the log says only how many.
func TestStartStripsStoredGateSettings(t *testing.T) {
	dir := t.TempDir()
	imports := config.GetOMFilesDir(dir)
	if err := os.MkdirAll(imports, 0o755); err != nil {
		t.Fatal(err)
	}
	old := "-- FwType 00000002\n-- HwType 00000012\n" +
		"CLU510000001 = GATE:new(5000, 0xC0A80104, 0xFFFFFF00, 0xC0A80101, \"home-net\", \"MARKER-PASSWORD-123-WIFI\")\n" +
		"SAT0001 = GATE:new(103, \"SAT0001\")\nSAT0001:set(4, \"MARKER-PASSWORD-123\")\n"
	stored := filepath.Join(imports, "om_510000001.lua")
	if err := os.WriteFile(stored, []byte(old), 0o644); err != nil {
		t.Fatal(err)
	}
	clean := "CLU220000002 = OBJECT:new(0, 0xC0A80105)\n"
	other := filepath.Join(imports, "om_220000002.lua")
	if err := os.WriteFile(other, []byte(clean), 0o644); err != nil {
		t.Fatal(err)
	}
	var logged bytes.Buffer
	prev := log.Writer()
	log.SetOutput(&logged)
	defer log.SetOutput(prev)

	// what LoadInitialData runs before the proxies load (it starts goroutines that log,
	// which a captured log would race with)
	stripStoredGateSettings(imports)

	data, err := os.ReadFile(stored)
	if err != nil || strings.Contains(string(data), "MARKER") || strings.Contains(string(data), "home-net") ||
		!strings.Contains(string(data), `SAT0001 = GATE:new(103, "SAT0001")`) {
		t.Errorf("stored copy %v:\n%s", err, data)
	}
	if data, _ := os.ReadFile(other); string(data) != clean {
		t.Errorf("a copy without GATE settings changed: %q", data)
	}
	if strings.Contains(logged.String(), "MARKER") || !strings.Contains(logged.String(), "removed 2 GATE settings from 1 stored om.lua copies") {
		t.Errorf("log:\n%s", logged.String())
	}
	if left, _ := filepath.Glob(filepath.Join(imports, "*.tmp")); len(left) > 0 {
		t.Errorf("left behind: %v", left)
	}
}

// A backup restored from before #214 brings copies with a GATE's settings back; the reload
// that follows removes them before the proxies load.
func TestReloadStripsStoredGateSettings(t *testing.T) {
	dir := t.TempDir()
	imports := config.GetOMFilesDir(dir)
	if err := os.MkdirAll(imports, 0o755); err != nil {
		t.Fatal(err)
	}
	stored := filepath.Join(imports, "om_510000001.lua")
	old := "-- FwType 00000002\n-- HwType 00000012\n" +
		"SAT0001 = GATE:new(103, \"SAT0001\")\nSAT0001:set(4, \"MARKER-PASSWORD-123\")\n"
	if err := os.WriteFile(stored, []byte(old), 0o644); err != nil {
		t.Fatal(err)
	}
	engine := lua.NewLuaEngine()
	t.Cleanup(engine.Close)
	s := &Server{serverDir: dir, luaEngine: engine, deviceConfig: &config.CLUDeviceConfig{}}
	if err := s.Reload(); err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(stored); err != nil || strings.Contains(string(data), "MARKER") {
		t.Errorf("stored copy after the reload %v:\n%s", err, data)
	}
}
