package plugins

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
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
