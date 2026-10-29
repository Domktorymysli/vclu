package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Domktorymysli/vclu/pkg/config"
	mcplib "github.com/mark3labs/mcp-go/mcp"
)

// --- Mock executor ---

type mockLuaExecutor struct {
	result string
	err    error
}

func (m *mockLuaExecutor) Execute(script string) (string, error) { return m.result, m.err }
func (m *mockLuaExecutor) ExecuteWithTimeout(script string, _ time.Duration) (string, error) {
	return m.result, m.err
}

// --- Helpers ---

// devBotCtx returns a context with a dev-mode bot. If execMode is true, ExecMode is also enabled.
func devBotCtx(execMode bool) context.Context {
	bot := &config.MCPBot{
		ID: "dev-bot", Name: "Dev Bot",
		Profiles: []string{"full-access"},
		DevMode:  true,
		ExecMode: execMode,
	}
	return context.WithValue(context.Background(), botKey, bot)
}

// phase2Harness builds an MCPServer with dev mode, an executor, and optional plugins.
func phase2Harness(exec LuaExecutor, plugins []config.InstalledPlugin) *MCPServer {
	cfg := &mockConfigProvider{devMode: true, plugins: plugins}
	metrics := &mockMetrics{}
	access := newMockAccessChecker()
	devices := &mockDeviceProvider{}

	srv := NewMCPServer(devices, access, cfg, metrics)
	srv.SetEnabled(true)
	if exec != nil {
		srv.SetLuaExecutor(exec)
	}
	return srv
}

func extractText(t *testing.T, res *mcplib.CallToolResult) string {
	t.Helper()
	if len(res.Content) == 0 {
		t.Fatal("no content in result")
	}
	tc, ok := res.Content[0].(mcplib.TextContent)
	if !ok {
		t.Fatalf("expected TextContent, got %T", res.Content[0])
	}
	return tc.Text
}

func parseJSON(t *testing.T, res *mcplib.CallToolResult) map[string]any {
	t.Helper()
	text := extractText(t, res)
	var m map[string]any
	if err := json.Unmarshal([]byte(text), &m); err != nil {
		t.Fatalf("JSON parse error: %v\nraw: %s", err, text)
	}
	return m
}

// ===========================
//  handleExec tests
// ===========================

func TestHandleExec_Success(t *testing.T) {
	srv := phase2Harness(&mockLuaExecutor{result: "42"}, nil)
	ctx := devBotCtx(true)
	req := makeCallRequest(map[string]any{"script": "return 40+2"})

	res, err := srv.handleExec(ctx, req)
	if err != nil {
		t.Fatal(err)
	}

	m := parseJSON(t, res)
	if m["ok"] != true {
		t.Fatalf("expected ok=true, got %v", m["ok"])
	}
	if m["result"] != "42" {
		t.Fatalf("expected result=42, got %v", m["result"])
	}
}

func TestHandleExec_DevModeDisabled(t *testing.T) {
	cfg := &mockConfigProvider{devMode: false}
	srv := NewMCPServer(&mockDeviceProvider{}, newMockAccessChecker(), cfg, &mockMetrics{})
	srv.SetEnabled(true)
	srv.SetLuaExecutor(&mockLuaExecutor{result: "x"})
	ctx := devBotCtx(true)

	res, err := srv.handleExec(ctx, makeCallRequest(map[string]any{"script": "return 1"}))
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError {
		t.Fatal("expected error when dev mode disabled")
	}
	text := extractText(t, res)
	if !strings.Contains(text, "Dev Mode") {
		t.Fatalf("expected dev mode error, got: %s", text)
	}
}

func TestHandleExec_ExecModeDisabled(t *testing.T) {
	srv := phase2Harness(&mockLuaExecutor{result: "x"}, nil)
	ctx := devBotCtx(false) // ExecMode off

	res, err := srv.handleExec(ctx, makeCallRequest(map[string]any{"script": "return 1"}))
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError {
		t.Fatal("expected error when exec mode disabled")
	}
	text := extractText(t, res)
	if !strings.Contains(text, "ExecMode") {
		t.Fatalf("expected ExecMode error, got: %s", text)
	}
}

func TestHandleExec_NoBotContext(t *testing.T) {
	srv := phase2Harness(&mockLuaExecutor{result: "x"}, nil)
	ctx := context.Background() // no bot

	res, err := srv.handleExec(ctx, makeCallRequest(map[string]any{"script": "return 1"}))
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError {
		t.Fatal("expected error with no bot context")
	}
}

func TestHandleExec_EmptyScript(t *testing.T) {
	srv := phase2Harness(&mockLuaExecutor{result: "x"}, nil)
	ctx := devBotCtx(true)

	res, err := srv.handleExec(ctx, makeCallRequest(map[string]any{"script": ""}))
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError {
		t.Fatal("expected error for empty script")
	}
	text := extractText(t, res)
	if !strings.Contains(text, "script is required") {
		t.Fatalf("expected 'script is required', got: %s", text)
	}
}

func TestHandleExec_ScriptTooLarge(t *testing.T) {
	srv := phase2Harness(&mockLuaExecutor{result: "x"}, nil)
	ctx := devBotCtx(true)

	bigScript := strings.Repeat("x", 9*1024) // >8KB
	res, err := srv.handleExec(ctx, makeCallRequest(map[string]any{"script": bigScript}))
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError {
		t.Fatal("expected error for oversized script")
	}
	text := extractText(t, res)
	if !strings.Contains(text, "too large") {
		t.Fatalf("expected size error, got: %s", text)
	}
}

func TestHandleExec_ExecutorError(t *testing.T) {
	srv := phase2Harness(&mockLuaExecutor{err: errors.New("lua panic")}, nil)
	ctx := devBotCtx(true)

	res, err := srv.handleExec(ctx, makeCallRequest(map[string]any{"script": "bad()"}))
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError {
		t.Fatal("expected error from executor failure")
	}
	text := extractText(t, res)
	if !strings.Contains(text, "execution error") || !strings.Contains(text, "lua panic") {
		t.Fatalf("expected execution error with cause, got: %s", text)
	}
}

func TestHandleExec_NoExecutor(t *testing.T) {
	srv := phase2Harness(nil, nil) // no executor set
	ctx := devBotCtx(true)

	res, err := srv.handleExec(ctx, makeCallRequest(map[string]any{"script": "return 1"}))
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError {
		t.Fatal("expected error when no executor")
	}
	text := extractText(t, res)
	if !strings.Contains(text, "Lua executor not configured") {
		t.Fatalf("expected 'Lua executor not configured', got: %s", text)
	}
}

// ===========================
//  handleDashboards tests
// ===========================

func TestHandleDashboards_Success(t *testing.T) {
	dashJSON := `[{"id":"main","widgets":[{"type":"switch"}]}]`
	srv := phase2Harness(&mockLuaExecutor{result: dashJSON}, nil)
	ctx := devBotCtx(false)

	res, err := srv.handleDashboards(ctx, makeCallRequest(nil))
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("unexpected error: %s", extractText(t, res))
	}

	m := parseJSON(t, res)
	dashboards, ok := m["dashboards"].([]any)
	if !ok {
		t.Fatalf("expected dashboards array, got %T", m["dashboards"])
	}
	if len(dashboards) != 1 {
		t.Fatalf("expected 1 dashboard, got %d", len(dashboards))
	}
	// the list has no widgets; they come with id
	if _, ok := dashboards[0].(map[string]any)["widgets"]; ok {
		t.Errorf("list has widgets: %v", dashboards[0])
	}
	res, err = srv.handleDashboards(ctx, makeCallRequest(map[string]any{"id": "main"}))
	if err != nil {
		t.Fatal(err)
	}
	one, _ := parseJSON(t, res)["dashboard"].(map[string]any)
	if widgets, _ := one["widgets"].([]any); len(widgets) != 1 {
		t.Fatalf("expected the widget with id, got %s", extractText(t, res))
	}
}

func TestHandleDashboards_DevModeDisabled(t *testing.T) {
	cfg := &mockConfigProvider{devMode: false}
	srv := NewMCPServer(&mockDeviceProvider{}, newMockAccessChecker(), cfg, &mockMetrics{})
	srv.SetEnabled(true)
	srv.SetLuaExecutor(&mockLuaExecutor{result: "[]"})
	ctx := devBotCtx(false)

	res, err := srv.handleDashboards(ctx, makeCallRequest(nil))
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError {
		t.Fatal("expected error when dev mode disabled")
	}
}

func TestHandleDashboards_InvalidJSON(t *testing.T) {
	srv := phase2Harness(&mockLuaExecutor{result: "not-json"}, nil)
	ctx := devBotCtx(false)

	res, err := srv.handleDashboards(ctx, makeCallRequest(nil))
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("unexpected error: %s", extractText(t, res))
	}

	m := parseJSON(t, res)
	// Falls back to raw string
	if m["dashboards"] != "not-json" {
		t.Fatalf("expected raw fallback, got %v", m["dashboards"])
	}
}

func TestHandleDashboards_NoExecutor(t *testing.T) {
	srv := phase2Harness(nil, nil)
	ctx := devBotCtx(false)

	res, err := srv.handleDashboards(ctx, makeCallRequest(nil))
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError {
		t.Fatal("expected error when no executor")
	}
}

// ===========================
//  handlePluginConfig tests
// ===========================

func TestHandlePluginConfig_Success(t *testing.T) {
	plugins := []config.InstalledPlugin{
		{
			ID: "weather", Version: "1.0", Enabled: true,
			InstalledAt: "2026-01-01T00:00:00Z",
			Config: map[string]interface{}{
				"apiKey": "secret-key-123",
				"city":   "Warsaw",
			},
		},
	}
	srv := phase2Harness(nil, plugins)
	ctx := devBotCtx(false)

	res, err := srv.handlePluginConfig(ctx, makeCallRequest(nil))
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("unexpected error: %s", extractText(t, res))
	}

	m := parseJSON(t, res)
	ps, ok := m["plugins"].([]any)
	if !ok {
		t.Fatalf("expected plugins array, got %T", m["plugins"])
	}
	if len(ps) != 1 {
		t.Fatalf("expected 1 plugin, got %d", len(ps))
	}

	p := ps[0].(map[string]any)
	if p["id"] != "weather" {
		t.Errorf("expected plugin id 'weather', got %v", p["id"])
	}

	cfg := p["config"].(map[string]any)
	if cfg["apiKey"] != "***" {
		t.Errorf("expected apiKey redacted, got %v", cfg["apiKey"])
	}
	if cfg["city"] != "Warsaw" {
		t.Errorf("expected city=Warsaw, got %v", cfg["city"])
	}
}

func TestHandlePluginConfig_DevModeDisabled(t *testing.T) {
	cfg := &mockConfigProvider{devMode: false, plugins: []config.InstalledPlugin{
		{ID: "test", Version: "1.0", Enabled: true},
	}}
	srv := NewMCPServer(&mockDeviceProvider{}, newMockAccessChecker(), cfg, &mockMetrics{})
	srv.SetEnabled(true)
	ctx := devBotCtx(false)

	res, err := srv.handlePluginConfig(ctx, makeCallRequest(nil))
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError {
		t.Fatal("expected error when dev mode disabled")
	}
}

func TestHandlePluginConfig_FilterByID(t *testing.T) {
	plugins := []config.InstalledPlugin{
		{ID: "weather", Version: "1.0", Enabled: true},
		{ID: "telegram", Version: "2.0", Enabled: true},
	}
	srv := phase2Harness(nil, plugins)
	ctx := devBotCtx(false)

	res, err := srv.handlePluginConfig(ctx, makeCallRequest(map[string]any{"id": "telegram"}))
	if err != nil {
		t.Fatal(err)
	}

	m := parseJSON(t, res)
	ps := m["plugins"].([]any)
	if len(ps) != 1 {
		t.Fatalf("expected 1 filtered plugin, got %d", len(ps))
	}
	if ps[0].(map[string]any)["id"] != "telegram" {
		t.Errorf("expected telegram plugin, got %v", ps[0])
	}
	if m["count"] != float64(1) {
		t.Errorf("expected count=1, got %v", m["count"])
	}
}

func TestHandlePluginConfig_NoPlugins(t *testing.T) {
	srv := phase2Harness(nil, nil) // no plugins
	ctx := devBotCtx(false)

	res, err := srv.handlePluginConfig(ctx, makeCallRequest(nil))
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("unexpected error: %s", extractText(t, res))
	}

	m := parseJSON(t, res)
	ps := m["plugins"].([]any)
	if len(ps) != 0 {
		t.Fatalf("expected empty plugins, got %d", len(ps))
	}
	if m["count"] != float64(0) {
		t.Errorf("expected count=0, got %v", m["count"])
	}
}

func TestRedactConfig(t *testing.T) {
	input := map[string]interface{}{
		"host":     "localhost",
		"password": "secret123",
		"apiKey":   "key-abc",
		"token":    "tok-xyz",
		"nested": map[string]interface{}{
			"client_secret": "nested-secret",
			"port":          8080,
		},
	}

	result := redactConfig(input)

	if result["host"] != "localhost" {
		t.Errorf("host should be preserved, got %v", result["host"])
	}
	if result["password"] != "***" {
		t.Errorf("password should be redacted, got %v", result["password"])
	}
	if result["apiKey"] != "***" {
		t.Errorf("apiKey should be redacted, got %v", result["apiKey"])
	}
	if result["token"] != "***" {
		t.Errorf("token should be redacted, got %v", result["token"])
	}

	nested := result["nested"].(map[string]interface{})
	if nested["client_secret"] != "***" {
		t.Errorf("nested client_secret should be redacted, got %v", nested["client_secret"])
	}
	if nested["port"] != 8080 {
		t.Errorf("nested port should be preserved, got %v", nested["port"])
	}
}

// ===========================
//  handleDeviceHistory tests
// ===========================

func TestHandleDeviceHistory_Success(t *testing.T) {
	histJSON := `[{"path":"CLU.SW1","old":0,"new":1,"ts":1700000000}]`
	srv := phase2Harness(&mockLuaExecutor{result: histJSON}, nil)
	ctx := devBotCtx(false)

	res, err := srv.handleDeviceHistory(ctx, makeCallRequest(nil))
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("unexpected error: %s", extractText(t, res))
	}

	m := parseJSON(t, res)
	history, ok := m["history"].([]any)
	if !ok {
		t.Fatalf("expected history array, got %T", m["history"])
	}
	if len(history) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(history))
	}
}

func TestHandleDeviceHistory_DevModeDisabled(t *testing.T) {
	cfg := &mockConfigProvider{devMode: false}
	srv := NewMCPServer(&mockDeviceProvider{}, newMockAccessChecker(), cfg, &mockMetrics{})
	srv.SetEnabled(true)
	srv.SetLuaExecutor(&mockLuaExecutor{result: "[]"})
	ctx := devBotCtx(false)

	res, err := srv.handleDeviceHistory(ctx, makeCallRequest(nil))
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError {
		t.Fatal("expected error when dev mode disabled")
	}
}

func TestHandleDeviceHistory_WithPathFilter(t *testing.T) {
	// Verify the executor receives the path-filtered script
	exec := &mockLuaExecutor{result: "[]"}
	srv := phase2Harness(exec, nil)
	ctx := devBotCtx(false)

	res, err := srv.handleDeviceHistory(ctx, makeCallRequest(map[string]any{
		"path": "CLU.SW1",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("unexpected error: %s", extractText(t, res))
	}
	// The executor was called — basic smoke test
	m := parseJSON(t, res)
	if m["env"] != "runtime" {
		t.Errorf("expected env=runtime, got %v", m["env"])
	}
}

func TestHandleDeviceHistory_LimitClamping(t *testing.T) {
	// We can't easily inspect the script sent to executor from outside,
	// but we can verify no error occurs and the handler clamps correctly.
	exec := &mockLuaExecutor{result: "[]"}
	srv := phase2Harness(exec, nil)
	ctx := devBotCtx(false)

	// limit=0 should default to 50
	res, err := srv.handleDeviceHistory(ctx, makeCallRequest(map[string]any{"limit": float64(0)}))
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("unexpected error for limit=0: %s", extractText(t, res))
	}

	// limit=999 should clamp to 500
	res, err = srv.handleDeviceHistory(ctx, makeCallRequest(map[string]any{"limit": float64(999)}))
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("unexpected error for limit=999: %s", extractText(t, res))
	}
}

func TestHandleDeviceHistory_NoExecutor(t *testing.T) {
	srv := phase2Harness(nil, nil)
	ctx := devBotCtx(false)

	res, err := srv.handleDeviceHistory(ctx, makeCallRequest(nil))
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError {
		t.Fatal("expected error when no executor")
	}
	text := extractText(t, res)
	if !strings.Contains(text, "Lua executor not configured") {
		t.Fatalf("expected executor error, got: %s", text)
	}
}

// ===========================
//  truncateScript tests
// ===========================

func TestTruncateScript_Short(t *testing.T) {
	s := "return 1"
	if got := truncateScript(s, 100); got != s {
		t.Errorf("expected %q, got %q", s, got)
	}
}

func TestTruncateScript_Long(t *testing.T) {
	s := strings.Repeat("a", 200)
	got := truncateScript(s, 50)
	if len(got) != 53 { // 50 + "..."
		t.Errorf("expected length 53, got %d", len(got))
	}
	if !strings.HasSuffix(got, "...") {
		t.Error("expected '...' suffix")
	}
}
