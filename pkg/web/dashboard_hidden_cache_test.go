package web

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// Bars and a calendar read while shown answer 403 as soon as visible() hides them, not when
// their cached answer runs out.
func TestDashboardHiddenWidgetNotServedFromCache(t *testing.T) {
	s, r, dataDir := barsServer(t, `
		datetime.setTimezone('UTC')
		pokaz = { on = true }
		Calendar.new('dom'):clock({ wait = false }):build()
		Dashboard.new('pub'):auth('public'):section('S', 'i')
			:bars('dzien', 'Energia', 'energia:licznik', { range = '3d', agg = 'delta',
				visible = function() return pokaz.on end })
			:calendar('kal', 'K', 'dom', { visible = function() return pokaz.on end })
			:build()
	`)
	r.GET("/api/dashboard/:id/calendar", s.handleAPIDashboardCalendar)
	now := time.Now()
	writeHourly(t, dataDir, "energia.licznik", now.Add(-50*time.Hour).Truncate(time.Hour), now, 1)
	get := func(url string) int {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, url, nil))
		return w.Code
	}
	urls := []string{
		"/api/dashboard/pub/bars?widget=dzien",
		"/api/dashboard/pub/calendar?widget=kal&from=2026-11-02&to=2026-11-08",
	}
	for _, u := range urls {
		if code := get(u); code != 200 {
			t.Fatalf("shown %s: %d", u, code)
		}
	}
	if _, err := s.luaExecutor.Execute(`pokaz.on = false`); err != nil {
		t.Fatal(err)
	}
	for _, u := range urls {
		if code := get(u); code != 403 {
			t.Errorf("hidden %s: %d, want 403", u, code)
		}
	}
	if _, err := s.luaExecutor.Execute(`pokaz.on = true`); err != nil {
		t.Fatal(err)
	}
	for _, u := range urls {
		if code := get(u); code != 200 {
			t.Errorf("shown again %s: %d", u, code)
		}
	}
}
