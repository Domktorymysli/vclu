package plugins

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/Domktorymysli/vclu/pkg/config"
)

func permissionsLoader(t *testing.T, repos []config.PluginRepository, installed []config.InstalledPlugin) *Loader {
	t.Helper()
	dir := t.TempDir()
	mc := config.VCLUConfig{Plugins: &config.VCLUPluginsConfig{Repositories: repos, Installed: installed}}
	data, err := json.Marshal(mc)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".vclu.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}
	return NewLoader(dir)
}

func TestEffectivePermissions(t *testing.T) {
	all := Permissions{LAN: true, MQTT: true, Modbus: true}
	official := config.PluginRepository{Name: "vCLU Official", URL: config.OfficialPluginsRepoURL + ".git/", Enabled: true, Default: true}
	// a repository that calls itself the default but is not the official one
	impostor := config.PluginRepository{Name: "mine", URL: "https://github.com/jan/vclu-plugins", Enabled: true, Default: true}
	foreign := config.PluginRepository{Name: "jan", URL: "https://github.com/jan/foo", Enabled: true}
	l := permissionsLoader(t, []config.PluginRepository{official, impostor, foreign}, []config.InstalledPlugin{
		{ID: "@vclu/solax", Repository: "vCLU Official"},
		{ID: "@vclu/hijack", Repository: "jan"},
		{ID: "@vclu/fake-default", Repository: "mine"},
		{ID: "@vclu/by-hand"},
		{ID: "@jan/by-hand"},
		{ID: "@vclu/gone-repo", Repository: "removed"},
	})

	cases := []struct {
		id       string
		declared *Permissions
		want     Permissions
	}{
		{"@vclu/solax", nil, all},
		{"@vclu/by-hand", nil, all},
		{"@vclu/hijack", nil, Permissions{}},
		{"@vclu/fake-default", nil, Permissions{}},
		{"@jan/by-hand", nil, Permissions{}},
		{"@vclu/gone-repo", nil, Permissions{}},
		{"@vclu/not-installed", nil, Permissions{}},
		// a declaration is followed as written, also for official plugins
		{"@vclu/solax", &Permissions{LAN: true}, Permissions{LAN: true}},
		{"@jan/by-hand", &Permissions{MQTT: true}, Permissions{MQTT: true}},
	}
	for _, c := range cases {
		got := l.effectivePermissions(c.id, PluginManifest{Permissions: c.declared})
		if got != c.want {
			t.Errorf("%s (declared %v): got %+v, want %+v", c.id, c.declared, got, c.want)
		}
	}
}

func TestManifestPermissionsParse(t *testing.T) {
	var m PluginManifest
	if err := json.Unmarshal([]byte(`{"id":"x","permissions":{"lan":true,"modbus":true}}`), &m); err != nil {
		t.Fatal(err)
	}
	if m.Permissions == nil || *m.Permissions != (Permissions{LAN: true, Modbus: true}) {
		t.Errorf("got %+v", m.Permissions)
	}
	var empty PluginManifest
	if err := json.Unmarshal([]byte(`{"id":"x","permissions":{}}`), &empty); err != nil {
		t.Fatal(err)
	}
	if empty.Permissions == nil || *empty.Permissions != (Permissions{}) {
		t.Errorf("an empty declaration grants nothing and is not a missing one: %+v", empty.Permissions)
	}
}
