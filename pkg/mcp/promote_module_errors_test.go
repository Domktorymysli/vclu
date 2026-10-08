package mcp

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/Domktorymysli/vclu/pkg/lua"
	mcplib "github.com/mark3labs/mcp-go/mcp"
)

// End-to-end coverage of handleDraftPromote's module-failure reporting.
//
// The promoted files land on disk, but the reload that follows decides whether
// the code actually runs. These tests pin the contract that a promote which
// leaves the runtime broken must NOT report success — and, symmetrically, that
// an unrelated module broken beforehand must not fail every later promote.

// scriptedReloader is a LuaReloader + ModuleLoadReporter whose behaviour is set
// per test: it can fail the reload, and it returns a canned module report that
// can differ before and after the reload.
type scriptedReloader struct {
	reloadErr error
	reloads   int
	// before is returned until the first Reload(), after that "after" is returned.
	before []lua.ModuleLoadResult
	after  []lua.ModuleLoadResult
}

func (r *scriptedReloader) Reload() error {
	r.reloads++
	return r.reloadErr
}

func (r *scriptedReloader) LastModuleLoad() []lua.ModuleLoadResult {
	if r.reloads == 0 {
		return r.before
	}
	return r.after
}

func fail(name, errText string) lua.ModuleLoadResult {
	return lua.ModuleLoadResult{Name: name, OK: false, Error: errText}
}

// newPromoteHarness wires a real MCPServer with a real workspace over a temp dir.
func newPromoteHarness(t *testing.T, rel *scriptedReloader) *MCPServer {
	t.Helper()

	cluDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(cluDir, "a"), 0755); err != nil {
		t.Fatalf("mkdir a/: %v", err)
	}

	srv := NewMCPServer(&mockDeviceProvider{}, newMockAccessChecker(),
		&mockConfigProvider{devMode: true}, &mockMetrics{})
	srv.SetEnabled(true)
	srv.InitWorkspace(cluDir, filepath.Join("..", "lua", "runtime"), rel)
	return srv
}

func promote(t *testing.T, srv *MCPServer, draftPath, content string) PromoteResult {
	t.Helper()

	if err := srv.workspace.CreateDraft(draftPath, content); err != nil {
		t.Fatalf("CreateDraft: %v", err)
	}

	res, err := srv.handleDraftPromote(devBotCtx(false), makeCallRequest(map[string]any{"path": draftPath}))
	if err != nil {
		t.Fatalf("handleDraftPromote: %v", err)
	}

	text := res.Content[0].(mcplib.TextContent).Text
	var out PromoteResult
	if err := json.Unmarshal([]byte(text), &out); err != nil {
		t.Fatalf("parse PromoteResult from %q: %v", text, err)
	}
	return out
}

// A reload that errors out never reaches module loading, so the report stays
// empty — OK must still be false.
func TestPromote_ReloadErrorFailsPromote(t *testing.T) {
	rel := &scriptedReloader{reloadErr: fmt.Errorf("permission denied")}
	srv := newPromoteHarness(t, rel)

	got := promote(t, srv, "modules/init.lua", `_x = 1`)

	if got.OK {
		t.Error("promote reported OK despite the reload failing")
	}
	if len(got.ModuleErrors) == 0 {
		t.Fatal("expected the reload error to be reported")
	}
}

// The regression this file exists for: a module that was already broken, is
// promoted, and is STILL broken but with a different error, must not be waved
// through as "pre-existing". Fixing a broken module with a broken fix is the
// common case, and reporting success there is the silent failure we set out to
// remove.
func TestPromote_SameModuleNewErrorIsNotMasked(t *testing.T) {
	rel := &scriptedReloader{
		before: []lua.ModuleLoadResult{fail("init.lua", "OLD: attempt to call a nil value")},
		after:  []lua.ModuleLoadResult{fail("init.lua", "NEW: unexpected symbol near ')'")},
	}
	srv := newPromoteHarness(t, rel)

	got := promote(t, srv, "modules/init.lua", `_x = 1`)

	if got.OK {
		t.Error("promote of a still-broken module reported OK")
	}
	if len(got.ModuleErrors) != 1 {
		t.Fatalf("expected 1 module error, got %v (preexisting=%v)", got.ModuleErrors, got.PreexistingErrors)
	}
	if len(got.PreexistingErrors) != 0 {
		t.Errorf("the failure of a just-promoted module must not be filed as pre-existing: %v",
			got.PreexistingErrors)
	}
}

// Even with an identical error, a module that was itself promoted is never
// demoted — the promote did not fix it, and that is this promote's business.
func TestPromote_PromotedModuleSameErrorStillFails(t *testing.T) {
	same := "boom"
	rel := &scriptedReloader{
		before: []lua.ModuleLoadResult{fail("init.lua", same)},
		after:  []lua.ModuleLoadResult{fail("init.lua", same)},
	}
	srv := newPromoteHarness(t, rel)

	got := promote(t, srv, "modules/init.lua", `_x = 1`)

	if got.OK {
		t.Error("promoting a module that remains broken reported OK")
	}
	if len(got.PreexistingErrors) != 0 {
		t.Errorf("a promoted module must never be filed as pre-existing: %v", got.PreexistingErrors)
	}
}

// The case the demotion exists for: an unrelated module was already broken with
// the same error. It must be reported, but must not fail this promote.
func TestPromote_UnrelatedPreexistingFailureDoesNotFailPromote(t *testing.T) {
	same := "long-standing breakage"
	rel := &scriptedReloader{
		before: []lua.ModuleLoadResult{fail("legacy.lua", same)},
		after:  []lua.ModuleLoadResult{fail("legacy.lua", same)},
	}
	srv := newPromoteHarness(t, rel)

	got := promote(t, srv, "modules/dashboards/new.lua", `_y = 1`)

	if !got.OK {
		t.Errorf("an unrelated pre-existing failure must not fail the promote; errors=%v", got.ModuleErrors)
	}
	if len(got.PreexistingErrors) != 1 {
		t.Errorf("the pre-existing failure must still be reported, got %v", got.PreexistingErrors)
	}
	if len(got.ModuleErrors) != 0 {
		t.Errorf("unrelated pre-existing failure leaked into module_errors: %v", got.ModuleErrors)
	}
}

// An unrelated module that starts failing during this promote's reload is new
// breakage and must fail the promote.
func TestPromote_UnrelatedNewFailureFailsPromote(t *testing.T) {
	rel := &scriptedReloader{
		before: nil,
		after:  []lua.ModuleLoadResult{fail("legacy.lua", "suddenly broken")},
	}
	srv := newPromoteHarness(t, rel)

	got := promote(t, srv, "modules/dashboards/new.lua", `_y = 1`)

	if got.OK {
		t.Error("a newly appeared module failure must fail the promote")
	}
	if len(got.ModuleErrors) != 1 {
		t.Errorf("expected the new failure in module_errors, got %v", got.ModuleErrors)
	}
}

// A clean promote stays clean.
func TestPromote_NoFailuresReportsOK(t *testing.T) {
	rel := &scriptedReloader{}
	srv := newPromoteHarness(t, rel)

	got := promote(t, srv, "modules/init.lua", `_x = 1`)

	if !got.OK {
		t.Errorf("clean promote reported failure: %v", got.ModuleErrors)
	}
	if len(got.ModuleErrors) != 0 || len(got.PreexistingErrors) != 0 {
		t.Errorf("unexpected errors: %v / %v", got.ModuleErrors, got.PreexistingErrors)
	}
}
