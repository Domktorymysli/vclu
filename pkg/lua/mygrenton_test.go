package lua

import (
	"strings"
	"testing"
	"time"

	"github.com/Domktorymysli/vclu/pkg/dashspec"
)

// Dashboards generated from vclu's model of an imported interface (#219, #220) run on
// Wire's helpers: the code is the generator's, the CLU a fake one.

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
		{Kind: dashspec.Toggle, ID: "w2", Label: "Hall light", Read: feat("DOU0001", 0),
			On: setA("DOU0001", 0, 1.0), Off: setA("DOU0001", 0, 0.0)},
		{Kind: dashspec.Status, ID: "w3", Label: "Brightness", Read: feat("DOU0001", 0), Unit: "%"},
		{Kind: dashspec.Status, ID: "w4", Label: "Setpoint", Read: &dashspec.Read{CLU: "CLU221000002", Variable: "setpoint"},
			Unit: "°C", Decimals: ip(1)},
		{Kind: dashspec.Status, ID: "w5", Label: "Status", Read: feat("DIN0001", 3)},
		{Kind: dashspec.Status, ID: "w6", Label: "Door", Read: feat("DIN0001", 0), OnText: "Open", OffText: "Closed"},
		{Kind: dashspec.Button, ID: "w6_click", Label: "Door", Do: &dashspec.Action{Op: dashspec.Execute,
			CLU: "CLU221000002", Object: "DOU0001", Index: 2, Value: 3000.0}},
		{Kind: dashspec.Button, ID: "w7", Label: "Evening", Do: &dashspec.Action{Op: dashspec.Script,
			CLU: "CLU221000002", Script: "setup", Args: []any{"evening", 2.0}}},
		{Kind: dashspec.Button, ID: "w8", Label: "Down", Do: &dashspec.Action{Op: dashspec.Script, CLU: "CLU221000002", Script: "setup"}},
		{Kind: dashspec.Slider, ID: "w9", Label: "Dimmer", Read: feat("DOU0001", 0), Min: fp(0), Max: fp(100), Unit: "%",
			Set: &dashspec.Action{Op: dashspec.SetFeature, CLU: "CLU221000002", Object: "DOU0001", Input: true}},
		{Kind: dashspec.Status, ID: "w10", Label: "CLU time", Read: &dashspec.Read{CLU: "CLU221000002", Feature: ip(9)}},
		{Kind: dashspec.Status, ID: "w11", Label: codeLabel, Read: feat("DIN0001", 0)},
		// written as the user might have left it: an object vclu has not imported
		{Kind: dashspec.Toggle, ID: "w12", Label: "Gone", Read: feat("DOU0009", 0), On: setA("DOU0009", 0, 1.0)},
		// a dimmer's 0-1 as 0-100
		{Kind: dashspec.Slider, ID: "w13", Label: "Lamp", Read: feat("DOU0001", 0), Min: fp(0), Max: fp(100), Unit: "%", Percent: true,
			Set: &dashspec.Action{Op: dashspec.SetFeature, CLU: "CLU221000002", Object: "DOU0001", Input: true}},
		// another feature of an object the registry does not have: not asked past the registry
		{Kind: dashspec.Status, ID: "w14", Label: "Secret", Read: feat("SAT0001", 4)},
	}},
	{ID: "2", Label: "Climate", Widgets: []dashspec.Widget{
		{Kind: dashspec.Thermostat, ID: "w20", Label: "Heating", Current: feat("THE0001", 14), Target: feat("THE0001", 12),
			State: feat("THE0001", 6), Min: fp(5), Max: fp(30),
			Set: &dashspec.Action{Op: dashspec.SetFeature, CLU: "CLU221000002", Object: "THE0001", Index: 3, Input: true}},
	}},
}}

// The CLU's side: its objects answer get, the user feature is set.
const myGrentonCLU = `
setpoint = 21.5
CLU221000002 = { get = function(self, i) return 1234 end }
DIN0001 = { get = function(self, i) if i == 3 then return "ready" end return 1 end }
DOU0001 = { get = function(self, i) return 0.75 end }
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
registerObject("CLU221000002.DIN0001", RemoteObject:new(CLU221000002, "DIN0001", "door"))
-- on the Sync list: the registry follows them
RemoteSubscription.config.enabled = true
RemoteSubscription.config.whitelist = { ["CLU221000002.DOU0001"] = true, ["CLU221000002.DIN0001"] = true }
Wire.refresh = 0`)
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

func TestWireWidgets(t *testing.T) {
	engine, _, _ := myGrentonEngine(t)
	var def dashDef
	dashJSON(t, engine, `Dashboard.API.getDefinition("imported_1")`, &def)
	for id, kind := range map[string]string{
		"w2": "toggle", "w3": "status", "w4": "status", "w5": "status", "w6": "status",
		"w6_click": "button", "w7": "button", "w8": "button", "w9": "slider", "w10": "status", "w11": "status",
		"w13": "slider", "w14": "status",
	} {
		if w := def.widget(id); w == nil || w["type"] != kind {
			t.Errorf("%s is %v, want %s", id, w, kind)
		}
	}
	// the label is text: it reads back as it was and runs nothing
	if w := def.widget("w11"); w["label"] != codeLabel {
		t.Errorf("label %q", w["label"])
	}
	if mustExec(t, engine, `return tostring(EXITED)`) != "false" {
		t.Error("a label ran")
	}
	var climate dashDef
	dashJSON(t, engine, `Dashboard.API.getDefinition("imported_2")`, &climate)
	if w := climate.widget("w20"); w == nil || w["type"] != "thermostat" {
		t.Errorf("thermostat %v", w)
	}
}

func TestWireReads(t *testing.T) {
	engine, _, _ := myGrentonEngine(t)
	mustExec(t, engine, `_:get("CLU221000002.DOU0001"):updateState(0.25)
		_:get("CLU221000002.DIN0001"):updateState(1)
		ASKED = {}
		local send = __go_send_command_async
		__go_send_command_async = function(ip, cmd, ...) ASKED[#ASKED + 1] = cmd return send(ip, cmd, ...) end`)
	vals := myGrentonValues(t, engine, "imported_1", func(v map[string]map[string]any) bool {
		return v["w4"]["value"] != nil && v["w5"]["value"] != nil && v["w10"]["value"] != nil
	})
	// what the registry follows, and what was asked in the background
	if vals["w2"]["value"] != true || vals["w3"]["value"] != 0.25 || vals["w4"]["value"] != 21.5 ||
		vals["w5"]["value"] != "ready" || vals["w6"]["value"] != "Open" || vals["w10"]["value"] != 1234.0 {
		t.Errorf("values %v", vals)
	}
	// an object not imported reads as nothing (not as off), and is never asked of the CLU
	if vals["w12"]["value"] != nil || vals["w14"]["value"] != nil {
		t.Errorf("gone %v %v", vals["w12"], vals["w14"])
	}
	if asked := mustExec(t, engine, `return table.concat(ASKED, "|")`); strings.Contains(asked, "SAT0001") {
		t.Errorf("asked past the registry: %s", asked)
	}
	if vals["w13"]["value"] != 25.0 {
		t.Errorf("percent %v", vals["w13"])
	}
	th := myGrentonValues(t, engine, "imported_2", func(v map[string]map[string]any) bool {
		return v["w20"]["current"] != nil && v["w20"]["target"] != nil && v["w20"]["state"] != nil
	})["w20"]
	// the page shows a state as 0/1
	if th["current"] != 20.5 || th["target"] != 22.0 || th["state"] != 1.0 || th["set"] != true {
		t.Errorf("thermostat %v", th)
	}
}

// A CLU that does not answer: the dashboard still answers, without the values it waits for,
// and a read whose answer was lost is asked again later.
func TestWireReadsNeverWait(t *testing.T) {
	engine, _, _ := myGrentonEngine(t)
	mustExec(t, engine, `ASKED = 0
		__go_send_command_async = function() ASKED = ASKED + 1 end
		__go_send_command = function() error("a read waited for the CLU") end`)
	start := time.Now()
	var vals map[string]map[string]any
	dashJSON(t, engine, `Dashboard.API.getValues("imported_1")`, &vals)
	if d := time.Since(start); d > time.Second {
		t.Errorf("values took %v", d)
	}
	if vals["w4"]["value"] != nil || vals["w4"]["error"] != nil || vals["w2"]["value"] != nil {
		t.Errorf("values %v", vals)
	}
	// still waiting: not asked again; after Wire.lost: asked again
	out := mustExec(t, engine, `local first = ASKED
		Dashboard.API.getValues("imported_1")
		local asked = ASKED
		Wire.lost = -1
		Dashboard.API.getValues("imported_1")
		return first .. " " .. asked .. " " .. tostring(first > 0 and asked == first and ASKED > asked)`)
	if !strings.HasSuffix(out, " true") {
		t.Errorf("asked first, while waiting, after lost: %s", out)
	}
}

func TestWireActions(t *testing.T) {
	engine, _, sent := myGrentonEngine(t)
	for _, tc := range []struct{ page, action, want string }{
		{"1", `"w2", "toggle", true`, `DOU0001:set(0, 1)`},
		{"1", `"w2", "toggle", false`, `DOU0001:set(0, 0)`},
		{"1", `"w9", "set", 40`, `DOU0001:set(0, 40)`},
		{"1", `"w13", "set", 40`, `DOU0001:set(0, 0.4)`},
		{"1", `"w6_click", "tap"`, `DOU0001:execute(2, 3000)`},
		{"1", `"w7", "tap"`, `setup("evening", 2)`},
		{"1", `"w8", "tap"`, `setup()`},
		{"2", `"w20", "set", 23.5`, `THE0001:set(3, 23.5)`},
	} {
		var resp map[string]any
		dashJSON(t, engine, `Dashboard.API.executeAction("imported_`+tc.page+`", `+tc.action+`)`, &resp)
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
func TestWireChecksAndLoad(t *testing.T) {
	engine, _, _ := myGrentonEngine(t)
	for _, bad := range []string{
		`Wire.set("CLU221000002.DOU0001;os.exit(1)", 0, 1)`,
		`Wire.set("CLU221000002", 0, 1)`,
		`Wire.set("CLU221000002.DOU0001", "0); os.exit(1", 1)`,
		`Wire.set("CLU221000002.DOU0001", 0, {})`,
		`Wire.variable("CLU221000002", "x;os.exit(1)")`,
		`Wire.variable("CLU221000002", "end")`,
		`Wire.script("CLU221000002", "setup()os.exit", 1)`,
		`Wire.script("CLU221000002", "setup", {})`,
		`Wire.feature("CLU221000002.DIN0001", -1)`,
	} {
		_, err := engine.Execute(bad)
		if err == nil {
			t.Errorf("%s was taken", bad)
			continue
		}
		// the error names the line that called Wire, not Wire's own
		if first, _, _ := strings.Cut(err.Error(), "\n"); strings.Contains(first, "Wire.lua") || !strings.Contains(first, "<string>:1:") {
			t.Errorf("%s: %s", bad, first)
		}
	}
	// a quote in a script argument stays inside its string
	mustExec(t, engine, `Wire.script("CLU221000002", "setup", '"); os.exit(1) --')()`)

	mustExec(t, engine, `package.preload["dashboards/imported/page_bad"] = function()
			Dashboard.new("bad"):section("x"):status("a", "A", nil, function() end):status("a", "A", nil, function() end):build()
		end
		package.preload["dashboards/imported/page_ok"] = function() Dashboard.new("ok"):section("x"):build() end`)
	out := mustExec(t, engine, `return tostring(Wire.load("dashboards/imported/page_bad")) .. " " ..
		tostring(Wire.load("dashboards/imported/page_ok")) .. " " .. #Wire.errors .. " " ..
		tostring(_dashboards["ok"] ~= nil)`)
	if out != "false true 1 true" {
		t.Errorf("load: %s", out)
	}
	if e := mustExec(t, engine, `return Wire.errors[1].error`); !strings.Contains(e, "Duplicate widget ID") {
		t.Errorf("error %s", e)
	}
}

// The registry's value only for an object on the Sync list (with Sync on) that has reported
// one: off the list, its _lastState is only what vclu last sent, so the CLU is asked (#219 review).
func TestWireValueOffSyncList(t *testing.T) {
	engine, _, _ := myGrentonEngine(t)
	// the CLU answers at once, with a value the registry does not have
	mustExec(t, engine, `_:get("CLU221000002.DOU0001"):updateState(0.25)
		ASKED = 0
		__go_send_command_async = function(ip, cmd, id) ASKED = ASKED + 1 __vclu_command_reply(id, 0.75) end`)
	read := `return tostring(Wire.value("CLU221000002.DOU0001")()) .. " " .. ASKED`
	// on the list, reported: from the registry, the CLU not asked
	if got := mustExec(t, engine, read); got != "0.25 0" {
		t.Errorf("on the Sync list: %s", got)
	}
	for name, setup := range map[string]string{
		"off the list":     `RemoteSubscription.config.whitelist = {}`,
		"Sync off":         `RemoteSubscription.config.enabled = false`,
		"no whitelist":     `RemoteSubscription.config.whitelist = nil`,
		"not reported yet": `_:get("CLU221000002.DOU0001")._lastState = nil`,
	} {
		mustExec(t, engine, `RemoteSubscription.config.enabled = true
			RemoteSubscription.config.whitelist = { ["CLU221000002.DOU0001"] = true }
			_:get("CLU221000002.DOU0001")._lastState = 0.25
			ASKED = 0
			`+setup)
		if got := mustExec(t, engine, read); got != "0.75 1" {
			t.Errorf("%s: %s, want the CLU's 0.75, asked once", name, got)
		}
	}
}

// A thermostat's getValue is its CurrentTemp (feature 14): it answers that feature, never 0.
func TestWireThermostatValue(t *testing.T) {
	engine, _, _ := myGrentonEngine(t)
	out := mustExec(t, engine, `
		local th = RemoteThermostat:new(CLU221000002, "THE0002", "hall")
		th._version = 2
		registerObject("CLU221000002.THE0002", th)
		RemoteSubscription.config.whitelist["CLU221000002.THE0002"] = true
		th:updateState(19.5)
		ASKED = {}
		__go_send_command_async = function(ip, cmd) ASKED[#ASKED + 1] = cmd return true end
		local cur = Wire.feature("CLU221000002.THE0002", 14)()
		local zero = Wire.value("CLU221000002.THE0002")()
		return tostring(cur) .. " " .. tostring(zero) .. " " .. table.concat(ASKED, "|")`)
	if !strings.HasPrefix(out, "19.5 nil ") || !strings.Contains(out, "THE0002:get(0)") || strings.Contains(out, "get(14)") {
		t.Errorf("thermostat: %s", out)
	}
}

// Unknown is neither on nor off: no value yet, -1, or nothing the CLU answered.
func TestWireIsOnUnknown(t *testing.T) {
	engine, _, _ := myGrentonEngine(t)
	out := mustExec(t, engine, `
		local out = {}
		for _, v in ipairs({ "nil", "-1", "'-1'", "0", "'0'", "1", "0.4", "''", "'false'", "true", "false", "{}" }) do
			local x = loadstring("return " .. v)()
			out[#out + 1] = v .. "=" .. tostring(Wire.isOn(function() return x end)()) ..
				"/" .. tostring(Wire.text(function() return x end, "on", "off")())
		end
		return table.concat(out, " ")`)
	want := "nil=nil/nil -1=nil/nil '-1'=nil/nil 0=false/off '0'=false/off 1=true/on 0.4=true/on " +
		"''=false/off 'false'=false/off true=true/on false=false/off {}=nil/nil"
	if out != want {
		t.Errorf("isOn/text:\n got %s\nwant %s", out, want)
	}
}

// A command that cannot be sent is an error the page shows, not a success.
func TestWireActionsFail(t *testing.T) {
	engine, _, sent := myGrentonEngine(t)
	mustExec(t, engine, `Dashboard.new("fail"):section("x")
		:button("gone", "Gone", nil, Wire.set("CLU221000002.DOU0009", 0, 1))
		:button("noclu", "No CLU", nil, Wire.script("CLU999000001", "setup"))
		:toggle("onlyon", "Only on", nil, function() return nil end, Wire.onOff(Wire.set("CLU221000002.DOU0001", 0, 1), nil))
		:slider("input", "Input", 0, 100, function() return 0 end, Wire.fromPercent(Wire.set("CLU221000002.DOU0001", 0, Wire.INPUT)))
		:build()`)
	for _, tc := range []struct{ action, want string }{
		{`"gone", "tap"`, "CLU221000002.DOU0009 is not imported"},
		{`"noclu", "tap"`, "CLU999000001 is not imported"},
		{`"onlyon", "toggle", false`, "no action to switch off"},
		{`"w12", "toggle", true`, "CLU221000002.DOU0009 is not imported"},
	} {
		page := "fail"
		if strings.HasPrefix(tc.action, `"w12"`) {
			page = "imported_1"
		}
		var resp map[string]any
		dashJSON(t, engine, `Dashboard.API.executeAction("`+page+`", `+tc.action+`)`, &resp)
		if resp["success"] == true || resp["error"] != tc.want {
			t.Errorf("%s: %v, want error %q", tc.action, resp, tc.want)
		}
		if got := sent.take(); got != "" {
			t.Errorf("%s sent %q", tc.action, got)
		}
	}
	// the side it has still switches
	var resp map[string]any
	dashJSON(t, engine, `Dashboard.API.executeAction("fail", "onlyon", "toggle", true)`, &resp)
	if resp["success"] != true || sent.take() != "DOU0001:set(0, 1)" {
		t.Errorf("only on: %v", resp)
	}
	// an action called without the value it takes
	for _, call := range []string{`Wire.fromPercent(Wire.set("CLU221000002.DOU0001", 0, Wire.INPUT))("x")`,
		`Wire.set("CLU221000002.DOU0001", 0, Wire.INPUT)(nil)`} {
		if _, err := engine.Execute(call); err == nil || !strings.Contains(err.Error(), "no value entered") {
			t.Errorf("%s: %v", call, err)
		}
	}
}

// Background reads wait at most Wire.perCLU at a time on one CLU; the others are asked
// when answers come or are lost, and another CLU has places of its own.
func TestWireReadsPerCLU(t *testing.T) {
	engine, _, _ := myGrentonEngine(t)
	out := mustExec(t, engine, `
		PENDING = {}
		NOW = 1000
		os.time = function() return NOW end
		__go_send_command_async = function(ip, cmd, id) PENDING[#PENDING + 1] = id end
		CLU221000003 = CLU_REMOTE:new(221000003, 0xC0A80004, EventBus:getShared())
		local reads = {}
		for i = 1, 10 do reads[i] = Wire.feature("CLU221000002", i) end
		local other = Wire.feature("CLU221000003", 1)
		local function poll() for _, r in ipairs(reads) do r() end other() end
		poll()
		local first = #PENDING
		poll()
		local again = #PENDING
		-- an answer frees a place
		__vclu_command_reply(table.remove(PENDING, 1), 1)
		poll()
		local answered = #PENDING
		-- lost answers free theirs
		NOW = NOW + Wire.lost
		poll()
		return first .. " " .. again .. " " .. answered .. " " .. #PENDING`)
	// 4 of CLU221000002 and 1 of CLU221000003; one more after an answer; once lost, 4 + 1 again
	if out != "5 5 5 10" {
		t.Errorf("pending after polls: %s, want 5 5 5 10", out)
	}
}
