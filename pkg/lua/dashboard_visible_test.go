package lua

import (
	"strings"
	"testing"
)

// opts.visible hides a widget, a group or a section while it returns false or nil; read on
// every values poll, so the page follows the state. Tests go through Dashboard.API.

const dashVisibleSetup = `
	_dashboards = {}
	V = { alarm = false, open = 0, reads = { lamp = 0 }, warns = {} }
	Logger.warn = function(self, msg, ...)
		local s = self:format(msg, ...)
		if s:find("Visible error", 1, true) then V.warns[#V.warns + 1] = s end
	end
	Dashboard.new('vis')
		:header()
			:status('h_temp', 'Outside', 'thermometer-half', function() return 12 end, '°C',
				{ visible = function() return V.alarm end })
			:chip('c_alarm', 'shield', function() return 'Armed' end, { visible = function() return V.alarm end })
		:section('Main', 'house')
			:toggle('lamp', 'Lamp', 'lightbulb', function() V.reads.lamp = V.reads.lamp + 1; return true end, function() end,
				{ visible = function() return V.open > 0 end })
			:button('btn', 'Gate', 'door-open', function() end, { visible = function() return not V.alarm end })
			:status('broken', 'Broken', 'bug', function() return 1 end, '', { visible = function() error('no sensor') end })
			:group('Evening', 'moon', { visible = function() return V.alarm end })
			:link('l1', 'Help', 'info', 'https://example.com')
			:status('s1', 'Temp', 'thermometer', function() return 20 end, '°C')
			:group('Rest')
			:button('b2', 'Ring', 'bell', function() end, { visible = function() return V.open > 1 end })
		:section('Alarm', 'shield', { visible = function() return V.alarm end })
			:button('arm', 'Arm', 'shield-lock', function() end)
		:section('Covers', 'window')
			:cover('cv', 'Window', 'window', { getPosition = function() return 0 end },
				{ visible = function() return V.open > 0 end })
		:build()
`

func TestDashboardVisible(t *testing.T) {
	engine := NewLuaEngine()
	defer engine.Close()
	mustExec(t, engine, dashVisibleSetup)

	var def struct {
		dashDef
		Hidden map[string]bool `json:"hidden"`
	}
	dashJSON(t, engine, `Dashboard.API.getDefinition("vis")`, &def)
	// a hidden widget is still in the definition, so the page can show it later
	if def.widget("lamp") == nil || def.widget("arm") == nil {
		t.Fatalf("hidden widgets left out of the definition: %v", def.Widgets)
	}
	// the group "Evening" hides what follows it; "Rest", section 3 (covers): none of their widgets
	// shown; section 2 (alarm): its own visible
	want := map[string]bool{
		"h_temp": true, "lamp": true, "b2": true, "cv": true,
		"_section_1/_group_3": true, "l1": true, "s1": true,
		"_section_1/_group_6": true, "_section_2": true, "arm": true, "_section_3": true,
	}
	if !equalBoolMaps(def.Hidden, want) {
		t.Errorf("definition hidden = %v, want %v", def.Hidden, want)
	}
	// a hidden chip has no state
	if c := def.widget("c_alarm"); c == nil || c["chip"] != nil {
		t.Errorf("hidden chip = %v", c)
	}

	var vals map[string]map[string]interface{}
	dashJSON(t, engine, `Dashboard.API.getValues("vis")`, &vals)
	hidden := map[string]bool{}
	for k, v := range vals["_hidden"] {
		hidden[k] = v == true
	}
	if !equalBoolMaps(hidden, want) {
		t.Errorf("values _hidden = %v, want %v", hidden, want)
	}
	// hidden widgets are not read
	for _, id := range []string{"lamp", "h_temp", "c_alarm", "cv"} {
		if _, ok := vals[id]; ok {
			t.Errorf("hidden %s in values: %v", id, vals[id])
		}
	}
	// not in the definition either: described, not read
	if n := mustExec(t, engine, `return V.reads.lamp`); n != "0" {
		t.Errorf("lamp read %s times", n)
	}
	if w := def.widget("lamp"); w["value"] != nil || w["type"] != "toggle" || w["label"] != "Lamp" {
		t.Errorf("hidden lamp in the definition = %v", w)
	}
	if _, ok := def.widget("lamp")["value"]; ok {
		t.Errorf("hidden lamp has a value in the definition: %v", def.widget("lamp"))
	}
	// an error in visible shows the widget
	if vals["broken"]["value"] != 1.0 {
		t.Errorf("broken = %v", vals["broken"])
	}

	var res map[string]interface{}
	dashJSON(t, engine, `Dashboard.API.executeAction("vis", "arm", "tap")`, &res)
	if res["error"] != "Widget is hidden" {
		t.Errorf("tap on a hidden widget = %v", res)
	}
	res = nil
	dashJSON(t, engine, `Dashboard.API.executeAction("vis", "btn", "tap")`, &res)
	if res["error"] != nil {
		t.Errorf("tap on a shown widget = %v", res)
	}

	// the state changes: shown on the next poll
	mustExec(t, engine, `V.alarm = true; V.open = 1`)
	vals = nil
	dashJSON(t, engine, `Dashboard.API.getValues("vis")`, &vals)
	hidden = map[string]bool{}
	for k := range vals["_hidden"] {
		hidden[k] = true
	}
	if !equalBoolMaps(hidden, map[string]bool{"btn": true, "b2": true, "_section_1/_group_6": true}) {
		t.Errorf("after the change _hidden = %v", hidden)
	}
	if vals["lamp"]["value"] != true || vals["h_temp"]["value"] != 12.0 || vals["c_alarm"]["text"] != "Armed" {
		t.Errorf("shown widgets = %v / %v / %v", vals["lamp"], vals["h_temp"], vals["c_alarm"])
	}
	res = nil
	dashJSON(t, engine, `Dashboard.API.executeAction("vis", "arm", "tap")`, &res)
	if res["error"] != nil {
		t.Errorf("tap after it is shown = %v", res)
	}

	// all shown (any value but false and nil shows): no _hidden at all
	mustExec(t, engine, `Dashboard.new('vis'):section('S')
		:status('s', 'S', 'x', function() return 1 end, '', { visible = function() return V.open end }):build()`)
	vals = nil
	dashJSON(t, engine, `Dashboard.API.getValues("vis")`, &vals)
	if _, ok := vals["_hidden"]; ok {
		t.Errorf("nothing hidden, yet _hidden = %v", vals["_hidden"])
	}

	// the broken function was logged once over all those polls
	if n := mustExec(t, engine, `return #V.warns`); n != "1" {
		t.Errorf("visible error logged %s times: %s", n, mustExec(t, engine, `return table.concat(V.warns, "\n")`))
	}
}

func equalBoolMaps(a, b map[string]bool) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

// A cover group, a room tile and the sections of an area or a floor take visible too; a hidden
// room tile takes its room's widgets along (not read, actions refused).
func TestDashboardVisibleAreasAndCovers(t *testing.T) {
	engine := newRoomEngine(t, `
		V = { on = false }
		local function on() return V.on end
		Dashboard.new('va')
			:section('Rooms', 'house')
				:room('r', 'Sypialnia', { visible = on })
				:covers('all', 'All covers', { 'syp.co', 'syp.cc' }, { visible = on })
			:section('Kitchen', 'egg', { visible = on }):area('Kuchnia')
			:build()
		Dashboard.new('vf'):floor('Piętro', { visible = on }):build()
	`)
	var vals map[string]map[string]interface{}
	dashJSON(t, engine, `Dashboard.API.getValues("va")`, &vals)
	h := vals["_hidden"]
	for _, k := range []string{"r", "all", "_section_0", "_section_1", "area:kuch.l"} {
		if h[k] != true {
			t.Errorf("%s not hidden: %v", k, h)
		}
	}
	// the room's overlay is section 2
	if h["_section_2"] == true {
		t.Errorf("the room's overlay is hidden: %v", h)
	}
	if _, ok := vals["r"]; ok {
		t.Errorf("hidden room tile read: %v", vals["r"])
	}
	// the room's widgets go with the tile: the page needs no key for them
	if _, ok := vals["area:syp.l1"]; ok || h["area:syp.l1"] == true {
		t.Errorf("overlay widget of a hidden room: %v / %v", vals["area:syp.l1"], h)
	}
	var res map[string]interface{}
	dashJSON(t, engine, `Dashboard.API.executeAction("va", "area:syp.l1", "toggle", true)`, &res)
	if res["error"] != "Widget is hidden" {
		t.Errorf("action in a hidden room = %v", res)
	}

	var floor map[string]map[string]interface{}
	dashJSON(t, engine, `Dashboard.API.getValues("vf")`, &floor)
	for _, k := range []string{"_section_0", "_section_1", "_section_2"} {
		if floor["_hidden"][k] != true {
			t.Errorf("floor section %s not hidden: %v", k, floor["_hidden"])
		}
	}

	mustExec(t, engine, `V.on = true`)
	vals = nil
	dashJSON(t, engine, `Dashboard.API.getValues("va")`, &vals)
	if _, ok := vals["_hidden"]; ok {
		t.Errorf("all shown, yet _hidden = %v", vals["_hidden"])
	}
	if vals["r"] == nil || vals["all"] == nil || vals["area:syp.l1"] == nil {
		t.Errorf("shown tiles missing: %v / %v / %v", vals["r"], vals["all"], vals["area:syp.l1"])
	}
	res = nil
	dashJSON(t, engine, `Dashboard.API.executeAction("va", "area:syp.l1", "toggle", true)`, &res)
	if res["error"] != nil {
		t.Errorf("action in a shown room = %v", res)
	}
}

// A section's cover counts leave hidden tiles out; a hidden chart or calendar gets no data.
func TestDashboardVisibleCountsChartsCalendars(t *testing.T) {
	engine := NewLuaEngine()
	defer engine.Close()
	mustExec(t, engine, `
		_dashboards = {}
		V = { on = false }
		local function on() return V.on end
		Calendar.new('dom'):clock({ wait = false }):event('sport', { title = 'Sport', every = 'day', time = '18:00' }):build()
		Dashboard.new('vc')
			:section('Covers', 'window', { covers = true })
				:cover('open', 'Open', 'window', { getPosition = function() return 50 end, getState = function() return 0 end })
				:cover('hid', 'Hidden', 'window', { getPosition = function() return 80 end, getState = function() return 0 end },
					{ visible = on })
			:section('Data', 'graph-up')
				:chart('ch', 'Power', 'power.w', { visible = on })
				:calendar('kal', 'Calendar', 'dom', { visible = on })
			:build()
	`)
	var vals map[string]map[string]interface{}
	dashJSON(t, engine, `Dashboard.API.getValues("vc")`, &vals)
	if c := vals["_section_0"]; c["open"] != 1.0 {
		t.Errorf("cover counts with a hidden tile = %v", c)
	}
	var used map[string]interface{}
	dashJSON(t, engine, `Dashboard.API.usesMetric("vc", "power.w")`, &used)
	if used["used"] != false {
		t.Errorf("hidden chart's metric = %v", used)
	}
	var rng map[string]interface{}
	dashJSON(t, engine, `Dashboard.API.calendarRange("vc", "kal", "2026-10-26", "2026-10-26")`, &rng)
	if rng["status"] != 403.0 {
		t.Errorf("hidden calendar's range = %v", rng)
	}

	mustExec(t, engine, `V.on = true`)
	vals, used, rng = nil, nil, nil
	dashJSON(t, engine, `Dashboard.API.getValues("vc")`, &vals)
	if c := vals["_section_0"]; c["open"] != 2.0 {
		t.Errorf("cover counts = %v", c)
	}
	dashJSON(t, engine, `Dashboard.API.usesMetric("vc", "power.w")`, &used)
	if used["used"] != true {
		t.Errorf("shown chart's metric = %v", used)
	}
	dashJSON(t, engine, `Dashboard.API.calendarRange("vc", "kal", "2026-10-26", "2026-10-26")`, &rng)
	if rng["error"] != nil {
		t.Errorf("shown calendar's range = %v", rng)
	}
}

// A visible that is not a function fails at the module's line.
func TestDashboardVisibleArgumentErrors(t *testing.T) {
	cases := []struct{ call, want string }{
		{`:section('S'):button('a', 'A', 'x', function() end, { visible = true })`, `widget "a": opts.visible must be a function`},
		{`:section('S', 'x', { visible = false })`, `section("S"): opts.visible must be a function`},
		{`:section('S'):section('T', 'x', { visible = 1 })`, `section("T"): opts.visible must be a function`},
		{`:section('S'):group('G', 'x', { visible = 'no' })`, `group "G": opts.visible must be a function`},
		{`:section('S'):group('G', 'x', true)`, `section:group("G"): opts must be a table`},
		{`:section('S'):group('G', 'x', { visibel = function() end })`, `section:group("G"): unknown option "visibel" (visible)`},
		{`:section('S'):covers('c', 'C', {}, { visible = true })`, `opts.visible must be a function`},
		{`:header():chip('c', 'x', function() end, { visible = true })`, `opts.visible must be a function`},
		{`:section('S'):room('r', 'Salon', { visible = true })`, `section:room("r"): visible must be a function`},
		{`:floor('Parter', { visible = true })`, `dash:floor: visible must be a function`},
		{`:section('S'):button('_hidden', 'A', 'x', function() end)`, `reserved`},
	}
	for _, c := range cases {
		engine := NewLuaEngine()
		_, err := engine.Execute("_dashboards = {}\nlocal ok, err = pcall(function()\n  Dashboard.new('e')" + c.call + "\nend)\nerror(err, 0)")
		engine.Close()
		if err == nil {
			t.Errorf("%s: no error", c.call)
			continue
		}
		if msg := err.Error(); !strings.Contains(msg, "<string>:3: ") || !strings.Contains(msg, c.want) {
			t.Errorf("%s: %s", c.call, msg)
		}
	}
}
