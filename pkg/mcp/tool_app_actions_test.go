package mcp

import (
	"context"
	"strings"
	"testing"

	"github.com/Domktorymysli/vclu/pkg/config"
	"github.com/Domktorymysli/vclu/pkg/history"
	"github.com/Domktorymysli/vclu/pkg/lua"
)

func appsHarness(t *testing.T) (*MCPServer, *mockAccessChecker, *lua.LuaEngine) {
	t.Helper()
	engine := lua.NewLuaEngine()
	t.Cleanup(engine.Close)
	if _, err := engine.Execute(`PluginSandbox:create("@jan/zakupy", nil)
		local _, err = PluginSandbox:execute("@jan/zakupy", [==[
			local p = Plugin:new("zakupy", {})
			local items = {}
			p:action("add", {description = {pl = "Dodaj do listy", en = "Add to the list"},
				params = {item = {type = "string", required = true}}}, function(params)
				items[#items + 1] = params.item
				return {count = #items, items = items}
			end)
		]==])
		assert(err == nil, err)`); err != nil {
		t.Fatal(err)
	}
	srv := phase2Harness(engine, []config.InstalledPlugin{{ID: "@jan/zakupy", Version: "1.0.0", Enabled: true}})
	return srv, srv.access.(*mockAccessChecker), engine
}

func TestAppActionsListAndCall(t *testing.T) {
	srv, _, _ := appsHarness(t)
	res, _ := srv.handleAppActions(botCtx(), makeCallRequest(nil))
	text := extractText(t, res)
	if res.IsError || !strings.Contains(text, `"plugin":"@jan/zakupy"`) || !strings.Contains(text, `"en":"Add to the list"`) {
		t.Fatalf("list: %s", text)
	}
	res, _ = srv.handleAppActions(botCtx(), makeCallRequest(map[string]any{"plugin": "@jan/other"}))
	if text := extractText(t, res); text != `{"actions":[]}` {
		t.Errorf("filter: %s", text)
	}

	res, _ = srv.handleAppAction(botCtx(), makeCallRequest(map[string]any{
		"plugin": "@jan/zakupy", "action": "add", "params": map[string]any{"item": "mleko"}}))
	if text := extractText(t, res); res.IsError || text != `{"result":{"count":1,"items":["mleko"]}}` {
		t.Fatalf("call: %s", text)
	}
	for what, args := range map[string]map[string]any{
		"unknown param":  {"plugin": "@jan/zakupy", "action": "add", "params": map[string]any{"item": "a", "x": 1}},
		"wrong type":     {"plugin": "@jan/zakupy", "action": "add", "params": map[string]any{"item": 1}},
		"missing param":  {"plugin": "@jan/zakupy", "action": "add"},
		"params a list":  {"plugin": "@jan/zakupy", "action": "add", "params": []any{"a"}},
		"unknown action": {"plugin": "@jan/zakupy", "action": "remove"},
		"bad plugin id":  {"plugin": "../x", "action": "add"},
	} {
		res, _ := srv.handleAppAction(botCtx(), makeCallRequest(args))
		if !res.IsError {
			t.Errorf("%s: not refused: %s", what, extractText(t, res))
		}
	}
}

// Control layer: a bot sees the actions of plugins its profiles read and runs them when they
// control the plugin; no bot, nothing.
func TestAppActionsFollowProfiles(t *testing.T) {
	srv, access, _ := appsHarness(t)
	call := map[string]any{"plugin": "@jan/zakupy", "action": "add", "params": map[string]any{"item": "a"}}

	access.controlPaths["@jan/zakupy"] = false
	res, _ := srv.handleAppActions(botCtx(), makeCallRequest(nil))
	if !strings.Contains(extractText(t, res), `"name":"add"`) {
		t.Error("readonly: the actions are listed")
	}
	if res, _ := srv.handleAppAction(botCtx(), makeCallRequest(call)); !res.IsError {
		t.Error("readonly: the action ran")
	}

	access.readPaths["@jan/zakupy"] = false
	res, _ = srv.handleAppActions(botCtx(), makeCallRequest(nil))
	if text := extractText(t, res); text != `{"actions":[]}` {
		t.Errorf("hidden: %s", text)
	}

	delete(access.readPaths, "@jan/zakupy")
	delete(access.controlPaths, "@jan/zakupy")
	if res, _ := srv.handleAppAction(context.Background(), makeCallRequest(call)); !res.IsError {
		t.Error("no bot: the action ran")
	}
	res, _ = srv.handleAppActions(context.Background(), makeCallRequest(nil))
	if text := extractText(t, res); text != `{"actions":[]}` {
		t.Errorf("no bot: %s", text)
	}
}

// An action runs as the bot, like vclu_device_set.
func TestAppActionActsAsBot(t *testing.T) {
	srv, _, engine := appsHarness(t)
	store := history.New(history.DefaultConfig(), nil)
	engine.SetHistoryStore(store)
	if _, err := engine.Execute(`
		local v = VirtualSwitch:new("v")
		registerObject("virt.app", v)
		expose(v, "switch", { path = "virt.app" })
		PluginSandbox.sandboxes["@jan/zakupy"].instance._sw = v
		PluginSandbox:execute("@jan/zakupy", [==[
			local p = Plugin.getPlugin("@jan/zakupy")
			p:action("on", {description = "x"}, function() p._sw:switchOn() return true end)
		]==])`); err != nil {
		t.Fatal(err)
	}
	res, _ := srv.handleAppAction(botCtx(), makeCallRequest(map[string]any{"plugin": "@jan/zakupy", "action": "on"}))
	if res.IsError {
		t.Fatalf("call: %s", extractText(t, res))
	}
	last, _ := store.Last("virt.app")
	if last.Origin != "mcp" || last.Actor != "agent:test-bot" {
		t.Fatalf("change: %+v", last)
	}
}

func TestRuntimeSchemaListsAppActions(t *testing.T) {
	srv, _, _ := appsHarness(t)
	res, _ := srv.handleRuntimeSchema(devBotCtx(false), makeCallRequest(nil))
	text := extractText(t, res)
	for _, want := range []string{`"apps":"in a plugin`, `"actions":[{"plugin":"@jan/zakupy","name":"add"`} {
		if !strings.Contains(text, want) {
			t.Errorf("schema: no %s", want)
		}
	}
}
