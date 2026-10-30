package web

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Domktorymysli/vclu/pkg/config"
	"github.com/Domktorymysli/vclu/pkg/updatecheck"
)

// firstNowClock fires the first wait at once and records every later one.
type firstNowClock struct {
	calls atomic.Int32
	waits chan time.Duration
}

func (f *firstNowClock) Now() time.Time { return time.Date(2026, 10, 29, 12, 0, 0, 0, time.UTC) }

func (f *firstNowClock) After(d time.Duration) <-chan time.Time {
	ch := make(chan time.Time, 1)
	if f.calls.Add(1) == 1 {
		ch <- f.Now()
	} else {
		f.waits <- d
	}
	return ch
}

const updateAnswer = `{"current":"1.6.0","update_available":true,"extra":1,
	"latest":{"version":"1.8.0","name":"vCLU v1.8.0","published_at":"2026-10-28T10:00:00Z","url":"https://github.com/Domktorymysli/vclu/releases/tag/v1.8.0",
	  "notes":{"en":"**New**"},"summary":{"en":{"highlights":"a thermostat"}}},
	"releases":[
	 {"version":"1.8.0","published_at":"2026-10-28T10:00:00Z","url":"https://github.com/Domktorymysli/vclu/releases/tag/v1.8.0",
	  "notes":{"en":"**New** <script>alert(1)</script>","pl":"**Nowe** [x](javascript:alert(1))"},
	  "summary":{"en":{"highlights":"a **thermostat** <b>x</b>","before_update":["rename ` + "`no-access`" + `","[read](https://x.y/a)"]}}},
	 {"version":"1.7.0","published_at":"2026-10-24T22:44:48Z","url":"https://github.com/Domktorymysli/vclu/releases/tag/v1.7.0","changelog":"old shape","assets":[]}]}`

// withUpdateChecker gives e a checker that has asked a fake service once.
func withUpdateChecker(t *testing.T, e *historyEnv) *firstNowClock {
	t.Helper()
	svc := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, updateAnswer)
	}))
	t.Cleanup(svc.Close)
	clock := &firstNowClock{waits: make(chan time.Duration, 8)}
	c := updatecheck.New(updatecheck.Options{
		Dir: e.dir, Version: "1.6.0-1c0df52", BaseURL: svc.URL, Clock: clock,
		Enabled: func() bool { return config.UpdateCheckEnabled(e.dir) },
	})
	e.s.SetUpdateChecker(c)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { c.Run(ctx); close(done) }()
	t.Cleanup(func() { cancel(); <-done })
	select {
	case <-clock.waits: // the check is over
	case <-time.After(10 * time.Second):
		t.Fatal("no check")
	}
	return clock
}

func TestUpdateAPI_NeedsLogin(t *testing.T) {
	e := newHistoryEnv(t)
	withUpdateChecker(t, e)
	if w := e.do(t, "GET", "/api/update", "", ""); w.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous: %d", w.Code)
	}
	if w := e.do(t, "PUT", "/api/update/settings", `{"enabled":false}`, ""); w.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous save: %d", w.Code)
	}
	for _, auth := range []string{"session", "apikey"} {
		if w := e.do(t, "GET", "/api/update", "", auth); w.Code != http.StatusOK {
			t.Errorf("%s: %d", auth, w.Code)
		}
	}
}

func TestUpdateAPI_Shape(t *testing.T) {
	e := newHistoryEnv(t)
	withUpdateChecker(t, e)
	for _, lang := range []string{"en", "pl"} {
		w := e.do(t, "GET", "/api/update?lang="+lang, "", "session")
		var res struct {
			Enabled         bool             `json:"enabled"`
			CheckedAt       *time.Time       `json:"checked_at"`
			Current         string           `json:"current"`
			UpdateAvailable bool             `json:"update_available"`
			Latest          map[string]any   `json:"latest"`
			Releases        []map[string]any `json:"releases"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &res); err != nil || w.Code != 200 {
			t.Fatalf("%s: %d %s", lang, w.Code, w.Body)
		}
		if !res.Enabled || res.CheckedAt == nil || res.Current != "1.6.0-1c0df52" || !res.UpdateAvailable ||
			res.Latest["version"] != "1.8.0" || len(res.Releases) != 2 {
			t.Fatalf("%s: %s", lang, w.Body)
		}
		// the panel gets rendered HTML only, not the service's markdown
		for _, r := range append(res.Releases, res.Latest) {
			for _, raw := range []string{"notes", "summary", "name"} {
				if _, ok := r[raw]; ok {
					t.Errorf("%s: raw %q passed on: %v", lang, raw, r)
				}
			}
		}
		if res.Latest["url"] == nil || res.Latest["published_at"] == nil {
			t.Errorf("%s latest: %v", lang, res.Latest)
		}
		html, _ := res.Releases[0]["notes_html"].(string)
		switch lang {
		case "en":
			if html != "<p><strong>New</strong> &lt;script&gt;alert(1)&lt;/script&gt;</p>" {
				t.Errorf("en notes %q", html)
			}
		case "pl":
			if html != "<p><strong>Nowe</strong> [x](javascript:alert(1))</p>" {
				t.Errorf("pl notes %q", html)
			}
		}
		// no Polish summary: the English one
		if h := res.Releases[0]["highlights_html"]; h != "a <strong>thermostat</strong> &lt;b&gt;x&lt;/b&gt;" {
			t.Errorf("%s highlights %q", lang, h)
		}
		bu := fmt.Sprint(res.Releases[0]["before_update_html"])
		if bu != `[rename <code>no-access</code> <a href="https://x.y/a" target="_blank" rel="noopener noreferrer">read</a>]` {
			t.Errorf("%s before update %s", lang, bu)
		}
		if _, ok := res.Releases[1]["notes_html"]; ok || res.Releases[1]["url"] == "" {
			t.Errorf("a release without notes: only its version and url: %v", res.Releases[1])
		}
		if _, ok := res.Releases[1]["changelog"]; ok {
			t.Errorf("unknown fields passed on: %v", res.Releases[1])
		}
	}
	if w := e.do(t, "GET", "/api/update", "", "session"); w.Header().Get("Cache-Control") != "no-store" {
		t.Errorf("Cache-Control %q", w.Header().Get("Cache-Control"))
	}
}

func TestUpdateAPI_BeforeTheFirstAnswer(t *testing.T) {
	e := newHistoryEnv(t)
	e.s.SetUpdateChecker(updatecheck.New(updatecheck.Options{Dir: e.dir, Version: "dev", BaseURL: "http://127.0.0.1:1"}))
	w := e.do(t, "GET", "/api/update", "", "session")
	want := `{"enabled":true,"checked_at":null,"current":"dev","update_available":false,"latest":null,"releases":[]}`
	if strings.TrimSpace(w.Body.String()) != want {
		t.Errorf("got %s\nwant %s", w.Body, want)
	}
}

func TestUpdateAPI_Off(t *testing.T) {
	e := newHistoryEnv(t)
	clock := withUpdateChecker(t, e)

	w := e.do(t, "PUT", "/api/update/settings", `{"enabled":false}`, "session")
	if w.Code != 200 || strings.TrimSpace(w.Body.String()) != `{"enabled":false,"forced_off":false}` {
		t.Fatalf("save off: %d %s", w.Code, w.Body)
	}
	if w := e.do(t, "GET", "/api/update", "", "session"); strings.TrimSpace(w.Body.String()) != `{"enabled":false}` {
		t.Errorf("off: %s", w.Body)
	}
	mc, _ := config.ReadMasterConfig(e.dir)
	if mc.UpdateCheckOn() || mc.Auth == nil || mc.Auth.Username != "tytus" {
		t.Errorf("not saved, or other sections lost: %+v", mc)
	}
	select {
	case d := <-clock.waits:
		t.Errorf("turning off rescheduled a check in %v", d)
	default:
	}

	// on again: a check within minutes, not at the next 6 h tick
	w = e.do(t, "PUT", "/api/update/settings", `{"enabled":true}`, "session")
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"enabled":true`) {
		t.Fatalf("save on: %d %s", w.Code, w.Body)
	}
	select {
	case d := <-clock.waits:
		if d < time.Minute || d > 10*time.Minute {
			t.Errorf("next check in %v after turning on", d)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("turning on did not reschedule")
	}

	t.Setenv(config.UpdateCheckEnv, "off")
	if w := e.do(t, "GET", "/api/update", "", "session"); strings.TrimSpace(w.Body.String()) != `{"enabled":false}` {
		t.Errorf("%s=off: %s", config.UpdateCheckEnv, w.Body)
	}
	if w := e.do(t, "GET", "/settings", "", "session"); !strings.Contains(w.Body.String(), `id="updateCheckEnabled"`) ||
		!strings.Contains(w.Body.String(), "VCLU_UPDATE_CHECK=off") {
		t.Errorf("settings page without the switch or the override note")
	}
	if w := e.do(t, "PUT", "/api/update/settings", `{}`, "session"); w.Code != http.StatusBadRequest {
		t.Errorf("empty body: %d", w.Code)
	}
}
