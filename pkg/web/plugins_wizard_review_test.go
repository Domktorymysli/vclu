package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/Domktorymysli/vclu/pkg/config"
	"github.com/Domktorymysli/vclu/pkg/plugins"
	"github.com/gin-gonic/gin"
)

// The install grants only the hosts and ports the confirmation showed: plugin.json read for
// the download may differ from the one read for the preview.
func TestPluginsAPIInstallGrantsOnlyWhatWasSeen(t *testing.T) {
	var reads atomic.Int32
	shown := `{"id":"cloud","version":"1.0.0","api":2,"permissions":{"internet":["a.example.com"],"lan":true,"tcp":true},"tcp":{"ports":[23]},"files":["init.lua"]}`
	other := `{"id":"cloud","version":"1.0.0","api":2,"permissions":{"internet":"*","lan":true,"tcp":true},"tcp":{"ports":[22,23]},"files":["init.lua"]}`
	repo := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/plugins.json":
			_, _ = w.Write([]byte(`{"namespace":"jan","plugins":[{"id":"cloud","version":"1.0.0","path":"plugins/cloud","tag":"cloud-v1.0.0"}]}`))
		case "/plugins/cloud/plugin.json":
			if reads.Add(1) == 1 {
				_, _ = w.Write([]byte(shown))
			} else {
				_, _ = w.Write([]byte(other))
			}
		case "/plugins/cloud/init.lua":
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
	gin.SetMode(gin.TestMode)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/plugins/preview?id=cloud&repository=test", nil)
	s.handlePluginsAPIPreview(c)
	var preview struct {
		Requested, Internet []string
		TCPPorts            []int
	}
	if err := json.Unmarshal(w.Body.Bytes(), &preview); err != nil || strings.Join(preview.Internet, ",") != "a.example.com" {
		t.Fatalf("preview %s", w.Body.String())
	}

	// as the panel sends it
	body, _ := json.Marshal(map[string]any{"id": "cloud", "repository": "test", "accept": preview.Requested,
		"seen": map[string]any{"internet": preview.Internet, "tcpPorts": preview.TCPPorts, "broadcastPorts": []int{}}})
	w = httptest.NewRecorder()
	c, _ = gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/plugins/install", strings.NewReader(string(body)))
	c.Request.Header.Set("Content-Type", "application/json")
	s.handlePluginsAPIInstall(c)
	if w.Code != http.StatusOK {
		t.Fatalf("install: %d %s", w.Code, w.Body.String())
	}
	_, pending, grants := installedInternet(t, s)
	if grants.Internet != nil && len(grants.Internet.Hosts) > 0 {
		t.Errorf("internet granted %v, the user saw a.example.com", grants.Internet.Hosts)
	}
	if strings.Join(pending, ",") != "*" {
		t.Errorf("pending internet %v, want *", pending)
	}
	if grants.TCP != nil {
		t.Errorf("tcp granted with ports the user did not see")
	}
	if grants.LAN == nil {
		t.Errorf("lan, shown and accepted, not granted")
	}
}

// A module edited after the diff was shown is not written over.
func TestPluginsAPIModuleCodeKeepsAnEditAfterTheDiff(t *testing.T) {
	dir := t.TempDir()
	pdir := filepath.Join(dir, "plugins", "jan--zakupy", "1.0.0")
	if err := os.MkdirAll(pdir, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{
		"plugin.json": `{"id": "zakupy", "version": "1.0.0", "api": 2, "dashboards": [{"id": "lista", "name": "Zakupy"}]}`,
		"init.lua":    "return {}",
		".complete":   "ok",
	} {
		if err := os.WriteFile(filepath.Join(pdir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := writeTestConfig(dir, &config.VCLUConfig{Plugins: &config.VCLUPluginsConfig{Installed: []config.InstalledPlugin{{
		ID: "@jan/zakupy", Version: "1.0.0", Enabled: false, Config: map[string]interface{}{},
	}}}}); err != nil {
		t.Fatal(err)
	}
	s := &Server{serverDir: dir, pluginController: plugins.NewLoader(dir)}
	call := func(method, body string, h func(*gin.Context)) (int, map[string]any) {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Params = gin.Params{{Key: "id", Value: "@jan/zakupy"}}
		c.Request = httptest.NewRequest(method, "/", strings.NewReader(body))
		c.Request.Header.Set("Content-Type", "application/json")
		h(c)
		var out map[string]any
		_ = json.Unmarshal(w.Body.Bytes(), &out)
		return w.Code, out
	}
	gin.SetMode(gin.TestMode)

	code, shown := call(http.MethodGet, "", s.handlePluginsAPIModuleCode)
	if code != http.StatusOK || !strings.Contains(shown["proposed"].(string), "dashboardAccess") {
		t.Fatalf("module-code: %d %v", code, shown)
	}
	path := plugins.ModulePath(dir, "@jan/zakupy")
	_ = os.MkdirAll(filepath.Dir(path), 0o755)
	if err := os.WriteFile(path, []byte("-- edited by hand\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	hash, _ := json.Marshal(map[string]any{"hash": shown["hash"]})
	if code, out := call(http.MethodPost, string(hash), s.handlePluginsAPIWriteModuleCode); code != http.StatusConflict {
		t.Errorf("write after an edit: %d %v", code, out)
	}
	if b, _ := os.ReadFile(path); string(b) != "-- edited by hand\n" {
		t.Errorf("the edit was written over: %q", b)
	}

	// shown again with the edit: written
	_, shown = call(http.MethodGet, "", s.handlePluginsAPIModuleCode)
	hash, _ = json.Marshal(map[string]any{"hash": shown["hash"]})
	if code, out := call(http.MethodPost, string(hash), s.handlePluginsAPIWriteModuleCode); code != http.StatusOK {
		t.Errorf("write: %d %v", code, out)
	}
	if b, _ := os.ReadFile(path); !strings.Contains(string(b), "dashboardAccess") {
		t.Errorf("module: %q", b)
	}
}
