package web

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/Domktorymysli/vclu/pkg/config"
	"github.com/Domktorymysli/vclu/pkg/notify"
	"github.com/gin-gonic/gin"
)

func weatherUpdate() []availablePlugin {
	return []availablePlugin{{Meta: PluginMetadata{Name: "Weather", Version: "1.10.0"}, FullID: "@vclu/weather",
		Installed: true, InstalledVersion: "1.2.0", UpdateAvailable: true}}
}

// A .vclu.json that does not parse changes nothing in the notice; with no plugins installed it goes.
func TestCheckPluginUpdatesConfig(t *testing.T) {
	e, n := newNotifyEnv(t)
	if err := writeTestConfig(e.dir, &config.VCLUConfig{Plugins: &config.VCLUPluginsConfig{Installed: []config.InstalledPlugin{
		{ID: "@vclu/weather", Version: "1.2.0"}}}}); err != nil {
		t.Fatal(err)
	}
	e.s.pluginNotices(weatherUpdate())
	if err := os.WriteFile(filepath.Join(e.dir, ".vclu.json"), []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	e.s.checkPluginUpdates(context.Background())
	e.s.resolvePluginNotices()
	if list, _, _ := n.List(); len(list) != 1 || list[0].Key != pluginUpdatesKey {
		t.Fatalf("broken config: %+v", list)
	}
	if err := os.Remove(filepath.Join(e.dir, ".vclu.json")); err != nil {
		t.Fatal(err)
	}
	if err := writeTestConfig(e.dir, &config.VCLUConfig{}); err != nil {
		t.Fatal(err)
	}
	e.s.checkPluginUpdates(context.Background())
	if list, _, _ := n.List(); len(list) != 0 {
		t.Fatalf("nothing installed, still %+v", list)
	}
}

// A plugin in two registries is in the notice once, with the newer version; one without a
// name is named by its id; equal names are ordered by id.
func TestPluginNoticesMerge(t *testing.T) {
	e, n := newNotifyEnv(t)
	if err := writeTestConfig(e.dir, &config.VCLUConfig{Plugins: &config.VCLUPluginsConfig{Installed: []config.InstalledPlugin{
		{ID: "@vclu/a", Version: "1.0.0"}, {ID: "@vclu/s1", Version: "1.0.0"}, {ID: "@vclu/s2", Version: "1.0.0"}}}}); err != nil {
		t.Fatal(err)
	}
	up := func(id, name, v string) availablePlugin {
		return availablePlugin{Meta: PluginMetadata{Name: name, Version: v}, FullID: id, Installed: true, InstalledVersion: "1.0.0", UpdateAvailable: true}
	}
	e.s.pluginNotices([]availablePlugin{up("@vclu/s2", "Same", "1.2.0"), up("@vclu/a", "", "1.5.0"), up("@vclu/a", "", "1.3.0"), up("@vclu/s1", "Same", "1.1.0")})
	list, _, _ := n.List()
	if len(list) != 1 || list[0].Detail != "@vclu/a 1.5.0, Same 1.1.0, Same 1.2.0" || list[0].Params["count"] != "3" {
		t.Fatalf("%+v", list)
	}
	var ids []string
	for _, u := range e.s.pendingPluginUpdates() {
		ids = append(ids, u.ID)
	}
	if got := strings.Join(ids, " "); got != "@vclu/a @vclu/s1 @vclu/s2" {
		t.Errorf("order %s", got)
	}
}

// A lock without params or wait has no end time.
func TestNotifyLockNoParams(t *testing.T) {
	e, n := newNotifyEnv(t)
	e.s.notifyLock(notify.MsgLockPIN, "pin:x", nil, 0)
	list, _, _ := n.List()
	if len(list) != 1 || list[0].Key != "lock:pin:x" || list[0].Link != "/settings" || list[0].Params["until"] != "" {
		t.Fatalf("%+v", list)
	}
}

// The available list marks what is installed and newer by version, not by text.
func TestPluginsAPIAvailable(t *testing.T) {
	repo := pluginRepo(t, "weather", "1.10.0", false)
	dir := t.TempDir()
	if err := writeTestConfig(dir, &config.VCLUConfig{Plugins: &config.VCLUPluginsConfig{
		Repositories: []config.PluginRepository{{Name: "test", URL: repo.URL, Enabled: true}, {Name: "off", URL: "http://127.0.0.1:1", Enabled: false}},
		Installed:    []config.InstalledPlugin{{ID: "@vclu/weather", Version: "1.9.0", Repository: "test"}}}}); err != nil {
		t.Fatal(err)
	}
	s := &Server{serverDir: dir}
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/plugins/available", nil)
	s.handlePluginsAPIAvailable(c)
	var resp struct {
		Plugins []map[string]any `json:"plugins"`
		Errors  []string         `json:"errors"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil || w.Code != http.StatusOK {
		t.Fatalf("%d %s: %v", w.Code, w.Body, err)
	}
	if len(resp.Plugins) != 1 || len(resp.Errors) != 0 {
		t.Fatalf("%+v", resp)
	}
	p := resp.Plugins[0]
	if p["fullId"] != "@vclu/weather" || p["namespace"] != "vclu" || p["version"] != "1.10.0" || p["repository"] != "test" ||
		p["installed"] != true || p["installedVersion"] != "1.9.0" || p["updateAvailable"] != true || p["repoUrl"] == "" {
		t.Errorf("%+v", p)
	}
}

// Versions that do not compare are newer when they differ.
func TestNewerVersion(t *testing.T) {
	for _, c := range []struct {
		v, installed string
		want         bool
	}{{"1.10.0", "1.9.0", true}, {"1.9.0", "1.10.0", false}, {"latest", "1.0.0", true}, {"latest", "latest", false}} {
		if got := newerVersion(c.v, c.installed); got != c.want {
			t.Errorf("newerVersion(%q, %q) = %v", c.v, c.installed, got)
		}
	}
}

// Once the check's time is up the registries left are not asked, each one an error.
func TestAvailablePluginsCancelled(t *testing.T) {
	var requests atomic.Int32
	reg := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { requests.Add(1) }))
	defer reg.Close()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	found, errs := availablePlugins(ctx, &config.VCLUConfig{Plugins: &config.VCLUPluginsConfig{
		Repositories: []config.PluginRepository{{Name: "a", URL: reg.URL, Enabled: true}, {Name: "b", URL: reg.URL, Enabled: true}}}}, "")
	if len(found) != 0 || strings.Join(errs, ";") != "a: context canceled;b: context canceled" || requests.Load() != 0 {
		t.Errorf("found %+v, errors %v, %d requests", found, errs, requests.Load())
	}
}

// An uninstalled plugin leaves the notice.
func TestPluginsAPIUninstallResolvesNotice(t *testing.T) {
	e, n := newNotifyEnv(t)
	if err := writeTestConfig(e.dir, &config.VCLUConfig{Plugins: &config.VCLUPluginsConfig{Installed: []config.InstalledPlugin{
		{ID: "@vclu/weather", Version: "1.2.0"}}}}); err != nil {
		t.Fatal(err)
	}
	e.s.pluginNotices(weatherUpdate())
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodDelete, "/api/plugins/@vclu/weather", nil)
	c.Params = gin.Params{{Key: "id", Value: "@vclu/weather"}}
	e.s.handlePluginsAPIUninstall(c)
	if w.Code != http.StatusOK {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	if list, _, _ := n.List(); len(list) != 0 {
		t.Errorf("uninstalled, still %+v", list)
	}
}

// A repository URL that is not one fails before any request.
func TestFetchRegistryBadURL(t *testing.T) {
	if _, err := fetchRegistryFromRepo(config.PluginRepository{URL: "http://\x7f"}); err == nil || !strings.Contains(err.Error(), "failed to fetch") {
		t.Errorf("err %v", err)
	}
}

// Two repositories list the same id: only a newer version in the one the plugin came from is
// an update, in the list and in the bell.
func TestAvailablePluginsUpdateFromSourceOnly(t *testing.T) {
	e, n := newNotifyEnv(t)
	source := manifestRepo(t, "shelly", "1.1.0", `{}`)
	other := manifestRepo(t, "shelly", "2.0.0", `{}`)
	if err := writeTestConfig(e.dir, &config.VCLUConfig{Plugins: &config.VCLUPluginsConfig{
		Repositories: []config.PluginRepository{{Name: "other", URL: other.URL, Enabled: true}, {Name: "source", URL: source.URL, Enabled: true}},
		Installed:    []config.InstalledPlugin{{ID: "@jan/shelly", Version: "1.0.0", Repository: "source"}}}}); err != nil {
		t.Fatal(err)
	}
	mc, err := config.ReadMasterConfig(e.dir)
	if err != nil {
		t.Fatal(err)
	}
	found, errs := availablePlugins(context.Background(), mc, "")
	if len(found) != 2 || len(errs) != 0 {
		t.Fatalf("found %+v, errors %v", found, errs)
	}
	for _, a := range found {
		want := a.Repo.Name == "source"
		if a.FullID != "@jan/shelly" || !a.Installed || a.InstalledVersion != "1.0.0" || a.UpdateAvailable != want {
			t.Errorf("%s from %s: installed %v %s, update %v, want %v", a.FullID, a.Repo.Name, a.Installed, a.InstalledVersion, a.UpdateAvailable, want)
		}
	}
	e.s.checkPluginUpdates(context.Background())
	list, _, _ := n.List()
	if len(list) != 1 || list[0].Key != pluginUpdatesKey || !strings.Contains(list[0].Detail, "1.1.0") || strings.Contains(list[0].Detail, "2.0.0") {
		t.Errorf("bell: %+v", list)
	}
}

// pluginFiles puts a version of a plugin on disk with the given plugin.json.
func pluginFiles(t *testing.T, dir, id, version, manifest string) {
	t.Helper()
	d := filepath.Join(dir, "plugins", strings.Replace(strings.TrimPrefix(id, "@"), "/", "--", 1), version)
	if err := os.MkdirAll(d, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(d, "plugin.json"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
}

// An update asking for more puts "waiting for permissions" in the bell; allowing it on the
// plugins page resolves it.
func TestPermissionNoticeAfterUpdate(t *testing.T) {
	e, n := newNotifyEnv(t)
	repo := manifestRepo(t, "shelly", "1.1.0",
		`{"id":"shelly","version":"1.1.0","api":2,"permissions":{"lan":true,"mqtt":true},"files":["init.lua"]}`)
	pluginFiles(t, e.dir, "@jan/shelly", "1.0.0", `{"id":"shelly","version":"1.0.0","api":2,"permissions":{"lan":true}}`)
	if err := writeTestConfig(e.dir, &config.VCLUConfig{Plugins: &config.VCLUPluginsConfig{
		Repositories: []config.PluginRepository{{Name: "test", URL: repo.URL, Enabled: true}},
		Installed:    []config.InstalledPlugin{{ID: "@jan/shelly", Version: "1.0.0", Enabled: true, Repository: "test"}},
	}}); err != nil {
		t.Fatal(err)
	}
	e.s.resolvePluginNotices()
	if list, _, _ := n.List(); len(list) != 0 {
		t.Fatalf("before the update: %+v", list)
	}
	if resp := postInstall(t, e.s, "shelly"); resp.Action != "updated" {
		t.Fatalf("answer %+v", resp)
	}
	list, _, _ := n.List()
	if len(list) != 1 || list[0].Key != pluginPermissionsKey || list[0].Msg != notify.MsgPluginPerms || list[0].Link != "/plugins" ||
		list[0].Level != notify.LevelInfo || list[0].Params["count"] != "1" || list[0].Params["plugins"] != "@jan/shelly" {
		t.Fatalf("after the update: %+v", list)
	}

	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Params = gin.Params{{Key: "id", Value: "@jan/shelly"}}
	c.Request = httptest.NewRequest(http.MethodPost, "/api/plugins/@jan/shelly/permissions", strings.NewReader(`{"lan":{},"mqtt":{}}`))
	c.Request.Header.Set("Content-Type", "application/json")
	e.s.handlePluginsAPIPermissions(c)
	if w.Code != http.StatusOK {
		t.Fatalf("permissions: %d %s", w.Code, w.Body.String())
	}
	if list, _, _ := n.List(); len(list) != 0 {
		t.Errorf("allowed, still %+v", list)
	}
}

// The notice counts enabled plugins only and names three of them.
func TestPermissionNoticeLists(t *testing.T) {
	e, n := newNotifyEnv(t)
	var installed []config.InstalledPlugin
	for i, id := range []string{"@jan/e", "@jan/d", "@jan/c", "@jan/b", "@jan/a"} {
		pluginFiles(t, e.dir, id, "1.0.0", `{"version":"1.0.0","api":2,"permissions":{"mqtt":true}}`)
		installed = append(installed, config.InstalledPlugin{ID: id, Version: "1.0.0", Enabled: i > 0, Grants: &config.PluginGrants{}})
	}
	if err := writeTestConfig(e.dir, &config.VCLUConfig{Plugins: &config.VCLUPluginsConfig{Installed: installed}}); err != nil {
		t.Fatal(err)
	}
	e.s.PluginsReloaded(nil)
	list, _, _ := n.List()
	if len(list) != 1 || list[0].Params["count"] != "4" || list[0].Params["plugins"] != "@jan/a, @jan/b, @jan/c +1" ||
		list[0].Title != "Plugins waiting for permissions: 4 (@jan/a, @jan/b, @jan/c +1)" {
		t.Fatalf("%+v", list)
	}
	// a broken .vclu.json changes nothing
	if err := os.WriteFile(filepath.Join(e.dir, ".vclu.json"), []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	e.s.resolvePluginNotices()
	if list, _, _ := n.List(); len(list) != 1 {
		t.Fatalf("broken config: %+v", list)
	}
	if err := os.Remove(filepath.Join(e.dir, ".vclu.json")); err != nil {
		t.Fatal(err)
	}
	if err := writeTestConfig(e.dir, &config.VCLUConfig{Plugins: &config.VCLUPluginsConfig{}}); err != nil {
		t.Fatal(err)
	}
	e.s.resolvePluginNotices()
	if list, _, _ := n.List(); len(list) != 0 {
		t.Errorf("nothing installed, still %+v", list)
	}
}
