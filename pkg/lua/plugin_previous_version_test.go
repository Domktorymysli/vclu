package lua

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/Domktorymysli/vclu/pkg/config"
)

const pluginInfoInit = `
local plugin = Plugin:new("mig", { name = "Mig" })
plugin:onInit(function(config, info)
    __seen = tostring(info.version) .. " from " .. tostring(info.previousVersion)
    if config.fail then error("migration failed") end
end)
return plugin
`

// onInit hears the version it was before an update until an init succeeds; a fresh install
// and an ordinary restart hear none.
func TestOnInitPreviousVersion(t *testing.T) {
	dir := t.TempDir()
	loader := installTestPlugin(t, dir, "@jan/mig", map[string]interface{}{"api": 2}, pluginInfoInit, false)
	engine := apiEngine(t, loader, dir)
	seen := func(want string) {
		t.Helper()
		mustLua(t, engine, `PluginSandbox.sandboxes["@jan/mig"].environment.__seen`, want)
	}
	load := func(cfg map[string]interface{}) error {
		_ = loader.UnloadPlugin("@jan/mig")
		return loader.LoadPlugin("@jan/mig", cfg)
	}
	if err := load(nil); err != nil {
		t.Fatal(err)
	}
	seen("1.0.0 from nil")
	if err := load(nil); err != nil {
		t.Fatal(err)
	}
	seen("1.0.0 from nil")

	// the update: new files, the lock names them
	src := filepath.Join(dir, "plugins", "jan--mig", "1.0.0")
	dst := filepath.Join(dir, "plugins", "jan--mig", "1.1.0")
	if err := os.Rename(src, dst); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(map[string]any{"id": "@jan/mig", "version": "1.1.0", "api": 2, "permissions": map[string]any{}})
	mustWrite(t, filepath.Join(dst, "plugin.json"), raw)
	lock, _ := json.Marshal(map[string]any{"@jan/mig": map[string]string{"version": "1.1.0", "path": "jan--mig/1.1.0"}})
	mustWrite(t, filepath.Join(dir, "plugins", "plugins.lock.json"), lock)

	if err := load(map[string]interface{}{"fail": true}); err == nil {
		t.Fatal("a failing init loaded")
	}
	if err := load(nil); err != nil {
		t.Fatal(err)
	}
	seen("1.1.0 from 1.0.0") // the failed start did not use it up
	if err := load(nil); err != nil {
		t.Fatal(err)
	}
	seen("1.1.0 from nil")

	mc, _ := config.ReadMasterConfig(dir)
	if got := mc.Plugins.Installed[0].StartedVersion; got != "1.1.0" {
		t.Errorf("startedVersion %q", got)
	}
}
