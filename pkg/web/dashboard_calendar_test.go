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
	if err := config.WriteMasterConfig(dir, &config.VCLUConfig{Auth: &config.VCLUAuthConfig{Username: "u", PasswordHash: hash}}); err != nil {
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
			:calendar('kal', 'Kalendarz', 'dom', { view = 'month', schedules = { 'pszok', 'nieznany' } })
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
	json.Unmarshal([]byte(day), &d)
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
		{"/api/dashboard/pub/calendar?widget=brak&from=2026-11-01&to=2026-11-02", `not built`, 200},
		{"/api/dashboard/pub/calendar?widget=inny&from=2026-11-01&to=2026-11-02", `not on this dashboard`, 200},
		{"/api/dashboard/pub/calendar?widget=kal&from=2026-11-01&to=2027-01-01", `at most 42 days`, 200},
		{"/api/dashboard/pub/calendar?widget=kal&from=2026-11-01%22)os.exit(&to=2026-11-02", `YYYY-MM-DD`, 400},
		{"/api/dashboard/pub/calendar?widget=kal%22)os.exit(--&from=2026-11-01&to=2026-11-02", `not on this dashboard`, 200},
		{"/api/dashboard/sess/calendar?widget=kal&from=2026-11-01&to=2026-11-02", ``, 401},
		{"/api/dashboard/none/calendar?widget=kal&from=2026-11-01&to=2026-11-02", ``, 404},
	} {
		code, body := get(tc.url)
		if code != tc.code || !strings.Contains(body, tc.want) {
			t.Errorf("%s: %d %s, want %d with %q", tc.url, code, body, tc.code, tc.want)
		}
	}
}
