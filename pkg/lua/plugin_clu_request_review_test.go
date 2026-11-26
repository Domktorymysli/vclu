package lua

import (
	"strings"
	"testing"
)

// Review of the CLU request check (#205): ways around it and what it must not break.

func cluEngine(t *testing.T) *LuaEngine {
	t.Helper()
	engine := budgetEngine(t, "@t/a")
	if _, err := engine.Execute(`
		__calls = {}
		local clu = { execute = function(_, m, s) __calls[#__calls + 1] = s; return 0 end,
			executeNoReply = function(self, m, s) PluginSandbox.checkCluRequest(); __calls[#__calls + 1] = s end }
		_registry["CLU2.DOU1"] = RemoteObject:new(clu, "DOU1", "DOU1")
	`); err != nil {
		t.Fatal(err)
	}
	return engine
}

// A plugin stopped at its time limit inside a read leaves no caller behind: modules still reach the CLU.
func TestReviewCluCheckAfterBudgetStop(t *testing.T) {
	engine := cluEngine(t)
	_, e := runPlugin(t, engine, "@t/a", `
		local o = _:get("CLU2.DOU1")
		while true do o:getInfo() end
	`)
	if !strings.Contains(e, "callback over") {
		t.Fatalf("not stopped: %s", e)
	}
	got, err := engine.Execute(`
		local ok, e = pcall(function() return _registry["CLU2.DOU1"]:get(1) end)
		return tostring(ok) .. "|" .. tostring(e)`)
	if err != nil || !strings.HasPrefix(got, "true") {
		t.Errorf("module after the stop: %q %v", got, err)
	}
}

// A module's listener the plugin's event runs is the module's code, also when it ends in a
// tail call to the CLU.
func TestReviewCluCheckModuleTailCallFromPluginEvent(t *testing.T) {
	engine := cluEngine(t)
	if _, err := engine.Execute(`
		local f = assert(loadstring([[
			__seen = {}
			EventBus:getShared():on("@t/a:x", function(v)
				__seen[#__seen + 1] = "plain"
				_registry["CLU2.DOU1"]:execute(1)
			end)
			EventBus:getShared():on("@t/a:x", function(v)
				__seen[#__seen + 1] = "tail"
				return _registry["CLU2.DOU1"]:execute(2)
			end)
		]], "modules/m.lua"))
		f()
	`); err != nil {
		t.Fatal(err)
	}
	if _, e := runPlugin(t, engine, "@t/a", `
		p = Plugin:new("a", {name = "A"})
		p:emit("@t/a:x", 1)
	`); e != "nil" {
		t.Logf("emit: %s", e)
	}
	mustLua(t, engine, `table.concat(__seen, ",") .. "|" .. table.concat(__calls, ";")`, "plain,tail|DOU1:execute(1);DOU1:execute(2)")
}

// A proxy another plugin holds is read as the plugin that reaches it, not as its first owner.
func TestReviewCluCheckProxyOfAnotherPlugin(t *testing.T) {
	engine := cluEngine(t)
	mustLua(t, engine, `PluginSandbox:create("@t/raw", {api = 2, grenton_raw = true}) and "ok"`, "ok")
	if _, e := runPlugin(t, engine, "@t/raw", `
		local p = Plugin:new("raw", {name = "Raw"})
		local s = p:sensor("dou", function() return 1 end)
		s.src = _:get("CLU2.DOU1")
	`); e != "nil" {
		t.Fatal(e)
	}
	_, e := runPlugin(t, engine, "@t/a", `
		local s = Plugin.getPlugin("@t/raw"):get("dou")
		return s.src:get(27)
	`)
	if !strings.Contains(e, "grenton_raw") {
		t.Errorf("read through the other plugin's proxy: %q", e)
	}
	mustLua(t, engine, `table.concat(__calls, ";")`, "")
}

// JSON of an object a plugin did not create carries no _fields either.
func TestReviewHiddenFieldsThroughJSON(t *testing.T) {
	engine := sandboxEngine(t, "@t/a", "")
	if _, err := engine.Execute(`_registry["mod.lampa"] = { name = "lampa", _secret = "s3", sub = { _pin = "1234", x = 1 } }`); err != nil {
		t.Fatal(err)
	}
	r, e := runPlugin(t, engine, "@t/a", `
		local o = _:get("mod.lampa")
		local a = JSON:decode(JSON:encode(o))
		local b = JSON:decode(JSON:encode({ wrapped = o.sub }))
		return tostring(a._secret) .. "|" .. tostring(a.sub and a.sub._pin) .. "|" .. tostring(b.wrapped._pin) .. "|" .. tostring(a.name) .. "|" .. tostring(b.wrapped.x)
	`)
	if e != "nil" || r != "nil|nil|nil|lampa|1" {
		t.Errorf("r=%q e=%q", r, e)
	}
}

// What a module calls getExposed on is the core's, not a function the plugin put there.
func TestReviewGetExposedIsTheCores(t *testing.T) {
	engine := sandboxEngine(t, "@t/a", "")
	_, e := runPlugin(t, engine, "@t/a", `
		p = Plugin:new("a", {name = "A"})
		local c = p:control("x", function() return 1 end, function(v) end)
		p:expose(c, "switch", { id = "x" })
		p.getExposed = function() return { { pin = function(_, v) __stolen = v end, remote = function(s) return s end } } end
	`)
	t.Logf("set: %s", e)
	mustLua(t, engine, `
		local h = Plugin.getPlugin("@t/a"):getExposed()[1]
		h:remote(); h:pin("1234")
		return tostring(h._pin)`, "1234")
}

// Every way a plugin reaches a read that asks the CLU is refused, and leaves nothing behind.
func TestReviewCluCheckWaysIn(t *testing.T) {
	engine := cluEngine(t)
	if _, err := engine.Execute(`
		local clu = _registry["CLU2.DOU1"]._clu
		_registry["CLU2.AOU1"] = RemoteNumber:new(clu, "AOU1", "AOU1")
		_registry["CLU2"] = CLU_REMOTE:new("CLU2", 3232235521, EventBus:getShared())
		local f = assert(loadstring([[
			local m = { name = "heat" }
			function m:getTemp() return _registry["CLU2.DOU1"]:get(5) end
			_registry["mod.heat"] = m
		]], "modules/heat.lua"))
		f()
	`); err != nil {
		t.Fatal(err)
	}
	for what, code := range map[string]string{
		"method reference": `local o = _:get("CLU2.DOU1"); local f = o.get; return f(o, 27)`,
		"pcall":            `local o = _:get("CLU2.DOU1"); local ok, e = pcall(o.get, o, 27); error(e, 0)`,
		"string key":       `local o = _:get("CLU2.DOU1"); return o["get"](o, 27)`,
		"getMin":           `return _:get("CLU2.AOU1"):getMin()`,
		"registry table":   `return _registry["CLU2.DOU1"]:get(27)`,
		"CLU_REMOTE get":   `return _:get("CLU2"):get("temp")`,
		"module's getter":  `return _:get("mod.heat"):getTemp()`,
		"unpack":           `local t = { _:get("CLU2.DOU1") }; local o = unpack(t); return o:get(27)`,
	} {
		_, e := runPlugin(t, engine, "@t/a", code)
		if !strings.Contains(e, "grenton_raw") {
			t.Errorf("%s: %q", what, e)
		}
	}
	mustLua(t, engine, `table.concat(__calls, ";")`, "")
	// a refused or failing read leaves the module's requests alone
	runPlugin(t, engine, "@t/a", `local o = _:get("CLU2.DOU1"); pcall(o.get, o, "x"); pcall(o.get, o, 1)`)
	mustLua(t, engine, `_registry["CLU2.DOU1"]:get(4); return table.concat(__calls, ";")`, "DOU1:get(4)")
	// state vclu knows stays readable
	if r, e := runPlugin(t, engine, "@t/a", `return tostring(_:get("CLU2.AOU1"):getValue()) .. "|" .. _:get("CLU2.DOU1"):getInfo().id`); e != "nil" || r != "nil|DOU1" {
		t.Errorf("known state: r=%q e=%q", r, e)
	}
}

// An object a module hands to a plugin: the plugin's request is still the plugin's.
func TestReviewCluCheckObjectFromAModule(t *testing.T) {
	engine := cluEngine(t)
	if _, e := runPlugin(t, engine, "@t/a", `
		p = Plugin:new("a", {name = "A"})
		function p:drive(o) o:execute(1) end
		function p:read(o) local v = o:get(2) return v end
	`); e != "nil" {
		t.Fatal(e)
	}
	got, err := engine.Execute(`
		local p = Plugin.getPlugin("@t/a")
		local o = _registry["CLU2.DOU1"]
		local ok1, e1 = pcall(function() p:drive(o) end)
		local ok2, e2 = pcall(function() p:read(o) end)
		return tostring(ok1) .. tostring(ok2) .. "|" .. table.concat(__calls, ";")`)
	if err != nil || got != "falsefalse|" {
		t.Errorf("got %q %v", got, err)
	}
}
