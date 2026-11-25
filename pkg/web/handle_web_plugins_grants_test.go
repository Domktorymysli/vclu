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

// A plugin that asks for a serial port gets nothing of it on install: it waits for the user.
func TestPluginsAPIInstallLeavesSerialPending(t *testing.T) {
	repo := manifestRepo(t, "rs", "1.0.0",
		`{"id":"rs","version":"1.0.0","api":2,"permissions":{"lan":true,"serial":true},"files":["init.lua"]}`)
	dir := t.TempDir()
	if err := writeTestConfig(dir, &config.VCLUConfig{Plugins: &config.VCLUPluginsConfig{
		Repositories: []config.PluginRepository{{Name: "test", URL: repo.URL, Enabled: true}},
	}}); err != nil {
		t.Fatal(err)
	}
	s := &Server{serverDir: dir}
	postInstall(t, s, "rs")

	got := installedPlugins(t, s)
	if len(got) != 1 || !lanOnly(got[0].Grants) || got[0].Grants.Serial != nil ||
		strings.Join(got[0].PendingPermissions, ",") != "serial" {
		t.Errorf("installed: %+v, want lan allowed and serial pending", got)
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
	funcs := templateFuncs()
	funcs["t"] = func(lang, key string) string { return key }
	tmpl := template.New("").Funcs(funcs)
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

// updateFrom installs plugin shelly from the "test" repository over the given entry.
func updateFrom(t *testing.T, manifest string, entry config.InstalledPlugin) config.InstalledPlugin {
	t.Helper()
	repo := manifestRepo(t, "shelly", "1.1.0", manifest)
	dir := t.TempDir()
	if err := writeTestConfig(dir, &config.VCLUConfig{Plugins: &config.VCLUPluginsConfig{
		Repositories: []config.PluginRepository{{Name: "test", URL: repo.URL, Enabled: true}},
		Installed:    []config.InstalledPlugin{entry},
	}}); err != nil {
		t.Fatal(err)
	}
	if resp := postInstall(t, &Server{serverDir: dir}, "shelly"); resp.Action != "updated" {
		t.Fatalf("answer %+v", resp)
	}
	mc, err := config.ReadMasterConfig(dir)
	if err != nil {
		t.Fatal(err)
	}
	return mc.Plugins.Installed[0]
}

// Without the old files nothing says what the user allowed: everything waits for them.
func TestPluginsAPIUpdateWithoutOldFilesWaits(t *testing.T) {
	got := updateFrom(t, `{"id":"shelly","version":"1.1.0","api":2,"permissions":{"lan":true},"files":["init.lua"]}`,
		config.InstalledPlugin{ID: "@jan/shelly", Version: "1.0.0", Enabled: true, Repository: "test"})
	if g := got.Grants; g == nil || g.LAN != nil || g.MQTT != nil || g.Modbus != nil {
		t.Errorf("grants without the old files: %+v, want none", g)
	}
}

// An unknown key is refused, not read as a grant without its list.
func TestPluginsAPIPermissionsRefusesUnknownKeys(t *testing.T) {
	dir := t.TempDir()
	if err := writeTestConfig(dir, &config.VCLUConfig{Plugins: &config.VCLUPluginsConfig{
		Installed: []config.InstalledPlugin{{ID: "@jan/shelly", Version: "1.0.0", Enabled: true}},
	}}); err != nil {
		t.Fatal(err)
	}
	for _, body := range []string{`{"lan":{"host":["192.168.1.5"]}}`, `{"lna":{}}`} {
		gin.SetMode(gin.TestMode)
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Params = gin.Params{{Key: "id", Value: "@jan/shelly"}}
		c.Request = httptest.NewRequest(http.MethodPost, "/api/plugins/@jan/shelly/permissions", strings.NewReader(body))
		c.Request.Header.Set("Content-Type", "application/json")
		(&Server{serverDir: dir}).handlePluginsAPIPermissions(c)
		if w.Code != http.StatusBadRequest {
			t.Errorf("%s: status %d", body, w.Code)
		}
	}
}

// The panel shows what a plugin asks for before installing it, next to every permission.
func TestPluginsAPIPreviewReadsTheManifest(t *testing.T) {
	repo := manifestRepo(t, "rs", "1.2.0",
		`{"id":"rs","version":"1.2.0","api":2,"permissions":{"internet":["api.example.com"],"lan":true,"serial":true},"files":["init.lua"]}`)
	dir := t.TempDir()
	if err := writeTestConfig(dir, &config.VCLUConfig{Plugins: &config.VCLUPluginsConfig{
		Repositories: []config.PluginRepository{{Name: "test", URL: repo.URL, Enabled: true}},
	}}); err != nil {
		t.Fatal(err)
	}
	s := &Server{serverDir: dir}
	preview := func(id, repository string) (int, map[string]any) {
		gin.SetMode(gin.TestMode)
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest(http.MethodGet, "/api/plugins/preview?id="+id+"&repository="+repository, nil)
		s.handlePluginsAPIPreview(c)
		var out map[string]any
		_ = json.Unmarshal(w.Body.Bytes(), &out)
		return w.Code, out
	}

	code, out := preview("rs", "test")
	if code != http.StatusOK || out["version"] != "1.2.0" {
		t.Fatalf("preview: %d %v", code, out)
	}
	join := func(v any) string {
		var parts []string
		for _, x := range v.([]any) {
			parts = append(parts, x.(string))
		}
		return strings.Join(parts, ",")
	}
	if got := join(out["requested"]); got != "internet,lan,serial" {
		t.Errorf("requested %q, want internet,lan,serial", got)
	}
	if got := join(out["internet"]); got != "api.example.com" {
		t.Errorf("internet %q", got)
	}
	if got := join(out["all"]); got != "internet,lan,mqtt,modbus,grenton_raw,tftp,serial" {
		t.Errorf("all %q", got)
	}
	if code, _ := preview("rs", "nope"); code != http.StatusNotFound {
		t.Errorf("unknown repository: %d, want 404", code)
	}
	if code, _ := preview("other", "test"); code != http.StatusBadGateway {
		t.Errorf("unknown plugin: %d, want 502", code)
	}
	if _, err := os.Stat(filepath.Join(dir, "plugins")); err == nil {
		if entries, _ := os.ReadDir(filepath.Join(dir, "plugins")); len(entries) > 0 {
			t.Errorf("preview wrote files: %v", entries)
		}
	}
}

// A new plugin gets only what the user confirmed; the rest waits as after an update.
func TestPluginsAPIInstallGrantsOnlyWhatWasAccepted(t *testing.T) {
	repo := manifestRepo(t, "shelly", "1.0.0",
		`{"id":"shelly","version":"1.0.0","api":2,"permissions":{"lan":true,"mqtt":true},"files":["init.lua"]}`)
	dir := t.TempDir()
	if err := writeTestConfig(dir, &config.VCLUConfig{Plugins: &config.VCLUPluginsConfig{
		Repositories: []config.PluginRepository{{Name: "test", URL: repo.URL, Enabled: true}},
	}}); err != nil {
		t.Fatal(err)
	}
	s := &Server{serverDir: dir}
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	// confirmed lan only (the manifest changed since the preview, or the user unticked mqtt)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/plugins/install",
		strings.NewReader(`{"id":"shelly","repository":"test","accept":["lan"]}`))
	c.Request.Header.Set("Content-Type", "application/json")
	s.handlePluginsAPIInstall(c)
	if w.Code != http.StatusOK {
		t.Fatalf("install: %d %s", w.Code, w.Body.String())
	}

	got := installedPlugins(t, s)
	if len(got) != 1 || !lanOnly(got[0].Grants) || strings.Join(got[0].PendingPermissions, ",") != "mqtt" {
		t.Errorf("installed: %+v, want lan allowed and mqtt pending", got)
	}
}

func installedInternet(t *testing.T, s *Server) (internet, pending []string, grants *config.PluginGrants) {
	t.Helper()
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/plugins/installed", nil)
	s.handlePluginsAPIInstalled(c)
	var out struct {
		Installed []struct {
			Internet, PendingInternet []string
			Grants                    *config.PluginGrants
		}
	}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil || len(out.Installed) != 1 {
		t.Fatalf("installed %s: %v", w.Body.String(), err)
	}
	p := out.Installed[0]
	return p.Internet, p.PendingInternet, p.Grants
}

// Installing allows the hosts asked for if the user accepted "internet"; an update adding a
// host waits for the user, the hosts allowed before keep working.
func TestPluginsAPIInternetHostsInstallAndUpdate(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, c := range []struct {
		accept, wantGrant string
	}{
		{`["internet","lan"]`, "a.example.com"},
		{`["lan"]`, ""},
	} {
		repo := manifestRepo(t, "cloud", "1.0.0",
			`{"id":"cloud","version":"1.0.0","api":2,"permissions":{"internet":["a.example.com"],"lan":true},"files":["init.lua"]}`)
		dir := t.TempDir()
		if err := writeTestConfig(dir, &config.VCLUConfig{Plugins: &config.VCLUPluginsConfig{
			Repositories: []config.PluginRepository{{Name: "test", URL: repo.URL, Enabled: true}},
		}}); err != nil {
			t.Fatal(err)
		}
		s := &Server{serverDir: dir}
		w := httptest.NewRecorder()
		ctx, _ := gin.CreateTestContext(w)
		ctx.Request = httptest.NewRequest(http.MethodPost, "/api/plugins/install",
			strings.NewReader(`{"id":"cloud","repository":"test","accept":`+c.accept+`}`))
		ctx.Request.Header.Set("Content-Type", "application/json")
		s.handlePluginsAPIInstall(ctx)
		if w.Code != http.StatusOK {
			t.Fatalf("install: %d %s", w.Code, w.Body.String())
		}
		internet, pending, grants := installedInternet(t, s)
		var granted string
		if grants.Internet != nil {
			granted = strings.Join(grants.Internet.Hosts, ",")
		}
		if strings.Join(internet, ",") != "a.example.com" || granted != c.wantGrant ||
			(c.wantGrant == "") != (strings.Join(pending, ",") == "a.example.com") {
			t.Errorf("accept %s: internet %v pending %v grant %q", c.accept, internet, pending, granted)
		}
	}

	// the update asks for another host and then for any
	for _, asks := range []string{`["a.example.com","b.example.com"]`, `"*"`} {
		repo := manifestRepo(t, "cloud", "1.1.0",
			`{"id":"cloud","version":"1.1.0","api":2,"permissions":{"internet":`+asks+`},"files":["init.lua"]}`)
		dir := t.TempDir()
		if err := writeTestConfig(dir, &config.VCLUConfig{Plugins: &config.VCLUPluginsConfig{
			Repositories: []config.PluginRepository{{Name: "test", URL: repo.URL, Enabled: true}},
			Installed: []config.InstalledPlugin{{ID: "@jan/cloud", Version: "1.0.0", Enabled: true, Repository: "test",
				Grants: &config.PluginGrants{Internet: &config.InternetGrant{Hosts: []string{"a.example.com"}}}}},
		}}); err != nil {
			t.Fatal(err)
		}
		s := &Server{serverDir: dir}
		if resp := postInstall(t, s, "cloud"); resp.Action != "updated" {
			t.Fatalf("answer %+v", resp)
		}
		_, pending, grants := installedInternet(t, s)
		wantPending := map[bool]string{true: "*", false: "b.example.com"}[asks == `"*"`]
		if grants.Internet == nil || strings.Join(grants.Internet.Hosts, ",") != "a.example.com" || strings.Join(pending, ",") != wantPending {
			t.Errorf("update to %s: grant %+v pending %v, want %s", asks, grants.Internet, pending, wantPending)
		}

		// the user allows it
		w := httptest.NewRecorder()
		ctx, _ := gin.CreateTestContext(w)
		ctx.Params = gin.Params{{Key: "id", Value: "@jan/cloud"}}
		ctx.Request = httptest.NewRequest(http.MethodPost, "/api/plugins/@jan/cloud/permissions",
			strings.NewReader(`{"internet":{"hosts":`+strings.Replace(asks, `"*"`, `["*"]`, 1)+`}}`))
		ctx.Request.Header.Set("Content-Type", "application/json")
		s.handlePluginsAPIPermissions(ctx)
		if w.Code != http.StatusOK {
			t.Fatalf("permissions: %d %s", w.Code, w.Body.String())
		}
		if _, pending, _ := installedInternet(t, s); len(pending) > 0 {
			t.Errorf("after allowing %s: pending %v", asks, pending)
		}
	}

	// a bad host in a grant is refused
	w := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(w)
	ctx.Params = gin.Params{{Key: "id", Value: "@jan/cloud"}}
	ctx.Request = httptest.NewRequest(http.MethodPost, "/api/plugins/@jan/cloud/permissions",
		strings.NewReader(`{"internet":{"hosts":["https://a.example.com/"]}}`))
	ctx.Request.Header.Set("Content-Type", "application/json")
	(&Server{serverDir: t.TempDir()}).handlePluginsAPIPermissions(ctx)
	if w.Code != http.StatusBadRequest {
		t.Errorf("bad host: %d %s", w.Code, w.Body.String())
	}
}
