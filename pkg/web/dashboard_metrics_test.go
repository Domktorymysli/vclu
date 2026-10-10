package web

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Domktorymysli/vclu/pkg/config"
	"github.com/Domktorymysli/vclu/pkg/lua"
	"github.com/Domktorymysli/vclu/pkg/metrics"
	"github.com/gin-gonic/gin"
)

// A public dashboard serves its own charts without login, and only those.
func TestDashboardMetricsFollowDashboardAuth(t *testing.T) {
	dir := t.TempDir()
	hash, _ := config.HashPassword("secret")
	if err := config.WriteMasterConfig(dir, &config.VCLUConfig{Auth: &config.VCLUAuthConfig{Username: "u", PasswordHash: hash}}); err != nil {
		t.Fatal(err)
	}
	dashCache.Invalidate()
	engine := lua.NewLuaEngine()
	defer engine.Close()
	if _, err := engine.Execute(`
		Dashboard.new('pub'):auth('public'):section('S','i')
			:chart('c', 'Temp', 'temp:a', {})
			:status('s', 'Temp B', 'i', function() return 1 end, '°C', { spark = 'temp:b' })
			:chart('q', 'Quoted', 'x:"a\\b', {})
			:build()
		Dashboard.new('sess'):auth('session'):section('S','i')
			:chart('c', 'Temp', 'temp:a', {}):build()
	`); err != nil {
		t.Fatal(err)
	}
	mc := metrics.NewCollector(t.TempDir(), metrics.WithRingSize(10))
	for _, p := range []string{"temp:a", "temp:b", "energia:secret", `x:"a\b`} {
		mc.Register(p, "", 10)
		mc.Ingest([]metrics.CollectResult{{Path: p, Value: 21.5}})
	}
	s := &Server{serverDir: dir, sessions: newSessionStore(), luaExecutor: engine, metricsCollector: mc}
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/api/dashboard/:id/metrics", s.handleAPIDashboardMetrics)

	session := ""
	get := func(url string) (int, string) {
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, url, nil)
		if session != "" {
			req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: session})
		}
		r.ServeHTTP(w, req)
		return w.Code, w.Body.String()
	}
	for _, tc := range []struct {
		url  string
		code int
	}{
		{"/api/dashboard/pub/metrics?path=temp:a&range=2h", 200},
		{"/api/dashboard/pub/metrics?path=temp:b", 200},
		{"/api/dashboard/pub/metrics?path=energia:secret", 403},
		{"/api/dashboard/pub/metrics?path=temp%22)%20os.exit(", 403},
		{"/api/dashboard/pub/metrics?path=x:%22a%5Cb", 200},
		{"/api/dashboard/pub/metrics?path=temp:a&range=36500d", 200},
		{"/api/dashboard/sess/metrics?path=temp:a", 401},
		{"/api/dashboard/none/metrics?path=temp:a", 404},
	} {
		code, body := get(tc.url)
		if code != tc.code {
			t.Errorf("%s: %d %s, want %d", tc.url, code, body, tc.code)
		}
		if code == 200 && !strings.Contains(body, "21.5") {
			t.Errorf("%s: no samples: %s", tc.url, body)
		}
	}
	// the same chart on a session dashboard opens after login
	session = s.sessions.create()
	if code, body := get("/api/dashboard/sess/metrics?path=temp:a"); code != 200 {
		t.Errorf("session: %d %s", code, body)
	}
}

func TestCapRange(t *testing.T) {
	for _, tc := range []struct {
		req   string
		drawn []string
		want  string
	}{
		{"2h", []string{"2h"}, ""},
		{"30m", []string{"24h"}, ""},
		{"36500d", []string{"2h", "24h"}, "24h0m0s"},
		{"7d", []string{"2h"}, "2h0m0s"},
	} {
		if got := capRange(tc.req, tc.drawn); got != tc.want {
			t.Errorf("capRange(%q, %v) = %q, want %q", tc.req, tc.drawn, got, tc.want)
		}
	}
}
