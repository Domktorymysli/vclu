package lua

import (
	"strings"
	"testing"
)

// A bars widget sends the page how it draws, not which metrics; barsSpec gives the server the
// metrics, the buckets and the house's zone.
func TestDashboardBarsDefinition(t *testing.T) {
	engine := NewLuaEngine()
	defer engine.Close()
	mustExec(t, engine, `
		_dashboards = {}
		datetime.setTimezone('Europe/Warsaw')
		Dashboard.new('bars'):section('Energia', 'lightning')
			:bars('dzien', 'Energia dziennie', 'energia:licznik', { agg = 'delta', unit = 'kWh', decimals = 1,
				compare = 'energia:pv', compareLabel = 'PV', color = 'warning', span = 2, refresh = 120 })
			:bars('def', 'Temperatura', 'temp:salon')
			:bars('doba', 'Moc', 'moc:dom', { range = '24h', agg = 'max' })
			:bars('m', 'Miesiac', 'moc:dom', { range = '30d', bucket = '1d', agg = 'sum' })
			:build()
		local b = Dashboard.new('builder')
		b:section('S', 'grid')
		b:bars('h', 'Godziny', 'moc:dom', { range = '36h', bucket = '1h' })
		b:build()`)
	var def dashDef
	dashJSON(t, engine, `Dashboard.API.getDefinition("bars")`, &def)
	w := def.widget("dzien")
	for k, want := range map[string]any{"type": "bars", "range": "7d", "bucket": "1d", "agg": "delta",
		"unit": "kWh", "decimals": 1.0, "compare": true, "compareLabel": "PV", "span": 2.0, "refresh": 120.0} {
		if w[k] != want {
			t.Errorf("dzien.%s = %v, want %v", k, w[k], want)
		}
	}
	if w["metricPath"] != nil || strings.Contains(mustExec(t, engine, `return Dashboard.API.getDefinition("bars")`), "energia:pv") {
		t.Errorf("the definition names the metrics: %v", w)
	}
	if d := def.widget("def"); d["range"] != "7d" || d["bucket"] != "1d" || d["agg"] != "avg" || d["compare"] != false {
		t.Errorf("defaults = %v", d)
	}
	if d := def.widget("doba"); d["bucket"] != "1h" {
		t.Errorf("a 24h range defaults to hours: %v", d)
	}

	var spec struct {
		Path, Compare, Bucket, Agg, Zone string
		Buckets                          int
	}
	dashJSON(t, engine, `Dashboard.API.barsSpec("bars", "dzien")`, &spec)
	if spec.Path != "energia:licznik" || spec.Compare != "energia:pv" || spec.Bucket != "1d" || spec.Buckets != 7 ||
		spec.Agg != "delta" || spec.Zone != "Europe/Warsaw" {
		t.Errorf("spec = %+v", spec)
	}
	for call, want := range map[string]int{
		`Dashboard.API.barsSpec("bars", "doba")`: 24,
		`Dashboard.API.barsSpec("bars", "m")`:    30,
		`Dashboard.API.barsSpec("builder", "h")`: 36,
	} {
		spec.Buckets = 0
		dashJSON(t, engine, call, &spec)
		if spec.Buckets != want {
			t.Errorf("%s: %d buckets, want %d", call, spec.Buckets, want)
		}
	}
	var fail struct {
		Error  string
		Status int
	}
	for call, status := range map[string]int{
		`Dashboard.API.barsSpec("bars", "nope")`:  403,
		`Dashboard.API.barsSpec("none", "dzien")`: 404,
	} {
		fail.Status = 0
		dashJSON(t, engine, call, &fail)
		if fail.Status != status {
			t.Errorf("%s = %+v, want status %d", call, fail, status)
		}
	}
	// the metric a bars widget aggregates is not readable as raw samples
	if res := mustExec(t, engine, `return Dashboard.API.usesMetric("bars", "energia:licznik")`); !strings.Contains(res, `"used":false`) {
		t.Errorf("usesMetric = %s", res)
	}
}

func TestDashboardBarsRefused(t *testing.T) {
	for call, msg := range map[string]string{
		`:bars('a', 'L', nil)`:                        "expected a metric path",
		`:bars('a', 'L', 'm', { agg = 'median' })`:    "agg must be one of",
		`:bars('a', 'L', 'm', { bucket = '15m' })`:    `bucket must be "1h" or "1d"`,
		`:bars('a', 'L', 'm', { range = '60d' })`:     "range must be a duration up to 31d",
		`:bars('a', 'L', 'm', { range = 'week' })`:    "range must be a duration",
		`:bars('a', 'L', 'm', { range = '0d' })`:      "range must be a duration",
		`:bars('a', 'L', 'm', { range = '0h' })`:      "range must be a duration",
		`:bars('a', 'L', 'm', { refresh = 5 })`:       "refresh must be a number of seconds, at least 30",
		`:bars('a', 'L', 'm', { refresh = '60' })`:    "refresh must be a number of seconds, at least 30",
		`:bars('a', 'L', 'm', { compare = 5 })`:       "compare expects a second metric path",
		`:bars('a', 'L', 'm', { decimals = 9 })`:      "opts.decimals must be a whole number 0-6",
		`:bars('a', 'L', 'm', { aggregate = 'sum' })`: `unknown option "aggregate"`,
	} {
		engine := NewLuaEngine()
		res := mustExec(t, engine, "_dashboards = {}\nlocal ok, err = pcall(function()\n  Dashboard.new('e'):section('S', 'grid')\n    "+call+"\nend) return tostring(err)")
		engine.Close()
		// the module's line, not Dashboard.lua
		if !strings.Contains(res, msg) || !strings.Contains(res, "<string>:") {
			t.Errorf("%s: %s", call, res)
		}
	}
	engine := NewLuaEngine()
	defer engine.Close()
	res := mustExec(t, engine, "_dashboards = {}\nlocal ok, err = pcall(function()\n  local b = Dashboard.new('e')\n  b:section('S', 'grid')\n  b:bars('a', 'L', 'm', { agg = 'x' })\nend) return tostring(err)")
	if !strings.Contains(res, "<string>:5: ") {
		t.Errorf("builder form: %s", res)
	}
}
