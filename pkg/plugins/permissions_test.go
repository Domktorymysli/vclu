package plugins

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
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
		api      int
		declared *Permissions
		want     Permissions
		official bool
	}{
		// API 1 (no "api"): everything, for any plugin, as before permissions existed
		{"@vclu/solax", 0, nil, all, true},
		{"@vclu/by-hand", 0, nil, all, true},
		{"@jan/by-hand", 1, nil, all, false},
		{"@vclu/hijack", 0, &Permissions{}, all, false},
		// API 2: the declaration as written; without it the internet only
		{"@vclu/solax", 2, nil, Permissions{}, true},
		{"@vclu/solax", 2, &Permissions{LAN: true}, Permissions{LAN: true}, true},
		{"@jan/by-hand", 2, &Permissions{MQTT: true}, Permissions{MQTT: true}, false},
		// official only by where it was installed from
		{"@vclu/fake-default", 2, nil, Permissions{}, false},
		{"@vclu/gone-repo", 2, nil, Permissions{}, false},
		{"@vclu/not-installed", 2, nil, Permissions{}, false},
	}
	for _, c := range cases {
		got, _, official := l.effectivePermissions(c.id, PluginManifest{API: c.api, Permissions: c.declared})
		if got != c.want || official != c.official {
			t.Errorf("%s (api %d, declared %v): got %+v official=%v, want %+v official=%v", c.id, c.api, c.declared, got, official, c.want, c.official)
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
	if err := NewKVStoreManager(dir).GetStore("@vclu/weather").Set("token", "secret", false, 0); err != nil {
		t.Fatal(err)
	}
	for _, other := range []string{"@VCLU/weather", "@vclu--weather", "vclu--weather"} {
		if _, ok := NewKVStoreManager(dir).GetStore(other).Get("token"); ok {
			t.Errorf("%s reads @vclu/weather's KV", other)
		}
	}
}

// Plugin.apiVersion tells a plugin which contract the runtime speaks
func TestPluginAPIMaxMatchesRuntime(t *testing.T) {
	src, err := os.ReadFile(filepath.Join("..", "lua", "runtime", "core", "PluginAPI.lua"))
	if err != nil {
		t.Fatal(err)
	}
	want := fmt.Sprintf("Plugin.apiVersion = %d ", PluginAPIMax)
	if !strings.Contains(string(src), want) {
		t.Errorf("PluginAPI.lua does not set %q", want)
	}
}

// A plugin gets what it asks for as far as the user allowed it; without grants in the
// config (installed by hand, or before grants existed) what it asks for.
func TestEffectivePermissionsAllowedByGrants(t *testing.T) {
	l := permissionsLoader(t, nil, []config.InstalledPlugin{
		{ID: "@jan/shelly", Grants: &config.PluginGrants{LAN: &config.LANGrant{}}},
		{ID: "@jan/none", Grants: &config.PluginGrants{}},
		{ID: "@jan/old"},
	})
	lanMQTT := &Permissions{LAN: true, MQTT: true}
	cases := []struct {
		id   string
		api  int
		want Permissions
	}{
		{"@jan/shelly", 2, Permissions{LAN: true}}, // mqtt asked for by an update, not allowed yet
		{"@jan/none", 2, Permissions{}},
		{"@jan/none", 1, Permissions{}}, // API 1 asks for everything; the user allowed nothing
		{"@jan/old", 2, *lanMQTT},
		{"@jan/not-installed", 2, *lanMQTT},
	}
	for _, c := range cases {
		if got, _, _ := l.effectivePermissions(c.id, PluginManifest{API: c.api, Permissions: lanMQTT}); got != c.want {
			t.Errorf("%s (api %d): got %+v, want %+v", c.id, c.api, got, c.want)
		}
	}
}

// An installed plugin without grants keeps what its files on disk ask for, once: an update
// that asks for more shows it as pending instead of getting it.
func TestRecordGrantsKeepsWhatTheFilesAskFor(t *testing.T) {
	l := permissionsLoader(t, nil, []config.InstalledPlugin{{ID: "@jan/shelly", Enabled: true}})
	writeManifest := func(version, manifest string) {
		t.Helper()
		dir := filepath.Join(l.pluginsDir, "jan--shelly", version)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "plugin.json"), []byte(manifest), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	writeManifest("1.0.0", `{"id":"shelly","version":"1.0.0","api":2,"permissions":{"lan":true}}`)
	l.recordGrants("@jan/shelly")

	writeManifest("1.1.0", `{"id":"shelly","version":"1.1.0","api":2,"permissions":{"lan":true,"mqtt":true}}`)
	l.recordGrants("@jan/shelly") // already recorded: not widened

	mc, err := config.ReadMasterConfig(l.serverDir)
	if err != nil {
		t.Fatal(err)
	}
	inst := mc.Plugins.Installed[0]
	if inst.Grants == nil || inst.Grants.LAN == nil || inst.Grants.MQTT != nil || inst.Grants.Modbus != nil {
		t.Fatalf("grants: %+v, want lan only", inst.Grants)
	}
	if got := l.PendingPermissions(inst); strings.Join(got, ",") != "mqtt" {
		t.Errorf("pending: %v, want [mqtt]", got)
	}
}

func TestValidateGrants(t *testing.T) {
	good := []config.PluginGrants{
		{LAN: &config.LANGrant{Hosts: []string{"192.168.1.20", "10.0.0.0/8", "fd00::1"}}},
		{MQTT: &config.MQTTGrant{Subscribe: []string{"shelly/#", "home/+/temp", "#"}, Publish: []string{}}},
		{},
	}
	bad := []config.PluginGrants{
		{LAN: &config.LANGrant{Hosts: []string{"shelly.local"}}},
		{LAN: &config.LANGrant{Hosts: []string{"192.168.1.0/33"}}},
		{MQTT: &config.MQTTGrant{Subscribe: []string{"shelly/#/x"}}},
		{MQTT: &config.MQTTGrant{Publish: []string{"home/kit+/temp"}}},
		{MQTT: &config.MQTTGrant{Subscribe: []string{""}}},
	}
	for _, g := range good {
		if err := ValidateGrants(g); err != nil {
			t.Errorf("%+v: %v", g, err)
		}
	}
	for _, g := range bad {
		if err := ValidateGrants(g); err == nil {
			t.Errorf("%+v accepted", g)
		}
	}
}

// The lists in the grants reach the sandbox; a plugin allowed in full has none.
func TestEffectivePermissionsLimits(t *testing.T) {
	l := permissionsLoader(t, nil, []config.InstalledPlugin{
		{ID: "@jan/shelly", Grants: &config.PluginGrants{
			LAN:  &config.LANGrant{Hosts: []string{"192.168.1.20"}},
			MQTT: &config.MQTTGrant{Subscribe: []string{"shelly/#"}, Publish: []string{}},
		}},
		{ID: "@jan/full", Grants: &config.PluginGrants{LAN: &config.LANGrant{}}},
	})
	m := PluginManifest{API: 2, Permissions: &Permissions{LAN: true, MQTT: true}}
	_, limits, _ := l.effectivePermissions("@jan/shelly", m)
	data, _ := json.Marshal(limits)
	if string(data) != `{"lanHosts":["192.168.1.20"],"mqttSubscribe":["shelly/#"],"mqttPublish":[]}` {
		t.Errorf("limits: %s", data)
	}
	_, limits, _ = l.effectivePermissions("@jan/full", m)
	if data, _ := json.Marshal(limits); string(data) != `{}` {
		t.Errorf("limits allowed in full: %s", data)
	}
}

// Grants keep the difference between no list (any) and an empty one (none) in .vclu.json.
func TestGrantsJSONKeepsEmptyLists(t *testing.T) {
	in := `{"lan":{},"mqtt":{"subscribe":["a/#"],"publish":[]}}`
	var g config.PluginGrants
	if err := json.Unmarshal([]byte(in), &g); err != nil {
		t.Fatal(err)
	}
	out, _ := json.Marshal(g)
	if string(out) != in {
		t.Errorf("round trip: %s", out)
	}
}
