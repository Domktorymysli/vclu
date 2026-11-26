package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/Domktorymysli/vclu/pkg/config"
	"github.com/gin-gonic/gin"
)

// pluginRepoServer serves one plugin; version and manifest can change between calls.
func pluginRepoServer(t *testing.T, version, manifest *atomic.Value) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		v := version.Load().(string)
		switch r.URL.Path {
		case "/plugins.json":
			_, _ = w.Write([]byte(`{"namespace":"jan","plugins":[{"id":"cloud","version":"` + v + `","path":"plugins/cloud","tag":"cloud-v` + v + `"}]}`))
		case "/plugins/cloud/plugin.json":
			_, _ = w.Write([]byte(manifest.Load().(string)))
		case "/plugins/cloud/init.lua":
			_, _ = w.Write([]byte("return {}\n"))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func callInstall(t *testing.T, s *Server, body string) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/plugins/install", strings.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	s.handlePluginsAPIInstall(c)
	return w
}

func callPreview(t *testing.T, s *Server, query string) map[string]any {
	t.Helper()
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/plugins/preview?"+query, nil)
	s.handlePluginsAPIPreview(c)
	var out map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("preview: %s", w.Body.String())
	}
	return out
}

func testInstalled(t *testing.T, dir, id string) config.InstalledPlugin {
	t.Helper()
	mc, err := config.ReadMasterConfig(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, inst := range mc.Plugins.Installed {
		if inst.ID == id {
			return inst
		}
	}
	t.Fatalf("%s not installed", id)
	return config.InstalledPlugin{}
}

// An update to a manifest on a lower plugin API is refused and the installed version stays;
// installing it from scratch works.
func TestPluginsAPIUpdateRefusesLowerAPI(t *testing.T) {
	gin.SetMode(gin.TestMode)
	var version, manifest atomic.Value
	version.Store("1.0.0")
	manifest.Store(`{"id":"cloud","version":"1.0.0","api":2,"permissions":{"internet":["a.example.com"]},"files":["init.lua"]}`)
	repo := pluginRepoServer(t, &version, &manifest)
	dir := t.TempDir()
	if err := writeTestConfig(dir, &config.VCLUConfig{Plugins: &config.VCLUPluginsConfig{
		Repositories: []config.PluginRepository{{Name: "test", URL: repo.URL, Enabled: true}},
	}}); err != nil {
		t.Fatal(err)
	}
	s := &Server{serverDir: dir}

	if w := callInstall(t, s, `{"id":"cloud","repository":"test"}`); w.Code != http.StatusOK {
		t.Fatalf("install: %d %s", w.Code, w.Body.String())
	}
	if got := testInstalled(t, dir, "@jan/cloud"); got.API != 2 || got.Version != "1.0.0" {
		t.Fatalf("installed %+v, want api 2 at 1.0.0", got)
	}

	version.Store("1.1.0")
	manifest.Store(`{"id":"cloud","version":"1.1.0","files":["init.lua"]}`)
	if p := callPreview(t, s, "id=cloud&repository=test&fullId=@jan/cloud"); p["downgradeFrom"] != float64(2) || p["api"] != float64(1) {
		t.Errorf("preview %v, want downgradeFrom 2 and api 1", p)
	}
	w := callInstall(t, s, `{"id":"cloud","repository":"test"}`)
	if w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), "api_downgrade") {
		t.Fatalf("update to API 1: %d %s, want 409 api_downgrade", w.Code, w.Body.String())
	}
	if got := testInstalled(t, dir, "@jan/cloud"); got.Version != "1.0.0" || got.API != 2 {
		t.Errorf("after the refused update %+v, want 1.0.0 on api 2", got)
	}

	// uninstalled and installed again: the user sees what API 1 asks for
	w = httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodDelete, "/api/plugins/@jan/cloud", nil)
	c.Params = gin.Params{{Key: "id", Value: "@jan/cloud"}}
	s.handlePluginsAPIUninstall(c)
	if w.Code != http.StatusOK {
		t.Fatalf("uninstall: %d %s", w.Code, w.Body.String())
	}
	if p := callPreview(t, s, "id=cloud&repository=test&fullId=@jan/cloud"); p["downgradeFrom"] != float64(0) {
		t.Errorf("preview after uninstall %v, want no downgrade", p)
	}
	if w := callInstall(t, s, `{"id":"cloud","repository":"test"}`); w.Code != http.StatusOK {
		t.Fatalf("install from scratch: %d %s", w.Code, w.Body.String())
	}
	if got := testInstalled(t, dir, "@jan/cloud"); got.API != 1 || got.Version != "1.1.0" {
		t.Errorf("installed from scratch %+v, want api 1 at 1.1.0", got)
	}
}
