package web

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/Domktorymysli/vclu/pkg/config"
	"github.com/Domktorymysli/vclu/pkg/plugins"
	"github.com/gin-gonic/gin"
)

// An update of a plugin whose lan grant has no ports recorded keeps the ports of the files it
// replaces; the port the update adds waits for the user.
func TestPluginsAPIUpdateKeepsPortsOfOlderGrant(t *testing.T) {
	gin.SetMode(gin.TestMode)
	repo := manifestRepo(t, "ws", "1.1.0",
		`{"id":"ws","version":"1.1.0","api":2,"permissions":{"lan":{"ports":[80,22]}},"files":["init.lua"]}`)
	dir := t.TempDir()
	old := filepath.Join(dir, "plugins", "jan--ws", "1.0.0")
	if err := os.MkdirAll(old, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(old, "plugin.json"),
		[]byte(`{"id":"ws","version":"1.0.0","api":2,"permissions":{"lan":{"ports":[80]}}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := writeTestConfig(dir, &config.VCLUConfig{Plugins: &config.VCLUPluginsConfig{
		Repositories: []config.PluginRepository{{Name: "test", URL: repo.URL, Enabled: true}},
		Installed: []config.InstalledPlugin{{ID: "@jan/ws", Version: "1.0.0", Enabled: true, Repository: "test", API: 2,
			Grants: &config.PluginGrants{LAN: &config.LANGrant{}}}},
	}}); err != nil {
		t.Fatal(err)
	}
	s := &Server{serverDir: dir}
	if w := callInstall(t, s, `{"id":"ws","repository":"test"}`); w.Code != http.StatusOK {
		t.Fatalf("update: %d %s", w.Code, w.Body.String())
	}
	got := testInstalled(t, dir, "@jan/ws")
	if got.Version != "1.1.0" || got.Grants == nil || got.Grants.LAN == nil || got.Grants.LAN.Ports == nil ||
		!slices.Equal(*got.Grants.LAN.Ports, []int{80}) {
		t.Fatalf("after the update %+v, want 1.1.0 with websocket ports [80] recorded", got)
	}
	v := plugins.NewLoader(dir).PermissionsView(got)
	if !slices.Equal(v.Pending.LANPorts, []int{22}) || !slices.Contains(v.Pending.Names(), "lan") {
		t.Errorf("pending %+v, want websocket port 22 and lan waiting", v.Pending)
	}
}

// A repository's plugin.json and documentation are read up to a limit.
func TestRepoFileReadsAreBounded(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasPrefix(r.URL.Path, "/plugins/big/"):
			_, _ = w.Write(make([]byte, maxRepoFile+1))
		case r.URL.Path == "/plugins/ok/plugin.json":
			_, _ = w.Write([]byte(`{"id":"ok","docs":[{"title":"Readme","file":"README.md"}]}`))
		case r.URL.Path == "/plugins/ok/README.md":
			_, _ = w.Write([]byte("# ok\n"))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	repo := config.PluginRepository{Name: "test", URL: srv.URL, Enabled: true}

	if _, err := fetchPluginDoc(repo, "plugins/big", "README.md", ""); err == nil || !strings.Contains(err.Error(), "larger than") {
		t.Errorf("big doc: %v, want a size refusal", err)
	}
	if _, err := fetchPluginSchema(repo, "plugins/big", ""); err == nil || !strings.Contains(err.Error(), "larger than") {
		t.Errorf("big plugin.json: %v, want a size refusal", err)
	}
	if doc, err := fetchPluginDoc(repo, "plugins/ok", "README.md", ""); err != nil || doc != "# ok\n" {
		t.Errorf("small doc: %q, %v", doc, err)
	}
	if schema, err := fetchPluginSchema(repo, "plugins/ok", ""); err != nil || schema["id"] != "ok" {
		t.Errorf("small plugin.json: %v, %v", schema, err)
	}
}
