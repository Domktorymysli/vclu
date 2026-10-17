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
	repo := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"namespace":"vclu","plugins":[{"id":"salda-recuperator","version":"1.2.0"}]}`))
	}))
	defer repo.Close()

	dir := t.TempDir()
	err := config.WriteMasterConfig(dir, &config.VCLUConfig{Plugins: &config.VCLUPluginsConfig{
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
}

// A first install answers with the full id and version too.
func TestPluginsAPIInstallAnswersVersion(t *testing.T) {
	repo := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"namespace":"vclu","plugins":[{"id":"solax","version":"1.0.1"}]}`))
	}))
	defer repo.Close()

	dir := t.TempDir()
	err := config.WriteMasterConfig(dir, &config.VCLUConfig{Plugins: &config.VCLUPluginsConfig{
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
	err := config.WriteMasterConfig(dir, &config.VCLUConfig{Plugins: &config.VCLUPluginsConfig{
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
