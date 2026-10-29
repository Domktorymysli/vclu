package lua

import (
	"strings"
	"testing"
	"time"

	"github.com/Domktorymysli/vclu/pkg/plugins"
)

// VirtualThermostat (#63): switches any on/off output by any temperature source, on a
// virtual clock. The tick that checks staleness and maxOn is run by advance().

const vtHarness = `
	TICKS = { list = {} }
	-- the core timer primitive on the virtual clock, as the MCP sandbox replaces it
	__go_setInterval = function(fn, ms) TICKS.ms = ms; table.insert(TICKS.list, fn); return #TICKS.list end
	function tick() for i, fn in ipairs(TICKS.list) do fn() end end
	LOGS = {}
	Logger.warn = function(self, fmt, ...) LOGS[#LOGS + 1] = "warn " .. Logger:format(fmt, ...) end
	Logger.info = function(self, fmt, ...) LOGS[#LOGS + 1] = "info " .. Logger:format(fmt, ...) end
	function logs() local s = table.concat(LOGS, "\n"); LOGS = {}; return s end
	-- a sensor (Zigbee over MQTT) and a relay that count their switching
	TEMP = VirtualNumber:new("temp_salon")
	HEATER = VirtualSwitch:new("grzejnik", { persist = true })
	SW = { n = 0, seq = {} }
	HEATER:onChange(function(v) SW.n = SW.n + 1; table.insert(SW.seq, v) end)
	function seq() local s = table.concat(SW.seq, ""); SW.seq = {}; return s end
	function reads(...) for i, v in ipairs({...}) do TEMP:setValue(v) end end
`

type vtEngine struct {
	*LuaEngine
	clk *stepClock
}

func newVTEngine(t *testing.T) *vtEngine {
	t.Helper()
	engine := NewLuaEngine()
	t.Cleanup(engine.Close)
	engine.SetKVStoreManager(plugins.NewKVStoreManager(t.TempDir()))
	te := &vtEngine{engine, &stepClock{t: time.Date(2026, 10, 27, 18, 0, 0, 0, time.UTC)}}
	te.harness(t)
	return te
}

func (te *vtEngine) harness(t *testing.T) {
	t.Helper()
	SetClock(te.state, fixedClockFunc(te.clk.now))
	if _, err := te.Execute(vtHarness); err != nil {
		t.Fatalf("harness: %v", err)
	}
}

func (te *vtEngine) reload(t *testing.T) {
	t.Helper()
	if err := te.Reinitialize("CLU"); err != nil {
		t.Fatal(err)
	}
	te.harness(t)
}

// advance moves the clock in ticks, as the interval timer would.
func (te *vtEngine) advance(t *testing.T, d time.Duration) {
	t.Helper()
	step := 10 * time.Second
	for d > 0 {
		s := step
		if d < s {
			s = d
		}
		te.clk.t = te.clk.t.Add(s)
		d -= s
		mustExec(t, te.LuaEngine, `tick()`)
	}
}

// The output goes on at target - hysteresis and off at target + hysteresis, and only on
// such a crossing; cooling the other way round; a new set point moves the thresholds.
func TestVirtualThermostat_Hysteresis(t *testing.T) {
	te := newVTEngine(t)
	got := mustExec(t, te.LuaEngine, `(function()
		TH = VirtualThermostat:new("salon", { source = TEMP, output = HEATER, hysteresis = 0.5, setpoint = 21 })
		local out = {}
		reads(20.6); table.insert(out, seq())                      -- in the band: stays off
		reads(20.5); table.insert(out, seq())                      -- lower threshold: on
		reads(20.8, 21.2, 21.4, 20.6, 21.4); table.insert(out, seq()) -- between: no switching
		reads(21.5); table.insert(out, seq())                      -- upper threshold: off
		reads(21.0, 20.6, 21.6, 22.0); table.insert(out, seq())    -- no switching
		reads(20.4); table.insert(out, seq())                      -- on
		reads(21.9); table.insert(out, seq())                      -- off
		TH:setSetpoint(22.5); table.insert(out, seq())             -- 21.9 <= 22.0: on at once
		TH:setSetpoint(22.5); table.insert(out, seq())             -- the same: nothing
		TH:setSetpoint(21); table.insert(out, seq())               -- 21.9 >= 21.5: off
		return table.concat(out, ",") .. "|" .. TH:getValue() .. "/" .. TH:getTarget()
	end)()`)
	if got != ",1,,0,,1,0,1,,0|21.9/21" {
		t.Errorf("heating switches = %s, want ,1,,0,,1,0,1,,0|21.9/21", got)
	}

	cool := mustExec(t, te.LuaEngine, `(function()
		local temp, fan = VirtualNumber:new("t2"), VirtualSwitch:new("klima")
		local s = { seq = {} }
		fan:onChange(function(v) table.insert(s.seq, v) end)
		local th = VirtualThermostat:new("sypialnia", { source = temp, output = fan, direction = "cool", setpoint = 24, hysteresis = 1 })
		for i, v in ipairs({ 24.9, 25, 24.5, 23.1, 23, 23.5, 25.2 }) do temp:setValue(v) end
		return table.concat(s.seq, "") .. "|" .. th:getTarget()
	end)()`)
	if cool != "101|24" {
		t.Errorf("cooling switches = %s, want 101|24", cool)
	}
}

// No change from the source for staleAfter switches the output off and marks the thermostat
// stale, logged once; the next reading takes it back. No reading at all since creation is
// stale too.
func TestVirtualThermostat_StaleSource(t *testing.T) {
	te := newVTEngine(t)
	mustExec(t, te.LuaEngine, `
		TH = VirtualThermostat:new("salon", { source = TEMP, output = HEATER, setpoint = 21, staleAfter = 900 })
		reads(20)
		logs()`)
	if got := mustExec(t, te.LuaEngine, `return TICKS.ms .. "/" .. seq()`); got != "10000/1" {
		t.Fatalf("tick/heating = %s, want 10000/1", got)
	}
	te.advance(t, 890*time.Second)
	if got := mustExec(t, te.LuaEngine, `return HEATER:getValue() .. "/" .. tostring(TH:getFault()) .. "/" .. logs()`); got != "1/nil/" {
		t.Errorf("before staleAfter: output/fault/logs = %s, want 1/nil/", got)
	}
	te.advance(t, 10*time.Second)
	te.advance(t, time.Hour)
	got := mustExec(t, te.LuaEngine, `return HEATER:getValue() .. "/" .. tostring(TH:getFault()) .. "/" .. seq() .. "|" .. logs()`)
	if !strings.HasPrefix(got, "0/stale/0|warn ") || strings.Count(got, "warn ") != 1 || !strings.Contains(got, "no reading") {
		t.Errorf("stale: output/fault/switches|logs = %q, want 0/stale/0 and one warning", got)
	}
	// readings below the target come back: heating again, told once
	got = mustExec(t, te.LuaEngine, `(function()
		reads(19.9)
		return HEATER:getValue() .. "/" .. tostring(TH:getFault()) .. "|" .. logs()
	end)()`)
	if !strings.HasPrefix(got, "1/nil|info ") || strings.Count(got, "\n") != 0 {
		t.Errorf("after a reading: %q, want 1/nil and one info", got)
	}

	// never a reading: stale after staleAfter from creation
	never := newVTEngine(t)
	mustExec(t, never.LuaEngine, `TH = VirtualThermostat:new("salon", { source = TEMP, output = HEATER, staleAfter = 60 })`)
	never.advance(t, 60*time.Second)
	if got := mustExec(t, never.LuaEngine, `return tostring(TH:getFault()) .. "/" .. HEATER:getValue()`); got != "stale/0" {
		t.Errorf("no reading ever: fault/output = %s, want stale/0", got)
	}
}

// maxOn switches the output off after that long on in one go; readings do not end it,
// a person's change of a setting does.
func TestVirtualThermostat_MaxOn(t *testing.T) {
	te := newVTEngine(t)
	mustExec(t, te.LuaEngine, `
		TH = VirtualThermostat:new("salon", { source = TEMP, output = HEATER, setpoint = 21, maxOn = 3600, staleAfter = 7200 })
		reads(20)
		seq(); logs()`)
	te.advance(t, 3590*time.Second)
	if got := mustExec(t, te.LuaEngine, `return HEATER:getValue() .. tostring(TH:getFault())`); got != "1nil" {
		t.Fatalf("before maxOn: %s", got)
	}
	te.advance(t, 10*time.Second)
	got := mustExec(t, te.LuaEngine, `(function()
		local a = HEATER:getValue() .. "/" .. tostring(TH:getFault())
		reads(19.5, 19)
		local b = HEATER:getValue() .. "/" .. tostring(TH:getFault())
		local l = logs()
		TH:setSetpoint(21)
		return a .. "," .. b .. "," .. HEATER:getValue() .. "/" .. tostring(TH:getFault()) .. "|" .. l
	end)()`)
	if !strings.HasPrefix(got, "0/maxOn,0/maxOn,1/nil|warn ") || !strings.Contains(got, "maxOn") {
		t.Errorf("maxOn = %q, want 0/maxOn,0/maxOn,1/nil and a warning", got)
	}
	// a stale source during a maxOn stop does not end it with the next reading
	te.advance(t, 3600*time.Second) // on again for maxOn: off
	te.advance(t, 7200*time.Second)
	if got := mustExec(t, te.LuaEngine, `(function() reads(18) return tostring(TH:getFault()) .. HEATER:getValue() end)()`); got != "maxOn0" {
		t.Errorf("maxOn then a reading = %s, want maxOn0", got)
	}
}

// Off (stop), set point and holiday modes, min/max, the events lib/panel and Home Assistant
// listen to, and bad arguments.
func TestVirtualThermostat_ModesAndBounds(t *testing.T) {
	te := newVTEngine(t)
	got := mustExec(t, te.LuaEngine, `(function()
		TH = VirtualThermostat:new("salon", { source = TEMP, output = HEATER, setpoint = 40, holiday = 16, min = 10, max = 30 })
		local ev = { list = {} }
		for i, e in ipairs(TH.CHANGE_EVENTS) do
			TH:onEvent(e, function(field, v) table.insert(ev.list, field .. "=" .. tostring(v)) end)
		end
		local out = { TH:getSetpoint() .. "/" .. TH:getTarget() .. "/" .. TH:getState() .. "/" .. TH:getMode() }
		reads(20); table.insert(out, seq())                      -- 20 <= 29.5: on
		TH:stop(); table.insert(out, seq() .. TH:getState())     -- off at once
		reads(19, 18); table.insert(out, seq())                  -- stopped: nothing
		TH:start(); table.insert(out, seq() .. TH:getState())    -- on again
		TH:setMode(1); table.insert(out, seq() .. TH:getTarget()) -- holiday 16: off
		TH:setSetpoint(23); table.insert(out, seq() .. TH:getSetpoint() .. "/" .. TH:getTarget())
		TH:setMode(0); table.insert(out, seq() .. TH:getTarget()) -- 23: on
		TH:setSetpoint(5); TH:setSetpoint("abc"); TH:setSetpoint(0/0)
		table.insert(out, seq() .. TH:getSetpoint())             -- clamped to min, the rest ignored
		local ok, err = pcall(TH.setMode, TH, 2)
		table.insert(out, tostring(ok) .. ":" .. tostring(err and err:find("mode must be") ~= nil))
		return table.concat(out, ",") .. "|" .. table.concat(ev.list, " ")
	end)()`)
	want := "30/30/1/0,1,00,,11,016,23/16,123,010,false:true|" +
		"_state=0 _state=1 _mode=1 _target=16 _point=23 _mode=0 _target=23 _point=10 _target=10"
	if got != want {
		t.Errorf("modes\n got: %s\nwant: %s", got, want)
	}

	bad := mustExec(t, te.LuaEngine, `(function()
		local out = {}
		for i, o in ipairs({
			{ output = HEATER },
			{ source = TEMP },
			{ source = TEMP, output = TEMP },
			{ source = "nie.ma", output = HEATER },
			{ source = TEMP, output = HEATER, direction = "up" },
			{ source = TEMP, output = HEATER, hysteresis = -1 },
			{ source = TEMP, output = HEATER, min = 30, max = 20 },
			{ source = TEMP, output = HEATER, staleAfter = 0 },
			{ source = TEMP, output = HEATER, maxOn = "x" },
			{ source = TEMP, output = HEATER, colour = 1 },
		}) do
			local ok = pcall(VirtualThermostat.new, VirtualThermostat, "x", o)
			table.insert(out, tostring(ok))
		end
		return table.concat(out, ",")
	end)()`)
	if bad != strings.TrimSuffix(strings.Repeat("false,", 10), ",") {
		t.Errorf("bad options accepted: %s", bad)
	}
}

// With persist the set point, the mode and the state survive a restart; the output does
// not: a new thermostat (a reload, a restart) switches it off and waits for a reading
// that arrives after it was created.
func TestVirtualThermostat_PersistAndReload(t *testing.T) {
	te := newVTEngine(t)
	mustExec(t, te.LuaEngine, `
		TH = VirtualThermostat:new("salon", { source = TEMP, output = HEATER, setpoint = 21, persist = true })
		TH2 = VirtualThermostat:new("lazienka", { source = TEMP, output = VirtualSwitch:new("x"), persist = "th.lazienka" })
		reads(20)
		assert(HEATER:getValue() == 1, "heating")
		TH:setSetpoint(22.5)
		TH2:setMode(1)
		TH2:stop()`)
	te.reload(t)
	got := mustExec(t, te.LuaEngine, `(function()
		-- released before the reload; then as after a kill: the relay on, the sensor its old value
		local before = HEATER:getValue()
		HEATER:switchOn()
		TEMP:setValue(20)
		before = before .. HEATER:getValue()
		TH = VirtualThermostat:new("salon", { source = TEMP, output = HEATER, setpoint = 21, persist = true })
		TH2 = VirtualThermostat:new("lazienka", { source = TEMP, output = VirtualSwitch:new("x"), persist = "th.lazienka" })
		local plain = VirtualThermostat:new("plain", { source = TEMP, output = VirtualSwitch:new("y"), setpoint = 21 })
		local out = { before .. "/" .. HEATER:getValue() }
		tick(); table.insert(out, HEATER:getValue())    -- the old value is not a reading
		reads(20.1); table.insert(out, HEATER:getValue())
		table.insert(out, TH:getSetpoint() .. "/" .. TH:getMode() .. "/" .. TH:getState())
		table.insert(out, TH2:getSetpoint() .. "/" .. TH2:getMode() .. "/" .. TH2:getState())
		table.insert(out, plain:getSetpoint())
		return table.concat(out, ",")
	end)()`)
	if got != "01/0,0,1,22.5/0/1,21/1/0,21" {
		t.Errorf("after a reload: %s, want 01/0,0,1,22.5/0/1,21/1/0,21", got)
	}

	// a saved set point above a lowered max is clamped
	mustExec(t, te.LuaEngine, `TH:setSetpoint(30)`)
	te.reload(t)
	if got := mustExec(t, te.LuaEngine, `return VirtualThermostat:new("salon", { source = TEMP, output = HEATER, max = 25, persist = true }):getSetpoint()`); got != "25" {
		t.Errorf("restored set point = %s, want 25 (max)", got)
	}
}

// A Grenton sensor's first report after a reload fires no OnChange: the tick takes it.
func TestVirtualThermostat_GrentonSourceFirstReport(t *testing.T) {
	te := newVTEngine(t)
	got := mustExec(t, te.LuaEngine, `(function()
		local clu = { executeNoReply = function() end }
		local src = RemoteThermometer:new(clu, "SEN1", "temp")
		TH = VirtualThermostat:new("salon", { source = src, output = HEATER, setpoint = 21 })
		src:updateState(19.5)               -- the first report: no OnChange
		local a = HEATER:getValue()
		tick()
		return a .. "/" .. HEATER:getValue() .. "/" .. TH:getValue()
	end)()`)
	if got != "0/1/19.5" {
		t.Errorf("output before/after the tick, value = %s, want 0/1/19.5", got)
	}
}

// lib/panel takes it as a Grenton thermostat: menu:add gives the thermostat row and page,
// + writes the set point, and in holiday mode the page only shows.
func TestVirtualThermostat_Panel(t *testing.T) {
	engine := newPanelEngine(t, `
VT_TEMP = VirtualNumber:new("vt_temp")
VT = VirtualThermostat:new("salon", { source = VT_TEMP, output = LAMP, setpoint = 21 })
VT_TEMP:setValue(20.4)
PANEL = Panel.new(P)
	:home(Panel.menu("Dom")
		:add("Salon", VT)
		:thermostat("Pokoj", VT, { readonly = true }))
`)
	got := mustExec(t, engine, `(function()
		T.advance(1000); local home = sent()
		click("PAN1", "btn4"); T.advance(1000); local page = sent()
		click("PAN1", "btn1"); T.advance(2000); local edited = sent()
		local setpoint = VT:getSetpoint()
		VT:setMode(1); T.advance(1000); local holiday = sent()
		click("PAN1", "btn1"); T.advance(2000); sent()
		return Panel.kind(VT) .. "\n#" .. home .. "\n#" .. page .. "\n#" .. edited .. "\n#" .. setpoint ..
			"\n#" .. holiday .. "\n#" .. VT:getSetpoint() .. "/" .. LAMP:getValue()
	end)()`)
	parts := strings.Split(got, "\n#")
	if len(parts) != 7 {
		t.Fatalf("parts = %d in\n%s", len(parts), got)
	}
	if parts[0] != "thermostat" {
		t.Errorf("Panel.kind = %s, want thermostat", parts[0])
	}
	mustContain(t, "rows show the temperature", parts[1], `"Salon"`, `"Pokoj"`, `"20.4"`)
	mustContain(t, "page shows target and temperature", parts[2], `"21.0"`, `"20.4"`, `"+"`)
	mustContain(t, "+ writes the set point", parts[3], `"21.5"`)
	if parts[4] != "21.5" {
		t.Errorf("set point after + = %s, want 21.5", parts[4])
	}
	mustContain(t, "holiday target shows", parts[5], `"16.0"`)
	mustNotContain(t, "holiday: no + and -", parts[5], `"+"`, `"-"`)
	if parts[6] != "21.5/0" {
		t.Errorf("holiday + changed the set point, or the output is on: %s, want 21.5/0", parts[6])
	}
}

// expose() makes it a Home Assistant climate, unchanged: current and target temperature
// and off/heat published, a target from HA goes to setSetpoint; exposeRegistry leaves it out.
func TestVirtualThermostat_HomeAssistant(t *testing.T) {
	engine := NewLuaEngine()
	t.Cleanup(engine.Close)
	got := mustExec(t, engine, `(function()
		_pub = {}
		HAStatePublisher.setup({
			publish = function(self, topic, payload) table.insert(_pub, { topic = topic, payload = payload }); return true end,
			subscribe = function() end, unsubscribe = function() end,
		})
		HACommands.setup({ subscribe = function() end })
		local function last(topic)
			local v
			for i, p in ipairs(_pub) do if p.topic == topic then v = p.payload end end
			return tostring(v)
		end
		local temp, heater = VirtualNumber:new("t"), VirtualSwitch:new("h")
		local th = VirtualThermostat:new("salon", { source = temp, output = heater, setpoint = 21 })
		registerObject("virt.salon", th)
		exposeRegistry()
		local registry = tostring(ExposedObjects:get("virt.salon"))
		local h = th:expose({ name = "Salon", max = 24 })
		local base = "vclu/exposed/vclu/climate/virt_salon"
		temp:setValue(20.2)
		local a = last(base .. "/current_temp") .. "/" .. last(base .. "/target_temp") .. "/" .. last(base .. "/mode")
		local ok = HACommands.handleExposedMessage(base .. "/target_temp/set", "26")
		local b = tostring(ok) .. "/" .. th:getSetpoint() .. "/" .. last(base .. "/target_temp")
		th:stop()
		return registry .. "," .. h.type .. "," .. a .. "," .. b .. "," .. last(base .. "/mode") .. "/" .. heater:getValue()
	end)()`)
	if got != "nil,climate,20.2/21/heat,true/24/24,off/0" {
		t.Errorf("HA climate = %s, want nil,climate,20.2/21/heat,true/24/24,off/0", got)
	}
}

// A listener that fails (a module's onChange, a panel's OnSettingsChange) never keeps the
// output on: a reading above the target, stop() and a stale source still switch it off.
// An output that fails to switch is switched again on the next decision.
func TestVirtualThermostat_FailingListenerOrOutput(t *testing.T) {
	te := newVTEngine(t)
	got := mustExec(t, te.LuaEngine, `(function()
		TH = VirtualThermostat:new("salon", { source = TEMP, output = HEATER, setpoint = 21, staleAfter = 60 })
		local out = {}
		reads(20); table.insert(out, HEATER:getValue())
		TH:onChange(function(v) if v > 21 then error("module bug") end end)
		reads(22); table.insert(out, HEATER:getValue())     -- above 21.5: off
		reads(20); table.insert(out, HEATER:getValue())     -- on again
		TH:onEvent("OnSettingsChange", function() error("panel bug") end)
		TH:stop(); table.insert(out, HEATER:getValue() .. TH:getState())
		TH:start(); table.insert(out, HEATER:getValue())
		return table.concat(out, ",") .. "|" .. tostring(logs():find("a listener failed") ~= nil)
	end)()`)
	if got != "1,0,1,00,1|true" {
		t.Errorf("failing listener: %s, want 1,0,1,00,1|true", got)
	}
	te.advance(t, 60*time.Second)
	if got := mustExec(t, te.LuaEngine, `return HEATER:getValue() .. "/" .. tostring(TH:getFault())`); got != "0/stale" {
		t.Errorf("stale with a failing listener: %s, want 0/stale", got)
	}

	// an output whose switchOff fails once
	got = mustExec(t, te.LuaEngine, `(function()
		local temp, relay = VirtualNumber:new("t3"), VirtualSwitch:new("r3")
		local f = { fail = false }
		local out = { switchOn = function() relay:switchOn() end,
			switchOff = function() if f.fail then error("bus down") end; relay:switchOff() end }
		VirtualThermostat:new("kuchnia", { source = temp, output = out, setpoint = 21 })
		temp:setValue(20)
		f.fail = true; temp:setValue(22)
		local a = relay:getValue()
		f.fail = false; temp:setValue(21.8) -- still above: off again
		return a .. "/" .. relay:getValue()
	end)()`)
	if got != "1/0" {
		t.Errorf("output retried: %s, want 1/0", got)
	}
}

// A cooling thermostat is "cool" in Home Assistant, in its state and in the modes announced.
func TestVirtualThermostat_HomeAssistantCool(t *testing.T) {
	engine := NewLuaEngine()
	t.Cleanup(engine.Close)
	got := mustExec(t, engine, `(function()
		_pub = {}
		HAStatePublisher.setup({
			publish = function(self, topic, payload) table.insert(_pub, { topic = topic, payload = payload }); return true end,
			subscribe = function() end, unsubscribe = function() end,
		})
		local function last(topic)
			local v
			for i, p in ipairs(_pub) do if p.topic == topic then v = p.payload end end
			return tostring(v)
		end
		local temp = VirtualNumber:new("t")
		local th = VirtualThermostat:new("sypialnia", { source = temp, output = VirtualSwitch:new("k"), direction = "cool", setpoint = 24 })
		registerObject("virt.sypialnia", th)
		th:expose({ name = "Sypialnia" })
		temp:setValue(26)
		local config = JSON.decode(last("homeassistant/climate/vclu_virt_sypialnia/config"))
		local mode = last("vclu/exposed/vclu/climate/virt_sypialnia/mode")
		th:stop()
		return table.concat(config.modes, "+") .. "," .. mode .. "," .. last("vclu/exposed/vclu/climate/virt_sypialnia/mode")
	end)()`)
	if got != "off+cool,cool,off" {
		t.Errorf("cooling in HA: modes,mode,stopped = %s, want off+cool,cool,off", got)
	}
}

// A source whose getValue() raises (a plugin that lost its device) still goes stale.
func TestVirtualThermostat_SourceRaises(t *testing.T) {
	te := newVTEngine(t)
	mustExec(t, te.LuaEngine, `
		SRC = { fail = false, getValue = function(self) if self.fail then error("device gone") end return nil end }
		TH = VirtualThermostat:new("salon", { source = SRC, output = HEATER, staleAfter = 60 })
		SRC.fail = true
		logs()`)
	te.advance(t, 60*time.Second)
	got := mustExec(t, te.LuaEngine, `return tostring(TH:getFault()) .. "|" .. logs()`)
	if !strings.HasPrefix(got, "stale|") || !strings.Contains(got, "no reading") {
		t.Errorf("raising source: %q, want stale with its warning", got)
	}
}

// The saved state has its own key, so a VirtualSwitch of the same name does not share it.
func TestVirtualThermostat_PersistKey(t *testing.T) {
	te := newVTEngine(t)
	got := mustExec(t, te.LuaEngine, `(function()
		local th = VirtualThermostat:new("salon", { source = TEMP, output = HEATER, persist = true })
		local named = VirtualThermostat:new("x", { source = TEMP, output = HEATER, persist = "lazienka" })
		th:setSetpoint(22)
		named:setSetpoint(23)
		local s = persist("virtual-objects")
		return type(s:get("thermostat:salon")) .. "/" .. type(s:get("thermostat:lazienka")) .. "/" .. type(s:get("salon"))
	end)()`)
	if got != "table/table/nil" {
		t.Errorf("persist keys: %s, want table/table/nil", got)
	}
}
