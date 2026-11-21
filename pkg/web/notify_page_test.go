package web

import (
	"encoding/json"
	"html/template"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/Domktorymysli/vclu/pkg/i18n"
	"github.com/Domktorymysli/vclu/pkg/notify"
	"github.com/gin-gonic/gin"
)

var sidebarPages = []string{"/stats", "/history", "/settings", "/debug", "/backup", "/plugins", "/tools", "/grenton",
	"/mqtt", "/satel", "/homekit", "/access", "/access/profiles", "/mcp-dashboard", "/clients", "/cloud", "/editor", "/dashboards"}

func TestNotifyBell_EveryPage(t *testing.T) {
	e := newHistoryEnv(t)
	for _, page := range sidebarPages {
		body := e.do(t, "GET", page, "", "session").Body.String()
		v := e.s.assetVersion()
		if !strings.Contains(body, `src="/static/js/notify-bell.js?v=`+v+`"`) || !strings.Contains(body, `class="topbar-actions"`) ||
			!strings.Contains(body, `<script type="application/json" id="notifyI18n">`) {
			t.Errorf("%s: no bell script, its texts or top bar actions", page)
		}
		// the update badge lives in the bell now
		if strings.Contains(body, "update-badge") || !strings.Contains(body, `src="/static/js/update-notice.js?v=`+v+`"`) {
			t.Errorf("%s: old update badge or no update modal", page)
		}
		if !strings.Contains(body, `href="/static/css/dark-theme.css?v=`+v+`"`) || !strings.Contains(body, `src="/static/js/storage-notice.js?v=`+v+`"`) {
			t.Errorf("%s: a changed file without the version", page)
		}
	}
}

// bellTexts reads the bell's texts (the notifyI18n blob) from a rendered page.
func bellTexts(t *testing.T, page string) map[string]string {
	t.Helper()
	m := regexp.MustCompile(`(?s)<script type="application/json" id="notifyI18n">(.*?)</script>`).FindStringSubmatch(page)
	if m == nil {
		t.Fatal("no notifyI18n")
	}
	texts := map[string]string{}
	if err := json.Unmarshal([]byte(m[1]), &texts); err != nil {
		t.Fatalf("notifyI18n: %v", err)
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
	if texts["notify.login.failed"] != i18n.Get("en", "notify.login.failed") || texts["notify.mark_all"] == "" || texts["notify.source.log"] == "" {
		t.Fatalf("texts: %v", texts)
	}

	now := time.Now()
	entries := []map[string]any{
		{"id": 7, "kind": "event", "source": "login", "level": "warning", "key": "login_failed:10.0.0.5",
			"title": "Failed panel login from 10.0.0.5", "msg": "notify.login.failed", "params": map[string]any{"ip": "10.0.0.5"},
			"link": "/settings", "count": 3, "first": now.Add(-time.Hour), "last": now.Add(-5 * time.Minute), "read": false},
		{"id": 3, "kind": "state", "source": "module", "level": "error", "key": "module:pompa",
			"title": "Module pompa failed to load", "msg": "notify.module.failed", "params": map[string]any{"module": "pompa"},
			"detail": "pompa.lua:3: boom", "link": "/editor?file=modules/pompa.lua", "count": 1, "first": now, "last": now, "read": false},
		{"id": 1, "kind": "state", "source": "plugin", "level": "info", "key": "plugin:x:2.0.0",
			"title": "Fallback title", "body": "Fallback body", "msg": "notify.no_such_key", "link": "https://example.com", "count": 1,
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
	if first["body"] != nil {
		t.Errorf("a translated entry shows its English body: %v", first)
	}
	if e := list[1].(map[string]any); e["text"] != "Module pompa failed to load" || e["href"] != "/editor?file=modules/pompa.lua" || e["body"] != "pompa.lua:3: boom" {
		t.Errorf("module entry: %v", e)
	}
	// unknown msg falls back to the title; outside links are not followed
	if e := list[2].(map[string]any); e["text"] != "Fallback title" || e["body"] != "Fallback body" || e["href"] != "#" || e["unread"] != false {
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
// update entry ("#update") with no modal to open leads to the settings.
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
		entry(1, "update", "notify.update.available", "#update", map[string]any{"version": "9.0.0"}),
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
	if upd["text"] != "Dostępna nowa wersja vclu 9.0.0" || upd["body"] != "Zobacz, co nowego" || upd["href"] != "#" || got["location"] != "/settings" {
		t.Errorf("update entry: %v, location %v", upd, got["location"])
	}
	if skipped["body"] != nil || skipped["href"] != "#" || skipped["text"] != "Pominięto kolejne powiadomienia (Blokady po zbyt wielu próbach): 4" {
		t.Errorf("skipped entry: %v", skipped)
	}
	if mark := texts["notify.mark_all_short"]; mark != "Przeczytane" {
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

// The switches come from the settings API: the page has none of its own.
func TestSettingsPage_NotificationsCard(t *testing.T) {
	e := newHistoryEnv(t)
	body := e.do(t, "GET", "/settings", "", "session").Body.String()
	if !strings.Contains(body, `id="notificationsCard"`) || !strings.Contains(body, `id="notifyMuted"`) ||
		!strings.Contains(body, `<div id="notifySources" class="mb-3"></div>`) || strings.Contains(body, `data-source=`) {
		t.Fatal("no notifications card, or switches in the page")
	}
	if !strings.Contains(body, `src="/static/js/settings.js?v=`+e.s.assetVersion()+`"`) {
		t.Error("settings.js without the version")
	}
}

// settings.js builds a switch per source with its label and hint, and sends only the one
// changed.
func TestSettingsPage_SourceSwitches(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not installed")
	}
	e := newHistoryEnv(t)
	texts := bellTexts(t, e.do(t, "GET", "/settings?lang=pl", "", "session").Body.String())
	b, _ := json.Marshal(texts)
	tf := filepath.Join(t.TempDir(), "texts.json")
	if err := os.WriteFile(tf, b, 0o600); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command(node, "testdata/notify_settings.js", "../../web/static/js/settings.js", tf).Output()
	if err != nil {
		t.Fatalf("node: %v %s", err, out)
	}
	var got struct {
		Switches []struct{ ID, Label, Hint string }
		Checked  map[string]bool
		Sent     string
	}
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	if len(got.Switches) != 3 || got.Switches[0].Label != "Nowe wersje vclu" || got.Switches[1].Hint != i18n.Get("pl", "notify.source.log_hint") ||
		got.Switches[2].Label != "future" || !got.Checked["update"] || got.Checked["log"] {
		t.Errorf("switches %+v", got)
	}
	if got.Sent != `{"sources":{"log":true}}` {
		t.Errorf("sent %s", got.Sent)
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
	body := render(gin.H{"lockedFor": "30 s", "lockedText": lockMessage("pl", "30 s"), "error": true})
	if !strings.Contains(body, "Spróbuj ponownie za 30 s") || strings.Contains(body, "{time}") {
		t.Errorf("no lock message:\n%s", body)
	}
	if strings.Contains(body, i18n.Get("pl", "login.error")) {
		t.Error("lock message and the password error together")
	}
}

// Every message and source has its text in every locale, and the locales have the same
// notify.* keys; the stats tools and the lock message too.
func TestNotifyLocaleKeys(t *testing.T) {
	if err := i18n.Load("../../web/locales"); err != nil {
		t.Fatal(err)
	}
	keys := map[string]bool{"login.locked": true, "stats.toggle": true, "stats.configure": true}
	for _, m := range []string{notify.MsgUpdate, notify.MsgPluginUpdates, notify.MsgModule, notify.MsgLog, notify.MsgLogin,
		notify.MsgLockLogin, notify.MsgLockPIN, notify.MsgLockDevice, notify.MsgLockToken, notify.MsgSuppressed} {
		keys[m] = true
	}
	for _, src := range notify.Sources {
		keys["notify.source."+src.ID] = true
	}
	for _, lang := range []string{"en", "pl"} {
		for k := range i18n.GetAll(lang) {
			if strings.HasPrefix(k, "notify.") {
				keys[k] = true
			}
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

// The texts are JSON in a script element: a text closing the element stays inside it.
func TestNotifyI18nEscaped(t *testing.T) {
	tmpl := template.Must(template.New("x").Funcs(templateFuncs()).Parse(
		`<script type="application/json" id="notifyI18n">{{json (tprefix .t "notify.")}}</script>`))
	var b strings.Builder
	evil := `</script><img src=x onerror=alert(1)>&<!--`
	if err := tmpl.Execute(&b, map[string]any{"t": map[string]string{"notify.title": evil, "other": "x"}}); err != nil {
		t.Fatal(err)
	}
	out := b.String()
	inner := strings.TrimSuffix(strings.TrimPrefix(out, `<script type="application/json" id="notifyI18n">`), `</script>`)
	if strings.Contains(inner, "<") || strings.Contains(inner, ">") {
		t.Fatalf("raw markup in the blob: %s", out)
	}
	var got map[string]string
	if err := json.Unmarshal([]byte(inner), &got); err != nil || len(got) != 1 || got["notify.title"] != evil {
		t.Errorf("blob %s: %v %v", inner, got, err)
	}
}

// Versioned links of the changed files are kept for a year; a page of the panel is not.
func TestChangedAssetsVersioned(t *testing.T) {
	e := newHistoryEnv(t)
	body := e.do(t, "GET", "/editor", "", "session").Body.String()
	v := e.s.assetVersion()
	if !strings.Contains(body, `src="/static/js/lua-editor.js?v=`+v+`"`) {
		t.Error("lua-editor.js without the version")
	}
	if body := e.do(t, "GET", "/stats", "", "session").Body.String(); !strings.Contains(body, `src="/static/js/stats.js?v=`+v+`"`) {
		t.Error("stats.js without the version")
	}
	w := e.do(t, "GET", "/static/js/notify-bell.js?v="+v, "", "session")
	if w.Code != 200 || !strings.Contains(w.Header().Get("Cache-Control"), "immutable") {
		t.Errorf("versioned file: %d %q", w.Code, w.Header().Get("Cache-Control"))
	}
	if w := e.do(t, "GET", "/static/js/notify-bell.js", "", "session"); strings.Contains(w.Header().Get("Cache-Control"), "immutable") {
		t.Error("a file without the version kept for a year")
	}
}
