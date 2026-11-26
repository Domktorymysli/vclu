package lua

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Domktorymysli/vclu/pkg/config"
	"github.com/Domktorymysli/vclu/pkg/plugins"
)

// A plugin written before permissions existed: LAN HTTP, MQTT, events and expose paths
// with any name, the HTTP global. Plugins like this are in use outside the official
// repository, so API 1 must keep running them unchanged.
const pluginV1Init = `
local plugin = Plugin:new("legacy", { name = "Legacy" })
local client = HTTP:new("legacy")
plugin:onInit(function()
    plugin:mqttSubscribe("legacy/+/set", function(topic, payload) end)
    plugin:emit("legacy:ready", {})
    EventBus.emit("parcel.ready", { n = 1 })
    local c = plugin:control("relay", function() return 0 end, function(v) end)
    plugin:expose(c, "switch", { path = "lampa_legacy" })
    __legacy_http = client ~= nil
end)
return plugin
`

func installTestPlugin(t *testing.T, dir, id string, manifest map[string]interface{}, init string, strict bool) *plugins.Loader {
	t.Helper()
	manifest["id"] = id
	manifest["version"] = "1.0.0"
	if _, ok := manifest["permissions"]; !ok && manifest["api"] == 2 {
		manifest["permissions"] = map[string]any{} // plugin API 2 declares them, {} for none
	}
	raw, _ := json.Marshal(manifest)
	dst := filepath.Join(dir, "plugins", strings.TrimPrefix(strings.ReplaceAll(id, "/", "--"), "@"), "1.0.0")
	if err := os.MkdirAll(dst, 0o755); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(dst, "plugin.json"), raw)
	mustWrite(t, filepath.Join(dst, "init.lua"), []byte(init))
	mustWrite(t, filepath.Join(dst, ".complete"), []byte("ok"))
	mc := config.VCLUConfig{Plugins: &config.VCLUPluginsConfig{
		Repositories: []config.PluginRepository{{Name: "jan", URL: "https://github.com/jan/vclu-plugin", Enabled: true}},
		Installed:    []config.InstalledPlugin{{ID: id, Version: "1.0.0", Repository: "jan", Enabled: true}},
		Strict:       strict,
	}}
	data, _ := json.Marshal(mc)
	mustWrite(t, filepath.Join(dir, ".vclu.json"), data)
	return plugins.NewLoader(dir)
}

func apiEngine(t *testing.T, loader *plugins.Loader, dir string) *LuaEngine {
	t.Helper()
	engine := NewLuaEngine()
	t.Cleanup(engine.Close)
	engine.SetKVStoreManager(plugins.NewKVStoreManager(dir))
	loader.SetLuaExecutor(engine)
	return engine
}

func TestPluginAPI1KeepsTheOldContract(t *testing.T) {
	dir := t.TempDir()
	loader := installTestPlugin(t, dir, "@jan/legacy", map[string]interface{}{}, pluginV1Init, false)
	engine := apiEngine(t, loader, dir)
	if err := loader.LoadPlugin("@jan/legacy", map[string]interface{}{}); err != nil {
		t.Fatalf("a plugin without \"api\" must load as before: %v", err)
	}
	mustLua(t, engine, `tostring(PluginSandbox.sandboxes["@jan/legacy"].environment.__legacy_http)`, "true")
	mustLua(t, engine, `PluginSandbox.sandboxes["@jan/legacy"].apiVersion`, "1")
	mustLua(t, engine, `tostring(PluginSandbox.sandboxes["@jan/legacy"].permissions.lan)`, "true")
	mustLua(t, engine, `tostring(_registry["lampa_legacy"] ~= nil)`, "true")

	// isolation holds on API 1 too
	for what, code := range map[string]string{
		"HTTP field": `HTTP.request = function() end`,
		"MQTT field": `MQTT.new = function() end`,
	} {
		r, e := runPlugin(t, engine, "@jan/legacy", code)
		wantRefused(t, what, r, e)
	}
	runPlugin(t, engine, "@jan/legacy", `string.upper = function() return "x" end`)
	mustLua(t, engine, `string.upper("a")`, "A")
}

func TestPluginAPI2FollowsItsDeclaration(t *testing.T) {
	dir := t.TempDir()
	loader := installTestPlugin(t, dir, "@jan/legacy", map[string]interface{}{"api": 2}, pluginV1Init, false)
	apiEngine(t, loader, dir)
	err := loader.LoadPlugin("@jan/legacy", map[string]interface{}{})
	if err == nil {
		t.Fatal("API 2 without permissions: the HTTP global and MQTT must be refused")
	}

	dir = t.TempDir()
	loader = installTestPlugin(t, dir, "@jan/v2", map[string]interface{}{"api": 2, "permissions": map[string]any{"mqtt": map[string]string{"why": "topics"}}}, `
		local plugin = Plugin:new("v2", {})
		plugin:onInit(function()
			plugin:mqttSubscribe("v2/set", function() end)
			plugin:emit("@jan/v2:ready", {})
			__v2_http = HTTP
		end)
		return plugin
	`, false)
	engine := apiEngine(t, loader, dir)
	if err := loader.LoadPlugin("@jan/v2", map[string]interface{}{}); err != nil {
		t.Fatalf("load: %v", err)
	}
	mustLua(t, engine, `tostring(PluginSandbox.sandboxes["@jan/v2"].environment.__v2_http)`, "nil")
	mustLua(t, engine, `tostring(PluginSandbox.sandboxes["@jan/v2"].permissions.lan)`, "false")
	r, e := runPlugin(t, engine, "@jan/v2", `Plugin.getPlugin("v2"):emit("parcel.ready", {})`)
	wantRefused(t, "API 2 event without its prefix", r, e)
}

func TestPluginAPIVersionChecks(t *testing.T) {
	dir := t.TempDir()
	loader := installTestPlugin(t, dir, "@jan/future", map[string]interface{}{"api": 3}, `return Plugin:new("future", {})`, false)
	apiEngine(t, loader, dir)
	err := loader.LoadPlugin("@jan/future", map[string]interface{}{})
	if err == nil || !strings.Contains(err.Error(), "update vCLU") {
		t.Errorf("API 3: %v", err)
	}

	dir = t.TempDir()
	loader = installTestPlugin(t, dir, "@jan/legacy", map[string]interface{}{}, pluginV1Init, true)
	apiEngine(t, loader, dir)
	err = loader.LoadPlugin("@jan/legacy", map[string]interface{}{})
	if err == nil || !strings.Contains(err.Error(), "strict") {
		t.Errorf("strict mode, API 1: %v", err)
	}

	dir = t.TempDir()
	loader = installTestPlugin(t, dir, "@jan/v2", map[string]interface{}{"api": 2}, `return Plugin:new("v2", {})`, true)
	apiEngine(t, loader, dir)
	if err := loader.LoadPlugin("@jan/v2", map[string]interface{}{}); err != nil {
		t.Errorf("strict mode, API 2: %v", err)
	}
}
