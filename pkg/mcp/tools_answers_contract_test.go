package mcp

import (
	"context"
	"fmt"
	"io"
	"strings"
	"testing"

	"github.com/Domktorymysli/vclu/pkg/logstream"
	"github.com/Domktorymysli/vclu/pkg/lua"
	mcplib "github.com/mark3labs/mcp-go/mcp"
)

// Contract "MCP dev tool answers": what an agent gets from vclu_logs and vclu_dashboards
// without arguments, and how it asks for more (see CONTRIBUTING.md).

// devCall calls a dev tool as a bot with sandbox access and decodes the answer.
func devCall(t *testing.T, h func(ctx context.Context, req mcplib.CallToolRequest) (*mcplib.CallToolResult, error), args map[string]any) map[string]any {
	t.Helper()
	res, err := h(devBotCtx(false), makeCallRequest(args))
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("tool error: %s", extractText(t, res))
	}
	return parseJSON(t, res)
}

// newLogServer feeds lines through a real LogStream, as the log package parses them.
func newLogServer(t *testing.T, lines ...string) *MCPServer {
	t.Helper()
	ls := logstream.NewLogStream(io.Discard, 2000)
	for _, l := range lines {
		if _, err := ls.Write([]byte(l + "\n")); err != nil {
			t.Fatal(err)
		}
	}
	srv := phase2Harness(nil, nil)
	srv.SetLogProvider(ls)
	return srv
}

func logEntries(t *testing.T, m map[string]any) []map[string]any {
	t.Helper()
	list, ok := m["logs"].([]any)
	if !ok {
		t.Fatalf("logs: want array, got %T", m["logs"])
	}
	out := make([]map[string]any, len(list))
	for i, e := range list {
		out[i] = e.(map[string]any)
	}
	return out
}

func TestContract_MCPLogs_DefaultHidesDebug(t *testing.T) {
	var lines []string
	for i := 0; i < 300; i++ {
		lines = append(lines,
			fmt.Sprintf("2026/10/27 21:00:00 [DEBUG] MQTT publish vclu/state/x %d", i),
			fmt.Sprintf("2026/10/27 21:00:00 [INFO] module salon: tick %d", i))
	}
	srv := newLogServer(t, lines...)

	got := logEntries(t, devCall(t, srv.handleLogs, nil))
	if len(got) != defaultLogLimit {
		t.Errorf("default answer has %d entries, want %d", len(got), defaultLogLimit)
	}
	for _, e := range got {
		if e["level"] == "debug" || strings.Contains(e["message"].(string), "MQTT publish") {
			t.Fatalf("debug entry without level: debug: %v", e)
		}
	}

	debug := logEntries(t, devCall(t, srv.handleLogs, map[string]any{"level": "debug"}))
	found := false
	for _, e := range debug {
		if e["level"] == "debug" {
			found = true
		}
	}
	if !found {
		t.Error(`level: "debug" returns no debug entries`)
	}
}

// Identical consecutive lines come as one entry with the count of repeats.
func TestContract_MCPLogs_RepeatsCollapsed(t *testing.T) {
	var lines []string
	for i := 0; i < 12; i++ {
		lines = append(lines, "2026/10/27 21:00:00 [WARN] satel: ETHM-1 busy")
	}
	lines = append(lines, "2026/10/27 21:00:01 [INFO] module salon: started")
	srv := newLogServer(t, lines...)

	got := logEntries(t, devCall(t, srv.handleLogs, nil))
	if len(got) != 2 {
		t.Fatalf("want 2 entries, got %d: %v", len(got), got)
	}
	if got[1]["message"] != "satel: ETHM-1 busy" || got[1]["repeated"] != 12.0 {
		t.Errorf("collapsed entry = %v, want the message with repeated 12", got[1])
	}
	if _, ok := got[0]["repeated"]; ok {
		t.Errorf("a single line has repeated: %v", got[0])
	}
}

// newDashboardServer runs the real Lua runtime behind vclu_dashboards.
func newDashboardServer(t *testing.T) *MCPServer {
	t.Helper()
	engine := lua.NewLuaEngine()
	t.Cleanup(engine.Close)
	if _, err := engine.Execute(`
		local lamp = VirtualSwitch:new("lamp")
		expose(lamp, "light", { path = "gab.l", name = "Lampa", area = "Gabinet" })
		Dashboard.new("biuro"):name("Biuro"):section("S")
			:room("gab", "Gabinet")
			:toggle("x", "X", "power", function() return false end, function() end)
			:build()
		Dashboard.new("start"):name("Start"):section("A")
			:toggle("y", "Y", "power", function() return false end, function() end)
			:build()
	`); err != nil {
		t.Fatal(err)
	}
	return phase2Harness(engine, nil)
}

func TestContract_MCPDashboards_ListWithoutWidgets(t *testing.T) {
	srv := newDashboardServer(t)
	m := devCall(t, srv.handleDashboards, nil)
	list, ok := m["dashboards"].([]any)
	if !ok || len(list) != 2 {
		t.Fatalf("dashboards = %v", m["dashboards"])
	}
	first := list[0].(map[string]any)
	if first["id"] != "biuro" || first["name"] != "Biuro" || first["widgetCount"] != 2.0 {
		t.Errorf("first dashboard = %v", first)
	}
	if _, ok := first["auth"]; !ok {
		t.Errorf("no auth in %v", first)
	}
	for _, d := range list {
		if _, ok := d.(map[string]any)["widgets"]; ok {
			t.Errorf("list without id has widgets: %v", d)
		}
	}
}

func TestContract_MCPDashboards_ByID(t *testing.T) {
	srv := newDashboardServer(t)
	m := devCall(t, srv.handleDashboards, map[string]any{"id": "biuro"})
	d, ok := m["dashboard"].(map[string]any)
	if !ok {
		t.Fatalf("dashboard = %v", m["dashboard"])
	}
	widgets, _ := d["widgets"].([]any)
	var ids []string
	for _, w := range widgets {
		ids = append(ids, w.(map[string]any)["id"].(string))
	}
	// the room tile, not the room's own widgets
	if strings.Join(ids, ",") != "gab,x" {
		t.Errorf("widgets = %v, want gab,x", ids)
	}

	res, err := srv.handleDashboards(devBotCtx(false), makeCallRequest(map[string]any{"id": "nope"}))
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError || !strings.Contains(extractText(t, res), "nope") {
		t.Errorf("unknown id: %s", extractText(t, res))
	}
}
