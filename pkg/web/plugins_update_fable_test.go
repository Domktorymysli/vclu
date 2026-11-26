package web

import (
	"net/http"
	"slices"
	"sync/atomic"
	"testing"

	"github.com/Domktorymysli/vclu/pkg/config"
	"github.com/Domktorymysli/vclu/pkg/plugins"
	"github.com/gin-gonic/gin"
)

// An update that declares a new action, or a permission the installed version did not have,
// gets neither: the grant keeps what the user confirmed, the rest waits in the panel.
func TestFableUpdateNewActionAndPermissionWait(t *testing.T) {
	gin.SetMode(gin.TestMode)
	var version, manifest atomic.Value
	version.Store("1.0.0")
	manifest.Store(`{"id":"cloud","version":"1.0.0","api":2,"permissions":{},"files":["init.lua"],
		"actions":[{"name":"add","description":"d"}]}`)
	repo := pluginRepoServer(t, &version, &manifest)
	dir := t.TempDir()
	if err := writeTestConfig(dir, &config.VCLUConfig{Plugins: &config.VCLUPluginsConfig{
		Repositories: []config.PluginRepository{{Name: "test", URL: repo.URL, Enabled: true}},
	}}); err != nil {
		t.Fatal(err)
	}
	s := &Server{serverDir: dir}
	if w := callInstall(t, s, `{"id":"cloud","repository":"test"}`); w.Code != http.StatusOK {
		t.Fatalf("install: %d %s", w.Code, w.Body.String())
	}
	inst := testInstalled(t, dir, "@jan/cloud")
	if inst.Grants == nil || inst.Grants.Actions == nil || !slices.Equal(*inst.Grants.Actions, []string{"add"}) || inst.Grants.LAN != nil {
		t.Fatalf("grants after install %+v", inst.Grants)
	}

	version.Store("1.1.0")
	manifest.Store(`{"id":"cloud","version":"1.1.0","api":2,"permissions":{"lan":{"ports":[80]}},"files":["init.lua"],
		"actions":[{"name":"add","description":"d"},{"name":"clear_all","description":"c","confirm":true}]}`)
	p := callPreview(t, s, "id=cloud&repository=test&fullId=@jan/cloud")
	if acts, _ := p["actions"].([]any); len(acts) != 2 {
		t.Errorf("preview actions %v", p["actions"])
	}
	if w := callInstall(t, s, `{"id":"cloud","repository":"test"}`); w.Code != http.StatusOK {
		t.Fatalf("update: %d %s", w.Code, w.Body.String())
	}
	inst = testInstalled(t, dir, "@jan/cloud")
	if inst.Version != "1.1.0" || inst.Grants == nil || !slices.Equal(*inst.Grants.Actions, []string{"add"}) || inst.Grants.LAN != nil {
		t.Fatalf("grants after update %+v (%s)", inst.Grants, inst.Version)
	}
	v := plugins.NewLoader(dir).PermissionsView(inst)
	if !slices.Equal(v.Pending.Actions, []string{"clear_all"}) || !slices.Equal(v.Pending.Names(), []string{"lan"}) || !slices.Equal(v.Pending.LANPorts, []int{80}) {
		t.Errorf("pending %+v", v.Pending)
	}
}
