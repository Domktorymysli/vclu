package lua

import (
	"fmt"
	"testing"

	"github.com/Domktorymysli/vclu/pkg/history"
)

// A 1-Wire sensor flapping 22.1 <-> 22.2: with filter delta 0.2 nothing is
// reported (no event, no onChange), getValue() still has the raw value.
func TestStateFilter_RemoteThermometer(t *testing.T) {
	runStateChange(t, `
		local th = RemoteThermometer:new(_clu, "ONE1")
		_registry["CLU1.ONE1"] = th
		local calls = {}
		th:onChange(function(v, old) table.insert(calls, {v, old}) end)
		_:sync("CLU1.ONE1", 22.1)
		expose(th, "temperature", { path = "CLU1.ONE1", filter = { delta = 0.2 } })
		takeEvents()

		_:sync("CLU1.ONE1", 22.2)
		expectNone("+0.1")
		if th:getValue() ~= 22.2 then error("getValue must be raw: " .. tostring(th:getValue())) end
		_:sync("CLU1.ONE1", 22.1)
		expectNone("back")
		_:sync("CLU1.ONE1", 22.2)
		_:sync("CLU1.ONE1", 22.3)
		expectOne("+0.2 from the last report", 22.1, 22.3, "temperature")
		_:sync("CLU1.ONE1", 22.2)
		expectNone("-0.1")
		_:sync("CLU1.ONE1", 21.9)
		expectOne("-0.4", 22.3, 21.9)
		if #calls ~= 2 or calls[1][2] ~= 22.1 or calls[1][1] ~= 22.3 then
			error("onChange must see only reported changes, got " .. #calls)
		end
	`)
}

func TestStateFilter_Heartbeat(t *testing.T) {
	runStateChange(t, `
		local now = 1000000
		__go_time_ms = function() return now end
		local th = RemoteThermometer:new(_clu, "ONE2")
		_registry["CLU1.ONE2"] = th
		_:sync("CLU1.ONE2", 20.0)
		expose(th, "temperature", { path = "CLU1.ONE2", filter = { delta = 0.5, heartbeat = 600 } })
		takeEvents()
		_:sync("CLU1.ONE2", 20.1)
		expectNone("small change")
		now = now + 600 * 1000
		_:sync("CLU1.ONE2", 20.2)
		expectOne("small change after heartbeat", 20.0, 20.2)
		_:sync("CLU1.ONE2", 20.2)
		expectNone("same value")
	`)
}

func TestStateFilter_VirtualAndPlugin(t *testing.T) {
	runStateChange(t, `
		local n = VirtualNumber:new("hum")
		_registry["virt.hum"] = n
		n:updateState(50)
		expose(n, "humidity", { path = "virt.hum", filter = { delta = 2 } })
		takeEvents()
		n:updateState(51)
		expectNone("virtual +1")
		if n:getValue() ~= 51 then error("raw value lost") end
		n:updateState(52.5)
		expectOne("virtual +2.5", 50, 52.5)

		-- a plugin sensor with its own onChange (no updateState)
		local cbs, val = {}, 10
		local p = {
			getValue = function() return val end,
			onChange = function(self, cb) table.insert(cbs, cb) end,
		}
		expose(p, "humidity", { path = "plugin.p", filter = { delta = 1 } })
		takeEvents()
		local function report(v) local old = val; val = v; for _, cb in ipairs(cbs) do cb(v, old) end end
		report(10.4)
		expectNone("plugin +0.4")
		report(11.2)
		expectOne("plugin +1.2", 10, 11.2)
	`)
}

func TestStateFilter_Validation(t *testing.T) {
	runStateChange(t, `
		local function fails(label, fn, want)
			local ok, err = pcall(fn)
			if ok or not tostring(err):find(want, 1, true) then
				error(label .. ": " .. tostring(err))
			end
		end
		local th = RemoteThermometer:new(_clu, "ONE3")
		fails("no delta", function() expose(th, "temperature", { path = "a", filter = {} }) end, "delta")
		fails("typo", function() expose(th, "temperature", { path = "a", filter = { delta = 1, heartbeet = 5 } }) end, "heartbeet")
		fails("control", function()
			expose(VirtualNumber:new("sp"), "number", { path = "c", filter = { delta = 1 } })
		end, "read-only")
		if ExposedObjects._objects["a"] then error("a rejected filter must not expose the object") end
		fails("binary", function()
			expose(RemoteSwitch:new(_clu, "DOU9"), "switch", { path = "b", filter = { delta = 1 } })
		end, "read-only")
	`)
}

// Unexposing removes the filter: every change is reported again.
func TestStateFilter_Unexpose(t *testing.T) {
	runStateChange(t, `
		local th = RemoteThermometer:new(_clu, "ONE4")
		_registry["CLU1.ONE4"] = th
		_:sync("CLU1.ONE4", 22.0)
		local h = expose(th, "temperature", { path = "CLU1.ONE4", filter = { delta = 1 } })
		h:unexpose()
		takeEvents()
		_:sync("CLU1.ONE4", 22.1)
		expectOne("after unexpose", 22.0, 22.1)
	`)
}

// CLU subscription reports (how the house's 1-Wire sensors arrive).
func TestStateFilter_SubscriptionReport(t *testing.T) {
	runStateChange(t, `
		local th = RemoteThermometer:new(_clu, "ONE5")
		_registry["CLU9.ONE5"] = th
		RemoteSubscription.activeSubscriptions["192.168.0.9:2"] = {
			cluSerial = "CLU9", objects = { { objId = "ONE5" } },
		}
		RemoteSubscription.updateRegistry("192.168.0.9", 2, { 22.1 })
		expose(th, "temperature", { path = "CLU9.ONE5", filter = { delta = 0.2 } })
		takeEvents()
		for _, v in ipairs({ 22.2, 22.1, 22.2, 22.1 }) do
			RemoteSubscription.updateRegistry("192.168.0.9", 2, { v })
		end
		expectNone("flapping by 0.1")
		RemoteSubscription.updateRegistry("192.168.0.9", 2, { 22.4 })
		expectOne("+0.3", 22.1, 22.4)
	`)
}

// handle:update() and a second obj:expose(opts) change the filter that runs,
// not only the stored options; filter = false removes it.
func TestStateFilter_UpdateAppliesFilter(t *testing.T) {
	runStateChange(t, `
		local th = RemoteThermometer:new(_clu, "ONE6")
		_registry["CLU1.ONE6"] = th
		_:sync("CLU1.ONE6", 20.0)
		local h = expose(th, "temperature", { path = "CLU1.ONE6" })
		takeEvents()

		h:update({ filter = { delta = 1 } })
		_:sync("CLU1.ONE6", 20.5)
		expectNone("filter added by update")

		th:expose({ filter = { delta = 5 } })
		_:sync("CLU1.ONE6", 22.0)
		expectNone("filter widened by obj:expose")

		h:update({ filter = false })
		_:sync("CLU1.ONE6", 22.1)
		expectOne("filter removed", 22.0, 22.1)

		local ok, err = pcall(function() h:update({ filter = { delta = -1 } }) end)
		if ok or not tostring(err):find("delta", 1, true) then error("bad update accepted: " .. tostring(err)) end
		if h.options.filter ~= false then error("rejected update changed the options") end
	`)
}

// scene and button have neither binary nor write, but are not numeric sensors.
func TestStateFilter_RejectsNonNumericTypes(t *testing.T) {
	runStateChange(t, `
		for _, typ in ipairs({ "scene", "button", "motion", "sensor", "number", "cover" }) do
			local obj = { execute = function() end, getValue = function() return 0 end }
			local ok, err = pcall(function()
				expose(obj, typ, { path = "x." .. typ, filter = { delta = 1 } })
			end)
			if ok or not tostring(err):find("read-only numeric", 1, true) then
				error(typ .. ": " .. tostring(err))
			end
		end
		expose({ getValue = function() return 1 end }, "illuminance", { path = "x.lux", filter = { delta = 10 } })
	`)
}

// After a restart the first reading of a filtered sensor goes to the bus
// (HA needs a state) but a difference under delta from the last value in
// history is not recorded as a change.
func TestStateFilter_FirstReadingAfterRestart(t *testing.T) {
	engine := NewLuaEngine()
	defer engine.Close()
	store := history.New(history.DefaultConfig(), nil)
	engine.SetHistoryStore(store)
	if _, err := engine.Execute(stateChangeHarness); err != nil {
		t.Fatal(err)
	}
	store.Record(history.Entry{Path: "CLU1.ONE7", Old: 22.0, Value: 21.9, Origin: "grenton"})
	store.BeginReload() // what a restart looks like to the store
	res, err := engine.Execute(`local ok, err = pcall(function()
		local th = RemoteThermometer:new(_clu, "ONE7")
		_registry["CLU1.ONE7"] = th
		expose(th, "temperature", { path = "CLU1.ONE7", filter = { delta = 0.2 } })
		takeEvents()
		_:sync("CLU1.ONE7", 21.8)
		expectOne("first reading reaches the bus", nil, 21.8)
		_:sync("CLU1.ONE7", 22.0)
		expectNone("0.1 from the value in history")
		_:sync("CLU1.ONE7", 22.1)
		expectOne("measured from history", 21.9, 22.1)
	end)
	return ok and "ok" or tostring(err)`)
	if err != nil || res != "ok" {
		t.Fatal(res, err)
	}
	got := store.Query(history.Query{Path: "CLU1.ONE7"})
	if len(got) != 2 || got[0].Old != 21.9 || got[0].Value != 22.1 {
		t.Fatalf("history: %+v", got)
	}
}

// history = false: the change still reaches the bus (HA, watchers), but not
// the history store; handle:update() turns it back on.
func TestExposeHistoryOff(t *testing.T) {
	engine := NewLuaEngine()
	defer engine.Close()
	store := history.New(history.DefaultConfig(), nil)
	engine.SetHistoryStore(store)
	if _, err := engine.Execute(stateChangeHarness); err != nil {
		t.Fatal(err)
	}
	res, err := engine.Execute(`local ok, err = pcall(function()
		local n = VirtualNumber:new("cnt", { min = 0, max = 1000 })
		local h = expose(n, "number", { path = "test:cnt", readonly = true, history = false })
		takeEvents()
		n:setValue(1)
		expectOne("event without history", 0, 1)
		if n:lastChange() ~= nil then error("lastChange must be empty") end
		h:update({ history = true })
		n:setValue(2)
		expectOne("event with history", 1, 2)
		local ok2, e2 = pcall(function() h:update({ history = "no" }) end)
		if ok2 or not tostring(e2):find("history must be", 1, true) then error("bad value accepted: " .. tostring(e2)) end
	end)
	return ok and "ok" or tostring(err)`)
	if err != nil || res != "ok" {
		t.Fatal(res, err)
	}
	if got := store.Query(history.Query{Path: "test:cnt"}); len(got) != 1 || fmt.Sprint(got[0].Value) != "2" {
		t.Fatalf("history: %+v", got)
	}
}
