package lua

import (
	"reflect"
	"strconv"
	"strings"
	"testing"
)

// Energy flow tile: node powers in W, home from the balance, the power on each line, invert,
// nodes left out, a failing source on its node only, today's kWh, and the argument errors.

const dashEnergySetup = `
	_dashboards = {}
	E = { pv = 3000, grid = -1200, battery = 800, soc = 54.6, home = 950,
		pvToday = 12.345, imp = 3.06, exp = 8.24, off = false }
	local function src(k) return function() return E[k] end end
	Dashboard.new('en')
		:section('Energy', 'lightning')
			:energy('all', 'Energia', { pv = src('pv'), grid = src('grid'), battery = src('battery'),
				batterySoc = src('soc'), today = { pv = src('pvToday'), grid_import = src('imp'),
				grid_export = src('exp'), home = function() error('no meter') end } })
			:energy('nobat', 'Bez baterii', { pv = src('pv'), grid = src('grid'),
				disabled = function() return E.off end })
			:energy('own', 'Dom z licznikiem', { pv = src('pv'), grid = src('grid'), home = src('home'), icon = 'sun', span = 3 })
			:energy('inv', 'Odwrotnie', { grid = src('grid'), battery = src('battery'), unit = 'kW',
				invert = { grid = true, battery = true } })
		:build()
`

func newEnergyEngine(t *testing.T) *LuaEngine {
	t.Helper()
	engine := NewLuaEngine()
	t.Cleanup(engine.Close)
	mustExec(t, engine, dashEnergySetup)
	return engine
}

func energyValues(t *testing.T, engine *LuaEngine) map[string]map[string]interface{} {
	t.Helper()
	var vals map[string]map[string]interface{}
	dashJSON(t, engine, `Dashboard.API.getValues("en")`, &vals)
	return vals
}

func energyNode(v map[string]interface{}, name string) map[string]interface{} {
	nodes, _ := v["nodes"].(map[string]interface{})
	n, _ := nodes[name].(map[string]interface{})
	return n
}

// flows as a map; an empty Lua table comes as [] and gives an empty map
func energyFlows(v map[string]interface{}) map[string]interface{} {
	f, _ := v["flows"].(map[string]interface{})
	if f == nil {
		f = map[string]interface{}{}
	}
	return f
}

func wantFlows(t *testing.T, what string, v map[string]interface{}, want map[string]float64) {
	t.Helper()
	got := energyFlows(v)
	w := map[string]interface{}{}
	for k, p := range want {
		w[k] = p
	}
	if !reflect.DeepEqual(got, w) {
		t.Errorf("%s flows = %v, want %v", what, got, w)
	}
}

func TestDashboardEnergyDefinition(t *testing.T) {
	engine := newEnergyEngine(t)
	var def dashDef
	dashJSON(t, engine, `Dashboard.API.getDefinition("en")`, &def)

	all := def.widget("all")
	if all["type"] != "energy" || all["icon"] != "lightning-charge" || all["label"] != "Energia" {
		t.Errorf("all = %v", all)
	}
	if _, ok := all["span"]; ok {
		t.Errorf("all has a span of its own: %v", all["span"])
	}
	if got := all["nodeList"]; !reflect.DeepEqual(got, []interface{}{"pv", "grid", "battery", "home"}) {
		t.Errorf("all nodeList = %v", got)
	}
	if got := all["todayList"]; !reflect.DeepEqual(got, []interface{}{"pv", "grid_import", "grid_export", "home"}) {
		t.Errorf("all todayList = %v", got)
	}
	// 3000 W of PV: 800 into the battery, 1200 out to the grid, 1000 for the home
	if n := energyNode(all, "pv"); n["power"] != float64(3000) {
		t.Errorf("pv = %v", n)
	}
	if n := energyNode(all, "battery"); n["power"] != float64(800) || n["soc"] != float64(55) {
		t.Errorf("battery = %v", n)
	}
	if n := energyNode(all, "home"); n["power"] != float64(1000) || n["computed"] != true {
		t.Errorf("home = %v", n)
	}
	wantFlows(t, "all", all, map[string]float64{"pv_home": 1000, "pv_grid": 1200, "pv_battery": 800,
		"grid_home": 0, "grid_battery": 0, "battery_home": 0})
	// rounded to 0.1 kWh; the failing home counter is left out
	if got := all["today"]; !reflect.DeepEqual(got, map[string]interface{}{"pv": 12.3, "grid_import": 3.1, "grid_export": 8.2}) {
		t.Errorf("today = %v", got)
	}

	own := def.widget("own")
	if own["icon"] != "sun" || own["span"] != float64(3) {
		t.Errorf("own = %v", own)
	}
	if n := energyNode(own, "home"); n["power"] != float64(950) || n["computed"] != nil {
		t.Errorf("own home = %v", n)
	}
}

func TestDashboardEnergyFlows(t *testing.T) {
	engine := newEnergyEngine(t)
	cases := []struct {
		name              string
		pv, grid, battery float64
		home              float64
		flows             map[string]float64
	}{
		// night: the battery and the grid feed the home
		{"night", 0, 400, -600, 1000, map[string]float64{"pv_home": 0, "pv_grid": 0, "pv_battery": 0,
			"grid_home": 400, "grid_battery": 0, "battery_home": 600}},
		// the battery gives more than the home takes: the rest goes to the grid
		{"battery to grid", 0, -300, -1000, 700, map[string]float64{"pv_home": 0, "pv_grid": 0, "pv_battery": 0,
			"grid_home": 0, "grid_battery": -300, "battery_home": 700}},
		// cheap tariff: the grid charges the battery
		{"grid to battery", 0, 2000, 1500, 500, map[string]float64{"pv_home": 0, "pv_grid": 0, "pv_battery": 0,
			"grid_home": 500, "grid_battery": 1500, "battery_home": 0}},
		// PV short of the home's need, the grid adds to it
		{"cloudy", 600, 900, 0, 1500, map[string]float64{"pv_home": 600, "pv_grid": 0, "pv_battery": 0,
			"grid_home": 900, "grid_battery": 0, "battery_home": 0}},
		// a balance below zero (readings out of step) is a home of 0
		{"out of step", 1000, -1500, 0, 0, map[string]float64{"pv_home": 0, "pv_grid": 1000, "pv_battery": 0,
			"grid_home": 0, "grid_battery": 0, "battery_home": 0}},
	}
	for _, c := range cases {
		mustExec(t, engine, `E.pv = `+strconv.FormatFloat(c.pv, 'f', -1, 64)+`; E.grid = `+strconv.FormatFloat(c.grid, 'f', -1, 64)+`; E.battery = `+strconv.FormatFloat(c.battery, 'f', -1, 64))
		v := energyValues(t, engine)["all"]
		if n := energyNode(v, "home"); n["power"] != c.home {
			t.Errorf("%s: home = %v, want %v", c.name, n, c.home)
		}
		wantFlows(t, c.name, v, c.flows)
	}
}

// invert flips grid and battery; unit kW scales to W
func TestDashboardEnergyInvertAndUnit(t *testing.T) {
	engine := newEnergyEngine(t)
	// the meter says +1.2 kW given to the grid, the battery +0.5 kW discharging
	mustExec(t, engine, `E.grid = 1.2; E.battery = 0.5`)
	v := energyValues(t, engine)["inv"]
	if n := energyNode(v, "grid"); n["power"] != float64(-1200) {
		t.Errorf("inv grid = %v", n)
	}
	if n := energyNode(v, "battery"); n["power"] != float64(-500) {
		t.Errorf("inv battery = %v", n)
	}
	if n := energyNode(v, "pv"); n != nil {
		t.Errorf("inv has a pv node: %v", n)
	}
	// 500 W from the battery goes to the grid; 1200 out is more than that, so the home is 0
	if n := energyNode(v, "home"); n["power"] != float64(0) {
		t.Errorf("inv home = %v", n)
	}
	wantFlows(t, "inv", v, map[string]float64{"grid_home": 0, "grid_battery": -500, "battery_home": 0})
}

// A node without its function is left out with its lines and today's counters
func TestDashboardEnergyMissingNodes(t *testing.T) {
	engine := newEnergyEngine(t)
	var def dashDef
	dashJSON(t, engine, `Dashboard.API.getDefinition("en")`, &def)
	nobat := def.widget("nobat")
	if got := nobat["nodeList"]; !reflect.DeepEqual(got, []interface{}{"pv", "grid", "home"}) {
		t.Errorf("nobat nodeList = %v", got)
	}
	if n := energyNode(nobat, "battery"); n != nil {
		t.Errorf("nobat battery = %v", n)
	}
	if n := energyNode(nobat, "home"); n["power"] != float64(1800) {
		t.Errorf("nobat home = %v", n)
	}
	wantFlows(t, "nobat", nobat, map[string]float64{"pv_home": 1800, "pv_grid": 1200, "grid_home": 0})

	mustExec(t, engine, `E.off = true`)
	if v := energyValues(t, engine)["nobat"]; v["disabled"] != true {
		t.Errorf("nobat disabled = %v", v["disabled"])
	}
}

// A failing or odd source shows on its node only; home from the balance is unknown then
func TestDashboardEnergyFailingSource(t *testing.T) {
	engine := newEnergyEngine(t)
	checks := []struct {
		grid, want string
	}{
		{`function() error('meter down') end`, "meter down"},
		{`function() error({ code = 5 }) end`, "table"},
		{`function() return 'abc' end`, "expected a number, got string"},
		{`function() return '0x10' end`, "expected a number, got string"},
		{`function() return '1e3' end`, "expected a number, got string"},
		{`function() return 0/0 end`, "expected a finite number"},
		{`function() return 1/0 end`, "expected a finite number"},
		{`function() return true end`, "expected a number, got boolean"},
	}
	for _, c := range checks {
		mustExec(t, engine, `_dashboards.en._sections[1].widgets[1].opts.grid = `+c.grid)
		v := energyValues(t, engine)["all"]
		g := energyNode(v, "grid")
		e, _ := g["error"].(string)
		if !strings.Contains(e, `section:energy("all"): grid: `) || !strings.Contains(e, c.want) || g["power"] != nil {
			t.Errorf("%s: grid = %v", c.grid, g)
		}
		if n := energyNode(v, "pv"); n["power"] != float64(3000) {
			t.Errorf("%s: pv = %v", c.grid, n)
		}
		if n := energyNode(v, "home"); n["none"] != true || n["power"] != nil {
			t.Errorf("%s: home = %v", c.grid, n)
		}
		// lines to the grid and the unknown home carry nothing; PV to the battery still does
		wantFlows(t, c.grid, v, map[string]float64{"pv_battery": 800})
	}

	// a decimal string is a number; nil is no reading, not an error
	mustExec(t, engine, `_dashboards.en._sections[1].widgets[1].opts.grid = function() return ' -1200.4 ' end`)
	if n := energyNode(energyValues(t, engine)["all"], "grid"); n["power"] != float64(-1200) {
		t.Errorf("decimal string: grid = %v", n)
	}
	mustExec(t, engine, `_dashboards.en._sections[1].widgets[1].opts.grid = function() return nil end`)
	v := energyValues(t, engine)["all"]
	if n := energyNode(v, "grid"); n["none"] != true || n["error"] != nil {
		t.Errorf("nil: grid = %v", n)
	}

	// a failing SoC leaves the battery's power
	mustExec(t, engine, `_dashboards.en._sections[1].widgets[1].opts.batterySoc = function() error('bms') end`)
	b := energyNode(energyValues(t, engine)["all"], "battery")
	if e, _ := b["socError"].(string); b["power"] != float64(800) || b["soc"] != nil || !strings.Contains(e, "batterySoc: ") {
		t.Errorf("failing soc: battery = %v", b)
	}
	mustExec(t, engine, `_dashboards.en._sections[1].widgets[1].opts.batterySoc = function() return 140 end`)
	if b := energyNode(energyValues(t, engine)["all"], "battery"); b["soc"] != float64(100) {
		t.Errorf("soc 140: battery = %v", b)
	}

	// the page stays: the other tiles are unaffected
	if n := energyNode(energyValues(t, engine)["own"], "grid"); n["power"] != float64(-1200) {
		t.Errorf("own grid = %v", n)
	}
}

func TestDashboardEnergyArgumentErrors(t *testing.T) {
	fn := `function() return 0 end`
	cases := []struct{ call, want string }{
		{`:section('S', 'grid'):energy('', 'L', { pv = ` + fn + ` })`, "id must be"},
		{`:section('S', 'grid'):energy('a', nil, { pv = ` + fn + ` })`, "label must be"},
		{`:section('S', 'grid'):energy('a', 'L')`, "opts must be a table"},
		{`:section('S', 'grid'):energy('a', 'L', {})`, "at least one of opts.pv"},
		{`:section('S', 'grid'):energy('a', 'L', { home = ` + fn + ` })`, "at least one of opts.pv"},
		{`:section('S', 'grid'):energy('a', 'L', { pv = ` + fn + `, solar = 1 })`, `unknown option "solar"`},
		{`:section('S', 'grid'):energy('a', 'L', { pv = 1500 })`, "opts.pv must be a function, got number"},
		{`:section('S', 'grid'):energy('a', 'L', { pv = ` + fn + `, home = 'x' })`, "opts.home must be a function"},
		{`:section('S', 'grid'):energy('a', 'L', { pv = ` + fn + `, batterySoc = ` + fn + ` })`, "batterySoc needs opts.battery"},
		{`:section('S', 'grid'):energy('a', 'L', { pv = ` + fn + `, unit = 'MW' })`, `opts.unit must be "W" or "kW"`},
		{`:section('S', 'grid'):energy('a', 'L', { pv = ` + fn + `, invert = true })`, "opts.invert must be a table"},
		{`:section('S', 'grid'):energy('a', 'L', { pv = ` + fn + `, invert = { pv = true } })`, `opts.invert: unknown key "pv"`},
		{`:section('S', 'grid'):energy('a', 'L', { grid = ` + fn + `, invert = { grid = 1 } })`, "opts.invert.grid must be true or false"},
		{`:section('S', 'grid'):energy('a', 'L', { pv = ` + fn + `, today = 5 })`, "opts.today must be a table"},
		{`:section('S', 'grid'):energy('a', 'L', { pv = ` + fn + `, today = { battery = ` + fn + ` } })`, `opts.today: unknown key "battery"`},
		{`:section('S', 'grid'):energy('a', 'L', { pv = ` + fn + `, today = { pv = 12 } })`, "opts.today.pv must be a function"},
		{`:section('S', 'grid'):energy('a', 'L', { pv = ` + fn + `, today = { grid_import = ` + fn + ` } })`, "opts.today.grid_import needs opts.grid"},
		{`:section('S', 'grid'):energy('a', 'L', { pv = ` + fn + `, icon = 'x y' })`, "opts.icon"},
		{`:section('S', 'grid'):energy('a', 'L', { pv = ` + fn + `, disabled = true })`, "opts.disabled"},
		{`:section('S', 'grid'):energy('a', 'L', { pv = ` + fn + `, span = 1.5 })`, "opts.span must be a whole number"},
		{`:header():energy('a', 'L', { pv = ` + fn + ` })`, "not the header"},
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
		if !strings.Contains(msg, "<string>:3: ") || !strings.Contains(msg, "section:energy") || !strings.Contains(msg, c.want) {
			t.Errorf("%s: %s", c.call, msg)
		}
	}
	// the builder's own method too
	engine := NewLuaEngine()
	defer engine.Close()
	_, err := engine.Execute("_dashboards = {}\nlocal ok, err = pcall(function()\n  local b = Dashboard.new('f'); b:section('S', 'g')\n  b:energy('a', 'L', { pv = 5 })\nend)\nerror(err, 0)")
	if err == nil || !strings.Contains(err.Error(), "<string>:4: ") || !strings.Contains(err.Error(), "section:energy") {
		t.Errorf("builder energy: %v", err)
	}
}

// PV without a reading (an inverter asleep) is 0 in the balance; its node and lines show nothing
func TestDashboardEnergyPVAsleep(t *testing.T) {
	engine := newEnergyEngine(t)
	mustExec(t, engine, `E.pv = nil; E.grid = 3000; E.battery = 2000`)
	v := energyValues(t, engine)["all"]
	if n := energyNode(v, "pv"); n["none"] != true || n["power"] != nil {
		t.Errorf("pv = %v", n)
	}
	if n := energyNode(v, "home"); n["power"] != float64(1000) || n["computed"] != true {
		t.Errorf("home = %v", n)
	}
	wantFlows(t, "pv asleep", v, map[string]float64{"grid_home": 1000, "grid_battery": 2000, "battery_home": 0})

	// a PV that fails still makes the home unknown
	mustExec(t, engine, `_dashboards.en._sections[1].widgets[1].opts.pv = function() error('dongle') end`)
	if n := energyNode(energyValues(t, engine)["all"], "home"); n["none"] != true {
		t.Errorf("failing pv: home = %v", n)
	}
}

// A measured home: the lines into it are scaled to its value, the others stay
func TestDashboardEnergyMeasuredHome(t *testing.T) {
	engine := newEnergyEngine(t)
	// balance 1800 W from PV; the meter says 950
	wantFlows(t, "own", energyValues(t, engine)["own"], map[string]float64{"pv_home": 950, "pv_grid": 1200, "grid_home": 0})
	// balance 600 from PV and 900 from the grid; the meter says 1000
	mustExec(t, engine, `E.pv = 600; E.grid = 900; E.home = 1000`)
	v := energyValues(t, engine)["own"]
	if n := energyNode(v, "home"); n["power"] != float64(1000) {
		t.Errorf("own home = %v", n)
	}
	wantFlows(t, "own scaled", v, map[string]float64{"pv_home": 400, "pv_grid": 0, "grid_home": 600})
}

// "1,5" is a number as "1.5" is; thousands separators and two commas are not
func TestDashboardEnergyDecimalComma(t *testing.T) {
	engine := newEnergyEngine(t)
	cases := map[string]float64{`'1,5'`: 1500, `' -0,25 '`: -250}
	bad := []string{`'1,500,0'`, `'1.500,5'`, `',5'`, `'1 500'`}
	set := `_dashboards.en._sections[1].widgets[4].opts.grid = function() return `
	for in, want := range cases {
		mustExec(t, engine, set+in+` end`)
		// inv flips the sign and takes kW
		if n := energyNode(energyValues(t, engine)["inv"], "grid"); n["power"] != -want {
			t.Errorf("%s: grid = %v, want %v", in, n, -want)
		}
	}
	for _, in := range bad {
		mustExec(t, engine, set+in+` end`)
		if n := energyNode(energyValues(t, engine)["inv"], "grid"); n["error"] == nil {
			t.Errorf("%s: grid = %v, want an error", in, n)
		}
	}
}

// Readings out of step: what a node gives out never exceeds what it has, no line is negative
func TestDashboardEnergyConservation(t *testing.T) {
	engine := newEnergyEngine(t)
	cases := [][3]float64{
		{1000, -1500, 0}, {0, -800, 500}, {500, 2000, -3000}, {3000, -4000, -1000},
		{2000, 1000, 5000}, {0, 0, -700}, {4000, -100, 300}, {100, 50, 0},
	}
	num := func(f float64) string { return strconv.FormatFloat(f, 'f', -1, 64) }
	for _, c := range cases {
		mustExec(t, engine, `E.pv = `+num(c[0])+`; E.grid = `+num(c[1])+`; E.battery = `+num(c[2]))
		f := energyFlows(energyValues(t, engine)["all"])
		g := func(k string) float64 { v, _ := f[k].(float64); return v }
		for k, v := range f {
			if k != "grid_battery" && v.(float64) < 0 {
				t.Errorf("%v: %s = %v", c, k, v)
			}
		}
		pv, imp, exp := c[0], max(c[1], 0), max(-c[1], 0)
		chg, dis := max(c[2], 0), max(-c[2], 0)
		gb := g("grid_battery")
		if s := g("pv_home") + g("pv_grid") + g("pv_battery"); s > pv {
			t.Errorf("%v: PV gives %v of %v", c, s, pv)
		}
		if s := g("grid_home") + max(gb, 0); s > imp {
			t.Errorf("%v: grid gives %v of %v", c, s, imp)
		}
		if s := g("pv_grid") + max(-gb, 0); s > exp {
			t.Errorf("%v: grid takes %v of %v", c, s, exp)
		}
		if s := g("battery_home") + max(-gb, 0); s > dis {
			t.Errorf("%v: battery gives %v of %v", c, s, dis)
		}
		if s := g("pv_battery") + max(gb, 0); s > chg {
			t.Errorf("%v: battery takes %v of %v", c, s, chg)
		}
	}
}
