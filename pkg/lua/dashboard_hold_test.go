package lua

import (
	"strings"
	"testing"
)

// Press and hold: opts.hold on a button and a toggle, and the gate tile. An action that needs a
// hold runs only when the request says it was held; the rest of a widget's actions do not.

const dashHoldSetup = `
	_dashboards = {}
	H = { calls = {} }
	local function log(s) H.calls[#H.calls + 1] = s end
	G = { state = 'closed' }
	Dashboard.new('hd')
		:section('S', 'grid')
			:button('b', 'Bell', 'bell', function() log('b') end, { hold = true })
			:button('b2', 'Slow', 'bell', function() log('b2') end, { hold = 2500.7 })
			:button('plain', 'Plain', 'bell', function() log('plain') end)
			:button('c', 'Confirm', 'bell', function() log('c') end, { confirm = 'Sure?' })
			:toggle('t', 'Lock', 'lock', function() return false end, function(v) log('t:' .. tostring(v)) end, { hold = 300 })
			:gate('g', 'Gate', {
				open = function() log('g:open'); return 'opening' end,
				close = function() log('g:close') end,
				stop = function() log('g:stop') end,
				state = function()
					if G.fail then error('reed gone') end
					return G.state
				end,
			})
			:gate('w', 'Wicket', { open = function() log('w:open') end, hold = false, icon = 'door-open', span = 2,
				disabled = function() return G.off == true end })
		:build()
`

func newHoldEngine(t *testing.T) *LuaEngine {
	t.Helper()
	engine := NewLuaEngine()
	t.Cleanup(engine.Close)
	mustExec(t, engine, dashHoldSetup)
	return engine
}

func holdAction(t *testing.T, engine *LuaEngine, widget, action, value string, held bool) map[string]interface{} {
	t.Helper()
	h := "nil"
	if held {
		h = "{ held = true }"
	}
	var res map[string]interface{}
	dashJSON(t, engine, `Dashboard.API.executeAction("hd", "`+widget+`", "`+action+`", `+value+`, `+h+`)`, &res)
	return res
}

func holdCalls(t *testing.T, engine *LuaEngine) string {
	t.Helper()
	return mustExec(t, engine, `local s = table.concat(H.calls, ','); H.calls = {}; return s`)
}

func TestDashboardHoldDefinition(t *testing.T) {
	engine := newHoldEngine(t)
	var def dashDef
	dashJSON(t, engine, `Dashboard.API.getDefinition("hd")`, &def)

	for id, want := range map[string]interface{}{"b": float64(800), "b2": float64(2500), "t": float64(300), "g": float64(800)} {
		if got := def.widget(id)["hold"]; got != want {
			t.Errorf("%s hold = %v, want %v", id, got, want)
		}
	}
	for _, id := range []string{"plain", "c", "w"} {
		if got, ok := def.widget(id)["hold"]; ok {
			t.Errorf("%s hold = %v, want none", id, got)
		}
	}
	if c := def.widget("c"); c["confirm"] != "Sure?" {
		t.Errorf("confirm = %v", c["confirm"])
	}

	g := def.widget("g")
	if g["type"] != "gate" || g["icon"] != "door-closed" || g["state"] != "closed" {
		t.Errorf("g = %v", g)
	}
	caps, _ := g["capabilities"].(map[string]interface{})
	if caps["close"] != true || caps["stop"] != true {
		t.Errorf("g capabilities = %v", caps)
	}
	w := def.widget("w")
	caps, _ = w["capabilities"].(map[string]interface{})
	if w["icon"] != "door-open" || w["span"] != float64(2) || caps["close"] != false || caps["stop"] != false {
		t.Errorf("w = %v", w)
	}
	if _, ok := w["state"]; ok {
		t.Errorf("w without state has state %v", w["state"])
	}
	// the functions stay on the server
	for _, k := range []string{"open", "close", "opts"} {
		if _, ok := g[k]; ok {
			t.Errorf("g has %s", k)
		}
	}
}

func TestDashboardHoldGateValues(t *testing.T) {
	engine := newHoldEngine(t)
	var vals map[string]map[string]interface{}
	for _, st := range []string{"open", "closed", "opening", "closing", "moving"} {
		mustExec(t, engine, `G.state = '`+st+`'`)
		dashJSON(t, engine, `Dashboard.API.getValues("hd")`, &vals)
		if vals["g"]["state"] != st || vals["g"]["disabled"] != false {
			t.Errorf("state %s: g = %v", st, vals["g"])
		}
	}
	mustExec(t, engine, `G.state = nil`)
	vals = nil
	dashJSON(t, engine, `Dashboard.API.getValues("hd")`, &vals)
	if _, ok := vals["g"]["state"]; ok || vals["g"]["error"] != nil {
		t.Errorf("nil state: g = %v", vals["g"])
	}
	mustExec(t, engine, `G.state = 'ajar'`)
	vals = nil
	dashJSON(t, engine, `Dashboard.API.getValues("hd")`, &vals)
	if e, _ := vals["g"]["error"].(string); !strings.Contains(e, `section:gate("g")`) || !strings.Contains(e, "ajar") {
		t.Errorf("bad state: g = %v", vals["g"])
	}
	mustExec(t, engine, `G.fail = true; G.off = true`)
	vals = nil
	dashJSON(t, engine, `Dashboard.API.getValues("hd")`, &vals)
	if e, _ := vals["g"]["error"].(string); !strings.Contains(e, "reed gone") {
		t.Errorf("state error: g = %v", vals["g"])
	}
	if vals["w"]["disabled"] != true {
		t.Errorf("w = %v", vals["w"])
	}
}

func TestDashboardHoldActions(t *testing.T) {
	engine := newHoldEngine(t)
	refused := "Press and hold to confirm"

	// without held nothing runs
	for _, c := range [][3]string{{"b", "tap", "nil"}, {"b2", "tap", "nil"}, {"t", "toggle", "true"}, {"g", "open", "nil"}, {"g", "close", "nil"}} {
		if res := holdAction(t, engine, c[0], c[1], c[2], false); res["error"] != refused {
			t.Errorf("%s %s without held = %v", c[0], c[1], res)
		}
		// four arguments, a context without held, or a context that is not a table: refused too
		for _, ctx := range []string{"", ", {}", ", { held = 'true' }", ", true", ", 'held'"} {
			var res map[string]interface{}
			dashJSON(t, engine, `Dashboard.API.executeAction("hd", "`+c[0]+`", "`+c[1]+`", `+c[2]+ctx+`)`, &res)
			if res["error"] != refused {
				t.Errorf("%s %s with ctx %q = %v", c[0], c[1], ctx, res)
			}
		}
	}
	if calls := holdCalls(t, engine); calls != "" {
		t.Fatalf("ran without a hold: %s", calls)
	}

	// held runs them
	if res := holdAction(t, engine, "b", "tap", "nil", true); res["success"] != true {
		t.Errorf("b held = %v", res)
	}
	if res := holdAction(t, engine, "t", "toggle", "true", true); res["success"] != true || res["value"] != true {
		t.Errorf("t held = %v", res)
	}
	if res := holdAction(t, engine, "g", "open", "nil", true); res["success"] != true || res["message"] != "opening" {
		t.Errorf("g open held = %v", res)
	}
	if res := holdAction(t, engine, "g", "close", "nil", true); res["success"] != true {
		t.Errorf("g close held = %v", res)
	}
	// stop is a tap; a widget without hold ignores held
	if res := holdAction(t, engine, "g", "stop", "nil", false); res["success"] != true {
		t.Errorf("g stop = %v", res)
	}
	if res := holdAction(t, engine, "w", "open", "nil", false); res["success"] != true {
		t.Errorf("w open (hold = false) = %v", res)
	}
	if res := holdAction(t, engine, "plain", "tap", "nil", false); res["success"] != true {
		t.Errorf("plain = %v", res)
	}
	if res := holdAction(t, engine, "plain", "tap", "nil", true); res["success"] != true {
		t.Errorf("plain held = %v", res)
	}
	if calls := holdCalls(t, engine); calls != "b,t:true,g:open,g:close,g:stop,w:open,plain,plain" {
		t.Errorf("calls = %s", calls)
	}

	// the usual refusals stay
	if res := holdAction(t, engine, "w", "close", "nil", true); res["error"] != "No close function" {
		t.Errorf("w close = %v", res)
	}
	if res := holdAction(t, engine, "w", "stop", "nil", true); res["error"] != "No stop function" {
		t.Errorf("w stop = %v", res)
	}
	if res := holdAction(t, engine, "g", "set", "50", true); res["error"] != "Invalid action for gate" {
		t.Errorf("g set = %v", res)
	}
	if res := holdAction(t, engine, "b", "toggle", "true", true); res["error"] != "Invalid action for button" {
		t.Errorf("b toggle = %v", res)
	}
	mustExec(t, engine, `G.off = true`)
	if res := holdAction(t, engine, "w", "open", "nil", true); res["error"] != "Widget is disabled" {
		t.Errorf("w disabled = %v", res)
	}
	if calls := holdCalls(t, engine); calls != "" {
		t.Errorf("refused actions ran: %s", calls)
	}
}

// Wrong options fail at the module's line, with the method's name.
func TestDashboardHoldArgumentErrors(t *testing.T) {
	btn := func(opts string) string {
		return `:section('S', 'grid'):button('a', 'L', 'bell', function() end, ` + opts + `)`
	}
	tog := func(opts string) string {
		return `:section('S', 'grid'):toggle('a', 'L', 'lock', function() end, function() end, ` + opts + `)`
	}
	gate := func(opts string) string { return `:section('S', 'grid'):gate('a', 'L', ` + opts + `)` }
	cases := []struct{ call, where, want string }{
		{btn(`{ hold = 299 }`), "section:button", "opts.hold must be"},
		{btn(`{ hold = 5001 }`), "section:button", "opts.hold must be"},
		{btn(`{ hold = '800' }`), "section:button", "opts.hold must be"},
		{btn(`{ hold = 0/0 }`), "section:button", "opts.hold must be"},
		{btn(`{ hold = true, confirm = 'Sure?' }`), "section:button", "not both"},
		{btn(`5`), "section:button", "opts must be a table"},
		{tog(`'x'`), "section:toggle", "opts must be a table"},
		{tog(`{ hold = 100 }`), "section:toggle", "opts.hold must be"},
		{gate(`nil`), "section:gate", "opts must be a table"},
		{gate(`{}`), "section:gate", "opts.open must be a function"},
		{gate(`{ open = function() end, colour = 1 }`), "section:gate", `unknown option "colour"`},
		{gate(`{ open = function() end, confirm = 'x' }`), "section:gate", `unknown option "confirm"`},
		{gate(`{ open = function() end, close = 1 }`), "section:gate", "opts.close must be a function"},
		{gate(`{ open = function() end, state = 'open' }`), "section:gate", "opts.state must be a function"},
		{gate(`{ open = function() end, disabled = true }`), "section:gate", "opts.disabled must be a function"},
		{gate(`{ open = function() end, icon = 'x y' }`), "section:gate", "opts.icon"},
		{gate(`{ open = function() end, hold = 10 }`), "section:gate", "opts.hold must be"},
		{`:section('S', 'grid'):gate('', 'L', { open = function() end })`, "section:gate", "id must be"},
		{`:section('S', 'grid'):gate('a', nil, { open = function() end })`, "section:gate", "label must be"},
		{`:header():gate('a', 'L', { open = function() end })`, "section:gate", "not the header"},
	}
	for _, c := range cases {
		engine := NewLuaEngine()
		_, err := engine.Execute("_dashboards = {}\nlocal ok, err = pcall(function()\n  Dashboard.new('e')" + c.call + "\nend)\nerror(err, 0)")
		engine.Close()
		if err == nil {
			t.Errorf("%s: no error", c.call)
			continue
		}
		msg := err.Error()
		if !strings.Contains(msg, "<string>:3: ") || !strings.Contains(msg, c.where) || !strings.Contains(msg, c.want) {
			t.Errorf("%s: %s", c.call, msg)
		}
	}
	// the builder's own methods too
	for _, call := range []string{
		"b:button('a', 'L', 'bell', function() end, { hold = 1 })",
		"b:toggle('a', 'L', 'lock', function() end, function() end, { hold = 1 })",
		"b:gate('a', 'L', {})",
	} {
		engine := NewLuaEngine()
		_, err := engine.Execute("_dashboards = {}\nlocal ok, err = pcall(function()\n  local b = Dashboard.new('f'); b:section('S', 'g')\n  " + call + "\nend)\nerror(err, 0)")
		engine.Close()
		if err == nil || !strings.Contains(err.Error(), "<string>:4: ") {
			t.Errorf("builder %s: %v", call, err)
		}
	}
}
