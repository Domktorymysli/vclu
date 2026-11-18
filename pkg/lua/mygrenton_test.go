package lua

import (
	"strings"
	"testing"
	"time"

	"github.com/Domktorymysli/vclu/pkg/dashspec"
)

// Dashboards generated from vclu's model of an imported interface (#219, #220) run on
// MyGrenton's helpers: the code is the generator's, the CLU a fake one.

func ip(n int) *int         { return &n }
func fp(f float64) *float64 { return &f }
func feat(o string, n int) *dashspec.Read {
	return &dashspec.Read{CLU: "CLU221000002", Object: o, Feature: ip(n)}
}
func setA(o string, i int, v any) *dashspec.Action {
	return &dashspec.Action{Op: dashspec.SetFeature, CLU: "CLU221000002", Object: o, Index: i, Value: v}
}

const codeLabel = "\"); os.exit(1) --]] \\ \x01"

var mgInterface = dashspec.Interface{ID: "if-1", Label: "Home", Source: "myGrenton", Pages: []dashspec.Page{
	{ID: "1", Label: "Hall", Widgets: []dashspec.Widget{
		{Kind: dashspec.Group, Label: "Lights"},
		{Kind: dashspec.Toggle, ID: "mg2", Label: "Hall light", Read: feat("DOU0001", 0),
			On: setA("DOU0001", 0, 1.0), Off: setA("DOU0001", 0, 0.0)},
		{Kind: dashspec.Status, ID: "mg3", Label: "Brightness", Read: feat("DOU0001", 0), Unit: "%"},
		{Kind: dashspec.Status, ID: "mg4", Label: "Setpoint", Read: &dashspec.Read{CLU: "CLU221000002", Variable: "setpoint"},
			Unit: "°C", Decimals: ip(1)},
		{Kind: dashspec.Status, ID: "mg5", Label: "Status", Read: feat("DIN0001", 3)},
		{Kind: dashspec.Status, ID: "mg6", Label: "Door", Read: feat("DIN0001", 0), OnText: "Open", OffText: "Closed"},
		{Kind: dashspec.Button, ID: "mg6_click", Label: "Door", Do: &dashspec.Action{Op: dashspec.Execute,
			CLU: "CLU221000002", Object: "DOU0001", Index: 2, Value: 3000.0}},
		{Kind: dashspec.Button, ID: "mg7", Label: "Evening", Do: &dashspec.Action{Op: dashspec.Script,
			CLU: "CLU221000002", Script: "setup", Args: []any{"evening", 2.0}}},
		{Kind: dashspec.Button, ID: "mg8", Label: "Down", Do: &dashspec.Action{Op: dashspec.Script, CLU: "CLU221000002", Script: "setup"}},
		{Kind: dashspec.Slider, ID: "mg9", Label: "Dimmer", Read: feat("DOU0001", 0), Min: fp(0), Max: fp(100), Unit: "%",
			Set: &dashspec.Action{Op: dashspec.SetFeature, CLU: "CLU221000002", Object: "DOU0001", Input: true}},
		{Kind: dashspec.Status, ID: "mg10", Label: "CLU time", Read: &dashspec.Read{CLU: "CLU221000002", Feature: ip(9)}},
		{Kind: dashspec.Status, ID: "mg11", Label: codeLabel, Read: feat("DIN0001", 0)},
		// written as the user might have left it: an object vclu has not imported
		{Kind: dashspec.Toggle, ID: "mg12", Label: "Gone", Read: feat("DOU0009", 0), On: setA("DOU0009", 0, 1.0)},
	}},
	{ID: "2", Label: "Climate", Widgets: []dashspec.Widget{
		{Kind: dashspec.Thermostat, ID: "mg20", Label: "Heating", Current: feat("THE0001", 14), Target: feat("THE0001", 12),
			State: feat("THE0001", 6), Min: fp(5), Max: fp(30),
			Set: &dashspec.Action{Op: dashspec.SetFeature, CLU: "CLU221000002", Object: "THE0001", Index: 3, Input: true}},
	}},
}}

// The CLU's side: its objects answer get, the user feature is set.
const myGrentonCLU = `
setpoint = 21.5
CLU221000002 = { get = function(self, i) return 1234 end }
DIN0001 = { get = function(self, i) if i == 3 then return "ready" end return 1 end }
THE0001 = { get = function(self, i) return ({ [14] = 20.5, [12] = 22, [6] = 1 })[i] end }
`

func myGrentonEngine(t *testing.T) (*LuaEngine, *fakeCLU, *sentCommands) {
	t.Helper()
	clu := newFakeCLU(t)
	if err := clu.L.DoString(myGrentonCLU); err != nil {
		t.Fatal(err)
	}
	engine := NewLuaEngine()
	t.Cleanup(engine.Close)
	engine.SetCommandSender(clu.send)
	sent := &sentCommands{}
	engine.SetCommandNoReplySender(sent.send)
	mustExec(t, engine, `
EXITED = false
os.exit = function() EXITED = true end
CLU221000002 = CLU_REMOTE:new(221000002, 0xC0A80003, EventBus:getShared())
registerObject("CLU221000002.DOU0001", RemoteSwitch:new(CLU221000002, "DOU0001", "hall_light"))
registerObject("CLU221000002.THE0001", RemoteObject:new(CLU221000002, "THE0001", "living"))
MyGrenton.refresh = 0`)
	for _, p := range mgInterface.Pages {
		mustExec(t, engine, dashspec.PageLua(mgInterface, p))
	}
	return engine, clu, sent
}

// values reads a dashboard until want says the background reads arrived.
func myGrentonValues(t *testing.T, engine *LuaEngine, id string, want func(map[string]map[string]any) bool) map[string]map[string]any {
	t.Helper()
	var vals map[string]map[string]any
	for i := 0; i < 100; i++ {
		dashJSON(t, engine, `Dashboard.API.getValues("`+id+`")`, &vals)
		if want(vals) {
			return vals
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("values never came: %v", vals)
	return nil
}

func TestMyGrentonWidgets(t *testing.T) {
	engine, _, _ := myGrentonEngine(t)
	var def dashDef
	dashJSON(t, engine, `Dashboard.API.getDefinition("grenton_page_1")`, &def)
	for id, kind := range map[string]string{
		"mg2": "toggle", "mg3": "status", "mg4": "status", "mg5": "status", "mg6": "status",
		"mg6_click": "button", "mg7": "button", "mg8": "button", "mg9": "slider", "mg10": "status", "mg11": "status",
	} {
		if w := def.widget(id); w == nil || w["type"] != kind {
			t.Errorf("%s is %v, want %s", id, w, kind)
		}
	}
	// the label is text: it reads back as it was and runs nothing
	if w := def.widget("mg11"); w["label"] != codeLabel {
		t.Errorf("label %q", w["label"])
	}
	if mustExec(t, engine, `return tostring(EXITED)`) != "false" {
		t.Error("a label ran")
	}
	var climate dashDef
	dashJSON(t, engine, `Dashboard.API.getDefinition("grenton_page_2")`, &climate)
	if w := climate.widget("mg20"); w == nil || w["type"] != "thermostat" {
		t.Errorf("thermostat %v", w)
	}
}

func TestMyGrentonReads(t *testing.T) {
	engine, _, _ := myGrentonEngine(t)
	mustExec(t, engine, `_:get("CLU221000002.DOU0001"):updateState(1)`)
	vals := myGrentonValues(t, engine, "grenton_page_1", func(v map[string]map[string]any) bool {
		return v["mg4"]["value"] != nil && v["mg5"]["value"] != nil && v["mg10"]["value"] != nil
	})
	// what the registry follows, and what was asked in the background
	if vals["mg2"]["value"] != true || vals["mg3"]["value"] != 1.0 || vals["mg4"]["value"] != 21.5 ||
		vals["mg5"]["value"] != "ready" || vals["mg6"]["value"] != "Open" || vals["mg10"]["value"] != 1234.0 {
		t.Errorf("values %v", vals)
	}
	// an object not imported reads as nothing
	if vals["mg12"]["value"] != false {
		t.Errorf("gone %v", vals["mg12"])
	}
	th := myGrentonValues(t, engine, "grenton_page_2", func(v map[string]map[string]any) bool {
		return v["mg20"]["current"] != nil && v["mg20"]["target"] != nil && v["mg20"]["state"] != nil
	})["mg20"]
	// the page shows a state as 0/1
	if th["current"] != 20.5 || th["target"] != 22.0 || th["state"] != 1.0 || th["set"] != true {
		t.Errorf("thermostat %v", th)
	}
}

// A CLU that does not answer: the dashboard still answers, without the values it waits for,
// and a read whose answer was lost is asked again later.
func TestMyGrentonReadsNeverWait(t *testing.T) {
	engine, _, _ := myGrentonEngine(t)
	mustExec(t, engine, `ASKED = 0
		__go_send_command_async = function() ASKED = ASKED + 1 end
		__go_send_command = function() error("a read waited for the CLU") end`)
	start := time.Now()
	var vals map[string]map[string]any
	dashJSON(t, engine, `Dashboard.API.getValues("grenton_page_1")`, &vals)
	if d := time.Since(start); d > time.Second {
		t.Errorf("values took %v", d)
	}
	if vals["mg4"]["value"] != nil || vals["mg4"]["error"] != nil || vals["mg2"]["value"] != false {
		t.Errorf("values %v", vals)
	}
	// still waiting: not asked again; after MyGrenton.lost: asked again
	out := mustExec(t, engine, `local first = ASKED
		Dashboard.API.getValues("grenton_page_1")
		local asked = ASKED
		MyGrenton.lost = -1
		Dashboard.API.getValues("grenton_page_1")
		return first .. " " .. asked .. " " .. tostring(first > 0 and asked == first and ASKED > asked)`)
	if !strings.HasSuffix(out, " true") {
		t.Errorf("asked first, while waiting, after lost: %s", out)
	}
}

func TestMyGrentonActions(t *testing.T) {
	engine, _, sent := myGrentonEngine(t)
	for _, tc := range []struct{ page, action, want string }{
		{"1", `"mg2", "toggle", true`, `DOU0001:set(0, 1)`},
		{"1", `"mg2", "toggle", false`, `DOU0001:set(0, 0)`},
		{"1", `"mg9", "set", 40`, `DOU0001:set(0, 40)`},
		{"1", `"mg6_click", "tap"`, `DOU0001:execute(2, 3000)`},
		{"1", `"mg7", "tap"`, `setup("evening", 2)`},
		{"1", `"mg8", "tap"`, `setup()`},
		{"1", `"mg12", "toggle", true`, ``},
		{"2", `"mg20", "set", 23.5`, `THE0001:set(3, 23.5)`},
	} {
		var resp map[string]any
		dashJSON(t, engine, `Dashboard.API.executeAction("grenton_page_`+tc.page+`", `+tc.action+`)`, &resp)
		if resp["success"] != true {
			t.Errorf("%s: %v", tc.action, resp)
		}
		if got := sent.take(); got != tc.want {
			t.Errorf("%s sent %q, want %q", tc.action, got, tc.want)
		}
	}
}

// What a user edits into a page is checked when it loads; a page that fails does not
// stop the others.
func TestMyGrentonChecksAndLoad(t *testing.T) {
	engine, _, _ := myGrentonEngine(t)
	for _, bad := range []string{
		`MyGrenton.set("CLU221000002.DOU0001;os.exit(1)", 0, 1)`,
		`MyGrenton.set("CLU221000002", 0, 1)`,
		`MyGrenton.set("CLU221000002.DOU0001", "0); os.exit(1", 1)`,
		`MyGrenton.set("CLU221000002.DOU0001", 0, {})`,
		`MyGrenton.variable("CLU221000002", "x;os.exit(1)")`,
		`MyGrenton.variable("CLU221000002", "end")`,
		`MyGrenton.script("CLU221000002", "setup()os.exit", 1)`,
		`MyGrenton.script("CLU221000002", "setup", {})`,
		`MyGrenton.feature("CLU221000002.DIN0001", -1)`,
	} {
		if _, err := engine.Execute(bad); err == nil {
			t.Errorf("%s was taken", bad)
		}
	}
	// a quote in a script argument stays inside its string
	mustExec(t, engine, `MyGrenton.script("CLU221000002", "setup", '"); os.exit(1) --')()`)

	mustExec(t, engine, `package.preload["dashboards/grenton/page_bad"] = function()
			Dashboard.new("bad"):section("x"):status("a", "A", nil, function() end):status("a", "A", nil, function() end):build()
		end
		package.preload["dashboards/grenton/page_ok"] = function() Dashboard.new("ok"):section("x"):build() end`)
	out := mustExec(t, engine, `return tostring(MyGrenton.load("dashboards/grenton/page_bad")) .. " " ..
		tostring(MyGrenton.load("dashboards/grenton/page_ok")) .. " " .. #MyGrenton.errors .. " " ..
		tostring(_dashboards["ok"] ~= nil)`)
	if out != "false true 1 true" {
		t.Errorf("load: %s", out)
	}
	if e := mustExec(t, engine, `return MyGrenton.errors[1].error`); !strings.Contains(e, "Duplicate widget ID") {
		t.Errorf("error %s", e)
	}
}
