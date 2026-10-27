package mcp

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Answers that carry what the agent asked for (#120): vclu_draft_edit, read ranges,
// sandbox logs without the runtime's start-up, arguments checked against the schema.

func newEditHarness(t *testing.T) (*MCPServer, string) {
	t.Helper()
	cluDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(cluDir, "a"), 0755); err != nil {
		t.Fatal(err)
	}
	srv := NewMCPServer(&mockDeviceProvider{}, newMockAccessChecker(),
		&mockConfigProvider{devMode: true}, &mockMetrics{})
	srv.SetEnabled(true)
	srv.InitWorkspace(cluDir, filepath.Join("..", "lua", "runtime"), &scriptedReloader{})
	return srv, cluDir
}

func callEdit(t *testing.T, srv *MCPServer, args map[string]any) (string, bool) {
	t.Helper()
	res, err := srv.handleDraftEdit(devBotCtx(false), makeCallRequest(args))
	if err != nil {
		t.Fatal(err)
	}
	return extractText(t, res), res.IsError
}

func editAudits(srv *MCPServer) int {
	n := 0
	for _, e := range srv.audit.GetAll() {
		if e.Tool == "draft_edit" {
			n++
		}
	}
	return n
}

func TestDraftEdit_ReplacesUniqueString(t *testing.T) {
	srv, _ := newEditHarness(t)
	content := "require(\"a\")\nrequire(\"b\")\n-- a long body that must not come back\n"
	if err := srv.workspace.CreateDraft("modules/init.lua", content); err != nil {
		t.Fatal(err)
	}
	text, isErr := callEdit(t, srv, map[string]any{
		"path": "modules/init.lua", "old_string": "require(\"b\")\n", "new_string": "require(\"b\")\nrequire(\"c\")\n",
	})
	if isErr {
		t.Fatalf("edit failed: %s", text)
	}
	if strings.Contains(text, "long body") {
		t.Errorf("answer holds the file: %s", text)
	}
	want := "require(\"a\")\nrequire(\"b\")\nrequire(\"c\")\n-- a long body that must not come back\n"
	if got, _ := srv.workspace.GetDraft("modules/init.lua"); got != want {
		t.Errorf("draft = %q", got)
	}
	m := map[string]any{}
	_ = json.Unmarshal([]byte(text), &m)
	if m["size"] != float64(len(want)) || m["replacements"] != 1.0 {
		t.Errorf("answer = %s", text)
	}
	if editAudits(srv) != 1 {
		t.Errorf("audit entries = %d, want 1", editAudits(srv))
	}
}

func TestDraftEdit_RefusesMissingOrRepeated(t *testing.T) {
	srv, _ := newEditHarness(t)
	content := "x = 1\nx = 1\n"
	if err := srv.workspace.CreateDraft("modules/a.lua", content); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ old, want string }{
		{"y = 2", "not found"},
		{"x = 1", "2 times"},
	} {
		text, isErr := callEdit(t, srv, map[string]any{"path": "modules/a.lua", "old_string": tc.old, "new_string": "z"})
		if !isErr || !strings.Contains(text, tc.want) {
			t.Errorf("old_string %q: %s", tc.old, text)
		}
	}
	if got, _ := srv.workspace.GetDraft("modules/a.lua"); got != content {
		t.Errorf("draft changed: %q", got)
	}

	// replace_all takes every occurrence
	text, isErr := callEdit(t, srv, map[string]any{"path": "modules/a.lua", "old_string": "x = 1", "new_string": "x = 2", "replace_all": true})
	if isErr || !strings.Contains(text, `"replacements":2`) {
		t.Fatalf("replace_all: %s", text)
	}
	if got, _ := srv.workspace.GetDraft("modules/a.lua"); got != "x = 2\nx = 2\n" {
		t.Errorf("draft = %q", got)
	}
}

func TestDraftEdit_StartsFromProduction(t *testing.T) {
	srv, cluDir := newEditHarness(t)
	if err := os.MkdirAll(filepath.Join(cluDir, "modules"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cluDir, "modules", "init.lua"), []byte("require(\"a\")\n"), 0644); err != nil {
		t.Fatal(err)
	}
	text, isErr := callEdit(t, srv, map[string]any{"path": "modules/init.lua", "old_string": "require(\"a\")", "new_string": "require(\"b\")"})
	if isErr {
		t.Fatalf("edit failed: %s", text)
	}
	if got, _ := srv.workspace.GetDraft("modules/init.lua"); got != "require(\"b\")\n" {
		t.Errorf("draft = %q", got)
	}
	// production stays until promote
	if prod, _ := os.ReadFile(filepath.Join(cluDir, "modules", "init.lua")); string(prod) != "require(\"a\")\n" {
		t.Errorf("production changed: %q", prod)
	}
}

func TestDraftEdit_FailsWithoutDraftOrProduction(t *testing.T) {
	srv, _ := newEditHarness(t)
	text, isErr := callEdit(t, srv, map[string]any{"path": "modules/none.lua", "old_string": "a", "new_string": "b"})
	if !isErr || !strings.Contains(text, "modules/none.lua") {
		t.Errorf("missing file: %s", text)
	}
	if editAudits(srv) != 0 {
		t.Error("a failed edit left an audit entry of a write")
	}
	if _, err := srv.workspace.GetDraft("modules/none.lua"); err == nil {
		t.Error("a draft was created")
	}
}

func TestDraftEdit_RefusesPathOutsideModules(t *testing.T) {
	srv, cluDir := newEditHarness(t)
	if err := os.WriteFile(filepath.Join(cluDir, "a", "user.lua"), []byte("x = 1\n"), 0644); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"a/user.lua", "modules/../a/user.lua"} {
		text, isErr := callEdit(t, srv, map[string]any{"path": p, "old_string": "x = 1", "new_string": "x = 2"})
		if !isErr || !strings.Contains(text, "path") {
			t.Errorf("%s: %s", p, text)
		}
	}
}

func TestReadFile_OffsetLimit(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "modules"), 0755); err != nil {
		t.Fatal(err)
	}
	content := "l1\nl2\nl3\nl4\nl5\n"
	if err := os.WriteFile(filepath.Join(dir, "modules", "m.lua"), []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	srv := phase2Harness(nil, nil)
	srv.SetFileProvider(NewFileProvider(dir))
	ctx := devBotCtx(false)

	res, _ := srv.handleReadFile(ctx, makeCallRequest(map[string]any{"path": "modules/m.lua"}))
	whole := parseJSON(t, res)
	if whole["content"] != content {
		t.Errorf("whole file = %q", whole["content"])
	}
	if _, ok := whole["total_lines"]; ok {
		t.Errorf("a call without a range changed its answer: %v", whole)
	}

	res, _ = srv.handleReadFile(ctx, makeCallRequest(map[string]any{"path": "modules/m.lua", "offset": 2.0, "limit": 2.0}))
	part := parseJSON(t, res)
	if part["content"] != "l2\nl3\n" || part["total_lines"] != 5.0 || part["offset"] != 2.0 {
		t.Errorf("range = %v", part)
	}

	res, _ = srv.handleReadFile(ctx, makeCallRequest(map[string]any{"path": "modules/m.lua", "offset": 4.0}))
	if tail := parseJSON(t, res); tail["content"] != "l4\nl5\n" {
		t.Errorf("offset to the end = %v", tail)
	}

	// a limit past int's range reads to the end
	res, _ = srv.handleReadFile(ctx, makeCallRequest(map[string]any{"path": "modules/m.lua", "offset": 2.0, "limit": 1e300}))
	if huge := parseJSON(t, res); huge["content"] != "l2\nl3\nl4\nl5\n" {
		t.Errorf("huge limit = %s", extractText(t, res))
	}

	res, _ = srv.handleReadFile(ctx, makeCallRequest(map[string]any{"path": "modules/m.lua", "offset": 9.0}))
	if !res.IsError || !strings.Contains(extractText(t, res), "5 lines") {
		t.Errorf("offset past the end: %s", extractText(t, res))
	}
}

func TestDraftTest_LeavesOutStartupLines(t *testing.T) {
	se, ws, _ := newTestSandbox(t)
	writeDraft(t, ws, "modules/probe.lua", `print("hello from probe") Logger:getShared():warn("probe warning")`)
	srv := phase2Harness(nil, nil)
	srv.sandbox, srv.workspace = se, ws

	run := func(verbose bool) string {
		args := map[string]any{"path": "modules/probe.lua", "steps": []any{map[string]any{"reload": true}}}
		if verbose {
			args["verbose"] = true
		}
		res, err := srv.handleDraftTest(devBotCtx(false), makeCallRequest(args))
		if err != nil {
			t.Fatal(err)
		}
		m := parseJSON(t, res)
		if m["success"] != true {
			t.Fatalf("draft failed: %s", extractText(t, res))
		}
		var lines []string
		for _, l := range m["logs"].([]any) {
			lines = append(lines, l.(string))
		}
		return strings.Join(lines, "\n")
	}

	logs := run(false)
	if strings.Contains(logs, "PluginHelpers loaded") {
		t.Errorf("start-up lines in logs:\n%s", logs)
	}
	// both loads (the first and the reload step) keep the module's lines
	if strings.Count(logs, "hello from probe") != 2 || strings.Count(logs, "probe warning") != 2 {
		t.Errorf("module lines missing:\n%s", logs)
	}
	if !strings.Contains(logs, "user.lua skipped") {
		t.Errorf("the sandbox's own warning is gone:\n%s", logs)
	}

	if verbose := run(true); !strings.Contains(verbose, "PluginHelpers loaded") {
		t.Errorf("verbose without start-up lines:\n%s", verbose)
	}
}

func TestDraftEdit_RefusesEmptyResult(t *testing.T) {
	srv, _ := newEditHarness(t)
	if err := srv.workspace.CreateDraft("modules/m.lua", "x = 1\n"); err != nil {
		t.Fatal(err)
	}
	text, isErr := callEdit(t, srv, map[string]any{"path": "modules/m.lua", "old_string": "x = 1\n", "new_string": ""})
	if !isErr || !strings.Contains(text, "empty") {
		t.Errorf("emptying edit: %s", text)
	}
	if got, _ := srv.workspace.GetDraft("modules/m.lua"); got != "x = 1\n" {
		t.Errorf("draft = %q", got)
	}
}

func TestReadFile_EmptyFile(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "modules"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "modules", "e.lua"), nil, 0644); err != nil {
		t.Fatal(err)
	}
	srv := phase2Harness(nil, nil)
	srv.SetFileProvider(NewFileProvider(dir))
	ctx := devBotCtx(false)

	res, _ := srv.handleReadFile(ctx, makeCallRequest(map[string]any{"path": "modules/e.lua", "offset": 1.0, "limit": 10.0}))
	if m := parseJSON(t, res); res.IsError || m["content"] != "" || m["total_lines"] != 0.0 {
		t.Errorf("offset 1 on an empty file: %s", extractText(t, res))
	}
	res, _ = srv.handleReadFile(ctx, makeCallRequest(map[string]any{"path": "modules/e.lua", "offset": 2.0}))
	if !res.IsError || !strings.Contains(extractText(t, res), "past the end") {
		t.Errorf("offset 2 on an empty file: %s", extractText(t, res))
	}
}
