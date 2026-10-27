package mcp

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	mcplib "github.com/mark3labs/mcp-go/mcp"
)

// callRegistered calls a tool as the MCP server dispatches it, through the registered handler.
func callRegistered(t *testing.T, srv *MCPServer, name string, args map[string]any) *mcplib.CallToolResult {
	t.Helper()
	tool := srv.server.GetTool(name)
	if tool == nil {
		t.Fatalf("tool %s not registered", name)
	}
	req := makeCallRequest(args)
	req.Params.Name = name
	res, err := tool.Handler(devBotCtx(true), req)
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func TestToolArgs_UnknownNamedWithSuggestion(t *testing.T) {
	srv := phase2Harness(&mockLuaExecutor{result: "1"}, nil)
	for _, tc := range []struct {
		tool string
		args map[string]any
		want []string
	}{
		{"vclu_logs", map[string]any{"search": "MQTT"}, []string{`unknown argument "search" for vclu_logs`, `did you mean "filter"?`, "Valid: filter, level, limit"}},
		{"vclu_logs", map[string]any{"lines": 20.0}, []string{`"lines"`, `did you mean "limit"?`}},
		{"vclu_logs", map[string]any{"levl": "error"}, []string{`did you mean "level"?`}},
		{"vclu_exec", map[string]any{"code": "return 1"}, []string{`"code"`, `did you mean "script"?`, "Valid: script"}},
	} {
		res := callRegistered(t, srv, tc.tool, tc.args)
		text := extractText(t, res)
		if !res.IsError {
			t.Errorf("%s %v: accepted: %s", tc.tool, tc.args, text)
			continue
		}
		for _, w := range tc.want {
			if !strings.Contains(text, w) {
				t.Errorf("%s %v: %q lacks %q", tc.tool, tc.args, text, w)
			}
		}
	}
}

func TestToolArgs_FarNameHasNoSuggestion(t *testing.T) {
	srv := phase2Harness(nil, nil)
	text := extractText(t, callRegistered(t, srv, "vclu_logs", map[string]any{"xyzzy": 1.0}))
	if !strings.Contains(text, `unknown argument "xyzzy"`) || strings.Contains(text, "did you mean") {
		t.Errorf("answer = %s", text)
	}
}

func TestToolArgs_MissingRequired(t *testing.T) {
	srv := phase2Harness(nil, nil)
	res := callRegistered(t, srv, "vclu_list_files", nil)
	text := extractText(t, res)
	if !res.IsError || !strings.Contains(text, `missing required argument "pattern" for vclu_list_files`) {
		t.Errorf("answer = %s", text)
	}
}

func TestToolArgs_NoParamsToolRejectsAny(t *testing.T) {
	srv := phase2Harness(nil, nil)
	res := callRegistered(t, srv, "vclu_scenes", map[string]any{"filter": "x"})
	text := extractText(t, res)
	if !res.IsError || !strings.Contains(text, "vclu_scenes takes no arguments") {
		t.Errorf("answer = %s", text)
	}
}

func TestToolArgs_ValidPass(t *testing.T) {
	srv := phase2Harness(&mockLuaExecutor{result: "1"}, nil)
	srv.SetLogProvider(newLogServer(t).logs)
	for _, tc := range []struct {
		tool string
		args map[string]any
	}{
		{"vclu_logs", map[string]any{"filter": "x", "level": "error", "limit": 5.0}},
		{"vclu_logs", nil},
		{"vclu_exec", map[string]any{"script": "return 1"}},
		{"vclu_scenes", nil},
		{"vclu_scenes", map[string]any{}},
	} {
		if res := callRegistered(t, srv, tc.tool, tc.args); res.IsError {
			t.Errorf("%s %v: %s", tc.tool, tc.args, extractText(t, res))
		}
	}
}

// Every registered tool declares an object schema, so the check has its argument names.
func TestToolArgs_EverySchemaDeclared(t *testing.T) {
	srv := phase2Harness(nil, nil)
	for name, tool := range srv.server.ListTools() {
		if tool.Tool.InputSchema.Type != "object" {
			t.Errorf("%s: schema type %q", name, tool.Tool.InputSchema.Type)
		}
		for _, r := range tool.Tool.InputSchema.Required {
			if _, ok := tool.Tool.InputSchema.Properties[r]; !ok {
				t.Errorf("%s: required %q is not a property", name, r)
			}
		}
	}
}

func TestToolArgs_NumericStringTaken(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "modules"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "modules", "m.lua"), []byte("l1\nl2\nl3\n"), 0644); err != nil {
		t.Fatal(err)
	}
	srv := phase2Harness(nil, nil)
	srv.SetFileProvider(NewFileProvider(dir))

	res := callRegistered(t, srv, "vclu_read_file", map[string]any{"path": "modules/m.lua", "offset": "2", "limit": " 1 "})
	if m := parseJSON(t, res); m["content"] != "l2\n" || m["total_lines"] != 3.0 {
		t.Errorf("numeric strings: %s", extractText(t, res))
	}
	res = callRegistered(t, srv, "vclu_read_file", map[string]any{"path": "modules/m.lua", "limit": 1})
	if m := parseJSON(t, res); m["content"] != "l1\n" {
		t.Errorf("an int limit: %s", extractText(t, res))
	}
}

func TestToolArgs_NonNumericRefused(t *testing.T) {
	srv := phase2Harness(nil, nil)
	srv.SetLogProvider(newLogServer(t).logs)
	for _, tc := range []struct {
		tool string
		args map[string]any
		want string
	}{
		{"vclu_logs", map[string]any{"limit": "ten"}, `argument "limit" for vclu_logs must be a number, got "ten"`},
		{"vclu_read_file", map[string]any{"path": "modules/m.lua", "offset": "NaN"}, `"offset"`},
		{"vclu_grep", map[string]any{"pattern": "x", "max_results": true}, `"max_results"`},
		{"vclu_device_history", map[string]any{"limit": []any{1.0}}, `"limit"`},
		{"vclu_audit_log", map[string]any{"limit": ""}, `"limit"`},
	} {
		res := callRegistered(t, srv, tc.tool, tc.args)
		if text := extractText(t, res); !res.IsError || !strings.Contains(text, tc.want) {
			t.Errorf("%s %v: %s", tc.tool, tc.args, text)
		}
	}
	// a number stays as it is, and null is still an absent optional argument
	if res := callRegistered(t, srv, "vclu_logs", map[string]any{"limit": 5.0, "level": nil}); res.IsError {
		t.Errorf("valid limit refused: %s", extractText(t, res))
	}
}
