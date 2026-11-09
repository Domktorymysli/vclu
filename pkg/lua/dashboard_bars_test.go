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
			:bars('kw', 'Kwartal', 'energia:licznik', { range = '90d', agg = 'delta' })
			:bars('rok', 'Rok', 'energia:licznik', { range = '365d', agg = 'delta' })
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
	if def.widget("kw")["bucket"] != "1w" || def.widget("rok")["bucket"] != "1mo" {
		t.Errorf("a quarter defaults to weeks, a year to months: %v %v", def.widget("kw"), def.widget("rok"))
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
		`Dashboard.API.barsSpec("bars", "kw")`:   13,
		`Dashboard.API.barsSpec("bars", "rok")`:  12,
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
		`:bars('a', 'L', nil)`:                                   "expected a metric path",
		`:bars('a', 'L', 'm', { agg = 'median' })`:               "agg must be one of",
		`:bars('a', 'L', 'm', { bucket = '15m' })`:               `bucket must be "1h", "1d", "1w" or "1mo"`,
		`:bars('a', 'L', 'm', { range = '400d' })`:               "range must be a duration up to 366d",
		`:bars('a', 'L', 'm', { range = '60d', bucket = '1h' })`: "1440 bars, at most 744",
		`:bars('a', 'L', 'm', { range = 'week' })`:               "range must be a duration",
		`:bars('a', 'L', 'm', { range = '0d' })`:                 "range must be a duration",
		`:bars('a', 'L', 'm', { range = '0h' })`:                 "range must be a duration",
		`:bars('a', 'L', 'm', { refresh = 5 })`:                  "refresh must be a number of seconds, at least 30",
		`:bars('a', 'L', 'm', { refresh = '60' })`:               "refresh must be a number of seconds, at least 30",
		`:bars('a', 'L', 'm', { compare = 5 })`:                  "compare expects a second metric path",
		`:bars('a', 'L', 'm', { decimals = 9 })`:                 "opts.decimals must be a whole number 0-6",
		`:bars('a', 'L', 'm', { aggregate = 'sum' })`:            `unknown option "aggregate"`,
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

// A chart with ranges sends them to the page, starts on the first (or range), and lets the
// server serve each of them.
func TestDashboardChartRanges(t *testing.T) {
	engine := NewLuaEngine()
	defer engine.Close()
	mustExec(t, engine, `
		_dashboards = {}
		Dashboard.new('ch'):auth('public'):section('S', 'grid')
			:chart('moc', 'Moc', 'moc:dom', { ranges = {'24h', '7d', '30d', '365d'} })
			:chart('t', 'Temp', 'temp:salon', { ranges = {'24h', '90d'}, range = '90d' })
			:chart('p', 'Plain', 'temp:pokoj', { range = '7d' })
			:build()`)
	var def dashDef
	dashJSON(t, engine, `Dashboard.API.getDefinition("ch")`, &def)
	if w := def.widget("moc"); w["range"] != "24h" || len(w["ranges"].([]any)) != 4 {
		t.Errorf("moc = %v", w)
	}
	if w := def.widget("t"); w["range"] != "90d" {
		t.Errorf("t = %v", w)
	}
	if w := def.widget("p"); w["range"] != "7d" || w["ranges"] != nil {
		t.Errorf("p = %v", w)
	}
	if res := mustExec(t, engine, `return Dashboard.API.usesMetric("ch", "moc:dom")`); !strings.Contains(res, `"365d"`) {
		t.Errorf("usesMetric = %s", res)
	}
	for call, msg := range map[string]string{
		`:chart('a', 'L', 'm', { ranges = {} })`:                           "ranges must be a list of 1 to 6",
		`:chart('a', 'L', 'm', { ranges = {'24h', 'rok'} })`:               "range rok is not a duration up to 366d",
		`:chart('a', 'L', 'm', { ranges = {'400d'} })`:                     "range 400d is not a duration up to 366d",
		`:chart('a', 'L', 'm', { ranges = {'24h', '7d'}, range = '30d' })`: `range "30d" is not one of ranges`,
	} {
		e := NewLuaEngine()
		res := mustExec(t, e, "_dashboards = {}\nlocal ok, err = pcall(function()\n  Dashboard.new('e'):section('S', 'grid')\n    "+call+"\nend) return tostring(err)")
		e.Close()
		if !strings.Contains(res, msg) || !strings.Contains(res, "<string>:") {
			t.Errorf("%s: %s", call, res)
		}
	}
}
