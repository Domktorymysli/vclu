package web

import (
	"archive/zip"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Domktorymysli/vclu/pkg/config"
	"github.com/Domktorymysli/vclu/pkg/notify"
	"github.com/Domktorymysli/vclu/pkg/updatecheck"
)

func newNotifyEnv(t *testing.T) (*historyEnv, *notify.Store) {
	t.Helper()
	e := newHistoryEnv(t)
	n := notify.New(notify.Options{Path: notify.Path(e.dir), ConfigDir: e.dir, Debounce: time.Hour})
	t.Cleanup(func() { _ = n.Close() })
	e.s.SetNotifier(n)
	return e, n
}

func postLogin(e *historyEnv, ip, user, pass string, hdr map[string]string) *httptest.ResponseRecorder {
	form := url.Values{"username": {user}, "password": {pass}}
	req := httptest.NewRequest("POST", "/login", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.RemoteAddr = ip + ":40000"
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	e.s.router.ServeHTTP(w, req)
	return w
}

// Five wrong logins from an address lock it for 30 s, the next lock twice as long; the right
// password clears it; another address is not locked. Entries name the address, never the user.
func TestLoginLimit(t *testing.T) {
	e, n := newNotifyEnv(t)
	now := time.Unix(1_800_000_000, 0)
	e.s.loginLocks.now = func() time.Time { return now }
	for i := 0; i < 4; i++ {
		if w := postLogin(e, "10.0.0.5", "hunter2-typed-here", "x", nil); w.Code != http.StatusOK {
			t.Fatalf("try %d: %d", i, w.Code)
		}
	}
	w := postLogin(e, "10.0.0.5", "tytus", "x", nil)
	if w.Code != http.StatusTooManyRequests || !strings.Contains(w.Body.String(), `id="loginLocked"`) || !strings.Contains(w.Body.String(), "30 s") {
		t.Fatalf("fifth: %d", w.Code)
	}
	if w := postLogin(e, "10.0.0.5", "tytus", "secret", nil); w.Code != http.StatusTooManyRequests {
		t.Fatalf("the right password while locked: %d", w.Code)
	}
	if w := postLogin(e, "10.0.0.6", "tytus", "secret", nil); w.Code != http.StatusFound {
		t.Fatalf("another address: %d", w.Code)
	}
	now = now.Add(31 * time.Second)
	for i := 0; i < 5; i++ {
		postLogin(e, "10.0.0.5", "tytus", "x", nil)
	}
	if got := e.s.loginLocks.remaining("10.0.0.5"); got != time.Minute {
		t.Errorf("second lock %v, want 1m", got)
	}
	now = now.Add(61 * time.Second)
	if w := postLogin(e, "10.0.0.5", "tytus", "secret", nil); w.Code != http.StatusFound {
		t.Fatalf("after the lock: %d", w.Code)
	}
	for i := 0; i < 4; i++ {
		postLogin(e, "10.0.0.5", "tytus", "x", nil)
	}
	if e.s.loginLocks.remaining("10.0.0.5") != 0 {
		t.Error("a login did not clear the count")
	}

	list, _, _ := n.List()
	b, _ := json.Marshal(list)
	if strings.Contains(string(b), "hunter2") || strings.Contains(string(b), "tytus") {
		t.Errorf("a user name in the entries: %s", b)
	}
	var login, lock *notify.Entry
	for i := range list {
		switch list[i].Key {
		case "login:10.0.0.5":
			login = &list[i]
		case "lock:login:10.0.0.5":
			lock = &list[i]
		}
	}
	if login == nil || login.Count != 14 || login.Params["ip"] != "10.0.0.5" || login.Source != notify.SourceLogin || login.Link != "/settings" {
		t.Errorf("login entry %+v", login)
	}
	if lock == nil || lock.Count != 2 || lock.Params["until"] == "" || lock.Msg != notify.MsgLockLogin {
		t.Errorf("lock entry %+v", lock)
	}
}

// A login posted from another site's page is refused before it counts.
func TestLoginCrossSiteRefused(t *testing.T) {
	e, n := newNotifyEnv(t)
	for _, h := range []map[string]string{
		{"Sec-Fetch-Site": "cross-site"},
		{"Origin": "http://evil.example"},
		{"Origin": "null"},
	} {
		for i := 0; i < 6; i++ {
			if w := postLogin(e, "10.0.0.7", "tytus", "secret", h); w.Code != http.StatusForbidden {
				t.Fatalf("%v: %d", h, w.Code)
			}
		}
	}
	if list, _, _ := n.List(); len(list) != 0 || e.s.loginLocks.remaining("10.0.0.7") != 0 {
		t.Fatalf("counted: %+v", list)
	}
	if w := postLogin(e, "10.0.0.7", "tytus", "secret", map[string]string{"Origin": "http://example.com", "Sec-Fetch-Site": "same-origin"}); w.Code != http.StatusFound {
		t.Errorf("same origin: %d", w.Code)
	}
}

// Behind a proxy that rewrites Host the browser's Sec-Fetch-Site decides; Origin is compared
// only without it.
func TestLoginCrossSiteBehindProxy(t *testing.T) {
	e, _ := newNotifyEnv(t)
	for _, c := range []struct {
		hdr  map[string]string
		want int
	}{
		{map[string]string{"Sec-Fetch-Site": "same-origin", "Origin": "https://dom.example.org"}, http.StatusFound},
		{map[string]string{"Sec-Fetch-Site": "none"}, http.StatusFound},
		{map[string]string{"Sec-Fetch-Site": "same-site", "Origin": "http://example.com"}, http.StatusForbidden},
		{map[string]string{"Origin": "https://dom.example.org"}, http.StatusForbidden},
		{map[string]string{"Origin": "http://example.com"}, http.StatusFound},
	} {
		if w := postLogin(e, "10.0.0.8", "tytus", "secret", c.hdr); w.Code != c.want {
			t.Errorf("%v: %d, want %d", c.hdr, w.Code, c.want)
		}
	}
}

// Wrong logins posted at the same time get no more tries than are left before the lock.
func TestLoginLimitConcurrent(t *testing.T) {
	e, n := newNotifyEnv(t)
	var wg sync.WaitGroup
	codes := make(chan int, 20)
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			codes <- postLogin(e, "10.0.0.9", "tytus", "x", nil).Code
		}()
	}
	wg.Wait()
	close(codes)
	wrong := 0
	for c := range codes {
		if c == http.StatusOK {
			wrong++
		}
	}
	list, _, _ := n.List()
	for _, en := range list {
		if en.Key == "login:10.0.0.9" && en.Count > lockTries {
			t.Errorf("%d tries checked, %d allowed", en.Count, lockTries)
		}
	}
	if wrong > lockTries-1 {
		t.Errorf("%d plain wrong answers", wrong)
	}
	if e.s.loginLocks.remaining("10.0.0.9") == 0 {
		t.Error("not locked")
	}
}

// A count is forgotten after an hour without tries; the error page keeps the CSRF token.
func TestLoginCountForgotten(t *testing.T) {
	e, _ := newNotifyEnv(t)
	now := time.Unix(1_800_000_000, 0)
	e.s.loginLocks.now = func() time.Time { return now }
	for i := 0; i < 3; i++ {
		postLogin(e, "10.0.0.10", "tytus", "x", nil)
	}
	w := postLogin(e, "10.0.0.10", "tytus", "x", map[string]string{"Cookie": sessionCookieName + "=" + e.session})
	if tok := e.s.sessions.getCSRF(e.session); tok == "" || !strings.Contains(w.Body.String(), `name="_csrf" value="`+tok+`"`) {
		t.Error("the error page has no CSRF token")
	}
	now = now.Add(time.Hour + time.Second)
	postLogin(e, "10.0.0.10", "tytus", "x", nil)
	if e.s.loginLocks.remaining("10.0.0.10") != 0 {
		t.Fatal("an old count locked")
	}
	for i := 0; i < 3; i++ {
		postLogin(e, "10.0.0.10", "tytus", "x", nil)
	}
	if w := postLogin(e, "10.0.0.10", "tytus", "x", nil); w.Code != http.StatusTooManyRequests {
		t.Errorf("fifth after the reset: %d", w.Code)
	}
}

func TestNotificationsAPI(t *testing.T) {
	e, n := newNotifyEnv(t)
	n.Add(notify.Entry{Kind: notify.KindEvent, Source: notify.SourceLogin, Level: notify.LevelWarning, Key: "login:1.2.3.4",
		Msg: notify.MsgLogin, Params: map[string]string{"ip": "1.2.3.4"}, Link: "/settings"})
	n.Add(notify.Entry{Kind: notify.KindState, Source: notify.SourceModule, Level: notify.LevelError, Key: "module:a.lua",
		Msg: notify.MsgModule, Params: map[string]string{"module": "a.lua"}, Detail: "boom"})

	if w := e.do(t, "GET", "/api/notifications", "", ""); w.Code != http.StatusUnauthorized {
		t.Fatalf("no login: %d", w.Code)
	}
	w := e.do(t, "GET", "/api/notifications", "", "apikey")
	var got struct {
		Entries []map[string]any `json:"entries"`
		Unread  int              `json:"unread"`
		Worst   string           `json:"worst"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil || w.Code != 200 || got.Unread != 2 || got.Worst != "error" || len(got.Entries) != 2 {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	for _, k := range []string{"id", "kind", "source", "level", "key", "title", "body", "detail", "msg", "params", "link", "count", "first", "last", "read"} {
		if _, ok := got.Entries[0][k]; !ok {
			t.Errorf("entry without %q: %v", k, got.Entries[0])
		}
	}
	if _, ok := got.Entries[1]["ip"]; ok || got.Entries[0]["detail"] != "boom" {
		t.Errorf("entries %v", got.Entries)
	}

	// a session needs the CSRF token
	req := httptest.NewRequest("POST", "/api/notifications/read", strings.NewReader(`{"all":true}`))
	req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: e.session})
	rw := httptest.NewRecorder()
	e.s.router.ServeHTTP(rw, req)
	if rw.Code != http.StatusForbidden {
		t.Fatalf("no CSRF: %d", rw.Code)
	}
	if w := e.do(t, "POST", "/api/notifications/read", `{}`, "session"); w.Code != http.StatusBadRequest {
		t.Errorf("empty read: %d", w.Code)
	}
	if w := e.do(t, "POST", "/api/notifications/read", `{"all":true}`, "session"); w.Code != 200 || !strings.Contains(w.Body.String(), `"unread":0`) {
		t.Fatalf("read: %d %s", w.Code, w.Body)
	}
	if w := e.do(t, "POST", "/api/notifications/clear", ``, "session"); w.Code != 200 || strings.Contains(w.Body.String(), "login:") || !strings.Contains(w.Body.String(), "module:a.lua") {
		t.Fatalf("clear: %d %s", w.Code, w.Body)
	}
	if w := e.do(t, "POST", "/api/notifications/mute", `{"key":"module:a.lua","for":"7d"}`, "session"); w.Code != 200 || strings.Contains(w.Body.String(), "module:a.lua") {
		t.Fatalf("mute: %d %s", w.Code, w.Body)
	}
	if w := e.do(t, "POST", "/api/notifications/mute", `{"key":"x","for":"soon"}`, "session"); w.Code != http.StatusBadRequest {
		t.Errorf("bad for: %d", w.Code)
	}
	// the new-version window hides a version for good
	if w := e.do(t, "POST", "/api/notifications/mute", `{"key":"update:1.13.0","for":"forever"}`, "session"); w.Code != 200 {
		t.Fatalf("mute forever: %d", w.Code)
	}
	if w := e.do(t, "POST", "/api/notifications/hide-update", `{"version":"1.13.0"}`, "session"); w.Code != http.StatusNotFound {
		t.Errorf("hide-update still there: %d", w.Code)
	}
	w = e.do(t, "GET", "/api/notifications/settings", "", "session")
	var set notify.Settings
	if err := json.Unmarshal(w.Body.Bytes(), &set); err != nil || len(set.Sources) != len(notify.Sources) || set.Sources[3].ID != "log" || set.Sources[3].Enabled ||
		set.Sources[3].Default || !set.Sources[0].Default ||
		len(set.Muted) != 2 || set.Muted[0].Key != "module:a.lua" || set.Muted[0].Title != "Module a.lua did not load" || set.Muted[1].Until != "forever" {
		t.Fatalf("settings %d %s", w.Code, w.Body)
	}
	if w := e.do(t, "PUT", "/api/notifications/settings", `{"sources":{"nope":true}}`, "session"); w.Code != http.StatusBadRequest {
		t.Errorf("unknown source: %d", w.Code)
	}
	if w := e.do(t, "PUT", "/api/notifications/settings", `{"disabled":[]}`, "session"); w.Code != http.StatusBadRequest {
		t.Errorf("the old shape: %d", w.Code)
	}
	if w := e.do(t, "PUT", "/api/notifications/settings", `{"sources":{"log":true}}`, "session"); w.Code != 200 || strings.Contains(w.Body.String(), `"enabled":false`) {
		t.Errorf("log on: %d %s", w.Code, w.Body)
	}
	if w := e.do(t, "DELETE", "/api/notifications/mute", `{"key":"update:1.13.0"}`, "session"); w.Code != 200 || strings.Contains(w.Body.String(), "update:1.13.0") {
		t.Errorf("unmute: %d %s", w.Code, w.Body)
	}
	mc, _ := config.ReadMasterConfig(e.dir)
	if mc.Notifications == nil || fmt.Sprint(mc.Notifications.Sources) != "map[log:true]" || mc.Notifications.Muted["module:a.lua"].Until == "" ||
		mc.Notifications.Muted["module:a.lua"].Title != "Module a.lua did not load" {
		t.Errorf(".vclu.json %+v", mc.Notifications)
	}
	if mc.Auth == nil || mc.Auth.Username != "tytus" {
		t.Error("the settings write lost another section")
	}
}

func TestNotificationsAPIWithoutStore(t *testing.T) {
	e := newHistoryEnv(t)
	if w := e.do(t, "GET", "/api/notifications", "", "apikey"); w.Code != 200 || w.Body.String() != `{"entries":[],"unread":0,"worst":""}` {
		t.Errorf("%d %s", w.Code, w.Body)
	}
	if w := e.do(t, "POST", "/api/notifications/read", `{"all":true}`, "apikey"); w.Code != http.StatusServiceUnavailable {
		t.Errorf("%d", w.Code)
	}
	postLogin(e, "10.0.0.1", "tytus", "x", nil) // no store: nothing to add to
}

// The latest version is one state: a newer one replaces it, the running one resolves it.
func TestUpdateAnswered(t *testing.T) {
	e, n := newNotifyEnv(t)
	answer := func(cur, latest string) *updatecheck.Answer {
		a := &updatecheck.Answer{Current: cur}
		if latest != "" {
			a.UpdateAvailable, a.Latest = true, &updatecheck.Release{Version: latest}
		}
		return a
	}
	keys := func() string {
		list, _, _ := n.List()
		var k []string
		for _, e := range list {
			k = append(k, e.Key)
		}
		return strings.Join(k, ",")
	}
	e.s.pluginCheck.Store(true) // no registries in tests
	e.s.UpdateAnswered(answer("1.12.0", "1.13.0"))
	e.s.UpdateAnswered(answer("1.12.0", "1.13.0"))
	if got := keys(); got != "update:1.13.0" {
		t.Fatalf("%s", got)
	}
	if list, _, _ := n.List(); list[0].Link != "#update" {
		t.Errorf("link %q", list[0].Link)
	}
	e.s.UpdateAnswered(answer("1.12.0", "1.14.0"))
	if got := keys(); got != "update:1.14.0" {
		t.Fatalf("replaced: %s", got)
	}
	e.s.UpdateAnswered(answer("1.14.0", ""))
	if got := keys(); got != "" {
		t.Fatalf("resolved: %s", got)
	}
	e.s.UpdateAnswered(answer("1.15.0", "1.15.0"))
	if got := keys(); got != "" {
		t.Fatalf("the running version: %s", got)
	}
	// a restart on the new version resolves the old notice before any check
	e.s.UpdateAnswered(answer("1.12.0", "1.13.0"))
	e.s.SetVersion("1.13.0")
	e.s.SetNotifier(n)
	if got := keys(); got != "" {
		t.Fatalf("after the update: %s", got)
	}
}

// Turning the check off, in the settings or by the environment at start, resolves the
// new-version notice: nothing would resolve it later.
func TestUpdateCheckOffResolves(t *testing.T) {
	e, n := newNotifyEnv(t)
	add := func() {
		n.Add(notify.Entry{Kind: notify.KindState, Source: notify.SourceUpdate, Key: "update:9.0.0", Msg: notify.MsgUpdate})
		n.Add(notify.Entry{Kind: notify.KindState, Source: notify.SourceModule, Key: "module:a.lua", Msg: notify.MsgModule})
	}
	keys := func() string {
		list, _, _ := n.List()
		var k []string
		for _, e := range list {
			k = append(k, e.Key)
		}
		return strings.Join(k, ",")
	}
	add()
	if w := e.do(t, "PUT", "/api/update/settings", `{"enabled":false}`, "session"); w.Code != 200 {
		t.Fatalf("%d", w.Code)
	}
	if got := keys(); got != "module:a.lua" {
		t.Fatalf("after off: %s", got)
	}
	if w := e.do(t, "PUT", "/api/update/settings", `{"enabled":true}`, "session"); w.Code != 200 {
		t.Fatalf("%d", w.Code)
	}
	add()
	t.Setenv(config.UpdateCheckEnv, "off")
	e.s.SetNotifier(n)
	if got := keys(); got != "module:a.lua" {
		t.Fatalf("forced off at start: %s", got)
	}
}

// A plugin is in the notice for a newer version only, and leaves it once installed or
// uninstalled; the notice goes with the last one.
func TestPluginNotices(t *testing.T) {
	e, n := newNotifyEnv(t)
	if err := writeTestConfig(e.dir, &config.VCLUConfig{Plugins: &config.VCLUPluginsConfig{Installed: []config.InstalledPlugin{
		{ID: "@vclu/weather", Version: "1.2.0"}, {ID: "@vclu/old", Version: "2.0.0"}}}}); err != nil {
		t.Fatal(err)
	}
	found := []availablePlugin{
		{Meta: PluginMetadata{Name: "Weather", Version: "1.10.0"}, FullID: "@vclu/weather", Installed: true, InstalledVersion: "1.2.0", UpdateAvailable: newerVersion("1.10.0", "1.2.0")},
		{Meta: PluginMetadata{Name: "Old", Version: "1.9.0"}, FullID: "@vclu/old", Installed: true, InstalledVersion: "2.0.0", UpdateAvailable: newerVersion("1.9.0", "2.0.0")},
		{Meta: PluginMetadata{Name: "New", Version: "1.0.0"}, FullID: "@vclu/new"},
	}
	e.s.pluginNotices(found)
	list, _, _ := n.List()
	if len(list) != 1 || list[0].Key != pluginUpdatesKey || list[0].Title != "Plugin updates: 1 (Weather)" || list[0].Link != "/plugins" ||
		list[0].Detail != "Weather 1.10.0" || list[0].Params["count"] != "1" {
		t.Fatalf("%+v", list)
	}
	if err := writeTestConfig(e.dir, &config.VCLUConfig{Plugins: &config.VCLUPluginsConfig{Installed: []config.InstalledPlugin{
		{ID: "@vclu/weather", Version: "1.10.0"}}}}); err != nil {
		t.Fatal(err)
	}
	e.s.resolvePluginNotices()
	if list, _, _ := n.List(); len(list) != 0 {
		t.Fatalf("installed, still %+v", list)
	}
}

// 30 plugins with updates are one entry and one delivery. It stays read while plugins only
// leave it (or a registry does not answer), and is unread again when a new one comes.
func TestPluginUpdatesOneEntry(t *testing.T) {
	e, n := newNotifyEnv(t)
	var deliveries []notify.Delivery
	n.Subscribe(func(d notify.Delivery) { deliveries = append(deliveries, d) })
	var installed []config.InstalledPlugin
	var found []availablePlugin
	for i := 0; i < 31; i++ {
		id := fmt.Sprintf("@vclu/p%02d", i)
		installed = append(installed, config.InstalledPlugin{ID: id, Version: "1.0.0"})
		if i < 30 {
			found = append(found, availablePlugin{Meta: PluginMetadata{Name: fmt.Sprintf("P%02d", i), Version: "1.1.0"}, FullID: id,
				Installed: true, InstalledVersion: "1.0.0", UpdateAvailable: true})
		}
	}
	if err := writeTestConfig(e.dir, &config.VCLUConfig{Plugins: &config.VCLUPluginsConfig{Installed: installed}}); err != nil {
		t.Fatal(err)
	}
	e.s.pluginNotices(found)
	list, unread, _ := n.List()
	if len(list) != 1 || unread != 1 || len(deliveries) != 1 {
		t.Fatalf("%d entries, %d deliveries", len(list), len(deliveries))
	}
	p := list[0].Params
	if p["count"] != "30" || p["plugins"] != "P00, P01, P02 +27" || list[0].Title != "Plugin updates: 30 (P00, P01, P02 +27)" {
		t.Fatalf("params %v, title %q", p, list[0].Title)
	}
	n.MarkRead(nil, true)

	// one installed, one registry silent: still read, nobody told
	installed[0].Version = "1.1.0"
	if err := writeTestConfig(e.dir, &config.VCLUConfig{Plugins: &config.VCLUPluginsConfig{Installed: installed}}); err != nil {
		t.Fatal(err)
	}
	e.s.pluginNotices(found[2:])
	list, unread, _ = n.List()
	if len(list) != 1 || unread != 0 || list[0].Params["count"] != "29" || len(deliveries) != 1 {
		t.Fatalf("after one installed: %+v unread %d, %d deliveries", list, unread, len(deliveries))
	}
	// a new one: unread again, told once
	found = append(found, availablePlugin{Meta: PluginMetadata{Name: "P30", Version: "2.0.0"}, FullID: "@vclu/p30",
		Installed: true, InstalledVersion: "1.0.0", UpdateAvailable: true})
	e.s.pluginNotices(found)
	list, unread, _ = n.List()
	if len(list) != 1 || unread != 1 || list[0].Params["count"] != "30" || len(deliveries) != 2 {
		t.Fatalf("after a new one: %+v unread %d, %d deliveries", list, unread, len(deliveries))
	}
	e.s.PluginsReloaded(nil)
	if err := writeTestConfig(e.dir, &config.VCLUConfig{}); err != nil {
		t.Fatal(err)
	}
	e.s.resolvePluginNotices()
	if list, _, _ := n.List(); len(list) != 0 || len(deliveries) != 3 || !deliveries[2].Resolved {
		t.Fatalf("all uninstalled: %+v", list)
	}
}

// With the plugin source off nothing asks the registries, and its notice goes at once; the
// update source off adds no new-version notice.
func TestPluginCheckOff(t *testing.T) {
	e, n := newNotifyEnv(t)
	var requests atomic.Int32
	reg := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		_, _ = w.Write([]byte(`{"namespace":"vclu","plugins":[{"id":"weather","name":"Weather","version":"2.0.0"}]}`))
	}))
	defer reg.Close()
	if err := writeTestConfig(e.dir, &config.VCLUConfig{Plugins: &config.VCLUPluginsConfig{
		Repositories: []config.PluginRepository{{Name: "test", URL: reg.URL, Enabled: true}},
		Installed:    []config.InstalledPlugin{{ID: "@vclu/weather", Version: "1.0.0"}}}}); err != nil {
		t.Fatal(err)
	}
	e.s.checkPluginUpdates(context.Background())
	if list, _, _ := n.List(); requests.Load() != 1 || len(list) != 1 || list[0].Key != pluginUpdatesKey {
		t.Fatalf("on: %d requests, %+v", requests.Load(), list)
	}

	if err := n.SetSources(map[string]bool{notify.SourcePlugin: false, notify.SourceUpdate: false}); err != nil {
		t.Fatal(err)
	}
	if list, _, _ := n.List(); len(list) != 0 {
		t.Fatalf("off, still %+v", list)
	}
	e.s.UpdateAnswered(&updatecheck.Answer{Current: "1.12.0", UpdateAvailable: true, Latest: &updatecheck.Release{Version: "1.13.0"}})
	e.s.checkPluginUpdates(context.Background())
	if e.s.pluginCheck.Load() || requests.Load() != 1 {
		t.Errorf("off: check started %v, %d requests", e.s.pluginCheck.Load(), requests.Load())
	}
	if list, _, _ := n.List(); len(list) != 0 {
		t.Errorf("off: %+v", list)
	}
}

// A registry larger than 2 MB is refused.
func TestRegistrySizeLimit(t *testing.T) {
	reg := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"plugins":[],"pad":"` + strings.Repeat("x", maxRegistrySize) + `"}`))
	}))
	defer reg.Close()
	if _, err := fetchRegistryFromRepo(config.PluginRepository{URL: reg.URL}); err == nil || !strings.Contains(err.Error(), "larger than") {
		t.Errorf("err %v", err)
	}
}

// A plugin updated by a reload (files newer than the config) has no notice left.
func TestPluginsReloadedResolves(t *testing.T) {
	e, n := newNotifyEnv(t)
	if err := writeTestConfig(e.dir, &config.VCLUConfig{Plugins: &config.VCLUPluginsConfig{Installed: []config.InstalledPlugin{
		{ID: "@vclu/weather", Version: "1.2.0"}}}}); err != nil {
		t.Fatal(err)
	}
	e.s.pluginNotices([]availablePlugin{{Meta: PluginMetadata{Name: "Weather", Version: "1.10.0"}, FullID: "@vclu/weather",
		Installed: true, InstalledVersion: "1.2.0", UpdateAvailable: true}})
	e.s.PluginsReloaded(map[string]string{"@vclu/weather": "1.9.0"})
	if list, _, _ := n.List(); len(list) != 1 {
		t.Fatalf("an older version resolved it: %+v", list)
	}
	e.s.PluginsReloaded(map[string]string{"@vclu/weather": "1.10.0"})
	if list, _, _ := n.List(); len(list) != 0 {
		t.Fatalf("loaded 1.10.0, still %+v", list)
	}
}

func TestBackupLeavesNotificationsOut(t *testing.T) {
	dir := t.TempDir()
	n := notify.New(notify.Options{Path: notify.Path(dir)})
	n.Add(notify.Entry{Kind: notify.KindEvent, Source: notify.SourceLogin, Key: "login:1.1.1.1"})
	if err := n.Close(); err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(filepath.Join(dir, "workspace", "audit.log"), []byte("x"), 0o600)
	zipPath := filepath.Join(t.TempDir(), "b.zip")
	if err := createBackupZip(zipPath, dir); err != nil {
		t.Fatal(err)
	}
	r, err := zip.OpenReader(zipPath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = r.Close() }()
	names := []string{}
	for _, f := range r.File {
		names = append(names, f.Name)
	}
	if got := strings.Join(names, " "); strings.Contains(got, notify.FileName) || !strings.Contains(got, "audit.log") {
		t.Errorf("zip: %s", got)
	}
}

// A source turned off costs nothing; with both on the plugin check runs in the background,
// one at a time.
func TestUpdateAnsweredSources(t *testing.T) {
	e, n := newNotifyEnv(t)
	var requests atomic.Int32
	hold := make(chan struct{})
	reg := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		<-hold
		_, _ = w.Write([]byte(`{"namespace":"vclu","plugins":[{"id":"weather","name":"Weather","version":"2.0.0"}]}`))
	}))
	defer reg.Close()
	if err := writeTestConfig(e.dir, &config.VCLUConfig{Plugins: &config.VCLUPluginsConfig{
		Repositories: []config.PluginRepository{{Name: "test", URL: reg.URL, Enabled: true}},
		Installed:    []config.InstalledPlugin{{ID: "@vclu/weather", Version: "1.0.0"}}}}); err != nil {
		t.Fatal(err)
	}
	answer := &updatecheck.Answer{Current: "1.12.0", UpdateAvailable: true, Latest: &updatecheck.Release{Version: "1.13.0"}}
	keys := func() string {
		list, _, _ := n.List()
		var k []string
		for _, e := range list {
			k = append(k, e.Key)
		}
		return strings.Join(k, ",")
	}

	// update off: no notice; plugin off: no check
	if err := n.SetSources(map[string]bool{notify.SourceUpdate: false, notify.SourcePlugin: false}); err != nil {
		t.Fatal(err)
	}
	e.s.UpdateAnswered(answer)
	if got := keys(); got != "" || e.s.pluginCheck.Load() || requests.Load() != 0 {
		t.Fatalf("both off: %q, check %v, %d requests", got, e.s.pluginCheck.Load(), requests.Load())
	}

	// both on: the notice at once, one check however often the answer comes
	if err := n.SetSources(map[string]bool{notify.SourceUpdate: true, notify.SourcePlugin: true}); err != nil {
		t.Fatal(err)
	}
	e.s.UpdateAnswered(answer)
	if got := keys(); got != "update:1.13.0" || !e.s.pluginCheck.Load() {
		t.Fatalf("both on: %q, check %v", got, e.s.pluginCheck.Load())
	}
	e.s.UpdateAnswered(answer)
	close(hold)
	deadline := time.Now().Add(5 * time.Second)
	for e.s.pluginCheck.Load() && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if e.s.pluginCheck.Load() || requests.Load() != 1 {
		t.Fatalf("check running %v, %d requests", e.s.pluginCheck.Load(), requests.Load())
	}
	if got := keys(); !strings.Contains(got, pluginUpdatesKey) || !strings.Contains(got, "update:1.13.0") {
		t.Fatalf("after the check: %q", got)
	}
}

// No answer or no bell: nothing happens.
func TestUpdateAnsweredNil(t *testing.T) {
	e, n := newNotifyEnv(t)
	e.s.UpdateAnswered(nil)
	if list, _, _ := n.List(); len(list) != 0 || e.s.pluginCheck.Load() {
		t.Fatalf("nil answer: %+v", list)
	}
	s := newHistoryEnv(t).s
	s.UpdateAnswered(&updatecheck.Answer{Current: "1.12.0", UpdateAvailable: true, Latest: &updatecheck.Release{Version: "1.13.0"}})
	if s.pluginCheck.Load() {
		t.Fatal("no notifier, check started")
	}
}

// Bad bodies are 400, no bell is 503, the API key works, a session needs the CSRF token and a
// failed settings write is 500.
func TestNotificationsAPIErrors(t *testing.T) {
	e, _ := newNotifyEnv(t)
	for _, c := range []struct{ method, url, body string }{
		{"POST", "/api/notifications/mute", `{`},
		{"POST", "/api/notifications/mute", `{"for":"7d"}`},
		{"POST", "/api/notifications/mute", `{"key":"k","for":"0d"}`},
		{"POST", "/api/notifications/mute", `{"key":"k","for":"400d"}`},
		{"POST", "/api/notifications/mute", `{"key":"k","for":"-1h"}`},
		{"DELETE", "/api/notifications/mute", `{`},
		{"DELETE", "/api/notifications/mute", `{"key":""}`},
		{"PUT", "/api/notifications/settings", `{`},
		{"PUT", "/api/notifications/settings", `{"sources":{"login":false,"nope":true}}`},
	} {
		if w := e.do(t, c.method, c.url, c.body, "apikey"); w.Code != http.StatusBadRequest {
			t.Errorf("%s %s %s: %d", c.method, c.url, c.body, w.Code)
		}
	}
	if s := e.s.notifier.Settings(); !s.Sources[0].Enabled || len(s.Muted) != 0 {
		t.Errorf("a refused request changed the settings: %+v", s)
	}

	ok := []struct{ method, url, body string }{
		{"POST", "/api/notifications/mute", `{"key":"k","for":"24h"}`},
		{"DELETE", "/api/notifications/mute", `{"key":"k"}`},
		{"GET", "/api/notifications/settings", ``},
		{"PUT", "/api/notifications/settings", `{"sources":{"log":true}}`},
		{"POST", "/api/notifications/clear", ``},
	}
	for _, c := range ok {
		if w := e.do(t, c.method, c.url, c.body, "apikey"); w.Code != http.StatusOK {
			t.Errorf("api key %s %s: %d %s", c.method, c.url, w.Code, w.Body)
		}
		if c.method == "GET" {
			continue
		}
		req := httptest.NewRequest(c.method, c.url, strings.NewReader(c.body))
		req.Header.Set("Content-Type", "application/json")
		req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: e.session})
		w := httptest.NewRecorder()
		e.s.router.ServeHTTP(w, req)
		if w.Code != http.StatusForbidden {
			t.Errorf("no CSRF %s %s: %d", c.method, c.url, w.Code)
		}
	}

	// .vclu.json not readable: the settings are not saved
	bad := t.TempDir()
	if err := os.WriteFile(filepath.Join(bad, ".vclu.json"), []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	e.s.notifier = notify.New(notify.Options{ConfigDir: bad, Logf: t.Logf})
	for _, c := range ok[:2] {
		if w := e.do(t, c.method, c.url, c.body, "apikey"); w.Code != http.StatusInternalServerError {
			t.Errorf("write fails %s %s: %d", c.method, c.url, w.Code)
		}
	}
	if w := e.do(t, "PUT", "/api/notifications/settings", ok[3].body, "apikey"); w.Code != http.StatusInternalServerError {
		t.Errorf("write fails PUT settings: %d", w.Code)
	}

	e.s.notifier = nil
	for _, c := range ok {
		if w := e.do(t, c.method, c.url, c.body, "apikey"); w.Code != http.StatusServiceUnavailable {
			t.Errorf("no bell %s %s: %d", c.method, c.url, w.Code)
		}
	}
}
