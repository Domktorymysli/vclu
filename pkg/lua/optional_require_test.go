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
