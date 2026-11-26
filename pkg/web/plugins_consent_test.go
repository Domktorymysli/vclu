package web

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/Domktorymysli/vclu/pkg/config"
	"github.com/gin-gonic/gin"
)

// confirmInstall installs as the panel does: what the request leaves out comes from the
// preview, with everything the plugin asks for accepted.
func confirmInstall(s *Server, c *gin.Context) {
	body, _ := io.ReadAll(c.Request.Body)
	c.Request.Body = io.NopCloser(bytes.NewReader(withConsent(s, body)))
	s.handlePluginsAPIInstall(c)
}

func withConsent(s *Server, body []byte) []byte {
	var req map[string]any
	if json.Unmarshal(body, &req) != nil {
		return body
	}
	seen, _ := req["seen"].(map[string]any)
	_, hasAccept := req["accept"]
	if hasAccept && seen != nil && seen["api"] != nil {
		return body
	}
	id, _ := req["id"].(string)
	repo, _ := req["repository"].(string)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet,
		"/api/plugins/preview?id="+url.QueryEscape(id)+"&repository="+url.QueryEscape(repo), nil)
	s.handlePluginsAPIPreview(c)
	var p struct {
		API       int
		Requested []string
		Seen      map[string]any
	}
	if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &p) != nil {
		p.API = 1 // no preview: the download fails the same way
	}
	if !hasAccept {
		req["accept"] = nonNil(p.Requested)
	}
	if seen == nil {
		seen = p.Seen
		if seen == nil {
			seen = map[string]any{}
		}
	}
	if seen["api"] == nil || seen["api"] == float64(0) {
		seen["api"] = p.API
	}
	req["seen"] = seen
	out, _ := json.Marshal(req)
	return out
}

func rawInstall(s *Server, body string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/plugins/install", strings.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	s.handlePluginsAPIInstall(c)
	return w
}

// Nothing installs or updates without the confirmation: accept and seen (with its API) are
// required, with a session as with an API key.
func TestInstallNeedsConsent(t *testing.T) {
	repo := pluginRepo(t, "solax", "1.0.1", true)
	dir := t.TempDir()
	if err := writeTestConfig(dir, &config.VCLUConfig{Plugins: &config.VCLUPluginsConfig{
		Repositories: []config.PluginRepository{{Name: "test", URL: repo.URL, Enabled: true}},
	}}); err != nil {
		t.Fatal(err)
	}
	s := &Server{serverDir: dir}
	gin.SetMode(gin.TestMode)
	for _, body := range []string{
		`{"id":"solax","repository":"test"}`,
		`{"id":"solax","repository":"test","accept":[]}`,
		`{"id":"solax","repository":"test","seen":{"api":1}}`,
		`{"id":"solax","repository":"test","accept":[],"seen":{}}`,
		`{"id":"solax","repository":"test","accept":null,"seen":{"api":1}}`,
	} {
		if w := rawInstall(s, body); w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "consent_required") {
			t.Errorf("%s: %d %s", body, w.Code, w.Body.String())
		}
	}
	mc, err := config.ReadMasterConfig(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(mc.Plugins.Installed) > 0 {
		t.Fatalf("installed %+v", mc.Plugins.Installed)
	}
	// as the panel sends it
	if w := rawInstall(s, `{"id":"solax","repository":"test","accept":[],"seen":{"api":1}}`); w.Code != http.StatusOK {
		t.Fatalf("confirmed install: %d %s", w.Code, w.Body.String())
	}
}

// The permissions only the user enables in the panel never come with an install, even accepted.
func TestInstallNeverGrantsPanelOnlyPermissions(t *testing.T) {
	repo := manifestRepo(t, "raw", "1.0.0",
		`{"id":"raw","version":"1.0.0","api":2,"permissions":{"clu":{"why":"CLU"},"tftp":{"why":"files"},"serial":{"why":"port"},"lan":{"why":"devices"}},"files":["init.lua"]}`)
	dir := t.TempDir()
	if err := writeTestConfig(dir, &config.VCLUConfig{Plugins: &config.VCLUPluginsConfig{
		Repositories: []config.PluginRepository{{Name: "test", URL: repo.URL, Enabled: true}},
	}}); err != nil {
		t.Fatal(err)
	}
	s := &Server{serverDir: dir}
	gin.SetMode(gin.TestMode)
	w := rawInstall(s, `{"id":"raw","repository":"test","accept":["lan","clu","tftp","serial"],"seen":{"api":2}}`)
	if w.Code != http.StatusOK {
		t.Fatalf("install: %d %s", w.Code, w.Body.String())
	}
	mc, err := config.ReadMasterConfig(dir)
	if err != nil || len(mc.Plugins.Installed) != 1 {
		t.Fatalf("entries %+v (%v)", mc.Plugins.Installed, err)
	}
	g := mc.Plugins.Installed[0].Grants
	if g == nil || g.LAN == nil || g.CLU != nil || g.TFTP != nil || g.Serial != nil {
		t.Errorf("grants %+v, want lan only", g)
	}
}
