package lua

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// The calendar and forecast widgets send the browser the IANA name of the zone the calendar
// expands in (#129): setTimezone's, else TZ, else the /etc/localtime link, else "" (never "Local").

func TestZoneNameOf(t *testing.T) {
	dir := t.TempDir()
	link := func(name, target string) string {
		p := filepath.Join(dir, name)
		if err := os.Symlink(target, p); err != nil {
			t.Fatal(err)
		}
		return p
	}
	linux := link("linux", "/usr/share/zoneinfo/Europe/Warsaw")
	macos := link("macos", "/var/db/timezone/zoneinfo/America/New_York")
	relative := link("relative", "../usr/share/zoneinfo/Asia/Tokyo")
	elsewhere := link("elsewhere", "/opt/zones/warsaw")
	unknown := link("unknown", "/usr/share/zoneinfo/Mars/Olympus")
	file := filepath.Join(dir, "file")
	if err := os.WriteFile(file, []byte("TZif"), 0o644); err != nil {
		t.Fatal(err)
	}
	env := func(tz string, set bool) func(string) (string, bool) {
		return func(k string) (string, bool) {
			if k == "TZ" && set {
				return tz, true
			}
			return "", false
		}
	}

	for _, tc := range []struct {
		name      string
		tz        string
		tzSet     bool
		localtime string
		want      string
	}{
		{"TZ", "Europe/Warsaw", true, macos, "Europe/Warsaw"},
		{"TZ with a colon", ":Asia/Tokyo", true, linux, "Asia/Tokyo"},
		{"TZ as a zoneinfo path", "/usr/share/zoneinfo/Europe/Berlin", true, linux, "Europe/Berlin"},
		{"TZ empty is UTC", "", true, linux, "UTC"},
		{"TZ unknown", "Nowhere/Town", true, linux, ""},
		{"TZ Local", "Local", true, linux, ""},
		{"link on Linux", "", false, linux, "Europe/Warsaw"},
		{"link on macOS", "", false, macos, "America/New_York"},
		{"relative link", "", false, relative, "Asia/Tokyo"},
		{"link outside zoneinfo", "", false, elsewhere, ""},
		{"link to an unknown zone", "", false, unknown, ""},
		{"a copied file", "", false, file, ""},
		{"no localtime", "", false, filepath.Join(dir, "missing"), ""},
	} {
		if got := zoneNameOf(env(tc.tz, tc.tzSet), tc.localtime); got != tc.want {
			t.Errorf("%s: zoneNameOf = %q, want %q", tc.name, got, tc.want)
		}
	}
}

const calendarZoneSetup = `
	_dashboards = {}
	Calendar.new('dom'):clock({ wait = false }):event('sport', { title = 'Sport', every = 'day', time = '18:00' }):build()
	Dashboard.new('z'):section('S', 'i')
		:calendar('kal', 'K', 'dom', { view = 'day' })
		:forecast('fc', 'F', function() return { days = {}, hours = {} } end)
		:build()
`

type zoneAnswer struct {
	Zone  string `json:"zone"`
	Items []struct {
		At float64 `json:"at"`
	} `json:"items"`
}

func zoneAnswers(t *testing.T, engine *LuaEngine) (zoneAnswer, string) {
	t.Helper()
	var rng zoneAnswer
	dashJSON(t, engine, `Dashboard.API.calendarRange("z", "kal", "2026-10-26", "2026-10-26")`, &rng)
	var def dashDef
	dashJSON(t, engine, `Dashboard.API.getDefinition("z")`, &def)
	fz, _ := def.widget("fc")["zone"].(string)
	return rng, fz
}

// A server in Tokyo without setTimezone: the 18:00 event is 18:00 in the zone the browser gets.
func TestCalendarZoneWithoutSetTimezone(t *testing.T) {
	tokyo, err := time.LoadLocation("Asia/Tokyo")
	if err != nil {
		t.Fatal(err)
	}
	defer func(l *time.Location) { time.Local = l }(time.Local)
	time.Local = tokyo
	t.Setenv("TZ", "Asia/Tokyo")
	defer func(f func() string) { localZoneName = f }(localZoneName)
	localZoneName = sync.OnceValue(func() string { return zoneNameOf(os.LookupEnv, "/etc/localtime") })

	engine := NewLuaEngine()
	t.Cleanup(engine.Close)
	SetClock(engine.state, fixedClock{time.Date(2026, 10, 26, 3, 0, 0, 0, time.UTC)})
	mustExec(t, engine, calendarZoneSetup)

	rng, fz := zoneAnswers(t, engine)
	if rng.Zone != "Asia/Tokyo" || fz != "Asia/Tokyo" {
		t.Fatalf("calendar zone %q, forecast zone %q, want Asia/Tokyo", rng.Zone, fz)
	}
	if len(rng.Items) != 1 {
		t.Fatalf("items = %+v", rng.Items)
	}
	loc, err := time.LoadLocation(rng.Zone)
	if err != nil {
		t.Fatal(err)
	}
	if got := time.Unix(int64(rng.Items[0].At), 0).In(loc).Format("15:04"); got != "18:00" {
		t.Errorf("event at %s in the zone sent, want 18:00", got)
	}
	if got := mustExec(t, engine, `return datetime.timezone()`); got != "" {
		t.Errorf("datetime.timezone() = %q without setTimezone", got)
	}
}

// No name to be found: "" as before, the browser uses its own zone.
func TestCalendarZoneUnknown(t *testing.T) {
	defer func(f func() string) { localZoneName = f }(localZoneName)
	localZoneName = func() string { return "" }

	engine := NewLuaEngine()
	t.Cleanup(engine.Close)
	mustExec(t, engine, calendarZoneSetup)
	if rng, fz := zoneAnswers(t, engine); rng.Zone != "" || fz != "" {
		t.Errorf("calendar zone %q, forecast zone %q, want empty", rng.Zone, fz)
	}
}

// setTimezone wins over the server's zone, as before.
func TestCalendarZoneSetTimezone(t *testing.T) {
	defer func(f func() string) { localZoneName = f }(localZoneName)
	localZoneName = func() string { return "Asia/Tokyo" }

	engine := NewLuaEngine()
	t.Cleanup(engine.Close)
	SetClock(engine.state, fixedClock{time.Date(2026, 10, 26, 3, 0, 0, 0, time.UTC)})
	mustExec(t, engine, `datetime.setTimezone('Europe/Warsaw')`+calendarZoneSetup)
	rng, fz := zoneAnswers(t, engine)
	if rng.Zone != "Europe/Warsaw" || fz != "Europe/Warsaw" {
		t.Fatalf("calendar zone %q, forecast zone %q, want Europe/Warsaw", rng.Zone, fz)
	}
	warsaw, _ := time.LoadLocation("Europe/Warsaw")
	if len(rng.Items) != 1 || time.Unix(int64(rng.Items[0].At), 0).In(warsaw).Format("15:04") != "18:00" {
		t.Errorf("items = %+v", rng.Items)
	}
	if got := mustExec(t, engine, `return datetime.timezone()`); got != "Europe/Warsaw" {
		t.Errorf("datetime.timezone() = %q", got)
	}
}
