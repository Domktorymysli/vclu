package web

import (
	"encoding/json"
	"html/template"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Domktorymysli/vclu/pkg/config"
	"github.com/gin-gonic/gin"
)

// manifestRepo serves a repository with one plugin whose plugin.json is manifest.
func manifestRepo(t *testing.T, id, version, manifest string) *httptest.Server {
	t.Helper()
	repo := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/plugins.json":
			_, _ = w.Write([]byte(`{"namespace":"jan","plugins":[{"id":"` + id + `","version":"` + version +
				`","path":"plugins/` + id + `","tag":"` + id + `-v` + version + `"}]}`))
		case "/plugins/" + id + "/plugin.json":
			_, _ = w.Write([]byte(manifest))
		case "/plugins/" + id + "/init.lua":
			_, _ = w.Write([]byte("return {}\n"))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(repo.Close)
	return repo
}

func installedPlugins(t *testing.T, s *Server) []struct {
	ID                 string
	Grants             *config.PluginGrants
	PendingPermissions []string
} {
	t.Helper()
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/plugins/installed", nil)
	s.handlePluginsAPIInstalled(c)
	var out struct {
		Installed []struct {
			ID                 string
			Grants             *config.PluginGrants
			PendingPermissions []string
		}
	}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("installed %s: %v", w.Body.String(), err)
	}
	return out.Installed
}

// lanOnly: lan allowed in full, nothing else.
func lanOnly(g *config.PluginGrants) bool {
	return g != nil && g.LAN != nil && g.LAN.Hosts == nil && g.MQTT == nil && g.Modbus == nil
}

// Installing allows what the plugin asks for at that moment.
func TestPluginsAPIInstallRecordsGrants(t *testing.T) {
	repo := manifestRepo(t, "shelly", "1.0.0",
		`{"id":"shelly","version":"1.0.0","api":2,"permissions":{"lan":true},"files":["init.lua"]}`)
	dir := t.TempDir()
	if err := writeTestConfig(dir, &config.VCLUConfig{Plugins: &config.VCLUPluginsConfig{
		Repositories: []config.PluginRepository{{Name: "test", URL: repo.URL, Enabled: true}},
	}}); err != nil {
		t.Fatal(err)
	}
	s := &Server{serverDir: dir}
	postInstall(t, s, "shelly")

	got := installedPlugins(t, s)
	if len(got) != 1 || !lanOnly(got[0].Grants) || len(got[0].PendingPermissions) > 0 {
		t.Errorf("installed: %+v, want lan allowed and nothing pending", got)
	}
}

// An update that asks for more does not get it: the plugin installed before grants keeps
// what its old files asked for, the rest is pending until the user allows it.
func TestPluginsAPIUpdateAskingForMoreWaitsForTheUser(t *testing.T) {
	repo := manifestRepo(t, "shelly", "1.1.0",
		`{"id":"shelly","version":"1.1.0","api":2,"permissions":{"lan":true,"mqtt":true},"files":["init.lua"]}`)
	dir := t.TempDir()
	old := filepath.Join(dir, "plugins", "jan--shelly", "1.0.0")
	if err := os.MkdirAll(old, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(old, "plugin.json"),
		[]byte(`{"id":"shelly","version":"1.0.0","api":2,"permissions":{"lan":true}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := writeTestConfig(dir, &config.VCLUConfig{Plugins: &config.VCLUPluginsConfig{
		Repositories: []config.PluginRepository{{Name: "test", URL: repo.URL, Enabled: true}},
		Installed:    []config.InstalledPlugin{{ID: "@jan/shelly", Version: "1.0.0", Enabled: true, Repository: "test"}},
	}}); err != nil {
		t.Fatal(err)
	}
	s := &Server{serverDir: dir}
	if resp := postInstall(t, s, "shelly"); resp.Action != "updated" {
		t.Fatalf("answer %+v", resp)
	}

	got := installedPlugins(t, s)
	if len(got) != 1 || !lanOnly(got[0].Grants) || strings.Join(got[0].PendingPermissions, ",") != "mqtt" {
		t.Fatalf("installed after the update: %+v, want lan allowed and mqtt pending", got)
	}

	// The user allows it
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Params = gin.Params{{Key: "id", Value: "@jan/shelly"}}
	c.Request = httptest.NewRequest(http.MethodPost, "/api/plugins/@jan/shelly/permissions",
		strings.NewReader(`{"lan":{},"mqtt":{}}`))
	c.Request.Header.Set("Content-Type", "application/json")
	s.handlePluginsAPIPermissions(c)
	if w.Code != http.StatusOK {
		t.Fatalf("permissions: %d %s", w.Code, w.Body.String())
	}
	got = installedPlugins(t, s)
	if g := got[0].Grants; g == nil || g.LAN == nil || g.MQTT == nil || len(got[0].PendingPermissions) > 0 {
		t.Errorf("after allowing: %+v", got)
	}
}

// Permissions of a plugin that is not installed: 404.
func TestPluginsAPIPermissionsUnknownPlugin(t *testing.T) {
	dir := t.TempDir()
	if err := writeTestConfig(dir, &config.VCLUConfig{Plugins: &config.VCLUPluginsConfig{}}); err != nil {
		t.Fatal(err)
	}
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Params = gin.Params{{Key: "id", Value: "@jan/nope"}}
	c.Request = httptest.NewRequest(http.MethodPost, "/api/plugins/@jan/nope/permissions", strings.NewReader(`{"lan":{}}`))
	c.Request.Header.Set("Content-Type", "application/json")
	(&Server{serverDir: dir}).handlePluginsAPIPermissions(c)
	if w.Code != http.StatusNotFound {
		t.Errorf("status %d: %s", w.Code, w.Body.String())
	}
}

// The plugins page hands the script what an update asks for and what is allowed, and
// whether the plugin waits for configuration.
func TestPluginsPageShowsPendingPermissions(t *testing.T) {
	dir := t.TempDir()
	pluginDir := filepath.Join(dir, "plugins", "jan--shelly", "1.1.0")
	if err := os.MkdirAll(pluginDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pluginDir, "plugin.json"),
		[]byte(`{"id":"shelly","version":"1.1.0","api":2,"permissions":{"lan":true,"mqtt":true}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := writeTestConfig(dir, &config.VCLUConfig{Plugins: &config.VCLUPluginsConfig{
		Installed: []config.InstalledPlugin{{ID: "@jan/shelly", Version: "1.1.0", Enabled: true, Grants: &config.PluginGrants{LAN: &config.LANGrant{}}}},
	}}); err != nil {
		t.Fatal(err)
	}
	s := &Server{serverDir: dir}
	gin.SetMode(gin.TestMode)
	r := gin.New()
	tmpl := template.New("").Funcs(template.FuncMap{
		"t":     func(lang, key string) string { return key },
		"json":  func(v interface{}) template.JS { b, _ := json.Marshal(v); return template.JS(b) },
		"brand": brandHTML,
	})
	r.SetHTMLTemplate(template.Must(tmpl.ParseGlob("../../web/templates/*.html")))
	r.GET("/plugins", s.handlePlugins)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/plugins", nil))
	body := strings.Join(strings.Fields(w.Body.String()), "") // html/template pads JS values
	for _, want := range []string{`grants:{"lan":{}}`, `needsConfig:false`, `pendingPermissions:["mqtt"]`} {
		if w.Code != 200 || !strings.Contains(body, want) {
			t.Errorf("%d, %q missing", w.Code, want)
		}
	}
}

// A host or topic filter that is not one is refused and the grants stay as they were.
func TestPluginsAPIPermissionsRefusesBadLists(t *testing.T) {
	dir := t.TempDir()
	if err := writeTestConfig(dir, &config.VCLUConfig{Plugins: &config.VCLUPluginsConfig{
		Installed: []config.InstalledPlugin{{ID: "@jan/shelly", Grants: &config.PluginGrants{LAN: &config.LANGrant{}}}},
	}}); err != nil {
		t.Fatal(err)
	}
	s := &Server{serverDir: dir}
	for _, body := range []string{`{"lan":{"hosts":["shelly.local"]}}`, `{"mqtt":{"subscribe":["a/#/b"]}}`} {
		gin.SetMode(gin.TestMode)
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Params = gin.Params{{Key: "id", Value: "@jan/shelly"}}
		c.Request = httptest.NewRequest(http.MethodPost, "/api/plugins/@jan/shelly/permissions", strings.NewReader(body))
		c.Request.Header.Set("Content-Type", "application/json")
		s.handlePluginsAPIPermissions(c)
		if w.Code != http.StatusBadRequest {
			t.Errorf("%s: %d %s", body, w.Code, w.Body.String())
		}
	}
	if got := installedPlugins(t, s); !lanOnly(got[0].Grants) {
		t.Errorf("grants after refused changes: %+v", got[0].Grants)
	}
}
