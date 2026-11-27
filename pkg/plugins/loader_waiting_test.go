package plugins

import (
	"os"
	"path/filepath"
	"testing"

	lua "github.com/yuin/gopher-lua"

	"github.com/Domktorymysli/vclu/pkg/config"
)

// The sandbox learns which permissions plugin.json asks for and the user has not allowed, so
// its error says to allow them on the plugins page: an API 1 plugin whose settings left out
// mqtt and modbus (it asks for both without declaring them), an API 2 plugin that declares modbus.
func TestLoadPluginPassesWaitingPermissions(t *testing.T) {
	l := permissionsLoader(t, nil, []config.InstalledPlugin{
		{ID: "@jan/old", Version: "1.0.0", Enabled: true, Grants: &config.PluginGrants{LAN: &config.LANGrant{}}},
		{ID: "@jan/new", Version: "1.0.0", Enabled: true, Grants: &config.PluginGrants{}},
		{ID: "@jan/all", Version: "1.0.0", Enabled: true},
	})
	write := func(id, manifest string) {
		dir := filepath.Join(l.pluginsDir, safePluginDir(id))
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "plugin.json"), []byte(manifest), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "init.lua"), []byte("return true\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("@jan/old", `{"id":"@jan/old","name":"Old","version":"1.0.0"}`)
	write("@jan/new", `{"id":"@jan/new","name":"New","version":"1.0.0","api":2,"permissions":{"modbus":true}}`)
	write("@jan/all", `{"id":"@jan/all","name":"All","version":"1.0.0"}`)

	L := lua.NewState()
	defer L.Close()
	if err := L.DoString(`
		waiting = {}
		JSON = {}
		function JSON:decode(s) return s end
		PluginSandbox = {}
		function PluginSandbox:create(id, perms) waiting[id] = perms.waiting end
		function PluginSandbox:setConfig() end
		function PluginSandbox:execute() return true end
		function PluginSandbox:cleanup() end
		Plugin = {}
		function Plugin.initFromConfig() end
	`); err != nil {
		t.Fatal(err)
	}
	l.SetLuaExecutor(&luaStateExecutor{L})
	for id, want := range map[string]string{"@jan/old": `["mqtt","modbus"]`, "@jan/new": `["modbus"]`, "@jan/all": `[]`} {
		if err := l.LoadPlugin(id, nil); err != nil {
			t.Fatalf("%s: %v", id, err)
		}
		if got := L.GetGlobal("waiting").(*lua.LTable).RawGetString(id).String(); got != want {
			t.Errorf("%s waiting %s, want %s", id, got, want)
		}
	}
}
