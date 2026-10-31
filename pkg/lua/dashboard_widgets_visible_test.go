package lua

import (
	"strings"
	"testing"
)

// Every widget type takes opts.visible; while hidden it is not read, in the definition, in a
// poll, in its bars, and its actions are refused. A new widget type goes on this list (the
// older ones: dashboard_visible_test.go).

const dashWidgetsVisibleSetup = `
	_dashboards = {}
	W = { show = false, reads = {} }
	local function reader(name, v)
		return function() W.reads[name] = (W.reads[name] or 0) + 1; return v end
	end
	local shown = function() return W.show end
	Dashboard.new('allvis')
		:section('All', 'house')
			:table('t', 'Table', reader('table', { { a = 1 } }), { columns = { { key = 'a' } }, visible = shown })
			:gauge('g', 'Gauge', reader('gauge', 5), { visible = shown })
			:gate('gt', 'Gate', { open = function() W.opened = true end, state = reader('gate', 'closed'), visible = shown })
			:bars('b', 'Bars', 'test:energy', { visible = shown })
			:colorTemp('ct', 'CCT', { getKelvin = reader('colorTemp', 3000), setKelvin = function() end }, { visible = shown })
			:thermostat('th', 'Thermo', nil, {
				getCurrent = reader('thermostat', 21), getTarget = function() return 22 end, setTarget = function() end,
				getMode = function() return 'manual' end, setMode = function() end,
			}, { modes = { manual = 'Ręczny', eco = 'Eko' }, visible = shown })
			:energy('e', 'Energy', { pv = reader('energy', 1000), grid = function() return 200 end, visible = shown })
			:slider('sl', 'Slider', 0, 10, reader('slider', 5), function() end, { visible = shown })
			:select('se', 'Select', 'gear', { 'a', 'b' }, reader('select', 'a'), function() end, { visible = shown })
			:image('im', 'Image', reader('image', 'https://example.com/i.jpg'), { visible = shown })
			:dimmer('di', 'Dimmer', 'lightbulb', { get = reader('dimmer', 40), set = function() end }, { visible = shown })
			:color('co', 'Color', 'palette', { getColor = reader('color', { r = 1, g = 2, b = 3 }), setColor = function() end },
				{ visible = shown })
			:player('pl', 'Player', { getState = reader('player', { playing = true }), play = function() end }, { visible = shown })
			:forecast('fc', 'Forecast', reader('forecast', { days = {}, hours = {} }), { visible = shown })
		:build()
`

var dashWidgetsVisibleIDs = []string{"t", "g", "gt", "b", "ct", "th", "e", "sl", "se", "im", "di", "co", "pl", "fc"}

func TestDashboardEveryWidgetVisible(t *testing.T) {
	engine := NewLuaEngine()
	defer engine.Close()
	mustExec(t, engine, dashWidgetsVisibleSetup)

	reads := func() string {
		return mustExec(t, engine, `local n = 0; for _ in pairs(W.reads) do n = n + 1 end; return tostring(n)`)
	}

	var def struct {
		dashDef
		Hidden map[string]bool `json:"hidden"`
	}
	dashJSON(t, engine, `Dashboard.API.getDefinition("allvis")`, &def)
	for _, id := range dashWidgetsVisibleIDs {
		if !def.Hidden[id] {
			t.Errorf("%s not hidden in the definition: %v", id, def.Hidden)
		}
	}
	var vals map[string]interface{}
	dashJSON(t, engine, `Dashboard.API.getValues("allvis")`, &vals)
	for _, id := range dashWidgetsVisibleIDs {
		if _, ok := vals[id]; ok {
			t.Errorf("hidden %s in the values: %v", id, vals[id])
		}
	}
	if n := reads(); n != "0" {
		t.Errorf("hidden widgets read: %s", mustExec(t, engine, `local s = ''; for k, v in pairs(W.reads) do s = s .. k .. '=' .. v .. ' ' end; return s`))
	}
	if got := mustExec(t, engine, `return Dashboard.API.barsSpec("allvis", "b")`); !strings.Contains(got, "403") {
		t.Errorf("bars of a hidden widget: %s", got)
	}
	for _, call := range []string{
		`Dashboard.API.executeAction("allvis", "gt", "open", nil, { held = true })`,
		`Dashboard.API.executeAction("allvis", "ct", "setKelvin", 4000)`,
		`Dashboard.API.executeAction("allvis", "th", "mode", "eco")`,
		`Dashboard.API.executeAction("allvis", "sl", "set", 3)`,
		`Dashboard.API.executeAction("allvis", "se", "select", "b")`,
		`Dashboard.API.executeAction("allvis", "di", "set", 50)`,
		`Dashboard.API.executeAction("allvis", "pl", "play")`,
	} {
		if got := mustExec(t, engine, "return "+call); !strings.Contains(got, "hidden") {
			t.Errorf("%s on a hidden widget: %s", call, got)
		}
	}

	mustExec(t, engine, `W.show = true`)
	dashJSON(t, engine, `Dashboard.API.getValues("allvis")`, &vals)
	for _, id := range dashWidgetsVisibleIDs {
		if id == "b" || id == "im" {
			continue // bars come from /bars, an image's url with the definition
		}
		if _, ok := vals[id]; !ok {
			t.Errorf("shown %s missing from the values", id)
		}
	}
	// the image's url is read for the definition, not the poll
	dashJSON(t, engine, `Dashboard.API.getDefinition("allvis")`, &def)
	if n := reads(); n != "13" {
		t.Errorf("shown widgets read: %s of 13: %s", n, mustExec(t, engine, `local s = ''; for k in pairs(W.reads) do s = s .. k .. ' ' end; return s`))
	}
	if got := mustExec(t, engine, `return Dashboard.API.barsSpec("allvis", "b")`); !strings.Contains(got, "test:energy") {
		t.Errorf("bars of a shown widget: %s", got)
	}
}

// A hidden widget's action is refused before its disabled() is called.
func TestDashboardHiddenWidgetDisabledNotRead(t *testing.T) {
	engine := NewLuaEngine()
	defer engine.Close()
	mustExec(t, engine, `
		_dashboards = {}
		H = { show = false, disabled = 0 }
		Dashboard.new('hd'):section('S')
			:button('b', 'B', 'x', function() end, {
				visible = function() return H.show end,
				disabled = function() H.disabled = H.disabled + 1; return false end })
		:build()
	`)
	if got := mustExec(t, engine, `return Dashboard.API.executeAction("hd", "b", "tap")`); !strings.Contains(got, "Widget is hidden") {
		t.Errorf("tap on a hidden button: %s", got)
	}
	if n := mustExec(t, engine, `return H.disabled`); n != "0" {
		t.Errorf("disabled() of a hidden widget called %s times", n)
	}
	mustExec(t, engine, `H.show = true`)
	if got := mustExec(t, engine, `return Dashboard.API.executeAction("hd", "b", "tap")`); !strings.Contains(got, "success") {
		t.Errorf("tap on a shown button: %s", got)
	}
}
