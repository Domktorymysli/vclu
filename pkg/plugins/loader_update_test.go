package plugins

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Domktorymysli/vclu/pkg/config"
	lua "github.com/yuin/gopher-lua"
)

func writeTestConfig(dir string, cfg *config.VCLUConfig) error {
	return config.UpdateMasterConfig(dir, func(mc *config.VCLUConfig) error {
		*mc = *cfg
		return nil
	})
}

// An update from the panel writes the new version to .vclu.json; the loader brings the files (#112).

const weatherIndex110 = `{"version":1,"name":"test","description":"d","url":"x","plugins":[{"id":"weather","name":"Weather","version":"1.1.0","path":"plugins/weather","tag":"weather-v1.1.0"}]}`

// installedWeather puts weather 1.0.2 on disk and in the lock, and configVersion in .vclu.json.
func installedWeather(t *testing.T, repoURL, configVersion string) (*Loader, *lua.LState) {
	t.Helper()
	loader, serverDir := newTestLoader(t)
	sd := safePluginDir("@vclu/weather")
	oldDir := filepath.Join(loader.pluginsDir, sd, "1.0.2")
	if err := os.MkdirAll(oldDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(oldDir, "plugin.json"), []byte(`{"id":"weather","name":"Weather","version":"1.0.2"}`), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(oldDir, "init.lua"), []byte("return 'old'\n"), 0644); err != nil {
		t.Fatal(err)
	}
	loader.writeLockEntry("@vclu/weather", LockEntry{Version: "1.0.2", GitTag: "weather-v1.0.2", Path: sd + "/1.0.2"})

	err := writeTestConfig(serverDir, &config.VCLUConfig{Plugins: &config.VCLUPluginsConfig{
		Repositories: []config.PluginRepository{configRepo("test", repoURL)},
		Installed: []config.InstalledPlugin{
			{ID: "@vclu/weather", Version: configVersion, Enabled: true, Repository: "test"},
		},
	}})
	if err != nil {
		t.Fatal(err)
	}

	L := lua.NewState()
	t.Cleanup(L.Close)
	if err := L.DoString(`
		got = {}
		JSON = {}
		function JSON:decode(s) return s end
		PluginSandbox = {}
		function PluginSandbox:create(id) end
		function PluginSandbox:setConfig(id, cfg, dir) got.dir = dir end
		function PluginSandbox:execute(id, code) got.code = code; return true end
		function PluginSandbox:cleanup(id) end
		Plugin = {}
		function Plugin.initFromConfig(id, cfg) end
	`); err != nil {
		t.Fatal(err)
	}
	loader.SetLuaExecutor(&luaStateExecutor{L})
	return loader, L
}

func loadedFrom(L *lua.LState) (dir, code string) {
	got := L.GetGlobal("got").(*lua.LTable)
	return got.RawGetString("dir").String(), got.RawGetString("code").String()
}

func TestLoadAllEnabledDownloadsConfiguredVersion(t *testing.T) {
	ts := newTestServer(map[string]string{
		"/plugins.json":                weatherIndex110,
		"/plugins/weather/plugin.json": `{"id":"weather","name":"Weather","version":"1.1.0","files":["init.lua"]}`,
		"/plugins/weather/init.lua":    "return 'new'\n",
	})
	defer ts.Close()
	loader, L := installedWeather(t, ts.URL, "1.1.0")
	loader.httpClient = ts.Client()

	if err := loader.LoadAllEnabled(); err != nil {
		t.Fatalf("LoadAllEnabled: %v", err)
	}
	if dir, code := loadedFrom(L); !strings.HasSuffix(dir, "1.1.0") || !strings.Contains(code, "new") {
		t.Errorf("loaded %s (%q), want 1.1.0", dir, code)
	}
	if entry := loader.ReadLockFile()["@vclu/weather"]; entry.Version != "1.1.0" {
		t.Errorf("lock %+v, want 1.1.0", entry)
	}
	if _, err := os.Stat(filepath.Join(loader.pluginsDir, safePluginDir("@vclu/weather"), "1.0.2")); !os.IsNotExist(err) {
		t.Errorf("1.0.2 still on disk after the update (%v)", err)
	}
}

func TestLoadAllEnabledKeepsFilesWhenDownloadFails(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer ts.Close()
	loader, L := installedWeather(t, ts.URL, "1.1.0")
	loader.httpClient = ts.Client()

	if err := loader.LoadAllEnabled(); err != nil {
		t.Fatalf("LoadAllEnabled: %v", err)
	}
	if dir, code := loadedFrom(L); !strings.HasSuffix(dir, "1.0.2") || !strings.Contains(code, "old") {
		t.Errorf("loaded %s (%q), want the 1.0.2 on disk", dir, code)
	}
}

func TestLoadAllEnabledAtConfiguredVersionDoesNotDownload(t *testing.T) {
	var requests atomic.Int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		_, _ = w.Write([]byte(weatherIndex110))
	}))
	defer ts.Close()
	loader, L := installedWeather(t, ts.URL, "1.0.2")
	loader.httpClient = ts.Client()

	if err := loader.LoadAllEnabled(); err != nil {
		t.Fatalf("LoadAllEnabled: %v", err)
	}
	if n := requests.Load(); n != 0 {
		t.Errorf("%d requests to the repository, want none", n)
	}
	if dir, _ := loadedFrom(L); !strings.HasSuffix(dir, "1.0.2") {
		t.Errorf("loaded %s, want 1.0.2", dir)
	}
}

// The configured version already downloaded but the lock still on the old one (a lost lock
// write): the cache hit points the lock at it, so the loader stops loading the old files.
func TestLoadAllEnabledCachedVersionFixesLock(t *testing.T) {
	ts := newTestServer(map[string]string{"/plugins.json": weatherIndex110})
	defer ts.Close()
	loader, L := installedWeather(t, ts.URL, "1.1.0")
	loader.httpClient = ts.Client()

	newDir := filepath.Join(loader.pluginsDir, "vclu--weather", "1.1.0")
	if err := os.MkdirAll(newDir, 0755); err != nil {
		t.Fatal(err)
	}
	for name, content := range map[string]string{
		"plugin.json": `{"id":"weather","name":"Weather","version":"1.1.0"}`,
		"init.lua":    "return 'new'\n",
		".complete":   "weather-v1.1.0",
	} {
		if err := os.WriteFile(filepath.Join(newDir, name), []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
	}

	if err := loader.LoadAllEnabled(); err != nil {
		t.Fatalf("LoadAllEnabled: %v", err)
	}
	if dir, code := loadedFrom(L); !strings.HasSuffix(dir, "1.1.0") || !strings.Contains(code, "new") {
		t.Errorf("loaded %s (%q), want the cached 1.1.0", dir, code)
	}
	if entry := loader.ReadLockFile()["@vclu/weather"]; entry.Path != "vclu--weather/1.1.0" {
		t.Errorf("lock %+v, want it on 1.1.0", entry)
	}
}

// Without a repository the loader keeps the files it has.
func TestLoadAllEnabledMismatchWithoutRepository(t *testing.T) {
	loader, L := installedWeather(t, "http://127.0.0.1:1", "1.1.0")
	cfg, err := config.ReadMasterConfig(loader.serverDir)
	if err != nil {
		t.Fatal(err)
	}
	cfg.Plugins.Installed[0].Repository = "gone"
	if err := writeTestConfig(loader.serverDir, cfg); err != nil {
		t.Fatal(err)
	}

	if err := loader.LoadAllEnabled(); err != nil {
		t.Fatalf("LoadAllEnabled: %v", err)
	}
	if dir, _ := loadedFrom(L); !strings.HasSuffix(dir, "1.0.2") {
		t.Errorf("loaded %s, want the 1.0.2 on disk", dir)
	}
}

// The repository moved past the configured version: the loader takes what it offers, and a
// second reload hits the cache instead of downloading the files again.
func TestLoadAllEnabledRepositoryAheadDownloadsOnce(t *testing.T) {
	var fileRequests atomic.Int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/plugins.json":
			_, _ = w.Write([]byte(strings.ReplaceAll(weatherIndex110, "1.1.0", "1.2.0")))
		case "/plugins/weather/plugin.json":
			fileRequests.Add(1)
			_, _ = w.Write([]byte(`{"id":"weather","name":"Weather","version":"1.2.0"}`))
		case "/plugins/weather/init.lua":
			fileRequests.Add(1)
			_, _ = w.Write([]byte("return 'newer'\n"))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer ts.Close()
	loader, L := installedWeather(t, ts.URL, "1.1.0")
	loader.httpClient = ts.Client()

	for i := 0; i < 2; i++ {
		if err := loader.LoadAllEnabled(); err != nil {
			t.Fatalf("LoadAllEnabled: %v", err)
		}
	}
	if dir, _ := loadedFrom(L); !strings.HasSuffix(dir, "1.2.0") {
		t.Errorf("loaded %s, want 1.2.0", dir)
	}
	if n := fileRequests.Load(); n != 2 {
		t.Errorf("%d file requests, want 2 (one download)", n)
	}
}

// The panel and the runtime loader are separate Loaders on one plugins dir: downloads
// running at the same time must not lose each other's lock entries.
func TestDownloadPluginConcurrentLoadersKeepLock(t *testing.T) {
	const n = 8
	files := map[string]string{}
	var listings []string
	for i := 0; i < n; i++ {
		id := fmt.Sprintf("p%d", i)
		listings = append(listings, fmt.Sprintf(`{"id":%q,"name":%q,"version":"1.0.0","path":"plugins/%s","tag":"%s-v1.0.0"}`, id, id, id, id))
		files["/plugins/"+id+"/plugin.json"] = fmt.Sprintf(`{"id":%q,"version":"1.0.0"}`, id)
		files["/plugins/"+id+"/init.lua"] = "return {}\n"
	}
	files["/plugins.json"] = `{"version":1,"plugins":[` + strings.Join(listings, ",") + `]}`
	ts := newTestServer(files)
	defer ts.Close()

	serverDir := t.TempDir()
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			l := NewLoader(serverDir)
			l.httpClient = ts.Client()
			if err := l.DownloadPlugin(configRepo("test", ts.URL), fmt.Sprintf("@vclu/p%d", i)); err != nil {
				t.Errorf("p%d: %v", i, err)
			}
		}(i)
	}
	wg.Wait()

	if lock := NewLoader(serverDir).ReadLockFile(); len(lock) != n {
		t.Errorf("lock has %d entries, want %d: %+v", len(lock), n, lock)
	}
}

// Files newer than the config (installed by hand, e.g. 1.0.2 while the config and the repository
// say 1.0.1) are not "updated" back: nothing is fetched, the newer files load and stay on disk.
// Seen on a real installation: Telegram 1.4.0 replaced by 1.3.1 and its directory removed.
func TestLoadAllEnabledKeepsNewerFilesThanConfig(t *testing.T) {
	var requests atomic.Int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		_, _ = w.Write([]byte(`{"version":1,"name":"test","description":"d","url":"x","plugins":[{"id":"weather","name":"Weather","version":"1.0.1","path":"plugins/weather","tag":"weather-v1.0.1"}]}`))
	}))
	defer ts.Close()
	loader, L := installedWeather(t, ts.URL, "1.0.1")
	loader.httpClient = ts.Client()

	if err := loader.LoadAllEnabled(); err != nil {
		t.Fatalf("LoadAllEnabled: %v", err)
	}
	if n := requests.Load(); n != 0 {
		t.Errorf("%d requests to the repository, want none", n)
	}
	if dir, code := loadedFrom(L); !strings.HasSuffix(dir, "1.0.2") || !strings.Contains(code, "old") {
		t.Errorf("loaded %s (%q), want the 1.0.2 on disk", dir, code)
	}
	if _, err := os.Stat(filepath.Join(loader.pluginsDir, safePluginDir("@vclu/weather"), "1.0.2", "plugin.json")); err != nil {
		t.Errorf("1.0.2 gone: %v", err)
	}
}

// A download removes only the versions it replaces: a newer one on disk stays.
func TestDownloadPluginKeepsNewerVersions(t *testing.T) {
	loader, tmpDir := newTestLoader(t)
	sd := safePluginDir("@vclu/weather")
	for _, v := range []string{"0.9.0", "1.4.0", "custom"} {
		dir := filepath.Join(tmpDir, "plugins", sd, v)
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "plugin.json"), []byte(`{"id":"weather"}`), 0644); err != nil {
			t.Fatal(err)
		}
	}
	ts := newTestServer(map[string]string{
		"/plugins.json":                `{"version":1,"name":"test","description":"d","url":"x","plugins":[{"id":"weather","name":"Weather","version":"1.0.0","path":"plugins/weather","tag":"weather-v1.0.0"}]}`,
		"/plugins/weather/plugin.json": `{"id":"weather","name":"Weather","version":"1.0.0","files":["init.lua"]}`,
		"/plugins/weather/init.lua":    "return {}\n",
	})
	defer ts.Close()
	loader.httpClient = ts.Client()

	if err := loader.DownloadPlugin(configRepo("test", ts.URL), "@vclu/weather"); err != nil {
		t.Fatal(err)
	}
	for v, want := range map[string]bool{"0.9.0": false, "1.0.0": true, "1.4.0": true, "custom": true} {
		_, err := os.Stat(filepath.Join(tmpDir, "plugins", sd, v))
		if got := err == nil; got != want {
			t.Errorf("%s on disk = %v, want %v", v, got, want)
		}
	}
}

func TestCompareVersions(t *testing.T) {
	for _, tc := range []struct {
		a, b string
		cmp  int
		ok   bool
	}{
		{"1.4.0", "1.3.1", 1, true}, {"1.3.1", "1.4.0", -1, true}, {"1.10.0", "1.9.9", 1, true},
		{"1.0", "1.0.0", 0, true}, {"v2.0.1", "2.0.1", 0, true}, {"2.0.0-beta", "2.0.0", 0, true},
		{"latest", "1.0.0", 0, false}, {"", "1.0.0", 0, false}, {"1.x", "1.0", 0, false},
		{"1.01", "1.1", 0, true}, {"1..2", "1.0", 0, false}, {"1.0.", "1.0", 0, false},
		{"1.99999999999999999999", "1.0", 0, false}, {"20261025.1", "20261024.9", 1, true},
	} {
		if cmp, ok := compareVersions(tc.a, tc.b); cmp != tc.cmp || ok != tc.ok {
			t.Errorf("compareVersions(%q, %q) = %d, %v; want %d, %v", tc.a, tc.b, cmp, ok, tc.cmp, tc.ok)
		}
	}
}

// An untagged plugin goes to "latest": the tagged directories older than its version go, a
// newer one stays; a tagged download then removes "latest".
func TestDownloadPluginUntaggedCleanup(t *testing.T) {
	loader, tmpDir := newTestLoader(t)
	sd := safePluginDir("@vclu/weather")
	for _, v := range []string{"1.0.0", "3.0.0"} {
		dir := filepath.Join(tmpDir, "plugins", sd, v)
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "plugin.json"), []byte(`{"id":"weather"}`), 0644); err != nil {
			t.Fatal(err)
		}
	}
	var index atomic.Value
	index.Store(`{"version":1,"plugins":[{"id":"weather","name":"Weather","version":"2.0.0","path":"plugins/weather"}]}`)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/plugins.json":
			_, _ = w.Write([]byte(index.Load().(string)))
		case "/plugins/weather/plugin.json":
			_, _ = w.Write([]byte(`{"id":"weather","version":"2.0.0","files":["init.lua"]}`))
		case "/plugins/weather/init.lua":
			_, _ = w.Write([]byte("return {}\n"))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer ts.Close()
	loader.httpClient = ts.Client()
	repo := configRepo("test", ts.URL)

	onDisk := func(want map[string]bool) {
		t.Helper()
		for v, w := range want {
			_, err := os.Stat(filepath.Join(tmpDir, "plugins", sd, v))
			if got := err == nil; got != w {
				t.Errorf("%s on disk = %v, want %v", v, got, w)
			}
		}
	}
	for i := 0; i < 2; i++ {
		if err := loader.DownloadPlugin(repo, "@vclu/weather"); err != nil {
			t.Fatal(err)
		}
	}
	onDisk(map[string]bool{"1.0.0": false, "latest": true, "3.0.0": true})
	if entry := loader.ReadLockFile()["@vclu/weather"]; entry.Path != sd+"/latest" || entry.Version != "2.0.0" {
		t.Errorf("lock %+v, want latest at 2.0.0", entry)
	}

	index.Store(`{"version":1,"plugins":[{"id":"weather","name":"Weather","version":"2.1.0","path":"plugins/weather","tag":"weather-v2.1.0"}]}`)
	if err := loader.DownloadPlugin(repo, "@vclu/weather"); err != nil {
		t.Fatal(err)
	}
	onDisk(map[string]bool{"latest": false, "2.1.0": true, "3.0.0": true})
}

// The panel's Update with a repository behind the disk (1.0.1 offered, 1.0.2 installed by hand)
// is an explicit choice: the plugin runs 1.0.1 from then on and 1.0.2 is not deleted.
func TestUpdateToRepositoryBehindDisk(t *testing.T) {
	ts := newTestServer(map[string]string{
		"/plugins.json":                strings.ReplaceAll(weatherIndex110, "1.1.0", "1.0.1"),
		"/plugins/weather/plugin.json": `{"id":"weather","name":"Weather","version":"1.0.1","files":["init.lua"]}`,
		"/plugins/weather/init.lua":    "return 'registry'\n",
	})
	defer ts.Close()
	loader, L := installedWeather(t, ts.URL, "1.0.1") // the panel writes the config after the download
	loader.httpClient = ts.Client()

	if err := loader.DownloadPlugin(configRepo("test", ts.URL), "@vclu/weather"); err != nil {
		t.Fatal(err)
	}
	if err := loader.LoadAllEnabled(); err != nil {
		t.Fatalf("LoadAllEnabled: %v", err)
	}
	if dir, code := loadedFrom(L); !strings.HasSuffix(dir, "1.0.1") || !strings.Contains(code, "registry") {
		t.Errorf("loaded %s (%q), want 1.0.1", dir, code)
	}
	if _, err := os.Stat(filepath.Join(loader.pluginsDir, safePluginDir("@vclu/weather"), "1.0.2", "plugin.json")); err != nil {
		t.Errorf("1.0.2 gone: %v", err)
	}
}

// Config ahead of both: the disk has 1.0.2, the repository lists only 1.0.1 (rolled back or a
// stale cache). The loader does not go back to 1.0.1: the files on disk load.
func TestLoadAllEnabledRepositoryBehindDiskDoesNotDowngrade(t *testing.T) {
	var files atomic.Int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/plugins.json" {
			_, _ = w.Write([]byte(`{"version":1,"name":"test","description":"d","url":"x","plugins":[{"id":"weather","name":"Weather","version":"1.0.1","path":"plugins/weather","tag":"weather-v1.0.1"}]}`))
			return
		}
		files.Add(1)
		w.WriteHeader(http.StatusNotFound)
	}))
	defer ts.Close()
	loader, L := installedWeather(t, ts.URL, "1.1.0")
	loader.httpClient = ts.Client()

	if err := loader.LoadAllEnabled(); err != nil {
		t.Fatalf("LoadAllEnabled: %v", err)
	}
	if n := files.Load(); n != 0 {
		t.Errorf("%d plugin file requests, want none", n)
	}
	if dir, code := loadedFrom(L); !strings.HasSuffix(dir, "1.0.2") || !strings.Contains(code, "old") {
		t.Errorf("loaded %s (%q), want the 1.0.2 on disk", dir, code)
	}
}

// A check that failed (offline) is not repeated on the next reload: a promote reloads the
// runtime, and each check could wait for the network.
func TestLoadAllEnabledDoesNotRetryFailedUpdateOnEveryReload(t *testing.T) {
	var requests atomic.Int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer ts.Close()
	loader, L := installedWeather(t, ts.URL, "1.1.0")
	loader.httpClient = ts.Client()

	for i := 0; i < 3; i++ {
		if err := loader.LoadAllEnabled(); err != nil {
			t.Fatalf("LoadAllEnabled %d: %v", i+1, err)
		}
	}
	if n := requests.Load(); n != 1 {
		t.Errorf("%d requests in 3 reloads, want 1", n)
	}
	if dir, _ := loadedFrom(L); !strings.HasSuffix(dir, "1.0.2") {
		t.Errorf("loaded %s, want 1.0.2", dir)
	}
	// after the retry time the check runs again
	loader.updateMu.Lock()
	for k := range loader.updateRetry {
		loader.updateRetry[k] = time.Now().Add(-time.Second)
	}
	loader.updateMu.Unlock()
	if err := loader.LoadAllEnabled(); err != nil {
		t.Fatal(err)
	}
	if n := requests.Load(); n != 2 {
		t.Errorf("%d requests after the retry time, want 2", n)
	}
}
