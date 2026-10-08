package lua

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Domktorymysli/vclu/pkg/history"
	lua "github.com/yuin/gopher-lua"
)

func TestStateContext_AdapterSources(t *testing.T) {
	runStateChange(t, `
		local sw = RemoteSwitch:new(_clu, "DOU1")
		registerObject("CLU220000592.DOU1", sw)
		_:sync("CLU220000592.DOU1", 0)
		local e = takeEvents()[1]
		assert(e.origin == "grenton" and e.actor == "device:CLU220000592", "sync: " .. e.origin .. " " .. e.actor)

		local r = RemoteSwitch:new(_clu, "DOU2")
		registerObject("CLU9.DOU2", r)
		RemoteSubscription.activeSubscriptions["192.168.0.9:7"] = { cluSerial = "CLU9", objects = { { objId = "DOU2" } } }
		RemoteSubscription.updateRegistry("192.168.0.9", 7, { 1 })
		e = takeEvents()[1]
		assert(e.origin == "grenton" and e.actor == "device:CLU9", "report: " .. e.origin .. " " .. e.actor)

		local z = SatelZone:new(2)
		registerObject("satel.zone.2", z)
		Satel._objects.zone[2] = z
		Satel._dispatch({ type = "zone", number = 2, zone = { violated = true } })
		e = takeEvents()[1]
		assert(e.origin == "physical" and e.actor == "device:satel", "satel: " .. e.origin .. " " .. e.actor)

		local v = VirtualSwitch:new("v")
		registerObject("virt.v", v)
		v:setValue(1)
		e = takeEvents()[1]
		assert(e.origin == "lua" and e.actor == "automation:unknown", "plain Lua: " .. e.origin .. " " .. e.actor)
	`)
}

// A handler reacting to a CLU report acts on its own: the light it switches
// must not look like a change reported by the CLU.
func TestStateContext_HandlersDoNotInheritAdapterSource(t *testing.T) {
	runStateChange(t, `
		local pir, light = SatelZone:new(2), RemoteSwitch:new(_clu, "DOU3")
		registerObject("satel.zone.2", pir)
		registerObject("CLU1.DOU3", light)
		Satel._objects.zone[2] = pir
		_:sync("CLU1.DOU3", 0)
		pir:onChange(function(v) if v == 1 then light:switchOn(60000) end end)
		takeEvents()
		Satel._dispatch({ type = "zone", number = 2, zone = { violated = true } })
		local byPath = {}
		for _, e in ipairs(takeEvents()) do byPath[e.path] = e end
		assert(byPath["satel.zone.2"].actor == "device:satel")
		local l = byPath["CLU1.DOU3"]
		assert(l and l.origin == "lua" and l.actor == "automation:unknown", "light: " .. tostring(l and l.actor))
	`)
}

func TestStateContext_WithAndTimers(t *testing.T) {
	engine := NewLuaEngine()
	defer engine.Close()
	luaOK(t, engine, `
		local v = VirtualSwitch:new("v")
		registerObject("virt.v", v)
		local c = {}
		StateBus:watch("virt.v", function(e) table.insert(c, e) end)

		StateContext.with({ origin = "mcp", actor = "agent:test", correlationId = "req-1" }, function()
			v:setValue(1)
			StateContext.with({ origin = "web", actor = "user:tytus" }, function() v:setValue(0) end)
			v:setValue(1)
		end)
		assert(c[1].actor == "agent:test" and c[1].correlationId == "req-1", "outer")
		assert(c[2].actor == "user:tytus", "nested")
		assert(c[3].actor == "agent:test", "stack restored after nested")
		assert(StateContext.current() == nil, "stack empty after with")

		local ok = pcall(StateContext.with, { origin = "mcp", actor = "agent:x" }, function()
			StateContext.push({ origin = "web", actor = "leak" })
			error("boom")
		end)
		assert(not ok and StateContext.current() == nil, "context left behind after an error")

	`)
}

// A timer callback runs in the context its timer was created in, and only
// for its own duration.
func TestStateContext_TimersCarryContext(t *testing.T) {
	L := lua.NewState()
	defer L.Close()
	if err := L.DoString(`
		_timers = {}
		function __go_setTimeout(fn, ms) table.insert(_timers, fn) return #_timers end
		function __go_setInterval(fn, ms) table.insert(_timers, fn) return #_timers end
	`); err != nil {
		t.Fatal(err)
	}
	if err := L.DoFile(filepath.Join(findRuntimePath(), "core", "StateContext.lua")); err != nil {
		t.Fatal(err)
	}
	if err := L.DoString(`
		local seen = {}
		local function probe() local c = StateContext.current() table.insert(seen, c and c.actor or "none") end
		StateContext.with({ origin = "mcp", actor = "agent:timer" }, function()
			__go_setTimeout(probe, 10)
			__go_setInterval(probe, 10)
		end)
		__go_setTimeout(probe, 10)
		for _, fn in ipairs(_timers) do fn() end
		assert(table.concat(seen, ",") == "agent:timer,agent:timer,none", table.concat(seen, ","))
		assert(StateContext.current() == nil, "timer left its context behind")
	`); err != nil {
		t.Fatal(err)
	}
}

func TestStateContext_WatchRunAs(t *testing.T) {
	runStateChange(t, `
		local pir, light = VirtualSensor:new("pir"), RemoteSwitch:new(_clu, "DOU4")
		registerObject("virt.pir", pir)
		registerObject("CLU1.DOU4", light)
		_:sync("CLU1.DOU4", 0)
		StateBus:watch("virt.pir", function() light:switchOn(60000) end, { to = 1, runAs = "automation:wiatrolap" })
		takeEvents()
		StateContext.with({ origin = "web", actor = "user:tytus", correlationId = "c-1" }, function()
			pir:updateState(1)
		end)
		local byPath = {}
		for _, e in ipairs(takeEvents()) do byPath[e.path] = e end
		local l = byPath["CLU1.DOU4"]
		assert(l, "light not switched")
		assert(l.actor == "automation:wiatrolap", "actor " .. l.actor)
		assert(l.origin == "web" and l.correlationId == "c-1", "cause kept: " .. l.origin .. " " .. tostring(l.correlationId))
	`)
}

// Module code runs as system:vclu, also when a reload was requested by
// someone: its timers must not act as that requester later.
func TestStateContext_ModuleLoadingIsSystem(t *testing.T) {
	engine := NewLuaEngine()
	defer engine.Close()
	store := history.New(history.DefaultConfig(), nil)
	engine.SetHistoryStore(store)
	dir := t.TempDir()
	mod := filepath.Join(dir, "modules")
	if err := os.MkdirAll(mod, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(mod, "init.lua"), []byte(`
		local v = VirtualSwitch:new("boot")
		registerObject("virt.boot", v)
		v:setValue(1)
	`), 0644); err != nil {
		t.Fatal(err)
	}
	luaOK(t, engine, `StateContext.push({ origin = "mcp", actor = "agent:promoter" })`)
	engine.LoadUserModules(dir)
	last, ok := store.Last("virt.boot")
	if !ok || last.Origin != "lua" || last.Actor != "automation:module:init" {
		t.Fatalf("module load change: %+v", last)
	}
	luaOK(t, engine, `
		local c = StateContext.current()
		assert(c and c.actor == "agent:promoter", "loading must restore the stack it found")
		StateContext.pop()
	`)
}

func TestStateContext_HistoryRecordsSource(t *testing.T) {
	engine, store, _ := newHistoryEngine(t)
	luaOK(t, engine, `
		local sw = RemoteSwitch:new({ executeNoReply = function() end }, "DOU1")
		registerObject("CLU220000592.DOU1", sw)
		_:sync("CLU220000592.DOU1", 0)
		StateContext.with({ origin = "mcp", actor = "agent:bot", correlationId = "r-9" }, function() sw:switchOn() end)
	`)
	last, _ := store.Last("CLU220000592.DOU1")
	if last.Origin != "mcp" || last.Actor != "agent:bot" || last.CorrelationID != "r-9" {
		t.Fatalf("history entry: %+v", last)
	}
}

func TestStateContext_ExecuteAsAndCallAs(t *testing.T) {
	engine, store, _ := newHistoryEngine(t)
	luaOK(t, engine, `
		local v = VirtualSwitch:new("v")
		registerObject("virt.exec", v)
		function _test_set(x) v:setValue(x) return "ok" end
	`)
	if _, err := engine.ExecuteAs("web", "user:tytus", "corr-1", `_test_set(1)`); err != nil {
		t.Fatal(err)
	}
	last, _ := store.Last("virt.exec")
	if last.Origin != "web" || last.Actor != "user:tytus" || last.CorrelationID != "corr-1" {
		t.Fatalf("ExecuteAs: %+v", last)
	}
	if _, err := engine.CallAs("mcp", "agent:bot", "", "_test_set", 0); err != nil {
		t.Fatal(err)
	}
	last, _ = store.Last("virt.exec")
	if last.Origin != "mcp" || last.Actor != "agent:bot" || last.CorrelationID == "" {
		t.Fatalf("CallAs: %+v (a correlation id must be generated)", last)
	}
	// A failing script and a timed-out one leave nothing on the stack
	engine.ExecuteAs("web", "user:x", "", `StateContext.push({origin="lua", actor="leak"}) error("boom")`)
	engine.ExecuteAsWithTimeout("mcp", "agent:slow", "", `while true do end`, 50*time.Millisecond)
	luaOK(t, engine, `assert(StateContext.current() == nil, "context left behind")`)
	if _, err := engine.Execute(`_test_set(1)`); err != nil {
		t.Fatal(err)
	}
	last, _ = store.Last("virt.exec")
	if last.Actor != "automation:unknown" {
		t.Fatalf("a plain Execute after them must have no context: %+v", last)
	}
}

func TestStateContext_MQTTAndHACommands(t *testing.T) {
	runStateChange(t, `
		local v = VirtualSwitch:new("v")
		registerObject("virt.mqtt", v)
		MQTT._subscriptions = MQTT._subscriptions or {}
		MQTT._subscriptions["zigbee/door"] = { callback = function() v:setValue(1) end }
		takeEvents()
		MQTT:_handleMessage("zigbee/door", "open", 0, false)
		local e = takeEvents()[1]
		assert(e and e.origin == "mqtt" and e.actor == "service:mqtt" and e.correlationId, "mqtt message: " .. tostring(e and e.actor))
		MQTT._subscriptions["zigbee/door"] = nil

		local lamp = VirtualSwitch:new("lamp")
		expose(lamp, "switch", { path = "test:lamp" })
		takeEvents()
		HACommands.handleExposedMessage("vclu/exposed/test/switch/lamp/set", "ON")
		e = takeEvents()[1]
		assert(e and e.origin == "mqtt" and e.actor == "service:home-assistant", "HA command: " .. tostring(e and e.actor))
	`)
}

// While modules load, the loop keeps serving timers between work items: they
// must not see the loading context, and timers they create must not keep it.
func TestStateContext_LoadingDoesNotLeakIntoOtherWork(t *testing.T) {
	engine := NewLuaEngine()
	defer engine.Close()
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() { defer close(done); engine.RunLoop(stop) }()
	defer func() { close(stop); <-done }()
	for engineState(engine.engineState.Load()) != stateRunning {
		time.Sleep(time.Millisecond)
	}

	if _, err := engine.Execute(`
		_c = { ticks = 0, leaked = 0, later = 0, laterLeaked = 0 }
		__go_setInterval(function()
			_c.ticks = _c.ticks + 1
			if StateContext.current() then _c.leaked = _c.leaked + 1 end
			__go_setTimeout(function()
				_c.later = _c.later + 1
				if StateContext.current() then _c.laterLeaked = _c.laterLeaked + 1 end
			end, 30)
		end, 10)
	`); err != nil {
		t.Fatal(err)
	}

	dir := t.TempDir()
	mod := filepath.Join(dir, "modules")
	os.MkdirAll(mod, 0755)
	slow := "local t = os.clock() while os.clock() - t < 0.05 do end\n"
	os.WriteFile(filepath.Join(mod, "init.lua"), []byte(slow), 0644)
	os.WriteFile(filepath.Join(mod, "a.lua"), []byte(slow), 0644)
	os.WriteFile(filepath.Join(mod, "b.lua"), []byte(slow), 0644)
	engine.LoadUserModules(dir)

	// wait for the timers created during loading to fire (slow under load)
	var ticks, leaked, later, laterLeaked int
	var got string
	for deadline := time.Now().Add(3 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		var err error
		if got, err = engine.Execute(`string.format("%d %d %d %d", _c.ticks, _c.leaked, _c.later, _c.laterLeaked)`); err != nil {
			t.Fatal(err)
		}
		fmt.Sscanf(got, "%d %d %d %d", &ticks, &leaked, &later, &laterLeaked)
		if later >= 5 {
			break
		}
	}
	if later < 5 {
		t.Fatalf("too few ticks to tell: %s", got)
	}
	if leaked != 0 || laterLeaked != 0 {
		t.Fatalf("loading context leaked: ticks %d leaked %d, later %d leaked %d", ticks, leaked, later, laterLeaked)
	}
}
