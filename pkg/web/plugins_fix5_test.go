package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/Domktorymysli/vclu/pkg/config"
	"github.com/gin-gonic/gin"
)

// A repository whose plugin.json is api 2 for the preview and api 1 afterwards.
func apiSwapRepo(t *testing.T) (*httptest.Server, *atomic.Int32) {
	var reads atomic.Int32
	repo := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/plugins.json":
			_, _ = w.Write([]byte(`{"namespace":"jan","plugins":[{"id":"cloud","version":"1.0.0","path":"plugins/cloud","tag":"cloud-v1.0.0"}]}`))
		case "/plugins/cloud/plugin.json":
			if reads.Add(1) == 1 {
				_, _ = w.Write([]byte(`{"id":"cloud","version":"1.0.0","api":2,"permissions":{"internet":["a.example.com"]},"files":["init.lua"]}`))
			} else {
				_, _ = w.Write([]byte(`{"id":"cloud","version":"1.0.0","files":["init.lua"]}`))
			}
		case "/plugins/cloud/init.lua":
			_, _ = w.Write([]byte("return {}\n"))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(repo.Close)
	return repo, &reads
}

func installRequest(s *Server, body map[string]any) *httptest.ResponseRecorder {
	b, _ := json.Marshal(body)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/plugins/install", strings.NewReader(string(b)))
	c.Request.Header.Set("Content-Type", "application/json")
	s.handlePluginsAPIInstall(c)
	return w
}

// The confirmation showed plugin API 2; files on API 1 are not installed.
func TestInstallRefusesAnotherAPIThanSeen(t *testing.T) {
	repo, _ := apiSwapRepo(t)
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
		API       int
		Requested []string
		Internet  []string
	}
	if err := json.Unmarshal(w.Body.Bytes(), &preview); err != nil || preview.API != 2 {
		t.Fatalf("preview %s", w.Body.String())
	}

	w = installRequest(s, map[string]any{"id": "cloud", "repository": "test", "accept": preview.Requested,
		"seen": map[string]any{"api": preview.API, "internet": preview.Internet}})
	if w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), "changed_since_preview") {
		t.Fatalf("install: %d %s", w.Code, w.Body.String())
	}
	mc, err := config.ReadMasterConfig(dir)
	if err != nil {
		t.Fatal(err)
	}
	if mc.Plugins != nil && len(mc.Plugins.Installed) > 0 {
		t.Errorf("installed %+v", mc.Plugins.Installed)
	}
}

// Without the field (an older panel) the install goes on as before.
func TestInstallWithoutSeenAPI(t *testing.T) {
	repo, _ := apiSwapRepo(t)
	dir := t.TempDir()
	if err := writeTestConfig(dir, &config.VCLUConfig{Plugins: &config.VCLUPluginsConfig{
		Repositories: []config.PluginRepository{{Name: "test", URL: repo.URL, Enabled: true}},
	}}); err != nil {
		t.Fatal(err)
	}
	s := &Server{serverDir: dir}
	gin.SetMode(gin.TestMode)
	if w := installRequest(s, map[string]any{"id": "cloud", "repository": "test"}); w.Code != http.StatusOK {
		t.Fatalf("install: %d %s", w.Code, w.Body.String())
	}
}
