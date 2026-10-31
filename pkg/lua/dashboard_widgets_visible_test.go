package lua

import (
	"strings"
	"testing"
)

// Every widget type takes opts.visible; while hidden it is not read, in the definition, in a
// poll, in its bars, and its actions are refused. A new widget type goes on this list.

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
		:build()
`

var dashWidgetsVisibleIDs = []string{"t", "g", "gt", "b", "ct", "th", "e"}

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
	} {
		if got := mustExec(t, engine, "return "+call); !strings.Contains(got, "hidden") {
			t.Errorf("%s on a hidden widget: %s", call, got)
		}
	}

	mustExec(t, engine, `W.show = true`)
	dashJSON(t, engine, `Dashboard.API.getValues("allvis")`, &vals)
	for _, id := range dashWidgetsVisibleIDs {
		if id == "b" {
			continue // bars come from /bars, not the poll
		}
		if _, ok := vals[id]; !ok {
			t.Errorf("shown %s missing from the values", id)
		}
	}
	if n := reads(); n != "6" {
		t.Errorf("shown widgets read: %s of 6", n)
	}
	if got := mustExec(t, engine, `return Dashboard.API.barsSpec("allvis", "b")`); !strings.Contains(got, "test:energy") {
		t.Errorf("bars of a shown widget: %s", got)
	}
}
