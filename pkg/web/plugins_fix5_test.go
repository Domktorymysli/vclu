package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

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
	confirmInstall(s, c)
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

// Without the field (an older panel) the install is refused: the API it showed is not known.
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
	if w := rawInstall(s, `{"id":"cloud","repository":"test","accept":["internet"],"seen":{"internet":["a.example.com"]}}`); w.Code != http.StatusBadRequest {
		t.Fatalf("install: %d %s", w.Code, w.Body.String())
	}
}

// A repository listing plugins of one namespace; files come slowly, so installs overlap.
func slowRepo(t *testing.T, namespace string, ids ...string) *httptest.Server {
	var list []string
	for _, id := range ids {
		list = append(list, `{"id":"`+id+`","version":"1.0.0","path":"plugins/`+id+`","tag":"`+id+`-v1.0.0"}`)
	}
	repo := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/plugins.json" {
			_, _ = w.Write([]byte(`{"namespace":"` + namespace + `","plugins":[` + strings.Join(list, ",") + `]}`))
			return
		}
		for _, id := range ids {
			switch r.URL.Path {
			case "/plugins/" + id + "/plugin.json":
				_, _ = w.Write([]byte(`{"id":"` + id + `","version":"1.0.0","api":2,"files":["init.lua"]}`))
				return
			case "/plugins/" + id + "/init.lua":
				time.Sleep(150 * time.Millisecond)
				_, _ = w.Write([]byte("return {}\n"))
				return
			}
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	t.Cleanup(repo.Close)
	return repo
}

func installTogether(s *Server, bodies ...map[string]any) []*httptest.ResponseRecorder {
	out := make([]*httptest.ResponseRecorder, len(bodies))
	var wg sync.WaitGroup
	for i, b := range bodies {
		wg.Add(1)
		go func() {
			defer wg.Done()
			out[i] = installRequest(s, b)
		}()
	}
	wg.Wait()
	return out
}

// The same id from two repositories at once: one is installed, the other is refused, and the
// entry names the repository its files came from.
func TestInstallsTogetherSameID(t *testing.T) {
	a, b := slowRepo(t, "jan", "cloud"), slowRepo(t, "jan", "cloud")
	dir := t.TempDir()
	if err := writeTestConfig(dir, &config.VCLUConfig{Plugins: &config.VCLUPluginsConfig{
		Repositories: []config.PluginRepository{{Name: "a", URL: a.URL, Enabled: true}, {Name: "b", URL: b.URL, Enabled: true}},
	}}); err != nil {
		t.Fatal(err)
	}
	s := &Server{serverDir: dir}
	gin.SetMode(gin.TestMode)
	res := installTogether(s, map[string]any{"id": "cloud", "repository": "a"}, map[string]any{"id": "cloud", "repository": "b"})
	ok, refused := 0, 0
	for _, w := range res {
		switch w.Code {
		case http.StatusOK:
			ok++
		case http.StatusConflict:
			refused++
		default:
			t.Errorf("install: %d %s", w.Code, w.Body.String())
		}
	}
	if ok != 1 || refused != 1 {
		t.Fatalf("%d installed, %d refused", ok, refused)
	}
	mc, err := config.ReadMasterConfig(dir)
	if err != nil || len(mc.Plugins.Installed) != 1 {
		t.Fatalf("entries %+v (%v)", mc.Plugins.Installed, err)
	}
}

// Two ids whose wizard modules share a file, at once: only one is installed.
func TestInstallsTogetherSharedModuleName(t *testing.T) {
	repo := slowRepo(t, "a", "b.c", "b_c")
	dir := t.TempDir()
	if err := writeTestConfig(dir, &config.VCLUConfig{Plugins: &config.VCLUPluginsConfig{
		Repositories: []config.PluginRepository{{Name: "a", URL: repo.URL, Enabled: true}},
	}}); err != nil {
		t.Fatal(err)
	}
	s := &Server{serverDir: dir}
	gin.SetMode(gin.TestMode)
	res := installTogether(s, map[string]any{"id": "b.c", "repository": "a"}, map[string]any{"id": "b_c", "repository": "a"})
	codes := []int{res[0].Code, res[1].Code}
	conflict := 0
	for _, w := range res {
		if w.Code == http.StatusConflict && strings.Contains(w.Body.String(), "module_name") {
			conflict++
		} else if w.Code != http.StatusOK {
			t.Errorf("install: %d %s", w.Code, w.Body.String())
		}
	}
	if conflict != 1 {
		t.Fatalf("codes %v, want one module_name refusal", codes)
	}
	mc, err := config.ReadMasterConfig(dir)
	if err != nil || len(mc.Plugins.Installed) != 1 {
		t.Fatalf("entries %+v (%v)", mc.Plugins.Installed, err)
	}
}
