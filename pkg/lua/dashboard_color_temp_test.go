package lua

import (
	"strings"
	"testing"
)

// The colour temperature widget: a warm-to-cool slider in kelvin, from functions, an object with
// getColorTemp/setColorTemp or mireds through opts.unit. Tests go through Dashboard.API, as the web does.

const dashColorTempSetup = `
	_dashboards = {}
	S = { k = 3000, b = 60, on = true, sets = {}, bsets = {}, calls = {} }
	FNS = {
		getKelvin = function() if S.fail then error('lamp gone') end return S.k end,
		setKelvin = function(k) if S.failSet then error({ code = 1 }) end S.sets[#S.sets + 1] = k end,
		getBrightness = function() return S.b end,
		setBrightness = function(p) S.bsets[#S.bsets + 1] = p end,
		isOn = function() return S.on end,
		switchOn = function() S.calls[#S.calls + 1] = 'on' end,
		switchOff = function() S.calls[#S.calls + 1] = 'off' end,
	}
	-- a Zigbee-like lamp in mireds, and an object with methods in kelvin
	M = { mired = 250, sets = {} }
	MIRED = { getKelvin = function() return M.mired end, setKelvin = function(v) M.sets[#M.sets + 1] = v end }
	Lamp = {}
	Lamp.__index = Lamp
	function Lamp:getColorTemp() return self.k end
	function Lamp:setColorTemp(k) self.k = k end
	OBJ = setmetatable({ k = '4000' }, Lamp)
	X = { off = false }

	Dashboard.new('ct')
		:section('Światło', 'lightbulb')
			:colorTemp('fns', 'Biuro', FNS)
			:colorTemp('mired', 'Salon', MIRED, { unit = 'mired', min = 2200, max = 6500, step = 100 })
			:colorTemp('obj', 'Kuchnia', OBJ, { icon = 'lamp', disabled = function() return X.off end })
		:build()
`

func newColorTempEngine(t *testing.T) *LuaEngine {
	t.Helper()
	engine := NewLuaEngine()
	t.Cleanup(engine.Close)
	mustExec(t, engine, dashColorTempSetup)
	return engine
}

func colorTempAction(t *testing.T, engine *LuaEngine, widget, action, value string) map[string]interface{} {
	t.Helper()
	var res map[string]interface{}
	dashJSON(t, engine, `Dashboard.API.executeAction("ct", "`+widget+`", "`+action+`", `+value+`)`, &res)
	return res
}

func TestDashboardColorTempDefinition(t *testing.T) {
	engine := newColorTempEngine(t)
	var def dashDef
	dashJSON(t, engine, `Dashboard.API.getDefinition("ct")`, &def)

	w := def.widget("fns")
	caps, _ := w["capabilities"].(map[string]interface{})
	if w["type"] != "colorTemp" || w["icon"] != "lightbulb" || w["kelvin"] != 3000.0 || w["brightness"] != 60.0 ||
		w["on"] != true || w["min"] != 2700.0 || w["max"] != 6500.0 || w["step"] != 50.0 ||
		caps["toggle"] != true || caps["brightness"] != true {
		t.Errorf("fns widget = %v", w)
	}
	// 1e6 / 250 mired = 4000 K
	w = def.widget("mired")
	caps, _ = w["capabilities"].(map[string]interface{})
	if w["kelvin"] != 4000.0 || w["min"] != 2200.0 || w["step"] != 100.0 || caps["toggle"] != false || caps["brightness"] != false {
		t.Errorf("mired widget = %v", w)
	}
	if _, ok := w["on"]; ok {
		t.Errorf("mired widget without isOn or brightness has on: %v", w)
	}
	// a decimal string reads as a number
	w = def.widget("obj")
	if w["kelvin"] != 4000.0 || w["icon"] != "lamp" {
		t.Errorf("obj widget = %v", w)
	}
}

// A reading out of min..max is shown as it is; a failing getter is that widget's error only;
// a hex string or garbage is no reading.
func TestDashboardColorTempValues(t *testing.T) {
	engine := newColorTempEngine(t)
	mustExec(t, engine, `S.k = 2000; S.on = false; M.mired = 'abc'; OBJ.k = '0x10'`)
	var vals map[string]map[string]interface{}
	dashJSON(t, engine, `Dashboard.API.getValues("ct")`, &vals)
	if v := vals["fns"]; v["kelvin"] != 2000.0 || v["on"] != false || v["disabled"] != false {
		t.Errorf("fns values = %v", v)
	}
	for _, id := range []string{"mired", "obj"} {
		if _, ok := vals[id]["kelvin"]; ok {
			t.Errorf("%s: a non-decimal reading gave kelvin: %v", id, vals[id])
		}
	}

	mustExec(t, engine, `S.fail = true; X.off = true`)
	dashJSON(t, engine, `Dashboard.API.getValues("ct")`, &vals)
	if v := vals["fns"]; !strings.Contains(v["error"].(string), "lamp gone") {
		t.Errorf("failing getter = %v", v)
	}
	if vals["obj"]["disabled"] != true {
		t.Errorf("obj disabled = %v", vals["obj"])
	}
	var def dashDef
	dashJSON(t, engine, `Dashboard.API.getDefinition("ct")`, &def)
	if w := def.widget("fns"); w == nil || w["error"] == nil {
		t.Errorf("definition with a failing getter = %v", w)
	}
}

func TestDashboardColorTempActions(t *testing.T) {
	engine := newColorTempEngine(t)
	// clamped to min..max and rounded; the reply carries what was set
	for _, c := range []struct {
		value string
		want  float64
	}{{"3000", 3000}, {"9000", 6500}, {"1000", 2700}, {"'3333.6'", 3334}} {
		res := colorTempAction(t, engine, "fns", "setKelvin", c.value)
		if res["success"] != true || res["value"] != c.want {
			t.Errorf("setKelvin %s = %v", c.value, res)
		}
	}
	if got := mustExec(t, engine, `return table.concat(S.sets, ',')`); got != "3000,6500,2700,3334" {
		t.Errorf("setKelvin calls = %s", got)
	}
	for _, bad := range []string{"'0x10'", "'warm'", "nil", "{}"} {
		if res := colorTempAction(t, engine, "fns", "setKelvin", bad); res["error"] != "Value must be a number" {
			t.Errorf("setKelvin %s = %v", bad, res)
		}
	}
	if res := colorTempAction(t, engine, "fns", "set", "40"); res["success"] != true || res["value"] != 40.0 {
		t.Errorf("set brightness = %v", res)
	}
	if res := colorTempAction(t, engine, "fns", "set", "120"); res["error"] != "Value out of range [0, 100]" {
		t.Errorf("set 120 = %v", res)
	}
	colorTempAction(t, engine, "fns", "switchOff", "nil")
	colorTempAction(t, engine, "fns", "switchOn", "nil")
	if got := mustExec(t, engine, `return table.concat(S.bsets, ',') .. ' ' .. table.concat(S.calls, ',')`); got != "40 off,on" {
		t.Errorf("brightness and switch calls = %s", got)
	}

	// mireds out: 1e6 / 4000 = 250; a clamped 9000 K is 6500 K = 154 mired
	colorTempAction(t, engine, "mired", "setKelvin", "4000")
	colorTempAction(t, engine, "mired", "setKelvin", "9000")
	if got := mustExec(t, engine, `return table.concat(M.sets, ',')`); got != "250,154" {
		t.Errorf("mired sets = %s", got)
	}
	for _, a := range []string{"set", "switchOn"} {
		if res := colorTempAction(t, engine, "mired", a, "50"); !strings.HasPrefix(res["error"].(string), "No ") {
			t.Errorf("mired %s without the function = %v", a, res)
		}
	}
	if res := colorTempAction(t, engine, "mired", "setColor", "1"); res["error"] != "Invalid action for colorTemp" {
		t.Errorf("unknown action = %v", res)
	}

	colorTempAction(t, engine, "obj", "setKelvin", "5000")
	if got := mustExec(t, engine, `return tostring(OBJ.k)`); got != "5000" {
		t.Errorf("object kelvin = %s", got)
	}
	mustExec(t, engine, `X.off = true`)
	if res := colorTempAction(t, engine, "obj", "setKelvin", "3000"); res["error"] != "Widget is disabled" {
		t.Errorf("disabled set = %v", res)
	}
	// a setter's error comes back as the action's error
	mustExec(t, engine, `S.failSet = true`)
	if res := colorTempAction(t, engine, "fns", "setKelvin", "3000"); res["error"] == nil {
		t.Errorf("failing setter = %v", res)
	}
}

func TestDashboardColorTempArgumentErrors(t *testing.T) {
	ok := `{ getKelvin = function() end, setKelvin = function() end }`
	cases := []struct{ call, want string }{
		{`:section('S'):colorTemp('', 'L', ` + ok + `)`, "id must be"},
		{`:section('S'):colorTemp('a', nil, ` + ok + `)`, "label must be"},
		{`:section('S'):colorTemp('a', 'L', nil)`, "no light (nil)"},
		{`:section('S'):colorTemp('a', 'L', 5)`, "fns must be"},
		{`:section('S'):colorTemp('a', 'L', { getKelvin = function() end })`, "needs getKelvin and setKelvin"},
		{`:section('S'):colorTemp('a', 'L', { getKelvin = function() end, setKelvin = 1 })`, "fns.setKelvin must be a function"},
		{`:section('S'):colorTemp('a', 'L', { getKelvin = function() end, setKelvin = function() end, set = function() end })`, `unknown key "set"`},
		{`:section('S'):colorTemp('a', 'L', setmetatable({}, { __index = { getColorTemp = function() end } }))`, "no setColorTemp"},
		{`:section('S'):colorTemp('a', 'L', ` + ok + `, 'x')`, "opts must be a table"},
		{`:section('S'):colorTemp('a', 'L', ` + ok + `, { minimum = 1 })`, `unknown option "minimum"`},
		{`:section('S'):colorTemp('a', 'L', ` + ok + `, { min = '2700' })`, "opts.min must be a number"},
		{`:section('S'):colorTemp('a', 'L', ` + ok + `, { min = 6500, max = 2700 })`, "opts.min must be below opts.max"},
		{`:section('S'):colorTemp('a', 'L', ` + ok + `, { min = 0, max = 2700 })`, "opts.min must be above 0"},
		{`:section('S'):colorTemp('a', 'L', ` + ok + `, { step = 0 })`, "opts.step must be above 0"},
		{`:section('S'):colorTemp('a', 'L', ` + ok + `, { unit = 'K' })`, `opts.unit must be "kelvin" or "mired"`},
		{`:section('S'):colorTemp('a', 'L', ` + ok + `, { icon = 'a b' })`, "opts.icon must be"},
		{`:section('S'):colorTemp('a', 'L', ` + ok + `, { disabled = true })`, "opts.disabled must be a function"},
		{`:header():colorTemp('a', 'L', ` + ok + `)`, "not the header"},
	}
	for _, c := range cases {
		for _, form := range []string{"section", "builder"} {
			call := c.call
			if form == "builder" {
				call = strings.Replace(strings.Replace(call, ":section('S'):", ":section('S') d:", 1), ":header():", ":header() d:", 1)
			}
			engine := NewLuaEngine()
			_, err := engine.Execute("_dashboards = {}\nlocal ok, err = pcall(function()\n  local d = Dashboard.new('e'); d" + call + "\nend)\nerror(err, 0)")
			engine.Close()
			if err == nil {
				t.Errorf("%s %s: no error", form, c.call)
				continue
			}
			msg := err.Error()
			if !strings.Contains(msg, "<string>:3: ") || !strings.Contains(msg, "colorTemp") || !strings.Contains(msg, c.want) {
				t.Errorf("%s %s: %s", form, c.call, msg)
			}
		}
	}
}

// section:area gives a light with getColorTemp/setColorTemp the colour temperature widget,
// with its brightness and switch when the object has them.
func TestDashboardColorTempInArea(t *testing.T) {
	engine := newColorTempEngine(t)
	mustExec(t, engine, `
		local d = VirtualDimmer:new('tasma')
		d.k = 3500
		function d:getColorTemp() return self.k end
		function d:setColorTemp(k) self.k = k end
		d:setBrightness(30)
		CT = d
		expose(d, 'light', { path = 'biuro.tasma', name = 'Taśma', area = 'Biuro' })
		expose(VirtualDimmer:new('zwykly'), 'dimmer', { path = 'biuro.zwykly', name = 'Zwykły', area = 'Biuro' })
		Dashboard.new('a'):section('Biuro'):area('Biuro'):build()`)
	var def dashDef
	dashJSON(t, engine, `Dashboard.API.getDefinition("a")`, &def)
	w := def.widget("area:biuro.tasma")
	caps, _ := w["capabilities"].(map[string]interface{})
	if w == nil || w["type"] != "colorTemp" || w["kelvin"] != 3500.0 || w["brightness"] != 30.0 || w["on"] != true ||
		caps["toggle"] != true || caps["brightness"] != true {
		t.Fatalf("area colorTemp = %v", w)
	}
	if w := def.widget("area:biuro.zwykly"); w == nil || w["type"] != "dimmer" {
		t.Errorf("a dimmer without colour temperature = %v", w)
	}
	var res map[string]interface{}
	dashJSON(t, engine, `Dashboard.API.executeAction("a", "area:biuro.tasma", "setKelvin", 6000)`, &res)
	if res["success"] != true || mustExec(t, engine, `return tostring(CT.k)`) != "6000" {
		t.Errorf("area setKelvin = %v, k = %s", res, mustExec(t, engine, `return tostring(CT.k)`))
	}
}
