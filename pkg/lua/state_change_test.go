package lua

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/Domktorymysli/vclu/pkg/history"
)

// C0 contract: one real value change = exactly one state_changed event with
// normalized fields; a change to the same value emits nothing.

const stateChangeHarness = `
	_ev = {}
	StateBus:getShared():on("state_changed", function(e) table.insert(_ev, e) end)
	_clu = { address = "192.168.0.5", executeNoReply = function() end }

	function takeEvents()
		local out = _ev
		_ev = {}
		return out
	end

	-- Fails unless exactly one event with the given old/value/type was emitted
	function expectOne(label, old, value, typ)
		local ev = takeEvents()
		if #ev ~= 1 then error(label .. ": expected 1 event, got " .. #ev) end
		local e = ev[1]
		if e.old ~= old or e.value ~= value then
			error(string.format("%s: got %s -> %s, want %s -> %s",
				label, tostring(e.old), tostring(e.value), tostring(old), tostring(value)))
		end
		if typ and e.type ~= typ then
			error(label .. ": type " .. tostring(e.type) .. ", want " .. typ)
		end
		return e
	end

	function expectNone(label)
		local ev = takeEvents()
		if #ev ~= 0 then error(label .. ": expected no events, got " .. #ev) end
	end
`

func runStateChange(t *testing.T, script string) {
	t.Helper()
	engine := NewLuaEngine()
	defer engine.Close()
	if _, err := engine.Execute(stateChangeHarness); err != nil {
		t.Fatalf("harness: %v", err)
	}
	// pcall: report the Lua error message as the test failure
	res, err := engine.Execute("local ok, err = pcall(function()\n" + script +
		"\nend)\nreturn ok and \"ok\" or tostring(err)")
	if err != nil {
		t.Fatal(err)
	}
	if res != "ok" {
		t.Fatal(res)
	}
}

func TestStateChange_SyncExposedRemote(t *testing.T) {
	runStateChange(t, `
		local sw = RemoteSwitch:new(_clu, "DOU1")
		_registry["CLU1.DOU1"] = sw
		expose(sw, "switch", { path = "CLU1.DOU1" })
		takeEvents()

		_:sync("CLU1.DOU1", 0)
		expectOne("nil->0", nil, 0, "switch")
		_:sync("CLU1.DOU1", 1)
		expectOne("0->1", 0, 1, "switch")
		_:sync("CLU1.DOU1", 1)
		expectNone("same value")
	`)
}

func TestStateChange_SyncNotExposedRemote(t *testing.T) {
	runStateChange(t, `
		_registry["CLU1.DOU2"] = RemoteSwitch:new(_clu, "DOU2")
		_:sync("CLU1.DOU2", "1")
		expectOne("nil->1", nil, 1, "DOUT")
		_:sync("CLU1.DOU2", 0)
		expectOne("1->0", 1, 0, "DOUT")
		_:sync("CLU1.DOU2", 0)
		expectNone("same value")
	`)
}

func TestStateChange_SyncVirtual(t *testing.T) {
	runStateChange(t, `
		local v = VirtualSwitch:new("v")
		_registry["virt.sw"] = v
		_:sync("virt.sw", 1)
		expectOne("plain 0->1", 0, 1, "DOUT")

		expose(v, "switch", { path = "virt.sw" })
		takeEvents()
		_:sync("virt.sw", 0)
		expectOne("exposed 1->0", 1, 0, "switch")
		_:sync("virt.sw", 0)
		expectNone("same value")
	`)
}

func TestStateChange_SetValue(t *testing.T) {
	runStateChange(t, `
		local v = VirtualSwitch:new("v")
		local hv = expose(v, "switch", { path = "virt.sv" })
		takeEvents()
		hv:setValue(1)
		expectOne("virtual 0->1", 0, 1, "switch")
		hv:setValue(1)
		expectNone("virtual same value")

		-- Remote without a known state: no OnChange, the adapter publishes
		local r = RemoteSwitch:new(_clu, "DOU3")
		_registry["CLU1.DOU3"] = r
		local hr = expose(r, "switch", { path = "CLU1.DOU3" })
		takeEvents()
		hr:setValue(1)
		expectOne("remote nil->1", nil, 1, "switch")
		hr:setValue(0)
		expectOne("remote 1->0", 1, 0, "switch")

		-- Write-only control (no getter) still reports the requested value
		local ctl = { set = function() end }
		local hc = expose(ctl, "number", { path = "plugin:ctl" })
		takeEvents()
		hc:setValue(3)
		expectOne("stateless", nil, 3, "number")
	`)
}

func TestStateChange_SubscriptionReport(t *testing.T) {
	runStateChange(t, `
		local a = RemoteSwitch:new(_clu, "DOU10")
		local b = RemoteSwitch:new(_clu, "DOU11")
		_registry["CLU9.DOU10"] = a
		_registry["CLU9.DOU11"] = b
		expose(a, "switch", { path = "CLU9.DOU10" })
		takeEvents()
		RemoteSubscription.activeSubscriptions["192.168.0.9:1"] = {
			cluSerial = "CLU9",
			objects = { { objId = "DOU10" }, { objId = "DOU11" } },
		}

		RemoteSubscription.updateRegistry("192.168.0.9", 1, { 0, 0 })
		local ev = takeEvents()
		if #ev ~= 2 then error("initial report: expected 2 events, got " .. #ev) end

		RemoteSubscription.updateRegistry("192.168.0.9", 1, { 1, 0 })
		expectOne("exposed 0->1", 0, 1, "switch")
		RemoteSubscription.updateRegistry("192.168.0.9", 1, { 1, 1 })
		expectOne("plain 0->1", 0, 1, "DOUT")
		RemoteSubscription.updateRegistry("192.168.0.9", 1, { 1, 1 })
		expectNone("same values")
	`)
}

func TestStateChange_GPIO(t *testing.T) {
	runStateChange(t, `
		local queue = { { 5, 0, 1 } }
		__go_gpio_poll = function()
			local c = table.remove(queue, 1)
			if not c then return nil end
			return c[1], c[2], c[3]
		end
		gpio.pollChanges()
		local e = expectOne("pin", 0, 1, "GPIO_INPUT")
		if e.path ~= "gpio.pin.5" then error("path " .. tostring(e.path)) end
	`)
}

func TestStateChange_EventFields(t *testing.T) {
	runStateChange(t, `
		_registry["CLU1.DOU4"] = RemoteSwitch:new(_clu, "DOU4")
		_:sync("CLU1.DOU4", 0)
		_:sync("CLU1.DOU4", 1)
		local ev = takeEvents()
		if #ev ~= 2 then error("expected 2 events, got " .. #ev) end
		for _, e in ipairs(ev) do
			if type(e.id) ~= "string" or e.id == "" then error("missing id") end
			if type(e.atMs) ~= "number" or e.atMs < 1e12 then error("bad atMs " .. tostring(e.atMs)) end
			if e.origin ~= "grenton" or e.actor ~= "device:CLU1" then error("bad origin/actor " .. e.origin .. " " .. e.actor) end
			if e.correlationId ~= nil then error("correlationId should be nil") end
			if e.path ~= "CLU1.DOU4" or e.obj ~= _registry["CLU1.DOU4"] then error("bad path/obj") end
		end
		if ev[1].id == ev[2].id then error("ids not unique") end
	`)
}

func TestStateChange_HomeKitConsumer(t *testing.T) {
	runStateChange(t, `
		_hk = {}
		__go_homekit_notify = function(path, value, typ) table.insert(_hk, { path, value, typ }) end
		local v = VirtualSwitch:new("v")
		local h = expose(v, "switch", { path = "virt.hk" })
		h:setValue(1)
		_:sync("virt.hk", 1)
		if #_hk ~= 1 then error("homekit notified " .. #_hk .. " times, want 1") end
		local n = _hk[1]
		if n[1] ~= "virt.hk" or n[2] ~= 1 or n[3] ~= "switch" then
			error("homekit got " .. tostring(n[1]) .. " " .. tostring(n[2]) .. " " .. tostring(n[3]))
		end
	`)
}

func TestStateChange_HistoryHasOld(t *testing.T) {
	engine := NewLuaEngine()
	defer engine.Close()
	engine.SetHistoryStore(history.New(history.DefaultConfig(), nil))
	raw, err := engine.Execute(`
		local v = VirtualSwitch:new("v")
		_registry["virt.hist"] = v
		expose(v, "switch", { path = "virt.hist" })
		_:sync("virt.hist", 1)
		_:sync("virt.hist", 1)
		_:sync("virt.hist", 0)
		return ExposedObjects.getHistory("virt.hist", 10)
	`)
	if err != nil {
		t.Fatal(err)
	}
	var hist []struct {
		ID    string  `json:"id"`
		Old   float64 `json:"old"`
		Value float64 `json:"value"`
		New   float64 `json:"new"`
		AtMs  int64   `json:"atMs"`
		Time  int64   `json:"time"`
	}
	if err := json.Unmarshal([]byte(raw), &hist); err != nil {
		t.Fatalf("history JSON: %v (%s)", err, raw)
	}
	if len(hist) != 2 {
		t.Fatalf("want 2 entries (no duplicates, no same-value), got %d: %s", len(hist), raw)
	}
	// Newest first: 1->0, then 0->1
	if hist[0].Old != 1 || hist[0].Value != 0 || hist[1].Old != 0 || hist[1].Value != 1 {
		t.Errorf("unexpected entries: %s", raw)
	}
	for _, h := range hist {
		if h.ID == "" || h.New != h.Value || h.Time != h.AtMs/1000 {
			t.Errorf("bad compat fields: %+v", h)
		}
	}
}

func TestStateChange_LocalModuleSetValue(t *testing.T) {
	runStateChange(t, `
		-- Local CLU modules keep state in obj.value and call onChange(value, old)
		local d = { value = 0, _cbs = {} }
		function d:onChange(cb) table.insert(self._cbs, cb) end
		function d:set(_, v)
			local old = self.value
			self.value = v
			if old ~= v then for _, cb in ipairs(self._cbs) do cb(v, old) end end
		end
		local h = expose(d, "switch", { path = "local.dout" })
		takeEvents()
		h:setValue(1)
		expectOne("0->1", 0, 1, "switch")
		h:setValue(1)
		expectNone("same value")
	`)
}

func TestStateChange_ExposedUnderOtherPath(t *testing.T) {
	runStateChange(t, `
		local sw = RemoteSwitch:new(_clu, "DOU9")
		_registry["CLU1.DOU9"] = sw
		local h = expose(sw, "switch", { path = "salon.lampa" })
		takeEvents()
		_:sync("CLU1.DOU9", 0)
		local e = expectOne("nil->0", nil, 0, "switch")
		if e.path ~= "salon.lampa" then error("path " .. e.path) end
		_:sync("CLU1.DOU9", 1)
		e = expectOne("0->1", 0, 1, "switch")
		if e.path ~= "salon.lampa" then error("path " .. e.path) end

		h:unexpose()
		takeEvents()
		_:sync("CLU1.DOU9", 0)
		e = expectOne("after unexpose", 1, 0, "DOUT")
		if e.path ~= "CLU1.DOU9" then error("path after unexpose " .. e.path) end
	`)
}

func TestStateChange_PluginOnChangeWithoutOld(t *testing.T) {
	runStateChange(t, `
		local ctl = { _cbs = {} }
		function ctl:onChange(cb) table.insert(self._cbs, cb) end
		function ctl:set() end
		function ctl:fire(v) for _, cb in ipairs(self._cbs) do cb(v) end end
		local h = expose(ctl, "number", { path = "plugin:fan" })
		takeEvents()
		ctl:fire(5)
		expectOne("first poll", nil, 5)
		ctl:fire(5)
		expectNone("same poll value")
		h:setValue(7)
		local e = expectOne("set", 5, 7)
		if e.oldValue ~= 5 then error("oldValue alias missing") end
		ctl:fire(7)
		expectNone("poll confirms set")
	`)
}

func TestStateChange_NestedChangeInListener(t *testing.T) {
	runStateChange(t, `
		local a = VirtualSwitch:new("a")
		local b = VirtualSwitch:new("b")
		_registry["virt.a"] = a
		_registry["virt.b"] = b
		expose(a, "switch", { path = "virt.a" })
		StateBus:getShared():on("state_changed", function(e)
			if e.path == "virt.a" and e.value == 1 then _:sync("virt.b", 1) end
		end)
		takeEvents()
		_:sync("virt.a", 1)
		local ev = takeEvents()
		if #ev ~= 2 then error("expected 2 events, got " .. #ev) end
		local seen = {}
		for _, e in ipairs(ev) do seen[e.path] = (seen[e.path] or 0) + 1 end
		if seen["virt.a"] ~= 1 or seen["virt.b"] ~= 1 then error("one event per object expected") end
	`)
}

func TestStateChange_SetValueWithGetter(t *testing.T) {
	runStateChange(t, `
		-- Setter clamps to 10 and ignores negatives; state only via get(0)
		local level = 0
		local ctl = {
			get = function() return level end,
			set = function(_, _, v) if v >= 0 then level = math.min(v, 10) end end,
		}
		local h = expose(ctl, "number", { path = "plugin:level" })
		takeEvents()
		h:setValue(20)
		expectOne("clamped", 0, 10, "number")
		h:setValue(-5)
		expectNone("rejected write")
		h:setValue(10)
		expectNone("unchanged value")

		-- Same through getValue()
		local dim = { v = 3 }
		function dim:getValue() return self.v end
		function dim:set(_, v) self.v = math.min(v, 5) end
		local hd = expose(dim, "number", { path = "plugin:dim" })
		takeEvents()
		hd:setValue(9)
		expectOne("getValue clamp", 3, 5)

		-- A helper updateState must not hide the getter
		local cu = { v = 0 }
		function cu:get() return self.v end
		function cu:set(_, v) self.v = v end
		function cu:updateState(v) self.v = v end
		local hu = expose(cu, "number", { path = "custom:updatestate" })
		takeEvents()
		hu:setValue(5)
		expectOne("getter with updateState", 0, 5)

		-- CLU objects never hit the blocking get(0), even without a known state
		local calls = 0
		local clu = { executeNoReply = function() end, execute = function() calls = calls + 1 end }
		local r = RemoteObject:new(clu, "DOU7")
		local hr = expose(r, "switch", { path = "CLU1.DOU7" })
		hr:setValue(1)
		if calls ~= 0 then error("get(0) sent to CLU " .. calls .. " times") end
	`)
}

func TestStateChange_IDsUniqueAcrossLuaStates(t *testing.T) {
	ids := map[string]bool{}
	for i := 0; i < 2; i++ {
		engine := NewLuaEngine()
		raw, err := engine.Execute(`
			local ids = {}
			StateBus:getShared():on("state_changed", function(e) table.insert(ids, e.id) end)
			_registry["CLU1.DOU1"] = RemoteSwitch:new({ executeNoReply = function() end }, "DOU1")
			_:sync("CLU1.DOU1", 0)
			_:sync("CLU1.DOU1", 1)
			return table.concat(ids, ",")
		`)
		engine.Close()
		if err != nil {
			t.Fatal(err)
		}
		for _, id := range strings.Split(raw, ",") {
			if id == "" || ids[id] || strings.HasPrefix(id, "lua") {
				t.Fatalf("duplicate, empty or non-Go id %q (all: %v)", id, ids)
			}
			ids[id] = true
		}
	}
	if len(ids) != 4 {
		t.Fatalf("want 4 ids, got %v", ids)
	}
}

func TestStateChange_PluginNotify(t *testing.T) {
	runStateChange(t, `
		-- Async setter: state changes only when the "HTTP reply" arrives
		local plugin = Plugin:new("@test/async", {})
		local state = { speed = 2 }
		local pending
		plugin:control("speed",
			function() return state.speed end,
			function(v) pending = v end)
		local ctl = plugin:get("speed")
		ctl:_notify()
		local h = expose(ctl, "fan", { path = "plugin:speed" })
		takeEvents()

		h:setValue(3)
		expectNone("before reply")
		state.speed = pending
		ctl:_notify()
		expectOne("after reply", 2, 3, "fan")
		ctl:_notify()
		expectNone("notify without change")
	`)
}

func TestStateChange_SetterEmitsOnChangeWithoutOld(t *testing.T) {
	runStateChange(t, `
		local ctl = { v = 0, _h = {} }
		function ctl:get() return self.v end
		function ctl:onChange(cb) table.insert(self._h, cb) end
		function ctl:set(_, v)
			self.v = v
			for _, cb in ipairs(self._h) do cb(v) end
		end
		local h = expose(ctl, "number", { path = "plugin:sync" })
		takeEvents()
		h:setValue(7)
		expectOne("set", 0, 7)
	`)
}

func TestStateChange_HAPublishesOncePerChange(t *testing.T) {
	runStateChange(t, `
		_pub = {}
		local mqtt = { publish = function(_, topic, payload) table.insert(_pub, { topic, payload }) end }
		HAStatePublisher.setup(mqtt, { autoExposeRegistry = false })

		local sw = RemoteSwitch:new(_clu, "DOU20")
		_registry["CLU1.DOU20"] = sw
		expose(sw, "switch", { path = "CLU1.DOU20" })
		RemoteSubscription.activeSubscriptions["192.168.0.9:2"] = {
			cluSerial = "CLU1", objects = { { objId = "DOU20" } },
		}
		_:sync("CLU1.DOU20", 0)

		local function states()
			local out = {}
			for _, p in ipairs(_pub) do
				if not p[1]:find("/config$") then table.insert(out, p[1] .. "=" .. tostring(p[2])) end
			end
			_pub = {}
			return out
		end
		states()

		_:sync("CLU1.DOU20", 1)
		local s = states()
		if #s ~= 1 then error("_:sync: " .. #s .. " state publishes: " .. table.concat(s, ", ")) end

		RemoteSubscription.updateRegistry("192.168.0.9", 2, { 0 })
		s = states()
		if #s ~= 1 then error("report: " .. #s .. " state publishes: " .. table.concat(s, ", ")) end

		RemoteSubscription.updateRegistry("192.168.0.9", 2, { 0 })
		_:sync("CLU1.DOU20", 0)
		s = states()
		if #s ~= 0 then error("same value: " .. #s .. " state publishes: " .. table.concat(s, ", ")) end
	`)
}

func TestStateChange_GetterError(t *testing.T) {
	runStateChange(t, `
		-- A failing getter must not crash setValue nor produce a made-up event
		local ctl = { set = function() end, get = function() error("device offline") end }
		local h = expose(ctl, "number", { path = "plugin:broken" })
		takeEvents()
		if not h:setValue(4) then error("setValue failed") end
		expectNone("setValue with failing getter")

		local plugin = Plugin:new("@test/broken", {})
		plugin:sensor("t", function() error("timeout") end)
		local s = plugin:get("t")
		expose(s, "temperature", { path = "plugin:t" })
		takeEvents()
		s:_notify()
		expectNone("_notify with failing getter")
	`)
}

// An optimistic update (switchOn from an automation) is a change too: it used
// to publish nothing for a non-exposed object, and the CLU echo with the same
// value did not either, so the change never reached history or HA.
func TestStateChange_OptimisticUpdate(t *testing.T) {
	runStateChange(t, `
		local plain = RemoteSwitch:new(_clu, "DOU30")
		registerObject("CLU1.DOU30", plain)
		_:sync("CLU1.DOU30", 0)
		takeEvents()
		plain:switchOn(60000)
		expectOne("plain switchOn", 0, 1, "DOUT")
		_:sync("CLU1.DOU30", 1)
		expectNone("echo with the same value")

		local shown = RemoteSwitch:new(_clu, "DOU31")
		registerObject("CLU1.DOU31", shown)
		expose(shown, "switch", { path = "CLU1.DOU31" })
		_:sync("CLU1.DOU31", 0)
		takeEvents()
		shown:switchOn()
		expectOne("exposed switchOn", 0, 1, "switch")

		local v = VirtualSwitch:new("v")
		registerObject("virt.opt", v)
		takeEvents()
		v:setValue(1)
		expectOne("virtual setValue", 0, 1, "DOUT")
	`)
}

// A plugin control whose setter applies the value at once: the poll after
// setValue calls _notify(), which must not publish the same change again.
func TestStateChange_PluginNotifyAfterSyncSet(t *testing.T) {
	runStateChange(t, `
		local plugin = Plugin:new("@test/sync", {})
		local state = { on = true }
		plugin:control("detection", function() return state.on end, function(v) state.on = v end)
		local ctl = plugin:get("detection")
		ctl:_notify()
		local h = expose(ctl, "switch", { path = "plugin:detection" })
		takeEvents()
		h:setValue(false)
		expectOne("set", true, false)
		ctl:_notify()
		expectNone("poll after a synchronous set")
	`)
}
