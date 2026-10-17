package mcp

import (
	"os"
	"path/filepath"
	"testing"
)

// Sandbox and the first reading of an object (#49): a set step is always a
// change for the watchers of its path, everything else follows the history.

// A second CLU with one switch that no step ever sets.
const testProxy2 = `
CLU220000593 = CLU_REMOTE:new(220000593, 0xC0A80006, system:getEventBus())
dom2 = {}
dom2.DOU1 = RemoteSwitch:new(CLU220000593, "DOU1", "x200003153_DOUT1_KORYTARZ")
registerObject("CLU220000593.DOU1", dom2.DOU1)
`

func writeProxy2(t *testing.T, serverDir string) {
	t.Helper()
	p := filepath.Join(serverDir, "imports", "proxy_220000593.lua")
	if err := os.WriteFile(p, []byte(testProxy2), 0644); err != nil {
		t.Fatal(err)
	}
}

// A set step on an exposed object (events go out under the expose path)
// fires watches on the expose path and on the registry path; a set by the
// object's name works the same; after a reload step the set still fires.
func TestScenario_SetOnExposedObjectFiresWatch(t *testing.T) {
	se, ws, serverDir := newTestSandbox(t)
	writeProxy(t, serverDir)
	writeDraft(t, ws, "modules/probe.lua", `
		local light = _:get("CLU220000592.DOU4992")
		expose(light, "switch", { path = "salon.lampa" })
		-- execute() records a CLU call without touching the state
		StateBus:watch("salon.lampa", function() light:execute(11, 0) end, { to = 1 })
		StateBus:watch("CLU220000592.DOU4992", function() light:execute(12, 0) end, { to = 1 })
	`)
	res := runScenario(t, se, []string{"modules/probe.lua"}, "2026-10-04T23:00:00+02:00", `[
		{"set": "CLU220000592.DOU4992", "value": 1},
		{"expect_clu_calls": [{"script": "DOU4992:execute(11, 0)"}, {"script": "DOU4992:execute(12, 0)"}]},
		{"expect_history": {"path": "salon.lampa", "changes": []}},
		{"set": "x200003152_DOUT1_WIATROLAP", "value": 0},
		{"expect_clu_calls": []},
		{"set": "x200003152_DOUT1_WIATROLAP", "value": 1},
		{"expect_clu_calls": [{"script": "DOU4992:execute(11, 0)"}, {"script": "DOU4992:execute(12, 0)"}]},
		{"reload": true},
		{"set": "CLU220000592.DOU4992", "value": 1},
		{"expect_clu_calls": [{"script": "DOU4992:execute(11, 0)"}, {"script": "DOU4992:execute(12, 0)"}]},
		{"expect_history": {"path": "salon.lampa", "changes": [{"value": 0, "old": 1}, {"value": 1, "old": 0}]}}
	]`)
	requireSuccess(t, res)
}

// After a reload step the object has no value again and the history knows
// the value the set repeats; the wrapper of __go_history_record is installed
// again in the new state, so the set still counts as a change.
func TestScenario_SetAfterReloadFiresWatch(t *testing.T) {
	se, ws, serverDir := newTestSandbox(t)
	writeProxy(t, serverDir)
	writeDraft(t, ws, "modules/probe.lua", `
		local light = _:get("CLU220000592.DOU4992")
		-- the argument carries e.old: 9 for nil (nothing known)
		StateBus:watch("CLU220000592.DOU4992", function(e)
			light:execute(11, e.old == nil and 9 or e.old)
		end, { to = 1 })
	`)
	res := runScenario(t, se, []string{"modules/probe.lua"}, "2026-10-04T23:00:00+02:00", `[
		{"set": "CLU220000592.DOU4992", "value": 1},
		{"expect_clu_calls": [{"script": "DOU4992:execute(11, 9)"}]},
		{"reload": true},
		{"set": "CLU220000592.DOU4992", "value": 1},
		{"expect_clu_calls": [{"script": "DOU4992:execute(11, 9)"}]},
		{"set": "CLU220000592.DOU4992", "value": 0},
		{"reload": true},
		{"set": "CLU220000592.DOU4992", "value": 1},
		{"expect_clu_calls": [{"script": "DOU4992:execute(11, 0)"}]},
		{"expect_history": {"path": "CLU220000592.DOU4992", "changes": [{"value": 0, "old": 1}, {"value": 1, "old": 0, "origin": "system"}]}}
	]`)
	requireSuccess(t, res)
}

// Changes a watch callback makes to other objects fire their watchers: a
// virtual object leaving its default, and a CLU object the scenario never
// set (in production it would have been reported before an automation acts
// on it; here nothing is known, so the automation's change is a change).
// The history still skips it as a first reading.
func TestScenario_ChainedChangesFireWatchers(t *testing.T) {
	se, ws, serverDir := newTestSandbox(t)
	writeProxy(t, serverDir)
	writeProxy2(t, serverDir)
	writeDraft(t, ws, "modules/probe.lua", `
		local light, other = _:get("CLU220000592.DOU4992"), _:get("CLU220000593.DOU1")
		registerObject("virt.mirror", VirtualSwitch:new("mirror"))
		StateBus:watch("CLU220000592.DOU4992", function()
			_:get("virt.mirror"):switchOn()
			other:switchOn()
		end, { to = 1 })
		StateBus:watch("virt.mirror", function() light:execute(13, 0) end, { to = 1 })
		StateBus:watch("CLU220000593.DOU1", function() light:execute(14, 0) end, { to = 1 })
	`)
	res := runScenario(t, se, []string{"modules/probe.lua"}, "2026-10-04T23:00:00+02:00", `[
		{"set": "CLU220000592.DOU4992", "value": 1},
		{"expect_clu_calls": [
			{"script": "DOU4992:execute(13, 0)"},
			{"script": "DOU1:execute(1, 0)", "target": "192.168.0.6"},
			{"script": "DOU4992:execute(14, 0)"}
		]},
		{"expect_state": {"CLU220000593.DOU1": 1, "virt.mirror": 1}},
		{"expect_history": {"path": "CLU220000593.DOU1", "changes": []}}
	]`)
	requireSuccess(t, res)

	// once set, the other object is known and a chained change still fires
	res = runScenario(t, se, []string{"modules/probe.lua"}, "2026-10-04T23:00:00+02:00", `[
		{"set": "CLU220000593.DOU1", "value": 0},
		{"set": "CLU220000592.DOU4992", "value": 1},
		{"expect_clu_calls": [
			{"script": "DOU4992:execute(13, 0)"},
			{"script": "DOU1:execute(1, 0)", "target": "192.168.0.6"},
			{"script": "DOU4992:execute(14, 0)"}
		]}
	]`)
	requireSuccess(t, res)
}

// An emit step is not a set: the handler's change of a virtual object is a
// plain change and fires its watcher; a set step right after is unaffected.
func TestScenario_EmitStepUnaffected(t *testing.T) {
	se, ws, serverDir := newTestSandbox(t)
	writeProxy(t, serverDir)
	writeDraft(t, ws, "modules/probe.lua", `
		local light = _:get("CLU220000592.DOU4992")
		registerObject("virt.mirror", VirtualSwitch:new("mirror"))
		EventBus:getShared():on("ruch", function() _:get("virt.mirror"):switchOn() end)
		StateBus:watch("virt.mirror", function() light:execute(15, 0) end, { to = 1 })
		StateBus:watch("CLU220000592.DOU4992", function() light:execute(16, 0) end, { to = 1 })
	`)
	res := runScenario(t, se, []string{"modules/probe.lua"}, "2026-10-04T23:00:00+02:00", `[
		{"emit": "ruch", "data": {}},
		{"expect_clu_calls": [{"script": "DOU4992:execute(15, 0)"}]},
		{"emit": "ruch", "data": {}},
		{"expect_clu_calls": []},
		{"set": "CLU220000592.DOU4992", "value": 1},
		{"expect_clu_calls": [{"script": "DOU4992:execute(16, 0)"}]}
	]`)
	requireSuccess(t, res)
}
