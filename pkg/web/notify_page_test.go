package web

import (
	"encoding/json"
	"html"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/Domktorymysli/vclu/pkg/i18n"
	"github.com/gin-gonic/gin"
)

var sidebarPages = []string{"/stats", "/history", "/settings", "/debug", "/backup", "/plugins", "/tools", "/grenton",
	"/mqtt", "/satel", "/homekit", "/access", "/access/profiles", "/mcp-dashboard", "/clients", "/cloud", "/editor", "/dashboards"}

func TestNotifyBell_EveryPage(t *testing.T) {
	e := newHistoryEnv(t)
	for _, page := range sidebarPages {
		body := e.do(t, "GET", page, "", "session").Body.String()
		if !strings.Contains(body, `src="/static/js/notify-bell.js"`) || !strings.Contains(body, `class="topbar-actions"`) {
			t.Errorf("%s: no bell script or top bar actions", page)
		}
		// the update badge lives in the bell now
		if strings.Contains(body, "update-badge") || !strings.Contains(body, `src="/static/js/update-notice.js"`) {
			t.Errorf("%s: old update badge or no update modal", page)
		}
	}
}

// bellTexts reads the data-* texts of the bell script from a rendered page, as dataset keys.
func bellTexts(t *testing.T, page string) map[string]string {
	t.Helper()
	tag := regexp.MustCompile(`(?s)<script src="/static/js/notify-bell.js"[^>]*>`).FindString(page)
	if tag == "" {
		t.Fatal("no bell script tag")
	}
	texts := map[string]string{}
	for _, m := range regexp.MustCompile(`data-([a-z0-9-]+)="([^"]*)"`).FindAllStringSubmatch(tag, -1) {
		key := regexp.MustCompile(`-([a-z0-9])`).ReplaceAllStringFunc(m[1], func(s string) string { return strings.ToUpper(s[1:]) })
		texts[key] = html.UnescapeString(m[2])
	}
	return texts
}

func runBell(t *testing.T, node string, texts map[string]string, answers ...any) map[string]any {
	t.Helper()
	return runBellMode(t, node, texts, "basic", answers...)
}

func runBellMode(t *testing.T, node string, texts map[string]string, mode string, answers ...any) map[string]any {
	t.Helper()
	dir := t.TempDir()
	write := func(name string, v any) string {
		b, _ := json.Marshal(v)
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, b, 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	out, err := exec.Command(node, "testdata/notify_bell.js", "../../web/static/js/notify-bell.js",
		write("answers.json", answers), write("texts.json", texts), mode).Output()
	if err != nil {
		t.Fatalf("node: %v %s", err, out)
	}
	var got map[string]any
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	return got
}

func TestNotifyBell_Render(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not installed")
	}
	e := newHistoryEnv(t)
	texts := bellTexts(t, e.do(t, "GET", "/stats?lang=en", "", "session").Body.String())
	if texts["mLoginFailed"] != i18n.Get("en", "notify.login.failed") || texts["markAll"] == "" {
		t.Fatalf("texts: %v", texts)
	}

	now := time.Now()
	entries := []map[string]any{
		{"id": 7, "kind": "event", "source": "login", "level": "warning", "key": "login_failed:10.0.0.5",
			"title": "Failed panel login from 10.0.0.5", "msg": "notify.login.failed", "params": map[string]any{"ip": "10.0.0.5"},
			"link": "/settings", "count": 3, "first": now.Add(-time.Hour), "last": now.Add(-5 * time.Minute), "read": false},
		{"id": 3, "kind": "state", "source": "module", "level": "error", "key": "module:pompa",
			"title": "Module pompa failed to load", "msg": "notify.module.failed", "params": map[string]any{"module": "pompa"},
			"link": "/editor?file=modules/pompa.lua", "count": 1, "first": now, "last": now, "read": false},
		{"id": 1, "kind": "state", "source": "plugin", "level": "info", "key": "plugin:x:2.0.0",
			"title": "Fallback title", "msg": "notify.no_such_key", "link": "https://example.com", "count": 1,
			"first": now, "last": now, "read": true},
	}
	got := runBell(t, node, texts,
		map[string]any{"entries": entries, "unread": 2, "worst": "error"},
		map[string]any{"entries": entries, "unread": 3, "worst": "error"})

	if got["label"] != "Notifications, 2 unread" || got["badge"] != "2" || !strings.Contains(got["badgeClass"].(string), "bg-danger") {
		t.Errorf("badge: %v %v %v", got["label"], got["badge"], got["badgeClass"])
	}
	list := got["entries"].([]any)
	if len(list) != 3 {
		t.Fatalf("entries: %v", list)
	}
	first := list[0].(map[string]any)
	if first["text"] != "Failed panel login from 10.0.0.5" || first["unread"] != true ||
		!strings.Contains(first["meta"].(string), "5 minutes ago") || !strings.Contains(first["meta"].(string), "×3") {
		t.Errorf("first entry: %v", first)
	}
	if e := list[1].(map[string]any); e["text"] != "Module pompa failed to load" || e["href"] != "/editor?file=modules/pompa.lua" {
		t.Errorf("module entry: %v", e)
	}
	// unknown msg falls back to the title; outside links are not followed
	if e := list[2].(map[string]any); e["text"] != "Fallback title" || e["href"] != "#" || e["unread"] != false {
		t.Errorf("fallback entry: %v", e)
	}

	calls, _ := json.Marshal(got["calls"])
	want := `[{"body":{"ids":[7]},"method":"POST","url":"/api/notifications/read"},` +
		`{"body":{"for":"24h","key":"module:pompa"},"method":"POST","url":"/api/notifications/mute"}]`
	if string(calls) != want {
		t.Errorf("calls:\n%s\nwant\n%s", calls, want)
	}
	if got["location"] != "/settings" {
		t.Errorf("after click: %v", got["location"])
	}
	// more unread on the next poll is announced
	if got["live"] != "Notifications, 3 unread" {
		t.Errorf("live: %q", got["live"])
	}

	empty := runBell(t, node, texts, map[string]any{"entries": []any{}, "unread": 0, "worst": ""})
	if !strings.Contains(empty["badgeClass"].(string), "d-none") || len(empty["empty"].([]any)) != 1 || empty["live"] != "" {
		t.Errorf("empty: %v", empty)
	}
}

// The poll does not rebuild an open list, only the badge; closing it shows the new list. The
// skipped count never shows its English body; links out of the panel are not followed; an
// update entry with no modal to open leads to the settings.
func TestNotifyBell_OpenListAndLinks(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not installed")
	}
	e := newHistoryEnv(t)
	texts := bellTexts(t, e.do(t, "GET", "/stats?lang=pl", "", "session").Body.String())
	now := time.Now()
	entry := func(id int, source, msg, link string, params map[string]any) map[string]any {
		return map[string]any{"id": id, "kind": "event", "source": source, "level": "info", "key": "k" + msg,
			"title": "English title", "body": "English body", "msg": msg, "params": params, "link": link,
			"count": 1, "first": now, "last": now, "read": false}
	}
	first := []map[string]any{
		entry(2, "lock", "notify.suppressed", "/\\evil.example", map[string]any{"count": "4", "source": "lock"}),
		entry(1, "update", "notify.update.available", "", map[string]any{"version": "9.0.0"}),
	}
	second := append([]map[string]any{entry(3, "login", "notify.login.failed", "/settings", map[string]any{"ip": "10.0.0.1"})}, first...)

	got := runBellMode(t, node, texts, "open",
		map[string]any{"entries": first, "unread": 2, "worst": "info"},
		map[string]any{"entries": second, "unread": 3, "worst": "info"})
	if len(got["before"].([]any)) != 2 || len(got["whileOpen"].([]any)) != 2 || got["badge"] != "3" || len(got["afterClose"].([]any)) != 3 {
		t.Errorf("open list: %v", got)
	}

	got = runBellMode(t, node, texts, "update", map[string]any{"entries": []map[string]any{first[1], first[0]}, "unread": 2, "worst": "info"})
	list := got["entries"].([]any)
	upd, skipped := list[0].(map[string]any), list[1].(map[string]any)
	if upd["text"] != "Dostępna nowa wersja vclu 9.0.0" || got["location"] != "/settings" {
		t.Errorf("update entry: %v, location %v", upd, got["location"])
	}
	if skipped["body"] != nil || skipped["href"] != "#" || skipped["text"] != "Pominięto kolejne powiadomienia (Blokady po zbyt wielu próbach): 4" {
		t.Errorf("skipped entry: %v", skipped)
	}
	if mark, _ := texts["markAllShort"]; mark != "Przeczytane" {
		t.Errorf("short label %q", mark)
	}
}

func TestStatsPage_SectionsAndShortcuts(t *testing.T) {
	e := newHistoryEnv(t)
	withStorage(t, e, 50)
	body := e.do(t, "GET", "/stats", "", "session").Body.String()
	shortcuts := map[string]string{"system": "/settings", "lua": "/editor", "clu": "/grenton", "http": "/settings",
		"plugins": "/plugins", "mqtt": "/mqtt", "homekit": "/homekit", "storage": "/history#settings"}
	for _, id := range []string{"system", "lua", "clu", "http", "plugins", "mqtt", "homekit", "pluginDetailsCard", "storage",
		"chart-memory", "chart-cmd", "chart-http", "chart-busy", "chart-lag", "diskFreeCard"} {
		card := regexp.MustCompile(`(?s)<div class="card stats-section[^"]*"[^>]*id="` + id + `">.*?<div class="card-body">`).FindString(body)
		if card == "" {
			t.Errorf("%s: no section card", id)
			continue
		}
		if !strings.Contains(card, `class="btn-icon section-toggle" aria-expanded="true"`) {
			t.Errorf("%s: no collapse toggle", id)
		}
		link := regexp.MustCompile(`class="btn-icon section-link" href="([^"]+)"`).FindStringSubmatch(card)
		switch {
		case shortcuts[id] == "" && link != nil:
			t.Errorf("%s: shortcut %s, want none", id, link[1])
		case shortcuts[id] != "" && (link == nil || link[1] != shortcuts[id]):
			t.Errorf("%s: shortcut %v, want %s", id, link, shortcuts[id])
		}
	}
	// section titles stay English
	if !strings.Contains(body, "<span>System Overview</span>") {
		t.Error("section title changed")
	}
}

func TestSettingsPage_NotificationsCard(t *testing.T) {
	e := newHistoryEnv(t)
	body := e.do(t, "GET", "/settings", "", "session").Body.String()
	if !strings.Contains(body, `id="notificationsCard"`) || !strings.Contains(body, `id="notifyMuted"`) {
		t.Fatal("no notifications card")
	}
	for _, src := range []string{"update", "plugin", "module", "log", "login", "lock"} {
		if !strings.Contains(body, `data-source="`+src+`"`) {
			t.Errorf("no switch for %s", src)
		}
	}
}

// The lock message shows only when the handler passes lockedFor.
func TestLoginPage_LockedMessage(t *testing.T) {
	e := newHistoryEnv(t)
	render := func(data gin.H) string {
		w := httptest.NewRecorder()
		data["lang"], data["t"] = "pl", i18n.GetAll("pl")
		if err := e.s.router.HTMLRender.Instance("login.html", data).Render(w); err != nil {
			t.Fatal(err)
		}
		return w.Body.String()
	}
	if body := render(gin.H{}); strings.Contains(body, "loginLocked") {
		t.Error("lock message without lockedFor")
	}
	body := render(gin.H{"lockedFor": "30 s", "error": true})
	if !strings.Contains(body, `data-time="30 s"`) || !strings.Contains(body, i18n.Get("pl", "login.locked")) {
		t.Errorf("no lock message:\n%s", body)
	}
	if strings.Contains(body, i18n.Get("pl", "login.error")) {
		t.Error("lock message and the password error together")
	}
}

// Keys of the bell, the stats tools and the lock message exist in every locale.
func TestNotifyLocaleKeys(t *testing.T) {
	if err := i18n.Load("../../web/locales"); err != nil {
		t.Fatal(err)
	}
	keys := map[string]bool{}
	for _, f := range []string{"_sidebar.html", "settings.html", "stats.html", "login.html"} {
		b, err := os.ReadFile(filepath.Join("../../web/templates", f))
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range regexp.MustCompile(`index \$?\.t "((?:notify|stats)\.[^"]+|login\.locked)"`).FindAllStringSubmatch(string(b), -1) {
			keys[m[1]] = true
		}
	}
	for _, k := range []string{"notify.update.available", "notify.plugin.update", "notify.module.failed", "notify.log.error",
		"notify.login.failed", "notify.lock.login", "notify.lock.pin", "notify.lock.token", "notify.suppressed"} {
		if !keys[k] {
			t.Errorf("%s not passed to the bell", k)
		}
	}
	for k := range keys {
		for _, lang := range []string{"en", "pl"} {
			if i18n.Get(lang, k) == k {
				t.Errorf("%s: no %s", lang, k)
			}
		}
	}
}
