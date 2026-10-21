package mcp

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/Domktorymysli/vclu/pkg/lua"
	mcplib "github.com/mark3labs/mcp-go/mcp"
)

// Contract "MCP device tools": the JSON that programs calling vclu_devices, vclu_device_set,
// vclu_device_execute, vclu_scenes and vclu_scene_execute rely on (see CONTRIBUTING.md).

// newContractServer runs the real Lua runtime behind the MCP handlers, as in production.
func newContractServer(t *testing.T) *MCPServer {
	t.Helper()
	engine := lua.NewLuaEngine()
	t.Cleanup(engine.Close)
	if _, err := engine.Execute(`
		local sw = VirtualSwitch:new("sw")
		registerObject("virt.sw", sw)
		expose(sw, "switch", { path = "virt.sw", name = "Lamp" })

		local dim = VirtualDimmer:new("dim")
		registerObject("virt.dim", dim)
		expose(dim, "dimmer", { path = "virt.dim", name = "Dimmer" })

		local cov = VirtualCover:new("cov")
		registerObject("virt.cov", cov)
		expose(cov, "cover", { path = "virt.cov", name = "Blind" })

		local ro = VirtualSwitch:new("ro")
		registerObject("virt.ro", ro)
		expose(ro, "switch", { path = "virt.ro", name = "Read only" })

		expose({ execute = function() end }, "scene", { path = "scene.movie", name = "Movie" })
	`); err != nil {
		t.Fatal(err)
	}

	access := newMockAccessChecker()
	access.controlPaths["virt.ro"] = false
	srv := NewMCPServer(NewDeviceProvider(engine), access, &mockConfigProvider{}, &mockMetrics{})
	srv.SetEnabled(true)
	return srv
}

// callTool calls a handler with a bot in the context and decodes the result as plain JSON.
func callTool(t *testing.T, h func(ctx context.Context, req mcplib.CallToolRequest) (*mcplib.CallToolResult, error), args map[string]any) map[string]any {
	t.Helper()
	res, err := h(botCtx(), makeCallRequest(args))
	if err != nil {
		t.Fatal(err)
	}
	text := extractText(t, res)
	if res.IsError {
		t.Fatalf("tool error: %s", text)
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(text), &m); err != nil {
		t.Fatalf("not a JSON object: %v\nraw: %s", err, text)
	}
	return m
}

// devicesByPath returns the devices[] entries of vclu_devices, keyed by path.
func devicesByPath(t *testing.T, srv *MCPServer) map[string]map[string]any {
	t.Helper()
	resp := callTool(t, srv.handleDevices, nil)
	list, ok := resp["devices"].([]any)
	if !ok {
		t.Fatalf("devices: want array, got %T (%v)", resp["devices"], resp["devices"])
	}
	out := map[string]map[string]any{}
	for i, item := range list {
		d, ok := item.(map[string]any)
		if !ok {
			t.Fatalf("devices[%d]: want object, got %T", i, item)
		}
		path, ok := d["path"].(string)
		if !ok {
			t.Fatalf("devices[%d].path: want string, got %T", i, d["path"])
		}
		out[path] = d
	}
	return out
}

func wantNumber(t *testing.T, what string, v any, lo, hi float64) float64 {
	t.Helper()
	n, ok := v.(float64)
	if !ok {
		t.Fatalf("%s: want number, got %T (%v)", what, v, v)
	}
	if n < lo || n > hi {
		t.Fatalf("%s: %v outside %v..%v", what, n, lo, hi)
	}
	return n
}

func TestContract_MCPDevices_Shape(t *testing.T) {
	srv := newContractServer(t)
	devices := devicesByPath(t, srv)

	for _, path := range []string{"virt.sw", "virt.dim", "virt.cov", "virt.ro"} {
		d, ok := devices[path]
		if !ok {
			t.Fatalf("%s missing from devices[]: %v", path, devices)
		}
		if _, ok := d["type"].(string); !ok {
			t.Errorf("%s.type: want string, got %T", path, d["type"])
		}
		if _, ok := d["name"].(string); !ok {
			t.Errorf("%s.name: want string, got %T", path, d["name"])
		}
		if _, ok := d["state"]; !ok {
			t.Errorf("%s.state: missing", path)
		}
		if _, ok := d["readonly"].(bool); !ok {
			t.Errorf("%s.readonly: want bool, got %T", path, d["readonly"])
		}
		methods, ok := d["methods"].([]any)
		if !ok {
			t.Errorf("%s.methods: want array, got %T", path, d["methods"])
		}
		for i, m := range methods {
			if _, ok := m.(string); !ok {
				t.Errorf("%s.methods[%d]: want string, got %T", path, i, m)
			}
		}
	}

	dim := devices["virt.dim"]
	if dim["type"] != "dimmer" || dim["name"] != "Dimmer" || dim["readonly"] != false {
		t.Errorf("dimmer: %v", dim)
	}
	if devices["virt.cov"]["type"] != "cover" {
		t.Errorf("cover type: %v", devices["virt.cov"]["type"])
	}
	// A device the bot may only read is reported as readonly
	if devices["virt.ro"]["readonly"] != true {
		t.Errorf("virt.ro readonly: %v", devices["virt.ro"]["readonly"])
	}
}

func TestContract_MCPDeviceSet_Scales(t *testing.T) {
	srv := newContractServer(t)

	// Dimmer: state is 0..1, not percent
	resp := callTool(t, srv.handleDeviceSet, map[string]any{"path": "virt.dim", "value": 0.5})
	if resp["ok"] != true {
		t.Fatalf("dimmer set ok: %v", resp)
	}
	if n := wantNumber(t, "dimmer new_state", resp["new_state"], 0, 1); n != 0.5 {
		t.Errorf("dimmer new_state: want 0.5, got %v", n)
	}
	if n := wantNumber(t, "dimmer state", devicesByPath(t, srv)["virt.dim"]["state"], 0, 1); n != 0.5 {
		t.Errorf("dimmer state in vclu_devices: want 0.5, got %v", n)
	}

	// Cover: position 0..100, 0 = closed
	resp = callTool(t, srv.handleDeviceSet, map[string]any{"path": "virt.cov", "value": 40})
	if resp["ok"] != true {
		t.Fatalf("cover set ok: %v", resp)
	}
	if n := wantNumber(t, "cover new_state", resp["new_state"], 0, 100); n != 40 {
		t.Errorf("cover new_state: want 40, got %v", n)
	}
	if n := wantNumber(t, "cover state", devicesByPath(t, srv)["virt.cov"]["state"], 0, 100); n != 40 {
		t.Errorf("cover state in vclu_devices: want 40, got %v", n)
	}

	// Switch: 1/0
	resp = callTool(t, srv.handleDeviceSet, map[string]any{"path": "virt.sw", "value": 1})
	if resp["ok"] != true {
		t.Fatalf("switch set ok: %v", resp)
	}
	if n := wantNumber(t, "switch new_state", resp["new_state"], 0, 1); n != 1 {
		t.Errorf("switch new_state: want 1, got %v", n)
	}
}

func TestContract_MCPDeviceExecute_Shape(t *testing.T) {
	srv := newContractServer(t)

	resp := callTool(t, srv.handleDeviceExecute, map[string]any{
		"path": "virt.cov", "method": "setPosition", "args": []any{100},
	})
	if resp["ok"] != true {
		t.Fatalf("execute ok: %v", resp)
	}
	// result is present even when the method returns nothing
	if r, ok := resp["result"]; !ok || r != nil {
		t.Errorf("execute result: want null, got %v (present %v)", r, ok)
	}
	if n := wantNumber(t, "cover state", devicesByPath(t, srv)["virt.cov"]["state"], 0, 100); n != 100 {
		t.Errorf("cover state after setPosition(100): want 100, got %v", n)
	}

	resp = callTool(t, srv.handleDeviceExecute, map[string]any{"path": "virt.cov", "method": "getPosition"})
	if resp["ok"] != true {
		t.Fatalf("execute ok: %v", resp)
	}
	if n := wantNumber(t, "getPosition result", resp["result"], 0, 100); n != 100 {
		t.Errorf("getPosition result: want 100, got %v", n)
	}
}

func TestContract_MCPScenes_Shape(t *testing.T) {
	srv := newContractServer(t)

	resp := callTool(t, srv.handleScenes, nil)
	list, ok := resp["scenes"].([]any)
	if !ok || len(list) != 1 {
		t.Fatalf("scenes: want array of 1, got %T (%v)", resp["scenes"], resp["scenes"])
	}
	sc, ok := list[0].(map[string]any)
	if !ok {
		t.Fatalf("scenes[0]: want object, got %T", list[0])
	}
	if sc["path"] != "scene.movie" || sc["name"] != "Movie" {
		t.Errorf("scenes[0]: want path scene.movie, name Movie, got %v", sc)
	}

	resp = callTool(t, srv.handleSceneExecute, map[string]any{"path": "scene.movie"})
	if resp["ok"] != true {
		t.Errorf("scene execute: %v", resp)
	}
}
