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
			:event('goscie', { title = 'Goście', date = '2026-11-14..2026-11-16', tags = { 'nic' } })
			:event('sport', { title = 'Siłownia', every = 'mo', time = '18:00', tags = { 'sport' } })
			:schedule('pszok', { title = 'PSZOK', values = { 'zamknięty', 'otwarty' }, default = 'zamknięty', colors = { otwarty = 'success' },
				{ every = 'we', hours = '08:00-13:00', value = 'otwarty' } })
			:clock({ wait = false })
			:build()
		Dashboard.new('pub'):auth('public'):section('S', 'i')
			:calendar('kal', 'Kalendarz', 'dom', { view = 'month', schedules = { 'pszok' }, namedays = true })
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

// A month sends at most three events a day, what a cell draws, and each day's count for "+n";
// a single day sends all of them.
func TestDashboardCalendarMonthSendsWhatCellsDraw(t *testing.T) {
	_, get := newCalendarDashboardServer(t, `
		local c = Calendar.new('dom'):clock({ wait = false })
		for i = 1, 5 do c:event('e' .. i, { every = 'day', time = '0' .. i .. ':00' }) end
		c:event('goscie', { date = '2026-11-02..2026-11-04' })
		c:build()
		Dashboard.new('pub'):auth('public'):section('S', 'i'):calendar('kal', 'K', 'dom'):build()
	`)
	var res struct {
		Zone string `json:"zone"`
		Days []struct {
			Date  string `json:"date"`
			Count int    `json:"count"`
		} `json:"days"`
		Items []struct{ ID, Date, Last string } `json:"items"`
	}
	code, body := get("/api/dashboard/pub/calendar?widget=kal&from=2026-11-01&to=2026-11-07")
	if code != 200 || json.Unmarshal([]byte(body), &res) != nil {
		t.Fatalf("%d %s", code, body)
	}
	perDay := map[string]int{}
	for _, it := range res.Items {
		last := it.Last
		if last == "" {
			last = it.Date
		}
		for _, d := range res.Days {
			if d.Date >= it.Date && d.Date <= last {
				perDay[d.Date]++
			}
		}
	}
	for _, d := range res.Days {
		want := 5
		if d.Date >= "2026-11-02" && d.Date <= "2026-11-04" {
			want = 6
		}
		if d.Count != want || perDay[d.Date] != 3 {
			t.Errorf("%s: count %d, sent %d; want %d and 3", d.Date, d.Count, perDay[d.Date], want)
		}
	}
	if code, body := get("/api/dashboard/pub/calendar?widget=kal&from=2026-11-03&to=2026-11-03"); code != 200 || strings.Count(body, `"id":`) != 6 {
		t.Errorf("one day: %d %s", code, body)
	}
}

// Answers are cached for a while, and a caller walking dates is limited per address.
func TestDashboardCalendarCacheAndLimit(t *testing.T) {
	engine, _ := newCalendarDashboardServer(t, `
		Calendar.new('dom'):clock({ wait = false }):event('a', { every = 'day', time = '10:00' }):build()
		Dashboard.new('pub'):auth('public'):section('S', 'i'):calendar('kal', 'K', 'dom'):build()
	`)
	s := &Server{serverDir: t.TempDir(), sessions: newSessionStore(), luaExecutor: engine,
		calendarLimiter: &rateLimiter{buckets: make(map[string]*bucket), rate: 0.001, burst: 3}}
	r := gin.New()
	r.GET("/api/dashboard/:id/calendar", s.handleAPIDashboardCalendar)
	get := func(day string) int {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/dashboard/pub/calendar?widget=kal&from="+day+"&to="+day, nil))
		return w.Code
	}
	for i := 0; i < 5; i++ {
		if code := get("2026-11-01"); code != 200 {
			t.Fatalf("cached day, request %d: %d", i, code)
		}
	}
	codes := []int{get("2026-11-02"), get("2026-11-03"), get("2026-11-04")}
	if codes[0] != 200 || codes[1] != 200 || codes[2] != http.StatusTooManyRequests {
		t.Errorf("new days with a burst of 3 (one used): %v", codes)
	}
	if code := get("2026-11-01"); code != 200 {
		t.Errorf("a cached answer is not limited: %d", code)
	}
}

// schedules = true draws every schedule of the calendar, in the order they were defined.
func TestDashboardCalendarAllSchedules(t *testing.T) {
	engine, get := newCalendarDashboardServer(t, `
		Calendar.new('dom'):clock({ wait = false })
			:schedule('ogrod', { title = 'Lampy w ogrodzie', values = { 'zgaszone', 'włączone' }, default = 'zgaszone',
				{ every = 'day', hours = '18:00-23:00', value = 'włączone' } })
			:schedule('taryfa', { values = { 'droga', 'tania' }, default = 'droga', { every = 'day', hours = '13:00-15:00', value = 'tania' } })
			:build()
		Dashboard.new('pub'):auth('public'):section('S', 'i')
			:calendar('wszystkie', 'K', 'dom', { view = 'day', schedules = true })
			:calendar('zadne', 'K', 'dom', { view = 'day' })
			:build()
	`)
	if _, err := engine.Execute(`Calendar.get('dom'):_check()`); err != nil {
		t.Fatal(err)
	}
	var res struct {
		Schedules []struct {
			ID, Title string
			Segments  []struct{ Value string }
		} `json:"schedules"`
	}
	code, body := get("/api/dashboard/pub/calendar?widget=wszystkie&from=2026-11-03&to=2026-11-03")
	if code != 200 || json.Unmarshal([]byte(body), &res) != nil {
		t.Fatalf("%d %s", code, body)
	}
	if len(res.Schedules) != 2 || res.Schedules[0].Title != "Lampy w ogrodzie" || res.Schedules[1].ID != "taryfa" ||
		len(res.Schedules[0].Segments) != 3 || res.Schedules[0].Segments[1].Value != "włączone" {
		t.Errorf("all schedules: %s", body)
	}
	if code, body := get("/api/dashboard/pub/calendar?widget=zadne&from=2026-11-03&to=2026-11-03"); code != 200 || strings.Contains(body, `"segments"`) {
		t.Errorf("no schedules option: %d %s", code, body)
	}
	if _, err := engine.Execute(`Dashboard.new('zle'):section('S', 'i'):calendar('k', 'K', 'dom', { schedules = 'ogrod' }):build()`); err == nil || !strings.Contains(err.Error(), "or true for all") {
		t.Errorf("a string is refused: %v", err)
	}
}

// A widget's options are checked when the dashboard is built: a typo is an error, not a widget
// that silently shows less. A calendar not built yet is checked by the endpoint instead.
func TestDashboardCalendarWidgetOptionsChecked(t *testing.T) {
	engine, _ := newCalendarDashboardServer(t, `
		Calendar.new('dom'):clock({ wait = false })
			:event('bio', { every = 'tu', time = '06:30', tags = { 'smieci' } })
			:schedule('pszok', { values = { 'zamknięty', 'otwarty' }, default = 'zamknięty', { every = 'we', hours = '08:00-13:00', value = 'otwarty' } })
			:build()
	`)
	for opts, want := range map[string]string{
		`{ schedule = { 'pszok' } }`: `unknown option "schedule"`,
		`{ schedules = { 'psok' } }`: `calendar "dom" has no schedule "psok" (schedules: "pszok")`,
		`{ tags = { 'śmieci' } }`:    `no event of calendar "dom" has the tag "śmieci" (tags: "smieci")`,
		`{ tags = 'smiec' }`:         `has the tag "smiec"`,
		`{ namedays = 'tak' }`:       `namedays must be true or false`,
		`{ view = 'week' }`:          `view must be "month" or "day"`,
	} {
		_, err := engine.Execute(`Dashboard.new('x'):section('S', 'i'):calendar('k', 'K', 'dom', ` + opts + `):build()`)
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: %v, want %q", opts, err, want)
		}
	}
	for _, ok := range []string{
		`'dom', { schedules = { 'pszok' }, tags = 'smieci', namedays = true, span = 2 }`,
		`'jeszcze-nie', { schedules = { 'cokolwiek' } }`,
	} {
		if _, err := engine.Execute(`Dashboard.new('y'):section('S', 'i'):calendar('k', 'K', ` + ok + `):build()`); err != nil {
			t.Errorf("%s: %v", ok, err)
		}
	}
}
