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
	entries, err := os.ReadDir(src)
	if err != nil {
		t.Fatal(err)
	}
	configs := map[string]map[string]interface{}{
		"@vclu/telegram":          {"botToken": "123:abc", "chatId": "1", "enablePolling": true, "pollingInterval": 5},
		"@vclu/weather":           {"apiKey": "x", "city": "Brwinów,pl", "interval": 7000, "units": "metric"},
		"@vclu/solax":             {"host": "http://192.0.2.10", "user": "admin", "pass": "admin", "interval": 30},
		"@vclu/salda-recuperator": {"ip": "http://192.0.2.11/", "login": "admin", "password": "x", "interval": 60},
		"@vclu/supla-power-meter": {"directUrl": "https://svr.example.invalid/direct/1/x/read?format=json", "interval": 60},
		"@vclu/time-sync":         {"autoSync": true, "interval": 3600, "timezone": "Europe/Warsaw"},
		"@vclu/frigate":           {"host": "192.0.2.12"},
		"@vclu/sun-position":      {"latitude": 52.14, "longitude": 20.71},
	}
	for _, mode := range []string{"new-manifests", "old-manifests"} {
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
					raw, _ = json.Marshal(m)
				}
				dst := filepath.Join(dir, "plugins", strings.TrimPrefix(strings.ReplaceAll(id, "/", "--"), "@"), version)
				if err := os.MkdirAll(dst, 0o755); err != nil {
					t.Fatal(err)
				}
				os.WriteFile(filepath.Join(dst, "plugin.json"), raw, 0o644)
				files, _ := os.ReadDir(filepath.Join(src, e.Name()))
				for _, f := range files {
					if f.Name() == "plugin.json" || f.IsDir() {
						continue
					}
					b, _ := os.ReadFile(filepath.Join(src, e.Name(), f.Name()))
					os.WriteFile(filepath.Join(dst, f.Name()), b, 0o644)
				}
				os.WriteFile(filepath.Join(dst, ".complete"), []byte("ok"), 0o644)
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
			if !strings.HasPrefix(out, "14 ") {
				t.Errorf("want 14 instances")
			}
			timersBefore := activeTimers(engine)
			fact, err := engine.Execute(`
				local modbus = Plugin.getPlugin("@vclu/modbus-gateway")
				local gw = modbus:create({ id = "rozdzielnica", host = "192.0.2.9", port = 4196, interval = 30, timeout = 500,
					devices = { { id = "ladowarka", unit = 1, name = "L", profile = "sdm120" } } })
				assert(gw, "modbus create")
				local amp = Plugin.getPlugin("@vclu/musiccast"):create({ id = "salon", ip = "192.0.2.159", name = "Amplituner" })
				assert(amp, "musiccast create")
				local rce = Plugin.getPlugin("@vclu/pse-rce"):create({ id = "rce" })
				assert(rce, "rce create"); local now = rce:now()
				local tas = Plugin.getPlugin("@vclu/tasmota-switch"):create({ ip = "192.0.2.100", name = "Lampka" })
				assert(tas, "tasmota create")
				local inf = Plugin.getPlugin("@vclu/influx-metrics"):create({ url = "http://192.0.2.50:8086/write?db=vclu", interval = 60, tags = { host = "vclu" } })
				assert(inf, "influx create"); inf:gauge("temperature", 22.5, { room = "salon" })
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
			timersBefore = activeTimers(engine)

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
			mustLua(t, engine, `local n = 0; for _ in pairs(Plugin.instances) do n = n + 1 end; return n`, "14")

			for _, line := range strings.Split(buf.String(), "\n") {
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
