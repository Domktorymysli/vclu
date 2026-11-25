package lua

import (
	"strings"
	"testing"
)

// Apps: plugin:dashboard and plugin:action (plugin API 2, no permission).

const appsPlugin = `
local plugin = Plugin:new("zakupy-dom", {})
local items = {}
__apps_items = items
plugin:dashboard("lista"):name("Zakupy")
	:section("Lista", "cart")
	:button("clear", "Clear", "trash", function() for i = #items, 1, -1 do items[i] = nil end return "ok" end)
	:status("n", "Count", "hash", function() return #items end)
	:button("boom", "Boom", "x", function() error("kaboom") end)
	:toggle("env", "Env", "x", function() return type(os.execute) == "nil" and type(Plugin.new) == "function" end, function() end)
	:build()
plugin:action("add", {
	description = {pl = "Dodaj", en = "Add"},
	params = {
		item = {type = "string", description = "What", required = true},
		qty = {type = "number"},
		urgent = {type = "boolean"},
	},
}, function(p)
	if p.item == "fail" then return nil, "no such shop" end
	if p.item == "throw" then error("broken") end
	if p.item == "fn" then return function() end end
	items[#items + 1] = p.item
	return {count = #items, qty = p.qty, urgent = p.urgent}
end)
return plugin
`

// the dashboards plugin.json declares, as the loader passes them
const appsDeclared = `{dashboards = {lista = {name = "Zakupy"}, x = {name = "X"}, a = {name = "A"}, b = {name = "B"}}}`

func appsEngine(t *testing.T) *LuaEngine {
	t.Helper()
	engine := sandboxEngine(t, "@jan/zakupy-dom", appsDeclared)
	if _, e := runPlugin(t, engine, "@jan/zakupy-dom", appsPlugin); e != "nil" {
		t.Fatalf("plugin: %s", e)
	}
	return engine
}

func luaOut(t *testing.T, engine *LuaEngine, expr string) string {
	t.Helper()
	out, err := engine.Execute(expr)
	if err != nil {
		t.Fatalf("%s: %v", expr, err)
	}
	return out
}

func TestPluginDashboardIdAndAuth(t *testing.T) {
	engine := appsEngine(t)
	list := luaOut(t, engine, `Dashboard.API.list()`)
	for _, want := range []string{`"id":"zakupy_dom_lista"`, `"auth":"session"`, `"plugin":"@jan/zakupy-dom"`} {
		if !strings.Contains(list, want) {
			t.Errorf("list %s: no %s", list, want)
		}
	}
	// an auth the plugin sets stays; building again replaces its own dashboard
	if _, e := runPlugin(t, engine, "@jan/zakupy-dom", `
		local p = Plugin.getPlugin("@jan/zakupy-dom")
		p:dashboard("lista"):auth("pin"):section("A"):button("b", "B", "x", function() end):build()
	`); e != "nil" {
		t.Fatalf("rebuild: %s", e)
	}
	mustLua(t, engine, `_dashboards["zakupy_dom_lista"]._auth`, "pin")

	for _, id := range []string{`"Lista"`, `"a-b"`, `""`, `5`, `"a/b"`, `string.rep("a", 65)`} {
		_, e := runPlugin(t, engine, "@jan/zakupy-dom", `Plugin.getPlugin("@jan/zakupy-dom"):dashboard(`+id+`)`)
		if !strings.Contains(e, "lower-case letters, digits and _") {
			t.Errorf("id %s: %s", id, e)
		}
	}
}

func TestPluginDashboardCollision(t *testing.T) {
	engine := NewLuaEngine()
	t.Cleanup(engine.Close)
	luaOut(t, engine, `Dashboard.new("zakupy_lista"):section("M"):build()`)
	luaOut(t, engine, `PluginSandbox:create("@jan/zakupy", {dashboards = {lista = {name = "L"}, lista_x = {name = "LX"}, late = {name = "Late"}}})
		PluginSandbox:create("@ola/zakupy-lista", {dashboards = {x = {name = "X"}}})`)

	_, e := runPlugin(t, engine, "@jan/zakupy", `Plugin:new("zakupy", {}):dashboard("lista")`)
	if !strings.Contains(e, "zakupy_lista already exists (a module)") {
		t.Fatalf("a module's dashboard: %s", e)
	}
	mustLua(t, engine, `_dashboards["zakupy_lista"]._sections[1].name`, "M")

	// another plugin's: zakupy-lista + "x" and zakupy + "lista_x" are one id
	if _, e := runPlugin(t, engine, "@ola/zakupy-lista", `Plugin:new("zakupy-lista", {}):dashboard("x"):section("O"):build()`); e != "nil" {
		t.Fatal(e)
	}
	_, e = runPlugin(t, engine, "@jan/zakupy", `Plugin.getPlugin("@jan/zakupy"):dashboard("lista_x"):build()`)
	if !strings.Contains(e, "(plugin @ola/zakupy-lista)") {
		t.Fatalf("another plugin's dashboard: %s", e)
	}
	// taken between dashboard() and build(): refused at build
	_, e = runPlugin(t, engine, "@jan/zakupy", `
		__d = Plugin.getPlugin("@jan/zakupy"):dashboard("late")
		return "ok"`)
	if e != "nil" {
		t.Fatal(e)
	}
	luaOut(t, engine, `Dashboard.new("zakupy_late"):build()`)
	_, e = runPlugin(t, engine, "@jan/zakupy", `__d:build()`)
	if !strings.Contains(e, "zakupy_late already exists") {
		t.Fatalf("taken before build: %s", e)
	}

	// cleanup removes only the plugin's own
	luaOut(t, engine, `PluginSandbox:cleanup("@ola/zakupy-lista"); PluginSandbox:cleanup("@jan/zakupy")`)
	mustLua(t, engine, `tostring(_dashboards["zakupy_lista_x"])`, "nil")
	mustLua(t, engine, `_dashboards["zakupy_lista"]._sections[1].name`, "M")
	mustLua(t, engine, `tostring(_dashboards["zakupy_late"] ~= nil)`, "true")
}

func TestPluginDashboardCallbacks(t *testing.T) {
	engine := appsEngine(t)
	out := luaOut(t, engine, `Dashboard.API.executeAction("zakupy_dom_lista", "boom", "tap", nil, {})`)
	if !strings.Contains(out, "kaboom") {
		t.Fatalf("error of a callback: %s", out)
	}
	out = luaOut(t, engine, `Dashboard.API.getValues("zakupy_dom_lista")`)
	if !strings.Contains(out, `"env"`) || !strings.Contains(out, `true`) {
		t.Fatalf("a callback runs in the plugin's environment: %s", out)
	}
	out = luaOut(t, engine, `Dashboard.API.executeAction("zakupy_dom_lista", "clear", "tap", nil, {})`)
	if !strings.Contains(out, `"success":true`) {
		t.Fatalf("tap: %s", out)
	}

	// the raw builder and sections stay out of reach
	r, e := runPlugin(t, engine, "@jan/zakupy-dom", `
		local p = Plugin.getPlugin("@jan/zakupy-dom")
		local d = p:dashboard("x")
		local s = d:section("S")
		local seen
		d:when({"morning", "afternoon", "evening", "night"}, function(b) seen = (b == d) end)
		return tostring(d._sections) .. tostring(d._id) .. tostring(s._builder) .. tostring(s._sectionIndex) .. tostring(seen)`)
	if e != "nil" || r != "nilnilnilniltrue" {
		t.Fatalf("facade fields: %s / %s", r, e)
	}
	for what, code := range map[string]string{
		"another self":  `local p = Plugin.getPlugin("@jan/zakupy-dom"); local a, b = p:dashboard("a"), p:dashboard("b"); a.build(b)`,
		"area":          `Plugin.getPlugin("@jan/zakupy-dom"):dashboard("a"):section("S"):area("Salon")`,
		"floor":         `Plugin.getPlugin("@jan/zakupy-dom"):dashboard("a"):floor("Parter")`,
		"covers":        `Plugin.getPlugin("@jan/zakupy-dom"):dashboard("a"):section("S"):covers("c", "C", {"CLU1.ROL1"})`,
		"calendar":      `Plugin.getPlugin("@jan/zakupy-dom"):dashboard("a"):section("S"):calendar("c", "C", "dom")`,
		"room":          `Plugin.getPlugin("@jan/zakupy-dom"):dashboard("a"):section("S"):room("r", "Salon")`,
		"duplicate ids": `Plugin.getPlugin("@jan/zakupy-dom"):dashboard("a"):section("S"):button("b", "B", "x", function() end):button("b", "B", "x", function() end):build()`,
	} {
		r, e := runPlugin(t, engine, "@jan/zakupy-dom", code)
		wantRefused(t, what, r, e)
		if strings.Contains(e, "Dashboard.lua") || strings.Contains(e, "PluginSandbox.lua") {
			t.Errorf("%s: the error names the core: %s", what, e)
		}
	}
}

func TestPluginActionSpec(t *testing.T) {
	engine := sandboxEngine(t, "@jan/a", "")
	runPlugin(t, engine, "@jan/a", `__p = Plugin:new("a", {})`)
	for what, code := range map[string]string{
		"name upper":     `__p:action("Add", {description = "x"}, function() end)`,
		"name digit":     `__p:action("1a", {description = "x"}, function() end)`,
		"name dash":      `__p:action("a-b", {description = "x"}, function() end)`,
		"no description": `__p:action("a", {}, function() end)`,
		"bad language":   `__p:action("a", {description = {de = "x"}}, function() end)`,
		"unknown field":  `__p:action("a", {description = "x", run = 1}, function() end)`,
		"param type":     `__p:action("a", {description = "x", params = {n = {type = "table"}}}, function() end)`,
		"param name":     `__p:action("a", {description = "x", params = {["a b"] = {type = "string"}}}, function() end)`,
		"param field":    `__p:action("a", {description = "x", params = {n = {type = "string", default = 1}}}, function() end)`,
		"required":       `__p:action("a", {description = "x", params = {n = {type = "string", required = "yes"}}}, function() end)`,
		"no fn":          `__p:action("a", {description = "x"})`,
	} {
		r, e := runPlugin(t, engine, "@jan/a", code)
		wantRefused(t, what, r, e)
	}
	r, e := runPlugin(t, engine, "@jan/a", `
		for i = 1, 32 do __p:action("a" .. i, {description = "x"}, function() end) end
		__p:action("a1", {description = "again"}, function() end)
		local ok, err = pcall(function() __p:action("a33", {description = "x"}, function() end) end)
		return tostring(err)`)
	if e != "nil" || !strings.Contains(r, "at most 32 actions") {
		t.Fatalf("limit: %s / %s", r, e)
	}
	if strings.Count(luaOut(t, engine, `PluginSandbox:listActions("@jan/a")`), `"name"`) != 32 {
		t.Fatal("32 actions listed")
	}
}

func TestPluginActionCall(t *testing.T) {
	engine := appsEngine(t)
	call := func(name, params string) string {
		t.Helper()
		return luaOut(t, engine, `PluginSandbox:callAction("@jan/zakupy-dom", "`+name+`", [==[`+params+`]==])`)
	}
	for params, want := range map[string]string{
		`{"item": "milk", "qty": 2, "urgent": true}`: `{"result":{`,
		`{}`:                                            `param item is required`,
		`{"item": "milk", "colour": "red"}`:             `unknown param colour (params: item, qty, urgent)`,
		`{"item": 5}`:                                   `param item is a string, got number`,
		`{"item": "milk", "qty": "2"}`:                  `param qty is a number, got string`,
		`{"item": "milk", "urgent": 1}`:                 `param urgent is a boolean, got number`,
		`{"item": "milk", "qty": {"a": 1}}`:             `param qty is a number, got table`,
		`{"item": "` + strings.Repeat("x", 1001) + `"}`: `longer than 1000 characters`,
		`{"item": "fail"}`:                              `"error":"no such shop","code":"failed"`,
		`{"item": "throw"}`:                             `broken`,
		`{"item": "fn"}`:                                `returned a function`,
		`[1, 2]`:                                        `unknown param 1`,
	} {
		if got := call("add", params); !strings.Contains(got, want) {
			t.Errorf("%.40s: %s, want %s", params, got, want)
		}
	}
	if got := call("nope", `{}`); !strings.Contains(got, `"code":"not_found"`) {
		t.Errorf("unknown action: %s", got)
	}
	if got := call("add", `{"item": "`+strings.Repeat("x", 1000)+`"}`); !strings.Contains(got, `"result"`) {
		t.Errorf("1000 characters pass: %.80s", got)
	}
	mustLua(t, engine, `#PluginSandbox.sandboxes["@jan/zakupy-dom"].environment.__apps_items`, "2")

	list := luaOut(t, engine, `PluginSandbox:listActions()`)
	for _, want := range []string{`"plugin":"@jan/zakupy-dom"`, `"name":"add"`, `"pl":"Dodaj"`, `"item":{`, `"required":true`} {
		if !strings.Contains(list, want) {
			t.Errorf("list %s: no %s", list, want)
		}
	}
	if got := luaOut(t, engine, `PluginSandbox:listActions("@jan/other")`); got != "[]" {
		t.Errorf("filter: %s", got)
	}

	// a secret in an error or result is masked; nil gives a null result
	luaOut(t, engine, `PluginSandbox:setSecrets("@jan/zakupy-dom", {token = "tajne-haslo"})`)
	runPlugin(t, engine, "@jan/zakupy-dom", `
		local p = Plugin.getPlugin("@jan/zakupy-dom")
		p:action("leak", {description = "x"}, function() error("token tajne-haslo") end)
		p:action("echo", {description = "x"}, function() return "tajne-haslo!" end)
		p:action("none", {description = "x"}, function() end)`)
	if got := call("leak", `{}`); strings.Contains(got, "tajne-haslo") || !strings.Contains(got, "***") {
		t.Errorf("secret in an error: %s", got)
	}
	if got := call("echo", `{}`); got != `{"result":"***!"}` {
		t.Errorf("secret in a result: %s", got)
	}
	if got := call("none", `{}`); got != `{"result":null}` {
		t.Errorf("nil result: %s", got)
	}

	// cleanup removes the actions and the dashboard
	luaOut(t, engine, `PluginSandbox:cleanup("@jan/zakupy-dom")`)
	if got := call("add", `{"item": "x"}`); !strings.Contains(got, "not_found") {
		t.Errorf("after cleanup: %s", got)
	}
	mustLua(t, engine, `PluginSandbox:listActions()`, "[]")
	mustLua(t, engine, `tostring(_dashboards["zakupy_dom_lista"])`, "nil")
	for _, id := range strings.Split(luaOut(t, engine, `table.concat(_dashboardOrder, ",")`), ",") {
		if id == "zakupy_dom_lista" {
			t.Error("still in _dashboardOrder")
		}
	}
}

// Through the loader: disabling the plugin takes its dashboard and actions with it.
func TestPluginAppsUnload(t *testing.T) {
	dir := t.TempDir()
	init := strings.Replace(appsPlugin, `Plugin:new("zakupy-dom", {})`, `Plugin:new("zakupy", {})`, 1)
	init = strings.Replace(init, `:name("Zakupy")`, "", 1)
	loader := installTestPlugin(t, dir, "@jan/zakupy", map[string]interface{}{"api": 2,
		"dashboards": []map[string]any{{"id": "lista", "name": map[string]string{"pl": "Zakupy", "en": "Shopping"}}}}, init, false)
	engine := apiEngine(t, loader, dir)
	if err := loader.LoadPlugin("@jan/zakupy", map[string]interface{}{}); err != nil {
		t.Fatal(err)
	}
	mustLua(t, engine, `_dashboards["zakupy_lista"]._plugin`, "@jan/zakupy")
	mustLua(t, engine, `_dashboards["zakupy_lista"]._name`, "Zakupy") // the manifest's name in Polish, the plugin set none
	if got := luaOut(t, engine, `PluginSandbox:callAction("@jan/zakupy", "add", '{"item":"milk"}')`); !strings.Contains(got, `"count":1`) {
		t.Fatalf("call: %s", got)
	}
	if err := loader.UnloadPlugin("@jan/zakupy"); err != nil {
		t.Fatal(err)
	}
	mustLua(t, engine, `tostring(_dashboards["zakupy_lista"])`, "nil")
	mustLua(t, engine, `PluginSandbox:listActions()`, "[]")
	// a reload brings both back
	if err := loader.LoadPlugin("@jan/zakupy", map[string]interface{}{}); err != nil {
		t.Fatal(err)
	}
	mustLua(t, engine, `_dashboards["zakupy_lista"]._plugin`, "@jan/zakupy")
}
