package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/Domktorymysli/vclu/pkg/config"
	"github.com/Domktorymysli/vclu/pkg/history"
)

type historyEnv struct {
	s       *Server
	store   *history.Store
	dir     string
	apiKey  string
	session string
	csrf    string
}

// newHistoryEnv builds the real router (auth, CSRF, routes) with auth on.
func newHistoryEnv(t *testing.T) *historyEnv {
	t.Helper()
	dir := t.TempDir()
	hash, _ := config.HashPassword("secret")
	key, keyHash, _ := config.GenerateAPIKey()
	if err := config.WriteMasterConfig(dir, &config.VCLUConfig{
		Auth: &config.VCLUAuthConfig{Username: "tytus", PasswordHash: hash, APIKeyHash: keyHash},
	}); err != nil {
		t.Fatal(err)
	}
	cfg := history.DefaultConfig()
	cfg.Dir = dir + "/history"
	store := history.New(cfg, nil)
	s := NewServer(0, "../../web", dir, nil, nil, nil)
	s.SetHistoryStore(store)
	session := s.sessions.create()
	return &historyEnv{s: s, store: store, dir: dir, apiKey: key, session: session, csrf: s.sessions.getCSRF(session)}
}

func (e *historyEnv) do(t *testing.T, method, url, body, auth string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, url, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	switch auth {
	case "apikey":
		req.Header.Set("X-API-Key", e.apiKey)
	case "session":
		req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: e.session})
		req.Header.Set("X-CSRF-Token", e.csrf)
	}
	w := httptest.NewRecorder()
	e.s.router.ServeHTTP(w, req)
	return w
}

func TestHistoryAPI_ReadAndPage(t *testing.T) {
	e := newHistoryEnv(t)
	for i := 0; i < 150; i++ {
		e.store.Record(history.Entry{Path: "light", Old: i, Value: i + 1, Origin: "lua", Actor: "automation:w"})
	}
	if w := e.do(t, "GET", "/api/history", "", ""); w.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous read: %d", w.Code)
	}
	for _, auth := range []string{"apikey", "session"} {
		w := e.do(t, "GET", "/api/history?path=light&limit=100", "", auth)
		var res history.SearchResult
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &res) != nil || len(res.Entries) != 100 || res.Next == "" {
			t.Fatalf("%s page 1: %d %s", auth, w.Code, w.Body.String()[:min(200, w.Body.Len())])
		}
		w = e.do(t, "GET", "/api/history?path=light&limit=100&cursor="+res.Next, "", auth)
		var res2 history.SearchResult
		json.Unmarshal(w.Body.Bytes(), &res2)
		if len(res2.Entries) != 50 || res2.Next != "" {
			t.Fatalf("%s page 2: %d entries, next %q", auth, len(res2.Entries), res2.Next)
		}
	}
	if w := e.do(t, "GET", "/api/history?since=yesterday", "", "session"); w.Code != http.StatusBadRequest {
		t.Errorf("bad since: %d", w.Code)
	}
	since := time.Now().Add(-time.Minute).Format(time.RFC3339)
	if w := e.do(t, "GET", "/api/history?since="+url.QueryEscape(since), "", "session"); w.Code != 200 {
		t.Errorf("RFC 3339 since: %d", w.Code)
	}
	if w := e.do(t, "GET", "/api/history/status", "", "apikey"); w.Code != 200 || !strings.Contains(w.Body.String(), `"memoryEntries":150`) {
		t.Errorf("status: %d %s", w.Code, w.Body.String())
	}
}

func TestHistoryAPI_AdminNeedsSession(t *testing.T) {
	e := newHistoryEnv(t)
	settings := `{"enabled":true,"memoryHours":12,"maxEntriesPerObject":500,"maxEntriesTotal":20000,"diskEnabled":true,"retentionDays":30,"maxDiskMB":256,"flushIntervalSeconds":60}`
	for _, c := range []struct{ method, url, body string }{
		{"PUT", "/api/history/settings", settings},
		{"POST", "/api/history/flush", ""},
		{"POST", "/api/history/clear-memory", `{"confirm":true}`},
		{"POST", "/api/history/clear-disk", `{"confirm":true}`},
	} {
		if w := e.do(t, c.method, c.url, c.body, "apikey"); w.Code != http.StatusForbidden {
			t.Errorf("API key on %s %s: %d, want 403", c.method, c.url, w.Code)
		}
	}

	w := e.do(t, "PUT", "/api/history/settings", settings, "session")
	if w.Code != 200 {
		t.Fatalf("save: %d %s", w.Code, w.Body.String())
	}
	if cfg := e.store.Config(); cfg.MemoryHours != 12 || cfg.MaxEntriesPerObject != 500 || cfg.RetentionDays != 30 || cfg.FlushInterval != time.Minute {
		t.Errorf("not applied live: %+v", cfg)
	}
	mc, _ := config.ReadMasterConfig(e.dir)
	if mc.History == nil || mc.History.MaxDiskMB != 256 || mc.Auth == nil || mc.Auth.Username != "tytus" {
		t.Errorf("not saved, or other sections lost: %+v", mc.History)
	}
	if strings.Contains(w.Body.String(), `"restartRequired":true`) {
		t.Error("only limits changed: no restart needed")
	}
	if w := e.do(t, "PUT", "/api/history/settings", strings.Replace(settings, `"enabled":true`, `"enabled":false`, 1), "session"); !strings.Contains(w.Body.String(), `"restartRequired":true`) {
		t.Errorf("turning history off needs a restart: %s", w.Body.String())
	}
}

func TestHistoryAPI_Validation(t *testing.T) {
	e := newHistoryEnv(t)
	base := map[string]int{"memoryHours": 24, "maxEntriesPerObject": 10000, "maxEntriesTotal": 50000, "retentionDays": 90, "maxDiskMB": 1024, "flushIntervalSeconds": 300}
	for field, bad := range map[string]int{"memoryHours": 0, "maxEntriesPerObject": 50, "maxEntriesTotal": 5000, "retentionDays": 4000, "maxDiskMB": 8, "flushIntervalSeconds": 5} {
		body := map[string]any{"enabled": true, "diskEnabled": true}
		for k, v := range base {
			body[k] = v
		}
		body[field] = bad
		b, _ := json.Marshal(body)
		if w := e.do(t, "PUT", "/api/history/settings", string(b), "session"); w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), field) {
			t.Errorf("%s=%d: %d %s", field, bad, w.Code, w.Body.String())
		}
	}
	if cfg := e.store.Config(); cfg.MemoryHours != 24 {
		t.Errorf("rejected settings changed the store: %+v", cfg)
	}
}

func TestHistoryAPI_DeletionNeedsConfirm(t *testing.T) {
	e := newHistoryEnv(t)
	e.store.Record(history.Entry{Path: "a", Old: 0, Value: 1})
	e.store.Flush()
	if w := e.do(t, "POST", "/api/history/clear-disk", `{}`, "session"); w.Code != http.StatusBadRequest {
		t.Fatalf("without confirm: %d", w.Code)
	}
	if st := e.store.Status(); st.DiskUsageBytes == 0 {
		t.Fatal("files deleted without confirm")
	}
	if w := e.do(t, "POST", "/api/history/clear-disk", `{"confirm":true}`, "session"); w.Code != 200 {
		t.Fatalf("with confirm: %d %s", w.Code, w.Body.String())
	}
	if st := e.store.Status(); st.DiskUsageBytes != 0 || st.MemoryEntries != 1 {
		t.Errorf("clear-disk: %+v", st)
	}
	e.do(t, "POST", "/api/history/clear-memory", `{"confirm":true}`, "session")
	if st := e.store.Status(); st.MemoryEntries != 0 {
		t.Errorf("clear-memory: %+v", st)
	}
}

// The page renders for an empty store, a store with entries and one whose
// disk writes fail.
func TestHistoryPage_Renders(t *testing.T) {
	e := newHistoryEnv(t)
	check := func(label string) {
		w := e.do(t, "GET", "/history?lang=pl", "", "session")
		if w.Code != 200 || !strings.Contains(w.Body.String(), "Historia i audyt") || !strings.Contains(w.Body.String(), "/static/js/history.js") {
			t.Fatalf("%s: %d", label, w.Code)
		}
		if !strings.Contains(w.Body.String(), `"clear_disk":"Usuń pliki historii"`) {
			t.Errorf("%s: page translations missing", label)
		}
	}
	check("empty")
	e.store.Record(history.Entry{Path: "a", Old: 0, Value: 1})
	check("with entries")
	if w := e.do(t, "GET", "/history", "", ""); w.Code != http.StatusFound {
		t.Errorf("anonymous page: %d, want a redirect to login", w.Code)
	}
}

// GET shows what is saved for the next start, not only what runs now.
func TestHistoryAPI_SettingsShowSaved(t *testing.T) {
	e := newHistoryEnv(t)
	off := false
	config.UpdateMasterConfig(e.dir, func(mc *config.VCLUConfig) error {
		mc.History = &config.VCLUHistoryConfig{DiskEnabled: &off, RetentionDays: 7, MaxEntriesTotal: 5000000}
		return nil
	})
	w := e.do(t, "GET", "/api/history/settings", "", "session")
	var res struct {
		Settings        map[string]any `json:"settings"`
		RestartRequired bool           `json:"restartRequired"`
	}
	json.Unmarshal(w.Body.Bytes(), &res)
	if res.Settings["diskEnabled"] != false || res.Settings["retentionDays"] != 7.0 || res.Settings["maxEntriesTotal"] != 200000.0 || !res.RestartRequired {
		t.Fatalf("%d %s", w.Code, w.Body.String())
	}
}
