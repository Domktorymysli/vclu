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

	"github.com/Domktorymysli/vclu/pkg/config"
	lua "github.com/yuin/gopher-lua"
)

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

	err := config.WriteMasterConfig(serverDir, &config.VCLUConfig{Plugins: &config.VCLUPluginsConfig{
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
	if err := config.WriteMasterConfig(loader.serverDir, cfg); err != nil {
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
