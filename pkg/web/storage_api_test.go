package web

import (
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/Domktorymysli/vclu/pkg/i18n"
	"github.com/Domktorymysli/vclu/pkg/storage"
)

const gib = 1 << 30

// fakeDisk is a file system whose free space the test sets.
type fakeDisk struct {
	mu          sync.Mutex
	total, free int64
}

func (f *fakeDisk) stat(string) (int64, int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.total, f.free, nil
}

// withStorage gives e a monitor over a data directory whose history is its largest part.
func withStorage(t *testing.T, e *historyEnv, freePercent int64) (*storage.Monitor, *fakeDisk) {
	t.Helper()
	data := t.TempDir()
	for path, size := range map[string]int{"clu/history/2026-11-09.jsonl": 3 << 20, "clu/metrics/m/2026-11-09.ts": 1 << 20} {
		p := filepath.Join(data, path)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, make([]byte, size), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	disk := &fakeDisk{total: 100 * gib, free: 100 * gib * freePercent / 100}
	m := storage.New(storage.Options{DataDir: data, StatFS: disk.stat,
		HistoryLast24h: func() int64 { return 100 << 20 }})
	m.Refresh()
	e.s.SetStorageMonitor(m)
	return m, disk
}

func TestStorageAPI_ShapeAndLogin(t *testing.T) {
	e := newHistoryEnv(t)
	if w := e.do(t, "GET", "/api/storage", "", "apikey"); w.Code != http.StatusServiceUnavailable {
		t.Fatalf("without a monitor: %d", w.Code)
	}
	withStorage(t, e, 8)
	if w := e.do(t, "GET", "/api/storage", "", ""); w.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous: %d", w.Code)
	}
	w := e.do(t, "GET", "/api/storage", "", "apikey")
	if w.Code != http.StatusOK {
		t.Fatalf("api key: %d", w.Code)
	}
	var got map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"ready", "level", "fileSystem", "parts", "largest", "growth", "sizesAt", "freeAt"} {
		if _, ok := got[k]; !ok {
			t.Errorf("no %q in %s", k, w.Body)
		}
	}
	fs := got["fileSystem"].(map[string]any)
	for _, k := range []string{"path", "totalBytes", "freeBytes", "freePercent", "lowBelowBytes", "criticalBelowBytes"} {
		if _, ok := fs[k]; !ok {
			t.Errorf("no fileSystem.%s", k)
		}
	}
	g := got["growth"].(map[string]any)
	for _, k := range []string{"historyLast24hBytes", "logBytesPerHour", "bytesPerDay", "daysToFull"} {
		if _, ok := g[k]; !ok {
			t.Errorf("no growth.%s", k)
		}
	}
	if got["level"] != "low" || got["largest"] != "history" || fs["freePercent"] != 8.0 {
		t.Errorf("level %v largest %v free %v", got["level"], got["largest"], fs["freePercent"])
	}
	if p := got["parts"].([]any); len(p) != 5 || p[0].(map[string]any)["name"] != "history" {
		t.Errorf("parts %v", p)
	}
	if w.Header().Get("Cache-Control") != "no-store" {
		t.Error("cacheable")
	}
}

func TestStorageCard_OnStatusPage(t *testing.T) {
	e := newHistoryEnv(t)
	w := e.do(t, "GET", "/stats", "", "session")
	if w.Code != 200 || strings.Contains(w.Body.String(), `id="storage"`) {
		t.Fatalf("without a monitor: %d, card shown", w.Code)
	}
	m, disk := withStorage(t, e, 1)
	disk.mu.Lock()
	disk.free = gib // 1 GiB at 100 MiB a day
	disk.mu.Unlock()
	m.RefreshFree()
	body := e.do(t, "GET", "/stats?lang=en", "", "session").Body.String()
	for _, want := range []string{`id="storage"`, "Disk nearly full", `data-part="history"`, "History",
		"Rest of the data directory", "is full in <strong>10</strong> days", `href="/history#settings"`, "3.0 MB"} {
		if !strings.Contains(body, want) {
			t.Errorf("card lacks %q", want)
		}
	}
	pl := e.do(t, "GET", "/stats?lang=pl", "", "session").Body.String()
	if !strings.Contains(pl, "Dysk prawie pełny") || !strings.Contains(pl, "3,0 MB") {
		t.Error("card not in Polish")
	}
	// the status link of the old page leads to the card's page
	if w := e.do(t, "GET", "/status", "", "session"); w.Code != http.StatusFound || w.Header().Get("Location") != "/stats" {
		t.Errorf("/status: %d %s", w.Code, w.Header().Get("Location"))
	}
}

func TestStorageBanner_EveryPageAndLevels(t *testing.T) {
	e := newHistoryEnv(t)
	withStorage(t, e, 50)
	for _, page := range []string{"/stats", "/history", "/settings", "/debug", "/backup", "/plugins"} {
		body := e.do(t, "GET", page, "", "session").Body.String()
		if !strings.Contains(body, `src="/static/js/storage-notice.js"`) || !strings.Contains(body, `class="topbar"`) {
			t.Errorf("%s: no banner script or top bar", page)
		}
	}

	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not installed")
	}
	if err := i18n.Load("../../web/locales"); err != nil {
		t.Fatal(err)
	}
	texts := map[string]string{
		"low": i18n.Get("en", "storage.banner_low"), "critical": i18n.Get("en", "storage.banner_critical"),
		"largest": i18n.Get("en", "storage.banner_largest"), "details": i18n.Get("en", "storage.banner_details"),
		"partHistory": i18n.Get("en", "storage.part_history"),
	}
	dir := t.TempDir()
	textsFile := filepath.Join(dir, "texts.json")
	b, _ := json.Marshal(texts)
	if err := os.WriteFile(textsFile, b, 0o600); err != nil {
		t.Fatal(err)
	}

	banner := func(percent int64) map[string]any {
		t.Helper()
		e2 := newHistoryEnv(t)
		withStorage(t, e2, percent)
		answer := filepath.Join(dir, "answer.json")
		if err := os.WriteFile(answer, e2.do(t, "GET", "/api/storage", "", "session").Body.Bytes(), 0o600); err != nil {
			t.Fatal(err)
		}
		out, err := exec.Command(node, "testdata/storage_notice.js", "../../web/static/js/storage-notice.js", answer, textsFile).Output()
		if err != nil {
			t.Fatalf("node: %v %s", err, out)
		}
		var got map[string]any
		if err := json.Unmarshal(out, &got); err != nil {
			t.Fatalf("%v: %s", err, out)
		}
		return got
	}
	if got := banner(8); got["shown"] != true || !strings.Contains(got["className"].(string), "alert-warning") ||
		!strings.Contains(got["text"].(string), "Most space: History (3 MB)") || got["link"] != "/stats#storage" {
		t.Errorf("8 %%: %v", got)
	}
	if got := banner(2); got["shown"] != true || !strings.Contains(got["className"].(string), "alert-danger") ||
		!strings.Contains(got["text"].(string), "Disk nearly full: 2 GB free (2%)") {
		t.Errorf("2 %%: %v", got)
	}
	if got := banner(11); got["shown"] != false {
		t.Errorf("11 %%: %v", got)
	}
}

func TestStoragePrometheus(t *testing.T) {
	e := newHistoryEnv(t)
	if s := e.s.storagePrometheus(); s != "" {
		t.Fatalf("without a monitor: %q", s)
	}
	withStorage(t, e, 50)
	out := e.s.storagePrometheus()
	for _, want := range []string{"vclu_disk_free_bytes 53687091200\n", "vclu_history_bytes ", "vclu_log_bytes_per_hour 0\n"} {
		if !strings.Contains(out, want) {
			t.Errorf("no %q in\n%s", want, out)
		}
	}
}

func TestFormatBytes(t *testing.T) {
	for n, want := range map[int64]string{0: "0 B", 1023: "1023 B", 1536: "1.5 KB", 3 << 20: "3.0 MB", 5 * gib / 2: "2.5 GB"} {
		if got := formatBytes(n, "en"); got != want {
			t.Errorf("%d: %q, want %q", n, got, want)
		}
	}
	if got := formatBytes(1536, "pl"); got != "1,5 KB" {
		t.Errorf("pl: %q", got)
	}
}
