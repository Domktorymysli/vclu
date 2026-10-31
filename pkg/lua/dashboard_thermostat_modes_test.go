package lua

import (
	"strings"
	"testing"
)

// The thermostat's mode switch: a VirtualThermostat brings its own modes (manual, holiday, off),
// a table of functions gets one with getMode, setMode and opts.modes. A mode goes through
// executeAction("mode"), refused as a target is (disabled, readonly).

const dashThermoModesSetup = `
	_dashboards = {}
	TH = VirtualThermostat:new("salon", { source = TEMP, output = HEATER, setpoint = 21, holiday = 16 })
	TEMP:setValue(19.5)
	X = { off = false, locked = false }
	-- a module's schedule: a flag the calendar follows, the mode by its own functions
	M = { mode = "manual", calls = {} }
	FNS = {
		getCurrent = function() return 20 end, getTarget = function() return 21 end,
		setTarget = function(v) M.calls[#M.calls + 1] = "target " .. v end,
		getMode = function() if M.fail then error("mode gone") end return M.mode end,
		setMode = function(v) M.calls[#M.calls + 1] = "mode " .. v; M.mode = v end,
	}
	Dashboard.new("tm")
		:section("Ogrzewanie")
			:thermostat("vt", "Salon", nil, TH, { disabled = function() return X.off end })
			:thermostat("vtro", "Salon RO", nil, TH, { readonly = true })
			:thermostat("vtlock", "Salon lock", nil, TH, { readonly = function() return X.locked end })
			:thermostat("vtnone", "Salon bez", nil, TH, { modes = false })
			:thermostat("vtlabels", "Salon etykiety", nil, TH, { modes = {
				{ value = 0, label = "Komfort" }, { value = "off", label = "Stop", icon = "x-circle" } } })
			:thermostat("mod", "Biuro", nil, FNS, { modes = {
				{ value = "manual", label = "Ręczny" },
				{ value = "schedule", label = "Harmonogram", icon = "calendar-week", readonly = true },
				{ value = "holiday", label = "Urlop", readonly = true } } })
			:thermostat("map", "Mapa", nil, FNS, { modes = { holiday = "Urlop", manual = "Ręczny", schedule = "Harmonogram" } })
		:build()
`

func newThermoModesEngine(t *testing.T) *vtEngine {
	t.Helper()
	te := newVTEngine(t)
	mustExec(t, te.LuaEngine, dashThermoModesSetup)
	return te
}

func thermoModeAction(t *testing.T, engine *LuaEngine, widget, action, value string) map[string]interface{} {
	t.Helper()
	var res map[string]interface{}
	dashJSON(t, engine, `Dashboard.API.executeAction("tm", "`+widget+`", "`+action+`", `+value+`)`, &res)
	return res
}

func thermoModeValues(t *testing.T, engine *LuaEngine) map[string]map[string]interface{} {
	t.Helper()
	var vals map[string]map[string]interface{}
	dashJSON(t, engine, `Dashboard.API.getValues("tm")`, &vals)
	return vals
}

func modeValues(w map[string]interface{}) []interface{} {
	list, _ := w["modes"].([]interface{})
	out := []interface{}{}
	for _, m := range list {
		out = append(out, m.(map[string]interface{})["value"])
	}
	return out
}

func TestDashboardThermostatModesDefinition(t *testing.T) {
	te := newThermoModesEngine(t)
	var def dashDef
	dashJSON(t, te.LuaEngine, `Dashboard.API.getDefinition("tm")`, &def)

	vt := def.widget("vt")
	caps, _ := vt["capabilities"].(map[string]interface{})
	if caps["mode"] != true || caps["set"] != true || vt["mode"] != 0.0 {
		t.Errorf("vt = %v", vt)
	}
	modes, _ := vt["modes"].([]interface{})
	if len(modes) != 3 {
		t.Fatalf("vt modes = %v", vt["modes"])
	}
	// the object's own modes carry a name for the page's texts, no label
	m0 := modes[0].(map[string]interface{})
	m2 := modes[2].(map[string]interface{})
	if m0["value"] != 0.0 || m0["name"] != "manual" || m0["label"] != nil || m2["value"] != "off" || m2["name"] != "off" {
		t.Errorf("vt modes = %v", modes)
	}
	// readonly = true: the modes for the state line, no switch
	if caps, _ := def.widget("vtro")["capabilities"].(map[string]interface{}); caps["mode"] != false || len(modeValues(def.widget("vtro"))) != 3 {
		t.Errorf("vtro = %v", def.widget("vtro"))
	}
	// a lock function keeps the switch in the page; the values lock it
	if caps, _ := def.widget("vtlock")["capabilities"].(map[string]interface{}); caps["mode"] != true {
		t.Errorf("vtlock = %v", def.widget("vtlock"))
	}
	none := def.widget("vtnone")
	if caps, _ := none["capabilities"].(map[string]interface{}); caps["mode"] != false || none["modes"] != nil {
		t.Errorf("vtnone = %v", none)
	}
	labels := def.widget("vtlabels")
	lm, _ := labels["modes"].([]interface{})
	if len(lm) != 2 || lm[0].(map[string]interface{})["label"] != "Komfort" ||
		lm[0].(map[string]interface{})["icon"] != "hand-index" || lm[1].(map[string]interface{})["icon"] != "x-circle" {
		t.Errorf("vtlabels = %v", labels["modes"])
	}
	mod := def.widget("mod")
	if got := modeValues(mod); len(got) != 3 || got[0] != "manual" || got[1] != "schedule" || got[2] != "holiday" || mod["mode"] != "manual" {
		t.Errorf("mod = %v", mod)
	}
	// a map is ordered by the well-known names
	if got := modeValues(def.widget("map")); len(got) != 3 || got[0] != "manual" || got[1] != "schedule" || got[2] != "holiday" {
		t.Errorf("map order = %v", got)
	}
}

// A VirtualThermostat: holiday by setMode, off by stop, and a mode chosen while off starts it.
func TestDashboardThermostatModesVirtual(t *testing.T) {
	te := newThermoModesEngine(t)
	e := te.LuaEngine
	res := thermoModeAction(t, e, "vt", "mode", "1")
	// the reply is the tile now: the holiday target, no + and -
	if res["success"] != true || res["mode"] != 1.0 || res["value"] != 1.0 || res["target"] != 16.0 || res["set"] != false {
		t.Fatalf("holiday = %v", res)
	}
	if got := mustExec(t, e, `return TH:getMode() .. " " .. TH:getTarget()`); got != "1 16" {
		t.Errorf("after holiday: %s", got)
	}
	v := thermoModeValues(t, e)["vt"]
	// holiday is readonly: no + and -, and a set is refused
	if v["mode"] != 1.0 || v["set"] != false {
		t.Errorf("holiday values = %v", v)
	}
	if res := thermoModeAction(t, e, "vt", "set", "22"); res["error"] != "Thermostat is not in manual mode" {
		t.Errorf("set in holiday = %v", res)
	}

	if res := thermoModeAction(t, e, "vt", "mode", `"off"`); res["success"] != true {
		t.Fatalf("off = %v", res)
	}
	if got := mustExec(t, e, `return TH:getState() .. " " .. TH:getMode()`); got != "0 1" {
		t.Errorf("after off: %s", got)
	}
	// off after holiday: the holiday target stays shown, so no + and -
	if v := thermoModeValues(t, e)["vt"]; v["mode"] != "off" || v["state"] != 0.0 || v["set"] != false {
		t.Errorf("off values = %v", v)
	}
	if res := thermoModeAction(t, e, "vt", "mode", "0"); res["success"] != true {
		t.Fatalf("manual = %v", res)
	}
	if got := mustExec(t, e, `return TH:getState() .. " " .. TH:getMode()`); got != "1 0" {
		t.Errorf("manual from off: %s", got)
	}
	if v := thermoModeValues(t, e)["vt"]; v["mode"] != 0.0 || v["set"] != true {
		t.Errorf("manual values = %v", v)
	}
	// off from manual: the set point can be changed while it is off, as before the switch
	mustExec(t, e, `TH:stop()`)
	if v := thermoModeValues(t, e)["vt"]; v["mode"] != "off" || v["set"] != true {
		t.Errorf("off from manual = %v", v)
	}
	mustExec(t, e, `TH:start()`)
	// labels of the module, values of the object
	if res := thermoModeAction(t, e, "vtlabels", "mode", `"off"`); res["success"] != true {
		t.Errorf("vtlabels off = %v", res)
	}
	if res := thermoModeAction(t, e, "vtlabels", "mode", "1"); res["error"] != "Unknown mode" {
		t.Errorf("vtlabels holiday (not listed) = %v", res)
	}
}

// A module's modes: setMode gets the value; schedule (readonly) takes no target.
func TestDashboardThermostatModesFunctions(t *testing.T) {
	te := newThermoModesEngine(t)
	e := te.LuaEngine
	if res := thermoModeAction(t, e, "mod", "mode", `"schedule"`); res["success"] != true || res["mode"] != "schedule" {
		t.Fatalf("schedule = %v", res)
	}
	if v := thermoModeValues(t, e)["mod"]; v["mode"] != "schedule" || v["set"] != false {
		t.Errorf("schedule values = %v", v)
	}
	if res := thermoModeAction(t, e, "mod", "set", "22"); res["error"] != "Thermostat is not in manual mode" {
		t.Errorf("set in schedule = %v", res)
	}
	// a map has no readonly: the target is set in every mode
	if v := thermoModeValues(t, e)["map"]; v["mode"] != "schedule" || v["set"] != true {
		t.Errorf("map values = %v", v)
	}
	// a mode the list does not have: nothing lit, no target
	mustExec(t, e, `M.mode = "eco"`)
	if v := thermoModeValues(t, e)["mod"]; v["mode"] != "eco" || v["set"] != false {
		t.Errorf("unlisted mode values = %v", v)
	}
	// a failing getMode: no mode, no target, the rest of the tile and the others stay
	mustExec(t, e, `M.fail = true`)
	vals := thermoModeValues(t, e)
	if v := vals["mod"]; v["mode"] != nil || v["set"] != false || v["current"] != 20.0 {
		t.Errorf("failing getMode = %v", v)
	}
	if vals["vt"]["current"] != 19.5 {
		t.Errorf("another tile broke: %v", vals["vt"])
	}
	if got := mustExec(t, e, `return table.concat(M.calls, ",")`); got != "mode schedule" {
		t.Errorf("calls = %s", got)
	}
}

// Refused: not a listed value, a wrong type, readonly, locked, disabled, a failing setMode.
func TestDashboardThermostatModesRefused(t *testing.T) {
	te := newThermoModesEngine(t)
	e := te.LuaEngine
	for _, c := range []struct{ widget, value, want string }{
		{"mod", `"eco"`, "Unknown mode"},
		{"mod", "0", "Unknown mode"},
		{"mod", "nil", "Unknown mode"},
		{"mod", "true", "Unknown mode"},
		{"vt", `"0"`, "Unknown mode"},
		{"vt", "2", "Unknown mode"},
		{"vtro", "1", "Read-only thermostat"},
		{"vtnone", "1", "Read-only thermostat"},
	} {
		if res := thermoModeAction(t, e, c.widget, "mode", c.value); res["error"] != c.want {
			t.Errorf("%s mode %s = %v, want %q", c.widget, c.value, res, c.want)
		}
	}
	mustExec(t, e, `X.locked = true`)
	if v := thermoModeValues(t, e)["vtlock"]; v["locked"] != true {
		t.Errorf("locked values = %v", v)
	}
	if res := thermoModeAction(t, e, "vtlock", "mode", "1"); res["error"] != "Read-only thermostat" {
		t.Errorf("locked mode = %v", res)
	}
	mustExec(t, e, `X.off = true`)
	if res := thermoModeAction(t, e, "vt", "mode", "1"); res["error"] != "Widget is disabled" {
		t.Errorf("disabled mode = %v", res)
	}
	if got := mustExec(t, e, `return TH:getMode() .. " " .. TH:getState() .. " " .. #M.calls`); got != "0 1 0" {
		t.Errorf("a refused mode reached the thermostat: %s", got)
	}
	mustExec(t, e, `FNS.setMode = nil`) // the widget keeps its own reference
	mustExec(t, e, `M.boom = true`)
	mustExec(t, e, `
		Dashboard.new("bad"):section("S"):thermostat("b", "B", nil, {
			getCurrent = function() return 20 end, getTarget = function() return 20 end,
			getMode = function() return 0 end, setMode = function() error(setmetatable({}, { __tostring = function() error("x") end })) end,
		}, { modes = { [0] = "A", [1] = "B" } }):build()`)
	var res map[string]interface{}
	dashJSON(t, e, `Dashboard.API.executeAction("bad", "b", "mode", 1)`, &res)
	if res["error"] != "error" {
		t.Errorf("failing setMode = %v", res)
	}
}

// A RemoteThermostat has no setMode: its mode shows, there is no switch; opts.modes says why.
func TestDashboardThermostatModesRemote(t *testing.T) {
	te := newThermoModesEngine(t)
	e := te.LuaEngine
	mustExec(t, e, `
		CLU = { executeNoReply = function() end, execute = function() end }
		RT = RemoteThermostat:new(CLU, "THE1", "Salon")
		Dashboard.new("r"):section("S"):thermostat("rt", "RT", nil, RT):build()`)
	var def dashDef
	dashJSON(t, e, `Dashboard.API.getDefinition("r")`, &def)
	w := def.widget("rt")
	if caps, _ := w["capabilities"].(map[string]interface{}); caps["mode"] != false || w["modes"] != nil {
		t.Errorf("remote = %v", w)
	}
	_, err := e.Execute("local ok, err = pcall(function()\n  Dashboard.new('x'):section('S'):thermostat('a', 'A', nil, RT, { modes = { [0] = 'A' } })\nend)\nerror(err, 0)")
	if err == nil || !strings.Contains(err.Error(), "<string>:2: ") || !strings.Contains(err.Error(), "has no setMode") {
		t.Errorf("remote with modes: %v", err)
	}
}

// An exposed VirtualThermostat in section:area gets its modes; a readonly expose disables them.
func TestDashboardThermostatModesInArea(t *testing.T) {
	te := newThermoModesEngine(t)
	e := te.LuaEngine
	mustExec(t, e, `
		expose(TH, "climate", { path = "salon.termostat", name = "Termostat", area = "Salon" })
		Dashboard.new("a"):section("S"):area("Salon", { kinds = { "climate" } }):build()`)
	var def dashDef
	dashJSON(t, e, `Dashboard.API.getDefinition("a")`, &def)
	w := def.widget("area:salon.termostat")
	if caps, _ := w["capabilities"].(map[string]interface{}); caps["mode"] != true || len(modeValues(w)) != 3 {
		t.Fatalf("area = %v", w)
	}
	var res map[string]interface{}
	dashJSON(t, e, `Dashboard.API.executeAction("a", "area:salon.termostat", "mode", 1)`, &res)
	if res["success"] != true {
		t.Errorf("area mode = %v", res)
	}
	mustExec(t, e, `ExposedObjects:get("salon.termostat"):readonly()`)
	dashJSON(t, e, `Dashboard.API.executeAction("a", "area:salon.termostat", "mode", 0)`, &res)
	if res["error"] != "Widget is disabled" {
		t.Errorf("readonly expose mode = %v", res)
	}
	if got := mustExec(t, e, `return TH:getMode()`); got != "1" {
		t.Errorf("mode = %s", got)
	}
}

func TestDashboardThermostatModesArgumentErrors(t *testing.T) {
	fns := `{ getCurrent = function() end, getTarget = function() end, getMode = function() end, setMode = function() end }`
	plain := `{ getCurrent = function() end, getTarget = function() end }`
	cases := []struct{ call, want string }{
		{`:thermostat('a', 'L', nil, ` + fns + `, { modes = 'manual' })`, "opts.modes must be a list"},
		{`:thermostat('a', 'L', nil, ` + fns + `, { modes = {} })`, "opts.modes is empty"},
		{`:thermostat('a', 'L', nil, ` + fns + `, { modes = { { value = 0, label = 'A' }, x = 1 } })`, "not both"},
		{`:thermostat('a', 'L', nil, ` + fns + `, { modes = { { value = 0, label = 'A' }, { value = 0, label = 'B' } } })`, "the value 0 is there twice"},
		{`:thermostat('a', 'L', nil, ` + fns + `, { modes = { { value = 0, label = 'A', colour = 'red' } } })`, `opts.modes[1]: unknown key "colour"`},
		{`:thermostat('a', 'L', nil, ` + fns + `, { modes = { { value = 0 } } })`, "opts.modes[1] needs a label"},
		{`:thermostat('a', 'L', nil, ` + fns + `, { modes = { { value = 0, label = '' } } })`, "opts.modes[1].label must be a non-empty string"},
		{`:thermostat('a', 'L', nil, ` + fns + `, { modes = { { value = 0/0, label = 'A' } } })`, "value must be a number or a non-empty string"},
		{`:thermostat('a', 'L', nil, ` + fns + `, { modes = { { value = true, label = 'A' } } })`, "value must be a number or a non-empty string"},
		{`:thermostat('a', 'L', nil, ` + fns + `, { modes = { { value = 0, label = 'A', icon = 'a b' } } })`, "icon must be a Bootstrap icon name"},
		{`:thermostat('a', 'L', nil, ` + fns + `, { modes = { { value = 0, label = 'A', readonly = 1 } } })`, "readonly must be true or false"},
		{`:thermostat('a', 'L', nil, ` + fns + `, { modes = { manual = 5 } })`, "must be a label"},
		{`:thermostat('a', 'L', nil, ` + fns + `)`, "fns.setMode needs opts.modes"},
		{`:thermostat('a', 'L', nil, ` + plain + `, { modes = { manual = 'R' } })`, "opts.modes needs fns.getMode and fns.setMode"},
		{`:thermostat('a', 'L', nil, { getCurrent = function() end, getTarget = function() end, setMode = function() end }, { modes = { manual = 'R' } })`, "opts.modes needs fns.getMode and fns.setMode"},
		{`:thermostat('a', 'L', nil, { getCurrent = function() end, getTarget = function() end, setMode = 1 })`, "fns.setMode must be a function"},
		{`:thermostat('a', 'L', nil, TH, { modes = { [2] = 'Auto' } })`, "2 is not a mode of this thermostat"},
		{`:thermostat('a', 'L', nil, TH, { modes = 1 })`, "or false"},
	}
	for _, c := range cases {
		te := newVTEngine(t)
		mustExec(t, te.LuaEngine, `TH = VirtualThermostat:new("salon", { source = TEMP, output = HEATER })`)
		_, err := te.Execute("_dashboards = {}\nlocal ok, err = pcall(function()\n  Dashboard.new('e'):section('S')" + c.call + "\nend)\nerror(err, 0)")
		if err == nil {
			t.Errorf("%s: no error", c.call)
			continue
		}
		msg := err.Error()
		if !strings.Contains(msg, "<string>:3: ") || !strings.Contains(msg, "section:thermostat") || !strings.Contains(msg, c.want) {
			t.Errorf("%s: %s", c.call, msg)
		}
	}
}

// A map with number keys goes by value; the reply carries the mode read back, without a sensor's error.
func TestDashboardThermostatModesNumberMapAndReply(t *testing.T) {
	te := newThermoModesEngine(t)
	e := te.LuaEngine
	mustExec(t, e, `
		N = { mode = 0 }
		Dashboard.new("n"):section("S"):thermostat("n", "N", nil, {
			getCurrent = function() error("sensor gone") end, getTarget = function() return 20 end,
			getMode = function() return N.mode end,
			setMode = function(v) N.asked = v end, -- applied later, as a CLU would
		}, { modes = { [10] = "Dziesięć", [3] = "Trzy", [0] = "Zero", [2] = "Dwa", [1] = "Jeden" } }):build()`)
	var def dashDef
	dashJSON(t, e, `Dashboard.API.getDefinition("n")`, &def)
	got := modeValues(def.widget("n"))
	want := []interface{}{0.0, 1.0, 2.0, 3.0, 10.0}
	if len(got) != len(want) {
		t.Fatalf("order = %v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("order = %v, want %v", got, want)
			break
		}
	}
	var res map[string]interface{}
	dashJSON(t, e, `Dashboard.API.executeAction("n", "n", "mode", 3)`, &res)
	if res["success"] != true || res["mode"] != 0.0 || res["value"] != 3.0 {
		t.Errorf("reply = %v, want the mode read back (0)", res)
	}
	if _, ok := res["error"]; ok {
		t.Errorf("a successful reply carries an error: %v", res)
	}
	if got := mustExec(t, e, `return N.asked`); got != "3" {
		t.Errorf("setMode got %s", got)
	}
}
