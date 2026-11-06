package lua

import (
	"strings"
	"testing"
)

// Attempts to leave the plugin sandbox. Each must fail; a change to the sandbox
// keeps all of them failing.

func sandboxEngine(t *testing.T, id, perms string) *LuaEngine {
	t.Helper()
	engine := NewLuaEngine()
	t.Cleanup(engine.Close)
	if perms == "" {
		perms = "nil"
	}
	if _, err := engine.Execute(`PluginSandbox:create("` + id + `", ` + perms + `)`); err != nil {
		t.Fatalf("sandbox create: %v", err)
	}
	return engine
}

// runPlugin runs code in the sandbox of id; returns its result and error as strings.
func runPlugin(t *testing.T, engine *LuaEngine, id, code string) (string, string) {
	t.Helper()
	_, err := engine.Execute(`
		local r, e = PluginSandbox:execute("` + id + `", [==[` + code + `]==])
		__esc_r = tostring(r)
		__esc_e = tostring(e)
	`)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	r, _ := engine.Execute("__esc_r")
	e, _ := engine.Execute("__esc_e")
	return r, e
}

func mustLua(t *testing.T, engine *LuaEngine, expr, want string) {
	t.Helper()
	got, err := engine.Execute(expr)
	if err != nil {
		t.Fatalf("%s: %v", expr, err)
	}
	if got != want {
		t.Errorf("%s = %q, want %q", expr, got, want)
	}
}

func wantRefused(t *testing.T, what, r, e string) {
	t.Helper()
	if e == "nil" {
		t.Errorf("%s: not refused (result %s)", what, r)
	}
}

// 1. Shared tables: a plugin's change must stay in the plugin.
func TestPluginEscapeSharedGlobals(t *testing.T) {
	engine := sandboxEngine(t, "@t/a", "")

	r, e := runPlugin(t, engine, "@t/a", `
		string.upper = function() return "pwned" end
		table.insert = nil
		math.floor = nil
		return "ok"
	`)
	if e != "nil" {
		t.Fatalf("own copies must be writable: %s", e)
	}
	_ = r
	mustLua(t, engine, `string.upper("a")`, "A")
	mustLua(t, engine, `type(table.insert)`, "function")
	mustLua(t, engine, `type(math.floor)`, "function")

	for _, code := range []string{
		`Logger.info = function() end`,
		`JSON.encode = function() return "x" end`,
		`EventBus.on = function() end`,
		`Plugin.getPlugin = function() end`,
		`_.get = function() end`,
	} {
		r, e := runPlugin(t, engine, "@t/a", code)
		wantRefused(t, code, r, e)
	}
	mustLua(t, engine, `type(Logger.info)`, "function")
	mustLua(t, engine, `JSON:encode({1})`, "[1]")

	// HTTP and MQTT are not in the sandbox at all
	r, _ = runPlugin(t, engine, "@t/a", `return tostring(HTTP) .. tostring(HttpClient) .. tostring(MQTT)`)
	if r != "nilnilnil" {
		t.Errorf("HTTP/MQTT globals reachable: %s", r)
	}

	// shared objects keep working through the facade
	r, e = runPlugin(t, engine, "@t/a", `Logger:info("from plugin"); return JSON:encode({a = 1})`)
	if e != "nil" || r != `{"a":1}` {
		t.Errorf("Logger/JSON facade: r=%s e=%s", r, e)
	}
}

// 2. Other plugins, the Plugin class and the plugin's own identity.
func TestPluginEscapePluginInstances(t *testing.T) {
	engine := sandboxEngine(t, "@t/a", "")
	if _, err := engine.Execute(`PluginSandbox:create("@t/b", {lan = true, mqtt = true, modbus = true})`); err != nil {
		t.Fatal(err)
	}
	_, e := runPlugin(t, engine, "@t/b", `
		local p = Plugin:new("b", {name = "B", version = "1.0.0"})
		p:sensor("temp", function() return 21 end)
		p.config = { botToken = "SECRET" }
	`)
	if e != "nil" {
		t.Fatalf("plugin b: %s", e)
	}
	_, e = runPlugin(t, engine, "@t/a", `p = Plugin:new("a", {name = "A", version = "1.0.0"})`)
	if e != "nil" {
		t.Fatalf("plugin a: %s", e)
	}

	refused := map[string]string{
		"Plugin.instances":          `return Plugin.instances["@t/b"].config.botToken`,
		"Plugin.list":               `return Plugin.list()`,
		"getPlugin config":          `return Plugin.getPlugin("@t/b").config.botToken`,
		"getPlugin sandbox":         `return Plugin.getPlugin("@t/b")._sandbox.api`,
		"_:get config":              `return _:get("@t/b").config.botToken`,
		"_registry config":          `return _registry["@t/b"].config.botToken`,
		"kvGet on other":            `return _:get("@t/b"):kvGet("x")`,
		"method on fake self":       `local f = p.kvGet; return f({id = "@t/b"}, "x")`,
		"rename self":               `p.id = "@t/b"`,
		"drop own sandbox":          `p._sandbox = nil`,
		"control of other's sensor": `return _:get("@t/b"):get("temp"):on("OnChange", print)`,
	}
	for what, code := range refused {
		r, e := runPlugin(t, engine, "@t/a", code)
		if e == "nil" && r != "nil" {
			t.Errorf("%s: reached %s", what, r)
		}
	}

	// The real class stays out of reach through the instance
	r, _ := runPlugin(t, engine, "@t/a", `return tostring(p.__index) .. tostring(p.new) .. tostring(p.instances) .. tostring(p._sandbox)`)
	if r != "nilnilnilnil" {
		t.Errorf("instance leaks class or sandbox: %s", r)
	}
	// Chaining returns the plugin's instance, not the real one
	r, _ = runPlugin(t, engine, "@t/a", `return tostring(p:onInit(function() end) == p)`)
	if r != "true" {
		t.Errorf("onInit chaining: %s", r)
	}
	// What another plugin may see: its sensors' values
	r, e = runPlugin(t, engine, "@t/a", `return _:get("@t/b"):get("temp"):get()`)
	if r != "21" {
		t.Errorf("reading other's sensor: r=%s e=%s", r, e)
	}
	// Its own instance through getPlugin is writable
	r, e = runPlugin(t, engine, "@t/a", `local me = Plugin.getPlugin("@t/a"); me.state = 5; return tostring(me == p)`)
	if r != "true" {
		t.Errorf("own getPlugin: r=%s e=%s", r, e)
	}
	mustLua(t, engine, `Plugin.instances["@t/b"].config.botToken`, "SECRET")
	mustLua(t, engine, `Plugin.instances["@t/a"].state`, "5")
	mustLua(t, engine, `Plugin.instances["@t/a"].id`, "@t/a")
}

// 3. Objects in the registry: read methods only, also through pairs.
func TestPluginEscapeRegistryMethods(t *testing.T) {
	engine := sandboxEngine(t, "@t/a", "")
	if _, err := engine.Execute(`
		__esc_switched = false
		_registry["CLU.LAMP"] = {
			value = 0, nested = { x = 1 },
			getValue = function(self) return self.value end,
			isOn = function(self) return self.value == 1 end,
			switchOn = function(self) __esc_switched = true; self.value = 1 end,
			getSelf = function(self) return self end,
		}
		_registry.someFn = function() __esc_switched = true end
	`); err != nil {
		t.Fatal(err)
	}

	for what, code := range map[string]string{
		"method call":       `_:get("CLU.LAMP"):switchOn()`,
		"dot call":          `local l = _:get("CLU.LAMP"); l.switchOn(l)`,
		"_registry method":  `_registry["CLU.LAMP"]:switchOn()`,
		"pairs raw fn":      `local l = _:get("CLU.LAMP"); for k, v in pairs(l) do if k == "switchOn" then v(l) end end`,
		"next raw fn":       `local l = _:get("CLU.LAMP"); local k, v = next(l); while k do if k == "switchOn" then v(l) end; k, v = next(l, k) end`,
		"pairs raw table":   `for k, v in pairs(_:get("CLU.LAMP")) do if k == "nested" then v.x = 2 end end`,
		"read returns self": `_:get("CLU.LAMP"):getSelf().value = 9`,
		"_registry fn":      `_registry.someFn()`,
	} {
		r, e := runPlugin(t, engine, "@t/a", code)
		wantRefused(t, what, r, e)
	}
	mustLua(t, engine, `tostring(__esc_switched)`, "false")
	mustLua(t, engine, `_registry["CLU.LAMP"].value`, "0")
	mustLua(t, engine, `_registry["CLU.LAMP"].nested.x`, "1")

	r, e := runPlugin(t, engine, "@t/a", `
		local l = _:get("CLU.LAMP")
		local n = 0
		for _ in pairs(l) do n = n + 1 end
		return tostring(l:getValue()) .. tostring(l:isOn()) .. tostring(_:value("CLU.LAMP")) .. tostring(n > 0)
	`)
	if r != "0false0true" {
		t.Errorf("reads: r=%s e=%s", r, e)
	}
}

// 4. MQTT and Modbus only with the permission.
func TestPluginEscapeMqttModbusPermissions(t *testing.T) {
	engine := sandboxEngine(t, "@t/a", "")
	if _, err := engine.Execute(`PluginSandbox:create("@t/b", {mqtt = true, modbus = true})`); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"@t/a", "@t/b"} {
		if _, e := runPlugin(t, engine, id, `p = Plugin:new("x", {name = "X"})`); e != "nil" {
			t.Fatal(e)
		}
	}

	for what, code := range map[string]string{
		"publish":   `p:mqttPublish("zigbee2mqtt/lamp/set", "ON")`,
		"subscribe": `p:mqttSubscribe("#", function() end)`,
		"modbus":    `return Modbus.writeSingle({host = "192.168.0.9", port = 502, unit = 1, address = 1, value = 1}, function() end)`,
	} {
		r, e := runPlugin(t, engine, "@t/a", code)
		wantRefused(t, what, r, e)
		if !strings.Contains(e, "permissions") {
			t.Errorf("%s: error should name the permission: %s", what, e)
		}
	}

	_, e := runPlugin(t, engine, "@t/b", `p:mqttSubscribe("frigate/events", function() end)`)
	if e != "nil" {
		t.Errorf("subscribe with permission: %s", e)
	}
	_, e = runPlugin(t, engine, "@t/b", `p:mqttPublish("frigate/x", {a = 1}, {retain = true, qos = 1})`)
	if e != "nil" {
		t.Errorf("publish with permission: %s", e)
	}
	r, e := runPlugin(t, engine, "@t/b", `Modbus.request = nil; return type(Modbus.isAvailable)`)
	if r != "function" || e != "nil" {
		t.Errorf("modbus with permission: r=%s e=%s", r, e)
	}
	mustLua(t, engine, `type(Modbus.request)`, "function")
}

// 5. Local addresses: the sandbox sets the plugin's HTTP instance.
func TestPluginEscapeLANPolicySet(t *testing.T) {
	engine := sandboxEngine(t, "@t/a", "")
	if _, err := engine.Execute(`PluginSandbox:create("@t/b", {lan = true})`); err != nil {
		t.Fatal(err)
	}
	engine.httpManager.mu.RLock()
	defer engine.httpManager.mu.RUnlock()
	if !engine.httpManager.noLAN["@t/a"] {
		t.Error("@t/a without lan may reach local addresses")
	}
	if engine.httpManager.noLAN["@t/b"] {
		t.Error("@t/b with lan is refused local addresses")
	}
}
