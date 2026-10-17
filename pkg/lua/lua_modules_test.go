package lua

import (
	"os"
	"path/filepath"
	"testing"
)

// createTestModulesDir creates a temporary server dir with modules/ and given files.
// Returns the serverDir path. Caller must defer os.RemoveAll(serverDir).
func createTestModulesDir(t *testing.T, files map[string]string) string {
	t.Helper()
	serverDir := t.TempDir()
	modulesDir := filepath.Join(serverDir, "modules")
	if err := os.MkdirAll(modulesDir, 0755); err != nil {
		t.Fatalf("failed to create modules dir: %v", err)
	}

	for name, content := range files {
		fullPath := filepath.Join(modulesDir, name)
		// Create parent dirs for subdirectory files
		if err := os.MkdirAll(filepath.Dir(fullPath), 0755); err != nil {
			t.Fatalf("failed to create dir for %s: %v", name, err)
		}
		if err := os.WriteFile(fullPath, []byte(content), 0644); err != nil {
			t.Fatalf("failed to write %s: %v", name, err)
		}
	}

	return serverDir
}

func TestLoadUserModules_InitFirst(t *testing.T) {
	// init.lua should execute before other files.
	// We verify by having init.lua set a global, then another file append to it.
	serverDir := createTestModulesDir(t, map[string]string{
		"init.lua": `_loadOrder = "init"`,
		"zzz.lua":  `_loadOrder = _loadOrder .. ",zzz"`,
		"aaa.lua":  `_loadOrder = _loadOrder .. ",aaa"`,
	})

	engine := NewLuaEngine()
	defer engine.Close()

	engine.LoadUserModules(serverDir)

	result, err := engine.Execute("_loadOrder")
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

	// init first, then aaa (alphabetically before zzz), then zzz
	expected := "init,aaa,zzz"
	if result != expected {
		t.Errorf("load order = %q, expected %q", result, expected)
	}
}

func TestLoadUserModules_NoInitLua(t *testing.T) {
	// When there's no init.lua, other files should still load alphabetically.
	serverDir := createTestModulesDir(t, map[string]string{
		"beta.lua":  `_loadOrder = (_loadOrder or "") .. "beta"`,
		"alpha.lua": `_loadOrder = (_loadOrder or "") .. "alpha,"`,
	})

	engine := NewLuaEngine()
	defer engine.Close()

	engine.LoadUserModules(serverDir)

	result, err := engine.Execute("_loadOrder")
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

	// alpha before beta (alphabetical)
	expected := "alpha,beta"
	if result != expected {
		t.Errorf("load order = %q, expected %q", result, expected)
	}
}

func TestLoadUserModules_SubdirNotAutoLoaded(t *testing.T) {
	// Files in subdirectories should NOT be auto-loaded.
	serverDir := createTestModulesDir(t, map[string]string{
		"init.lua":          `_loaded = "init"`,
		"helpers/utils.lua": `_loaded = "SHOULD_NOT_LOAD"`,
	})

	engine := NewLuaEngine()
	defer engine.Close()

	engine.LoadUserModules(serverDir)

	result, err := engine.Execute("_loaded")
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

	if result != "init" {
		t.Errorf("_loaded = %q, expected %q — subdirectory file was auto-loaded!", result, "init")
	}
}

func TestLoadUserModules_SubdirRequireWorks(t *testing.T) {
	// Subdirectory modules should be loadable via require().
	serverDir := createTestModulesDir(t, map[string]string{
		"init.lua":          `local h = require("helpers.utils"); _fromSubdir = h.msg`,
		"helpers/utils.lua": `local M = {}; M.msg = "hello from subdir"; return M`,
	})

	engine := NewLuaEngine()
	defer engine.Close()

	engine.LoadUserModules(serverDir)

	result, err := engine.Execute("_fromSubdir")
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

	if result != "hello from subdir" {
		t.Errorf("_fromSubdir = %q, expected %q", result, "hello from subdir")
	}
}

func TestLoadUserModules_EmptyDir(t *testing.T) {
	// Empty modules dir should not crash.
	serverDir := createTestModulesDir(t, map[string]string{})

	engine := NewLuaEngine()
	defer engine.Close()

	// Should not panic or error
	engine.LoadUserModules(serverDir)

	result, err := engine.Execute("type(_loadOrder)")
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	if result != "nil" {
		t.Errorf("expected nil global, got %q", result)
	}
}

func TestLoadUserModules_NoModulesDir(t *testing.T) {
	// Missing modules/ directory should not crash.
	serverDir := t.TempDir()

	engine := NewLuaEngine()
	defer engine.Close()

	// Should not panic or error
	engine.LoadUserModules(serverDir)
}

func TestLoadUserModules_NonLuaFilesIgnored(t *testing.T) {
	// .txt, .json etc should be ignored.
	serverDir := createTestModulesDir(t, map[string]string{
		"init.lua":   `_loaded = "init"`,
		"readme.txt": `this should not be loaded`,
		"data.json":  `{"not": "lua"}`,
	})

	engine := NewLuaEngine()
	defer engine.Close()

	engine.LoadUserModules(serverDir)

	result, err := engine.Execute("_loaded")
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

	if result != "init" {
		t.Errorf("_loaded = %q, expected %q", result, "init")
	}
}

func TestLoadUserModules_SyntaxErrorContinues(t *testing.T) {
	// A file with syntax error should not prevent other files from loading.
	serverDir := createTestModulesDir(t, map[string]string{
		"aaa.lua":    `_loaded = "aaa"`,
		"broken.lua": `function broken(`,
		"ccc.lua":    `_loaded = _loaded .. ",ccc"`,
	})

	engine := NewLuaEngine()
	defer engine.Close()

	engine.LoadUserModules(serverDir)

	result, err := engine.Execute("_loaded")
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

	// aaa loads, broken fails, ccc still loads
	if result != "aaa,ccc" {
		t.Errorf("_loaded = %q, expected %q", result, "aaa,ccc")
	}
}

func TestLoadUserModules_GlobalFunctionsCallable(t *testing.T) {
	// Global functions defined in user modules should be callable via Execute().
	serverDir := createTestModulesDir(t, map[string]string{
		"scenes.lua": `function movieMode() return "lights dimmed" end`,
	})

	engine := NewLuaEngine()
	defer engine.Close()

	engine.LoadUserModules(serverDir)

	result, err := engine.Execute("movieMode()")
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

	if result != "lights dimmed" {
		t.Errorf("movieMode() = %q, expected %q", result, "lights dimmed")
	}
}

// A top-level file that init.lua requires runs once, and a module loaded by
// the loader gives its result to a later require.
func TestLoadUserModules_RequiredTopLevelRunsOnce(t *testing.T) {
	serverDir := createTestModulesDir(t, map[string]string{
		"init.lua":  `_runs = {}; require("lampa")`,
		"lampa.lua": `_runs.lampa = (_runs.lampa or 0) + 1; return { name = "lampa" }`,
		"later.lua": `_runs.later = (_runs.later or 0) + 1; return { name = "later" }`,
		"zz.lua":    `_runs.got = require("later").name`,
	})
	engine := NewLuaEngine()
	defer engine.Close()

	for _, r := range engine.LoadUserModules(serverDir) {
		if !r.OK {
			t.Fatalf("%s: %s", r.Name, r.Error)
		}
	}
	got, err := engine.Execute(`_runs.lampa .. "," .. _runs.later .. "," .. _runs.got`)
	if err != nil {
		t.Fatal(err)
	}
	if got != "1,1,later" {
		t.Errorf("runs = %q, want 1,1,later", got)
	}
}
