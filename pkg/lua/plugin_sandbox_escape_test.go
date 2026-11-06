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

func activeTimers(e *LuaEngine) int { return e.timerManager.Stats().ActiveTimers }

// Functions a plugin puts on its instance are called by the core and modules on the
// real instance (init, create, get): the plugin's code still gets only its proxy.
func TestPluginEscapeOwnMethodsSeeProxy(t *testing.T) {
	engine := sandboxEngine(t, "@t/a", "")
	if _, e := runPlugin(t, engine, "@t/a", `
		L = {}
		p = Plugin:new("a", {name = "A"})
		p.init = function(self, cfg) L.init = self end
		p.get = function(self, id) L.get = self; return nil end
		function p:create(opts)
			L.create = self
			self:sensor(opts.id, function() return 7 end)
			return { id = opts.id }
		end
	`); e != "nil" {
		t.Fatal(e)
	}
	if _, err := engine.Execute(`
		Plugin.initFromConfig("@t/a", {})
		__made = Plugin.getPlugin("@t/a"):create({ id = "s1" })
		local _ = Plugin.getPlugin("@t/a"):get("x")
	`); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"init", "create", "get"} {
		r, _ := runPlugin(t, engine, "@t/a", `local s = L.`+k+`; return tostring(s == p) .. tostring(s._sandbox) .. tostring(s.instances) .. tostring(s.httpRequest == p.httpRequest)`)
		if r != "truenilniltrue" {
			t.Errorf("%s got the real instance: %s", k, r)
		}
	}
	mustLua(t, engine, `__made.id`, "s1")
	mustLua(t, engine, `Plugin.instances["@t/a"]._sensors.s1:get()`, "7")
}

// A read method's arguments: a CLU object builds Lua run on the CLU from them.
func TestPluginEscapeCLUCodeInjection(t *testing.T) {
	engine := sandboxEngine(t, "@t/a", "")
	if _, err := engine.Execute(`
		__cmds = {}
		local clu = {
			execute = function(self, n, cmd) __cmds[#__cmds + 1] = cmd; return 0 end,
			executeNoReply = function(self, n, cmd) __cmds[#__cmds + 1] = cmd end,
		}
		_registry["CLU1.DOU1"] = setmetatable({ _clu = clu, _objId = "DOU1" }, RemoteObject)
	`); err != nil {
		t.Fatal(err)
	}
	for what, code := range map[string]string{
		"code in index":  `_:get("CLU1.DOU1"):get("0) DOU2:switchOn() --")`,
		"table argument": `_:get("CLU1.DOU1"):get({})`,
	} {
		r, e := runPlugin(t, engine, "@t/a", code)
		wantRefused(t, what, r, e)
	}
	mustLua(t, engine, `tostring(__cmds[1])`, "nil")
	if _, e := runPlugin(t, engine, "@t/a", `_:get("CLU1.DOU1"):get(0)`); e != "nil" {
		t.Errorf("a read with an index: %s", e)
	}
	mustLua(t, engine, `__cmds[1]`, "DOU1:get(0)")
	// modules: an index from a string still works, code in it does not
	mustLua(t, engine, `_registry["CLU1.DOU1"]:get("3"); return __cmds[2]`, "DOU1:get(3)")
	if _, err := engine.Execute(`_registry["CLU1.DOU1"]:set("0, 1) DOU2:switchOn(", 1)`); err == nil {
		t.Error("set with code in the index was sent")
	}
}

func TestPluginEscapeLoggerShared(t *testing.T) {
	engine := sandboxEngine(t, "@t/a", "")
	r, e := runPlugin(t, engine, "@t/a", `Logger:info("x"); return tostring(Logger.getShared) .. tostring(Logger.new) .. tostring(JSON._encodeTable)`)
	if r != "nilnilnil" || e != "nil" {
		t.Errorf("Logger/JSON beyond their functions: r=%s e=%s", r, e)
	}
}

func TestPluginEscapeForeignTimer(t *testing.T) {
	engine := sandboxEngine(t, "@t/a", "")
	if _, err := engine.Execute(`__mt = __go_setInterval(function() end, 100000, "module")`); err != nil {
		t.Fatal(err)
	}
	before := activeTimers(engine)
	if _, e := runPlugin(t, engine, "@t/a", `for i = 1, 500 do clearTimeout(i) end`); e != "nil" {
		t.Fatal(e)
	}
	if after := activeTimers(engine); after != before {
		t.Errorf("a plugin cleared a module's timer: %d -> %d", before, after)
	}
}

// After cleanup: no poller, no registry entry, and code still reachable (a sensor a
// module kept) starts nothing.
func TestPluginEscapeAfterCleanup(t *testing.T) {
	engine := sandboxEngine(t, "@t/a", "")
	baseline := activeTimers(engine)
	if _, e := runPlugin(t, engine, "@t/a", `
		p = Plugin:new("a", {name = "A"})
		setInterval(100000, function() end)
		local poll = p:poller("x", { interval = 100000 })
		poll:onTick(function(done) done() end)
		poll:start()
		__poll = poll
		local si = setInterval
		p:sensor("s", function() si(100000, function() end); return 1 end)
	`); e != "nil" {
		t.Fatal(e)
	}
	if _, err := engine.Execute(`__kept = Plugin.getPlugin("@t/a"):get("s")`); err != nil {
		t.Fatal(err)
	}
	// what UnloadPlugin does: unload.lua, then PluginSandbox:cleanup
	if _, err := engine.Execute(`Plugin.instances["@t/a"] = nil; PluginSandbox:cleanup("@t/a")`); err != nil {
		t.Fatal(err)
	}
	if n := activeTimers(engine); n != baseline {
		t.Errorf("timers after cleanup: %d, baseline %d", n, baseline)
	}
	mustLua(t, engine, `tostring(_registry["@t/a"])`, "nil")
	engine.Execute(`pcall(function() __kept:get() end)`)
	if n := activeTimers(engine); n != baseline {
		t.Errorf("a disabled plugin started a timer: %d, baseline %d", n, baseline)
	}
}

func TestPluginEscapeEventPayload(t *testing.T) {
	engine := sandboxEngine(t, "@t/a", "")
	if _, e := runPlugin(t, engine, "@t/a", `EventBus.on("satel.OnZoneChange", function(z) __got = z.alarm; z.alarm = false end)`); e != "nil" {
		t.Fatal(e)
	}
	if _, err := engine.Execute(`
		local bus = EventBus:getShared()
		bus:on("satel.OnZoneChange", function(z) __seen = z.alarm end)
		bus:emit("satel.OnZoneChange", { alarm = true })
	`); err != nil {
		t.Fatal(err)
	}
	mustLua(t, engine, `tostring(__seen)`, "true")
	mustLua(t, engine, `tostring(PluginSandbox.sandboxes["@t/a"].environment.__got)`, "true")
}

func TestPluginEscapeExposePath(t *testing.T) {
	engine := sandboxEngine(t, "@t/a", "")
	_, e := runPlugin(t, engine, "@t/a", `
		p = Plugin:new("a", {name = "A"})
		local c = p:control("x", function() return 0 end, function(v) end)
		local h = p:expose(c, "switch", { path = "CLU220000592.DOU9999" })
		return h:getPath()
	`)
	if e != "nil" {
		t.Fatalf("expose: %s", e)
	}
	mustLua(t, engine, `tostring(_registry["CLU220000592.DOU9999"])`, "nil")
	mustLua(t, engine, `tostring(_registry["@t/a:CLU220000592.DOU9999"] ~= nil)`, "true")
}
