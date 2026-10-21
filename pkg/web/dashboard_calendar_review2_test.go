package web

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/Domktorymysli/vclu/pkg/config"
	"github.com/Domktorymysli/vclu/pkg/lua"
	"github.com/gin-gonic/gin"
)

// Request values that are not plain ids reach Lua only as string literals: nothing runs.
func TestCalendarWidgetReview2_InjectionIsInert(t *testing.T) {
	engine, get := newCalendarDashboardServer(t, `
		Calendar.new('dom'):clock({ wait = false }):build()
		Dashboard.new('pub'):auth('public'):section('S', 'i'):calendar('kal', 'K', 'dom'):build()
	`)
	payloads := []string{
		`"..(function() pwned = 1 end)().."`,
		`\"..(function() pwned = 2 end)()..\"`,
		"x\")\npwned = 3\n--",
		"x\")\rpwned = 4\r--",
		`]]pwned = 5--[[`,
		"x\x00\")pwned = 6--",
		`\`, `\\`, `kal\`, "kal\xff\xfe", "żółć🙂", "  ",
		strings.Repeat(`"`, 128),
	}
	for _, p := range payloads {
		code, body := get("/api/dashboard/pub/calendar?widget=" + url.QueryEscape(p) + "&from=2026-11-01&to=2026-11-02")
		if code != http.StatusForbidden {
			t.Errorf("widget %q: %d %s", p, code, body)
		}
		code, body = get("/api/dashboard/" + url.PathEscape(p) + "/calendar?widget=kal&from=2026-11-01&to=2026-11-02")
		if code != http.StatusNotFound && code != http.StatusMovedPermanently && code != http.StatusTemporaryRedirect {
			t.Errorf("dashboard %q: %d %s", p, code, body)
		}
	}
	if code, _ := get("/api/dashboard/pub/calendar?widget=" + strings.Repeat("a", 129) + "&from=2026-11-01&to=2026-11-02"); code != 400 {
		t.Errorf("129-byte widget id: %d", code)
	}
	if v, err := engine.Execute(`return tostring(pwned)`); err != nil || v != "nil" {
		t.Fatalf("payload ran: pwned=%v (%v)", v, err)
	}
}

// Dates that are not real days, or outside the range, are refused without running anything heavy.
func TestCalendarWidgetReview2_Dates(t *testing.T) {
	_, get := newCalendarDashboardServer(t, `
		datetime.setTimezone("Europe/Warsaw")
		Calendar.new('dom'):holidays('pl'):location(52.2, 21.0)
			:event('bio', { title = 'Bio', every = 'tu', time = '06:30' })
			:clock({ wait = false }):build()
		Dashboard.new('pub'):auth('public'):section('S', 'i'):calendar('kal', 'K', 'dom', { namedays = true }):build()
	`)
	for _, tc := range []struct {
		from, to string
		code     int
	}{
		{"2026-02-31", "2026-02-31", 400},
		{"2026-02-29", "2026-02-29", 400},
		{"2028-02-29", "2028-02-29", 200},
		{"0000-00-00", "0000-00-00", 400},
		{"2026-13-01", "2026-13-01", 400},
		{"2026-1-01", "2026-01-02", 400},
		{"２０２６-11-01", "2026-11-02", 400},
		{"2026-11-01 ", "2026-11-02", 400},
		{"2026-11-01\n", "2026-11-02", 400},
		{"2026-11-05", "2026-11-01", 400},
		{"0000-01-01", "0000-01-01", 400},
		{"9999-12-31", "9999-12-31", 400},
		{"9999-11-20", "9999-12-31", 400},
		{"2100-12-31", "2100-12-31", 200},
		{"0001-01-01", "9999-12-31", 400},
	} {
		start := time.Now()
		code, body := get("/api/dashboard/pub/calendar?widget=kal&from=" + url.QueryEscape(tc.from) + "&to=" + url.QueryEscape(tc.to))
		if code != tc.code {
			t.Errorf("%s..%s: %d %s, want %d", tc.from, tc.to, code, body, tc.code)
		}
		if d := time.Since(start); d > 500*time.Millisecond {
			t.Errorf("%s..%s took %v", tc.from, tc.to, d)
		}
	}
}

// Each auth mode guards the calendar endpoint; a PIN cookie of one dashboard opens no other.
func TestCalendarWidgetReview2_Auth(t *testing.T) {
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
		Calendar.new('dom'):event('e', { title = 'Sekret', date = '2026-11-03' }):clock({ wait = false }):build()
		Dashboard.new('pub'):auth('public'):section('S', 'i'):calendar('pubkal', 'K', 'dom'):build()
		Dashboard.new('pin1'):auth('pin'):pin('1234'):section('S', 'i'):calendar('kal', 'K', 'dom'):build()
		Dashboard.new('pin2'):auth('pin'):pin('9999'):section('S', 'i'):calendar('kal', 'K', 'dom'):build()
		Dashboard.new('sess'):auth('session'):section('S', 'i'):calendar('kal', 'K', 'dom'):build()
		Dashboard.dynamic('dyn', function(d) d:auth('session'):section('S', 'i'):calendar('kal', 'K', 'dom') end)
	`); err != nil {
		t.Fatal(err)
	}
	s := &Server{serverDir: dir, sessions: newSessionStore(), luaExecutor: engine}
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/api/dashboard/:id/calendar", s.handleAPIDashboardCalendar)
	exp := time.Now().Add(time.Hour).Unix()
	pin1 := &http.Cookie{Name: "vclu_dash_pin1", Value: s.makeDashPinCookie("pin1", "1234", exp)}
	// pin1's cookie value presented under pin2's name
	pin1as2 := &http.Cookie{Name: "vclu_dash_pin2", Value: pin1.Value}
	stale := &http.Cookie{Name: "vclu_dash_pin1", Value: s.makeDashPinCookie("pin1", "0000", exp)}
	sess := &http.Cookie{Name: sessionCookieName, Value: s.sessions.create()}
	q := "/calendar?widget=kal&from=2026-11-01&to=2026-11-07"
	for _, tc := range []struct {
		url    string
		cookie *http.Cookie
		code   int
	}{
		{"/api/dashboard/pub/calendar?widget=pubkal&from=2026-11-01&to=2026-11-07", nil, 200},
		{"/api/dashboard/pub" + q, nil, 403}, // a calendar of another dashboard
		{"/api/dashboard/pin1" + q, nil, 401},
		{"/api/dashboard/pin1" + q, stale, 401},
		{"/api/dashboard/pin1" + q, pin1, 200},
		{"/api/dashboard/pin2" + q, pin1, 401},
		{"/api/dashboard/pin2" + q, pin1as2, 401},
		{"/api/dashboard/pin2" + q, sess, 200},
		{"/api/dashboard/sess" + q, nil, 401},
		{"/api/dashboard/sess" + q, pin1, 401},
		{"/api/dashboard/sess" + q, &http.Cookie{Name: sessionCookieName, Value: "forged"}, 401},
		{"/api/dashboard/sess" + q, sess, 200},
		{"/api/dashboard/dyn" + q, nil, 401},
		{"/api/dashboard/dyn" + q, sess, 200},
		{"/api/dashboard/nope" + q, nil, 404},
	} {
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, tc.url, nil)
		if tc.cookie != nil {
			req.AddCookie(tc.cookie)
		}
		r.ServeHTTP(w, req)
		if w.Code != tc.code {
			t.Errorf("%s (%v): %d %s, want %d", tc.url, tc.cookie, w.Code, w.Body.String(), tc.code)
		}
		if w.Code >= 400 && strings.Contains(w.Body.String(), "Sekret") {
			t.Errorf("%s: refused but sent events: %s", tc.url, w.Body.String())
		}
	}
}

// An empty calendar (no events, no schedules) answers with lists the widget can iterate.
func TestCalendarWidgetReview2_EmptyCalendar(t *testing.T) {
	engine, get := newCalendarDashboardServer(t, `
		Calendar.new('pusty'):clock({ wait = false }):build()
		Dashboard.new('pub'):auth('public'):section('S', 'i')
			:calendar('kal', 'K', 'pusty', { schedules = {} }):build()
	`)
	if _, err := engine.Execute(`Calendar.get('pusty'):_check()`); err != nil {
		t.Fatal(err)
	}
	for _, rng := range []string{"from=2026-11-01&to=2026-11-01", "from=2026-10-26&to=2026-12-06"} {
		code, body := get("/api/dashboard/pub/calendar?widget=kal&" + rng)
		if code != 200 || strings.Contains(body, `"error"`) {
			t.Errorf("%s: %d %s", rng, code, body)
		}
		t.Logf("%s: %.200s", rng, body)
	}
}

// The heaviest request a public page may send: 42 days of a calendar with many sun windows.
// Logged only: it holds the Lua VM (all automations) that long, with no rate limit.
func TestCalendarWidgetReview2_WorstRequestCost(t *testing.T) {
	var b strings.Builder
	b.WriteString(`datetime.setTimezone("Europe/Warsaw")
		local c = Calendar.new('dom'):holidays('pl'):location(52.2, 21.0)
	`)
	for i := 0; i < 20; i++ {
		fmt.Fprintf(&b, "c:event('sun%d', { title = 'S%d', every = 'day', sun = { azimuth = { %d, %d }, elevation = 15 } })\n", i, i, 100+i, 200+i)
	}
	for i := 0; i < 30; i++ {
		fmt.Fprintf(&b, "c:event('ev%d', { title = 'E%d', every = 'day', time = '%02d:%02d', duration = '1h' })\n", i, i, i%24, i%60)
	}
	b.WriteString(`c:clock({ wait = false }):build()
		Dashboard.new('pub'):auth('public'):section('S', 'i'):calendar('kal', 'K', 'dom', { namedays = true }):build()
	`)
	_, get := newCalendarDashboardServer(t, b.String())
	var worst time.Duration
	// a new six-week range each time: the per-rule sun cache (400 days) does not help
	for i := 0; i < 10; i++ {
		from := time.Date(2030+i*7, 1, 1, 0, 0, 0, 0, time.UTC)
		u := "/api/dashboard/pub/calendar?widget=kal&from=" + from.Format("2006-01-02") + "&to=" + from.AddDate(0, 0, 41).Format("2006-01-02")
		start := time.Now()
		code, body := get(u)
		d := time.Since(start)
		if code != 200 {
			t.Fatalf("%d %.300s", code, body)
		}
		worst = max(worst, d)
		if i == 0 {
			t.Logf("first: %v, %d bytes", d, len(body))
		}
	}
	t.Logf("worst 42-day request with 20 sun windows and 30 daily events: %v", worst)
}

// A dense calendar (65 daily events, 2730 occurrences in six weeks; 61 is enough) breaks the month view:
// JSON.encode's table.concat overflows the Lua registry and the widget gets a 500.
func TestCalendarWidgetReview2_DenseMonthOverflowsRegistry(t *testing.T) {
	var b strings.Builder
	b.WriteString("local c = Calendar.new('dom')\n")
	for i := 0; i < 65; i++ {
		fmt.Fprintf(&b, "c:event('ev%d', { title = 'E%d', every = 'day', time = '%02d:%02d' })\n", i, i, i%24, i%60)
	}
	b.WriteString(`c:clock({ wait = false }):build()
		Dashboard.new('pub'):auth('public'):section('S', 'i'):calendar('kal', 'K', 'dom'):build()
	`)
	_, get := newCalendarDashboardServer(t, b.String())
	if code, body := get("/api/dashboard/pub/calendar?widget=kal&from=2026-10-26&to=2026-12-06"); code != 200 {
		t.Errorf("month view of a dense calendar: %d %.200s", code, body)
	}
	if code, _ := get("/api/dashboard/pub/calendar?widget=kal&from=2026-11-01&to=2026-11-01"); code != 200 {
		t.Errorf("day view after the failure: %d", code)
	}
}
