package lua

import (
	"strings"
	"testing"
)

// Cover groups (#97): one tile moves several covers, commands spaced on timers.

// A virtual clock under TIMERS (what setTimeout and after use), a CLU that records every
// script with the time it was sent, three Grenton rollers (the second one reports a tilt).
const dashCoversSetup = `
	_dashboards = {}
	T = { now = 0, q = {}, seq = 0 }
	function T.advance(ms)
		local target = T.now + ms
		while true do
			local best, bid
			for id, t in pairs(T.q) do
				if t.at <= target and (not best or t.at < best.at or (t.at == best.at and id < bid)) then best, bid = t, id end
			end
			if not best then break end
			T.q[bid] = nil
			T.now = best.at
			best.fn()
		end
		T.now = target
	end
	TIMERS.setTimeout = function(self, ms, fn)
		T.seq = T.seq + 1
		T.q[T.seq] = { at = T.now + ms, fn = StateContext.bind(fn) }
		return T.seq
	end
	TIMERS.clearTimer = function(self, id) T.q[id] = nil end

	SENT = {}
	CLU = { executeNoReply = function(self, n, s) SENT[#SENT + 1] = { at = T.now, s = s } end }
	function sent()
		local out = {}
		for i, e in ipairs(SENT) do out[i] = e.at .. ' ' .. e.s end
		SENT = {}
		return table.concat(out, '\n')
	end
	function roller(id, pos)
		local c = RemoteCover:new(CLU, id, id)
		c._hasPosition = true
		c._lastState = pos
		c._movement = 0
		_registry['CLU1.' .. id] = c
		return c
	end
	A = roller('ROL1', 100)
	B = roller('ROL2', 40)
	B._lastTilt = 30
	C = roller('ROL3', 0)
	X = { off = false }

	Dashboard.new('cv')
		:section('Rolety', 'window', { covers = true })
			:covers('all', 'Wszystkie', { 'CLU1.ROL1', B, 'CLU1.ROL3', 'CLU1.ROL9', B })
			:cover('single', 'Okno', 'window', { getPosition = function() return 70 end, getState = function() return 0 end,
				open = function() end, close = function() end, stop = function() end })
		:section('Południe', 'sun')
			:covers('south', 'Południe', function() return _:byTag('south') end,
				{ icon = 'sun', disabled = function() return X.off end })
		:build()
`

func newCoversEngine(t *testing.T) *LuaEngine {
	t.Helper()
	engine := NewLuaEngine()
	t.Cleanup(engine.Close)
	mustExec(t, engine, dashCoversSetup)
	return engine
}

func coversAction(t *testing.T, engine *LuaEngine, widget, action, value string) map[string]interface{} {
	t.Helper()
	var res map[string]interface{}
	dashJSON(t, engine, `Dashboard.API.executeAction("cv", "`+widget+`", "`+action+`", `+value+`)`, &res)
	return res
}

func TestDashboardCoversDefinition(t *testing.T) {
	engine := newCoversEngine(t)

	var def struct {
		Sections []map[string]interface{} `json:"sections"`
		dashDef
	}
	dashJSON(t, engine, `Dashboard.API.getDefinition("cv")`, &def)
	all := def.widget("all")
	// A 100, B 40, C 0; the missing path and the second B are skipped
	if all["type"] != "covers" || all["icon"] != "window" || all["label"] != "Wszystkie" ||
		all["open"] != 2.0 || all["closed"] != 1.0 || all["moving"] != 0.0 ||
		all["position"] != 47.0 || all["tilt"] != 30.0 || all["set"] != true {
		t.Errorf("all = %v", all)
	}
	south := def.widget("south")
	if south["icon"] != "sun" || south["open"] != 0.0 || south["position"] != nil || south["tilt"] != nil || south["set"] != false {
		t.Errorf("south = %v", south)
	}
	// the header counts the cover tile and the group's covers
	if c, _ := def.Sections[0]["covers"].(map[string]interface{}); c["open"] != 3.0 || c["closed"] != 1.0 || c["moving"] != 0.0 {
		t.Errorf("section covers = %v", def.Sections[0])
	}
	if _, ok := def.Sections[1]["covers"]; ok {
		t.Errorf("a section without covers = true has counts: %v", def.Sections[1])
	}
}

// Each button reaches every cover, 50 ms apart; the action returns after the first one.
func TestDashboardCoversButtonsSpaced(t *testing.T) {
	engine := newCoversEngine(t)
	for _, c := range []struct{ action, cmd string }{
		{"open", "execute(10, 100, 91)"},
		{"stop", "execute(3)"},
		{"close", "execute(10, 0, 91)"},
	} {
		if res := coversAction(t, engine, "all", c.action, "nil"); res["success"] != true {
			t.Fatalf("%s: %v", c.action, res)
		}
		// not blocking: only the first command went out
		if got := mustExec(t, engine, `return sent()`); got != "0 ROL1:"+c.cmd {
			t.Errorf("%s at once:\n%s", c.action, got)
		}
		if got := mustExec(t, engine, `T.advance(49); return sent()`); got != "" {
			t.Errorf("%s before 50 ms:\n%s", c.action, got)
		}
		got := mustExec(t, engine, `T.advance(1000); return sent()`)
		want := "50 ROL2:" + c.cmd + "\n100 ROL3:" + c.cmd
		if got != want {
			t.Errorf("%s later:\n%s\nwant:\n%s", c.action, got, want)
		}
		mustExec(t, engine, `T.now = 0`)
	}

	if res := coversAction(t, engine, "all", "set", "35"); res["success"] != true || res["value"] != 35.0 {
		t.Fatalf("set: %v", res)
	}
	got := mustExec(t, engine, `T.advance(1000); return sent()`)
	if got != "0 ROL1:execute(10, 35, 91)\n50 ROL2:execute(10, 35, 91)\n100 ROL3:execute(10, 35, 91)" {
		t.Errorf("set:\n%s", got)
	}
	for _, bad := range []string{"101", "-1", "'x'"} {
		if res := coversAction(t, engine, "all", "set", bad); res["error"] == nil {
			t.Errorf("set %s: %v", bad, res)
		}
	}
}

func TestDashboardCoversTiltOnlyWhereReported(t *testing.T) {
	engine := newCoversEngine(t)
	if res := coversAction(t, engine, "all", "tilt", "45"); res["success"] != true {
		t.Fatalf("tilt: %v", res)
	}
	if got := mustExec(t, engine, `T.advance(1000); return sent()`); got != "0 ROL2:execute(9, 45)" {
		t.Errorf("tilt:\n%s", got)
	}
	if res := coversAction(t, engine, "all", "tilt", "91"); res["error"] == nil {
		t.Errorf("tilt 91: %v", res)
	}
	// a group where no cover reports a tilt refuses it
	mustExec(t, engine, `V = VirtualCover:new('v1'); V:addTag('south')`)
	if res := coversAction(t, engine, "south", "tilt", "10"); res["error"] == nil {
		t.Errorf("tilt without a tilt cover: %v", res)
	}
}

// "Up" then quickly "stop": the covers not reached yet get only the stop.
func TestDashboardCoversNewCommandCancelsSeries(t *testing.T) {
	engine := newCoversEngine(t)
	coversAction(t, engine, "all", "open", "nil")
	mustExec(t, engine, `T.advance(10)`)
	coversAction(t, engine, "all", "stop", "nil")
	got := mustExec(t, engine, `T.advance(1000); return sent()`)
	want := "0 ROL1:execute(10, 100, 91)\n10 ROL1:execute(3)\n60 ROL2:execute(3)\n110 ROL3:execute(3)"
	if got != want {
		t.Errorf("open then stop:\n%s\nwant:\n%s", got, want)
	}
	// the cancelled timer is gone, not just ignored
	if n := mustExec(t, engine, `local n = 0; for k in pairs(T.q) do n = n + 1 end; return tostring(n)`); n != "0" {
		t.Errorf("%s timers left", n)
	}
}

// Counts and the average follow positions and movement, the section header too.
func TestDashboardCoversCountsFollowState(t *testing.T) {
	engine := newCoversEngine(t)
	type vals map[string]map[string]interface{}
	read := func() vals {
		var v vals
		dashJSON(t, engine, `Dashboard.API.getValues("cv")`, &v)
		return v
	}

	coversAction(t, engine, "all", "close", "nil")
	mustExec(t, engine, `T.advance(1000)`)
	v := read()
	if a := v["all"]; a["moving"] != 3.0 || a["open"] != 0.0 || a["closed"] != 0.0 || a["disabled"] != false {
		t.Errorf("moving: %v", a)
	}
	if s := v["_section_0"]; s["moving"] != 3.0 || s["open"] != 1.0 || s["closed"] != 0.0 {
		t.Errorf("section while moving: %v", s)
	}
	if _, ok := v["_section_1"]; ok {
		t.Errorf("counts for a section without covers = true: %v", v["_section_1"])
	}

	// the CLU reports: two stopped closed, one stopped half way
	mustExec(t, engine, `A._movement = 0; A._lastState = 0; B._movement = 0; B._lastState = 50; C._movement = 0; C._lastState = 0`)
	v = read()
	if a := v["all"]; a["moving"] != 0.0 || a["open"] != 1.0 || a["closed"] != 2.0 || a["position"] != 17.0 {
		t.Errorf("stopped: %v", a)
	}
	mustExec(t, engine, `B._lastState = 0`)
	if a := read()["all"]; a["open"] != 0.0 || a["closed"] != 3.0 || a["position"] != 0.0 {
		t.Errorf("all closed: %v", a)
	}
}

// A function list is read each time: a cover tagged later shows up and takes commands.
func TestDashboardCoversFunctionListAndDisabled(t *testing.T) {
	engine := newCoversEngine(t)
	var v map[string]map[string]interface{}
	dashJSON(t, engine, `Dashboard.API.getValues("cv")`, &v)
	if s := v["south"]; s["open"] != 0.0 || s["closed"] != 0.0 || s["position"] != nil {
		t.Errorf("empty south: %v", s)
	}
	mustExec(t, engine, `V = VirtualCover:new('v1', { initial = 60 }); V:addTag('south')`)
	v = nil
	dashJSON(t, engine, `Dashboard.API.getValues("cv")`, &v)
	if s := v["south"]; s["open"] != 1.0 || s["position"] != 60.0 || s["set"] != true || s["tilt"] != nil {
		t.Errorf("south with a cover: %v", s)
	}
	coversAction(t, engine, "south", "close", "nil")
	if p := mustExec(t, engine, `return tostring(V:getPosition())`); p != "0" {
		t.Errorf("virtual cover after close: %s", p)
	}

	mustExec(t, engine, `X.off = true`)
	if res := coversAction(t, engine, "south", "open", "nil"); res["error"] != "Widget is disabled" {
		t.Errorf("disabled: %v", res)
	}
	if p := mustExec(t, engine, `return tostring(V:getPosition())`); p != "0" {
		t.Errorf("disabled group moved the cover: %s", p)
	}
	v = nil
	dashJSON(t, engine, `Dashboard.API.getValues("cv")`, &v)
	if v["south"]["disabled"] != true {
		t.Errorf("south values: %v", v["south"])
	}
}

// A list function that fails or returns nothing leaves an empty tile, the rest keeps working.
func TestDashboardCoversBrokenList(t *testing.T) {
	engine := NewLuaEngine()
	defer engine.Close()
	mustExec(t, engine, `
		_dashboards = {}
		Dashboard.new('b'):section('S', 'grid', { covers = true })
			:covers('err', 'E', function() error('no tag') end)
			:covers('nil', 'N', function() return nil end)
			:toggle('t', 'T', 'x', function() return true end, function() end)
		:build()
	`)
	var v map[string]map[string]interface{}
	dashJSON(t, engine, `Dashboard.API.getValues("b")`, &v)
	if v["err"]["open"] != 0.0 || v["nil"]["open"] != 0.0 || v["t"]["value"] != true || v["_section_0"]["open"] != 0.0 {
		t.Errorf("values = %v", v)
	}
	var res map[string]interface{}
	dashJSON(t, engine, `Dashboard.API.executeAction("b", "err", "open")`, &res)
	if res["success"] != true {
		t.Errorf("open on an empty group: %v", res)
	}
}

// Wrong arguments fail at the module's line and name section:covers.
func TestDashboardCoversArgumentErrors(t *testing.T) {
	cases := []struct{ call, want string }{
		{`:section('S', 'grid'):covers('', 'L', {})`, "id must be"},
		{`:section('S', 'grid'):covers('a', nil, {})`, "label must be"},
		{`:section('S', 'grid'):covers('a', 'L', 'CLU1.ROL1')`, "list must be"},
		{`:section('S', 'grid'):covers('a', 'L', { 5 })`, "item 1 must be a cover"},
		{`:section('S', 'grid'):covers('a', 'L', { {} })`, "item 1 must be a cover"},
		{`:section('S', 'grid'):covers('a', 'L', VirtualCover:new('x'))`, "put the cover in { }"},
		{`:section('S', 'grid'):covers('a', 'L', {}, 'x')`, "opts must be a table"},
		{`:section('S', 'grid'):covers('a', 'L', {}, { colour = 1 })`, `unknown option "colour"`},
		{`:section('S', 'grid'):covers('a', 'L', {}, { icon = 'x y' })`, "opts.icon"},
		{`:section('S', 'grid'):covers('a', 'L', {}, { disabled = true })`, "opts.disabled"},
		{`:header():covers('a', 'L', {})`, "not the header"},
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
		if !strings.Contains(msg, "<string>:3: ") || !strings.Contains(msg, "section:covers") || !strings.Contains(msg, c.want) {
			t.Errorf("%s: %s", c.call, msg)
		}
	}
}

// Movement and slats are separate series: a tilt does not cut a set short, a set not a tilt;
// stop ends both.
func TestDashboardCoversMoveAndTiltSeries(t *testing.T) {
	engine := newCoversEngine(t)
	coversAction(t, engine, "all", "set", "30")
	mustExec(t, engine, `T.advance(10)`)
	coversAction(t, engine, "all", "tilt", "80")
	got := mustExec(t, engine, `T.advance(1000); return sent()`)
	want := "0 ROL1:execute(10, 30, 91)\n10 ROL2:execute(9, 80)\n50 ROL2:execute(10, 30, 91)\n100 ROL3:execute(10, 30, 91)"
	if got != want {
		t.Errorf("set then tilt:\n%s\nwant:\n%s", got, want)
	}

	mustExec(t, engine, `T.now = 0; C._lastTilt = 10`)
	coversAction(t, engine, "all", "tilt", "60")
	mustExec(t, engine, `T.advance(10)`)
	coversAction(t, engine, "all", "stop", "nil")
	got = mustExec(t, engine, `T.advance(1000); return sent()`)
	want = "0 ROL2:execute(9, 60)\n10 ROL1:execute(3)\n60 ROL2:execute(3)\n110 ROL3:execute(3)"
	if got != want {
		t.Errorf("tilt then stop:\n%s\nwant:\n%s", got, want)
	}
}

// A group disabled while its series runs sends nothing more.
func TestDashboardCoversDisabledStopsSeries(t *testing.T) {
	engine := newCoversEngine(t)
	mustExec(t, engine, `A:addTag('south'); B:addTag('south'); C:addTag('south')`)
	coversAction(t, engine, "south", "open", "nil")
	got := mustExec(t, engine, `X.off = true; T.advance(1000); return sent()`)
	if got != "0 ROL1:execute(10, 100, 91)" {
		t.Errorf("disabled mid-series:\n%s", got)
	}
}

// A cover without a reading counts as unknown, never as closed.
func TestDashboardCoversUnknown(t *testing.T) {
	engine := newCoversEngine(t)
	mustExec(t, engine, `
		D = RemoteCover:new(CLU, 'ROL4', 'ROL4')
		D._hasPosition = true
		E = RemoteCover:new(CLU, 'ROL5', 'ROL5')
		Dashboard.new('u'):section('S', 'grid', { covers = true })
			:covers('mixed', 'M', { C, D })
			:covers('none', 'N', { D, E })
		:build()
	`)
	var v map[string]map[string]interface{}
	dashJSON(t, engine, `Dashboard.API.getValues("u")`, &v)
	if m := v["mixed"]; m["closed"] != 1.0 || m["unknown"] != 1.0 || m["open"] != 0.0 || m["position"] != 0.0 {
		t.Errorf("mixed = %v", m)
	}
	if n := v["none"]; n["unknown"] != 2.0 || n["closed"] != 0.0 || n["position"] != nil {
		t.Errorf("none = %v", n)
	}
	if s := v["_section_0"]; s["unknown"] != 2.0 || s["closed"] != 1.0 {
		t.Errorf("section = %v", s)
	}
}

// Ids the page uses for its own keys are refused at the module's line.
func TestDashboardReservedWidgetIDs(t *testing.T) {
	for _, call := range []string{
		`:section('S', 'grid'):covers('_section_0', 'L', {})`,
		`:section('S', 'grid'):toggle('_group_1', 'L', 'x', function() end, function() end)`,
		`:header():chip('_section_x', 'x', function() end)`,
	} {
		engine := NewLuaEngine()
		_, err := engine.Execute("_dashboards = {}\nlocal ok, err = pcall(function()\n  Dashboard.new('e')" + call + "\nend)\nerror(err, 0)")
		engine.Close()
		if err == nil || !strings.Contains(err.Error(), "<string>:3: ") || !strings.Contains(err.Error(), "reserved") {
			t.Errorf("%s: %v", call, err)
		}
	}
}
