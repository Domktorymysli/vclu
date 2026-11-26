package lua

import (
	"strings"
	"testing"
	"time"
)

// The module↔plugin boundary of plugin API 2: plain data crosses as copies, objects with
// functions as handles, module functions a plugin calls run later. One Lua state per
// plugin will keep this contract, so plugins written today keep working.

const boundaryPlugin = `
	p = Plugin:new("a", {name = "A", version = "1.2.3"})
	p.config = { token = "s3cret" }
	local kept, watchers, objs = nil, {}, {}
	p:sensor("temp", function() return __temp or 20 end)
	function p:keep(t) kept = t; return t end
	function p:kept() return kept end
	function p:make(name)
		if objs[name] then return objs[name] end
		local o = { n = 1, label = name, _secret = "inside" }
		function o:inc(by) self.n = self.n + (by or 1); return self.n end
		function o:me() return self end
		objs[name] = o
		return o
	end
	function p:watch(fn) watchers[#watchers + 1] = fn end
	function p:fire(v) for _, f in ipairs(watchers) do f(v, { n = v }) end end
	function p:use(o) return o:getValue(), (pcall(o.switchOn, o)) end
	function p:take(h) return type(h) end
	function p:ping(x) return x end
`

func boundaryEngine(t *testing.T) *LuaEngine {
	t.Helper()
	engine := sandboxEngine(t, "@t/a", "")
	if _, e := runPlugin(t, engine, "@t/a", boundaryPlugin); e != "nil" {
		t.Fatal(e)
	}
	return engine
}

func TestBoundaryDataIsCopied(t *testing.T) {
	engine := boundaryEngine(t)
	mustLua(t, engine, `
		local pl = Plugin.getPlugin("@t/a")
		local t = { list = { 1, 2 }, name = "x" }
		local back = pl:keep(t)
		t.name = "changed"; t.list[1] = 99
		back.name = "mine"
		local inside = pl:kept()
		return inside.name .. inside.list[1] .. tostring(back ~= t)`, "x1true")
	// a cycle or a function in plain data from a module is not plain data
	mustLua(t, engine, `
		local t = {}; t.self = t
		local ok, e = pcall(function() return Plugin.getPlugin("@t/a"):keep(t) end)
		return tostring(ok) .. " " .. tostring(e):match("cycle")`, "false cycle")
}

func TestBoundaryHandles(t *testing.T) {
	engine := boundaryEngine(t)
	mustLua(t, engine, `
		local pl = Plugin.getPlugin("@t/a")
		local h = pl:make("lamp")
		local keys = 0
		for _ in pairs(h) do keys = keys + 1 end
		return h:inc() .. h:inc(5) .. " " .. h.label .. " " .. tostring(h._secret) .. " " .. keys
			.. " " .. tostring(pl:make("lamp") == h) .. " " .. tostring(h:me() == h)
			.. " " .. tostring(tostring(h):match("^<@t/a handle %d+>$") ~= nil)`, "27 lamp nil 0 true true true")
	// what a module sets stays on its handle
	mustLua(t, engine, `
		local h = Plugin.getPlugin("@t/a"):make("lamp")
		h.n = 100
		return tostring(h:inc())`, "8")
}

func TestBoundaryFacade(t *testing.T) {
	engine := boundaryEngine(t)
	mustLua(t, engine, `
		local pl = Plugin.getPlugin("@t/a")
		local ok, e = pcall(function() return pl:kvGet("x") end)
		local ok2 = pcall(function() pl.id = "@t/b" end)
		return pl.id .. " " .. pl.version .. " " .. tostring(pl.config) .. " " .. tostring(pl._sandbox)
			.. " " .. tostring(ok) .. " " .. tostring(tostring(e):match("plugin's own") ~= nil) .. " " .. tostring(ok2)
			.. " " .. tostring(_:get("@t/a") == pl) .. " " .. tostring(pl)`,
		"@t/a 1.2.3 nil nil false true false true <plugin @t/a>")
	// the sensor a module exposes is a handle; its value and type read as before
	mustLua(t, engine, `
		local s = Plugin.getPlugin("@t/a"):get("temp")
		return tostring(s:get()) .. " " .. s._type .. " " .. tostring(s._getter)`, "20 sensor nil")
}

func TestBoundaryCallbacksRunLater(t *testing.T) {
	engine := boundaryEngine(t)
	engine.StartTimers()
	mustLua(t, engine, `
		__got = {}
		local pl = Plugin.getPlugin("@t/a")
		pl:watch(function(v, t) __got[#__got + 1] = v .. ":" .. t.n end)
		pl:fire(1); pl:fire(2)
		return tostring(#__got)`, "0")
	pumpTimers(engine, 200*time.Millisecond)
	mustLua(t, engine, `return table.concat(__got, ",")`, "1:1,2:2")
}

func TestBoundaryOnChangeUnsubscribe(t *testing.T) {
	engine := boundaryEngine(t)
	engine.StartTimers()
	mustLua(t, engine, `
		__seen = {}
		local s = Plugin.getPlugin("@t/a"):get("temp")
		__off = s:onChange(function(v) __seen[#__seen + 1] = v end)
		return type(__off)`, "function")
	if _, e := runPlugin(t, engine, "@t/a", `__temp = 21; p:get("temp"):_notify()`); e != "nil" {
		t.Fatal(e)
	}
	pumpTimers(engine, 200*time.Millisecond)
	mustLua(t, engine, `__off(); return table.concat(__seen, ",")`, "21")
	if _, e := runPlugin(t, engine, "@t/a", `__temp = 22; p:get("temp"):_notify()`); e != "nil" {
		t.Fatal(e)
	}
	pumpTimers(engine, 200*time.Millisecond)
	mustLua(t, engine, `return table.concat(__seen, ",")`, "21")
}

func TestBoundaryStoppedPlugin(t *testing.T) {
	engine := boundaryEngine(t)
	mustLua(t, engine, `__pl = Plugin.getPlugin("@t/a"); __h = __pl:make("x"); __f = __pl.ping; return tostring(__h:inc())`, "2")
	if _, err := engine.Execute(`PluginSandbox:cleanup("@t/a")`); err != nil {
		t.Fatal(err)
	}
	for _, code := range []string{`__h:inc()`, `__pl:make("y")`, `__pl:get("temp")`} {
		out, err := engine.Execute(`local ok, e = pcall(function() return ` + code + ` end); return tostring(ok) .. " " .. tostring(e)`)
		if err != nil || !strings.HasPrefix(out, "false") || !strings.Contains(out, "@t/a: stopped") {
			t.Errorf("%s after the plugin stopped: %q %v", code, out, err)
		}
	}
	// the facade's name stays readable: a module can say which plugin is gone
	mustLua(t, engine, `return __pl.id`, "@t/a")
}

func TestBoundaryHouseObjectReadOnly(t *testing.T) {
	engine := boundaryEngine(t)
	mustLua(t, engine, `
		local Dout = {}
		Dout.__index = Dout
		function Dout:getValue() return 1 end
		function Dout:switchOn() self.on = true end
		__dout = setmetatable({}, Dout)
		local v, switched = Plugin.getPlugin("@t/a"):use(__dout)
		return tostring(v) .. " " .. tostring(switched) .. " " .. tostring(__dout.on)`, "1 false nil")
}

func TestBoundaryOtherPluginsObject(t *testing.T) {
	engine := boundaryEngine(t)
	if _, err := engine.Execute(`PluginSandbox:create("@t/b", nil)`); err != nil {
		t.Fatal(err)
	}
	if _, e := runPlugin(t, engine, "@t/b", `q = Plugin:new("b", {}); function q:take(v) return type(v) end`); e != "nil" {
		t.Fatal(e)
	}
	mustLua(t, engine, `
		local h = Plugin.getPlugin("@t/a"):make("lamp")
		local ok, e = pcall(function() return Plugin.getPlugin("@t/b"):take(h) end)
		local own = Plugin.getPlugin("@t/a"):take(h)
		return tostring(ok) .. " " .. tostring(tostring(e):match("give its path") ~= nil) .. " " .. own`, "false true table")
}

// A method through the facade against the same call on the instance itself.
func BenchmarkBoundaryCall(b *testing.B) {
	for _, bc := range []struct{ name, target string }{
		{"facade", `Plugin.getPlugin("@t/a")`},
		{"instance", `Plugin.instances["@t/a"]`},
	} {
		b.Run(bc.name, func(b *testing.B) {
			engine := NewLuaEngine()
			defer engine.Close()
			if _, err := engine.Execute(`PluginSandbox:create("@t/a", nil)`); err != nil {
				b.Fatal(err)
			}
			if _, err := engine.Execute(`PluginSandbox:execute("@t/a", [==[` + boundaryPlugin + `]==])`); err != nil {
				b.Fatal(err)
			}
			if _, err := engine.Execute(`__t = ` + bc.target + `; __o = __t:make("x")`); err != nil {
				b.Fatal(err)
			}
			b.ResetTimer()
			if _, err := engine.Execute(`for i = 1, ` + itoa(b.N) + ` do __t:ping(i); __o:inc() end`); err != nil {
				b.Fatal(err)
			}
		})
	}
}

func itoa(n int) string {
	s := ""
	if n == 0 {
		return "0"
	}
	for n > 0 {
		s = string(rune('0'+n%10)) + s
		n /= 10
	}
	return s
}
