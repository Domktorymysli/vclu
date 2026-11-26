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
	"github.com/Domktorymysli/vclu/pkg/plugins"
	"github.com/gin-gonic/gin"
)

func wizardServer(t *testing.T, grants *config.PluginGrants) (*Server, string) {
	t.Helper()
	dir := t.TempDir()
	pdir := filepath.Join(dir, "plugins", "jan--clu", "1.0.0")
	if err := os.MkdirAll(pdir, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{
		"plugin.json": `{"id": "clu", "version": "1.0.0", "api": 2, "permissions": {}, "form": "form.json"}`,
		"form.json": `{"settings": {"ip": {"type": "host", "required": true}, "token": {"type": "secret"}},
		  "devices": {"fields": {"id": {"type": "string", "readonly": true}, "ip": {"type": "host"}}}}`,
		"init.lua":  "return {}",
		".complete": "ok",
	} {
		if err := os.WriteFile(filepath.Join(pdir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	mc := config.VCLUConfig{Plugins: &config.VCLUPluginsConfig{Installed: []config.InstalledPlugin{{
		ID: "@jan/clu", Version: "1.0.0", Enabled: false, Grants: grants,
		Config: map[string]interface{}{"agentKey": "kept"},
	}}}}
	data, _ := json.Marshal(mc)
	if err := os.WriteFile(filepath.Join(dir, ".vclu.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}
	return &Server{serverDir: dir, pluginController: plugins.NewLoader(dir)}, dir
}

func wizardCall(t *testing.T, s *Server, method, body string, h func(*gin.Context)) (int, map[string]any) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Params = gin.Params{{Key: "id", Value: "@jan/clu"}}
	c.Request = httptest.NewRequest(method, "/", strings.NewReader(body))
	h(c)
	var out map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	return w.Code, out
}

func TestPluginsAPIFormSavesStepsApart(t *testing.T) {
	s, dir := wizardServer(t, &config.PluginGrants{LAN: &config.LANGrant{Hosts: []string{"10.0.0.1"}}})

	code, out := wizardCall(t, s, http.MethodPost, `{"settings": {"ip": "10.0.0.5", "token": "s3cret"}}`, s.handlePluginsAPISaveForm)
	if code != http.StatusOK {
		t.Fatalf("save settings: %d %v", code, out)
	}
	code, out = wizardCall(t, s, http.MethodPost, `{"devices": [{"id": "a", "ip": "10.0.0.6"}]}`, s.handlePluginsAPISaveForm)
	if code != http.StatusOK {
		t.Fatalf("save devices: %d %v", code, out)
	}

	entry, _ := installedEntry(dir, "@jan/clu")
	if entry.Config["ip"] != "10.0.0.5" || entry.Config["agentKey"] != "kept" || entry.Config["token"] != nil {
		t.Fatalf("config: %v", entry.Config)
	}
	if rows, _ := entry.Config["devices"].([]any); len(rows) != 1 {
		t.Fatalf("devices: %v", entry.Config["devices"])
	}
	// a narrowed lan grant gets the hosts of both steps
	if got := strings.Join(entry.Grants.LAN.Hosts, ","); got != "10.0.0.1,10.0.0.5,10.0.0.6" {
		t.Fatalf("lan hosts: %s", got)
	}
	secrets, _ := plugins.PluginSecrets(dir, "@jan/clu")
	if secrets["token"] != "s3cret" {
		t.Fatalf("secret: %v", secrets)
	}

	// the panel sees that a secret is set, never its value; saving settings again keeps it
	_, out = wizardCall(t, s, http.MethodGet, "", s.handlePluginsAPIForm)
	if raw, _ := json.Marshal(out); strings.Contains(string(raw), "s3cret") {
		t.Fatalf("the form shows a secret: %s", raw)
	}
	if out["secrets"].(map[string]any)["token"] != true {
		t.Fatalf("secrets: %v", out["secrets"])
	}
	wizardCall(t, s, http.MethodPost, `{"settings": {"ip": "10.0.0.5"}}`, s.handlePluginsAPISaveForm)
	if secrets, _ := plugins.PluginSecrets(dir, "@jan/clu"); secrets["token"] != "s3cret" {
		t.Fatalf("a secret left out was lost: %v", secrets)
	}
}

func TestPluginsAPIFormRefusesWhatTheFormDoesNotDeclare(t *testing.T) {
	s, _ := wizardServer(t, nil)
	for what, body := range map[string]string{
		"unknown setting": `{"settings": {"ip": "10.0.0.5", "shell": "x"}}`,
		"bad host":        `{"settings": {"ip": "example.com"}}`,
		"unknown key":     `{"settings": {"ip": "10.0.0.5"}, "grants": {}}`,
		"device field":    `{"devices": [{"id": "a", "cmd": "x"}]}`,
	} {
		if code, out := wizardCall(t, s, http.MethodPost, body, s.handlePluginsAPISaveForm); code != http.StatusBadRequest {
			t.Errorf("%s: %d %v", what, code, out)
		}
	}
	// no lan grant: hosts are not added to one
	wizardCall(t, s, http.MethodPost, `{"settings": {"ip": "10.0.0.5"}}`, s.handlePluginsAPISaveForm)
	entry, _ := installedEntry(s.serverDir, "@jan/clu")
	if entry.Grants != nil {
		t.Fatalf("grants: %+v", entry.Grants)
	}
}
