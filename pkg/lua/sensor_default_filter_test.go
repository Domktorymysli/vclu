package lua

import (
	"testing"
	"time"

	"github.com/Domktorymysli/vclu/pkg/history"
)

// Sensors that exposeRegistry() exposes get the default filter of their type (#192); a sensor
// exposed by hand keeps what the module wrote.

const sensorFilterSetup = `
	_pub = {}
	HAStatePublisher.setup({
		publish = function(self, topic, payload) table.insert(_pub, { topic = topic, payload = payload }); return true end,
		subscribe = function() end, unsubscribe = function() end,
	})
	_clu = { address = "192.168.0.5", executeNoReply = function() end }
	function sensor(id, grentonType)
		local s = RemoteValueSensor:new(_clu, id, id, grentonType)
		registerObject("CLU1." .. id, s)
		return s
	end
	function statePublishes(id)
		local n = 0
		for _, p in ipairs(_pub) do
			if p.topic == "vclu/exposed/vclu/sensor/clu1_" .. string.lower(id) .. "/state" then n = n + 1 end
		end
		return n
	end
	function filterOf(path)
		local f = ExposedObjects:get(path).options.filter
		if f == nil then return "nil" end
		if f == false then return "false" end
		return tostring(f.delta) .. "/" .. tostring(f.heartbeat)
	end
`

func newSensorFilterEngine(t *testing.T) (*LuaEngine, *history.Store, *stepClock) {
	t.Helper()
	engine, store, clk := newHistoryEngine(t)
	luaOK(t, engine, sensorFilterSetup)
	return engine, store, clk
}

func historyCount(store *history.Store, path string) int {
	return len(store.Query(history.Query{Path: path}))
}

func TestSensorDefaultFilter_ByType(t *testing.T) {
	engine, _, _ := newSensorFilterEngine(t)
	got := mustExec(t, engine, `(function()
		sensor("PAN1", "PANELSENSTEMP")
		sensor("PAN2", "PANELSENSLIGHT")
		sensor("HUM1", "HUMIDITY_SENSOR")
		registerObject("CLU1.DOU1", RemoteSwitch:new(_clu, "DOU1"))
		registerObject("CLU1.DIN1", RemoteSensor:new(_clu, "DIN1"))
		exposeRegistry()
		local out = {}
		for _, p in ipairs({ "CLU1.PAN1", "CLU1.PAN2", "CLU1.HUM1", "CLU1.DOU1", "CLU1.DIN1" }) do
			out[#out + 1] = filterOf(p)
		end
		return table.concat(out, ",")
	end)()`)
	if got != "0.2/600,5/600,1/600,nil,nil" {
		t.Errorf("filters of PANELSENSTEMP, PANELSENSLIGHT, HUMIDITY_SENSOR, DOUT, DIN = %s", got)
	}
}

// 25.5, 25.6, 25.5, 25.6 is one change in history and on MQTT; getValue() stays raw.
func TestSensorDefaultFilter_HistoryAndMQTT(t *testing.T) {
	engine, store, clk := newSensorFilterEngine(t)
	luaOK(t, engine, `
		S = sensor("PAN1", "PANELSENSTEMP")
		exposeRegistry()
		_:sync("CLU1.PAN1", 25.0)
		_pub = {}
	`)
	before := historyCount(store, "CLU1.PAN1")
	luaOK(t, engine, `
		for i, v in ipairs({ 25.5, 25.6, 25.5, 25.6 }) do _:sync("CLU1.PAN1", v) end
		assert(S:getValue() == 25.6, "getValue must be raw: " .. tostring(S:getValue()))
		assert(statePublishes("PAN1") == 1, "MQTT state publishes: " .. statePublishes("PAN1"))
	`)
	if n := historyCount(store, "CLU1.PAN1") - before; n != 1 {
		t.Fatalf("flapping 25.5/25.6 gave %d history entries, want 1", n)
	}

	luaOK(t, engine, `
		_:sync("CLU1.PAN1", 25.8)
		assert(statePublishes("PAN1") == 2, "25.5 then 25.8: " .. statePublishes("PAN1"))
		_:sync("CLU1.PAN1", 25.9)
		assert(statePublishes("PAN1") == 2, "0.1 within the heartbeat was published")
	`)
	clk.t = clk.t.Add(600 * time.Second)
	luaOK(t, engine, `
		_:sync("CLU1.PAN1", 25.7)
		assert(statePublishes("PAN1") == 3, "0.1 after the heartbeat: " .. statePublishes("PAN1"))
	`)
	got := store.Query(history.Query{Path: "CLU1.PAN1"})
	if n := len(got) - before; n != 3 || got[0].Old != 25.8 || got[0].Value != 25.7 {
		t.Fatalf("history after 25.8 and the heartbeat: %+v", got)
	}
}

// A sensor exposed by hand: no filter unless the module writes one; filter = false is none.
func TestSensorDefaultFilter_HandExposed(t *testing.T) {
	engine, store, _ := newSensorFilterEngine(t)
	luaOK(t, engine, `
		sensor("PAN1", "PANELSENSTEMP"):expose({ name = "Salon" })
		expose(sensor("PAN2", "PANELSENSTEMP"), "temperature", { filter = false })
		exposeRegistry()
		assert(filterOf("CLU1.PAN1") == "nil", "hand-exposed got " .. filterOf("CLU1.PAN1"))
		assert(filterOf("CLU1.PAN2") == "false", "filter = false got " .. filterOf("CLU1.PAN2"))
		for i, v in ipairs({ 25.0, 25.1, 25.0, 25.1 }) do
			_:sync("CLU1.PAN1", v)
			_:sync("CLU1.PAN2", v)
		end
		assert(statePublishes("PAN1") == 4 and statePublishes("PAN2") == 4,
			"publishes " .. statePublishes("PAN1") .. "/" .. statePublishes("PAN2"))
	`)
	for _, p := range []string{"CLU1.PAN1", "CLU1.PAN2"} {
		if n := historyCount(store, p); n != 3 {
			t.Errorf("%s: %d history entries, want 3 (first reading is the baseline)", p, n)
		}
	}
}

// handle:update({ filter = false }) on an imported sensor reports every change again.
func TestSensorDefaultFilter_UpdateAfterStart(t *testing.T) {
	engine, _, _ := newSensorFilterEngine(t)
	luaOK(t, engine, `
		sensor("PAN1", "PANELSENSTEMP")
		exposeRegistry()
		_:sync("CLU1.PAN1", 25.0)
		_:sync("CLU1.PAN1", 25.1)
		assert(statePublishes("PAN1") == 1, "default filter: " .. statePublishes("PAN1"))
		-- an update republishes the state: count from after it
		assert(ExposedObjects:get("CLU1.PAN1"):update({ filter = false }))
		_pub = {}
		_:sync("CLU1.PAN1", 25.0)
		_:sync("CLU1.PAN1", 25.1)
		assert(statePublishes("PAN1") == 2, "after filter = false: " .. statePublishes("PAN1"))
		ExposedObjects:get("CLU1.PAN1"):update({ filter = { delta = 1 } })
		_pub = {}
		_:sync("CLU1.PAN1", 25.6)
		assert(statePublishes("PAN1") == 0, "after delta = 1: " .. statePublishes("PAN1"))
	`)
}

// A module sets an imported sensor's options before exposeRegistry() with ExposedObjects:defaults().
func TestSensorDefaultFilter_ModuleDefaults(t *testing.T) {
	engine, _, _ := newSensorFilterEngine(t)
	luaOK(t, engine, `
		sensor("PAN1", "PANELSENSTEMP")
		sensor("PAN2", "PANELSENSLIGHT")
		registerObject("CLU1.DOU1", RemoteSwitch:new(_clu, "DOU1"))
		ExposedObjects:defaults("CLU1.PAN1", { filter = false, area = "Salon" })
		ExposedObjects:defaults("CLU1.PAN2", { filter = { delta = 20, heartbeat = 900 } })
		local function fails(label, fn, want)
			local ok, err = pcall(fn)
			assert(not ok and tostring(err):find(want, 1, true), label .. ": " .. tostring(err))
		end
		fails("unknown path", function() ExposedObjects:defaults("CLU1.NOPE", { filter = false }) end, "no object")
		fails("filter on a switch", function() ExposedObjects:defaults("CLU1.DOU1", { filter = { delta = 1 } }) end, "read-only")
		fails("path", function() ExposedObjects:defaults("CLU1.PAN1", { path = "x" }) end, "path")
		fails("hidden", function() ExposedObjects:defaults("CLU1.PAN1", { hidden = true }) end, "hidden")
		registerObject("CLU1.THE1", RemoteThermostat:new(_clu, "THE1"))
		fails("climate", function() ExposedObjects:defaults("CLU1.THE1", { name = "x" }) end, "does not expose")
		registerObject("CLU1.X1", RemoteObject:new(_clu, "X1"))
		CLU1_X1 = _:get("CLU1.X1")
		CLU1_X1._getExposeType = function() return nil end
		fails("no expose type", function() ExposedObjects:defaults("CLU1.X1", { name = "x" }) end, "does not expose")

		exposeRegistry()
		assert(filterOf("CLU1.PAN1") == "false", "PAN1: " .. filterOf("CLU1.PAN1"))
		assert(ExposedObjects:get("CLU1.PAN1").options.area == "Salon", "area lost")
		assert(filterOf("CLU1.PAN2") == "20/900", "PAN2: " .. filterOf("CLU1.PAN2"))
		_:sync("CLU1.PAN1", 25.0)
		_:sync("CLU1.PAN1", 25.1)
		assert(statePublishes("PAN1") == 2, "PAN1 without a filter: " .. statePublishes("PAN1"))

		-- after start it goes to the handle
		ExposedObjects:defaults("CLU1.PAN1", { filter = { delta = 1 } })
		assert(filterOf("CLU1.PAN1") == "1/nil", "PAN1 after start: " .. filterOf("CLU1.PAN1"))
		_pub = {}
		_:sync("CLU1.PAN1", 25.5)
		assert(statePublishes("PAN1") == 0, "PAN1 filtered after start: " .. statePublishes("PAN1"))

		-- exposed again by an access change: the module's options stay
		local h = ExposedObjects:get("CLU1.PAN1")
		h._exposed = false
		ExposedObjects:_remove("CLU1.PAN1")
		ExposedObjects:exposeRegistry(nil, true)
		assert(filterOf("CLU1.PAN1") == "1/nil", "PAN1 exposed again: " .. filterOf("CLU1.PAN1"))
		assert(ExposedObjects:get("CLU1.PAN1").options.area == "Salon", "area lost when exposed again")
	`)
}

// A filtered source still counts as reporting for a VirtualThermostat: flapping 21.0/21.1 every
// 500 s is never "no reading for staleAfter", and the thermostat sees the raw reading.
func TestSensorDefaultFilter_ThermostatSourceNotStale(t *testing.T) {
	te := newVTEngine(t)
	mustExec(t, te.LuaEngine, `
		SRC = RemoteValueSensor:new({ address = "192.168.0.5", executeNoReply = function() end }, "PAN1", "Salon", "PANELSENSTEMP")
		registerObject("CLU1.PAN1", SRC)
		exposeRegistry()
		_:sync("CLU1.PAN1", 21.1)
		TH = VirtualThermostat:new("salon", { source = SRC, output = HEATER, setpoint = 21 })
		logs()`)
	// ends on 21.0, a change the filter swallows
	for i := 0; i < 11; i++ {
		te.advance(t, 500*time.Second)
		v := "21.0"
		if i%2 == 1 {
			v = "21.1"
		}
		mustExec(t, te.LuaEngine, `_:sync("CLU1.PAN1", `+v+`)`)
	}
	te.advance(t, 10*time.Second)
	got := mustExec(t, te.LuaEngine, `return tostring(TH:getFault()) .. "/" .. tostring(TH:getValue()) .. "|" .. logs()`)
	if got != "nil/21|" {
		t.Errorf("fault/temperature|logs = %q, want nil/21 and no warning", got)
	}
}
