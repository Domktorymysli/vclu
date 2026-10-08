package lua

import (
	"strings"
	"testing"
)

// LoadUserModules must report per-file outcomes. A reload that "succeeds" while
// modules/init.lua failed to load leaves the runtime silently broken — callers
// (vclu_reload, vclu_draft_promote) rely on these results to say so.

func TestLoadUserModules_ReportsFailure(t *testing.T) {
	serverDir := createTestModulesDir(t, map[string]string{
		"init.lua": `this is not valid lua ((`,
		"ok.lua":   `_okLoaded = true`,
	})

	engine := NewLuaEngine()
	defer engine.Close()

	results := engine.LoadUserModules(serverDir)

	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d: %+v", len(results), results)
	}

	byName := map[string]ModuleLoadResult{}
	for _, r := range results {
		byName[r.Name] = r
	}

	init, ok := byName["init.lua"]
	if !ok {
		t.Fatal("no result for init.lua")
	}
	if init.OK {
		t.Error("init.lua has a syntax error but was reported as loaded")
	}
	if init.Error == "" {
		t.Error("failed module must carry an error message")
	}

	good, ok := byName["ok.lua"]
	if !ok {
		t.Fatal("no result for ok.lua")
	}
	if !good.OK {
		t.Errorf("ok.lua should have loaded, got error: %s", good.Error)
	}
}

func TestLoadUserModules_LastModuleLoadRetained(t *testing.T) {
	serverDir := createTestModulesDir(t, map[string]string{
		"broken.lua": `error("boom")`,
	})

	engine := NewLuaEngine()
	defer engine.Close()

	engine.LoadUserModules(serverDir)

	last := engine.LastModuleLoad()
	if len(last) != 1 {
		t.Fatalf("expected 1 retained result, got %d", len(last))
	}
	if last[0].OK {
		t.Error("broken.lua reported as loaded")
	}
	if !strings.Contains(last[0].Error, "boom") {
		t.Errorf("retained error should mention the failure, got %q", last[0].Error)
	}
}

func TestLoadUserModules_AllOKReportsNoFailures(t *testing.T) {
	serverDir := createTestModulesDir(t, map[string]string{
		"init.lua": `_a = 1`,
		"b.lua":    `_b = 2`,
	})

	engine := NewLuaEngine()
	defer engine.Close()

	results := engine.LoadUserModules(serverDir)
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}
	for _, r := range results {
		if !r.OK {
			t.Errorf("%s unexpectedly failed: %s", r.Name, r.Error)
		}
	}
}

// A require() of a missing subdirectory module must be reported as a failure of
// the requiring file — this is the exact shape of the bug that shipped silently.
func TestLoadUserModules_FailedRequireReported(t *testing.T) {
	serverDir := createTestModulesDir(t, map[string]string{
		"init.lua": `require("dashboards/nope")`,
	})

	engine := NewLuaEngine()
	defer engine.Close()

	results := engine.LoadUserModules(serverDir)
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].OK {
		t.Fatal("init.lua required a missing module but was reported as loaded")
	}
	if !strings.Contains(results[0].Error, "nope") {
		t.Errorf("error should name the missing module, got %q", results[0].Error)
	}
}

// Reinitialize must drop the previous report. A reload can fail before it ever
// reaches LoadUserModules; if the old report survived, a caller would read past
// successes and conclude the modules are fine while the runtime is broken.
func TestReinitialize_ClearsLastModuleLoad(t *testing.T) {
	serverDir := createTestModulesDir(t, map[string]string{
		"ok.lua": `_x = 1`,
	})

	engine := NewLuaEngine()
	defer engine.Close()

	engine.LoadUserModules(serverDir)
	if len(engine.LastModuleLoad()) != 1 {
		t.Fatalf("setup: expected 1 result, got %d", len(engine.LastModuleLoad()))
	}

	if err := engine.Reinitialize("VCLU"); err != nil {
		t.Fatalf("Reinitialize: %v", err)
	}

	if got := engine.LastModuleLoad(); len(got) != 0 {
		t.Errorf("stale module report survived Reinitialize: %+v", got)
	}
}

// The slice returned by LoadUserModules must not alias the retained report.
// Note this must go through LoadUserModules, not LastModuleLoad: the latter
// already copied before the fix, so asserting on it would pass either way.
func TestLoadUserModules_ReturnsIndependentCopy(t *testing.T) {
	serverDir := createTestModulesDir(t, map[string]string{
		"ok.lua": `_x = 1`,
	})

	engine := NewLuaEngine()
	defer engine.Close()

	results := engine.LoadUserModules(serverDir)
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	results[0].Name = "mutated"

	if retained := engine.LastModuleLoad(); retained[0].Name == "mutated" {
		t.Error("LoadUserModules returned a slice aliasing the retained report")
	}
}

func TestLoadUserModules_NoModulesDirReportsNothing(t *testing.T) {
	serverDir := t.TempDir()

	engine := NewLuaEngine()
	defer engine.Close()

	if results := engine.LoadUserModules(serverDir); len(results) != 0 {
		t.Errorf("expected no results without a modules dir, got %+v", results)
	}
}
