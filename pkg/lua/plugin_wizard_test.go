package lua

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Domktorymysli/vclu/pkg/plugins"
)

const wizardForm = `{
  "settings": {
    "ip":    { "type": "host", "required": true, "label": "CLU IP" },
    "token": { "type": "secret", "required": true, "label": "Token" }
  },
  "devices": {
    "discover": true,
    "row": "{name}",
    "fields": {
      "id":   { "type": "string", "required": true, "readonly": true },
      "name": { "type": "string", "required": true },
      "kind": { "type": "enum", "options": ["DOUT", "DIMM"] }
    }
  }
}`

const wizardInit = `
local plugin = Plugin:new("clu", { name = "CLU" })
plugin:onInit(function(cfg)
    __seen_token = plugin:secret("token")
    __seen_ip = cfg.ip
end)
plugin:discover(function(cfg, done)
    setTimeout(50, function()
        done({
            { id = "DOU1", name = "Lamp " .. cfg.ip, kind = "DOUT", extra = "dropped field" },
            { id = "DIM1", name = "Dimmer", kind = "LED" },
        })
    end)
end)
plugin:moduleCode(function(cfg, devices)
    local out = { 'local clu = Plugin.getPlugin("@jan/clu")' }
    for _, d in ipairs(devices) do
        out[#out + 1] = string.format('-- %s %s', d.id, d.name)
    end
    return table.concat(out, "\n")
end)
return plugin
`

func TestPluginWizard(t *testing.T) {
	dir := t.TempDir()
	loader := installTestPlugin(t, dir, "@jan/clu", map[string]interface{}{"api": 2, "form": "form.json"}, wizardInit, false)
	pluginDir := filepath.Join(dir, "plugins", "jan--clu", "1.0.0")
	mustWrite(t, filepath.Join(pluginDir, "form.json"), []byte(wizardForm))
	engine := apiEngine(t, loader, dir)
	engine.StartTimers()
	stop := make(chan struct{})
	go engine.RunLoop(stop)
	t.Cleanup(func() { close(stop) })

	form, raw, err := loader.Form("@jan/clu")
	if err != nil || form == nil || len(raw) == 0 {
		t.Fatalf("form: %v %v", form, err)
	}
	if form.Devices.RowKey() != "id" {
		t.Fatalf("row key: %q", form.Devices.RowKey())
	}

	// step 1: the secret goes apart from the config
	values, err := form.Check(map[string]any{"ip": "192.168.1.10", "token": "s3cret"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, inConfig := values.Config["token"]; inConfig || values.Secrets["token"] != "s3cret" {
		t.Fatalf("secret in config: %v / %v", values.Config, values.Secrets)
	}
	if err := plugins.SetPluginSecrets(dir, "@jan/clu", values.Secrets); err != nil {
		t.Fatal(err)
	}
	if _, err := form.Check(map[string]any{"ip": "not-an-ip"}, nil); err == nil {
		t.Fatal("a host field takes an IP address")
	}

	if err := loader.LoadPlugin("@jan/clu", values.Config); err != nil {
		t.Fatal(err)
	}
	env := `PluginSandbox.sandboxes["@jan/clu"].environment`
	mustLua(t, engine, env+`.__seen_token`, "s3cret")
	mustLua(t, engine, env+`.__seen_ip`, "192.168.1.10")
	mustLua(t, engine, `tostring(PluginSandbox.sandboxes["@jan/clu"].environment.__plugin_config.token)`, "nil")

	// step 2: discover, async; a row with a bad value is dropped, an unknown field too
	rows, dropped, err := loader.Discover(context.Background(), "@jan/clu")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0]["id"] != "DOU1" || rows[0]["extra"] != nil || len(dropped) != 1 {
		t.Fatalf("rows %v, dropped %v", rows, dropped)
	}

	values, err = form.Check(map[string]any{"ip": "192.168.1.10"}, rows)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(values.Hosts, ",") != "192.168.1.10" {
		t.Fatalf("hosts: %v", values.Hosts)
	}
	_ = loader.UnloadPlugin("@jan/clu")
	if err := loader.LoadPlugin("@jan/clu", values.Config); err != nil {
		t.Fatal(err)
	}

	// step 3: the module from the saved devices
	code, err := loader.ModuleCode("@jan/clu")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(code, "-- DOU1 Lamp 192.168.1.10") {
		t.Fatalf("code: %q", code)
	}
	_ = os.MkdirAll(filepath.Join(dir, "modules"), 0o755)
	mustWrite(t, filepath.Join(dir, "modules", "init.lua"), []byte("local x = 1\nreturn x\n"))
	if err := plugins.WriteModuleCode(dir, "@jan/clu", code); err != nil {
		t.Fatal(err)
	}
	if err := plugins.WriteModuleCode(dir, "@jan/clu", code); err != nil {
		t.Fatal(err)
	}
	read := func(rel string) string {
		b, err := os.ReadFile(filepath.Join(dir, rel))
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	if got := read("modules/plugins/jan__clu.lua"); got != plugins.ModuleText("@jan/clu", code) {
		t.Fatalf("module: %q", got)
	}
	if got := read("modules/plugins/init.lua"); strings.Count(got, `require("plugins/jan__clu")`) != 1 {
		t.Fatalf("plugins/init.lua: %q", got)
	}
	if got := read("modules/init.lua"); got != "local x = 1\nrequire(\"plugins\")\nreturn x\n" {
		t.Fatalf("modules/init.lua: %q", got)
	}
}

func TestPluginWizardWithoutDiscover(t *testing.T) {
	dir := t.TempDir()
	loader := installTestPlugin(t, dir, "@jan/plain", map[string]interface{}{"api": 2, "form": "form.json"},
		`local p = Plugin:new("plain", {}) return p`, false)
	mustWrite(t, filepath.Join(dir, "plugins", "jan--plain", "1.0.0", "form.json"), []byte(wizardForm))
	apiEngine(t, loader, dir)
	if err := loader.LoadPlugin("@jan/plain", map[string]any{"ip": "10.0.0.1"}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := loader.Discover(context.Background(), "@jan/plain"); err == nil || !strings.Contains(err.Error(), "no discover") {
		t.Fatalf("discover without plugin:discover: %v", err)
	}
	if _, err := loader.ModuleCode("@jan/plain"); err == nil || !strings.Contains(err.Error(), "no moduleCode") {
		t.Fatalf("moduleCode without plugin:moduleCode: %v", err)
	}
}

// A commented-out require stays commented out: the user turned the plugin's module off.
func TestWizardKeepsARequireTurnedOff(t *testing.T) {
	dir := t.TempDir()
	_ = os.MkdirAll(filepath.Join(dir, "modules", "plugins"), 0o755)
	mustWrite(t, filepath.Join(dir, "modules", "plugins", "init.lua"), []byte("-- require(\"plugins/jan__clu\")\n"))
	if err := plugins.WriteModuleCode(dir, "@jan/clu", "x = 1"); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(filepath.Join(dir, "modules", "plugins", "init.lua"))
	if string(b) != "-- require(\"plugins/jan__clu\")\n" {
		t.Fatalf("init.lua: %q", b)
	}
}
