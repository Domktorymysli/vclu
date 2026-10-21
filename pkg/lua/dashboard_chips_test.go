package lua

import (
	"strings"
	"testing"
)

// Header chips (#93): an icon and a short text from a function, hidden while it gives nil.

const dashChipsSetup = `
	_dashboards = {}
	_chip = { wash = nil, alarm = 'armed' }
	Dashboard.new('chips')
		:header()
			:status('h_temp', 'Outside', 'thermometer-half', function() return 12.5 end, '°C')
			:chip('wake', 'alarm', function() return '6:30' end, { link = 'alarms' })
			:chip('wash', 'water', function() return _chip.wash end, { color = 'info' })
			:chip('sec', 'shield', function()
				if _chip.alarm == 'armed' then return { text = 'Armed', icon = 'shield-lock', color = 'danger' } end
				return { text = 'Off' }
			end, { color = function() return 'success' end, link = 'https://example.com/alarm' })
			:chip('broken', 'bug', function() error('no sensor') end)
			:chip('bare', 'moon', function() return { icon = 'moon-stars' } end)
			:chip('who', 'people', function() return 'Anna, Piotr' end,
				{ link = '/history', disabled = function() return _chip.away == true end })
		:section('Lights', 'lightbulb')
			:toggle('t1', 'Ceiling', 'lightbulb', function() return true end, function() end)
		:build()
`

func TestDashboardHeaderChips(t *testing.T) {
	engine := NewLuaEngine()
	defer engine.Close()
	if _, err := engine.Execute(dashChipsSetup); err != nil {
		t.Fatalf("setup failed: %v", err)
	}

	var def dashDef
	dashJSON(t, engine, `Dashboard.API.getDefinition("chips")`, &def)
	chipOf := func(w map[string]interface{}) map[string]interface{} {
		c, _ := w["chip"].(map[string]interface{})
		return c
	}

	wake := def.widget("wake")
	if wake["type"] != "chip" || wake["icon"] != "alarm" || wake["link"] != "/d/alarms" {
		t.Errorf("wake = %v", wake)
	}
	if c := chipOf(wake); c["text"] != "6:30" || c["icon"] != nil || c["color"] != nil {
		t.Errorf("wake chip = %v", c)
	}
	// hidden: the widget is there for the page, without a state
	if w := def.widget("wash"); w == nil || w["chip"] != nil {
		t.Errorf("wash = %v", w)
	}
	sec := def.widget("sec")
	if sec["link"] != "https://example.com/alarm" || sec["icon"] != "shield" {
		t.Errorf("sec = %v", sec)
	}
	if c := chipOf(sec); c["text"] != "Armed" || c["icon"] != "shield-lock" || c["color"] != "danger" {
		t.Errorf("sec chip = %v", c)
	}
	if c := chipOf(def.widget("broken")); !strings.Contains(c["error"].(string), "no sensor") || c["text"] != nil {
		t.Errorf("broken chip = %v", c)
	}
	if w := def.widget("who"); w["link"] != "/history" || w["disabled"] != false {
		t.Errorf("who = %v", w)
	}

	var vals map[string]map[string]interface{}
	dashJSON(t, engine, `Dashboard.API.getValues("chips")`, &vals)
	if v := vals["wake"]; len(v) != 1 || v["text"] != "6:30" {
		t.Errorf("wake values = %v", v)
	}
	if _, ok := vals["wash"]; ok {
		t.Errorf("hidden chip in values: %v", vals["wash"])
	}
	if v := vals["sec"]; v["text"] != "Armed" || v["icon"] != "shield-lock" || v["color"] != "danger" {
		t.Errorf("sec values = %v", v)
	}
	if v := vals["bare"]; v["text"] != "" || v["icon"] != "moon-stars" {
		t.Errorf("bare values = %v", v)
	}
	if v := vals["broken"]; !strings.Contains(v["error"].(string), "no sensor") {
		t.Errorf("broken values = %v", v)
	}
	// the other widgets keep working next to the broken chip
	if vals["h_temp"]["value"] != 12.5 || vals["t1"]["value"] != true {
		t.Errorf("other widgets = %v / %v", vals["h_temp"], vals["t1"])
	}
	if v := vals["who"]; v["text"] != "Anna, Piotr" || v["disabled"] != false {
		t.Errorf("who values = %v", v)
	}

	// the state changes: the washing machine starts, the alarm is disarmed (opts.color comes back)
	mustExec(t, engine, `_chip.wash = '45 min'; _chip.alarm = 'off'; _chip.away = true`)
	vals = nil
	dashJSON(t, engine, `Dashboard.API.getValues("chips")`, &vals)
	if v := vals["wash"]; v["text"] != "45 min" || v["color"] != "info" {
		t.Errorf("wash values = %v", v)
	}
	if v := vals["sec"]; v["text"] != "Off" || v["icon"] != nil || v["color"] != "success" {
		t.Errorf("sec values = %v", v)
	}
	if v := vals["who"]; v["disabled"] != true {
		t.Errorf("who values = %v", v)
	}

	var res map[string]interface{}
	dashJSON(t, engine, `Dashboard.API.executeAction("chips", "wake", "tap")`, &res)
	if res["error"] == nil {
		t.Errorf("a chip took an action: %v", res)
	}
}

func TestDashboardChipWrongReturnIsAnError(t *testing.T) {
	engine := NewLuaEngine()
	defer engine.Close()
	mustExec(t, engine, `
		_dashboards = {}
		Dashboard.new('c'):header():chip('b', 'x', function() return true end):build()
	`)
	var vals map[string]map[string]interface{}
	dashJSON(t, engine, `Dashboard.API.getValues("c")`, &vals)
	if e, _ := vals["b"]["error"].(string); !strings.Contains(e, `header:chip("b")`) || !strings.Contains(e, "boolean") {
		t.Errorf("values = %v", vals)
	}
}

// Wrong arguments fail at the module's line and name header:chip.
func TestDashboardChipArgumentErrors(t *testing.T) {
	cases := []struct{ call, want string }{
		{`:section('S', 'grid'):chip('a', 'x', function() end)`, "header only"},
		{`:header():chip('', 'x', function() end)`, "id must be"},
		{`:header():chip('a', nil, function() end)`, "icon must be"},
		{`:header():chip('a', 'x', 'text')`, "fn must be a function"},
		{`:header():chip('a', 'x', function() end, 'red')`, "opts must be a table"},
		{`:header():chip('a', 'x', function() end, { colour = 'red' })`, `unknown option "colour"`},
		{`:header():chip('a', 'x', function() end, { color = 5 })`, "opts.color"},
		{`:header():chip('a', 'x', function() end, { disabled = true })`, "opts.disabled"},
		{`:header():chip('a', 'x', function() end, { link = 'javascript:alert(1)' })`, "opts.link"},
		{`:header():chip('a', 'x', function() end, { link = '' })`, "opts.link"},
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
		if !strings.Contains(msg, "<string>:3: ") || !strings.Contains(msg, "header:chip") || !strings.Contains(msg, c.want) {
			t.Errorf("%s: %s", c.call, msg)
		}
	}
}
