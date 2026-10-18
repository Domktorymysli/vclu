package lua

import (
	"strings"
	"testing"
)

// A colour widget given a Grenton RGB light reads what the light keeps (no CLU query on a
// poll), shows white only for an RGBW module, and drives colour, brightness and white (#41).

const dashColorLightSetup = `
	_dashboards = {}
	_sent = {}
	local clu = {
		executeNoReply = function(s, i, cmd) table.insert(_sent, cmd) end,
		execute = function(s, i, cmd) table.insert(_sent, "QUERY " .. cmd) end,
	}
	_rgb = RemoteRGBLight:new(clu, "LED1", "salon")
	_rgbw = RemoteRGBLight:new(clu, "LED2", "kuchnia")
	_rgbw._hasWhite = true
	Dashboard.new('led')
		:section('S', 'icon')
		:color('rgb', 'Salon', 'palette', _rgb)
		:color('rgbw', 'Kuchnia', 'palette', _rgbw)
		:build()
`

func TestDashboardColorWidgetFromLight(t *testing.T) {
	engine := NewLuaEngine()
	defer engine.Close()
	if _, err := engine.Execute(dashColorLightSetup); err != nil {
		t.Fatalf("setup failed: %v", err)
	}

	var def dashDef
	dashJSON(t, engine, `Dashboard.API.getDefinition("led")`, &def)
	caps := func(id string) map[string]interface{} {
		c, _ := def.widget(id)["capabilities"].(map[string]interface{})
		return c
	}
	if c := caps("rgb"); c["white"] != false || c["brightness"] != true || c["toggle"] != true {
		t.Errorf("rgb capabilities = %v", c)
	}
	if c := caps("rgbw"); c["white"] != true {
		t.Errorf("rgbw capabilities = %v", c)
	}

	// reports as the subscription delivers them
	mustExec(t, engine, `
		_rgbw:fromFeature(6, '"#ff5500"')
		_rgbw:fromFeature(15, 128)
		_rgbw:updateState(_rgbw:fromFeature(0, 0.5))
	`)
	var vals map[string]map[string]interface{}
	dashJSON(t, engine, `Dashboard.API.getValues("led")`, &vals)
	v := vals["rgbw"]
	rgb, _ := v["rgb"].(map[string]interface{})
	if rgb["r"] != 255.0 || rgb["g"] != 85.0 || rgb["b"] != 0.0 || v["brightness"] != 50.0 || v["white"] != 50.0 || v["on"] != true {
		t.Errorf("rgbw values = %v", v)
	}
	if _, ok := vals["rgb"]["rgb"]; ok {
		t.Errorf("rgb before any report has no colour: %v", vals["rgb"])
	}
	if got := mustExec(t, engine, `table.concat(_sent, ";")`); strings.Contains(got, "QUERY") {
		t.Errorf("a poll asked the CLU: %s", got)
	}

	// actions reach the light on the CLU's scales
	mustExec(t, engine, `_sent = {}`)
	for _, a := range []string{
		`Dashboard.API.executeAction("led", "rgbw", "setColor", {r = 0, g = 128, b = 255})`,
		`Dashboard.API.executeAction("led", "rgbw", "set", 25)`,
		`Dashboard.API.executeAction("led", "rgbw", "setWhite", 100)`,
		`Dashboard.API.executeAction("led", "rgbw", "switchOff")`,
	} {
		if res := mustExec(t, engine, a); !strings.Contains(res, `"success":true`) {
			t.Errorf("%s: %s", a, res)
		}
	}
	got := mustExec(t, engine, `table.concat(_sent, ";")`)
	for _, want := range []string{`LED2:execute(6, "#0080ff")`, `LED2:execute(0, 0.25)`, `LED2:execute(12, 255)`, `LED2:execute(10, 0)`} {
		if !strings.Contains(got, want) {
			t.Errorf("sent %s, missing %s", got, want)
		}
	}
	if res := mustExec(t, engine, `Dashboard.API.executeAction("led", "rgb", "setWhite", 50)`); !strings.Contains(res, "No setWhite function") {
		t.Errorf("white on an RGB module: %s", res)
	}
}

// Before its first report a light gives no values (and no CLU query), switchOn reaches it, and
// a colour out of range is clamped before it goes to the CLU.
func TestDashboardColorWidgetFromLightEdges(t *testing.T) {
	engine := NewLuaEngine()
	defer engine.Close()
	if _, err := engine.Execute(dashColorLightSetup); err != nil {
		t.Fatalf("setup failed: %v", err)
	}
	var vals map[string]map[string]interface{}
	dashJSON(t, engine, `Dashboard.API.getValues("led")`, &vals)
	for _, k := range []string{"rgb", "brightness", "white", "on"} {
		if v, ok := vals["rgbw"][k]; ok {
			t.Errorf("before a report %s = %v", k, v)
		}
	}
	mustExec(t, engine, `_sent = {}`)
	for _, a := range []string{
		`Dashboard.API.executeAction("led", "rgbw", "switchOn")`,
		`Dashboard.API.executeAction("led", "rgbw", "setColor", {r = 300, g = 12.6, b = -5})`,
	} {
		if res := mustExec(t, engine, a); !strings.Contains(res, `"success":true`) {
			t.Errorf("%s: %s", a, res)
		}
	}
	got := mustExec(t, engine, `table.concat(_sent, ";")`)
	for _, want := range []string{`LED2:execute(9, 0)`, `LED2:execute(6, "#ff0d00")`} {
		if !strings.Contains(got, want) {
			t.Errorf("sent %s, missing %s", got, want)
		}
	}
	if strings.Contains(got, "QUERY") {
		t.Errorf("asked the CLU: %s", got)
	}
}

// A colour widget built from functions works as before: no white unless it has getWhite/setWhite.
func TestDashboardColorWidgetFunctionsUnchanged(t *testing.T) {
	engine := NewLuaEngine()
	defer engine.Close()
	if _, err := engine.Execute(`
		_dashboards = {}
		_c = {}
		Dashboard.new('fn')
			:section('S', 'icon')
			:color('c', 'C', 'palette', {
				getColor = function() return {r = 1, g = 2, b = 3} end,
				setColor = function(r, g, b) _c = {r, g, b} end,
				get = function() return 40 end, set = function() end,
			})
			:build()
	`); err != nil {
		t.Fatalf("setup failed: %v", err)
	}
	var def dashDef
	dashJSON(t, engine, `Dashboard.API.getDefinition("fn")`, &def)
	w := def.widget("c")
	if c, _ := w["capabilities"].(map[string]interface{}); c["white"] != false || c["brightness"] != true {
		t.Errorf("capabilities = %v", w["capabilities"])
	}
	if _, ok := w["white"]; ok {
		t.Errorf("white without getWhite: %v", w["white"])
	}
	mustExec(t, engine, `Dashboard.API.executeAction("fn", "c", "setColor", {r = 10, g = 20, b = 30})`)
	if got := mustExec(t, engine, `table.concat(_c, ",")`); got != "10,20,30" {
		t.Errorf("setColor got %s", got)
	}
}
