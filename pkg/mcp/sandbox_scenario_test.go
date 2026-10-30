package mcp

import (
	"os"
	"strings"
	"testing"
	"time"

	lua "github.com/yuin/gopher-lua"
)

var zone2 = []ObjectDescriptor{{
	Path: "satel.zone.2", Kind: "satel_zone", State: 0.0,
	Config: map[string]any{"number": 2.0, "name": "PIR WIATROŁAP"},
}}

// newWiatrolapSandbox loads the production wiatrołap module with the
// Warsaw zone set first, as modules/init.lua does.
func newWiatrolapSandbox(t *testing.T) (*SandboxEngine, []string) {
	t.Helper()
	se, ws, serverDir := newTestSandbox(t)
	writeProxy(t, serverDir)
	src, err := os.ReadFile("testdata/wiatrolap.lua")
	if err != nil {
		t.Fatal(err)
	}
	writeDraft(t, ws, "modules/tz.lua", `datetime.setTimezone("Europe/Warsaw")`)
	writeDraft(t, ws, "modules/satel/wiatrolap.lua", string(src))
	return se, []string{"modules/tz.lua", "modules/satel/wiatrolap.lua"}
}

func mustSteps(t *testing.T, js string) []SandboxStep {
	t.Helper()
	steps, err := ParseSteps([]byte(js))
	if err != nil {
		t.Fatal(err)
	}
	return steps
}

func mustTime(t *testing.T, s string) time.Time {
	t.Helper()
	at, err := time.Parse(time.RFC3339, s)
	if err != nil {
		t.Fatal(err)
	}
	return at
}

func runScenario(t *testing.T, se *SandboxEngine, paths []string, at, steps string) *SandboxResult {
	t.Helper()
	res, err := se.RunScenario(paths, SandboxScenario{
		At: mustTime(t, at), Steps: mustSteps(t, steps), Fixtures: zone2,
	})
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func requireSuccess(t *testing.T, res *SandboxResult) {
	t.Helper()
	if !res.Success {
		t.Fatalf("scenario failed:\nerrors: %s\nlogs: %s", strings.Join(res.Errors, "\n"), strings.Join(res.Logs, "\n"))
	}
}

func TestScenario_WiatrolapNight(t *testing.T) {
	se, paths := newWiatrolapSandbox(t)
	res := runScenario(t, se, paths, "2026-10-04T23:00:00+02:00", `[
		{"set": "satel.zone.2", "value": 1},
		{"expect_clu_calls": [{"script": "DOU4992:execute(1, 60000)", "target": "192.168.0.5"}]},
		{"expect_state": {"CLU220000592.DOU4992": 1}},
		{"set": "satel.zone.2", "value": 0},
		{"advance": 61},
		{"set": "CLU220000592.DOU4992", "value": 0},
		{"set": "satel.zone.2", "value": 1},
		{"expect_clu_calls": [{"script": "DOU4992:execute(1, 60000)"}]}
	]`)
	requireSuccess(t, res)
}

func TestScenario_WiatrolapManualOffPauses(t *testing.T) {
	se, paths := newWiatrolapSandbox(t)
	res := runScenario(t, se, paths, "2026-10-04T23:00:00+02:00", `[
		{"set": "satel.zone.2", "value": 1},
		{"advance": 20},
		{"set": "CLU220000592.DOU4992", "value": 0},
		{"expect_clu_calls": [{"script": "DOU4992:execute(1, 60000)"}]},
		{"set": "satel.zone.2", "value": 0},
		{"set": "satel.zone.2", "value": 1},
		{"expect_clu_calls": []},
		{"set": "satel.zone.2", "value": 0},
		{"advance": 600},
		{"set": "satel.zone.2", "value": 1},
		{"expect_clu_calls": [{"script": "DOU4992:execute(1, 60000)"}]}
	]`)
	requireSuccess(t, res)
}

func TestScenario_WiatrolapDayAndManualOn(t *testing.T) {
	se, paths := newWiatrolapSandbox(t)
	res := runScenario(t, se, paths, "2026-10-04T12:00:00+02:00", `[
		{"set": "satel.zone.2", "value": 1},
		{"expect_clu_calls": []}
	]`)
	requireSuccess(t, res)

	res = runScenario(t, se, paths, "2026-10-04T22:00:00+02:00", `[
		{"set": "CLU220000592.DOU4992", "value": 1},
		{"set": "satel.zone.2", "value": 1},
		{"expect_clu_calls": []}
	]`)
	requireSuccess(t, res)
}

func TestScenario_FailedExpectationsReported(t *testing.T) {
	se, paths := newWiatrolapSandbox(t)
	res := runScenario(t, se, paths, "2026-10-04T12:00:00+02:00", `[
		{"set": "satel.zone.2", "value": 1},
		{"expect_clu_calls": [{"script": "DOU4992:execute(1, 60000)"}]},
		{"expect_state": {"CLU220000592.DOU4992": 1, "satel.zone.99": 0}},
		{"set": "nope.path", "value": 1}
	]`)
	if res.Success {
		t.Fatal("expected failure")
	}
	all := strings.Join(res.Errors, "\n")
	for _, want := range []string{
		"step 2 (expect_clu_calls): got 0 calls",
		"step 3 (expect_state): CLU220000592.DOU4992 = null, want 1",
		"step 3 (expect_state): no object 'satel.zone.99'",
		"step 4 (set): no object 'nope.path'",
	} {
		if !strings.Contains(all, want) {
			t.Errorf("missing %q in:\n%s", want, all)
		}
	}
}

func TestScenario_ReloadKeepsClockAndKVDropsTimers(t *testing.T) {
	se, ws, _ := newTestSandbox(t)
	writeDraft(t, ws, "modules/probe.lua", `
		local loads = (__go_kv_get("probe", "loads")) or 0
		__go_kv_set("probe", "loads", loads + 1)
		probeLoadedAt = os.time()
		probeFired = 0
		__go_setTimeout(function() __go_kv_set("probe", "fired", true) end, 30000)
	`)
	writeDraft(t, ws, "modules/check.lua", `
		function check(wantLoads, wantFired)
			local loads = __go_kv_get("probe", "loads")
			local fired = __go_kv_get("probe", "fired") == true
			if loads ~= wantLoads or fired ~= wantFired then
				error(string.format("loads=%s fired=%s", tostring(loads), tostring(fired)))
			end
		end
	`)
	res, err := se.RunScenario([]string{"modules/check.lua", "modules/probe.lua"}, SandboxScenario{
		At: mustTime(t, "2026-01-01T00:00:00Z"),
		Steps: mustSteps(t, `[
			{"advance": 10},
			{"reload": true},
			{"advance": 25},
			{"emit": "check", "data": {}},
			{"expect_state": {}}
		]`),
	})
	if err != nil {
		t.Fatal(err)
	}
	requireSuccess(t, res)

	// Old timer (due at 30 s) must not fire after the reload at 10 s; the new
	// load scheduled its own at 40 s, also not reached at 35 s.
	res, err = se.RunScenario([]string{"modules/check.lua", "modules/probe.lua"}, SandboxScenario{
		At:    mustTime(t, "2026-01-01T00:00:00Z"),
		Steps: mustSteps(t, `[{"advance": 10}, {"reload": true}, {"advance": 25}]`),
	})
	if err != nil {
		t.Fatal(err)
	}
	requireSuccess(t, res)
}

func TestScenario_MissingObjectAndUnknownKindWarn(t *testing.T) {
	se, ws, _ := newTestSandbox(t)
	writeDraft(t, ws, "modules/probe.lua", `local z = _:get("satel.zone.7")`)
	res, err := se.RunScenario([]string{"modules/probe.lua"}, SandboxScenario{
		Fixtures: []ObjectDescriptor{{Path: "x.y", Kind: "mystery"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	logs := strings.Join(res.Logs, "\n")
	for _, want := range []string{"x.y: unknown kind 'mystery'", "no object 'satel.zone.7'"} {
		if !strings.Contains(logs, want) {
			t.Errorf("missing %q in logs:\n%s", want, logs)
		}
	}
}

func TestScenario_MissingObjectWarnsForValue(t *testing.T) {
	se, ws, _ := newTestSandbox(t)
	// _:value returns nil for a wrong path; the sandbox must still say the object is missing
	writeDraft(t, ws, "modules/probe.lua", `local v = _:value("satel.zone.8")`)
	res, err := se.RunScenario([]string{"modules/probe.lua"}, SandboxScenario{})
	if err != nil {
		t.Fatal(err)
	}
	if logs := strings.Join(res.Logs, "\n"); !strings.Contains(logs, "no object 'satel.zone.8'") {
		t.Errorf("missing the no-object warning in logs:\n%s", logs)
	}
	requireSuccess(t, res)
}

func TestParseSteps_Validation(t *testing.T) {
	bad := map[string]string{
		`[{"set": "a", "value": 1, "advance": 5}]`: "exactly one",
		`[{"sett": "a"}]`:                          "unknown field",
		`[{}]`:                                     "exactly one",
		`[{"set": "a"}]`:                           "set needs a path and a value",
		`[{"advance": 0}]`:                         "advance must be",
		`[{"advance": 5, "value": 1}]`:             "value is only allowed with set",
		`[{"reload": false}]`:                      "reload must be true",
		`[{"advance": 5, "data": {}}]`:             "data is only allowed with emit",
		`{"set": "a"}`:                             "steps must be an array",
	}
	for js, want := range bad {
		if _, err := ParseSteps([]byte(js)); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: err = %v, want %q", js, err, want)
		}
	}
	if _, err := ParseSteps([]byte(`[{"set": "a", "value": null}, {"expect_clu_calls": []}]`)); err != nil {
		t.Errorf("valid steps rejected: %v", err)
	}
}

func TestScenario_LongAdvanceWithInterval(t *testing.T) {
	se, ws, _ := newTestSandbox(t)
	writeDraft(t, ws, "modules/probe.lua", `
		ticks = 0
		__go_setInterval(function() ticks = ticks + 1 end, 1000)
		function check() if ticks ~= 86400 then error("ticks=" .. ticks) end end
	`)
	start := time.Now()
	res, err := se.RunScenario([]string{"modules/probe.lua"}, SandboxScenario{
		Steps: mustSteps(t, `[{"advance": 86400}, {"emit": "noop", "data": {}}]`),
	})
	if err != nil {
		t.Fatal(err)
	}
	requireSuccess(t, res)
	// virtual time: a real 1 s sleep per tick would take a day
	limit := 2 * time.Second
	if raceEnabled {
		limit = 20 * time.Second
	}
	if d := time.Since(start); d > limit {
		t.Errorf("a day of 1 s ticks took %v", d)
	}
}

// Without modules/init.lua the draft still sees the live runtime's zone.
func TestScenario_TimezoneFromLiveRuntime(t *testing.T) {
	se, ws, serverDir := newTestSandbox(t)
	writeProxy(t, serverDir)
	src, err := os.ReadFile("testdata/wiatrolap.lua")
	if err != nil {
		t.Fatal(err)
	}
	writeDraft(t, ws, "modules/satel/wiatrolap.lua", string(src))
	se.SetObjectSource(func() (string, error) {
		return `{"version": 1, "timezone": "Europe/Warsaw", "objects": []}`, nil
	})
	// 20:30 UTC = 22:30 in Warsaw: night there, not in UTC
	res, err := se.RunScenario([]string{"modules/satel/wiatrolap.lua"}, SandboxScenario{
		At:       mustTime(t, "2026-10-04T20:30:00Z"),
		Fixtures: zone2,
		Steps: mustSteps(t, `[
			{"set": "satel.zone.2", "value": 1},
			{"expect_clu_calls": [{"script": "DOU4992:execute(1, 60000)"}]}
		]`),
	})
	if err != nil {
		t.Fatal(err)
	}
	requireSuccess(t, res)
}

func TestScenario_TimezoneArgAndDST(t *testing.T) {
	se, ws, _ := newTestSandbox(t)
	writeDraft(t, ws, "modules/probe.lua", `
		function hourIs(want)
			local h = datetime.now().hour
			if h ~= want then error("hour " .. h .. ", want " .. want) end
		end
		EventBus:getShared():on("hour", function(d) hourIs(d.want) end)
	`)
	// 22:30 CEST on 24 Oct; a day later clocks went back, so 21:30 CET
	res, err := se.RunScenario([]string{"modules/probe.lua"}, SandboxScenario{
		At:       mustTime(t, "2026-10-24T20:30:00Z"),
		Timezone: "Europe/Warsaw",
		Steps: mustSteps(t, `[
			{"emit": "hour", "data": {"want": 22}},
			{"advance": 86400},
			{"emit": "hour", "data": {"want": 21}}
		]`),
	})
	if err != nil {
		t.Fatal(err)
	}
	requireSuccess(t, res)
}

func TestScenario_SetByName(t *testing.T) {
	se, paths := newWiatrolapSandbox(t)
	res := runScenario(t, se, paths, "2026-10-04T23:00:00+02:00", `[
		{"set": "x200003152_DOUT1_WIATROLAP", "value": 1},
		{"expect_state": {"x200003152_DOUT1_WIATROLAP": 1, "CLU220000592.DOU4992": 1}}
	]`)
	requireSuccess(t, res)
}

func TestScenario_CluCallsLimit(t *testing.T) {
	se, ws, serverDir := newTestSandbox(t)
	writeProxy(t, serverDir)
	writeDraft(t, ws, "modules/probe.lua", `
		local l = _:get("CLU220000592.DOU4992")
		for i = 1, 1005 do l:execute(0, i) end
	`)
	res, err := se.RunScenario([]string{"modules/probe.lua"}, SandboxScenario{
		Steps: mustSteps(t, `[{"expect_clu_calls": []}]`),
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.CluCallsDropped != 5 || len(res.CluCalls) != maxCluCalls {
		t.Fatalf("calls=%d dropped=%d", len(res.CluCalls), res.CluCallsDropped)
	}
	if res.Success || !strings.Contains(strings.Join(res.Errors, " "), "truncated") {
		t.Fatalf("expect_clu_calls must fail on truncated calls: %v", res.Errors)
	}
}

func TestScenario_TimeoutAbortsLua(t *testing.T) {
	old := sandboxTimeout
	sandboxTimeout = 300 * time.Millisecond
	defer func() { sandboxTimeout = old }()

	se, ws, _ := newTestSandbox(t)
	writeDraft(t, ws, "modules/probe.lua", `
		EventBus:getShared():on("spin", function() while true do end end)
		__go_setInterval(function() end, 10)
	`)
	for name, steps := range map[string]string{
		"busy Lua":     `[{"emit": "spin", "data": {}}]`,
		"long advance": `[{"advance": 2000000}, {"advance": 2000000}, {"advance": 2000000}]`,
	} {
		start := time.Now()
		res, err := se.RunScenario([]string{"modules/probe.lua"}, SandboxScenario{Steps: mustSteps(t, steps)})
		if err != nil {
			t.Fatal(err)
		}
		if d := time.Since(start); d > 1500*time.Millisecond {
			t.Errorf("%s: returned after %v", name, d)
		}
		if res.Success {
			t.Errorf("%s: expected failure", name)
		}
	}
}

func TestParseSteps_AdvanceOverflow(t *testing.T) {
	for _, js := range []string{`[{"advance": 1e10}]`, `[{"advance": 1e300}]`, `[{"advance": 2678401}]`} {
		if _, err := ParseSteps([]byte(js)); err == nil {
			t.Errorf("%s accepted", js)
		}
	}
}

func TestScenario_CyclicKVValueIsAnError(t *testing.T) {
	se, ws, _ := newTestSandbox(t)
	writeDraft(t, ws, "modules/probe.lua", `
		local t = {} t.self = t
		local err = __go_kv_set("x", "y", t)
		if not tostring(err):find("cyclic") then error("got " .. tostring(err)) end
	`)
	res, err := se.RunDraft([]string{"modules/probe.lua"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	requireSuccess(t, res)
}

// om.lua event bindings may call user.lua functions while loading, as in
// production where user.lua is loaded first.
func TestSandbox_UserLuaBeforeOM(t *testing.T) {
	se, ws, serverDir := newTestSandbox(t)
	a := serverDir + "/a"
	if err := os.WriteFile(a+"/user.lua", []byte(`function userHelper() return 42 end`), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(a+"/om.lua", []byte(`omValue = userHelper()`), 0644); err != nil {
		t.Fatal(err)
	}
	writeDraft(t, ws, "modules/probe.lua", `if omValue ~= 42 then error("om.lua ran before user.lua") end`)
	res, err := se.RunDraft([]string{"modules/probe.lua"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	requireSuccess(t, res)
}

func TestScenario_StateChangesSorted(t *testing.T) {
	se, ws, _ := newTestSandbox(t)
	writeDraft(t, ws, "modules/probe.lua", `
		for _, p in ipairs({ "z.b", "a.c", "m.a", "b.x", "q.q", "c.d" }) do
			local v = VirtualSwitch:new(p)
			registerObject(p, v)
			v:setValue(1)
		end
	`)
	for i := 0; i < 5; i++ {
		res, err := se.RunDraft([]string{"modules/probe.lua"}, nil)
		if err != nil {
			t.Fatal(err)
		}
		var paths []string
		for _, c := range res.StateChanges {
			paths = append(paths, c.Path)
		}
		if got := strings.Join(paths, ","); got != "a.c,b.x,c.d,m.a,q.q,z.b" {
			t.Fatalf("run %d: state_changes order %s", i, got)
		}
	}
}

func TestScenario_AdvanceLimitAbortsScenario(t *testing.T) {
	se, ws, _ := newTestSandbox(t)
	writeDraft(t, ws, "modules/probe.lua", `
		local function again() __go_setTimeout(again, 1) end
		again()
	`)
	res, err := se.RunScenario([]string{"modules/probe.lua"}, SandboxScenario{
		Steps: mustSteps(t, `[{"advance": 3600}, {"expect_state": {}}, {"advance": 1}]`),
	})
	if err != nil {
		t.Fatal(err)
	}
	all := strings.Join(res.Errors, "\n")
	if res.Success || !strings.Contains(all, "step 1 (advance)") || strings.Contains(all, "step 3") {
		t.Fatalf("scenario must stop at the failed advance: %s", all)
	}
}

func TestSandbox_LoggerLinesOnce(t *testing.T) {
	se, ws, _ := newTestSandbox(t)
	writeDraft(t, ws, "modules/probe.lua", `
		Logger:getShared():info("once %d", 1)
		Logger:getShared():warn("100% sure")
	`)
	res, err := se.RunDraft([]string{"modules/probe.lua"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	requireSuccess(t, res)
	logs := strings.Join(res.Logs, "\n")
	if strings.Count(logs, "[INFO] once 1") != 1 || strings.Count(logs, "100% sure") != 1 {
		t.Errorf("each Logger line must appear once:\n%s", logs)
	}
}

func TestScenario_BetweenFollowsVirtualClock(t *testing.T) {
	se, ws, _ := newTestSandbox(t)
	writeDraft(t, ws, "modules/probe.lua", `
		EventBus:getShared():on("night", function(d)
			if datetime.between("21:00", "06:00") ~= d.want then
				error("between at " .. os.date("!%H:%M") .. " UTC, want " .. tostring(d.want))
			end
		end)
	`)
	res, err := se.RunScenario([]string{"modules/probe.lua"}, SandboxScenario{
		At:       mustTime(t, "2026-10-07T20:30:00+02:00"),
		Timezone: "Europe/Warsaw",
		Steps: mustSteps(t, `[
			{"emit": "night", "data": {"want": false}},
			{"advance": 1800},
			{"emit": "night", "data": {"want": true}},
			{"advance": 32400},
			{"emit": "night", "data": {"want": false}}
		]`),
	})
	if err != nil {
		t.Fatal(err)
	}
	requireSuccess(t, res)
}

func TestScenario_HandlerErrorFailsStep(t *testing.T) {
	se, ws, _ := newTestSandbox(t)
	writeDraft(t, ws, "modules/probe.lua", `
		EventBus:getShared():on("check", function(d) if not d.ok then error("handler says no") end end)
		StateBus:getShared():on("state_changed", function(e) if e.value == 13 then error("unlucky") end end)
		_v = VirtualSwitch:new("v")
		registerObject("virt.v", _v)
	`)
	res, err := se.RunScenario([]string{"modules/probe.lua"}, SandboxScenario{
		Events: []InjectEvent{{Name: "check", Data: map[string]any{"ok": false}}},
		Steps: mustSteps(t, `[
			{"emit": "check", "data": {"ok": true}},
			{"emit": "check", "data": {"ok": false}},
			{"set": "virt.v", "value": 13}
		]`),
	})
	if err != nil {
		t.Fatal(err)
	}
	all := strings.Join(res.Errors, "\n")
	for _, want := range []string{"event check:", "step 2 (emit): runtime logged an error", "handler says no", "step 3 (set): runtime logged an error", "unlucky"} {
		if !strings.Contains(all, want) {
			t.Errorf("missing %q in:\n%s", want, all)
		}
	}
	if strings.Contains(all, "step 1") || res.Success {
		t.Errorf("only steps 2 and 3 must fail:\n%s", all)
	}
}

func TestScenario_ExpectHistory(t *testing.T) {
	se, paths := newWiatrolapSandbox(t)
	res := runScenario(t, se, paths, "2026-10-04T23:00:00+02:00", `[
		{"set": "CLU220000592.DOU4992", "value": 0},
		{"set": "satel.zone.2", "value": 1},
		{"advance": 20},
		{"set": "CLU220000592.DOU4992", "value": 0},
		{"reload": true},
		{"set": "CLU220000592.DOU4992", "value": 0},
		{"expect_history": {"path": "CLU220000592.DOU4992", "changes": [
			{"old": 0, "value": 1},
			{"old": 1, "value": 0}
		]}},
		{"expect_history": {"path": "satel.zone.2", "changes": [{"old": 0, "value": 1}]}}
	]`)
	requireSuccess(t, res)

	res = runScenario(t, se, paths, "2026-10-04T23:00:00+02:00", `[
		{"set": "satel.zone.2", "value": 1},
		{"expect_history": {"path": "satel.zone.2", "changes": [{"value": 0}]}}
	]`)
	if res.Success || !strings.Contains(strings.Join(res.Errors, " "), "step 2 (expect_history)") {
		t.Fatalf("mismatch not reported: %v", res.Errors)
	}
}

func TestScenario_LastChangeAndWatchInDraft(t *testing.T) {
	se, ws, _ := newTestSandbox(t)
	writeDraft(t, ws, "modules/probe.lua", `
		local s = VirtualSwitch:new("s")
		registerObject("virt.s", s)
		local c = { n = 0 }
		StateBus:watch("virt.s", function() c.n = c.n + 1 end, { to = 1 })
		EventBus:getShared():on("check", function(d)
			local since = s:since()
			if since ~= d.since or c.n ~= d.n then
				error("since=" .. tostring(since) .. " n=" .. c.n)
			end
		end)
	`)
	res, err := se.RunScenario([]string{"modules/probe.lua"}, SandboxScenario{
		Steps: mustSteps(t, `[
			{"set": "virt.s", "value": 1},
			{"advance": 90},
			{"emit": "check", "data": {"since": 90, "n": 1}},
			{"set": "virt.s", "value": 0},
			{"set": "virt.s", "value": 1},
			{"emit": "check", "data": {"since": 0, "n": 2}}
		]`),
	})
	if err != nil {
		t.Fatal(err)
	}
	requireSuccess(t, res)
}

func TestScenario_ErrorHooksAndPerStepCap(t *testing.T) {
	se, ws, _ := newTestSandbox(t)
	writeDraft(t, ws, "modules/probe.lua", `
		for i = 1, 150 do Logger:getShared():error("noise while loading %d", i) end
		EventBus:getShared():on("class", function() Logger:error("class-level %s", "boom") end)
		EventBus:getShared():on("helper", function() log.error("helper %d", 7) end)
	`)
	res, err := se.RunScenario([]string{"modules/probe.lua"}, SandboxScenario{
		Steps: mustSteps(t, `[{"emit": "class", "data": {}}, {"emit": "helper", "data": {}}]`),
	})
	if err != nil {
		t.Fatal(err)
	}
	all := strings.Join(res.Errors, "\n")
	for _, want := range []string{"step 1 (emit): runtime logged an error: class-level boom", "step 2 (emit): runtime logged an error: helper 7"} {
		if !strings.Contains(all, want) {
			t.Errorf("missing %q (load errors must not use up the cap):\n%s", want, all)
		}
	}
}

func TestGoToLua_KeepsHoles(t *testing.T) {
	L := lua.NewState()
	defer L.Close()
	L.SetGlobal("t", goToLua(L, []any{1.0, nil, 3.0}))
	if err := L.DoString(`assert(t[1] == 1 and t[2] == nil and t[3] == 3, "holes lost")`); err != nil {
		t.Fatal(err)
	}
}

// The wiatrołap logic with actors instead of a ±5 s window: the CLU
// switching the light off after our minute is our doing, an earlier
// switch-off is a person.
func TestScenario_TimedCommandAttribution(t *testing.T) {
	se, ws, serverDir := newTestSandbox(t)
	writeProxy(t, serverDir)
	writeDraft(t, ws, "modules/probe.lua", `
		local light = _:get("CLU220000592.DOU4992")
		local pir = VirtualSensor:new("pir")
		registerObject("virt.pir", pir)
		StateBus:watch("virt.pir", function() light:switchOn(60000) end,
			{ to = 1, runAs = "automation:wiatrolap" })
		EventBus:getShared():on("motion", function() pir:updateState(1) pir:updateState(0) end)
	`)
	res, err := se.RunScenario([]string{"modules/probe.lua"}, SandboxScenario{
		Steps: mustSteps(t, `[
			{"set": "CLU220000592.DOU4992", "value": 0},
			{"emit": "motion", "data": {}},
			{"advance": 61},
			{"set": "CLU220000592.DOU4992", "value": 0},
			{"emit": "motion", "data": {}},
			{"advance": 20},
			{"set": "CLU220000592.DOU4992", "value": 0},
			{"expect_history": {"path": "CLU220000592.DOU4992", "changes": [
				{"value": 1, "actor": "automation:wiatrolap"},
				{"value": 0, "actor": "automation:wiatrolap"},
				{"value": 1, "actor": "automation:wiatrolap"},
				{"value": 0, "origin": "grenton", "actor": "device:CLU220000592"}
			]}}
		]`),
	})
	if err != nil {
		t.Fatal(err)
	}
	requireSuccess(t, res)
}

// VirtualPresence runs on the sandbox's virtual clock: motion during the hold extends it,
// the light goes off hold s after the last motion.
func TestScenario_VirtualPresence(t *testing.T) {
	se, ws, _ := newTestSandbox(t)
	writeDraft(t, ws, "modules/presence.lua", `
		local pir = VirtualSensor:new("pir")
		registerObject("virt.pir", pir)
		local light = VirtualSwitch:new("light")
		registerObject("virt.light", light)
		local p = VirtualPresence:new("hall", { trigger = { "virt.pir" }, hold = 60 })
		registerObject("virt.hall", p)
		p:onSwitchOn(function() light:switchOn() end)
		p:onSwitchOff(function() light:switchOff() end)
		EventBus:getShared():on("motion", function() pir:updateState(1) pir:updateState(0) end)
	`)
	res, err := se.RunScenario([]string{"modules/presence.lua"}, SandboxScenario{
		Steps: mustSteps(t, `[
			{"emit": "motion", "data": {}},
			{"advance": 50},
			{"emit": "motion", "data": {}},
			{"advance": 50},
			{"expect_state": {"virt.light": 1, "virt.hall": 1}},
			{"advance": 11},
			{"expect_state": {"virt.light": 0, "virt.hall": 0}},
			{"expect_history": {"path": "virt.light", "changes": [{"value": 1}, {"value": 0}]}}
		]`),
	})
	if err != nil {
		t.Fatal(err)
	}
	requireSuccess(t, res)
}

func TestScenario_DraftLoadsAsModule(t *testing.T) {
	se, ws, _ := newTestSandbox(t)
	writeDraft(t, ws, "modules/energy.lua", `
		local v = VirtualSwitch:new("v")
		registerObject("virt.load", v)
		v:setValue(1)
		__go_setTimeout(function() v:setValue(0) end, 1000)
	`)
	res, err := se.RunScenario([]string{"modules/energy.lua"}, SandboxScenario{
		Steps: mustSteps(t, `[
			{"advance": 2},
			{"expect_history": {"path": "virt.load", "changes": [
				{"value": 1, "origin": "lua", "actor": "automation:module:energy"},
				{"value": 0, "origin": "lua", "actor": "automation:module:energy"}
			]}}
		]`),
	})
	if err != nil {
		t.Fatal(err)
	}
	requireSuccess(t, res)
}

// A person switches the light off during the automation's minute, on again
// and off before the minute ends: the last switch-off is the person's too.
func TestScenario_StaleExpectationIsDropped(t *testing.T) {
	se, ws, serverDir := newTestSandbox(t)
	writeProxy(t, serverDir)
	writeDraft(t, ws, "modules/probe.lua", `
		local light = _:get("CLU220000592.DOU4992")
		EventBus:getShared():on("auto", function()
			StateContext.with({ origin = "lua", actor = "automation:w" }, function() light:switchOn(60000) end)
		end)
	`)
	res, err := se.RunScenario([]string{"modules/probe.lua"}, SandboxScenario{
		Steps: mustSteps(t, `[
			{"set": "CLU220000592.DOU4992", "value": 0},
			{"emit": "auto", "data": {}},
			{"advance": 20},
			{"set": "CLU220000592.DOU4992", "value": 0},
			{"advance": 30},
			{"set": "CLU220000592.DOU4992", "value": 1},
			{"advance": 8},
			{"set": "CLU220000592.DOU4992", "value": 0},
			{"expect_history": {"path": "CLU220000592.DOU4992", "changes": [
				{"value": 1, "actor": "automation:w"},
				{"value": 0, "actor": "device:CLU220000592"},
				{"value": 1, "actor": "device:CLU220000592"},
				{"value": 0, "actor": "device:CLU220000592"}
			]}}
		]`),
	})
	if err != nil {
		t.Fatal(err)
	}
	requireSuccess(t, res)
}

// A Lua reload during the automation's minute: the first reading after it
// must not drop the expectation, the CLU's switch-off is still the automation's.
func TestScenario_ExpectationSurvivesReload(t *testing.T) {
	se, ws, serverDir := newTestSandbox(t)
	writeProxy(t, serverDir)
	writeDraft(t, ws, "modules/probe.lua", `
		local light = _:get("CLU220000592.DOU4992")
		EventBus:getShared():on("auto", function()
			StateContext.with({ origin = "lua", actor = "automation:w" }, function() light:switchOn(60000) end)
		end)
	`)
	res, err := se.RunScenario([]string{"modules/probe.lua"}, SandboxScenario{
		Steps: mustSteps(t, `[
			{"set": "CLU220000592.DOU4992", "value": 0},
			{"emit": "auto", "data": {}},
			{"advance": 30},
			{"reload": true},
			{"set": "CLU220000592.DOU4992", "value": 1},
			{"advance": 31},
			{"set": "CLU220000592.DOU4992", "value": 0},
			{"expect_history": {"path": "CLU220000592.DOU4992", "changes": [
				{"value": 1, "actor": "automation:w"},
				{"value": 0, "actor": "automation:w"}
			]}}
		]`),
	})
	if err != nil {
		t.Fatal(err)
	}
	requireSuccess(t, res)
}

// wiatrolapAs adds a helper draft switching the light as a given actor, the
// way vCLU entry points do through StateContext.
func wiatrolapAs(t *testing.T, se *SandboxEngine, paths []string) []string {
	t.Helper()
	writeDraft(t, se.ws, "modules/as.lua", `
		local light = _:get("CLU220000592.DOU4992")
		EventBus:getShared():on("as", function(d)
			StateContext.with({ origin = d.origin, actor = d.actor }, function()
				if d.value == 1 then light:switchOn() else light:switchOff() end
			end)
		end)
	`)
	return append(paths, "modules/as.lua")
}

func TestScenario_WiatrolapOffByOthersPauses(t *testing.T) {
	se, paths := newWiatrolapSandbox(t)
	paths = wiatrolapAs(t, se, paths)
	for _, who := range []string{
		`{"origin": "web", "actor": "user:tytus"}`,
		`{"origin": "mcp", "actor": "agent:bot"}`,
		`{"origin": "mqtt", "actor": "service:home-assistant"}`,
	} {
		data := strings.Replace(who, "{", `{"value": 0, `, 1)
		res := runScenario(t, se, paths, "2026-10-04T23:00:00+02:00", `[
			{"set": "CLU220000592.DOU4992", "value": 0},
			{"set": "satel.zone.2", "value": 1},
			{"set": "satel.zone.2", "value": 0},
			{"advance": 10},
			{"emit": "as", "data": `+data+`},
			{"set": "satel.zone.2", "value": 1},
			{"expect_clu_calls": [{"script": "DOU4992:execute(1, 60000)"}, {"script": "DOU4992:execute(2, 0)"}]}
		]`)
		requireSuccess(t, res)
	}
}

func TestScenario_WiatrolapReloads(t *testing.T) {
	se, paths := newWiatrolapSandbox(t)
	// reload during the minute: the CLU's switch-off is still ours, no pause
	res := runScenario(t, se, paths, "2026-10-04T23:00:00+02:00", `[
		{"set": "CLU220000592.DOU4992", "value": 0},
		{"set": "satel.zone.2", "value": 1},
		{"set": "satel.zone.2", "value": 0},
		{"advance": 30},
		{"reload": true},
		{"set": "CLU220000592.DOU4992", "value": 1},
		{"set": "satel.zone.2", "value": 0},
		{"advance": 31},
		{"set": "CLU220000592.DOU4992", "value": 0},
		{"set": "satel.zone.2", "value": 1},
		{"expect_clu_calls": [{"script": "DOU4992:execute(1, 60000)"}, {"script": "DOU4992:execute(1, 60000)"}]}
	]`)
	requireSuccess(t, res)

	// reload during a pause: the pause is kept in persist() and survives it
	res = runScenario(t, se, paths, "2026-10-04T23:00:00+02:00", `[
		{"set": "CLU220000592.DOU4992", "value": 0},
		{"set": "satel.zone.2", "value": 1},
		{"set": "satel.zone.2", "value": 0},
		{"advance": 20},
		{"set": "CLU220000592.DOU4992", "value": 0},
		{"reload": true},
		{"set": "CLU220000592.DOU4992", "value": 0},
		{"set": "satel.zone.2", "value": 0},
		{"set": "satel.zone.2", "value": 1},
		{"expect_clu_calls": [{"script": "DOU4992:execute(1, 60000)"}]},
		{"set": "satel.zone.2", "value": 0},
		{"advance": 600},
		{"set": "satel.zone.2", "value": 1},
		{"expect_clu_calls": [{"script": "DOU4992:execute(1, 60000)"}]}
	]`)
	requireSuccess(t, res)
}

func TestScenario_PersistSurvivesReload(t *testing.T) {
	se, ws, _ := newTestSandbox(t)
	writeDraft(t, ws, "modules/probe.lua", `
		local st = persist("probe", { n = 0, loads = 0 })
		st.loads = st.loads + 1
		EventBus:getShared():on("inc", function() st.n = st.n + 1 end)
		EventBus:getShared():on("check", function(d)
			if st.n ~= d.n or st.loads ~= d.loads then
				error("n=" .. st.n .. " loads=" .. st.loads)
			end
		end)
	`)
	res, err := se.RunScenario([]string{"modules/probe.lua"}, SandboxScenario{
		Steps: mustSteps(t, `[
			{"emit": "inc", "data": {}},
			{"emit": "inc", "data": {}},
			{"reload": true},
			{"emit": "check", "data": {"n": 2, "loads": 2}}
		]`),
	})
	if err != nil {
		t.Fatal(err)
	}
	requireSuccess(t, res)
}

// A set step is a change made by the agent: it fires a watch even on a CLU
// object that has no value yet (a first reading would not).
func TestScenario_FirstSetFiresWatch(t *testing.T) {
	se, ws, serverDir := newTestSandbox(t)
	writeProxy(t, serverDir)
	writeDraft(t, ws, "modules/probe.lua", `
		StateBus:watch("CLU220000592.DOU4992", function()
			_:get("CLU220000592.DOU4992"):switchOff()
		end, { to = 1 })
	`)
	res := runScenario(t, se, []string{"modules/probe.lua"}, "2026-10-04T23:00:00+02:00", `[
		{"set": "CLU220000592.DOU4992", "value": 1},
		{"expect_clu_calls": [{"script": "DOU4992:execute(2, 0)"}]}
	]`)
	requireSuccess(t, res)
}
