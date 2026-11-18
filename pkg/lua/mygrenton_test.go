package lua

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// MyGrenton turns the widgets of a myGrenton interface into dashboard widgets (#219).

// One page with every widget and call type, as the files of #220 write it.
const myGrentonPage = `
local s = Dashboard.new('mg'):section('Hall')
MyGrenton.widget(s, { type = "HEADER", id = 1, text = { label = "Lights" } })
MyGrenton.widget(s, { type = "ON_OFF", id = 2, buttonBistable = { label = "Hall light",
	state = { callType = "ATTRIBUTE", cluId = "CLU221000002", objectName = "DOU0001", index = 0 },
	actionOn = { callType = "PSEUDO_METHOD", cluId = "CLU221000002", objectName = "DOU0001", index = 0, type = "ON", value = 1 },
	actionOff = { callType = "PSEUDO_METHOD", cluId = "CLU221000002", objectName = "DOU0001", index = 0, type = "OFF", value = 0 } } })
MyGrenton.widget(s, { type = "VALUE", id = 3, value = { label = "Brightness", unit = "%", min = 0, max = 100,
	state = { callType = "ATTRIBUTE", cluId = "CLU221000002", objectName = "DOU0001", index = 0 } } })
MyGrenton.widget(s, { type = "VALUE_V2", id = 4, label = "Setpoint", unit = "°C", precision = 1, valueType = "INTEGER",
	value = { callType = "VARIABLE", cluId = "CLU221000002", objectName = "CLU221000002", index = "setpoint" } })
MyGrenton.widget(s, { type = "TEXT", id = 5, label = "Status",
	text = { callType = "ATTRIBUTE", cluId = "CLU221000002", objectName = "DIN0001", index = 3 } })
MyGrenton.widget(s, { type = "CONTACT_SENSOR", id = 6, label = "Door", onIndication = "Open", offIndication = "Closed",
	object = { value = { callType = "ATTRIBUTE", cluId = "CLU221000002", objectName = "DIN0001", index = 0 },
		clickAction = { callType = "METHOD", cluId = "CLU221000002", objectName = "DOU0001", index = 2, type = "CLICK", value = 3000 } } })
MyGrenton.widget(s, { type = "SCENE", id = 7, buttonMonostable = { label = "Evening",
	actionClick = { callType = "SCRIPT", cluId = "CLU221000002", objectName = "setup", type = "CLICK", args = { "evening", 2 } } } })
MyGrenton.widget(s, { type = "SCENE_DOUBLE", id = 8,
	buttonMonostableFirst = { label = "Up", actionClick = { callType = "SCRIPT", cluId = "CLU221000002", objectName = "setup", args = { 1, -1 } } },
	buttonMonostableSecond = { label = "Down", actionClick = { callType = "SCRIPT", cluId = "CLU221000002", objectName = "setup" } } })
MyGrenton.widget(s, { type = "VALUE", id = 9, value = { label = "Dimmer", unit = "%", min = 0, max = 100,
	state = { callType = "ATTRIBUTE", cluId = "CLU221000002", objectName = "DOU0001", index = 0 },
	actionClick = { callType = "PSEUDO_METHOD", cluId = "CLU221000002", objectName = "DOU0001", index = 0, value = "$value$" } } })
MyGrenton.widget(s, { type = "THERMOSTAT", id = 20,
	thermostatCurrentTemp = { state = { callType = "ATTRIBUTE", cluId = "CLU221000002", objectName = "THE0001", index = 14 } },
	thermostatTargetTemp = { min = 5, max = 30,
		state = { callType = "ATTRIBUTE", cluId = "CLU221000002", objectName = "THE0001", index = 12 },
		actionClick = { callType = "PSEUDO_METHOD", cluId = "CLU221000002", objectName = "THE0001", index = 3, value = "$value$" } },
	thermostatState = { label = "Heating", state = { callType = "ATTRIBUTE", cluId = "CLU221000002", objectName = "THE0001", index = 6 } } })
MyGrenton.widget(s, { type = "SCHEDULER", id = 21, label = "Week" })
MyGrenton.widget(s, { type = "ON_OFF", id = 22, buttonBistable = { label = "Gone",
	state = { callType = "ATTRIBUTE", cluId = "CLU229999999", objectName = "DOU0009", index = 0 } } })
MyGrenton.widget(s, { type = "SCENE", id = 23, buttonMonostable = { label = '"); os.exit(1) --',
	actionClick = { callType = "METHOD", cluId = "CLU221000002", objectName = 'DOU0001:x() os.exit(1) --', index = 0 } } })
MyGrenton.widget(s, { type = "TEXT", id = 27, label = '"); os.exit(1) --]] \\ \001',
	text = { callType = "ATTRIBUTE", cluId = "CLU221000002", objectName = "DIN0001", index = 0 } })
s:build()
`

// The CLU's side: its objects answer get, the user feature is set.
const myGrentonCLU = `
setpoint = 21.5
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
MyGrenton.refresh = 0
`+myGrentonPage)
	return engine, clu, sent
}

// values reads the dashboard until want says the background reads arrived.
func myGrentonValues(t *testing.T, engine *LuaEngine, want func(map[string]map[string]any) bool) map[string]map[string]any {
	t.Helper()
	var vals map[string]map[string]any
	for i := 0; i < 100; i++ {
		dashJSON(t, engine, `Dashboard.API.getValues("mg")`, &vals)
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
	dashJSON(t, engine, `Dashboard.API.getDefinition("mg")`, &def)
	kinds := map[string]string{}
	labels := map[string]string{}
	for _, w := range def.Widgets {
		id, _ := w["id"].(string)
		kinds[id], _ = w["type"].(string)
		labels[id], _ = w["label"].(string)
	}
	for id, kind := range map[string]string{
		"mg2": "toggle", "mg3": "status", "mg4": "status", "mg5": "status", "mg6": "status",
		"mg6_click": "button", "mg7": "button", "mg8_1": "button", "mg8_2": "button", "mg9": "slider",
		"mg20": "thermostat", "mg27": "status",
	} {
		if kinds[id] != kind {
			t.Errorf("%s is %q, want %s", id, kinds[id], kind)
		}
	}
	for _, id := range []string{"mg21", "mg22", "mg23"} {
		if _, ok := kinds[id]; ok {
			t.Errorf("%s was added", id)
		}
	}
	// labels are text: they read back as they were and run nothing
	if labels["mg27"] != "\"); os.exit(1) --]] \\ \x01" || labels["mg8_2"] != "Down" || labels["mg20"] != "Heating" {
		t.Errorf("labels %q", labels)
	}
	if mustExec(t, engine, `return tostring(EXITED)`) != "false" {
		t.Error("a label or a name ran")
	}

	left := mustExec(t, engine, `local out = {}
		for _, l in ipairs(MyGrenton.leftOut) do out[#out + 1] = l.widget .. ": " .. l.reason end
		return table.concat(out, "\n")`)
	for _, want := range []string{
		`SCHEDULER "Week": no vclu widget for this type`,
		`ON_OFF "Gone": state: CLU CLU229999999 is not imported`,
		`actionClick: object DOU0001:x() os.exit(1) -- is no object ID`,
	} {
		if !strings.Contains(left, want) {
			t.Errorf("left out has no %q:\n%s", want, left)
		}
	}
}

func TestMyGrentonReads(t *testing.T) {
	engine, _, _ := myGrentonEngine(t)
	mustExec(t, engine, `_:get("CLU221000002.DOU0001"):updateState(1)`)
	vals := myGrentonValues(t, engine, func(v map[string]map[string]any) bool {
		th := v["mg20"]
		return v["mg4"]["value"] != nil && v["mg5"]["value"] != nil && th["current"] != nil && th["target"] != nil
	})
	// what the registry follows, and what was asked in the background
	if vals["mg2"]["value"] != true || vals["mg3"]["value"] != 1.0 || vals["mg4"]["value"] != 21.5 || vals["mg5"]["value"] != "ready" {
		t.Errorf("values %v", vals)
	}
	if vals["mg6"]["value"] != "Open" {
		t.Errorf("contact %v", vals["mg6"])
	}
	// the page shows a state as 0/1
	if th := vals["mg20"]; th["current"] != 20.5 || th["target"] != 22.0 || th["state"] != 1.0 || th["set"] != true {
		t.Errorf("thermostat %v", vals["mg20"])
	}
}

// A CLU that does not answer: the dashboard still answers, without the values it waits for.
func TestMyGrentonReadsNeverWait(t *testing.T) {
	engine, _, _ := myGrentonEngine(t)
	mustExec(t, engine, `__go_send_command_async = function() end
		__go_send_command = function() error("a read waited for the CLU") end`)
	start := time.Now()
	var vals map[string]map[string]any
	dashJSON(t, engine, `Dashboard.API.getValues("mg")`, &vals)
	if d := time.Since(start); d > time.Second {
		t.Errorf("values took %v", d)
	}
	if vals["mg4"]["value"] != nil || vals["mg4"]["error"] != nil || vals["mg2"]["value"] != false {
		t.Errorf("values %v", vals)
	}
}

func TestMyGrentonActions(t *testing.T) {
	engine, _, sent := myGrentonEngine(t)
	for _, tc := range []struct{ action, want string }{
		{`"mg2", "toggle", true`, `DOU0001:set(0, 1)`},
		{`"mg2", "toggle", false`, `DOU0001:set(0, 0)`},
		{`"mg9", "set", 40`, `DOU0001:set(0, 40)`},
		{`"mg6_click", "tap"`, `DOU0001:execute(2, 3000)`},
		{`"mg7", "tap"`, `setup("evening", 2)`},
		{`"mg8_1", "tap"`, `setup(1, -1)`},
		{`"mg8_2", "tap"`, `setup()`},
		{`"mg20", "set", 23.5`, `THE0001:set(3, 23.5)`},
	} {
		var resp map[string]any
		dashJSON(t, engine, `Dashboard.API.executeAction("mg", `+tc.action+`)`, &resp)
		if resp["success"] != true {
			t.Errorf("%s: %v", tc.action, resp)
		}
		if got := sent.take(); got != tc.want {
			t.Errorf("%s sent %q, want %q", tc.action, got, tc.want)
		}
	}
}

func TestMyGrentonDescribe(t *testing.T) {
	engine, _, _ := myGrentonEngine(t)
	out := mustExec(t, engine, `return JSON.encode(MyGrenton.describe({ type = "SCENE_DOUBLE", id = 8,
		buttonMonostableFirst = { label = "Up", actionClick = { callType = "SCRIPT", cluId = "CLU221000002", objectName = "setup", args = { 1 } } },
		buttonMonostableSecond = { label = "Down", actionClick = { callType = "PSEUDO_METHOD", cluId = "CLU221000002", objectName = "DOU0001", index = 0, value = 0 } } }))`)
	var d struct {
		Widgets []struct{ Kind, ID, Label string }
		Does    []string
	}
	if err := json.Unmarshal([]byte(out), &d); err != nil {
		t.Fatal(err, out)
	}
	if len(d.Widgets) != 2 || d.Widgets[1].Kind != "button" || d.Widgets[1].Label != "Down" ||
		strings.Join(d.Does, ",") != "CLU221000002.DOU0001[0],CLU221000002:setup()" {
		t.Errorf("%s", out)
	}
	if n := mustExec(t, engine, `return #MyGrenton.leftOut`); n != "3" {
		t.Errorf("describe changed leftOut: %s", n)
	}
}
