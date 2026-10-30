package lua

import (
	"strings"
	"testing"
)

// Gauge tile: the value as getValue returns it (the page clamps the drawing), nil left out,
// a failing getValue as the tile's error, a target from a number or a function.

const dashGaugeSetup = `
	_dashboards = {}
	G = { power = 7.42, limit = 11 }
	Dashboard.new('g')
		:section('Energia', 'lightning')
			:gauge('power', 'Moc domu', function() return G.power end, { min = 0, max = 14, unit = 'kW', decimals = 1,
				thresholds = { { 8, 'warning' }, { 11, 'danger' } }, target = function() return G.limit end })
			:gauge('plain', 'Plain', function() return G.plain end)
			:gauge('bar', 'Bar', function() return '42' end, { style = 'bar', target = 50, min = -10, max = 60,
				icon = 'water', span = 2, color = '#1a2b3c', disabled = function() return G.off == true end })
			:gauge('fail', 'Fail', function() if G.fail then error('meter gone') end return 1 end)
			:gauge('text', 'Text', function() return G.text end)
		:build()
`

func newGaugeEngine(t *testing.T) *LuaEngine {
	t.Helper()
	engine := NewLuaEngine()
	t.Cleanup(engine.Close)
	mustExec(t, engine, dashGaugeSetup)
	return engine
}

func gaugeValues(t *testing.T, engine *LuaEngine) map[string]map[string]interface{} {
	t.Helper()
	var vals map[string]map[string]interface{}
	dashJSON(t, engine, `Dashboard.API.getValues("g")`, &vals)
	return vals
}

func TestDashboardGaugeDefinition(t *testing.T) {
	engine := newGaugeEngine(t)
	var def dashDef
	dashJSON(t, engine, `Dashboard.API.getDefinition("g")`, &def)

	p := def.widget("power")
	if p["type"] != "gauge" || p["icon"] != "speedometer2" || p["style"] != "arc" || p["value"] != 7.42 ||
		p["min"] != float64(0) || p["max"] != float64(14) || p["unit"] != "kW" || p["decimals"] != float64(1) ||
		p["target"] != float64(11) {
		t.Errorf("power = %v", p)
	}
	th := list(p["thresholds"])
	if len(th) != 2 || th[0]["value"] != float64(8) || th[0]["color"] != "warning" || th[1]["value"] != float64(11) || th[1]["color"] != "danger" {
		t.Errorf("power thresholds = %v", p["thresholds"])
	}

	// defaults: 0..100, no unit, no target, no thresholds (an empty list, not an object)
	plain := def.widget("plain")
	if plain["min"] != float64(0) || plain["max"] != float64(100) || plain["unit"] != "" || plain["style"] != "arc" {
		t.Errorf("plain = %v", plain)
	}
	if v, ok := plain["value"]; ok {
		t.Errorf("plain value = %v, want none", v)
	}
	if _, ok := plain["target"]; ok {
		t.Errorf("plain has a target: %v", plain["target"])
	}
	if th, ok := plain["thresholds"].([]interface{}); !ok || len(th) != 0 {
		t.Errorf("plain thresholds = %#v", plain["thresholds"])
	}

	// a numeric string from a CLU is a number; the static target and own options pass through
	bar := def.widget("bar")
	if bar["style"] != "bar" || bar["value"] != float64(42) || bar["target"] != float64(50) || bar["min"] != float64(-10) ||
		bar["icon"] != "water" || bar["span"] != float64(2) || bar["color"] != "#1a2b3c" {
		t.Errorf("bar = %v", bar)
	}

	// the agents' listing names the type
	var dashes []map[string]interface{}
	dashJSON(t, engine, `Dashboard.API.list()`, &dashes)
	if ws := list(dashes[0]["widgets"]); len(ws) != 5 || ws[0]["type"] != "gauge" {
		t.Errorf("list = %v", dashes)
	}
}

// A value outside min..max reaches the page as it is; the target follows its function
func TestDashboardGaugeValues(t *testing.T) {
	engine := newGaugeEngine(t)
	mustExec(t, engine, `G.power = 15.8; G.limit = 9; G.off = true`)
	vals := gaugeValues(t, engine)
	if v := vals["power"]; v["value"] != 15.8 || v["target"] != float64(9) || v["disabled"] != false || v["error"] != nil {
		t.Errorf("power = %v", v)
	}
	if v := vals["bar"]; v["value"] != float64(42) || v["target"] != float64(50) || v["disabled"] != true {
		t.Errorf("bar = %v", v)
	}
	if v := vals["plain"]; len(v) != 1 || v["disabled"] != false {
		t.Errorf("plain = %v, want no value", v)
	}

	// a target function giving nil, what is not a number, or failing sends none: the page drops the marker
	for _, limit := range []string{"nil", "'none'", "function() end"} {
		mustExec(t, engine, `G.limit = `+limit)
		if v, ok := gaugeValues(t, engine)["power"]["target"]; ok {
			t.Errorf("target from %s = %v", limit, v)
		}
	}
	mustExec(t, engine, `Dashboard.new('t'):section('S', 'grid')
		:gauge('t', 'T', function() return 1 end, { target = function() error('no limit') end }):build()`)
	var vals2 map[string]map[string]interface{}
	dashJSON(t, engine, `Dashboard.API.getValues("t")`, &vals2)
	if _, ok := vals2["t"]["target"]; ok || vals2["t"]["value"] != float64(1) || vals2["t"]["error"] != nil {
		t.Errorf("failing target = %v", vals2["t"])
	}
}

// getValue failing, or giving what is not a number, is the tile's error with a null value
func TestDashboardGaugeError(t *testing.T) {
	engine := newGaugeEngine(t)
	mustExec(t, engine, `G.fail = true; G.text = 'high'`)
	vals := gaugeValues(t, engine)
	if v := vals["fail"]; v["value"] != nil || !strings.Contains(v["error"].(string), "meter gone") {
		t.Errorf("fail = %v", v)
	}
	if e, _ := vals["text"]["error"].(string); !strings.Contains(e, `section:gauge("text")`) || !strings.Contains(e, "got string") {
		t.Errorf("text = %v", vals["text"])
	}
	var def dashDef
	dashJSON(t, engine, `Dashboard.API.getDefinition("g")`, &def)
	if e, _ := def.widget("fail")["error"].(string); !strings.Contains(e, "meter gone") {
		t.Errorf("fail definition = %v", def.widget("fail"))
	}
	mustExec(t, engine, `G.fail = false; G.text = true`)
	vals = gaugeValues(t, engine)
	if v := vals["fail"]; v["value"] != float64(1) || v["error"] != nil {
		t.Errorf("fail after recovery = %v", v)
	}
	if e, _ := vals["text"]["error"].(string); !strings.Contains(e, "got boolean") {
		t.Errorf("text boolean = %v", vals["text"])
	}

	// NaN, inf and numerals other than decimal (hex) are errors, not a dash
	mustExec(t, engine, `G.text = 0/0`)
	if e, _ := gaugeValues(t, engine)["text"]["error"].(string); !strings.Contains(e, "not finite") {
		t.Errorf("text NaN = %v", gaugeValues(t, engine)["text"])
	}
	mustExec(t, engine, `G.text = math.huge * 2`)
	if e, _ := gaugeValues(t, engine)["text"]["error"].(string); !strings.Contains(e, "got a number") {
		t.Errorf("text inf = %v", gaugeValues(t, engine)["text"])
	}
	for _, str := range []string{"'nan'", "'0x1F'", "'1e3'"} {
		mustExec(t, engine, `G.text = `+str)
		if v := gaugeValues(t, engine)["text"]; v["value"] != nil || v["error"] == nil {
			t.Errorf("text %s = %v", str, v)
		}
	}
	mustExec(t, engine, `G.text = ' -2.5 '`)
	if v := gaugeValues(t, engine)["text"]; v["value"] != -2.5 || v["error"] != nil {
		t.Errorf("text ' -2.5 ' = %v", v)
	}
	// an error value tostring cannot turn into text still gives an error
	mustExec(t, engine, `Dashboard.new('bad'):section('S', 'grid'):gauge('b', 'B', function()
		error(setmetatable({}, { __tostring = function() error('no text') end })) end):build()`)
	var bad map[string]map[string]interface{}
	dashJSON(t, engine, `Dashboard.API.getValues("bad")`, &bad)
	if bad["b"]["error"] == nil {
		t.Errorf("bad error value = %v", bad["b"])
	}

	var res map[string]interface{}
	dashJSON(t, engine, `Dashboard.API.executeAction("g", "power", "set", 5)`, &res)
	if res["error"] != "Read-only widget" {
		t.Errorf("action = %v", res)
	}
}

// Wrong arguments fail at the module's line and name section:gauge.
func TestDashboardGaugeArgumentErrors(t *testing.T) {
	fn := `function() return 1 end`
	g := `:section('S', 'grid'):gauge('a', 'L', ` + fn
	cases := []struct{ call, want string }{
		{`:section('S', 'grid'):gauge('', 'L', ` + fn + `)`, "id must be"},
		{`:section('S', 'grid'):gauge('a', nil, ` + fn + `)`, "label must be"},
		{`:section('S', 'grid'):gauge('a', 'L', 5)`, "getValue must be a function"},
		{g + `, 'x')`, "opts must be a table"},
		{g + `, { maximum = 3 })`, `unknown option "maximum"`},
		{g + `, { min = '0' })`, "opts.min must be a number"},
		{g + `, { max = 0/0 })`, "opts.max must be a number"},
		{g + `, { min = 10, max = 10 })`, "opts.min must be below opts.max"},
		{g + `, { max = -5 })`, "opts.min must be below opts.max"},
		{g + `, { decimals = 1.5 })`, "opts.decimals must be a whole number 0-6"},
		{g + `, { unit = 'kilowatts!' })`, "opts.unit"},
		{g + `, { style = 'ring' })`, `opts.style must be "arc" or "bar"`},
		{g + `, { icon = 'x y' })`, "opts.icon"},
		{g + `, { disabled = true })`, "opts.disabled"},
		{g + `, { color = 'warnign' })`, "opts.color must be"},
		{g + `, { target = '11' })`, "opts.target must be a number or a function"},
		{g + `, { thresholds = 60 })`, "opts.thresholds must be a list"},
		{g + `, { thresholds = { 60, 'warning' } })`, "threshold 1 must be { value, color }"},
		{g + `, { thresholds = { [1] = { 60, 'warning' }, [3] = { 90, 'danger' } } })`, "opts.thresholds must be a list"},
		{g + `, { thresholds = { warning = 60 } })`, "opts.thresholds must be a list"},
		{g + `, { thresholds = { { 60 } } })`, "threshold 1: colour must be"},
		{g + `, { thresholds = { { 60, 'red-ish' } } })`, "threshold 1: colour must be"},
		{g + `, { thresholds = { { 90, 'danger' }, { 60, 'warning' } } })`, "ascending"},
		{g + `, { thresholds = { { 60, 'warning' }, { 60, 'danger' } } })`, "ascending"},
		{`:header():gauge('a', 'L', ` + fn + `)`, "not the header"},
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
		if !strings.Contains(msg, "<string>:3: ") || !strings.Contains(msg, "section:gauge") || !strings.Contains(msg, c.want) {
			t.Errorf("%s: %s", c.call, msg)
		}
	}
	// the builder's own method too
	engine := NewLuaEngine()
	defer engine.Close()
	_, err := engine.Execute("_dashboards = {}\nlocal ok, err = pcall(function()\n  local b = Dashboard.new('f'); b:section('S', 'g')\n  b:gauge('a', 'L', function() end, { decimals = 9 })\nend)\nerror(err, 0)")
	if err == nil || !strings.Contains(err.Error(), "<string>:4: ") || !strings.Contains(err.Error(), "section:gauge") {
		t.Errorf("builder gauge: %v", err)
	}
	// colours the page knows, hex and rgb()/hsl() pass
	mustExec(t, engine, `Dashboard.new('ok'):section('S', 'grid'):gauge('a', 'L', function() end, { thresholds = {
		{ 1, 'success' }, { 2, '#fa0' }, { 3, '#ffaa0080' }, { 4, 'rgb(10, 20, 30)' }, { 5, 'hsl(20, 50%, 40%)' }, { 6, 'accent' } } }):build()`)
}
