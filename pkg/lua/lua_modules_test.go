package lua

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Domktorymysli/vclu/pkg/history"
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

// A user file named like a runtime module (string, debug, TypeMap...) still
// runs, and require() of that name keeps the runtime module.
func TestLoadUserModules_RuntimeModuleNameStillRuns(t *testing.T) {
	serverDir := createTestModulesDir(t, map[string]string{
		"string.lua":  `_ran = (_ran or "") .. "string,"`,
		"debug.lua":   `_ran = (_ran or "") .. "debug,"`,
		"TypeMap.lua": `_ran = (_ran or "") .. "TypeMap,"; return { user = true }`,
	})
	engine := NewLuaEngine()
	defer engine.Close()
	luaOK(t, engine, `_tm = require("TypeMap"); assert(type(_tm) == "table" and not _tm.user)`)

	for _, r := range engine.LoadUserModules(serverDir) {
		if !r.OK {
			t.Fatalf("%s: %s", r.Name, r.Error)
		}
	}
	got, err := engine.Execute(`_ran`)
	if err != nil {
		t.Fatal(err)
	}
	if got != "TypeMap,debug,string," {
		t.Errorf("ran = %q, want all three files", got)
	}
	luaOK(t, engine, `
		assert(require("TypeMap") == _tm, "runtime TypeMap replaced by modules/TypeMap.lua")
		assert(require("string") == string and string.format("%d", 1) == "1", "runtime string broken")
		assert(require("debug").traceback, "runtime debug broken")
	`)
}

// A subdirectory file that init.lua requires runs once, also when another
// top-level file requires it again, and is never run by the loader.
func TestLoadUserModules_RequiredSubdirRunsOnce(t *testing.T) {
	serverDir := createTestModulesDir(t, map[string]string{
		"init.lua":     `_runs = {}; _x = require("scenes/x")`,
		"scenes/x.lua": `_runs.x = (_runs.x or 0) + 1; return { name = "x" }`,
		"zz.lua":       `_same = require("scenes/x") == _x`,
	})
	engine := NewLuaEngine()
	defer engine.Close()

	results := engine.LoadUserModules(serverDir)
	if len(results) != 2 {
		t.Fatalf("results = %+v, want init.lua and zz.lua only", results)
	}
	for _, r := range results {
		if !r.OK {
			t.Fatalf("%s: %s", r.Name, r.Error)
		}
	}
	luaOK(t, engine, `assert(_runs.x == 1 and _same, "x ran " .. tostring(_runs.x) .. " same=" .. tostring(_same))`)
}

// The dotted require form works for a subdirectory file too.
func TestLoadUserModules_RequiredSubdirDottedForm(t *testing.T) {
	serverDir := createTestModulesDir(t, map[string]string{
		"init.lua":     `_runs = {}; require("scenes.x")`,
		"scenes/x.lua": `_runs.x = (_runs.x or 0) + 1`,
	})
	engine := NewLuaEngine()
	defer engine.Close()
	for _, r := range engine.LoadUserModules(serverDir) {
		if !r.OK {
			t.Fatalf("%s: %s", r.Name, r.Error)
		}
	}
	luaOK(t, engine, `assert(_runs.x == 1, "x ran " .. tostring(_runs.x))`)
}

// An error in a top-level file names the file and the line, as DoFile did.
func TestLoadUserModules_ErrorNamesFileAndLine(t *testing.T) {
	serverDir := createTestModulesDir(t, map[string]string{
		"broken.lua": "local x = 1\nerror(\"boom\")",
		"syntax.lua": "function broken(",
	})
	engine := NewLuaEngine()
	defer engine.Close()

	got := map[string]ModuleLoadResult{}
	for _, r := range engine.LoadUserModules(serverDir) {
		got[r.Name] = r
	}
	if r := got["broken.lua"]; r.OK || !strings.Contains(r.Error, "broken.lua:2: boom") {
		t.Errorf("broken.lua: %+v", r)
	}
	if r := got["syntax.lua"]; r.OK || !strings.Contains(r.Error, "syntax.lua") {
		t.Errorf("syntax.lua: %+v", r)
	}
}

// A top-level file that fails while init.lua requires it does not run again:
// init.lua carries the error, the file's own entry points at it.
func TestLoadUserModules_RequiredFileErrorRunsOnce(t *testing.T) {
	serverDir := createTestModulesDir(t, map[string]string{
		"init.lua":  `_runs = {}; require("lampa")`,
		"lampa.lua": "_runs.lampa = (_runs.lampa or 0) + 1\nerror(\"boom\")",
		"zz.lua":    `_zz = true`,
	})
	engine := NewLuaEngine()
	defer engine.Close()

	got := map[string]ModuleLoadResult{}
	for _, r := range engine.LoadUserModules(serverDir) {
		got[r.Name] = r
	}
	if r := got["init.lua"]; r.OK || !strings.Contains(r.Error, "lampa.lua:2: boom") {
		t.Errorf("init.lua: %+v", r)
	}
	if r := got["lampa.lua"]; r.OK || !strings.Contains(r.Error, "required it") {
		t.Errorf("lampa.lua: %+v", r)
	}
	if r := got["zz.lua"]; !r.OK {
		t.Errorf("zz.lua: %+v", r)
	}
	luaOK(t, engine, `assert(_runs.lampa == 1, "lampa ran " .. tostring(_runs.lampa))`)
}

// What a file returns is what a later require gets; nil and false count as loaded.
func TestLoadUserModules_ReturnValueCached(t *testing.T) {
	serverDir := createTestModulesDir(t, map[string]string{
		"a.lua": `return false`,
		"b.lua": `_b = 1`,
		"c.lua": `return { name = "c" }`,
	})
	engine := NewLuaEngine()
	defer engine.Close()
	for _, r := range engine.LoadUserModules(serverDir) {
		if !r.OK {
			t.Fatalf("%s: %s", r.Name, r.Error)
		}
	}
	luaOK(t, engine, `
		assert(package.loaded.a == true and package.loaded.b == true, "a/b not marked loaded")
		assert(package.loaded.c.name == "c" and require("c") == package.loaded.c, "c not cached")
	`)
}

// After a reload the files run again, also the ones init.lua requires.
func TestLoadUserModules_ReloadRunsAgain(t *testing.T) {
	serverDir := createTestModulesDir(t, map[string]string{
		"init.lua":  `_runs = {}; require("lampa")`,
		"lampa.lua": `_runs.lampa = (_runs.lampa or 0) + 1`,
		"other.lua": `_runs.other = (_runs.other or 0) + 1`,
	})
	engine := NewLuaEngine()
	defer engine.Close()
	for i := 0; i < 2; i++ {
		if i > 0 {
			if err := engine.Reinitialize("CLU"); err != nil {
				t.Fatal(err)
			}
		}
		for _, r := range engine.LoadUserModules(serverDir) {
			if !r.OK {
				t.Fatalf("load %d: %s: %s", i, r.Name, r.Error)
			}
		}
		luaOK(t, engine, `assert(_runs.lampa == 1 and _runs.other == 1, "lampa=" .. tostring(_runs.lampa) .. " other=" .. tostring(_runs.other))`)
	}
}

// A top-level module's state changes act as that module.
func TestLoadUserModules_ModuleActor(t *testing.T) {
	serverDir := createTestModulesDir(t, map[string]string{
		"lampa.lua": `
			local v = VirtualSwitch:new("lampa")
			registerObject("virt.lampa", v)
			v:setValue(1)
		`,
	})
	engine := NewLuaEngine()
	defer engine.Close()
	store := history.New(history.DefaultConfig(), nil)
	engine.SetHistoryStore(store)
	for _, r := range engine.LoadUserModules(serverDir) {
		if !r.OK {
			t.Fatalf("%s: %s", r.Name, r.Error)
		}
	}
	last, ok := store.Last("virt.lampa")
	if !ok || last.Origin != "lua" || last.Actor != "automation:module:lampa" {
		t.Fatalf("module change: %+v", last)
	}
}
