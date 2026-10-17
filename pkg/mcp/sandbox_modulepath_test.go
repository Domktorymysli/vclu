package mcp

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Domktorymysli/vclu/pkg/workspace"
)

// The sandbox must resolve require() exactly like production does.
//
// Production (pkg/lua.LoadUserModules) anchors package.path ON the modules/
// directory, so modules/dashboards/home.lua is require("dashboards/home").
// The sandbox previously anchored one level higher (the drafts root), which made
// vclu_draft_test accept require("modules.dashboards.home") — a path that fails
// in production — and reject the path that actually works. A green draft_test
// therefore proved nothing about require(). These tests pin the two roots together.

// newTestSandbox builds a SandboxEngine over a temp workspace + server dir.
func newTestSandbox(t *testing.T) (*SandboxEngine, *workspace.Workspace, string) {
	t.Helper()

	serverDir := t.TempDir()
	cluDataPath := filepath.Join(serverDir, "a")
	if err := os.MkdirAll(cluDataPath, 0755); err != nil {
		t.Fatalf("mkdir a/: %v", err)
	}
	// runInSandbox requires om.lua to exist.
	if err := os.WriteFile(filepath.Join(cluDataPath, "om.lua"), []byte("-- empty\n"), 0644); err != nil {
		t.Fatalf("write om.lua: %v", err)
	}

	ws, err := workspace.New(serverDir)
	if err != nil {
		t.Fatalf("workspace.New: %v", err)
	}

	runtimePath := findRuntimePath(t)
	return NewSandboxEngine(runtimePath, cluDataPath, serverDir, ws), ws, serverDir
}

// findRuntimePath locates pkg/lua/runtime relative to this test file.
func findRuntimePath(t *testing.T) string {
	t.Helper()
	// pkg/mcp -> pkg/lua/runtime
	p, err := filepath.Abs(filepath.Join("..", "lua", "runtime"))
	if err != nil {
		t.Fatalf("abs runtime path: %v", err)
	}
	if _, err := os.Stat(p); err != nil {
		t.Skipf("lua runtime not found at %s: %v", p, err)
	}
	return p
}

func writeDraft(t *testing.T, ws *workspace.Workspace, path, content string) {
	t.Helper()
	if err := ws.CreateDraft(path, content); err != nil {
		t.Fatalf("CreateDraft(%s): %v", path, err)
	}
}

// TestSandboxRequire_MatchesProductionRoot is the regression test for the bug:
// a draft in modules/dashboards/ must be require-able as "dashboards/x".
func TestSandboxRequire_MatchesProductionRoot(t *testing.T) {
	se, ws, _ := newTestSandbox(t)

	writeDraft(t, ws, "modules/dashboards/probe.lua", `return { msg = "loaded" }`)
	writeDraft(t, ws, "modules/init.lua", `
		local p = require("dashboards/probe")
		if p.msg ~= "loaded" then error("unexpected module value") end
	`)

	result, err := se.RunDraft([]string{"modules/init.lua"}, nil)
	if err != nil {
		t.Fatalf("RunDraft returned error: %v", err)
	}
	if !result.Success {
		t.Fatalf("require(\"dashboards/probe\") failed in sandbox but works in production; errors=%v",
			result.Errors)
	}
}

// Lua turns dots into path separators, so the dotted form must work identically.
func TestSandboxRequire_DottedFormWorks(t *testing.T) {
	se, ws, _ := newTestSandbox(t)

	writeDraft(t, ws, "modules/dashboards/probe.lua", `return { msg = "loaded" }`)
	writeDraft(t, ws, "modules/init.lua", `
		local p = require("dashboards.probe")
		if p.msg ~= "loaded" then error("unexpected module value") end
	`)

	result, err := se.RunDraft([]string{"modules/init.lua"}, nil)
	if err != nil {
		t.Fatalf("RunDraft returned error: %v", err)
	}
	if !result.Success {
		t.Fatalf("dotted require form failed in sandbox; errors=%v", result.Errors)
	}
}

// The inverse guard: the "modules." prefix does NOT resolve in production, so the
// sandbox must reject it too. Without this, the sandbox green-lights broken code.
func TestSandboxRequire_ModulesPrefixRejected(t *testing.T) {
	se, ws, _ := newTestSandbox(t)

	writeDraft(t, ws, "modules/dashboards/probe.lua", `return { msg = "loaded" }`)
	writeDraft(t, ws, "modules/init.lua", `require("modules.dashboards.probe")`)

	result, err := se.RunDraft([]string{"modules/init.lua"}, nil)
	if err != nil {
		t.Fatalf("RunDraft returned error: %v", err)
	}
	if result.Success {
		t.Fatal(`require("modules.dashboards.probe") succeeded in sandbox but fails in production — ` +
			`the sandbox root is anchored above modules/ again`)
	}
}

// A draft may require a module that exists only in production and was not drafted;
// drafts shadow production, production fills the gaps.
func TestSandboxRequire_FallsBackToProductionModules(t *testing.T) {
	se, ws, serverDir := newTestSandbox(t)

	prodDir := filepath.Join(serverDir, "modules", "helpers")
	if err := os.MkdirAll(prodDir, 0755); err != nil {
		t.Fatalf("mkdir prod helpers: %v", err)
	}
	if err := os.WriteFile(filepath.Join(prodDir, "utils.lua"),
		[]byte(`return { msg = "from production" }`), 0644); err != nil {
		t.Fatalf("write prod module: %v", err)
	}

	writeDraft(t, ws, "modules/init.lua", `
		local u = require("helpers/utils")
		if u.msg ~= "from production" then error("unexpected: " .. tostring(u.msg)) end
	`)

	result, err := se.RunDraft([]string{"modules/init.lua"}, nil)
	if err != nil {
		t.Fatalf("RunDraft returned error: %v", err)
	}
	if !result.Success {
		t.Fatalf("draft could not require a production-only module; errors=%v", result.Errors)
	}
}

// When a module exists in both places the draft version must win.
func TestSandboxRequire_DraftShadowsProduction(t *testing.T) {
	se, ws, serverDir := newTestSandbox(t)

	prodDir := filepath.Join(serverDir, "modules", "helpers")
	if err := os.MkdirAll(prodDir, 0755); err != nil {
		t.Fatalf("mkdir prod helpers: %v", err)
	}
	if err := os.WriteFile(filepath.Join(prodDir, "utils.lua"),
		[]byte(`return { msg = "production" }`), 0644); err != nil {
		t.Fatalf("write prod module: %v", err)
	}
	writeDraft(t, ws, "modules/helpers/utils.lua", `return { msg = "draft" }`)
	writeDraft(t, ws, "modules/init.lua", `
		local u = require("helpers/utils")
		if u.msg ~= "draft" then error("production shadowed the draft: " .. tostring(u.msg)) end
	`)

	result, err := se.RunDraft([]string{"modules/init.lua"}, nil)
	if err != nil {
		t.Fatalf("RunDraft returned error: %v", err)
	}
	if !result.Success {
		t.Fatalf("draft did not shadow production module; errors=%v", result.Errors)
	}
}

// A missing module must surface a readable error, not a silent pass.
func TestSandboxRequire_MissingModuleReportsError(t *testing.T) {
	se, ws, _ := newTestSandbox(t)

	writeDraft(t, ws, "modules/init.lua", `require("dashboards/does_not_exist")`)

	result, err := se.RunDraft([]string{"modules/init.lua"}, nil)
	if err != nil {
		t.Fatalf("RunDraft returned error: %v", err)
	}
	if result.Success {
		t.Fatal("requiring a missing module unexpectedly succeeded")
	}
	joined := strings.Join(result.Errors, " ")
	if !strings.Contains(joined, "does_not_exist") {
		t.Errorf("error should name the missing module, got: %s", joined)
	}
}

// Drafts must see the same pure libraries as production; init.lua starts with
// datetime.setTimezone, which made every draft_test fail when datetime was missing.
func TestSandbox_HasProductionLibraries(t *testing.T) {
	se, ws, _ := newTestSandbox(t)

	writeDraft(t, ws, "modules/init.lua", `
		datetime.setTimezone("Europe/Warsaw")
		if datetime.timezone() ~= "Europe/Warsaw" then error("timezone not set") end
		local d = datetime.now()
		if d.tz ~= "CET" and d.tz ~= "CEST" then error("unexpected zone " .. tostring(d.tz)) end
		if json.decode('{"a":1}').a ~= 1 then error("json missing") end
		if type(xml) ~= "table" or type(color) ~= "table" then error("xml/color missing") end
	`)

	result, err := se.RunDraft([]string{"modules/init.lua"}, nil)
	if err != nil {
		t.Fatalf("RunDraft returned error: %v", err)
	}
	if !result.Success {
		t.Fatalf("production libraries missing in sandbox; errors=%v", result.Errors)
	}
}

// As in production, a draft that a draft init.lua requires runs once.
func TestSandbox_RequiredDraftRunsOnce(t *testing.T) {
	se, ws, _ := newTestSandbox(t)
	writeDraft(t, ws, "modules/init.lua", `_runs = (_runs or 0); require("lampa")`)
	writeDraft(t, ws, "modules/lampa.lua", `
		_runs = (_runs or 0) + 1
		if _runs > 1 then error("lampa ran twice") end
	`)
	res, err := se.RunDraft([]string{"modules/init.lua", "modules/lampa.lua"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Success {
		t.Fatalf("draft failed: %v", res.Errors)
	}
}

// A draft named like a runtime module (TypeMap, string...) still runs, and
// require() of that name keeps the runtime module.
func TestSandbox_DraftWithRuntimeModuleNameRuns(t *testing.T) {
	se, ws, _ := newTestSandbox(t)
	writeDraft(t, ws, "modules/init.lua", `_tm = require("TypeMap")`)
	writeDraft(t, ws, "modules/TypeMap.lua", `_ran = (_ran or 0) + 1; return { user = true }`)
	writeDraft(t, ws, "modules/zz_check.lua", `
		assert(_ran == 1, "TypeMap.lua ran " .. tostring(_ran))
		assert(require("TypeMap") == _tm and not _tm.user, "runtime TypeMap replaced")
	`)
	res, err := se.RunDraft([]string{"modules/init.lua", "modules/TypeMap.lua", "modules/zz_check.lua"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Success {
		t.Fatalf("draft failed: %v", res.Errors)
	}
	if !strings.Contains(strings.Join(res.Logs, "\n"), "modules/TypeMap.lua has a runtime module's name") {
		t.Errorf("no warning about the name clash in logs: %v", res.Logs)
	}
}

// A subdirectory draft that the init draft requires, by either spelling, runs once.
func TestSandbox_RequiredSubdirDraftRunsOnce(t *testing.T) {
	for _, form := range []string{"scenes/x", "scenes.x"} {
		se, ws, _ := newTestSandbox(t)
		writeDraft(t, ws, "modules/init.lua", `require("`+form+`")`)
		writeDraft(t, ws, "modules/scenes/x.lua", `
			_runs = (_runs or 0) + 1
			if _runs > 1 then error("scenes/x ran twice") end
		`)
		res, err := se.RunDraft([]string{"modules/init.lua", "modules/scenes/x.lua"}, nil)
		if err != nil {
			t.Fatal(err)
		}
		if !res.Success {
			t.Errorf("require(%q): %v", form, res.Errors)
		}
	}
}

// An error in a required draft names the draft and the line; the run stops there.
func TestSandbox_RequiredDraftErrorNamesFile(t *testing.T) {
	se, ws, _ := newTestSandbox(t)
	writeDraft(t, ws, "modules/init.lua", `require("lampa")`)
	writeDraft(t, ws, "modules/lampa.lua", "local x = 1\nerror(\"boom\")")
	res, err := se.RunDraft([]string{"modules/init.lua", "modules/lampa.lua"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.Success || !strings.Contains(strings.Join(res.Errors, "\n"), "lampa.lua:2: boom") {
		t.Fatalf("errors = %v", res.Errors)
	}
}
