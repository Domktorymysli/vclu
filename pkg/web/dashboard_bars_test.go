package web

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Domktorymysli/vclu/pkg/config"
	"github.com/Domktorymysli/vclu/pkg/lua"
	"github.com/Domktorymysli/vclu/pkg/metrics"
	"github.com/gin-gonic/gin"
)

// writeHourly puts a counter rising by step each hour from..to into the collector's daily
// files, as the flush names them (the server's local date).
func writeHourly(t *testing.T, dataDir, dirName string, from, to time.Time, step float64) {
	t.Helper()
	dir := filepath.Join(dataDir, dirName)
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	v := 1000.0
	for ts := from; ts.Before(to); ts = ts.Add(time.Hour) {
		f, err := os.OpenFile(filepath.Join(dir, ts.Local().Format("2006-01-02")+".ts"), os.O_APPEND|os.O_WRONLY|os.O_CREATE, 0644)
		if err != nil {
			t.Fatal(err)
		}
		_, err = fmt.Fprintf(f, "%d %.10g\n", ts.Unix(), v)
		if cerr := f.Close(); err == nil {
			err = cerr
		}
		if err != nil {
			t.Fatal(err)
		}
		v += step
	}
}

func barsServer(t *testing.T, lua string) (*Server, *gin.Engine, string) {
	t.Helper()
	dir := t.TempDir()
	hash, _ := config.HashPassword("secret")
	if err := writeTestConfig(dir, &config.VCLUConfig{Auth: &config.VCLUAuthConfig{Username: "u", PasswordHash: hash}}); err != nil {
		t.Fatal(err)
	}
	dashCache.Invalidate()
	engine := newBarsEngine(t, lua)
	dataDir := t.TempDir()
	mc := metrics.NewCollector(dataDir, metrics.WithRingSize(10))
	for _, p := range []string{"energia:licznik", "energia:pv", "energia:secret"} {
		mc.Register(p, "kWh", 3600)
	}
	s := &Server{serverDir: dir, sessions: newSessionStore(), luaExecutor: engine, metricsCollector: mc}
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/api/dashboard/:id/bars", s.handleAPIDashboardBars)
	r.GET("/api/dashboard/:id/metrics", s.handleAPIDashboardMetrics)
	return s, r, dataDir
}

func newBarsEngine(t *testing.T, script string) *lua.LuaEngine {
	t.Helper()
	engine := lua.NewLuaEngine()
	t.Cleanup(engine.Close)
	if _, err := engine.Execute(script); err != nil {
		t.Fatal(err)
	}
	return engine
}

// A public dashboard serves its bars without login, only its own widgets' series, and the
// metrics behind them are not readable raw; session and PIN dashboards keep their gates.
func TestDashboardBarsFollowDashboardAuth(t *testing.T) {
	s, r, dataDir := barsServer(t, `
		datetime.setTimezone('UTC')
		Dashboard.new('pub'):auth('public'):section('S','i')
			:bars('dzien', 'Energia', 'energia:licznik', { range = '3d', agg = 'delta', compare = 'energia:pv' })
			:chart('c', 'Temp', 'temp:a', {})
			:build()
		Dashboard.new('sess'):auth('session'):section('S','i')
			:bars('dzien', 'Energia', 'energia:licznik', { range = '3d', agg = 'delta' }):build()
		Dashboard.new('pin'):auth('pin'):pin('1234'):section('S','i')
			:bars('dzien', 'Energia', 'energia:licznik', { range = '3d', agg = 'delta' }):build()
	`)
	now := time.Now()
	start := now.Add(-80 * time.Hour).Truncate(time.Hour)
	writeHourly(t, dataDir, "energia.licznik", start, now, 1)
	writeHourly(t, dataDir, "energia.pv", start, now, 0.5)

	cookies := map[string]string{}
	get := func(url string) (int, string) {
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, url, nil)
		for k, v := range cookies {
			req.AddCookie(&http.Cookie{Name: k, Value: v})
		}
		r.ServeHTTP(w, req)
		return w.Code, w.Body.String()
	}
	for _, tc := range []struct {
		url  string
		code int
	}{
		{"/api/dashboard/pub/bars?widget=dzien", 200},
		{"/api/dashboard/pub/bars?widget=c", 403},
		{"/api/dashboard/pub/bars?widget=nope%22)%20os.exit(", 403},
		{"/api/dashboard/pub/bars", 400},
		{"/api/dashboard/pub/metrics?path=energia:licznik", 403},
		{"/api/dashboard/sess/bars?widget=dzien", 401},
		{"/api/dashboard/pin/bars?widget=dzien", 401},
		{"/api/dashboard/none/bars?widget=dzien", 404},
	} {
		if code, body := get(tc.url); code != tc.code {
			t.Errorf("%s: %d %s, want %d", tc.url, code, body, tc.code)
		}
	}

	_, body := get("/api/dashboard/pub/bars?widget=dzien")
	var res barsResult
	if err := json.Unmarshal([]byte(body), &res); err != nil {
		t.Fatal(err)
	}
	if res.Bucket != "1d" || res.Agg != "delta" || res.Zone != "UTC" || len(res.Buckets) != 3 {
		t.Fatalf("result = %s", body)
	}
	for i, b := range res.Buckets[:2] {
		if b.V == nil || *b.V != 24 || b.C == nil || *b.C != 12 || b.Partial {
			t.Errorf("full day %d = %+v, want 24 and 12", i, b)
		}
		if b.Hour != 0 || time.Unix(b.T, 0).UTC().Format("2006-01-02") != b.Date {
			t.Errorf("day %d does not start at midnight: %+v", i, b)
		}
	}
	if today := res.Buckets[2]; !today.Partial || today.Date != now.UTC().Format("2006-01-02") {
		t.Errorf("today = %+v", today)
	}

	// the same widget opens after login, and with the PIN's cookie
	cookies[sessionCookieName] = s.sessions.create()
	if code, body := get("/api/dashboard/sess/bars?widget=dzien"); code != 200 {
		t.Errorf("session: %d %s", code, body)
	}
	delete(cookies, sessionCookieName)
	cookies["vclu_dash_pin"] = s.makeDashPinCookie("pin", "1234", time.Now().Add(time.Hour).Unix())
	if code, body := get("/api/dashboard/pin/bars?widget=dzien"); code != 200 {
		t.Errorf("PIN: %d %s", code, body)
	}
	if !strings.Contains(body, `"partial":true`) {
		t.Errorf("no partial bucket: %s", body)
	}
}

// Days in the house's zone: in Warsaw the fall-back day has 25 hourly rises, the spring one 23,
// and an hour without samples is a gap.
func TestDashboardBarsHouseZoneDST(t *testing.T) {
	loc, err := time.LoadLocation("Europe/Warsaw")
	if err != nil {
		t.Skip(err)
	}
	s, _, dataDir := barsServer(t, `Dashboard.new('x'):section('S','i'):bars('b', 'B', 'energia:licznik'):build()`)
	writeHourly(t, dataDir, "energia.licznik", time.Date(2026, 3, 27, 0, 0, 0, 0, loc), time.Date(2026, 3, 31, 0, 0, 0, 0, loc), 1)
	writeHourly(t, dataDir, "energia.licznik", time.Date(2026, 10, 23, 0, 0, 0, 0, loc), time.Date(2026, 10, 27, 0, 0, 0, 0, loc), 1)

	for _, tc := range []struct {
		now  time.Time
		want []float64
	}{
		{time.Date(2026, 3, 30, 12, 0, 0, 0, loc), []float64{24, 23, 24}},
		{time.Date(2026, 10, 26, 12, 0, 0, 0, loc), []float64{24, 25, 24}},
	} {
		res, err := s.barsSeries(barsSpec{Path: "energia:licznik", Bucket: "1d", Buckets: 3, Agg: "delta", Zone: "Europe/Warsaw"}, tc.now)
		if err != nil {
			t.Fatal(err)
		}
		for i, want := range tc.want {
			if b := res.Buckets[i]; b.V == nil || *b.V != want {
				t.Errorf("%s day %d (%s) = %v, want %v", tc.now.Format("01-02"), i, b.Date, b.V, want)
			}
		}
		if wd := res.Buckets[2].Weekday; wd != 1 {
			t.Errorf("weekday of %s = %d, want Monday", res.Buckets[2].Date, wd)
		}
	}

	// hourly over the fall-back night: 02:00 twice, labelled by its local hour
	res, err := s.barsSeries(barsSpec{Path: "energia:licznik", Bucket: "1h", Buckets: 5, Agg: "delta", Zone: "Europe/Warsaw"},
		time.Date(2026, 10, 25, 3, 30, 0, 0, loc))
	if err != nil {
		t.Fatal(err)
	}
	var hours []int
	for _, b := range res.Buckets {
		hours = append(hours, b.Hour)
	}
	if fmt.Sprint(hours) != "[0 1 2 2 3]" {
		t.Errorf("hours = %v, want [0 1 2 2 3]", hours)
	}

	// the spring-forward night: the 01:00 bucket ends at 03:00
	res, _ = s.barsSeries(barsSpec{Path: "energia:licznik", Bucket: "1h", Buckets: 3, Agg: "delta", Zone: "Europe/Warsaw"},
		time.Date(2026, 3, 29, 3, 30, 0, 0, loc))
	var spans []string
	for _, b := range res.Buckets {
		spans = append(spans, fmt.Sprintf("%d-%d", b.Hour, b.EndHour))
	}
	if fmt.Sprint(spans) != "[0-1 1-3 3-4]" {
		t.Errorf("spring-forward spans = %v, want [0-1 1-3 3-4]", spans)
	}

	// no data for the range: every bucket a gap
	res, _ = s.barsSeries(barsSpec{Path: "energia:licznik", Bucket: "1d", Buckets: 2, Agg: "avg", Zone: "Europe/Warsaw"},
		time.Date(2026, 6, 1, 12, 0, 0, 0, loc))
	for _, b := range res.Buckets {
		if b.V != nil {
			t.Errorf("June = %+v, want a gap", b)
		}
	}
}

// Days before the retention are marked old (nothing is kept for them), not shown as an outage.
func TestDashboardBarsOldBuckets(t *testing.T) {
	s, _, _ := barsServer(t, `Dashboard.new('x'):section('S','i'):bars('b', 'B', 'energia:licznik'):build()`)
	now := time.Now()
	res, err := s.barsSeries(barsSpec{Path: "energia:licznik", Bucket: "1d", Buckets: 10, Agg: "avg", Zone: "UTC"}, now)
	if err != nil {
		t.Fatal(err)
	}
	if res.Kept != metrics.DefaultRetainDays {
		t.Errorf("kept = %d", res.Kept)
	}
	old := 0
	for _, b := range res.Buckets {
		if b.Old {
			old++
		}
	}
	// 10 days back, 7 kept: the 3 oldest days and the one the cut-off falls in
	if old != 3 && old != 4 {
		t.Errorf("%d old buckets, want 3 or 4: %+v", old, res.Buckets)
	}
	if res.Buckets[9].Old || !res.Buckets[0].Old {
		t.Errorf("old marks in the wrong place: %+v", res.Buckets)
	}
}

// Parallel misses of one widget read and aggregate once; the others wait for that answer.
func TestDashboardBarsMergesConcurrentLoads(t *testing.T) {
	_, r, _ := barsServer(t, `Dashboard.new('pub'):auth('public'):section('S','i'):bars('b', 'B', 'energia:licznik'):build()`)
	orig := barsLoad
	defer func() { barsLoad = orig }()
	var mu sync.Mutex
	loads := 0
	release := make(chan struct{})
	barsLoad = func(s *Server, id, widget string) (int, []byte) {
		mu.Lock()
		loads++
		mu.Unlock()
		<-release
		return orig(s, id, widget)
	}
	var wg sync.WaitGroup
	codes := make([]int, 8)
	for i := range codes {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			w := httptest.NewRecorder()
			r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/dashboard/pub/bars?widget=b", nil))
			codes[i] = w.Code
		}(i)
	}
	// let every request reach the load before the first one finishes
	time.Sleep(200 * time.Millisecond)
	close(release)
	wg.Wait()
	if loads != 1 {
		t.Errorf("%d loads for 8 parallel requests, want 1", loads)
	}
	for i, c := range codes {
		if c != 200 {
			t.Errorf("request %d: %d", i, c)
		}
	}
}
