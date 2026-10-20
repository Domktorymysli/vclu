package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Domktorymysli/vclu/pkg/config"
	"github.com/Domktorymysli/vclu/pkg/lua"
	"github.com/gin-gonic/gin"
)

// A calendar widget serves the occurrences of its calendar for a range of days, under the
// dashboard's auth, and only for a widget that is on the dashboard.
func TestDashboardCalendarRange(t *testing.T) {
	dir := t.TempDir()
	hash, _ := config.HashPassword("secret")
	if err := config.UpdateMasterConfig(dir, func(mc *config.VCLUConfig) error {
		mc.Auth = &config.VCLUAuthConfig{Username: "u", PasswordHash: hash}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	dashCache.Invalidate()
	engine := lua.NewLuaEngine()
	defer engine.Close()
	if _, err := engine.Execute(`
		datetime.setTimezone("Europe/Warsaw")
		Calendar.new('dom'):holidays('pl')
			:event('bio', { title = 'Bio', every = 'tu', time = '06:30' })
			:event('goscie', { title = 'Goście', date = '2026-11-14..2026-11-16' })
			:event('sport', { title = 'Siłownia', every = 'mo', time = '18:00', tags = { 'sport' } })
			:schedule('pszok', { title = 'PSZOK', default = 'zamknięty', colors = { otwarty = 'success' },
				{ every = 'we', hours = '08:00-13:00', value = 'otwarty' } })
			:clock({ wait = false })
			:build()
		Dashboard.new('pub'):auth('public'):section('S', 'i')
			:calendar('kal', 'Kalendarz', 'dom', { view = 'month', schedules = { 'pszok', 'nieznany' }, namedays = true })
			:calendar('bezsportu', 'Bez sportu', Calendar.get('dom'), { tags = { 'nic' } })
			:calendar('brak', 'Brak', 'nieznany')
			:build()
		Dashboard.new('sess'):auth('session'):section('S', 'i'):calendar('kal', 'K', 'dom'):build()
	`); err != nil {
		t.Fatal(err)
	}
	s := &Server{serverDir: dir, sessions: newSessionStore(), luaExecutor: engine}
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/api/dashboard/:id/calendar", s.handleAPIDashboardCalendar)
	get := func(url string) (int, string) {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, url, nil))
		return w.Code, w.Body.String()
	}

	code, body := get("/api/dashboard/pub/calendar?widget=kal&from=2026-11-09&to=2026-11-16")
	if code != 200 {
		t.Fatalf("%d %s", code, body)
	}
	var res struct {
		Days []struct {
			Date    string `json:"date"`
			Holiday bool   `json:"holiday"`
			Workday bool   `json:"workday"`
		} `json:"days"`
		Items []struct {
			ID, Title, Date, Last string
			AllDay                bool `json:"allDay"`
		} `json:"items"`
	}
	if err := json.Unmarshal([]byte(body), &res); err != nil {
		t.Fatal(err)
	}
	if len(res.Days) != 8 || res.Days[2].Date != "2026-11-11" || !res.Days[2].Holiday || res.Days[2].Workday {
		t.Errorf("days: %+v", res.Days)
	}
	var got []string
	for _, it := range res.Items {
		got = append(got, it.ID+"@"+it.Date+".."+it.Last)
	}
	if want := "sport@2026-11-09..2026-11-09,bio@2026-11-10..2026-11-10,goscie@2026-11-14..2026-11-16,sport@2026-11-16..2026-11-16"; strings.Join(got, ",") != want {
		t.Errorf("items:\n%s\nwant\n%s", strings.Join(got, ","), want)
	}

	// a single day carries the schedules as segments; a range of days does not
	if strings.Contains(body, `"schedules"`) {
		t.Errorf("schedules in a range of days: %s", body)
	}
	// holiday names in a range; name days only for a single day
	if !strings.Contains(body, `"holidayName":"Święto Niepodległości"`) || strings.Contains(body, `"namedays"`) {
		t.Errorf("range: holiday name or name days: %s", body)
	}
	if _, one := get("/api/dashboard/pub/calendar?widget=kal&from=2026-10-20&to=2026-10-20"); !strings.Contains(one, `"namedays":[`) || !strings.Contains(one, "Irena") {
		t.Errorf("one day: no name days: %s", one)
	}

	// the engine here runs no timers: make the first check by hand
	if _, err := engine.Execute(`Calendar.get('dom'):_check()`); err != nil {
		t.Fatal(err)
	}
	_, day := get("/api/dashboard/pub/calendar?widget=kal&from=2026-11-04&to=2026-11-04")
	var d struct {
		Schedules []struct {
			ID, Title string
			Segments  []struct {
				Value, Color string
			}
		}
	}
	if err := json.Unmarshal([]byte(day), &d); err != nil {
		t.Fatalf("day: %v: %s", err, day)
	}
	if len(d.Schedules) != 1 || d.Schedules[0].Title != "PSZOK" || len(d.Schedules[0].Segments) != 3 ||
		d.Schedules[0].Segments[1].Value != "otwarty" || d.Schedules[0].Segments[1].Color != "success" {
		t.Errorf("schedules: %s", day)
	}

	if _, body := get("/api/dashboard/pub/calendar?widget=bezsportu&from=2026-11-09&to=2026-11-16"); strings.Contains(body, "sport") || strings.Contains(body, "bio") {
		t.Errorf("tags filter: %s", body)
	}
	for _, tc := range []struct {
		url, want string
		code      int
	}{
		{"/api/dashboard/pub/calendar?widget=brak&from=2026-11-01&to=2026-11-02", `not built`, 404},
		{"/api/dashboard/pub/calendar?widget=inny&from=2026-11-01&to=2026-11-02", `not on this dashboard`, 403},
		{"/api/dashboard/pub/calendar?widget=kal&from=2026-11-01&to=2027-01-01", `at most 42 days`, 400},
		// the month view asks for 42 days (six weeks); one more is refused
		{"/api/dashboard/pub/calendar?widget=kal&from=2026-10-26&to=2026-12-06", `"days":[`, 200},
		{"/api/dashboard/pub/calendar?widget=kal&from=2026-10-26&to=2026-12-07", `at most 42 days`, 400},
		{"/api/dashboard/pub/calendar?widget=kal&from=2026-11-02&to=2026-11-01", `YYYY-MM-DD`, 400},
		{"/api/dashboard/pub/calendar?widget=kal&from=2026-11-01%22)os.exit(&to=2026-11-02", `YYYY-MM-DD`, 400},
		{"/api/dashboard/pub/calendar?widget=kal%22)os.exit(--&from=2026-11-01&to=2026-11-02", `not on this dashboard`, 403},
		{"/api/dashboard/sess/calendar?widget=kal&from=2026-11-01&to=2026-11-02", ``, 401},
		{"/api/dashboard/none/calendar?widget=kal&from=2026-11-01&to=2026-11-02", ``, 404},
	} {
		code, body := get(tc.url)
		if code != tc.code || !strings.Contains(body, tc.want) {
			t.Errorf("%s: %d %s, want %d with %q", tc.url, code, body, tc.code, tc.want)
		}
	}
}

func newCalendarDashboardServer(t *testing.T, script string) (*lua.LuaEngine, func(url string) (int, string)) {
	t.Helper()
	dashCache.Invalidate()
	engine := lua.NewLuaEngine()
	t.Cleanup(engine.Close)
	if _, err := engine.Execute(script); err != nil {
		t.Fatal(err)
	}
	s := &Server{serverDir: t.TempDir(), sessions: newSessionStore(), luaExecutor: engine}
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/api/dashboard/:id/calendar", s.handleAPIDashboardCalendar)
	return engine, func(url string) (int, string) {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, url, nil))
		return w.Code, w.Body.String()
	}
}

// What a calendar widget does not show is not sent either: on a public page sunrise and
// sunset give away where the house is.
func TestDashboardCalendarSendsOnlyWhatTheWidgetShows(t *testing.T) {
	_, get := newCalendarDashboardServer(t, `
		datetime.setTimezone("Europe/Warsaw")
		Calendar.new('dom'):holidays('pl'):location(52.2297, 21.0122):clock({ wait = false }):build()
		Dashboard.new('pub'):auth('public'):section('S', 'i')
			:calendar('pelny', 'K', 'dom', { namedays = true })
			:calendar('goly', 'K', 'dom', { sun = false, holidays = false, namedays = false })
			:calendar('domyslny', 'K', 'dom')
			:build()
	`)
	type day struct {
		Holiday     bool     `json:"holiday"`
		HolidayName string   `json:"holidayName"`
		Namedays    []string `json:"namedays"`
		Sunrise     int64    `json:"sunrise"`
		Sunset      int64    `json:"sunset"`
	}
	one := func(widget string) day {
		t.Helper()
		code, body := get("/api/dashboard/pub/calendar?widget=" + widget + "&from=2026-11-11&to=2026-11-11")
		var res struct{ Days []day }
		if err := json.Unmarshal([]byte(body), &res); err != nil || code != 200 || len(res.Days) != 1 {
			t.Fatalf("%s: %d %s (%v)", widget, code, body, err)
		}
		return res.Days[0]
	}
	full := one("pelny")
	if !full.Holiday || full.HolidayName == "" || len(full.Namedays) == 0 || full.Sunrise == 0 || full.Sunset == 0 {
		t.Errorf("all switched on: %+v", full)
	}
	if full.Sunrise%60 != 0 || full.Sunset%60 != 0 {
		t.Errorf("sun times not rounded to the minute: %+v", full)
	}
	if bare := one("goly"); bare.Holiday || bare.HolidayName != "" || bare.Namedays != nil || bare.Sunrise != 0 || bare.Sunset != 0 {
		t.Errorf("switched off but sent: %+v", bare)
	}
	if def := one("domyslny"); def.Namedays != nil || def.Sunrise == 0 || def.HolidayName == "" {
		t.Errorf("defaults (holidays and sun on, name days off): %+v", def)
	}
}

// A Lua error behind the calendar endpoint is logged; the answer, which a public page sees,
// carries no paths or stack.
func TestDashboardCalendarErrorHidesTheLuaError(t *testing.T) {
	engine, get := newCalendarDashboardServer(t, `
		Calendar.new('dom'):clock({ wait = false }):build()
		Dashboard.new('pub'):auth('public'):section('S', 'i'):calendar('kal', 'K', 'dom'):build()
	`)
	if _, err := engine.Execute(`Calendar.get = function() error('/srv/vclu/modules/secret.lua: boom') end`); err != nil {
		t.Fatal(err)
	}
	code, body := get("/api/dashboard/pub/calendar?widget=kal&from=2026-11-01&to=2026-11-02")
	if code != http.StatusInternalServerError || !strings.Contains(body, "calendar unavailable") {
		t.Errorf("%d %s", code, body)
	}
	for _, leak := range []string{"secret", "/srv", "boom", "stack", ".lua"} {
		if strings.Contains(body, leak) {
			t.Errorf("body leaks %q: %s", leak, body)
		}
	}
}
