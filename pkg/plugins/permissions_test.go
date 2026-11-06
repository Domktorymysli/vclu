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
		official bool
	}{
		{"@vclu/solax", nil, all, true},
		{"@vclu/by-hand", nil, all, true},
		{"@vclu/hijack", nil, Permissions{}, false},
		{"@vclu/fake-default", nil, Permissions{}, false},
		{"@jan/by-hand", nil, Permissions{}, false},
		{"@vclu/gone-repo", nil, Permissions{}, false},
		{"@vclu/not-installed", nil, Permissions{}, false},
		// a declaration is followed as written, also for official plugins
		{"@vclu/solax", &Permissions{LAN: true}, Permissions{LAN: true}, true},
		{"@jan/by-hand", &Permissions{MQTT: true}, Permissions{MQTT: true}, false},
	}
	for _, c := range cases {
		got, official := l.effectivePermissions(c.id, PluginManifest{Permissions: c.declared})
		if got != c.want || official != c.official {
			t.Errorf("%s (declared %v): got %+v official=%v, want %+v official=%v", c.id, c.declared, got, official, c.want, c.official)
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

// Directory and KV names of two ids never meet, also on a case-insensitive file system.
func TestPluginIDsKeepTheirNames(t *testing.T) {
	for _, id := range []string{"@VCLU/weather", "@vclu/Weather", "@a--b/c", "@a/b--c", "@a-/b", "@a/-b", "@a/b-", "@-a/b"} {
		if validPluginID(id) {
			t.Errorf("%s accepted", id)
		}
	}
	for _, id := range []string{"@vclu/weather", "@vclu/sun-position", "@jan/foo.bar", "weather", "@vclu/pse-rce"} {
		if !validPluginID(id) {
			t.Errorf("%s refused", id)
		}
	}
	dir := t.TempDir()
	NewKVStoreManager(dir).GetStore("@vclu/weather").Set("token", "secret", false, 0)
	for _, other := range []string{"@VCLU/weather", "@vclu--weather", "vclu--weather"} {
		if _, ok := NewKVStoreManager(dir).GetStore(other).Get("token"); ok {
			t.Errorf("%s reads @vclu/weather's KV", other)
		}
	}
}
