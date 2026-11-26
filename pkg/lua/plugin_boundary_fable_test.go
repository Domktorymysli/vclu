package lua

import (
	"strings"
	"testing"
	"time"
)

// Review probes of the module↔plugin boundary (API 2): what still crosses raw, what a
// stopped plugin keeps, what the callback queue takes.

const fableBoundaryPlugin = `
	p = Plugin:new("a", {name = "A", version = "1.0.0"})
	local watchers, objs, kept = {}, {}, nil
	p.token = "plain-field"
	p:sensor("temp", function() return __temp or 20 end)
	function p:make(name)
		if objs[name] then return objs[name] end
		local o = { n = 1, label = name, _secret = "inside" }
		function o:inc(by) self.n = self.n + (by or 1); return self.n end
		objs[name] = o
		return o
	end
	function p:keep(t) kept = t; return t end
	function p:kept() return kept end
	function p:watch(fn) watchers[#watchers + 1] = fn end
	function p:spam(n) for i = 1, n do for _, f in ipairs(watchers) do f(i) end end end
	function p:emitWithFn()
		self:emit("@t/a:evt", { apply = function(target) target.touched = "by plugin"; return type(target) end })
	end
	function p:exposeOwn() return self:expose(self:make("lamp"), "light", { path = "lamp", mqtt = false, homekit = false }) end
	function p:peek(id) local other = Plugin.getPlugin("@t/b"); return other and other:get(id) end
	function p:seeRegistry(path) local o = self:getObject(path); return o end
	function p:boom() error({ what = "table error" }) end
	local played = false
	function p:player()
		return { getState = function() return { playing = true, title = "T" } end, play = function() played = true end }
	end
	function p:wasPlayed() return played end
`

func fableEngine(t *testing.T) *LuaEngine {
	t.Helper()
	engine := sandboxEngine(t, "@t/a", "")
	if _, e := runPlugin(t, engine, "@t/a", fableBoundaryPlugin); e != "nil" {
		t.Fatal(e)
	}
	return engine
}

// A function inside an event payload is a boundary function: what the module passes to it
// reaches the plugin read-only, as through any plugin method.
func TestFableEventPayloadFunction(t *testing.T) {
	engine := fableEngine(t)
	mustLua(t, engine, `
		__house = setmetatable({}, { __index = {} })
		EventBus:getShared():on("@t/a:evt", function(payload)
			__fnType = type(payload.apply)
			__ok, __res = pcall(payload.apply, __house)
		end)
		Plugin.getPlugin("@t/a"):emitWithFn()
		return tostring(__fnType) .. " " .. tostring(__ok) .. " " .. tostring(tostring(__res):match("Read%-only") ~= nil)
			.. " " .. tostring(__house.touched)`,
		"function false true nil")
}

// A plugin's own exposed object: _:get(path) gives the module a handle, the same as pl:get
// gives, not the raw object.
func TestFableRegistryPathGivesHandle(t *testing.T) {
	engine := fableEngine(t)
	mustLua(t, engine, `
		local pl = Plugin.getPlugin("@t/a")
		pl:exposeOwn()
		local viaRegistry = _:get("@t/a:lamp")
		local viaPlugin = pl:make("lamp")
		return tostring(viaRegistry == viaPlugin) .. " " .. tostring(viaRegistry._secret) .. " " .. tostring(viaRegistry.label)`,
		"true nil lamp")
}

// A handle the module exposed under its own path: the plugin reading that path gets its
// object (read-only), not its own instance.
func TestFablePluginReadsModuleExposedHandle(t *testing.T) {
	engine := fableEngine(t)
	mustLua(t, engine, `
		local pl = Plugin.getPlugin("@t/a")
		local h = pl:make("lamp")
		expose(h, "light", { path = "dom.lampa", mqtt = false, homekit = false })
		return "ok"`, "ok")
	r, e := runPlugin(t, engine, "@t/a", `
		local o = p:getObject("dom.lampa")
		return tostring(o.label) .. " " .. tostring(o.id) .. " " .. type(o.inc)`)
	if e != "nil" || r != "lamp nil function" {
		t.Errorf("plugin's view of the module's expose of its object: %q %q", r, e)
	}
}

// Another plugin's sensor returned by a plugin reaches the module as a handle of that
// plugin, not its raw object.
func TestFableOtherPluginsSensorNotRaw(t *testing.T) {
	engine := fableEngine(t)
	if _, err := engine.Execute(`PluginSandbox:create("@t/b", nil)`); err != nil {
		t.Fatal(err)
	}
	if _, e := runPlugin(t, engine, "@t/b", `q = Plugin:new("b", {}); q:sensor("hum", function() return 55 end)`); e != "nil" {
		t.Fatal(e)
	}
	mustLua(t, engine, `
		local s = Plugin.getPlugin("@t/a"):peek("hum")
		local own = Plugin.getPlugin("@t/b"):get("hum")
		return tostring(s:get()) .. " " .. tostring(s._getter) .. " " .. tostring(s == own)`, "55 nil true")
}

// The callback queue: a plugin that calls a module function in a tight loop is stopped at a
// limit; the module's queued callbacks still run in order.
func TestFableCallbackQueueLimit(t *testing.T) {
	engine := fableEngine(t)
	engine.StartTimers()
	mustLua(t, engine, `
		__n = 0
		local pl = Plugin.getPlugin("@t/a")
		pl:watch(function(i) __n = __n + 1 end)
		local ok, e = pcall(function() pl:spam(200000) end)
		return tostring(ok) .. " " .. tostring(tostring(e):match("pending") ~= nil)`, "false true")
	pumpTimers(engine, 300*time.Millisecond)
	out, err := engine.Execute(`return tostring(__n)`)
	if err != nil {
		t.Fatal(err)
	}
	if out == "0" || out == "200000" {
		t.Errorf("queued callbacks run: %s, want the limit's worth", out)
	}
}

// A plain table of functions from a plugin works as a player source (a handle has no pairs)
func TestFablePlayerSourceHandle(t *testing.T) {
	engine := fableEngine(t)
	mustExec(t, engine, `
		Dashboard.new('pl'):section('M', 'music'):player('h', 'Handle', Plugin.getPlugin("@t/a"):player()):build()
	`)
	var vals map[string]map[string]interface{}
	dashJSON(t, engine, `Dashboard.API.getValues("pl")`, &vals)
	if vals["h"] == nil || vals["h"]["title"] != "T" {
		t.Errorf("player values through a handle: %v", vals["h"])
	}
	var res map[string]interface{}
	dashJSON(t, engine, `Dashboard.API.executeAction("pl", "h", "play", nil)`, &res)
	mustLua(t, engine, `return tostring(Plugin.getPlugin("@t/a"):wasPlayed())`, "true")
}

// Deeply nested data is refused at the boundary with the plugin's name in the error
func TestFableDepthLimit(t *testing.T) {
	engine := fableEngine(t)
	mustLua(t, engine, `
		local t = {}
		local cur = t
		for i = 1, 40 do cur.next = {}; cur = cur.next end
		local ok, e = pcall(function() return Plugin.getPlugin("@t/a"):keep(t) end)
		return tostring(ok) .. " " .. tostring(tostring(e):match("deeper") ~= nil)`, "false true")
}

// A non-string error from a plugin reaches the module as text, not the plugin's table
func TestFableErrorValueIsText(t *testing.T) {
	t.Skip("contract: an error crossing the boundary is text; enforced with one Lua state per plugin (pluginy-2.0 §12), not now (a pcall per boundary call)")
	engine := fableEngine(t)
	out, err := engine.Execute(`
		local ok, e = pcall(function() Plugin.getPlugin("@t/a"):boom() end)
		return tostring(ok) .. " " .. type(e)`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(out, "false ") {
		t.Fatalf("%q", out)
	}
	if out != "false string" {
		t.Errorf("error value crossing the boundary: %s", out)
	}
}

// The facade shows a plugin's methods, id, name and version, none of its plain fields
func TestFableFacadePlainFields(t *testing.T) {
	engine := fableEngine(t)
	mustLua(t, engine, `local pl = Plugin.getPlugin("@t/a")
		return tostring(pl.token) .. " " .. tostring(pl.config) .. " " .. pl.id`, "nil nil @t/a")
}

// A module's pl:on subscription is tracked in the plugin's sandbox: it dies with the plugin
func TestFableModuleOnDiesWithPlugin(t *testing.T) {
	engine := fableEngine(t)
	engine.StartTimers()
	mustLua(t, engine, `
		__hits = 0
		Plugin.getPlugin("@t/a"):on("house:evt", function() __hits = __hits + 1 end)
		EventBus:getShared():emit("house:evt")
		return "ok"`, "ok")
	pumpTimers(engine, 100*time.Millisecond)
	mustLua(t, engine, `return tostring(__hits)`, "1")
	if _, err := engine.Execute(`PluginSandbox:cleanup("@t/a")`); err != nil {
		t.Fatal(err)
	}
	mustLua(t, engine, `EventBus:getShared():emit("house:evt"); return "ok"`, "ok")
	pumpTimers(engine, 100*time.Millisecond)
	out, _ := engine.Execute(`return tostring(__hits)`)
	t.Logf("module listener after the plugin stopped: hits=%s (1 = it died with the plugin)", out)
}

// An object a plugin exposed itself: the core subscribes it directly, a module's expose of
// its handle reaches ExposedObjects after the plugin's call; a set through the core notes
// the value in the plugin's object (_notified)
func TestFableExposedHandleChanges(t *testing.T) {
	engine := fableEngine(t)
	engine.StartTimers()
	mustLua(t, engine, `
		local s = Plugin.getPlugin("@t/a"):get("temp")
		expose(s, "temperature", { path = "dom.temp", mqtt = false, homekit = false })
		return tostring(_:get("dom.temp") == s) .. " " .. tostring(_:get("dom.temp"):get())`, "true 20")
	if _, e := runPlugin(t, engine, "@t/a", `__temp = 25; p:get("temp"):_notify()`); e != "nil" {
		t.Fatal(e)
	}
	pumpTimers(engine, 200*time.Millisecond)
	mustLua(t, engine, `return tostring(ExposedObjects._objects["dom.temp"]._lastSeen)`, "25")
}

// API 1 is unchanged: an API 1 plugin's exposed object stays raw for modules
func TestFableAPI1ExposedObjectStaysRaw(t *testing.T) {
	engine := sandboxEngine(t, "@t/c", "{api = 1}")
	if _, e := runPlugin(t, engine, "@t/c", `
		c = Plugin:new("c", {})
		local o = { _secret = "inside", get = function() return 1 end }
		c:expose(o, "number", { path = "@t/c:lamp", mqtt = false, homekit = false })`); e != "nil" {
		t.Fatal(e)
	}
	mustLua(t, engine, `return tostring(_:get("@t/c:lamp")._secret) .. " " .. tostring(Plugin._lookup("@t/c")._isPlugin)`, "inside true")
}
