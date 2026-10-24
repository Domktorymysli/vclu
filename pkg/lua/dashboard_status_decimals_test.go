package lua

import (
	"strings"
	"testing"
)

// opts.decimals of a status reaches the page, which rounds a number to it; the value stays the
// number the function returns.
func TestDashboardStatusDecimals(t *testing.T) {
	engine := NewLuaEngine()
	defer engine.Close()
	mustExec(t, engine, `
		_dashboards = {}
		Dashboard.new('dec')
			:header()
				:status('h', 'Teraz', 'thermometer-half', function() return 21.37 end, '°C', { decimals = 1 })
			:section('S', 'grid')
				:status('t', 'Salon', 'thermometer-half', function() return 21.37 end, '°C', { decimals = 1 })
				:status('w', 'Moc', 'plug', function() return 1245.8 end, 'W', { decimals = 0 })
				:status('raw', 'Bez', 'plug', function() return 3.14159 end, 'x')
			:build()`)
	var def dashDef
	dashJSON(t, engine, `Dashboard.API.getDefinition("dec")`, &def)
	for id, want := range map[string]float64{"h": 1, "t": 1, "w": 0} {
		if w := def.widget(id); w["decimals"] != want || w["value"] == nil {
			t.Errorf("%s = %v, want decimals %v", id, w, want)
		}
	}
	if w := def.widget("raw"); w["decimals"] != nil || w["value"] != 3.14159 {
		t.Errorf("raw = %v, want no decimals", w)
	}
	var vals map[string]map[string]interface{}
	dashJSON(t, engine, `Dashboard.API.getValues("dec")`, &vals)
	if vals["t"]["value"] != 21.37 {
		t.Errorf("values carry the number as returned: %v", vals["t"])
	}
}

func TestDashboardStatusDecimalsRefused(t *testing.T) {
	for _, call := range []string{
		`:section('S', 'grid'):status('a', 'L', 'x', function() return 1 end, 'W', { decimals = 1.5 })`,
		`:section('S', 'grid'):status('a', 'L', 'x', function() return 1 end, 'W', { decimals = -1 })`,
		`:section('S', 'grid'):status('a', 'L', 'x', function() return 1 end, 'W', { decimals = 7 })`,
		`:section('S', 'grid'):status('a', 'L', 'x', function() return 1 end, 'W', { decimals = '1' })`,
		// the builder form: dash:section(...) then dash:status(...)
		"\nlocal b = Dashboard.new('f'); b:section('S', 'grid'); b:status('a', 'L', 'x', function() return 1 end, 'W', { decimals = 9 })",
	} {
		engine := NewLuaEngine()
		_, err := engine.Execute("_dashboards = {}\nlocal ok, err = pcall(function()\n  Dashboard.new('e')" + call + "\nend)\nerror(err, 0)")
		engine.Close()
		if err == nil || !strings.Contains(err.Error(), "opts.decimals must be a whole number 0-6") || !strings.Contains(err.Error(), "<string>:") {
			t.Errorf("%s: %v", call, err)
		}
	}
}
