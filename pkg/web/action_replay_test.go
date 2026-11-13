package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Domktorymysli/vclu/pkg/lua"
	"github.com/gin-gonic/gin"
)

// A repeated actionId gets the first answer and runs nothing; another id, another person or a
// request without an id runs again; a repeat after the TTL runs again.
func TestDashboardActionReplay(t *testing.T) {
	dashCache.Invalidate()
	savedLimiter, savedReplay := actionLimiter, actionReplay
	actionLimiter = &rateLimiter{buckets: make(map[string]*bucket), rate: 1000, burst: 1000}
	now := time.Unix(1_000_000, 0)
	var clock sync.Mutex
	actionReplay = &actionReplays{byID: make(map[string]*actionAnswer), now: func() time.Time {
		clock.Lock()
		defer clock.Unlock()
		return now
	}}
	t.Cleanup(func() { actionLimiter, actionReplay = savedLimiter, savedReplay })

	engine := lua.NewLuaEngine()
	defer engine.Close()
	if _, err := engine.Execute(`
		PULSES = 0
		Dashboard.new('a')
			:section('S', 'i')
				:button('gate', 'Brama', nil, function() PULSES = PULSES + 1; return 'pulse ' .. PULSES end)
			:build()`); err != nil {
		t.Fatal(err)
	}
	s := &Server{serverDir: t.TempDir(), sessions: newSessionStore(), luaExecutor: engine}
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.POST("/api/dashboard/:id/action", s.handleAPIDashboardAction)
	post := func(body, from string) *httptest.ResponseRecorder {
		t.Helper()
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/api/dashboard/a/action", strings.NewReader(body))
		req.RemoteAddr = from + ":1234"
		r.ServeHTTP(w, req)
		return w
	}
	pulses := func() string {
		t.Helper()
		v, err := engine.Execute(`return tostring(PULSES)`)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	tap := func(id string) string {
		if id == "" {
			return `{"widgetId":"gate","action":"tap"}`
		}
		return `{"widgetId":"gate","action":"tap","actionId":"` + id + `"}`
	}

	first := post(tap("phone-0001"), "10.0.0.2")
	if first.Code != http.StatusOK || !strings.Contains(first.Body.String(), "pulse 1") {
		t.Fatalf("first = %d %s", first.Code, first.Body)
	}
	again := post(tap("phone-0001"), "10.0.0.2")
	if again.Code != http.StatusOK || again.Body.String() != first.Body.String() || again.Header().Get("X-Vclu-Replay") != "1" {
		t.Errorf("repeat = %d %s %v", again.Code, again.Body, again.Header())
	}
	if p := pulses(); p != "1" {
		t.Errorf("pulses after a repeat = %s, want 1", p)
	}

	post(tap("phone-0002"), "10.0.0.2")
	post(tap("phone-0001"), "10.0.0.3") // someone else's id is not this one
	post(tap(""), "10.0.0.2")
	post(tap(""), "10.0.0.2")
	if p := pulses(); p != "5" {
		t.Errorf("pulses = %s, want 5", p)
	}

	clock.Lock()
	now = now.Add(actionReplayTTL)
	clock.Unlock()
	if w := post(tap("phone-0001"), "10.0.0.2"); !strings.Contains(w.Body.String(), "pulse 6") {
		t.Errorf("after the TTL = %s", w.Body)
	}

	// asking how an action went runs nothing
	check := func(id string) string {
		return `{"widgetId":"gate","action":"tap","check":true,"actionId":"` + id + `"}`
	}
	if w := post(check("phone-0001"), "10.0.0.2"); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "pulse 6") {
		t.Errorf("check of a run action = %d %s", w.Code, w.Body)
	}
	if w := post(check("phone-0009"), "10.0.0.2"); w.Code != http.StatusNotFound {
		t.Errorf("check of an unknown action = %d %s", w.Code, w.Body)
	}
	if w := post(check("phone-0001"), "10.0.0.9"); w.Code != http.StatusNotFound {
		t.Errorf("check of someone else's action = %d", w.Code)
	}
	if w := post(`{"widgetId":"gate","action":"tap","check":true}`, "10.0.0.2"); w.Code != http.StatusBadRequest {
		t.Errorf("check without an id = %d", w.Code)
	}
	if p := pulses(); p != "6" {
		t.Errorf("pulses after checks = %s, want 6", p)
	}

	if w := post(tap("short"), "10.0.0.2"); w.Code != http.StatusBadRequest {
		t.Errorf("bad actionId = %d", w.Code)
	}
}

// A repeat that arrives while the first is still running waits for its answer; a rate-limited
// action is not remembered, so its repeat runs.
func TestActionReplayWaitsAndForgets(t *testing.T) {
	r := &actionReplays{byID: make(map[string]*actionAnswer), now: time.Now}
	prev, finish := r.claim(context.Background(), "k")
	if prev != nil {
		t.Fatal("first claim had an answer")
	}
	got := make(chan *actionAnswer)
	go func() {
		p, _ := r.claim(context.Background(), "k")
		got <- p
	}()
	select {
	case <-got:
		t.Fatal("the repeat did not wait")
	case <-time.After(50 * time.Millisecond):
	}
	finish(200, []byte(`{"success":true}`))
	if p := <-got; p.status != 200 || string(p.body) != `{"success":true}` {
		t.Errorf("repeat got %d %s", p.status, p.body)
	}

	_, finish = r.claim(context.Background(), "limited")
	finish(0, nil)
	if p, f := r.claim(context.Background(), "limited"); p != nil || f == nil {
		t.Error("an action that did not run was remembered")
	}

	ctx, cancel := context.WithCancel(context.Background())
	_, _ = r.claim(context.Background(), "stuck")
	cancel()
	if p, _ := r.claim(ctx, "stuck"); p == nil || p.status != 0 {
		t.Errorf("a cancelled wait = %+v", p)
	}
}

func TestActionReplayCap(t *testing.T) {
	now := time.Unix(0, 0)
	r := &actionReplays{byID: make(map[string]*actionAnswer), now: func() time.Time { return now }}
	for i := 0; i < actionReplayMax+10; i++ {
		now = now.Add(time.Millisecond)
		_, finish := r.claim(context.Background(), strings.Repeat("x", i+1))
		finish(200, nil)
	}
	if n := len(r.byID); n > actionReplayMax {
		t.Errorf("%d answers kept, cap %d", n, actionReplayMax)
	}
	if _, ok := r.byID["x"]; ok {
		t.Error("the oldest answer was kept")
	}
}
