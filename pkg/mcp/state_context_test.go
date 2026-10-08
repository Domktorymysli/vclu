package mcp

import (
	"testing"

	"github.com/Domktorymysli/vclu/pkg/history"
	"github.com/Domktorymysli/vclu/pkg/lua"
)

// Changes made through MCP carry the bot of the request.
func TestMCP_ChangesActAsBot(t *testing.T) {
	engine := lua.NewLuaEngine()
	defer engine.Close()
	store := history.New(history.DefaultConfig(), nil)
	engine.SetHistoryStore(store)
	if _, err := engine.Execute(`
		local v = VirtualSwitch:new("v")
		registerObject("virt.mcp", v)
		expose(v, "switch", { path = "virt.mcp" })
	`); err != nil {
		t.Fatal(err)
	}

	dp := NewDeviceProvider(engine)
	if _, err := dp.SetValue(devBotCtx(false), "virt.mcp", 1); err != nil {
		t.Fatal(err)
	}
	last, _ := store.Last("virt.mcp")
	if last.Origin != "mcp" || last.Actor != "agent:dev-bot" || last.CorrelationID == "" {
		t.Fatalf("device set: %+v", last)
	}

	srv := phase2Harness(engine, nil)
	res, err := srv.handleExec(devBotCtx(true), makeCallRequest(map[string]any{"script": `_:get("virt.mcp"):setValue(0)`}))
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("exec: %s", extractText(t, res))
	}
	last, _ = store.Last("virt.mcp")
	if last.Origin != "mcp" || last.Actor != "agent:dev-bot" {
		t.Fatalf("vclu_exec: %+v", last)
	}
}
