package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Domktorymysli/vclu/pkg/config"
	"github.com/gin-gonic/gin"
)

// Updating an installed plugin answers with its full id and new version, and keeps its
// configuration (#31: the page keys installed plugins by full id).
func TestPluginsAPIInstallUpdateKeepsConfig(t *testing.T) {
	repo := pluginRepo(t, "salda-recuperator", "1.2.0", true)

	dir := t.TempDir()
	err := writeTestConfig(dir, &config.VCLUConfig{Plugins: &config.VCLUPluginsConfig{
		Repositories: []config.PluginRepository{{Name: "test", URL: repo.URL, Enabled: true}},
		Installed: []config.InstalledPlugin{{
			ID: "@vclu/salda-recuperator", Version: "1.1.0", Enabled: false, Repository: "test",
			Config: map[string]interface{}{"host": "192.168.0.20"},
		}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	s := &Server{serverDir: dir}

	if resp := postInstall(t, s, "salda-recuperator"); resp.Action != "updated" || resp.FullID != "@vclu/salda-recuperator" || resp.Version != "1.2.0" {
		t.Fatalf("answer %+v", resp)
	}

	cfg, err := config.ReadMasterConfig(dir)
	if err != nil {
		t.Fatal(err)
	}
	inst := cfg.Plugins.Installed
	if len(inst) != 1 || inst[0].ID != "@vclu/salda-recuperator" || inst[0].Version != "1.2.0" ||
		inst[0].Enabled || inst[0].Config["host"] != "192.168.0.20" {
		t.Errorf(".vclu.json plugins: %+v", inst)
	}
	// The update brings the files too, not only the version in the config (#112)
	if _, err := os.Stat(filepath.Join(dir, "plugins", "vclu--salda-recuperator", "1.2.0", "init.lua")); err != nil {
		t.Errorf("1.2.0 not on disk: %v", err)
	}
}

// The update's download removes the version it replaces, not one newer than it.
func TestPluginsAPIInstallUpdateCleansOlderOnly(t *testing.T) {
	repo := pluginRepo(t, "salda-recuperator", "1.2.0", true)

	dir := t.TempDir()
	base := filepath.Join(dir, "plugins", "vclu--salda-recuperator")
	for _, v := range []string{"1.1.0", "2.0.0"} {
		if err := os.MkdirAll(filepath.Join(base, v), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(base, v, "plugin.json"), []byte(`{"version":"`+v+`"}`), 0644); err != nil {
			t.Fatal(err)
		}
	}
	err := writeTestConfig(dir, &config.VCLUConfig{Plugins: &config.VCLUPluginsConfig{
		Repositories: []config.PluginRepository{{Name: "test", URL: repo.URL, Enabled: true}},
		Installed:    []config.InstalledPlugin{{ID: "@vclu/salda-recuperator", Version: "1.1.0", Enabled: true, Repository: "test"}},
	}})
	if err != nil {
		t.Fatal(err)
	}

	postInstall(t, &Server{serverDir: dir}, "salda-recuperator")
	for v, want := range map[string]bool{"1.1.0": false, "1.2.0": true, "2.0.0": true} {
		_, err := os.Stat(filepath.Join(base, v))
		if got := err == nil; got != want {
			t.Errorf("%s on disk = %v, want %v", v, got, want)
		}
	}
}

// An update whose download fails answers with an error and leaves the config as it was (#112).
func TestPluginsAPIInstallUpdateDownloadFails(t *testing.T) {
	repo := pluginRepo(t, "salda-recuperator", "1.2.0", false)

	dir := t.TempDir()
	installed := []config.InstalledPlugin{{ID: "@vclu/salda-recuperator", Version: "1.1.0", Enabled: true, Repository: "test"}}
	err := writeTestConfig(dir, &config.VCLUConfig{Plugins: &config.VCLUPluginsConfig{
		Repositories: []config.PluginRepository{{Name: "test", URL: repo.URL, Enabled: true}},
		Installed:    installed,
	}})
	if err != nil {
		t.Fatal(err)
	}
	s := &Server{serverDir: dir}

	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/plugins/install",
		strings.NewReader(`{"id":"salda-recuperator","repository":"test"}`))
	c.Request.Header.Set("Content-Type", "application/json")
	s.handlePluginsAPIInstall(c)

	if w.Code == http.StatusOK || !strings.Contains(w.Body.String(), "download") {
		t.Errorf("status %d: %s, want a download error", w.Code, w.Body.String())
	}
	cfg, err := config.ReadMasterConfig(dir)
	if err != nil {
		t.Fatal(err)
	}
	if inst := cfg.Plugins.Installed; len(inst) != 1 || inst[0].Version != "1.1.0" {
		t.Errorf(".vclu.json plugins: %+v, want 1.1.0 kept", inst)
	}
}

// pluginRepo serves a repository with one tagged plugin; without files its downloads answer 404.
func pluginRepo(t *testing.T, id, version string, files bool) *httptest.Server {
	t.Helper()
	repo := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/plugins.json":
			_, _ = w.Write([]byte(`{"namespace":"vclu","plugins":[{"id":"` + id + `","version":"` + version +
				`","path":"plugins/` + id + `","tag":"` + id + `-v` + version + `"}]}`))
		case files && r.URL.Path == "/plugins/"+id+"/plugin.json":
			_, _ = w.Write([]byte(`{"id":"` + id + `","version":"` + version + `","files":["init.lua"]}`))
		case files && r.URL.Path == "/plugins/"+id+"/init.lua":
			_, _ = w.Write([]byte("return {}\n"))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(repo.Close)
	return repo
}

// A first install answers with the full id and version too.
func TestPluginsAPIInstallAnswersVersion(t *testing.T) {
	repo := pluginRepo(t, "solax", "1.0.1", true)

	dir := t.TempDir()
	err := writeTestConfig(dir, &config.VCLUConfig{Plugins: &config.VCLUPluginsConfig{
		Repositories: []config.PluginRepository{{Name: "test", URL: repo.URL, Enabled: true}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	s := &Server{serverDir: dir}

	if resp := postInstall(t, s, "solax"); resp.Action != "installed" || resp.FullID != "@vclu/solax" || resp.Version != "1.0.1" {
		t.Fatalf("answer %+v", resp)
	}
}

type installAnswer struct {
	Action, FullID, Version string
}

func postInstall(t *testing.T, s *Server, id string) installAnswer {
	t.Helper()
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/plugins/install",
		strings.NewReader(`{"id":"`+id+`","repository":"test"}`))
	c.Request.Header.Set("Content-Type", "application/json")
	s.handlePluginsAPIInstall(c)

	var resp installAnswer
	if w.Code != http.StatusOK {
		t.Fatalf("status %d: %s", w.Code, w.Body.String())
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("answer %s: %v", w.Body.String(), err)
	}
	return resp
}

// Only a plugin that asks for configuration and has none is marked "not configured";
// a factory plugin declares none and is configured from code.
func TestPluginsAPIInstalledNeedsConfig(t *testing.T) {
	dir := t.TempDir()
	manifests := map[string]string{
		"vclu--musiccast": `{"id":"musiccast","version":"0.1.1"}`,
		"vclu--solax":     `{"id":"solax","version":"1.0.1","config":{"host":{"type":"string"}}}`,
		"vclu--salda":     `{"id":"salda","version":"1.2.0","config":{"host":{"type":"string"}}}`,
	}
	for name, manifest := range manifests {
		pluginDir := filepath.Join(dir, "plugins", name, "1.0.0")
		if err := os.MkdirAll(pluginDir, 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(pluginDir, "plugin.json"), []byte(manifest), 0644); err != nil {
			t.Fatal(err)
		}
	}
	err := writeTestConfig(dir, &config.VCLUConfig{Plugins: &config.VCLUPluginsConfig{
		Installed: []config.InstalledPlugin{
			{ID: "@vclu/musiccast", Version: "0.1.1", Enabled: true},
			{ID: "@vclu/solax", Version: "1.0.1", Enabled: true},
			{ID: "@vclu/salda", Version: "1.2.0", Enabled: true, Config: map[string]interface{}{"host": "192.168.0.20"}},
		},
	}})
	if err != nil {
		t.Fatal(err)
	}
	s := &Server{serverDir: dir}

	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/plugins/installed", nil)
	s.handlePluginsAPIInstalled(c)

	var resp struct {
		Installed []struct {
			ID          string `json:"id"`
			NeedsConfig bool   `json:"needsConfig"`
		} `json:"installed"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("%v: %s", err, w.Body.String())
	}
	got := map[string]bool{}
	for _, p := range resp.Installed {
		got[p.ID] = p.NeedsConfig
	}
	want := map[string]bool{"@vclu/musiccast": false, "@vclu/solax": true, "@vclu/salda": false}
	for id, needs := range want {
		if v, ok := got[id]; !ok || v != needs {
			t.Errorf("%s needsConfig = %v (listed %v), want %v", id, v, ok, needs)
		}
	}
}
