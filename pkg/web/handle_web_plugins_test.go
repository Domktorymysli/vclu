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

// Updating an installed plugin answers with its full id and new version, and keeps its
// configuration (#31: the page keys installed plugins by full id).
func TestPluginsAPIInstallUpdateKeepsConfig(t *testing.T) {
	repo := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"namespace":"vclu","plugins":[{"id":"salda-recuperator","version":"1.2.0"}]}`))
	}))
	defer repo.Close()

	dir := t.TempDir()
	err := config.WriteMasterConfig(dir, &config.VCLUConfig{Plugins: &config.VCLUPluginsConfig{
		Repositories: []config.PluginRepository{{Name: "test", URL: repo.URL, Enabled: true}},
		Installed: []config.InstalledPlugin{{
			ID: "@vclu/salda-recuperator", Version: "1.1.0", Enabled: false, Repository: "test",
			Config: map[string]interface{}{"host": "192.168.0.20"},
		}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	s := &Server{serverDir: dir}

	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/plugins/install",
		strings.NewReader(`{"id":"salda-recuperator","repository":"test"}`))
	c.Request.Header.Set("Content-Type", "application/json")
	s.handlePluginsAPIInstall(c)

	var resp struct {
		Action, FullID, Version string
	}
	json.Unmarshal(w.Body.Bytes(), &resp)
	if w.Code != http.StatusOK || resp.Action != "updated" || resp.FullID != "@vclu/salda-recuperator" || resp.Version != "1.2.0" {
		t.Fatalf("answer %d %s", w.Code, w.Body.String())
	}

	cfg, err := config.ReadMasterConfig(dir)
	if err != nil {
		t.Fatal(err)
	}
	inst := cfg.Plugins.Installed
	if len(inst) != 1 || inst[0].ID != "@vclu/salda-recuperator" || inst[0].Version != "1.2.0" ||
		inst[0].Enabled || inst[0].Config["host"] != "192.168.0.20" {
		t.Errorf(".vclu.json plugins: %+v", inst)
	}
}
