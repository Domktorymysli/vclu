package web

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/Domktorymysli/vclu/pkg/config"
	"github.com/gin-gonic/gin"
)

func setRepos(t *testing.T, dir string, repos ...config.PluginRepository) {
	t.Helper()
	if err := config.UpdateMasterConfig(dir, func(mc *config.VCLUConfig) error {
		mc.Plugins.Repositories = repos
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

// A repository removed and added again under its name at another address does not update the
// plugin; the same address written with a trailing / or .git does.
func TestPluginsAPIUpdateOnlyFromTheSameAddress(t *testing.T) {
	gin.SetMode(gin.TestMode)
	var v1, m1, v2, m2 atomic.Value
	v1.Store("1.0.0")
	m1.Store(`{"id":"cloud","version":"1.0.0","api":2,"permissions":{}}`)
	v2.Store("1.1.0")
	m2.Store(`{"id":"cloud","version":"1.1.0","api":2,"permissions":{"internet":"*"}}`)
	first := pluginRepoServer(t, &v1, &m1)
	other := pluginRepoServer(t, &v2, &m2)
	dir := t.TempDir()
	if err := writeTestConfig(dir, &config.VCLUConfig{Plugins: &config.VCLUPluginsConfig{
		Repositories: []config.PluginRepository{{Name: "test", URL: first.URL, Enabled: true}},
	}}); err != nil {
		t.Fatal(err)
	}
	s := &Server{serverDir: dir}
	if w := callInstall(t, s, `{"id":"cloud","repository":"test"}`); w.Code != http.StatusOK {
		t.Fatalf("install: %d %s", w.Code, w.Body.String())
	}
	if got := testInstalled(t, dir, "@jan/cloud"); got.RepositoryURL == "" {
		t.Fatalf("address not recorded: %+v", got)
	}

	setRepos(t, dir, config.PluginRepository{Name: "test", URL: other.URL, Enabled: true})
	w := callInstall(t, s, `{"id":"cloud","repository":"test"}`)
	if w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), "source_moved") {
		t.Fatalf("update from another address: %d %s, want 409 source_moved", w.Code, w.Body.String())
	}
	if got := testInstalled(t, dir, "@jan/cloud"); got.Version != "1.0.0" {
		t.Errorf("version %s after the refusal, want 1.0.0", got.Version)
	}
	mc, _ := config.ReadMasterConfig(dir)
	found, _ := availablePlugins(t.Context(), mc, dir)
	if len(found) != 1 || !found[0].SourceMoved || found[0].UpdateAvailable {
		t.Errorf("listing %+v, want source moved and no update", found)
	}

	// the first address again, written differently
	v1.Store("1.0.1")
	m1.Store(`{"id":"cloud","version":"1.0.1","api":2,"permissions":{}}`)
	setRepos(t, dir, config.PluginRepository{Name: "test", URL: first.URL + "/", Enabled: true})
	if w := callInstall(t, s, `{"id":"cloud","repository":"test"}`); w.Code != http.StatusOK {
		t.Fatalf("update from the same address with /: %d %s", w.Code, w.Body.String())
	}
	if got := testInstalled(t, dir, "@jan/cloud"); got.Version != "1.0.1" {
		t.Errorf("version %s, want 1.0.1", got.Version)
	}
}

// An entry from before addresses were recorded is checked against where its files came from.
func TestPluginsAPIUpdateOldEntryChecksTheFiles(t *testing.T) {
	gin.SetMode(gin.TestMode)
	var v1, m1, v2, m2 atomic.Value
	v1.Store("1.0.0")
	m1.Store(`{"id":"cloud","version":"1.0.0","api":2,"permissions":{}}`)
	v2.Store("1.1.0")
	m2.Store(`{"id":"cloud","version":"1.1.0","api":2,"permissions":{}}`)
	first := pluginRepoServer(t, &v1, &m1)
	other := pluginRepoServer(t, &v2, &m2)
	dir := t.TempDir()
	if err := writeTestConfig(dir, &config.VCLUConfig{Plugins: &config.VCLUPluginsConfig{
		Repositories: []config.PluginRepository{{Name: "test", URL: first.URL, Enabled: true}},
	}}); err != nil {
		t.Fatal(err)
	}
	s := &Server{serverDir: dir}
	if w := callInstall(t, s, `{"id":"cloud","repository":"test"}`); w.Code != http.StatusOK {
		t.Fatalf("install: %d %s", w.Code, w.Body.String())
	}
	// as written by a vclu that did not record the address
	if err := config.UpdateMasterConfig(dir, func(mc *config.VCLUConfig) error {
		mc.Plugins.Installed[0].RepositoryURL = ""
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	setRepos(t, dir, config.PluginRepository{Name: "test", URL: other.URL, Enabled: true})
	if w := callInstall(t, s, `{"id":"cloud","repository":"test"}`); w.Code != http.StatusConflict {
		t.Fatalf("update of an old entry from another address: %d %s, want 409", w.Code, w.Body.String())
	}
}

// Two plugins whose wizards would write the same module file are not installed together.
func TestPluginsAPIInstallRefusesASharedModuleName(t *testing.T) {
	gin.SetMode(gin.TestMode)
	repo := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/plugins.json":
			_, _ = w.Write([]byte(`{"namespace":"jan","plugins":[{"id":"b.c","version":"1.0.0","path":"plugins/bc1"},{"id":"b_c","version":"1.0.0","path":"plugins/bc2"}]}`))
		case "/plugins/bc1/plugin.json":
			_, _ = w.Write([]byte(`{"id":"b.c","version":"1.0.0","api":2,"permissions":{}}`))
		case "/plugins/bc2/plugin.json":
			_, _ = w.Write([]byte(`{"id":"b_c","version":"1.0.0","api":2,"permissions":{}}`))
		case "/plugins/bc1/init.lua", "/plugins/bc2/init.lua":
			_, _ = w.Write([]byte("return {}\n"))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer repo.Close()
	dir := t.TempDir()
	if err := writeTestConfig(dir, &config.VCLUConfig{Plugins: &config.VCLUPluginsConfig{
		Repositories: []config.PluginRepository{{Name: "test", URL: repo.URL, Enabled: true}},
	}}); err != nil {
		t.Fatal(err)
	}
	s := &Server{serverDir: dir}
	if w := callInstall(t, s, `{"id":"b.c","repository":"test"}`); w.Code != http.StatusOK {
		t.Fatalf("install b.c: %d %s", w.Code, w.Body.String())
	}
	w := callInstall(t, s, `{"id":"b_c","repository":"test"}`)
	if w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), "module_name") {
		t.Fatalf("install b_c next to b.c: %d %s, want 409 module_name", w.Code, w.Body.String())
	}
}
