package lua

import (
	"strconv"
	"strings"
	"testing"
)

// The thermostat widget (#64): current temperature, target, +/- that set the target. Tests go
// through Dashboard.API, as the web does; a Grenton thermostat is a RemoteThermostat on a stub CLU.

const dashThermostatSetup = `
	_dashboards = {}
	-- a thermostat behind functions: nil until the first report
	S = { current = nil, target = nil, state = nil, sets = {} }
	FNS = {
		getCurrent = function() if S.fail then error('sensor gone') end return S.current end,
		getTarget = function() return S.target end,
		setTarget = function(v) S.sets[#S.sets + 1] = v end,
		getState = function() return S.state end,
	}
	-- a Grenton thermostat; the CLU only records what would be sent
	SENT = {}
	CLU = {
		executeNoReply = function(self, n, s) SENT[#SENT + 1] = s end,
		execute = function(self, n, s) SENT[#SENT + 1] = 'QUERY ' .. s end,
	}
	TH = RemoteThermostat:new(CLU, 'THE1', 'Salon')
	function report(state, mode, point, target, current)
		TH:fromFeature(RemoteThermostat.FEATURE_STATE, state)
		TH:fromFeature(RemoteThermostat.FEATURE_MODE, mode)
		TH:fromFeature(RemoteThermostat.FEATURE_POINT, point)
		TH:fromFeature(RemoteThermostat.FEATURE_TARGET, target)
		TH._lastState = current
	end
	function sent() local s = table.concat(SENT, ';'); SENT = {}; return s end
	X = { off = false }

	Dashboard.new('th')
		:section('Ogrzewanie', 'thermometer-half')
			:thermostat('fns', 'Salon', nil, FNS, { min = 15, max = 25 })
			:thermostat('ro', 'Sypialnia', 'thermometer', { getCurrent = FNS.getCurrent, getTarget = FNS.getTarget })
			:thermostat('obj', 'Grenton', nil, TH, { disabled = function() return X.off end })
			:thermostat('objro', 'Grenton RO', nil, TH, { readonly = true })
		:build()
`

func newThermostatEngine(t *testing.T) *LuaEngine {
	t.Helper()
	engine := NewLuaEngine()
	t.Cleanup(engine.Close)
	mustExec(t, engine, dashThermostatSetup)
	return engine
}

func thermostatAction(t *testing.T, engine *LuaEngine, widget, action, value string) map[string]interface{} {
	t.Helper()
	var res map[string]interface{}
	dashJSON(t, engine, `Dashboard.API.executeAction("th", "`+widget+`", "`+action+`", `+value+`)`, &res)
	return res
}

func thermostatValues(t *testing.T, engine *LuaEngine) map[string]map[string]interface{} {
	t.Helper()
	var vals map[string]map[string]interface{}
	dashJSON(t, engine, `Dashboard.API.getValues("th")`, &vals)
	return vals
}

func TestDashboardThermostatDefinition(t *testing.T) {
	engine := newThermostatEngine(t)
	var def dashDef
	dashJSON(t, engine, `Dashboard.API.getDefinition("th")`, &def)

	w := def.widget("fns")
	caps, _ := w["capabilities"].(map[string]interface{})
	if w["type"] != "thermostat" || w["icon"] != "thermometer-half" || w["label"] != "Salon" ||
		w["min"] != 15.0 || w["max"] != 25.0 || w["step"] != 0.5 || w["unit"] != "°C" ||
		caps["set"] != true || w["set"] != true || w["disabled"] != false {
		t.Errorf("fns = %v", w)
	}
	// nothing reported yet: no current, no target (the page shows --), never 0
	for _, k := range []string{"current", "target", "state", "mode"} {
		if v, ok := w[k]; ok {
			t.Errorf("fns.%s = %v before the first report", k, v)
		}
	}
	ro := def.widget("ro")
	caps, _ = ro["capabilities"].(map[string]interface{})
	if ro["icon"] != "thermometer" || ro["min"] != 5.0 || ro["max"] != 35.0 || caps["set"] != false || ro["set"] != false {
		t.Errorf("ro = %v", ro)
	}
	obj := def.widget("obj")
	caps, _ = obj["capabilities"].(map[string]interface{})
	if obj["type"] != "thermostat" || caps["set"] != true {
		t.Errorf("obj = %v", obj)
	}
	objro := def.widget("objro")
	caps, _ = objro["capabilities"].(map[string]interface{})
	if caps["set"] != false || objro["set"] != false {
		t.Errorf("objro = %v", objro)
	}
}

// Before the first report the values carry no current or target; then what is reported.
func TestDashboardThermostatValues(t *testing.T) {
	engine := newThermostatEngine(t)
	v := thermostatValues(t, engine)["fns"]
	if _, ok := v["current"]; ok {
		t.Errorf("current before a report: %v", v)
	}
	if _, ok := v["target"]; ok {
		t.Errorf("target before a report: %v", v)
	}
	mustExec(t, engine, `S.current = 21.37; S.target = '21.5'; S.state = true`)
	v = thermostatValues(t, engine)["fns"]
	if v["current"] != 21.4 || v["target"] != 21.5 || v["state"] != 1.0 || v["set"] != true || v["disabled"] != false {
		t.Errorf("fns = %v", v)
	}
	// a failing getter: an error, no value
	mustExec(t, engine, `S.fail = true`)
	v = thermostatValues(t, engine)["fns"]
	if _, ok := v["current"]; ok || !strings.Contains(v["error"].(string), "sensor gone") {
		t.Errorf("failing getCurrent = %v", v)
	}
}

// One set reaches setTarget once with the value; the reply carries it for the page.
func TestDashboardThermostatSet(t *testing.T) {
	engine := newThermostatEngine(t)
	res := thermostatAction(t, engine, "fns", "set", "21.5")
	if res["success"] != true || res["value"] != 21.5 {
		t.Fatalf("set = %v", res)
	}
	if got := mustExec(t, engine, `return #S.sets .. ' ' .. tostring(S.sets[1])`); got != "1 21.5" {
		t.Errorf("setTarget calls = %s, want 1 21.5", got)
	}
	// the range ends are allowed
	for _, v := range []string{"15", "25"} {
		if res := thermostatAction(t, engine, "fns", "set", v); res["success"] != true {
			t.Errorf("set %s = %v", v, res)
		}
	}
}

// A read-only thermostat refuses a set and calls nothing.
func TestDashboardThermostatReadOnlyRefusesSet(t *testing.T) {
	engine := newThermostatEngine(t)
	if res := thermostatAction(t, engine, "ro", "set", "21"); res["error"] != "Read-only thermostat" {
		t.Errorf("read-only set = %v", res)
	}
	mustExec(t, engine, `report(1, 0, 20, 20, 19.5)`)
	if res := thermostatAction(t, engine, "objro", "set", "21"); res["error"] != "Read-only thermostat" {
		t.Errorf("readonly object set = %v", res)
	}
	if got := mustExec(t, engine, `return #S.sets .. '|' .. sent()`); got != "0|" {
		t.Errorf("read-only reached the thermostat: %s", got)
	}
}

// Out of min..max, not a number, another action, a disabled widget: refused, nothing called.
func TestDashboardThermostatRefusesBadSet(t *testing.T) {
	engine := newThermostatEngine(t)
	for _, c := range []struct{ widget, action, value, want string }{
		{"fns", "set", "25.5", "Value out of range [15, 25]"},
		{"fns", "set", "14.5", "Value out of range [15, 25]"},
		{"fns", "set", "-100", "Value out of range [15, 25]"},
		{"fns", "set", `"21"`, "Value must be a number"},
		{"fns", "set", `"abc"`, "Value must be a number"},
		{"fns", "set", "nil", "Value must be a number"},
		{"fns", "set", "true", "Value must be a number"},
		{"fns", "set", "0/0", "Value must be a number"},
		{"fns", "set", "math.huge", "Value out of range [15, 25]"},
		{"fns", "toggle", "true", "Invalid action for thermostat"},
		{"obj", "set", "40", "Value out of range [5, 35]"},
	} {
		if res := thermostatAction(t, engine, c.widget, c.action, c.value); res["error"] != c.want {
			t.Errorf("%s %s %s = %v, want %q", c.widget, c.action, c.value, res, c.want)
		}
	}
	mustExec(t, engine, `report(1, 0, 20, 20, 19.5); X.off = true`)
	if res := thermostatAction(t, engine, "obj", "set", "21"); res["error"] != "Widget is disabled" {
		t.Errorf("disabled set = %v", res)
	}
	if got := mustExec(t, engine, `return #S.sets .. '|' .. sent()`); got != "0|" {
		t.Errorf("a refused set reached the thermostat: %s", got)
	}
}

// A RemoteThermostat in place of fns: its temperature, target, state and mode; the set point
// is written only in manual mode, as lib/panel's menu:thermostat.
func TestDashboardThermostatRemoteObject(t *testing.T) {
	engine := newThermostatEngine(t)
	v := thermostatValues(t, engine)["obj"]
	if _, ok := v["current"]; ok {
		t.Errorf("current before a report: %v", v)
	}
	// mode not reported yet: set allowed, as the panel does
	if v["set"] != true {
		t.Errorf("set before the mode is known = %v", v)
	}

	mustExec(t, engine, `report(1, 0, 20, 20, 19.46)`)
	v = thermostatValues(t, engine)["obj"]
	if v["current"] != 19.5 || v["target"] != 20.0 || v["state"] != 1.0 || v["mode"] != 0.0 || v["set"] != true {
		t.Errorf("manual = %v", v)
	}
	if res := thermostatAction(t, engine, "obj", "set", "21.5"); res["success"] != true {
		t.Fatalf("set = %v", res)
	}
	// SetPointValue (set 3), once; never feature 0, the sensor binding
	if got := mustExec(t, engine, `return sent()`); got != "THE1:set(3, 21.5)" {
		t.Errorf("sent = %q, want THE1:set(3, 21.5)", got)
	}
	// the widget shows what the CLU reports, not the value sent
	if v = thermostatValues(t, engine)["obj"]; v["target"] != 20.0 {
		t.Errorf("target after a set = %v, want the reported 20", v["target"])
	}

	// holiday (1) and schedule (2): no buttons, the endpoint refuses
	for _, mode := range []string{"1", "2", "3"} {
		mustExec(t, engine, `report(1, `+mode+`, 21.5, 16, 19.5)`)
		v = thermostatValues(t, engine)["obj"]
		if m, _ := strconv.ParseFloat(mode, 64); v["set"] != false || v["mode"] != m {
			t.Errorf("mode %s = %v", mode, v)
		}
		if res := thermostatAction(t, engine, "obj", "set", "22"); res["error"] != "Thermostat is not in manual mode" {
			t.Errorf("mode %s set = %v", mode, res)
		}
	}
	if got := mustExec(t, engine, `return sent()`); got != "" {
		t.Errorf("sent outside manual mode: %q", got)
	}
	// stopped
	mustExec(t, engine, `report(0, 0, 21.5, 21.5, 19.5)`)
	if v = thermostatValues(t, engine)["obj"]; v["state"] != 0.0 || v["set"] != true {
		t.Errorf("stopped = %v", v)
	}
}

// A getMode that fails says no: the target is not written blind.
func TestDashboardThermostatFailingModeRefuses(t *testing.T) {
	engine := newThermostatEngine(t)
	mustExec(t, engine, `
		Dashboard.new('m'):section('S'):thermostat('t', 'T', nil, {
			getCurrent = function() return 20 end, getTarget = function() return 20 end,
			setTarget = function(v) S.sets[#S.sets + 1] = v end,
			getMode = function() error('no mode') end,
		}):build()`)
	var res map[string]interface{}
	dashJSON(t, engine, `Dashboard.API.executeAction("m", "t", "set", 21)`, &res)
	if res["error"] != "Thermostat is not in manual mode" {
		t.Errorf("set = %v", res)
	}
	if got := mustExec(t, engine, `return #S.sets`); got != "0" {
		t.Errorf("setTarget called %s times", got)
	}
}

func TestDashboardThermostatArgumentErrors(t *testing.T) {
	ok := `{ getCurrent = function() end, getTarget = function() end }`
	cases := []struct{ call, want string }{
		{`:section('S', 'grid'):thermostat('', 'L', nil, ` + ok + `)`, "id must be"},
		{`:section('S', 'grid'):thermostat('a', nil, nil, ` + ok + `)`, "label must be"},
		{`:section('S', 'grid'):thermostat('a', 'L', 'x y', ` + ok + `)`, "icon must be"},
		{`:section('S', 'grid'):thermostat('a', 'L', nil, 5)`, "fns must be"},
		// a wrong object says what it is and what it lacks; nil points at the path
		{`:section('S', 'grid'):thermostat('a', 'L', nil, RemoteSwitch:new({}, 'DOU9270', 'Lampa'))`,
			"DOU9270 (DOUT) is not a thermostat: it has no getTarget, setSetpoint"},
		{`:section('S', 'grid'):thermostat('a', 'L', nil, nil)`, "no thermostat (nil): check the path in _:get(...)"},
		{`:section('S', 'grid'):thermostat('a', 'L', nil, { getCurrent = function() end })`, "needs getCurrent and getTarget"},
		{`:section('S', 'grid'):thermostat('a', 'L', nil, { getCurrent = function() end, getTarget = 20 })`, "fns.getTarget must be a function"},
		{`:section('S', 'grid'):thermostat('a', 'L', nil, { getCurrent = function() end, getTarget = function() end, set = function() end })`, `unknown key "set"`},
		{`:section('S', 'grid'):thermostat('a', 'L', nil, ` + ok + `, 'x')`, "opts must be a table"},
		{`:section('S', 'grid'):thermostat('a', 'L', nil, ` + ok + `, { minimum = 1 })`, `unknown option "minimum"`},
		{`:section('S', 'grid'):thermostat('a', 'L', nil, ` + ok + `, { min = '5' })`, "opts.min must be a number"},
		{`:section('S', 'grid'):thermostat('a', 'L', nil, ` + ok + `, { min = 25, max = 20 })`, "opts.min must be below opts.max"},
		{`:section('S', 'grid'):thermostat('a', 'L', nil, ` + ok + `, { step = 0 })`, "opts.step must be above 0"},
		{`:section('S', 'grid'):thermostat('a', 'L', nil, ` + ok + `, { unit = 5 })`, "opts.unit"},
		{`:section('S', 'grid'):thermostat('a', 'L', nil, ` + ok + `, { readonly = 1 })`, "opts.readonly must be true, false or a function"},
		{`:section('S', 'grid'):thermostat('a', 'L', nil, ` + ok + `, { disabled = true })`, "opts.disabled"},
		{`:header():thermostat('a', 'L', nil, ` + ok + `)`, "not the header"},
	}
	for _, c := range cases {
		engine := NewLuaEngine()
		_, err := engine.Execute("_dashboards = {}\nlocal ok, err = pcall(function()\n  Dashboard.new('e')" + c.call + "\nend)\nerror(err, 0)")
		engine.Close()
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

// The builder form (dash:thermostat after dash:section) gives the same widget.
func TestDashboardThermostatBuilderForm(t *testing.T) {
	engine := newThermostatEngine(t)
	mustExec(t, engine, `
		local d = Dashboard.new('b')
		d:section('S')
		d:thermostat('t', 'T', nil, TH, { step = 1 })
		d:build()`)
	var def dashDef
	dashJSON(t, engine, `Dashboard.API.getDefinition("b")`, &def)
	if w := def.widget("t"); w == nil || w["type"] != "thermostat" || w["step"] != 1.0 {
		t.Errorf("builder widget = %v", w)
	}
}

// section:area shows an exposed thermostat only through kinds (or dashboard = true), with the
// expose options' range; a readonly expose disables it.
func TestDashboardThermostatInArea(t *testing.T) {
	engine := newThermostatEngine(t)
	mustExec(t, engine, `
		report(1, 0, 20, 20, 19.5)
		expose(TH, 'climate', { path = 'salon.termostat', name = 'Termostat', area = 'Salon', min = 16, max = 26 })
		expose(VirtualSwitch:new('sufit'), 'light', { path = 'salon.sufit', name = 'Sufit', area = 'Salon' })
		Dashboard.new('a')
			:section('Domyślne'):area('Salon')
			:section('Z termostatem'):area('Salon', { kinds = { 'light', 'climate' } })
			:build()`)
	var def dashDef
	dashJSON(t, engine, `Dashboard.API.getDefinition("a")`, &def)
	w := def.widget("area:salon.termostat")
	if w == nil || w["section"] != 1.0 {
		t.Fatalf("thermostat widget = %v (default kinds must leave it out)", w)
	}
	if w["type"] != "thermostat" || w["label"] != "Termostat" || w["min"] != 16.0 || w["max"] != 26.0 ||
		w["target"] != 20.0 || w["current"] != 19.5 || w["disabled"] != false {
		t.Errorf("area thermostat = %v", w)
	}
	var res map[string]interface{}
	dashJSON(t, engine, `Dashboard.API.executeAction("a", "area:salon.termostat", "set", 21)`, &res)
	if res["success"] != true {
		t.Errorf("area set = %v", res)
	}
	dashJSON(t, engine, `Dashboard.API.executeAction("a", "area:salon.termostat", "set", 27)`, &res)
	if res["error"] != "Value out of range [16, 26]" {
		t.Errorf("area set out of range = %v", res)
	}
	if got := mustExec(t, engine, `return sent()`); got != "THE1:set(3, 21)" {
		t.Errorf("sent = %q", got)
	}
	mustExec(t, engine, `ExposedObjects:get('salon.termostat'):readonly()`)
	dashJSON(t, engine, `Dashboard.API.executeAction("a", "area:salon.termostat", "set", 22)`, &res)
	if res["error"] != "Widget is disabled" {
		t.Errorf("readonly expose set = %v", res)
	}
	if got := mustExec(t, engine, `return sent()`); got != "" {
		t.Errorf("readonly expose sent %q", got)
	}
}

// readonly as a function locks and unlocks the buttons while the dashboard runs: while it returns
// true (or fails) the values say set=false and locked, and a set is refused; then it works again.
func TestDashboardThermostatReadOnlyFunction(t *testing.T) {
	engine := newThermostatEngine(t)
	mustExec(t, engine, `
		L = { locked = true }
		Dashboard.new('th2')
			:section('Ogrzewanie', 'thermometer-half')
				:thermostat('lock', 'Biuro', nil, TH, { readonly = function()
					if L.fail then error('lock gone') end
					return L.locked
				end })
			:build()
		report(1, 0, 20, 20, 19.5)`)
	values := func() map[string]interface{} {
		var vals map[string]map[string]interface{}
		dashJSON(t, engine, `Dashboard.API.getValues("th2")`, &vals)
		return vals["lock"]
	}
	set := func() map[string]interface{} {
		var res map[string]interface{}
		dashJSON(t, engine, `Dashboard.API.executeAction("th2", "lock", "set", 21)`, &res)
		return res
	}

	var def dashDef
	dashJSON(t, engine, `Dashboard.API.getDefinition("th2")`, &def)
	if caps, _ := def.widget("lock")["capabilities"].(map[string]interface{}); caps["set"] != true {
		t.Errorf("a lockable thermostat has its buttons in the page: %v", def.widget("lock"))
	}

	if v := values(); v["set"] != false || v["locked"] != true {
		t.Errorf("locked values = %v", v)
	}
	if res := set(); res["error"] != "Read-only thermostat" {
		t.Errorf("locked set = %v", res)
	}
	if got := mustExec(t, engine, `return sent()`); got != "" {
		t.Errorf("locked set reached the CLU: %s", got)
	}

	mustExec(t, engine, `L.locked = false`)
	if v := values(); v["set"] != true || v["locked"] != false {
		t.Errorf("unlocked values = %v", v)
	}
	if res := set(); res["success"] != true {
		t.Errorf("unlocked set = %v", res)
	}
	if got := mustExec(t, engine, `return sent()`); !strings.Contains(got, "THE1:set(3, 21)") {
		t.Errorf("unlocked set sent %q", got)
	}

	// a failing lock locks
	mustExec(t, engine, `L.fail = true`)
	if v := values(); v["set"] != false || v["locked"] != true {
		t.Errorf("failing lock values = %v", v)
	}
	if res := set(); res["error"] != "Read-only thermostat" {
		t.Errorf("failing lock set = %v", res)
	}
}

// A room tile shows an exposed thermostat in its widgets only through kinds; the tile's counts
// leave it out. A room or area with default kinds is as before, unless the expose says
// dashboard = true. The room's thermostat takes a set through its area id.
func TestDashboardThermostatInRoom(t *testing.T) {
	engine := newThermostatEngine(t)
	mustExec(t, engine, `
		report(1, 0, 20, 20, 19.5)
		expose(TH, 'climate', { path = 'salon.termostat', name = 'Termostat', area = 'Salon' })
		expose(VirtualSwitch:new('sufit'), 'light', { path = 'salon.sufit', name = 'Sufit', area = 'Salon' })
		Dashboard.new('r')
			:section('Pokoje'):room('salon', 'Salon', { kinds = { 'light', 'climate' } })
			:build()
		Dashboard.new('plain')
			:section('Pokoje'):room('salon', 'Salon')
			:section('Salon'):area('Salon')
			:build()`)

	var def dashDef
	dashJSON(t, engine, `Dashboard.API.getDefinition("r")`, &def)
	w := def.widget("area:salon.termostat")
	if w == nil || w["type"] != "thermostat" || w["target"] != 20.0 || w["current"] != 19.5 {
		t.Fatalf("room thermostat = %v", w)
	}
	var vals map[string]map[string]interface{}
	dashJSON(t, engine, `Dashboard.API.getValues("r")`, &vals)
	if tile := vals["salon"]; tile["on"] != 0.0 || tile["open"] != 0.0 {
		t.Errorf("room tile counts the thermostat: %v", tile)
	}
	var res map[string]interface{}
	dashJSON(t, engine, `Dashboard.API.executeAction("r", "area:salon.termostat", "set", 22)`, &res)
	if res["success"] != true {
		t.Errorf("room set = %v", res)
	}
	if got := mustExec(t, engine, `return sent()`); got != "THE1:set(3, 22)" {
		t.Errorf("sent = %q", got)
	}

	var plain dashDef
	dashJSON(t, engine, `Dashboard.API.getDefinition("plain")`, &plain)
	for _, x := range plain.Widgets {
		if x["type"] == "thermostat" {
			t.Errorf("default kinds show a thermostat: %v", x)
		}
	}
	if plain.widget("area:salon.sufit") == nil {
		t.Errorf("default kinds lost the light: %v", plain.Widgets)
	}

	// dashboard = true opts in: the default kinds show it
	mustExec(t, engine, `
		TH2 = RemoteThermostat:new(CLU, 'THE2', 'Kuchnia')
		expose(TH2, 'climate', { path = 'salon.termostat2', area = 'Salon', dashboard = true })`)
	dashJSON(t, engine, `Dashboard.API.getDefinition("plain")`, &plain)
	if x := plain.widget("area:salon.termostat2"); x == nil || x["type"] != "thermostat" {
		t.Errorf("dashboard = true thermostat = %v", x)
	}
}

// Any object with getValue, getTarget and setSetpoint is a thermostat, not only a Grenton one:
// a virtual thermostat written in Lua (a class with those methods) gets the widget and its sets.
func TestDashboardThermostatOtherObject(t *testing.T) {
	engine := NewLuaEngine()
	defer engine.Close()
	mustExec(t, engine, `
		_dashboards = {}
		Piec = {}
		Piec.__index = Piec
		function Piec.new() return setmetatable({ temp = 19.6, cel = 21, sets = {} }, Piec) end
		function Piec:getValue() return self.temp end
		function Piec:getTarget() return self.cel end
		function Piec:setSetpoint(v) self.sets[#self.sets + 1] = v; self.cel = v end
		piec = Piec.new()
		Dashboard.new('v'):section('S', 'grid'):thermostat('piec', 'Piec', nil, piec):build()`)

	var vals map[string]map[string]interface{}
	dashJSON(t, engine, `Dashboard.API.getValues("v")`, &vals)
	if v := vals["piec"]; v["current"] != 19.6 || v["target"] != 21.0 || v["set"] != true {
		t.Errorf("values = %v", v)
	}
	var res map[string]interface{}
	dashJSON(t, engine, `Dashboard.API.executeAction("v", "piec", "set", 21.5)`, &res)
	if res["success"] != true {
		t.Fatalf("set = %v", res)
	}
	if got := mustExec(t, engine, `return #piec.sets .. ' ' .. piec.sets[1]`); got != "1 21.5" {
		t.Errorf("setSetpoint calls = %s", got)
	}
}
