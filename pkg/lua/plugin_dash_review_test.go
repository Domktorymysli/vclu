package lua

import (
	"strings"
	"testing"
)

// Review of #206: plugin dashboards reach only what the plugin gives them; list and input
// actions as the page and the apps send them.

// Widgets naming the house's data by id (a calendar's schedule, a metric) are refused on a
// plugin's dashboard, on the builder and on a section.
func TestReviewPluginDashboardHouseWidgets(t *testing.T) {
	engine := accessEngine(t)
	for what, code := range map[string]string{
		"schedule":         `:section("S"):schedule("s", "S", "dom", "ogrzewanie")`,
		"builder schedule": `:section("S"); d:schedule("s", "S", "dom", "ogrzewanie")`,
		"chart":            `:section("S"):chart("c", "C", "temp:salon")`,
	} {
		r, e := runPlugin(t, engine, "@jan/zakupy-dom", `local d = Plugin.getPlugin("@jan/zakupy-dom"):dashboard("a")
			d`+code)
		wantRefused(t, what, r, e)
		if e != "nil" && !strings.Contains(e, "not available in a plugin's dashboard") {
			t.Errorf("%s: %s", what, e)
		}
	}
}

// Bars on a plugin's dashboard come from the plugin's own function: a metric path, as the
// source or as compare, is refused on the builder and on a section.
func TestReviewPluginDashboardBarsOwnSamples(t *testing.T) {
	engine := accessEngine(t)
	for what, code := range map[string]string{
		"metric":         `:section("S"):bars("b", "B", "energia:licznik")`,
		"builder metric": `:section("S"); d:bars("b", "B", "energia:licznik")`,
		"compare metric": `:section("S"):bars("b", "B", function() return {} end, { compare = "energia:licznik" })`,
	} {
		r, e := runPlugin(t, engine, "@jan/zakupy-dom", `local d = Plugin.getPlugin("@jan/zakupy-dom"):dashboard("a")
			d`+code)
		wantRefused(t, what, r, e)
		if e != "nil" && !strings.Contains(e, "takes a function returning its samples") {
			t.Errorf("%s: %s", what, e)
		}
	}
	if r, _ := runPlugin(t, engine, "@jan/zakupy-dom", `return Plugin.getPlugin("@jan/zakupy-dom"):has("widget:bars")`); r != "true" {
		t.Errorf(`has("widget:bars") = %s`, r)
	}
	if _, e := runPlugin(t, engine, "@jan/zakupy-dom", `Plugin.getPlugin("@jan/zakupy-dom"):dashboard("b"):section("S")
		:bars("b", "B", function() return { { t = 200, v = 2 }, { t = 100, v = 1 } } end,
			{ range = "30d", bucket = "1d", agg = "sum", compare = function() return { { t = 100, v = 5 } } end })
		:build()`); e != "nil" {
		t.Fatal(e)
	}
	// opts changed after build() do not reach the widget
	if _, e := runPlugin(t, engine, "@jan/zakupy-dom", `local o = { range = "30d" }
		Plugin.getPlugin("@jan/zakupy-dom"):dashboard("x"):section("S"):bars("m", "M", function() return {} end, o):build()
		o.compare = "energia:licznik"`); e != "nil" {
		t.Fatal(e)
	}
	if out := luaOut(t, engine, `Dashboard.API.barsSpec("zakupy_dom_x", "m")`); strings.Contains(out, "energia") {
		t.Errorf("compare swapped after build: %s", out)
	}
	// and a metric on a plugin's widget is not served, however it got there
	mustExec(t, engine, `_dashboards["zakupy_dom_x"]._sections[1].widgets[1].opts.compare = "energia:licznik"`)
	if out := luaOut(t, engine, `Dashboard.API.barsSpec("zakupy_dom_x", "m")`); !strings.Contains(out, `"status":403`) {
		t.Errorf("metric on a plugin's bars: %s", out)
	}
	out := luaOut(t, engine, `Dashboard.API.barsSpec("zakupy_dom_b", "b")`)
	if !strings.Contains(out, `"pathFn":true`) || !strings.Contains(out, `"compareFn":true`) ||
		!strings.Contains(out, `"pathSamples":[{"t":100,"v":1},{"t":200,"v":2}]`) || strings.Contains(out, `"path":`) {
		t.Errorf("spec: %s", out)
	}
}

// A camera of the house shown on a plugin's dashboard does not reach remote viewers, even
// when the user lets the dashboard out of the house.
func TestReviewPluginDashboardCameraNotRemote(t *testing.T) {
	engine := accessEngine(t)
	mustExec(t, engine, `Dashboard.new("dom"):remote():section("S"):image("k", "K", "/api/camera/garaz/snapshot"):build()`)
	if _, e := runPlugin(t, engine, "@jan/zakupy-dom", `Plugin.getPlugin("@jan/zakupy-dom"):dashboard("b")
		:section("S"):image("i", "I", "/api/camera/wejscie/snapshot")
		:image("f", "F", function() return "/api/camera/salon/snapshot" end):build()`); e != "nil" {
		t.Fatal(e)
	}
	if e := module(t, engine, `Plugin.getPlugin("@jan/zakupy-dom"):dashboard("b"):remote()`); e != "" {
		t.Fatal(e)
	}
	for _, cam := range []string{"wejscie", "salon"} {
		if out := luaOut(t, engine, `Dashboard.API.remoteCamera("`+cam+`", "member")`); !strings.Contains(out, `"used":false`) {
			t.Errorf("%s through a plugin's dashboard: %s", cam, out)
		}
	}
	// a module's dashboard still serves its own camera
	if out := luaOut(t, engine, `Dashboard.API.remoteCamera("garaz", "member")`); !strings.Contains(out, `"used":true`) {
		t.Errorf("module camera: %s", out)
	}
}

// List and input on a plugin's dashboard: what the page sends is checked before the plugin's
// callbacks run; a remote viewer of a readonly list changes nothing.
func TestReviewPluginListActions(t *testing.T) {
	engine := accessEngine(t)
	if _, e := runPlugin(t, engine, "@jan/zakupy-dom", `
		__calls = {}
		local items = { { id = 7, label = "Mleko" }, { id = "x", label = "Chleb" } }
		local function log(...) __calls[#__calls + 1] = table.concat({ ... }, "|") end
		Plugin.getPlugin("@jan/zakupy-dom"):dashboard("b"):section("S")
			:list("ls", "L", function() return items end, {
				onToggle = function(id, done) log("t", type(id), tostring(id), tostring(done)) end,
				onDelete = function(id) log("d", type(id), tostring(id)) end,
				onAdd = function(text) log("a", text) end,
				maxLength = 5 })
			:list("ro", "RO", function() return items end, { remote = "readonly",
				onToggle = function() log("ro") end, onDelete = function() log("ro") end, onAdd = function() log("ro") end })
			:input("in", "I", nil, function(text) log("i", text) end, { maxLength = 3 })
			:build()`); e != "nil" {
		t.Fatal(e)
	}
	module(t, engine, `Plugin.getPlugin("@jan/zakupy-dom"):dashboard("b"):remote()`)
	act := func(args string) string {
		t.Helper()
		return luaOut(t, engine, `Dashboard.API.executeAction("zakupy_dom_b", `+args+`)`)
	}
	for what, c := range map[string]struct{ args, want string }{
		"number id as the page sends it": {`"ls", "toggle", { id = "7", done = true }`, `"success":true`},
		"number id as a number":          {`"ls", "delete", { id = 7 }`, `"success":true`},
		"string id":                      {`"ls", "delete", { id = "x" }`, `"success":true`},
		"unknown id":                     {`"ls", "delete", { id = "8" }`, `Unknown item`},
		"id as a table":                  {`"ls", "delete", { id = { 7 } }`, `Value must be {id}`},
		"toggle without done":            {`"ls", "toggle", { id = "7" }`, `Value must be {id, done}`},
		"done as text":                   {`"ls", "toggle", { id = "7", done = "true" }`, `Value must be {id, done}`},
		"five Polish letters":            {`"ls", "add", "żółćź"`, `"success":true`},
		"six Polish letters":             {`"ls", "add", "żółćźń"`, `longer than 5 characters`},
		"controls as spaces":             {`"ls", "add", "a\nb\tc"`, `"success":true`},
		"only controls":                  {`"ls", "add", "\n\r\t"`, `Empty text`},
		"input limit":                    {`"in", "submit", "abcd"`, `longer than 3 characters`},
		"input number":                   {`"in", "submit", 12`, `Value must be text`},
		"unknown action":                 {`"ls", "rename", "x"`, `Invalid action for list`},
		"remote readonly toggle":         {`"ro", "toggle", { id = "7", done = true }, { role = "member" }`, `"status":403`},
		"remote readonly add":            {`"ro", "add", "x", { role = "member" }`, `"status":403`},
		"remote readonly delete":         {`"ro", "delete", { id = "7" }, { role = "member" }`, `"status":403`},
	} {
		if got := act(c.args); !strings.Contains(got, c.want) {
			t.Errorf("%s: %s, want %s", what, got, c.want)
		}
	}
	calls := luaOut(t, engine, `table.concat(PluginSandbox.sandboxes["@jan/zakupy-dom"].environment.__calls, ",")`)
	for _, want := range []string{"t|number|7|true", "d|number|7", "d|string|x", "a|żółćź", "a|a b c"} {
		if !strings.Contains(calls, want) {
			t.Errorf("calls %q: no %q", calls, want)
		}
	}
	if strings.Contains(calls, "ro") {
		t.Errorf("a readonly list ran a callback: %s", calls)
	}
}

// An id given as 1 and "1" is the same item for the page: the list is refused.
func TestReviewPluginListDuplicateIds(t *testing.T) {
	engine := accessEngine(t)
	runPlugin(t, engine, "@jan/zakupy-dom", `Plugin.getPlugin("@jan/zakupy-dom"):dashboard("b"):section("S")
		:list("ls", "L", function() return { { id = 1, label = "a" }, { id = "1", label = "b" } } end, { onDelete = function() __deleted = true end })
		:build()`)
	if out := luaOut(t, engine, `Dashboard.API.getValues("zakupy_dom_b")`); !strings.Contains(out, `is used twice`) {
		t.Errorf("values: %s", out)
	}
	if out := luaOut(t, engine, `Dashboard.API.executeAction("zakupy_dom_b", "ls", "delete", { id = "1" })`); !strings.Contains(out, `is used twice`) {
		t.Errorf("delete: %s", out)
	}
	mustLua(t, engine, `tostring(PluginSandbox.sandboxes["@jan/zakupy-dom"].environment.__deleted)`, "nil")
}

// An action's text param is limited in characters, as its error says, not in bytes.
func TestReviewPluginActionTextInCharacters(t *testing.T) {
	engine := appsEngine(t)
	call := func(item string) string {
		return luaOut(t, engine, `PluginSandbox:callAction("@jan/zakupy-dom", "add", [==[{"item": "`+item+`"}]==])`)
	}
	if got := call(strings.Repeat("ż", 1000)); !strings.Contains(got, `"result"`) {
		t.Errorf("1000 Polish letters: %.80s", got)
	}
	if got := call(strings.Repeat("ż", 1001)); !strings.Contains(got, "longer than 1000 characters") {
		t.Errorf("1001 Polish letters: %.80s", got)
	}
}
