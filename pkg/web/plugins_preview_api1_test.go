package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Domktorymysli/vclu/pkg/config"
	"github.com/gin-gonic/gin"
)

// A plugin on API 1 reaches any internet host: the install dialog shows it as asked for, "*".
func TestPluginsAPIPreviewShowsAPI1Internet(t *testing.T) {
	repo := manifestRepo(t, "old", "1.0.0", `{"id":"old","version":"1.0.0","files":["init.lua"]}`)
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
	c.Request = httptest.NewRequest(http.MethodGet, "/api/plugins/preview?id=old&repository=test", nil)
	s.handlePluginsAPIPreview(c)
	var out struct {
		API       int      `json:"api"`
		Requested []string `json:"requested"`
		Internet  []string `json:"internet"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil || w.Code != http.StatusOK {
		t.Fatalf("preview: %d %s", w.Code, w.Body.String())
	}
	if out.API != 1 || strings.Join(out.Requested, ",") != "internet,lan,mqtt,modbus" || strings.Join(out.Internet, ",") != "*" {
		t.Errorf("API 1 preview: %+v, want api 1, internet,lan,mqtt,modbus and internet *", out)
	}
}
