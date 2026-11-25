package web

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/Domktorymysli/vclu/pkg/config"
	"github.com/Domktorymysli/vclu/pkg/lua"
	"github.com/Domktorymysli/vclu/pkg/plugins"
	"github.com/gin-gonic/gin"
)

// A plugin that declares a dashboard and has no form: the wizard is its dashboards and code.
func dashWizardServer(t *testing.T) (*Server, string) {
	t.Helper()
	dir := t.TempDir()
	pdir := filepath.Join(dir, "plugins", "jan--clu", "1.0.0")
	if err := os.MkdirAll(pdir, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{
		"plugin.json": `{"id": "clu", "version": "1.0.0", "api": 2,
			"dashboards": [{"id": "lista", "name": {"pl": "Zakupy", "en": "Shopping"}, "description": "Lista"}]}`,
		"init.lua":  "return {}",
		".complete": "ok",
	} {
		if err := os.WriteFile(filepath.Join(pdir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	mc := config.VCLUConfig{Plugins: &config.VCLUPluginsConfig{Installed: []config.InstalledPlugin{{
		ID: "@jan/clu", Version: "1.0.0", Enabled: false, Config: map[string]interface{}{"agentKey": "kept"},
	}}}}
	data, _ := json.Marshal(mc)
	if err := os.WriteFile(filepath.Join(dir, ".vclu.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}
	return &Server{serverDir: dir, pluginController: plugins.NewLoader(dir)}, dir
}

func TestPluginsAPIFormDashboardsWithoutForm(t *testing.T) {
	s, dir := dashWizardServer(t)
	code, out := wizardCall(t, s, http.MethodGet, "", s.handlePluginsAPIForm)
	if code != http.StatusOK || out["form"] != nil {
		t.Fatalf("form: %d %v", code, out)
	}
	list, _ := out["dashboards"].([]any)
	if len(list) != 1 || list[0].(map[string]any)["path"] != "/d/clu_lista" {
		t.Fatalf("dashboards: %v", out["dashboards"])
	}
	access := out["dashboardAccess"].(map[string]any)["lista"].(map[string]any)
	if access["name"] != "Zakupy" || access["auth"] != "session" {
		t.Fatalf("defaults: %v", access)
	}
	if _, ok := out["roles"].([]any); !ok {
		t.Fatalf("roles: %v", out["roles"])
	}

	// field errors come back by field, nothing is saved
	code, out = wizardCall(t, s, http.MethodPost, `{"dashboards": {"lista": {"auth": "pin", "pin": "12"}}}`, s.handlePluginsAPISaveForm)
	if code != http.StatusBadRequest || out["fields"].(map[string]any)["lista.pin"] == nil {
		t.Fatalf("bad pin: %d %v", code, out)
	}
	for what, body := range map[string]string{
		"with settings": `{"dashboards": {"lista": {"auth": "session"}}, "settings": {}}`,
		"undeclared":    `{"dashboards": {"inna": {"auth": "session"}}}`,
		"unknown key":   `{"dashboards": {"lista": {"auth": "session", "lua": "x"}}}`,
	} {
		if code, out := wizardCall(t, s, http.MethodPost, body, s.handlePluginsAPISaveForm); code != http.StatusBadRequest {
			t.Errorf("%s: %d %v", what, code, out)
		}
	}
	// no form: settings are still refused
	if code, _ := wizardCall(t, s, http.MethodPost, `{"settings": {}}`, s.handlePluginsAPISaveForm); code != http.StatusUnprocessableEntity {
		t.Errorf("settings without a form: %d", code)
	}

	nasty := `a"]] ..os.exit()--`
	body, _ := json.Marshal(map[string]any{"dashboards": map[string]any{"lista": map[string]any{
		"name": nasty, "auth": "public", "remote": true, "remotePin": true, "pin": "4321", "roles": []string{"domownicy", "gosia"}}}})
	if code, out := wizardCall(t, s, http.MethodPost, string(body), s.handlePluginsAPISaveForm); code != http.StatusOK {
		t.Fatalf("save: %d %v", code, out)
	}
	entry, _ := installedEntry(dir, "@jan/clu")
	if entry.Config["agentKey"] != "kept" || entry.Config[plugins.DashboardsKey] == nil {
		t.Fatalf("config: %v", entry.Config)
	}

	// the code step: the generated block, quoted; written with its hash
	code, out = wizardCall(t, s, http.MethodGet, "", s.handlePluginsAPIModuleCode)
	if code != http.StatusOK {
		t.Fatalf("module code: %d %v", code, out)
	}
	proposed := out["proposed"].(string)
	want := `    clu:dashboardAccess("lista", {
        name = "a\"]] ..os.exit()--",
        auth = "public",
        pin = "4321",
        remote = { pin = true, roles = { "domownicy", "gosia" } },
    })`
	if !strings.Contains(proposed, want) || !strings.Contains(proposed, `local clu = Plugin.getPlugin("@jan/clu")`) {
		t.Fatalf("proposed:\n%s", proposed)
	}
	if code, out := wizardCall(t, s, http.MethodPost, `{"hash": "x"}`, s.handlePluginsAPIWriteModuleCode); code != http.StatusConflict {
		t.Fatalf("stale hash: %d %v", code, out)
	}
	if code, out := wizardCall(t, s, http.MethodPost, `{"hash": "`+out["hash"].(string)+`"}`, s.handlePluginsAPIWriteModuleCode); code != http.StatusOK {
		t.Fatalf("write: %d %v", code, out)
	}
	got, _ := plugins.ReadModuleCode(dir, "@jan/clu")
	if got != proposed {
		t.Fatalf("written:\n%s", got)
	}
	if init, _ := os.ReadFile(filepath.Join(dir, "modules", "plugins", "init.lua")); !strings.Contains(string(init), `require("plugins/jan__clu")`) {
		t.Fatalf("require: %s", init)
	}

	// the form shows the saved step again, not as a setting
	_, out = wizardCall(t, s, http.MethodGet, "", s.handlePluginsAPIForm)
	if a := out["dashboardAccess"].(map[string]any)["lista"].(map[string]any); a["name"] != nasty || a["pin"] != "4321" {
		t.Fatalf("saved step: %v", a)
	}
}

// A plugin with a form keeps its settings step; the saved dashboards step is not a setting.
func TestPluginsAPIFormDashboardsNextToForm(t *testing.T) {
	s, dir := wizardServer(t, nil)
	pj := filepath.Join(dir, "plugins", "jan--clu", "1.0.0", "plugin.json")
	_ = os.WriteFile(pj, []byte(`{"id": "clu", "version": "1.0.0", "api": 2, "form": "form.json",
		"dashboards": [{"id": "lista", "name": "Lista"}]}`), 0o644)
	if code, out := wizardCall(t, s, http.MethodPost, `{"dashboards": {"lista": {"auth": "off"}}}`, s.handlePluginsAPISaveForm); code != http.StatusOK {
		t.Fatalf("save: %d %v", code, out)
	}
	if code, out := wizardCall(t, s, http.MethodPost, `{"settings": {"ip": "10.0.0.5"}}`, s.handlePluginsAPISaveForm); code != http.StatusOK {
		t.Fatalf("settings: %d %v", code, out)
	}
	entry, _ := installedEntry(dir, "@jan/clu")
	if entry.Config[plugins.DashboardsKey] == nil {
		t.Fatalf("saving settings dropped the dashboards step: %v", entry.Config)
	}
	_, out := wizardCall(t, s, http.MethodGet, "", s.handlePluginsAPIForm)
	if _, leaked := out["settings"].(map[string]any)[plugins.DashboardsKey]; leaked || out["dashboards"] == nil {
		t.Fatalf("form: %v", out)
	}
}

// The page and the API of a plugin's dashboard follow what a module granted.
func TestPluginDashboardAccessServed(t *testing.T) {
	dashCache.Invalidate()
	engine := lua.NewLuaEngine()
	t.Cleanup(engine.Close)
	if _, err := engine.Execute(`PluginSandbox:create("@jan/zakupy", {dashboards = {lista = {name = "Zakupy"}}})
		local _, err = PluginSandbox:execute("@jan/zakupy", [==[
			Plugin:new("zakupy", {}):dashboard("lista"):section("L"):button("b", "B", "x", function() end):build()
		]==])
		assert(err == nil, err)`); err != nil {
		t.Fatal(err)
	}
	s := NewServer(0, "../../web", t.TempDir(), &config.CLUDeviceConfig{}, &config.CLUKeys{}, engine)
	t.Cleanup(func() {
		if s.live != nil {
			s.live.shutdown()
		}
	})
	gin.SetMode(gin.TestMode)
	newAuthWithAPIKey(t, s)
	page := func() int {
		code, _ := actionsReq(t, s.router, http.MethodGet, "/d/zakupy_lista", "", "")
		return code
	}
	if code := page(); code != http.StatusFound {
		t.Fatalf("the plugin's session: %d", code)
	}
	grant := func(opts string) {
		t.Helper()
		if _, err := engine.Execute(`Plugin.getPlugin("@jan/zakupy"):dashboardAccess("lista", ` + opts + `)`); err != nil {
			t.Fatal(err)
		}
		dashCache.Invalidate()
		s.remoteRolesMu.Lock()
		s.remoteRolesCache = nil
		s.remoteRolesMu.Unlock()
	}
	grant(`{ auth = "public", remote = { roles = { "domownicy", "gosia" } } }`)
	if code := page(); code != http.StatusOK {
		t.Fatalf("public: %d", code)
	}
	if code, _ := actionsReq(t, s.router, http.MethodGet, "/api/dashboard/zakupy_lista/values", "", ""); code != http.StatusOK {
		t.Fatalf("public, values: %d", code)
	}
	if roles := s.knownRoles(); !slices.Contains(roles, "gosia") || !slices.Contains(roles, "domownicy") {
		t.Fatalf("known roles: %v", roles)
	}
	grant(`{ auth = "off" }`)
	if code := page(); code != http.StatusNotFound {
		t.Fatalf("off: %d", code)
	}
	if code, _ := actionsReq(t, s.router, http.MethodGet, "/api/dashboard/zakupy_lista/values", "", ""); code != http.StatusNotFound {
		t.Fatalf("off, values: %d", code)
	}
	if roles := s.knownRoles(); slices.Contains(roles, "gosia") {
		t.Fatalf("off is not remote: %v", roles)
	}
}
