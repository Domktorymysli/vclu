package server

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Domktorymysli/vclu/pkg/config"
	"github.com/Domktorymysli/vclu/pkg/lua"
)

// TestReloadLuaEngineLoadsUserModules guards the Grenton reset path (req_reset →
// reloadLuaEngine). It MUST load modules/*.lua; otherwise manual expose() calls in
// modules/dom.lua are skipped and the post-load exposeRegistry() exposes the ENTIRE
// registry to HA/HomeKit. That asymmetry (LoadInitialData/Reload loaded modules, the
// reset path did not) was the original "after restart HA shows everything" bug.
func TestReloadLuaEngineLoadsUserModules(t *testing.T) {
	dir := t.TempDir()
	for _, sub := range []string{"a", "modules"} {
		if err := os.MkdirAll(filepath.Join(dir, sub), 0o755); err != nil {
			t.Fatal(err)
		}
	}

	// modules/dom.lua exposes an object under a path only this file knows. If the
	// reset path skips LoadUserModules, neither the global nor the expose appear.
	dom := `
		_RESET_PROBE = (_RESET_PROBE or 0) + 1
		expose({
			_type = "DOUT",
			get = function() return 0 end,
			set = function() end,
			onChange = function(self, cb) return function() end end,
		}, "switch", { name = "ProbeLamp", path = "probe:lamp" })
	`
	if err := os.WriteFile(filepath.Join(dir, "modules", "dom.lua"), []byte(dom), 0o644); err != nil {
		t.Fatal(err)
	}

	engine := lua.NewLuaEngine()
	defer engine.Close()

	s := &Server{
		serverDir:    dir,
		deviceConfig: &config.CLUDeviceConfig{FirmwareVersionStr: "test"},
		luaEngine:    engine,
	}
	// Mirror production: the post-load hook auto-exposes the rest of the registry.
	s.SetOnLuaReset(func() { _, _ = engine.Execute("exposeRegistry()") })

	s.reloadLuaEngine()

	// modules/dom.lua must have run exactly once on the reset path...
	if got, _ := engine.Execute("tostring(_RESET_PROBE)"); got != "1" {
		t.Fatalf("modules/dom.lua not loaded on reset path: _RESET_PROBE=%q (want 1)", got)
	}
	// ...and its manual expose() must have taken effect.
	if got, _ := engine.Execute(`tostring(ExposedObjects:get("probe:lamp") ~= nil)`); got != "true" {
		t.Fatal("manual expose() from modules/dom.lua did not take effect on reset path " +
			"(reloadLuaEngine likely missing LoadUserModules)")
	}
}
