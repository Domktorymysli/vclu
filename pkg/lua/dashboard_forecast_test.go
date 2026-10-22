package lua

import (
	"strings"
	"testing"
	"time"
)

// Weather forecast tile (#96): days from today in the house's zone, hours from now, rounded
// values, sanitised icons, nil as "no forecast", an error as the tile's error.

// 00:30 on 23 October in Warsaw, still the 22nd in UTC: "today" must come from the house's zone
var forecastNow = time.Date(2026, 10, 22, 22, 30, 0, 0, time.UTC)

const dashForecastSetup = `
	_dashboards = {}
	datetime.setTimezone('Europe/Warsaw')
	local now = math.floor(__go_time_ms() / 1000)
	F = { fail = false }
	F.data = {
		updated = now - 600,
		days = {
			{ date = '2026-10-24', iconName = 'cloud-rain', min = 4.26, max = 12.04, rain = 2.345, pop = 0.426 },
			{ date = '2026-10-22', iconName = 'sun', min = 1, max = 2, rain = 0, pop = 0 },   -- yesterday in Warsaw
			{ date = '2026-10-23', icon = 'sun', min = 3, max = 11.96, rain = 0, pop = 0 },     -- icon as the alias
			{ date = '2026-10-25', iconName = '"><img src=x>', min = -2.36, max = 5, rain = -1, pop = 1.7 },
			{ date = '2026-10-26', min = 0, max = 6 },
			{ date = '2026-10-27', iconName = 'snow', min = -5, max = -1, rain = 0.04, pop = 0.2 },
			{ date = '2026-10-28', iconName = 'clouds', min = 2, max = 7 },
			{ date = 'jutro', iconName = 'sun', min = 2, max = 7 },
			'not a day',
		},
		hours = {
			{ at = now + 3 * 3600, temp = 6.04, rain = 0.26, iconName = 'cloud' },
			{ at = now - 2 * 3600, temp = 9, rain = 0 },               -- past
			{ at = now - 1800, temp = 7.46 },                          -- the current hour, no rain
			{ at = now + 24 * 3600, temp = 4, rain = 1 },
			{ at = now + 25 * 3600, temp = 3, rain = 0 },              -- past the 24 h
			{ at = 'soon', temp = 3 },
		},
	}
	local function src()
		if F.fail then error('forecast gone') end
		if F.bad then return 'sunny' end
		return F.data
	end
	-- like @vclu/weather: methods on the instance, called with ':'
	W = { _forecast = nil }
	function W:getForecast() return self._forecast end
	function W:getData() return { iconName = 'sun' } end
	Dashboard.new('fc')
		:section('Weather', 'cloud-sun')
			:forecast('fc', 'Pogoda', src)
			:forecast('short', 'Short', src, { days = 2, hours = 6, unit = '°F', icon = 'umbrella', span = 3,
				disabled = function() return F.off == true end })
			:forecast('nochart', 'No chart', src, { hours = 0 })
			:forecast('owm', 'OWM', W)
		:build()
`

func newForecastEngine(t *testing.T) *LuaEngine {
	t.Helper()
	engine := NewLuaEngine()
	t.Cleanup(engine.Close)
	SetClock(engine.state, fixedClock{forecastNow})
	mustExec(t, engine, dashForecastSetup)
	return engine
}

func forecastValues(t *testing.T, engine *LuaEngine) map[string]map[string]interface{} {
	t.Helper()
	var vals map[string]map[string]interface{}
	dashJSON(t, engine, `Dashboard.API.getValues("fc")`, &vals)
	return vals
}

func list(v interface{}) []map[string]interface{} {
	arr, _ := v.([]interface{})
	out := make([]map[string]interface{}, 0, len(arr))
	for _, e := range arr {
		m, _ := e.(map[string]interface{})
		out = append(out, m)
	}
	return out
}

func dates(days []map[string]interface{}) string {
	var s []string
	for _, d := range days {
		s = append(s, d["date"].(string))
	}
	return strings.Join(s, ",")
}

func TestDashboardForecastDefinition(t *testing.T) {
	engine := newForecastEngine(t)
	var def dashDef
	dashJSON(t, engine, `Dashboard.API.getDefinition("fc")`, &def)

	fc := def.widget("fc")
	if fc["type"] != "forecast" || fc["icon"] != "cloud-sun" || fc["label"] != "Pogoda" || fc["unit"] != "°C" || fc["chartHours"] != float64(24) {
		t.Errorf("fc = %v", fc)
	}
	if _, ok := fc["span"]; ok {
		t.Errorf("fc has a span of its own: %v", fc["span"])
	}
	// past days and dates that are not dates are dropped, the rest in order, five by default
	days := list(fc["days"])
	if got := dates(days); got != "2026-10-23,2026-10-24,2026-10-25,2026-10-26,2026-10-27" {
		t.Fatalf("fc days = %s", got)
	}
	want := []map[string]interface{}{
		{"date": "2026-10-23", "icon": "sun", "min": float64(3), "max": float64(12), "rain": float64(0), "pop": float64(0)},
		{"date": "2026-10-24", "icon": "cloud-rain", "min": 4.3, "max": float64(12), "rain": 2.3, "pop": float64(43)},
		{"date": "2026-10-25", "icon": "cloud", "min": -2.4, "max": float64(5), "rain": float64(0), "pop": float64(100)},
		{"date": "2026-10-26", "icon": "cloud", "min": float64(0), "max": float64(6)},
		{"date": "2026-10-27", "icon": "snow", "min": float64(-5), "max": float64(-1), "rain": float64(0), "pop": float64(20)},
	}
	for i, w := range want {
		if len(days[i]) != len(w) {
			t.Errorf("day %d = %v, want %v", i, days[i], w)
			continue
		}
		for k, v := range w {
			if days[i][k] != v {
				t.Errorf("day %d %s = %v, want %v", i, k, days[i][k], v)
			}
		}
	}

	// hours from the current one to 24 h ahead, in order, without icons
	hours := list(fc["hours"])
	now := float64(forecastNow.Unix())
	wantH := []map[string]interface{}{
		{"at": now - 1800, "temp": 7.5, "rain": float64(0)},
		{"at": now + 3*3600, "temp": float64(6), "rain": 0.3},
		{"at": now + 24*3600, "temp": float64(4), "rain": float64(1)},
	}
	if len(hours) != len(wantH) {
		t.Fatalf("fc hours = %v", hours)
	}
	for i, w := range wantH {
		if len(hours[i]) != len(w) {
			t.Errorf("hour %d = %v, want %v", i, hours[i], w)
		}
		for k, v := range w {
			if hours[i][k] != v {
				t.Errorf("hour %d %s = %v, want %v", i, k, hours[i][k], v)
			}
		}
	}
	if fc["updated"] != now-600 {
		t.Errorf("fc updated = %v", fc["updated"])
	}

	short := def.widget("short")
	if short["icon"] != "umbrella" || short["unit"] != "°F" || short["span"] != float64(3) || short["chartHours"] != float64(6) {
		t.Errorf("short = %v", short)
	}
	if got := dates(list(short["days"])); got != "2026-10-23,2026-10-24" {
		t.Errorf("short days = %s", got)
	}
	if h := list(short["hours"]); len(h) != 2 || h[1]["at"] != now+3*3600 {
		t.Errorf("short hours = %v", h)
	}
	nochart := def.widget("nochart")
	if h := list(nochart["hours"]); len(h) != 0 || nochart["chartHours"] != float64(0) || len(list(nochart["days"])) != 5 {
		t.Errorf("nochart = %v", nochart)
	}
}

// Past days drop off as the clock moves; the values follow the source
func TestDashboardForecastFollowsClock(t *testing.T) {
	engine := newForecastEngine(t)
	SetClock(engine.state, fixedClock{forecastNow.Add(48 * time.Hour)})
	vals := forecastValues(t, engine)
	if got := dates(list(vals["fc"]["days"])); got != "2026-10-25,2026-10-26,2026-10-27,2026-10-28" {
		t.Errorf("two days later fc days = %s", got)
	}
	if h := list(vals["fc"]["hours"]); len(h) != 0 {
		t.Errorf("two days later fc hours = %v", h)
	}
	if v := vals["short"]; v["disabled"] != false {
		t.Errorf("short disabled = %v", v["disabled"])
	}
}

// nil before the first fetch is "no forecast"; an error or a wrong type is the tile's error
func TestDashboardForecastNoneAndError(t *testing.T) {
	engine := newForecastEngine(t)
	var def dashDef
	dashJSON(t, engine, `Dashboard.API.getDefinition("fc")`, &def)
	if owm := def.widget("owm"); owm["none"] != true || owm["days"] != nil || owm["error"] != nil {
		t.Errorf("owm before a fetch = %v", owm)
	}
	vals := forecastValues(t, engine)
	if v := vals["owm"]; len(v) != 2 || v["none"] != true || v["disabled"] != false {
		t.Errorf("owm values = %v", v)
	}

	mustExec(t, engine, `F.fail = true; F.off = true`)
	vals = forecastValues(t, engine)
	if v := vals["fc"]; !strings.Contains(v["error"].(string), "forecast gone") || v["days"] != nil {
		t.Errorf("fc after an error = %v", v)
	}
	if v := vals["short"]; v["disabled"] != true || v["error"] == nil {
		t.Errorf("short after an error = %v", v)
	}
	def = dashDef{}
	dashJSON(t, engine, `Dashboard.API.getDefinition("fc")`, &def)
	if e, _ := def.widget("fc")["error"].(string); !strings.Contains(e, "forecast gone") {
		t.Errorf("fc definition after an error = %v", def.widget("fc"))
	}

	mustExec(t, engine, `F.fail = false; F.bad = true`)
	vals = forecastValues(t, engine)
	if e, _ := vals["fc"]["error"].(string); !strings.Contains(e, `section:forecast("fc")`) || !strings.Contains(e, "got string") {
		t.Errorf("fc returning a string = %v", vals["fc"])
	}
}

// The Weather plugin is recognised by getForecast and called as a method
func TestDashboardForecastWeatherPlugin(t *testing.T) {
	engine := newForecastEngine(t)
	mustExec(t, engine, `W._forecast = { days = { { date = '2026-10-23', iconName = 'cloud-sun', min = 5.56, max = 13.3, rain = 0.4, pop = 0.6 } },
		hours = { { at = math.floor(__go_time_ms() / 1000) + 3600, temp = 9.96, rain = 0.1, iconName = 'cloud-sun' } }, updated = 1 }`)
	vals := forecastValues(t, engine)
	v := vals["owm"]
	days, hours := list(v["days"]), list(v["hours"])
	if len(days) != 1 || days[0]["icon"] != "cloud-sun" || days[0]["min"] != 5.6 || days[0]["pop"] != float64(60) {
		t.Errorf("owm days = %v", days)
	}
	if len(hours) != 1 || hours[0]["temp"] != float64(10) || v["none"] != nil {
		t.Errorf("owm = %v", v)
	}
}

// Wrong arguments fail at the module's line and name section:forecast.
func TestDashboardForecastArgumentErrors(t *testing.T) {
	fn := `function() end`
	cases := []struct{ call, want string }{
		{`:section('S', 'grid'):forecast('', 'L', ` + fn + `)`, "id must be"},
		{`:section('S', 'grid'):forecast('a', nil, ` + fn + `)`, "label must be"},
		{`:section('S', 'grid'):forecast('a', 'L', nil)`, "src must be a function"},
		{`:section('S', 'grid'):forecast('a', 'L', { getData = function() end })`, "src must be a function"},
		{`:section('S', 'grid'):forecast('a', 'L', ` + fn + `, 'x')`, "opts must be a table"},
		{`:section('S', 'grid'):forecast('a', 'L', ` + fn + `, { day = 3 })`, `unknown option "day"`},
		{`:section('S', 'grid'):forecast('a', 'L', ` + fn + `, { days = 0 })`, "opts.days must be a whole number 1-7"},
		{`:section('S', 'grid'):forecast('a', 'L', ` + fn + `, { days = 8 })`, "opts.days"},
		{`:section('S', 'grid'):forecast('a', 'L', ` + fn + `, { days = 2.5 })`, "opts.days"},
		{`:section('S', 'grid'):forecast('a', 'L', ` + fn + `, { hours = 49 })`, "opts.hours must be a whole number 0-48"},
		{`:section('S', 'grid'):forecast('a', 'L', ` + fn + `, { hours = '24' })`, "opts.hours"},
		{`:section('S', 'grid'):forecast('a', 'L', ` + fn + `, { unit = 5 })`, "opts.unit"},
		{`:section('S', 'grid'):forecast('a', 'L', ` + fn + `, { icon = 'x y' })`, "opts.icon"},
		{`:section('S', 'grid'):forecast('a', 'L', ` + fn + `, { disabled = true })`, "opts.disabled"},
		{`:header():forecast('a', 'L', ` + fn + `)`, "not the header"},
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
		if !strings.Contains(msg, "<string>:3: ") || !strings.Contains(msg, "section:forecast") || !strings.Contains(msg, c.want) {
			t.Errorf("%s: %s", c.call, msg)
		}
	}
	// the builder's own method too
	engine := NewLuaEngine()
	defer engine.Close()
	_, err := engine.Execute("_dashboards = {}\nlocal ok, err = pcall(function()\n  local b = Dashboard.new('f'); b:section('S', 'g')\n  b:forecast('a', 'L', {}, { hours = 99 })\nend)\nerror(err, 0)")
	if err == nil || !strings.Contains(err.Error(), "<string>:4: ") || !strings.Contains(err.Error(), "section:forecast") {
		t.Errorf("builder forecast: %v", err)
	}
}
