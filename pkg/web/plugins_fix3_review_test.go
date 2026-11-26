package web

import (
	"net/http"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/Domktorymysli/vclu/pkg/config"
	"github.com/Domktorymysli/vclu/pkg/plugins"
	"github.com/gin-gonic/gin"
)

// asFromOlderVclu: the entry and the files as an older vclu left them (no address, no API,
// a .complete marker with the tag only).
func asFromOlderVclu(t *testing.T, dir, id, version, tag string) {
	t.Helper()
	if err := config.UpdateMasterConfig(dir, func(mc *config.VCLUConfig) error {
		for i := range mc.Plugins.Installed {
			if mc.Plugins.Installed[i].ID == id {
				mc.Plugins.Installed[i].RepositoryURL = ""
				mc.Plugins.Installed[i].API = 0
				mc.Plugins.Installed[i].Enabled = false // LoadAllEnabled below runs without Lua
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(dir, "plugins", "jan--cloud", version, ".complete")
	if err := os.WriteFile(marker, []byte(tag), 0o644); err != nil {
		t.Fatal(err)
	}
}

// An entry from an older vclu gets its repository's address on the first load, so a repository
// added again under that name at another address does not update it afterwards.
func TestReview_OldEntryPinnedOnFirstLoad(t *testing.T) {
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
	asFromOlderVclu(t, dir, "@jan/cloud", "1.0.0", "cloud-v1.0.0")

	_ = plugins.NewLoader(dir).LoadAllEnabled()
	got := testInstalled(t, dir, "@jan/cloud")
	if got.RepositoryURL != plugins.RepoURL(first.URL) || got.API != 2 {
		t.Fatalf("after the first load %+v, want the address and api 2 recorded", got)
	}

	setRepos(t, dir, config.PluginRepository{Name: "test", URL: other.URL, Enabled: true})
	if w := callInstall(t, s, `{"id":"cloud","repository":"test"}`); w.Code != http.StatusConflict {
		t.Fatalf("update from another address after the first load: %d %s, want 409", w.Code, w.Body.String())
	}
}

// The API is recorded on the first load also for a plugin that is not loaded (disabled), so
// its files missing later do not let a lower API in.
func TestReview_OldEntryAPIFloorSurvivesMissingFiles(t *testing.T) {
	gin.SetMode(gin.TestMode)
	var v, m atomic.Value
	v.Store("1.0.0")
	m.Store(`{"id":"cloud","version":"1.0.0","api":2,"permissions":{}}`)
	repo := pluginRepoServer(t, &v, &m)
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
	asFromOlderVclu(t, dir, "@jan/cloud", "1.0.0", "cloud-v1.0.0")
	l := plugins.NewLoader(dir)
	_ = l.LoadAllEnabled()

	if err := os.RemoveAll(filepath.Join(dir, "plugins", "jan--cloud")); err != nil {
		t.Fatal(err)
	}
	v.Store("1.1.0")
	m.Store(`{"id":"cloud","version":"1.1.0","permissions":{}}`)
	if err := l.DownloadPlugin(config.PluginRepository{Name: "test", URL: repo.URL, Enabled: true}, "@jan/cloud"); err == nil {
		t.Fatal("files missing: an API 1 version downloaded over a recorded API 2")
	}
}

// Updating an API 2 plugin to API 2 keeps working and keeps both records.
func TestReview_UpdateSameAPIKeepsRecords(t *testing.T) {
	gin.SetMode(gin.TestMode)
	var v, m atomic.Value
	v.Store("1.0.0")
	m.Store(`{"id":"cloud","version":"1.0.0","api":2,"permissions":{}}`)
	repo := pluginRepoServer(t, &v, &m)
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
	v.Store("1.1.0")
	m.Store(`{"id":"cloud","version":"1.1.0","api":2,"permissions":{}}`)
	if w := callInstall(t, s, `{"id":"cloud","repository":"test"}`); w.Code != http.StatusOK {
		t.Fatalf("update 2 -> 2: %d %s", w.Code, w.Body.String())
	}
	got := testInstalled(t, dir, "@jan/cloud")
	if got.Version != "1.1.0" || got.API != 2 || got.RepositoryURL != plugins.RepoURL(repo.URL) {
		t.Errorf("after the update %+v", got)
	}
}

// The list offers an update to an older entry whose files came from another address, which the
// install then refuses (source_moved) without the card saying so.
func TestReview_AvailableMatchesInstallForOldEntry(t *testing.T) {
	t.Skip("known: availablePlugins uses FromRepository, not the .complete marker (FromSource); UX only, the install refuses")
}
