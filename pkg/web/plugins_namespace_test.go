package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Domktorymysli/vclu/pkg/config"
	"github.com/gin-gonic/gin"
)

// A repository other than the official one listing @vclu plugins: not offered, not installed.
func TestReservedNamespaceOnlyFromOfficial(t *testing.T) {
	repo := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/plugins.json" {
			_, _ = w.Write([]byte(`{"namespace":"vclu","plugins":[{"id":"tasmota","version":"9.0.0","path":"plugins/tasmota"}]}`))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	t.Cleanup(repo.Close)
	dir := t.TempDir()
	mc := &config.VCLUConfig{Plugins: &config.VCLUPluginsConfig{
		Repositories: []config.PluginRepository{{Name: "test", URL: repo.URL, Enabled: true}},
	}}
	if err := writeTestConfig(dir, mc); err != nil {
		t.Fatal(err)
	}

	found, errs := availablePlugins(context.Background(), mc, dir)
	if len(found) != 0 || len(errs) != 1 || !strings.Contains(errs[0], "reserved") {
		t.Errorf("offered %+v, errors %v", found, errs)
	}
	w := installRequest(&Server{serverDir: dir}, map[string]any{"id": "tasmota", "repository": "test"})
	if w.Code != http.StatusForbidden || !strings.Contains(w.Body.String(), "reserved_namespace") {
		t.Errorf("install: %d %s", w.Code, w.Body.String())
	}
}

// plugin.json unreadable: the permissions stay as they were, none is saved past it.
func TestPermissionsNotSavedWithoutManifest(t *testing.T) {
	dir := t.TempDir()
	files := filepath.Join(dir, "plugins", "jan--broken", "1.0.0")
	if err := os.MkdirAll(files, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(files, "plugin.json"), []byte(`{"id":`), 0o644); err != nil {
		t.Fatal(err)
	}
	before := &config.PluginGrants{}
	if err := writeTestConfig(dir, &config.VCLUConfig{Plugins: &config.VCLUPluginsConfig{
		Installed: []config.InstalledPlugin{{ID: "@jan/broken", Version: "1.0.0", Grants: before}},
	}}); err != nil {
		t.Fatal(err)
	}
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Params = gin.Params{{Key: "id", Value: "@jan/broken"}}
	c.Request = httptest.NewRequest(http.MethodPost, "/api/plugins/@jan/broken/permissions",
		strings.NewReader(`{"lan":{"anyPort":true},"clu":{},"tftp":{}}`))
	c.Request.Header.Set("Content-Type", "application/json")
	(&Server{serverDir: dir}).handlePluginsAPIPermissions(c)
	if w.Code != http.StatusConflict {
		t.Errorf("status %d: %s", w.Code, w.Body.String())
	}
	mc, _ := config.ReadMasterConfig(dir)
	if g := mc.Plugins.Installed[0].Grants; g == nil || g.LAN != nil || g.CLU != nil || g.TFTP != nil {
		t.Errorf("grants after: %+v", g)
	}
}
