package lua

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Domktorymysli/vclu/pkg/config"
	"github.com/Domktorymysli/vclu/pkg/plugins"
)

// Every official plugin loads, inits, runs its factories, serves its sensors, unloads
// and loads again, with its manifest and with one without permissions (an installed
// plugin from before they existed). Opt in with VCLU_PLUGINS_DIR=<checkout>/plugins:
// the pollers reach their services.
func TestOfficialPluginsSmoke(t *testing.T) {
	src := os.Getenv("VCLU_PLUGINS_DIR")
	if src == "" {
		t.Skip("set VCLU_PLUGINS_DIR to the plugins directory of a vclu-plugins checkout")
	}
	smokePlugins(t, src, []string{"new-manifests", "old-manifests"}, true)
}

// Official plugins as people installed them with vclu 1.12, on plugin API 1 (testdata, never
// updated; one for each part of API 1 that API 2 took away): they load, make their devices,
// serve their sensors, unload and load again as before plugin API 2.
func TestAPI1PluginsOfVclu112(t *testing.T) {
	smokePlugins(t, filepath.Join("pkg", "lua", "testdata", "plugins-api1"), []string{"as-installed"}, false)
}

// smokePlugins runs the plugins in src through a load, their factories, an unload and a reload;
// strict: every official plugin is there (a vclu-plugins checkout), else the ones present.
func smokePlugins(t *testing.T, src string, modes []string, strict bool) {
	entries, err := os.ReadDir(src)
	if err != nil {
		t.Fatal(err)
	}
	configs := map[string]map[string]interface{}{
		"@vclu/telegram":          {"botToken": "123:abc", "chatId": "1", "enablePolling": true, "pollingInterval": 5},
		"@vclu/weather":           {"apiKey": "x", "city": "Brwinów,pl", "interval": 7000, "units": "metric"},
		"@vclu/solax":             {"host": "http://10.255.255.10", "user": "admin", "pass": "admin", "interval": 30},
		"@vclu/salda-recuperator": {"ip": "http://10.255.255.11/", "login": "admin", "password": "x", "interval": 60},
		"@vclu/supla-power-meter": {"directUrl": "https://svr0.supla.org/direct/1/x/read?format=json", "interval": 60},
		"@vclu/time-sync":         {"autoSync": true, "interval": 3600, "timezone": "Europe/Warsaw"},
		"@vclu/frigate":           {"host": "10.255.255.12"},
		"@vclu/sun-position":      {"latitude": 52.14, "longitude": 20.71},
	}
	for _, mode := range modes {
		t.Run(mode, func(t *testing.T) {
			dir := t.TempDir()
			engine := NewLuaEngine()
			defer engine.Close()
			engine.SetKVStoreManager(plugins.NewKVStoreManager(dir))
			loader := plugins.NewLoader(dir)
			loader.SetLuaExecutor(engine)
			engine.StartTimers()

			var installed []config.InstalledPlugin
			var ids []string
			for _, e := range entries {
				if !e.IsDir() {
					continue
				}
				raw, err := os.ReadFile(filepath.Join(src, e.Name(), "plugin.json"))
				if err != nil {
					t.Fatal(err)
				}
				var m map[string]interface{}
				if err := json.Unmarshal(raw, &m); err != nil {
					t.Fatal(err)
				}
				id, version := m["id"].(string), m["version"].(string)
				if !strings.HasPrefix(id, "@") {
					id = "@vclu/" + id
				}
				if mode == "old-manifests" {
					delete(m, "permissions")
					delete(m, "api")
					raw, _ = json.Marshal(m)
				}
				dst := filepath.Join(dir, "plugins", strings.TrimPrefix(strings.ReplaceAll(id, "/", "--"), "@"), version)
				if err := os.MkdirAll(dst, 0o755); err != nil {
					t.Fatal(err)
				}
				mustWrite(t, filepath.Join(dst, "plugin.json"), raw)
				files, _ := os.ReadDir(filepath.Join(src, e.Name()))
				for _, f := range files {
					if f.Name() == "plugin.json" || f.IsDir() {
						continue
					}
					b, _ := os.ReadFile(filepath.Join(src, e.Name(), f.Name()))
					mustWrite(t, filepath.Join(dst, f.Name()), b)
				}
				mustWrite(t, filepath.Join(dst, ".complete"), []byte("ok"))
				installed = append(installed, config.InstalledPlugin{ID: id, Version: version, Repository: "vCLU Official", Enabled: true, Config: configs[id]})
				ids = append(ids, id)
			}
			mc := config.VCLUConfig{Plugins: &config.VCLUPluginsConfig{
				Repositories: []config.PluginRepository{{Name: "vCLU Official", URL: config.OfficialPluginsRepoURL, Enabled: true, Default: true}},
				Installed:    installed,
			}}
			data, _ := json.Marshal(mc)
			if err := os.WriteFile(filepath.Join(dir, ".vclu.json"), data, 0o600); err != nil {
				t.Fatal(err)
			}

			var buf bytes.Buffer
			log.SetOutput(&buf)
			defer log.SetOutput(os.Stderr)

			baseTimers := activeTimers(engine)
			loadAll := func(round string) {
				for _, id := range ids {
					if err := loader.LoadPlugin(id, configs[id]); err != nil {
						t.Errorf("%s %s: %v", round, id, err)
					}
				}
			}
			loadAll("load")
			out, err := engine.Execute(`
				local res = {}
				local n = 0
				for id, inst in pairs(Plugin.instances) do
					n = n + 1
					local l = inst:listSensorsAndControls()
					local got, errs = 0, {}
					for _, s in ipairs(l.sensors) do
						local ok, e = pcall(function() return inst:get(s):get() end)
						got = got + 1
						if not ok then errs[#errs + 1] = s .. ": " .. tostring(e) end
					end
					for _, c in ipairs(l.controls) do
						local ok, e = pcall(function() return inst:get(c):get() end)
						got = got + 1
						if not ok then errs[#errs + 1] = c .. ": " .. tostring(e) end
					end
					local okS, st = pcall(function() return inst:stats() end)
					res[id] = { sensors = got, errors = errs, stats = okS, name = inst.name, version = inst.version }
				end
				return n .. " " .. JSON:encode(res)
			`)
			if err != nil {
				t.Fatal(err)
			}
			t.Logf("loaded: %s", out)
			if !strings.HasPrefix(out, fmt.Sprintf("%d ", len(ids))) {
				t.Errorf("want %d instances", len(ids))
			}
			fact, err := engine.Execute(fmt.Sprintf("local strict = %v\n", strict) + `
				-- a plugin of the set: required in a full checkout, skipped when this set has none
				local function plugin(id)
					local p = Plugin.getPlugin(id)
					assert(p or not strict, id .. " is published")
					return p
				end
				local modbus = Plugin.getPlugin("@vclu/modbus-gateway")
				local gw = modbus:create({ id = "rozdzielnica", host = "10.255.255.9", port = 4196, interval = 30, timeout = 500,
					devices = { { id = "ladowarka", unit = 1, name = "L", profile = "sdm120" } } })
				assert(gw, "modbus create")
				local musiccast = plugin("@vclu/musiccast")
				if musiccast then assert(musiccast:create({ id = "salon", ip = "10.255.255.159", name = "Amplituner" }), "musiccast create") end
				local now
				local pse = plugin("@vclu/pse-rce")
				if pse then
					local rce = pse:create({ id = "rce" })
					assert(rce, "rce create"); now = rce:now()
				end
				-- the older switch plugin, in a checkout that still has it (not in the published index)
				local sw = Plugin.getPlugin("@vclu/tasmota-switch")
				if sw then assert(sw:create({ ip = "10.255.255.100", name = "Lampka" }), "tasmota-switch create") end
				local influx = plugin("@vclu/influx-metrics")
				if influx then
					local inf = influx:create({ url = "http://10.255.255.50:8086/write?db=vclu", interval = 60, tags = { host = "vclu" } })
					assert(inf, "influx create"); inf:gauge("temperature", 22.5, { room = "salon" })
				end
				-- module code of the READMEs: handles of the plugins' objects (API 2)
				local tasmota = plugin("@vclu/tasmota")
				if tasmota then
					local plug = tasmota:device({ ip = "10.255.255.101" })
					local lamp = plug:relay(1)
					expose(lamp, "light", { path = "tasmota.lampa", name = "Lampa", mqtt = false, homekit = false })
					local power = plug:power()
					expose(power, "number", { path = "tasmota.lampa_moc", name = "Lampa moc", readonly = true, unit = "W", mqtt = false, homekit = false })
					assert(tasmota:device({ ip = "10.255.255.101" }) == plug, "tasmota: the same device is the same handle")
					lamp:onChange(function(v, old) end)
					assert(lamp:get() == nil and plug:isOnline() == false, "tasmota: no reading yet")
				end
				local zakupy = plugin("@vclu/zakupy")
				if zakupy then zakupy:dashboard("lista"):name("Zakupy"):auth("session") end
				local n = 0
				for _ in pairs(ExposedObjects._objects or {}) do n = n + 1 end
				return "factories ok, exposed=" .. n .. " rce:now=" .. tostring(now)
			`)
			if err != nil {
				t.Errorf("factories: %v", err)
			}
			t.Logf("%s", fact)
			pumpTimers(engine, 1500*time.Millisecond)
			out2, err := engine.Execute(`
				local errs = {}
				for id, inst in pairs(Plugin.instances) do
					local l = inst:listSensorsAndControls()
					for _, s in ipairs(l.sensors) do
						local ok, e = pcall(function() return inst:get(s):get() end)
						if not ok then errs[#errs + 1] = id .. "/" .. s .. ": " .. tostring(e) end
					end
				end
				return #errs .. " " .. table.concat(errs, "; ")
			`)
			if err != nil {
				t.Fatal(err)
			}
			t.Logf("sensor errors after 1.5 s: %s", out2)
			timersBefore := activeTimers(engine)

			for _, id := range ids {
				if err := loader.UnloadPlugin(id); err != nil {
					t.Errorf("unload %s: %v", id, err)
				}
			}
			mustLua(t, engine, `local n = 0; for _ in pairs(Plugin.instances) do n = n + 1 end; return n`, "0")
			mustLua(t, engine, `local n = 0; for k, v in pairs(_registry) do if type(k) == "string" and k:sub(1,1) == "@" then n = n + 1 end end; return n`, "0")
			mustLua(t, engine, `local n = 0; for k in pairs(PluginSandbox.sandboxes) do n = n + 1 end; return n`, "0")
			pumpTimers(engine, 1200*time.Millisecond)
			t.Logf("timers: %d before load, %d while loaded, %d after unload: %v", baseTimers, timersBefore, activeTimers(engine), timerLabels(engine))
			if activeTimers(engine) != baseTimers {
				t.Errorf("timers left after unload: %d", activeTimers(engine))
			}

			loadAll("reload")
			mustLua(t, engine, `local n = 0; for _ in pairs(Plugin.instances) do n = n + 1 end; return n`, fmt.Sprint(len(ids)))

			for _, line := range strings.Split(buf.String(), "\n") {
				// a plugin calls only the hosts its manifest lists
				if strings.Contains(line, "is not on the host list") {
					t.Errorf("%s", line)
				}
				l := strings.ToLower(line)
				if strings.Contains(l, "error") || strings.Contains(l, "read-only") || strings.Contains(l, "refused") ||
					strings.Contains(l, "not available in a plugin") || strings.Contains(l, "may emit only") ||
					strings.Contains(l, "disabled") || strings.Contains(l, "needs \"") {
					t.Logf("LOG %s", line)
				}
			}
		})
	}
}

func timerLabels(e *LuaEngine) []string {
	tm := e.timerManager
	tm.mu.Lock()
	defer tm.mu.Unlock()
	var out []string
	for _, t := range tm.timers {
		out = append(out, fmt.Sprintf("%d:%s", t.ID, t.Label))
	}
	return out
}

func mustWrite(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
}
