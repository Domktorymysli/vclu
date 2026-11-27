package lua

import (
	"strings"
	"testing"
)

// A module grants who opens a plugin's dashboard with the chain of Dashboard.new.
func TestPluginDashboardChainFromModule(t *testing.T) {
	engine := accessEngine(t)
	if e := module(t, engine, `Plugin.getPlugin("@jan/zakupy-dom"):dashboard("lista")
		:name("Zakupy")
		:icon("basket")
		:auth("public")
		:remote({ pin = false })
		:roles("domownicy")`); e != "" {
		t.Fatalf("chain: %s", e)
	}
	d := `_dashboards["zakupy_dom_lista"]`
	mustLua(t, engine, d+`._name`, "Zakupy")
	mustLua(t, engine, d+`._icon`, "basket")
	if e := module(t, engine, `Plugin.getPlugin("@jan/zakupy-dom"):dashboard("lista"):name("Bez ikony")`); e != "" {
		t.Fatal(e)
	}
	mustLua(t, engine, d+`._icon`, "cart") // the plugin's own, as it set it
	if e := module(t, engine, `Plugin.getPlugin("@jan/zakupy-dom"):dashboard("lista"):name("Zakupy"):icon("basket"):auth("public"):remote({ pin = false }):roles("domownicy")`); e != "" {
		t.Fatal(e)
	}
	mustLua(t, engine, d+`._auth`, "public")
	mustLua(t, engine, d+`._remote.roles[1]`, "domownicy")
	if out := luaOut(t, engine, `Dashboard.API.remoteList()`); !strings.Contains(out, `"zakupy_dom_lista"`) || !strings.Contains(out, `"domownicy"`) {
		t.Fatalf("remote list: %s", out)
	}

	// a PIN asked for keeps the dashboard closed until it is given
	if e := module(t, engine, `local d = Plugin.getPlugin("@jan/zakupy-dom"):dashboard("lista"):auth("pin")
		__closed = tostring(_dashboards["zakupy_dom_lista"]._off)
		d:pin("4321")`); e != "" {
		t.Fatalf("pin: %s", e)
	}
	mustLua(t, engine, `__closed`, "true")
	mustLua(t, engine, d+`._auth`, "pin")
	mustLua(t, engine, d+`._pin`, "4321")
	mustLua(t, engine, `tostring(`+d+`._off)`, "nil")

	for what, code := range map[string]string{
		"widget":              `Plugin.getPlugin("@jan/zakupy-dom"):dashboard("lista"):section("S")`,
		"build":               `Plugin.getPlugin("@jan/zakupy-dom"):dashboard("lista"):build()`,
		"undeclared":          `Plugin.getPlugin("@jan/zakupy-dom"):dashboard("inna")`,
		"auth":                `Plugin.getPlugin("@jan/zakupy-dom"):dashboard("lista"):auth("none")`,
		"pin":                 `Plugin.getPlugin("@jan/zakupy-dom"):dashboard("lista"):pin("12")`,
		"roles first":         `Plugin.getPlugin("@jan/zakupy-dom"):dashboard("lista"):roles("a")`,
		"bad role":            `Plugin.getPlugin("@jan/zakupy-dom"):dashboard("lista"):remote():roles("A B")`,
		"remote key":          `Plugin.getPlugin("@jan/zakupy-dom"):dashboard("lista"):remote({ x = 1 })`,
		"name":                `Plugin.getPlugin("@jan/zakupy-dom"):dashboard("lista"):name("a\nb")`,
		"icon":                `Plugin.getPlugin("@jan/zakupy-dom"):dashboard("lista"):icon("<b>")`,
		"not a method":        `local d = Plugin.getPlugin("@jan/zakupy-dom"):dashboard("lista"); d.name({}, "x")`,
		"remote not a method": `local d = Plugin.getPlugin("@jan/zakupy-dom"):dashboard("lista"); d.remote({}, true)`,
		"set a field":         `Plugin.getPlugin("@jan/zakupy-dom"):dashboard("lista").auth = "public"`,
	} {
		e := module(t, engine, code)
		if e == "" {
			t.Errorf("%s: went through", what)
			continue
		}
		if strings.Contains(e, "Dashboard.lua") || strings.Contains(e, "PluginSandbox.lua") {
			t.Errorf("%s: the error names the core: %s", what, e)
		}
	}
	if e := module(t, engine, `Plugin.getPlugin("@jan/zakupy-dom"):dashboard("lista"):section("S")`); !strings.Contains(e, "built by the plugin") {
		t.Errorf("widget refusal: %s", e)
	}
	// a new chain is the whole grant, as Dashboard.new again: what it does not set is the plugin's
	if e := module(t, engine, `Plugin.getPlugin("@jan/zakupy-dom"):dashboard("lista"):name("Inna")`); e != "" {
		t.Fatal(e)
	}
	mustLua(t, engine, d+`._name`, "Inna")
	mustLua(t, engine, d+`._auth`, "session")
	mustLua(t, engine, `tostring(`+d+`._remote)`, "nil")
	// a refused value leaves the chain as it was
	if e := module(t, engine, `local b = Plugin.getPlugin("@jan/zakupy-dom"):dashboard("lista"):auth("public")
		pcall(b.pin, b, "12")
		b:name("Po")`); e != "" {
		t.Fatal(e)
	}
	mustLua(t, engine, d+`._auth`, "public")
	mustLua(t, engine, d+`._name`, "Po")
}

// No plugin sets who opens a dashboard: neither its own through the module's chain nor another's.
func TestPluginDashboardChainNotFromPlugin(t *testing.T) {
	engine := accessEngine(t)
	r, e := runPlugin(t, engine, "@jan/zakupy-dom", `Plugin.getPlugin("@jan/zakupy-dom").dashboard = function() return {} end`)
	wantRefused(t, "replace dashboard", r, e)

	luaOut(t, engine, `PluginSandbox:create("@ola/inny", nil)`)
	r, e = runPlugin(t, engine, "@ola/inny", `Plugin:new("inny", {})
		Plugin.getPlugin("@jan/zakupy-dom"):dashboard("lista"):auth("public")`)
	wantRefused(t, "another plugin", r, e)

	// a builder a module made is refused in a plugin's hands too, however it is called
	if e := module(t, engine, `__b = Plugin.getPlugin("@jan/zakupy-dom"):dashboard("lista")`); e != "" {
		t.Fatal(e)
	}
	luaOut(t, engine, `PluginSandbox.sandboxes["@ola/inny"].environment.__b = __b`)
	for what, code := range map[string]string{
		"method":    `__b:auth("public")`,
		"pcall":     `local ok = pcall(__b.auth, __b, "public"); if ok then return "through" end; error("refused")`,
		"xpcall":    `local ok = xpcall(function() return __b:auth("public") end, function(e) return e end); if ok then return "through" end; error("refused")`,
		"field":     `__b._opts = { auth = "public" }`,
		"no guard":  `if __b._guard ~= nil or __b._opts ~= nil or __b._plugin ~= nil then return "fields" end; error("refused")`,
		"metatable": `if getmetatable and getmetatable(__b) ~= false then return "metatable" end; error("refused")`,
	} {
		r, e := runPlugin(t, engine, "@ola/inny", code)
		wantRefused(t, "module's builder: "+what, r, e)
	}
	mustLua(t, engine, `_dashboards["zakupy_dom_lista"]._auth`, "session")
}
