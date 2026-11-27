package lua

import (
	"strings"
	"testing"
)

// Plugin dashboards: the plugin builds them, the user's module grants who opens them.

const accessPlugin = `
local p = Plugin:new("zakupy-dom", {})
p:dashboard("lista"):icon("cart"):section("L"):button("b", "B", "x", function() end):build()
`

func accessEngine(t *testing.T) *LuaEngine {
	t.Helper()
	engine := sandboxEngine(t, "@jan/zakupy-dom", appsDeclared)
	if _, e := runPlugin(t, engine, "@jan/zakupy-dom", accessPlugin); e != "nil" {
		t.Fatalf("plugin: %s", e)
	}
	return engine
}

// module runs code as a module would (the core's environment); "" when it went through.
func module(t *testing.T, engine *LuaEngine, code string) string {
	t.Helper()
	out, err := engine.Execute(`local ok, e = pcall(function()
` + code + `
end)
return ok and "" or tostring(e)`)
	if err != nil {
		t.Fatalf("module: %v", err)
	}
	return out
}

func TestPluginDashboardMustBeDeclared(t *testing.T) {
	engine := sandboxEngine(t, "@jan/a", `{dashboards = {lista = {name = "Lista"}}}`)
	_, e := runPlugin(t, engine, "@jan/a", `Plugin:new("a", {}):dashboard("inna")`)
	if !strings.Contains(e, `declare it in plugin.json "dashboards"`) {
		t.Fatalf("undeclared on API 2: %s", e)
	}
	if _, e := runPlugin(t, engine, "@jan/a", `Plugin.getPlugin("@jan/a"):dashboard("lista"):section("S"):build()`); e != "nil" {
		t.Fatalf("declared: %s", e)
	}
	// the manifest's name is the default
	mustLua(t, engine, `_dashboards["a_lista"]._name`, "Lista")

	// API 1 keeps the contract from before: any id
	old := sandboxEngine(t, "@jan/b", `{api = 1}`)
	if _, e := runPlugin(t, old, "@jan/b", `Plugin:new("b", {}):dashboard("inna"):section("S"):build()`); e != "nil" {
		t.Fatalf("API 1: %s", e)
	}
}

func TestPluginDashboardCannotWidenItself(t *testing.T) {
	engine := accessEngine(t)
	for what, code := range map[string]string{
		"auth public":       `Plugin.getPlugin("@jan/zakupy-dom"):dashboard("a"):auth("public")`,
		"auth other":        `Plugin.getPlugin("@jan/zakupy-dom"):dashboard("a"):auth("none")`,
		"auth nil":          `Plugin.getPlugin("@jan/zakupy-dom"):dashboard("a"):auth()`,
		"pin":               `Plugin.getPlugin("@jan/zakupy-dom"):dashboard("a"):pin("1234")`,
		"remote":            `Plugin.getPlugin("@jan/zakupy-dom"):dashboard("a"):remote()`,
		"remote false":      `Plugin.getPlugin("@jan/zakupy-dom"):dashboard("a"):remote(false)`,
		"roles":             `Plugin.getPlugin("@jan/zakupy-dom"):dashboard("a"):roles("x")`,
		"section remote":    `Plugin.getPlugin("@jan/zakupy-dom"):dashboard("a"):section("S"):remote()`,
		"section roles":     `Plugin.getPlugin("@jan/zakupy-dom"):dashboard("a"):section("S"):roles("x")`,
		"grant itself":      `Plugin.getPlugin("@jan/zakupy-dom"):dashboardAccess("lista", {auth = "public"})`,
		"grant by field":    `local p = Plugin.getPlugin("@jan/zakupy-dom"); p.dashboardAccess(p, "lista", {auth = "public"})`,
		"grant by class":    `Plugin.dashboardAccess(Plugin.getPlugin("@jan/zakupy-dom"), "lista", {auth = "public"})`,
		"grant by registry": `_registry["@jan/zakupy-dom"]:dashboardAccess("lista", {auth = "public"})`,
		"hijack the call":   `Plugin.getPlugin("@jan/zakupy-dom").dashboardAccess = function(_, _, o) __stolen = o end`,
	} {
		r, e := runPlugin(t, engine, "@jan/zakupy-dom", code)
		wantRefused(t, what, r, e)
		if strings.Contains(e, "Dashboard.lua") || strings.Contains(e, "PluginSandbox.lua") {
			t.Errorf("%s: the error names the core: %s", what, e)
		}
	}
	_, e := runPlugin(t, engine, "@jan/zakupy-dom", `Plugin.getPlugin("@jan/zakupy-dom"):dashboard("a"):remote()`)
	if !strings.Contains(e, `:dashboard("a"):remote()`) {
		t.Errorf("the refusal says where the user grants it: %s", e)
	}
	mustLua(t, engine, `_dashboards["zakupy_dom_lista"]._auth`, "session")
	mustLua(t, engine, `tostring(_dashboards["zakupy_dom_lista"]._remote)`, "nil")

	// another plugin cannot grant itself access to this one's either
	luaOut(t, engine, `PluginSandbox:create("@ola/inny", nil)`)
	r, e := runPlugin(t, engine, "@ola/inny", `Plugin:new("inny", {})
		Plugin.getPlugin("@jan/zakupy-dom"):dashboardAccess("lista", {auth = "public"})`)
	wantRefused(t, "another plugin", r, e)
	mustLua(t, engine, `_dashboards["zakupy_dom_lista"]._auth`, "session")
	mustLua(t, engine, `tostring(next(_dashboardAccess))`, "nil")

	// the module's call still reaches the core: the plugin neither sees the PIN nor skips "off"
	if e := module(t, engine, `Plugin.getPlugin("@jan/zakupy-dom"):dashboardAccess("lista", { auth = "pin", pin = "1234" })`); e != "" {
		t.Fatal(e)
	}
	mustLua(t, engine, `tostring(rawget(Plugin.instances["@jan/zakupy-dom"], "dashboardAccess")) .. _dashboards["zakupy_dom_lista"]._auth`, "nilpin")
}

func TestPluginDashboardAccessFromModule(t *testing.T) {
	engine := accessEngine(t)
	if e := module(t, engine, `Plugin.getPlugin("@jan/zakupy-dom"):dashboardAccess("lista", {
		name = "Kuchnia", auth = "public", remote = { pin = true, roles = { "domownicy", "gosia" } }, pin = "1234" })`); e != "" {
		t.Fatal(e)
	}
	// applied to the built dashboard at once
	mustLua(t, engine, `_dashboards["zakupy_dom_lista"]._auth`, "public")
	mustLua(t, engine, `_dashboards["zakupy_dom_lista"]._name`, "Kuchnia")
	remote := luaOut(t, engine, `Dashboard.API.remoteList()`)
	if !strings.Contains(remote, `"roles":["domownicy","gosia"]`) || !strings.Contains(remote, `"pin":true`) {
		t.Fatalf("remote list: %s", remote)
	}
	if out := luaOut(t, engine, `Dashboard.API.getAuth("zakupy_dom_lista", "domownicy")`); !strings.Contains(out, `"auth":"pin"`) {
		t.Fatalf("remote auth: %s", out)
	}
	if out := luaOut(t, engine, `Dashboard.API.verifyPin("zakupy_dom_lista", "1234", "domownicy")`); !strings.Contains(out, `"valid":true`) {
		t.Fatalf("pin: %s", out)
	}

	// the plugin's own name and auth later stay under the user's
	if _, e := runPlugin(t, engine, "@jan/zakupy-dom", `
		Plugin.getPlugin("@jan/zakupy-dom"):dashboard("lista"):name("Mine"):auth("pin"):section("L"):build()`); e != "nil" {
		t.Fatal(e)
	}
	mustLua(t, engine, `_dashboards["zakupy_dom_lista"]._auth .. " " .. _dashboards["zakupy_dom_lista"]._name`, "public Kuchnia")

	// a plugin reload keeps the grant: built again, it gets it
	luaOut(t, engine, `PluginSandbox:cleanup("@jan/zakupy-dom")`)
	mustLua(t, engine, `tostring(_dashboards["zakupy_dom_lista"])`, "nil")
	luaOut(t, engine, `PluginSandbox:create("@jan/zakupy-dom", `+appsDeclared+`)`)
	if _, e := runPlugin(t, engine, "@jan/zakupy-dom", accessPlugin); e != "nil" {
		t.Fatal(e)
	}
	mustLua(t, engine, `_dashboards["zakupy_dom_lista"]._auth`, "public")

	// a grant replaces the previous one whole: what it leaves out is the plugin's again
	module(t, engine, `Plugin.getPlugin("@jan/zakupy-dom"):dashboardAccess("lista", { auth = "pin", pin = "0000" })`)
	mustLua(t, engine, `_dashboards["zakupy_dom_lista"]._name .. " " .. tostring(_dashboards["zakupy_dom_lista"]._remote)`, "Zakupy nil")
	if out := luaOut(t, engine, `Dashboard.API.remoteList()`); out != "[]" && out != "{}" {
		t.Fatalf("no longer remote: %s", out)
	}

	// granted before the plugin builds: the build gets it
	module(t, engine, `Plugin.getPlugin("@jan/zakupy-dom"):dashboardAccess("b", { auth = "public", remote = true })`)
	runPlugin(t, engine, "@jan/zakupy-dom", `Plugin.getPlugin("@jan/zakupy-dom"):dashboard("b"):section("S"):build()`)
	mustLua(t, engine, `_dashboards["zakupy_dom_b"]._auth`, "public")
	if out := luaOut(t, engine, `Dashboard.API.remoteList()`); !strings.Contains(out, `"id":"zakupy_dom_b"`) || !strings.Contains(out, `"roles":[]`) && !strings.Contains(out, `"roles":{}`) {
		t.Fatalf("remote for every member: %s", out)
	}

	// roles from a function, as Dashboard's :roles
	module(t, engine, `Plugin.getPlugin("@jan/zakupy-dom"):dashboardAccess("b", { remote = { roles = function() return { "sprzatanie" } end } })`)
	if out := luaOut(t, engine, `Dashboard.API.remoteList()`); !strings.Contains(out, `"sprzatanie"`) {
		t.Fatalf("roles from a function: %s", out)
	}
}

func TestPluginDashboardAccessOff(t *testing.T) {
	engine := accessEngine(t)
	module(t, engine, `Plugin.getPlugin("@jan/zakupy-dom"):dashboardAccess("lista", { auth = "off", remote = true })`)
	if out := luaOut(t, engine, `Dashboard.API.getDefinition("zakupy_dom_lista")`); !strings.Contains(out, "not found") {
		t.Fatalf("off is not served: %s", out)
	}
	if out := luaOut(t, engine, `Dashboard.API.getAuth("zakupy_dom_lista")`); !strings.Contains(out, "not found") {
		t.Fatalf("off auth: %s", out)
	}
	if out := luaOut(t, engine, `Dashboard.API.list()`); strings.Contains(out, "zakupy_dom_lista") {
		t.Fatalf("off is not listed: %s", out)
	}
	if out := luaOut(t, engine, `Dashboard.API.remoteList()`); strings.Contains(out, "zakupy_dom_lista") {
		t.Fatalf("off is not remote: %s", out)
	}
	// the id stays the plugin's: a module cannot take it while it is off
	_, e := runPlugin(t, engine, "@jan/zakupy-dom", `Plugin.getPlugin("@jan/zakupy-dom"):dashboard("lista"):section("S"):build()`)
	if e != "nil" {
		t.Fatalf("rebuild while off: %s", e)
	}
	module(t, engine, `Plugin.getPlugin("@jan/zakupy-dom"):dashboardAccess("lista", { auth = "session" })`)
	if out := luaOut(t, engine, `Dashboard.API.getAuth("zakupy_dom_lista")`); !strings.Contains(out, `"auth":"session"`) {
		t.Fatalf("on again: %s", out)
	}
}

func TestPluginDashboardAccessValidation(t *testing.T) {
	engine := accessEngine(t)
	for what, c := range map[string]struct{ opts, want string }{
		"undeclared":     {`"inna", {}`, "declares no dashboard inna"},
		"not a string":   {`5, {}`, "declares no dashboard 5"},
		"no opts":        {`"lista"`, "opts is a table"},
		"unknown option": {`"lista", { roles = {"a"} }`, `unknown option "roles"`},
		"auth":           {`"lista", { auth = "everyone" }`, `auth is "session"`},
		"name type":      {`"lista", { name = 5 }`, "name is a text"},
		"name empty":     {`"lista", { name = "" }`, "name is a text"},
		"name long":      {`"lista", { name = string.rep("ż", 61) }`, "name is a text"},
		"name newline":   {`"lista", { name = "a\nb" }`, "name is a text"},
		"pin":            {`"lista", { auth = "pin", pin = "12a4" }`, "pin is a text of 4 digits"},
		"pin number":     {`"lista", { auth = "pin", pin = 1234 }`, "pin is a text of 4 digits"},
		"pin missing":    {`"lista", { auth = "pin" }`, "a PIN is asked for"},
		"remote pin":     {`"lista", { remote = { pin = true } }`, "a PIN is asked for"},
		"remote type":    {`"lista", { remote = "yes" }`, "remote is false, true"},
		"remote option":  {`"lista", { remote = { x = 1 } }`, `remote: unknown option "x"`},
		"role":           {`"lista", { remote = { roles = { "Gosia" } } }`, `invalid role "Gosia"`},
		"role type":      {`"lista", { remote = { roles = "gosia" } }`, "remote.roles is a list"},
	} {
		e := module(t, engine, `
			local p = Plugin.getPlugin("@jan/zakupy-dom")
			p:dashboardAccess(`+c.opts+`)`)
		if !strings.Contains(e, c.want) {
			t.Errorf("%s: %q, want %q", what, e, c.want)
		}
		// the error names the module's line, not the core
		if !strings.Contains(e, ":4:") || strings.Contains(e, "PluginAPI") || strings.Contains(e, "Dashboard.lua") {
			t.Errorf("%s: the error does not point at the module: %s", what, e)
		}
	}
	mustLua(t, engine, `tostring(next(_dashboardAccess))`, "nil")
	mustLua(t, engine, `_dashboards["zakupy_dom_lista"]._auth`, "session")
}

func TestPluginDashboardAccessClearedOnLuaReload(t *testing.T) {
	engine := accessEngine(t)
	module(t, engine, `Plugin.getPlugin("@jan/zakupy-dom"):dashboardAccess("lista", { auth = "public" })`)
	mustLua(t, engine, `_dashboards["zakupy_dom_lista"]._auth`, "public")
	if err := engine.Reinitialize("CLU"); err != nil {
		t.Fatal(err)
	}
	mustLua(t, engine, `tostring(next(_dashboardAccess))`, "nil")
	luaOut(t, engine, `PluginSandbox:create("@jan/zakupy-dom", `+appsDeclared+`)`)
	if _, e := runPlugin(t, engine, "@jan/zakupy-dom", accessPlugin); e != "nil" {
		t.Fatal(e)
	}
	mustLua(t, engine, `_dashboards["zakupy_dom_lista"]._auth`, "session")
}
