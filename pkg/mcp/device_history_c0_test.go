package mcp

import (
	"testing"

	"github.com/Domktorymysli/vclu/pkg/history"
	"github.com/Domktorymysli/vclu/pkg/lua"
)

// vclu_device_history on a real runtime: one entry per change, old filled,
// compat new/time kept next to value/atMs.
func TestHandleDeviceHistory_RealRuntime(t *testing.T) {
	engine := lua.NewLuaEngine()
	defer engine.Close()
	store := history.New(history.DefaultConfig(), nil)
	engine.SetHistoryStore(store)
	if _, err := engine.Execute(`
		local v = VirtualSwitch:new("v")
		_registry["virt.hist"] = v
		expose(v, "switch", { path = "virt.hist" })
		_:sync("virt.hist", 1)
		_:sync("virt.hist", 1)
		_:sync("virt.hist", 0)
	`); err != nil {
		t.Fatal(err)
	}

	srv := phase2Harness(engine, nil)
	srv.SetHistoryStore(store)
	res, err := srv.handleDeviceHistory(devBotCtx(false), makeCallRequest(map[string]any{"path": "virt.hist"}))
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("unexpected error: %s", extractText(t, res))
	}
	history, ok := parseJSON(t, res)["history"].([]any)
	if !ok {
		t.Fatalf("history is not an array: %s", extractText(t, res))
	}
	if len(history) != 2 {
		t.Fatalf("want 2 entries, got %d: %s", len(history), extractText(t, res))
	}
	want := []struct{ old, value float64 }{{1, 0}, {0, 1}} // newest first
	for i, h := range history {
		e := h.(map[string]any)
		if e["old"] != want[i].old || e["value"] != want[i].value || e["new"] != want[i].value {
			t.Errorf("entry %d: %v", i, e)
		}
		atMs, _ := e["atMs"].(float64)
		if id, _ := e["id"].(string); id == "" || atMs == 0 || e["time"] != float64(int64(atMs)/1000) {
			t.Errorf("entry %d: bad id/atMs/time: %v", i, e)
		}
	}
}

// History is read-only data for any bot, but only of objects it may read: a
// hidden object looks like one that does not exist.
func TestHandleDeviceHistory_HiddenObjects(t *testing.T) {
	store := history.New(history.DefaultConfig(), nil)
	store.Record(history.Entry{Path: "light", Old: 0, Value: 1})
	store.Record(history.Entry{Path: "alarm", Old: 0, Value: 1})

	access := newMockAccessChecker()
	access.readPaths["alarm"] = false
	cfg := &mockConfigProvider{devMode: false} // no DevMode needed with the store
	srv := NewMCPServer(&mockDeviceProvider{}, access, cfg, &mockMetrics{})
	srv.SetEnabled(true)
	srv.SetHistoryStore(store)

	for name, args := range map[string]map[string]any{
		"all":    nil,
		"direct": {"path": "alarm"},
	} {
		res, err := srv.handleDeviceHistory(devBotCtx(false), makeCallRequest(args))
		if err != nil {
			t.Fatal(err)
		}
		if res.IsError {
			t.Fatalf("%s: %s", name, extractText(t, res))
		}
		for _, h := range parseJSON(t, res)["history"].([]any) {
			if h.(map[string]any)["path"] == "alarm" {
				t.Errorf("%s: hidden object in history", name)
			}
		}
	}
	res, _ := srv.handleDeviceHistory(devBotCtx(false), makeCallRequest(map[string]any{"path": "light", "since": "bad"}))
	if !res.IsError {
		t.Error("invalid since accepted")
	}
}
