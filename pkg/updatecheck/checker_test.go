package updatecheck

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Domktorymysli/vclu/pkg/config"
)

// fakeClock hands every After to the test, which fires it.
type fakeClock struct {
	mu    sync.Mutex
	now   time.Time
	waits chan wait
}

type wait struct {
	d  time.Duration
	ch chan time.Time
}

func newFakeClock() *fakeClock {
	return &fakeClock{now: time.Date(2026, 10, 29, 12, 0, 0, 0, time.UTC), waits: make(chan wait, 16)}
}

func (f *fakeClock) Now() time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.now
}

func (f *fakeClock) After(d time.Duration) <-chan time.Time {
	ch := make(chan time.Time, 1)
	f.waits <- wait{d, ch}
	return ch
}

// next waits until the checker sleeps again, i.e. the previous check is over.
func (f *fakeClock) next(t *testing.T) wait {
	t.Helper()
	select {
	case w := <-f.waits:
		return w
	case <-time.After(10 * time.Second):
		t.Fatal("checker did not schedule the next check")
		return wait{}
	}
}

func (f *fakeClock) fire(w wait) {
	f.mu.Lock()
	f.now = f.now.Add(w.d)
	now := f.now
	f.mu.Unlock()
	w.ch <- now
}

func assertFirst(t *testing.T, w wait) {
	t.Helper()
	if w.d < time.Minute || w.d > 10*time.Minute {
		t.Fatalf("first check after %v, want 1-10 min", w.d)
	}
}

func assertRegular(t *testing.T, w wait) {
	t.Helper()
	if w.d < 5*time.Hour+30*time.Minute || w.d > 6*time.Hour+30*time.Minute {
		t.Fatalf("next check after %v, want 6 h ± 30 min", w.d)
	}
}

type service struct {
	srv      *httptest.Server
	calls    atomic.Int32
	status   atomic.Int32
	body     atomic.Value // string
	reqs     chan *http.Request
	holding  atomic.Bool // answers wait for release
	release  chan struct{}
	inFlight atomic.Int32
	most     atomic.Int32
}

func newService(t *testing.T) *service {
	s := &service{reqs: make(chan *http.Request, 16), release: make(chan struct{})}
	s.status.Store(200)
	s.body.Store(`{"current":"1.7.0","update_available":false,"latest":null,"releases":[]}`)
	s.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.calls.Add(1)
		n := s.inFlight.Add(1)
		defer s.inFlight.Add(-1)
		if n > s.most.Load() {
			s.most.Store(n)
		}
		s.reqs <- r
		if s.holding.Load() {
			<-s.release
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(int(s.status.Load()))
		_, _ = fmt.Fprint(w, s.body.Load().(string))
	}))
	t.Cleanup(s.srv.Close)
	return s
}

type logs struct {
	mu    sync.Mutex
	lines []string
}

func (l *logs) logf(format string, args ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.lines = append(l.lines, fmt.Sprintf(format, args...))
}

func (l *logs) count(prefix string) int {
	l.mu.Lock()
	defer l.mu.Unlock()
	n := 0
	for _, s := range l.lines {
		if strings.HasPrefix(s, prefix) {
			n++
		}
	}
	return n
}

type env struct {
	c     *Checker
	clock *fakeClock
	svc   *service
	logs  *logs
	dir   string
}

func start(t *testing.T, enabled func() bool) *env {
	t.Helper()
	e := &env{clock: newFakeClock(), svc: newService(t), logs: &logs{}, dir: t.TempDir()}
	e.c = New(Options{Dir: e.dir, Version: "1.7.0-907bb3a", Enabled: enabled, BaseURL: e.svc.srv.URL, Clock: e.clock, Logf: e.logs.logf})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { e.c.Run(ctx); close(done) }()
	t.Cleanup(func() { cancel(); <-done })
	return e
}

func TestRequestShape(t *testing.T) {
	e := start(t, nil)
	e.clock.fire(e.clock.next(t))
	r := <-e.svc.reqs
	q := r.URL.Query()
	keys := make([]string, 0, len(q))
	for k := range q {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	if r.Method != "GET" || r.URL.Path != "/v1/check" || strings.Join(keys, ",") != "arch,id,os,version" {
		t.Fatalf("request %s %s", r.Method, r.URL)
	}
	id, _ := os.ReadFile(filepath.Join(e.dir, IDFile))
	if q.Get("id") != strings.TrimSpace(string(id)) || !uuidRe.MatchString(q.Get("id")) {
		t.Errorf("id %q, file %q", q.Get("id"), id)
	}
	if q.Get("version") != "1.7.0-907bb3a" || q.Get("os") == "" || q.Get("arch") != Arch() {
		t.Errorf("query %v", q)
	}
	if ua := r.Header.Get("User-Agent"); ua != "vclu/1.7.0-907bb3a" {
		t.Errorf("User-Agent %q", ua)
	}
	for k := range r.Header { // only Go's defaults besides the User-Agent
		if k != "User-Agent" && k != "Accept-Encoding" {
			t.Errorf("extra header %s: %v", k, r.Header[k])
		}
	}
}

func TestArch(t *testing.T) {
	for _, c := range []struct{ goarch, goarm, want string }{
		{"arm", "7", "armv7"}, {"arm", "6", "armv6"}, {"arm", "7,softfloat", "armv7"},
		{"arm64", "", "arm64"}, {"amd64", "", "amd64"}, {"arm", "", ""},
	} {
		if got := archFor(c.goarch, c.goarm); got != c.want {
			t.Errorf("archFor(%q, %q) = %q, want %q", c.goarch, c.goarm, got, c.want)
		}
	}
}

func TestScheduleNoRetry(t *testing.T) {
	e := start(t, nil)
	w := e.clock.next(t)
	assertFirst(t, w)
	e.svc.status.Store(503)
	e.clock.fire(w)
	w = e.clock.next(t) // after the failure: the regular interval, no retry
	assertRegular(t, w)
	if n := e.svc.calls.Load(); n != 1 {
		t.Fatalf("%d requests after one failed check", n)
	}
	e.svc.status.Store(429)
	e.clock.fire(w)
	assertRegular(t, e.clock.next(t))
	if n := e.svc.calls.Load(); n != 2 {
		t.Fatalf("%d requests, want 2", n)
	}
	if e.logs.count("[DEBUG]") != 2 || len(e.logs.lines) != 2 {
		t.Errorf("failures are debug lines: %q", e.logs.lines)
	}
}

func TestOneCheckInFlight(t *testing.T) {
	e := start(t, nil)
	e.svc.holding.Store(true)
	e.clock.fire(e.clock.next(t))
	<-e.svc.reqs
	e.c.Reschedule() // must not start a second check meanwhile
	select {
	case w := <-e.clock.waits:
		t.Fatalf("scheduled %v while a check is in flight", w.d)
	default:
	}
	close(e.svc.release)
	assertRegular(t, e.clock.next(t))
	assertFirst(t, e.clock.next(t)) // the Reschedule, after the check ended
	if n := e.svc.most.Load(); n != 1 {
		t.Errorf("%d checks in flight", n)
	}
}

func TestOffSendsNothing(t *testing.T) {
	t.Run("file", func(t *testing.T) {
		var dir string
		e := start(t, func() bool { return config.UpdateCheckEnabled(dir) })
		dir = e.dir
		off := false
		if err := config.UpdateMasterConfig(dir, func(c *config.VCLUConfig) error {
			c.UpdateCheck = &config.VCLUUpdateCheckConfig{Enabled: &off}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		for range 3 {
			e.clock.fire(e.clock.next(t))
		}
		e.clock.next(t)
		if n := e.svc.calls.Load(); n != 0 {
			t.Fatalf("%d requests while off", n)
		}
		if _, err := os.Stat(filepath.Join(dir, IDFile)); !os.IsNotExist(err) {
			t.Errorf("instance id created while off: %v", err)
		}
	})
	t.Run("env", func(t *testing.T) {
		t.Setenv(config.UpdateCheckEnv, "off")
		var dir string
		e := start(t, func() bool { return config.UpdateCheckEnabled(dir) })
		dir = e.dir // no file: on, but the environment wins
		for range 3 {
			e.clock.fire(e.clock.next(t))
		}
		e.clock.next(t)
		if n := e.svc.calls.Load(); n != 0 {
			t.Fatalf("%d requests with %s=off", n, config.UpdateCheckEnv)
		}
	})
}

func TestTurnOffAndOnAtRunTime(t *testing.T) {
	var on atomic.Bool
	on.Store(true)
	e := start(t, on.Load)
	e.clock.fire(e.clock.next(t))
	w := e.clock.next(t)
	on.Store(false)
	e.clock.fire(w)
	e.clock.next(t)
	if n := e.svc.calls.Load(); n != 1 {
		t.Fatalf("%d requests, the one after turning off must not happen", n)
	}
	on.Store(true)
	e.c.Reschedule()
	w = e.clock.next(t) // the 6 h wait is dropped for a new first delay
	assertFirst(t, w)
	e.clock.fire(w)
	e.clock.next(t)
	if n := e.svc.calls.Load(); n != 2 {
		t.Fatalf("%d requests after turning on, want 2", n)
	}
}

func TestKeepsLastGoodAnswer(t *testing.T) {
	e := start(t, nil)
	e.svc.body.Store(`{"current":"1.6.0","update_available":true,"new_field":{"x":1},
		"latest":{"version":"1.8.0","name":"vCLU v1.8.0","published_at":"2026-10-28T10:00:00Z","url":"https://github.com/Domktorymysli/vclu/releases/tag/v1.8.0","assets":[]},
		"releases":[{"version":"1.8.0","url":"https://github.com/Domktorymysli/vclu/releases/tag/v1.8.0","notes":{"en":"**New**","pl":"**Nowe**"},"changelog":"long"},
		{"version":"1.7.0","url":"https://github.com/Domktorymysli/vclu/releases/tag/v1.7.0","changelog":"old shape"}]}`)
	e.clock.fire(e.clock.next(t))
	w := e.clock.next(t)
	a, at := e.c.Last()
	if a == nil || !a.UpdateAvailable || a.Latest.Version != "1.8.0" || len(a.Releases) != 2 || a.Releases[0].Notes["pl"] != "**Nowe**" || a.Releases[1].Notes != nil {
		t.Fatalf("answer %+v", a)
	}
	for _, bad := range []struct {
		status int
		body   string
	}{{500, `{"error":"x"}`}, {200, `not json`}, {429, ``},
		// valid but over maxBodySize: cut off, so it fails to decode
		{200, `{"current":"1.6.0","update_available":true,"latest":{"version":"9.9.9","notes":{"en":"` + strings.Repeat("x", maxBodySize) + `"}},"releases":[]}`}} {
		e.svc.status.Store(int32(bad.status))
		e.svc.body.Store(bad.body)
		e.clock.fire(w)
		w = e.clock.next(t)
		got, gotAt := e.c.Last()
		if got != a || !gotAt.Equal(at) {
			t.Fatalf("after %d %.40q the last good answer is gone: %+v", bad.status, bad.body, got)
		}
	}
}

func TestBadRequestLoggedOnce(t *testing.T) {
	e := start(t, nil)
	e.svc.status.Store(400)
	e.svc.body.Store(`{"error":"invalid version"}`)
	e.clock.fire(e.clock.next(t))
	for range 2 {
		e.clock.fire(e.clock.next(t))
	}
	e.clock.next(t)
	if n := e.logs.count("[Update] the update service refused"); n != 1 {
		t.Errorf("400 logged %d times: %q", n, e.logs.lines)
	}
	if a, _ := e.c.Last(); a != nil {
		t.Errorf("a 400 is no answer: %+v", a)
	}
}

func TestRedirectFailsCheck(t *testing.T) {
	var other atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		other.Add(1)
		_, _ = fmt.Fprint(w, `{"current":"1.7.0","update_available":true,"latest":{"version":"9.9.9"},"releases":[]}`)
	}))
	defer target.Close()
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL+r.URL.RequestURI(), http.StatusFound)
	}))
	defer redirect.Close()

	clock := newFakeClock()
	c := New(Options{Dir: t.TempDir(), Version: "1.7.0", BaseURL: redirect.URL, Clock: clock, Logf: (&logs{}).logf})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go c.Run(ctx)
	clock.fire(clock.next(t))
	clock.next(t)
	if a, _ := c.Last(); a != nil || other.Load() != 0 {
		t.Fatalf("redirect followed: answer %+v, %d requests elsewhere", a, other.Load())
	}
}
