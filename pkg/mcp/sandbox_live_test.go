package mcp

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/Domktorymysli/vclu/pkg/lua"
)

// The sandbox takes Satel zones from the production runtime when no
// fixtures are given: SandboxObjects.export() -> SandboxObjects.load().
func TestScenario_LiveObjectsFromRuntime(t *testing.T) {
	engine := lua.NewLuaEngine()
	defer engine.Close()
	if _, err := engine.Execute(`
		local z = SatelZone:new(2)
		z:_applyState({ violated = true, name = "PIR WIATROŁAP" })
		registerObject("satel.zone.2", z)
	`); err != nil {
		t.Fatal(err)
	}

	se, ws, _ := newTestSandbox(t)
	se.SetObjectSource(func() (string, error) { return engine.Execute("SandboxObjects.export()") })
	writeDraft(t, ws, "modules/probe.lua", `
		local z = _:get("satel.zone.2")
		if not z then error("zone missing") end
		if z:getValue() ~= 1 or z.number ~= 2 or z._name ~= "PIR WIATROŁAP" then
			error("zone rebuilt wrong: " .. tostring(z:getValue()) .. " " .. tostring(z._name))
		end
	`)
	res, err := se.RunScenario([]string{"modules/probe.lua"}, SandboxScenario{
		Steps: mustSteps(t, `[{"set": "satel.zone.2", "value": 0}, {"expect_state": {"satel.zone.2": 0}}]`),
	})
	if err != nil {
		t.Fatal(err)
	}
	requireSuccess(t, res)

	// Fixtures replace the live snapshot entirely
	res, err = se.RunScenario([]string{"modules/probe.lua"}, SandboxScenario{Fixtures: []ObjectDescriptor{}})
	if err != nil {
		t.Fatal(err)
	}
	if res.Success || !strings.Contains(strings.Join(res.Errors, " "), "zone missing") {
		t.Fatalf("empty fixtures must hide live objects: %v", res.Errors)
	}
}

func TestScenario_LiveSnapshotWrongVersionSkipped(t *testing.T) {
	se, ws, _ := newTestSandbox(t)
	se.SetObjectSource(func() (string, error) { return `{"version": 99, "objects": []}`, nil })
	writeDraft(t, ws, "modules/probe.lua", `x = 1`)
	res, err := se.RunScenario([]string{"modules/probe.lua"}, SandboxScenario{})
	if err != nil {
		t.Fatal(err)
	}
	requireSuccess(t, res)
	if !strings.Contains(strings.Join(res.Logs, "\n"), "version 99") {
		t.Errorf("version mismatch not reported: %v", res.Logs)
	}
}

func TestHandleDraftTest_ScenarioArgs(t *testing.T) {
	se, ws, serverDir := newTestSandbox(t)
	writeProxy(t, serverDir)
	src, err := os.ReadFile("testdata/wiatrolap.lua")
	if err != nil {
		t.Fatal(err)
	}
	writeDraft(t, ws, "modules/tz.lua", `datetime.setTimezone("Europe/Warsaw")`)
	writeDraft(t, ws, "modules/satel/wiatrolap.lua", string(src))

	srv := phase2Harness(nil, nil)
	srv.sandbox, srv.workspace = se, ws

	var args map[string]any
	if err := json.Unmarshal([]byte(`{
		"path": ["modules/tz.lua", "modules/satel/wiatrolap.lua"],
		"at": "2026-10-04T23:00:00+02:00",
		"fixtures": [{"path": "satel.zone.2", "kind": "satel_zone", "state": 0, "config": {"number": 2}}],
		"steps": [
			{"set": "satel.zone.2", "value": 1},
			{"expect_clu_calls": [{"script": "DOU4992:execute(1, 60000)"}]}
		]
	}`), &args); err != nil {
		t.Fatal(err)
	}
	res, err := srv.handleDraftTest(devBotCtx(false), makeCallRequest(args))
	if err != nil {
		t.Fatal(err)
	}
	m := parseJSON(t, res)
	if m["success"] != true {
		t.Fatalf("scenario failed: %s", extractText(t, res))
	}
	if calls, _ := m["clu_calls"].([]any); len(calls) != 1 {
		t.Errorf("clu_calls = %v", m["clu_calls"])
	}

	// Clients with a stale schema send arrays as JSON strings
	stepsJSON, _ := json.Marshal(args["steps"])
	fixturesJSON, _ := json.Marshal(args["fixtures"])
	args["steps"], args["fixtures"] = string(stepsJSON), string(fixturesJSON)
	res, err = srv.handleDraftTest(devBotCtx(false), makeCallRequest(args))
	if err != nil {
		t.Fatal(err)
	}
	if m := parseJSON(t, res); m["success"] != true {
		t.Fatalf("string-encoded steps/fixtures: %s", extractText(t, res))
	}

	for name, bad := range map[string]map[string]any{
		"at":       {"path": "modules/tz.lua", "at": "23:00"},
		"steps":    {"path": "modules/tz.lua", "steps": []any{map[string]any{"advance": 5, "reload": true}}},
		"fixtures": {"path": "modules/tz.lua", "fixtures": []any{map[string]any{"path": "a", "klass": "x"}}},
	} {
		res, err := srv.handleDraftTest(devBotCtx(false), makeCallRequest(bad))
		if err != nil {
			t.Fatal(err)
		}
		if !res.IsError || !strings.Contains(extractText(t, res), "invalid "+name) {
			t.Errorf("%s: want 'invalid %s' error, got %s", name, name, extractText(t, res))
		}
	}
}
