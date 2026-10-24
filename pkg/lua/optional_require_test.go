package lua

import "testing"

// optional(name) is require for a module that may be missing: a module that is not there, or
// gives nothing (its plugin is not installed, so require gives true), is nil without a word; a
// module that is there and fails while loading is nil with its error in the log.
func TestOptionalRequire(t *testing.T) {
	serverDir := createTestModulesDir(t, map[string]string{
		"init.lua": `
			_warns = {}
			Logger.warn = function(self, msg, ...) _warns[#_warns + 1] = self:format(msg, ...) end
			_got = {
				table = optional("energia/power"),
				fn = optional("jedna_funkcja"),
				nothing = optional("pogoda"),
				missing = optional("nie/ma"),
				broken = optional("zepsuty"),
				inner = optional("potrzebuje_brakujacego"),
			}`,
		"energia/power.lua":               `return { totalPower = function() return 1200 end }`,
		"jedna_funkcja/init.lua":          `return function() return 'ok' end`,
		"pogoda/init.lua":                 `local w = nil; if not w then return end; return { temp = 1 }`,
		"zepsuty/init.lua":                `local x = nil; return x.pole`,
		"potrzebuje_brakujacego/init.lua": `local m = require("tez/nie/ma"); return m`,
	})
	engine := NewLuaEngine()
	defer engine.Close()
	// in subdirectories, so the loader leaves them to require
	for _, r := range engine.LoadUserModules(serverDir) {
		if !r.OK {
			t.Fatalf("%s: %s", r.Name, r.Error)
		}
	}

	luaOK(t, engine, `
		assert(_got.table.totalPower() == 1200, "a module's table")
		assert(type(_got.fn) == "function" and _got.fn() == "ok", "a module that returns a function")
		assert(_got.nothing == nil, "a module that returns nothing: nil, not true")
		assert(_got.missing == nil, "a missing module: nil")
		assert(_got.broken == nil, "a failing module: nil")
		assert(_got.inner == nil, "a module that requires a missing one: nil")
	`)
	// one warning each for the module that fails and the one whose require fails inside it
	luaOK(t, engine, `
		assert(#_warns == 2, "warnings: " .. table.concat(_warns, " | "))
		assert(_warns[1]:find('optional("zepsuty")', 1, true), _warns[1])
		assert(_warns[2]:find('optional("potrzebuje_brakujacego")', 1, true) and _warns[2]:find("tez/nie/ma", 1, true), _warns[2])
	`)
}

// The not-found test matches the whole name: "a" that requires a missing "a/b" (or "c" that
// requires "c.d") fails, it is not "not there"; a missing dotted name stays silent.
func TestOptionalRequireNameMatching(t *testing.T) {
	serverDir := createTestModulesDir(t, map[string]string{
		"init.lua": `
			_warns = {}
			Logger.warn = function(self, msg, ...) _warns[#_warns + 1] = self:format(msg, ...) end
			_got = {
				slash = optional("a"),
				dot = optional("c"),
				dotted = optional("energia.power"),
				dottedMissing = optional("nie.ma"),
			}`,
		"a/init.lua":        `return require("a/b")`,
		"c/init.lua":        `return require("c.d")`,
		"energia/power.lua": `return { kw = 1.2 }`,
	})
	engine := NewLuaEngine()
	defer engine.Close()
	for _, r := range engine.LoadUserModules(serverDir) {
		if !r.OK {
			t.Fatalf("%s: %s", r.Name, r.Error)
		}
	}
	luaOK(t, engine, `
		assert(_got.slash == nil and _got.dot == nil, "failing modules: nil")
		assert(_got.dotted and _got.dotted.kw == 1.2, "a dotted name loads like require")
		assert(_got.dottedMissing == nil, "a missing dotted name: nil")
		assert(#_warns == 2, "warnings: " .. table.concat(_warns, " | "))
		assert(_warns[1]:find('optional("a")', 1, true) and _warns[1]:find("a/b", 1, true), _warns[1])
		assert(_warns[2]:find('optional("c")', 1, true) and _warns[2]:find("c.d", 1, true), _warns[2])
	`)
}

// A second optional() of a failing module does not run it again (require keeps gopher-lua's
// sentinel) but gives nil and warns again; a missing module stays silent every time.
func TestOptionalRequireRepeated(t *testing.T) {
	serverDir := createTestModulesDir(t, map[string]string{
		"init.lua": `
			_runs = { n = 0 }
			_warns = {}
			Logger.warn = function(self, msg, ...) _warns[#_warns + 1] = self:format(msg, ...) end
			_got = { optional("zepsuty"), optional("zepsuty"), optional("nie/ma"), optional("nie/ma") }`,
		"zepsuty/init.lua": `_runs.n = _runs.n + 1; local x = nil; return x.pole`,
	})
	engine := NewLuaEngine()
	defer engine.Close()
	engine.LoadUserModules(serverDir)
	luaOK(t, engine, `
		assert(next(_got) == nil, "all nil")
		assert(_runs.n == 1, "runs: " .. _runs.n)
		assert(#_warns == 2, "warnings: " .. table.concat(_warns, " | "))
		assert(_warns[2]:find('optional("zepsuty")', 1, true), _warns[2])
	`)
}

// A flat modules/*.lua file that fails under optional() is still run by the loader and reported
// as failed; one that loads is not run a second time.
func TestOptionalRequireFlatModuleStillReported(t *testing.T) {
	serverDir := createTestModulesDir(t, map[string]string{
		"init.lua":  `_got = optional("dobry"); _bad = optional("zly")`,
		"dobry.lua": `_dobryRuns = (_dobryRuns or 0) + 1; return { ok = true }`,
		"zly.lua":   `local x = nil; return x.pole`,
	})
	engine := NewLuaEngine()
	defer engine.Close()
	byName := map[string]ModuleLoadResult{}
	for _, r := range engine.LoadUserModules(serverDir) {
		byName[r.Name] = r
	}
	if !byName["init.lua"].OK || !byName["dobry.lua"].OK {
		t.Fatalf("results: %+v", byName)
	}
	if r := byName["zly.lua"]; r.OK || r.Error == "" {
		t.Fatalf("zly.lua must be reported as failed: %+v", r)
	}
	luaOK(t, engine, `
		assert(_got and _got.ok, "optional gives the flat module")
		assert(_dobryRuns == 1, "the loader does not run it again: " .. tostring(_dobryRuns))
		assert(_bad == nil, "a failing flat module: nil")
	`)
}

// Plugins have no require, so no optional either: their environment has no _G fallback.
func TestOptionalNotInPluginSandbox(t *testing.T) {
	engine := NewLuaEngine()
	defer engine.Close()
	luaOK(t, engine, `
		assert(type(optional) == "function", "a global in the runtime")
		PluginSandbox:create("@test/opt")
		local r, e = PluginSandbox:execute("@test/opt", [[return tostring(optional) .. "," .. tostring(require)]])
		assert(e == nil, tostring(e))
		assert(r == "nil,nil", r)
	`)
}
