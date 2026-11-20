package lua

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"
)

// A GATE Alarm's objects (#214) as the OM import generates their proxy
// (omparser.GenerateProxy; TestGenerateProxyGateAlarm there checks the same lines).
const gateAlarmProxy = `
CLU510000001 = CLU_REMOTE:new(510000001, 0xC0A80104, system:getEventBus())
local _clu = CLU510000001
alarm = {}
alarm.satel_panel = RemoteGateSatel:new(_clu, "SAT0001", "satel_panel")
alarm.alarm_ground = RemoteGateSatelZone:new(_clu, "SAT0002", "alarm_ground")
alarm.pir_hall = RemoteGateSatelInput:new(_clu, "SAT0003", "pir_hall")
alarm.siren = RemoteGateSatelOutput:new(_clu, "SAT0004", "siren")
alarm.jablotron = RemoteGateJablotron:new(_clu, "JAB0001", "jablotron")
alarm.section_house = RemoteGateJablotronSection:new(_clu, "JAB0002", "section_house")
alarm.pg_gate = RemoteGateJablotronOutput:new(_clu, "JAB0003", "pg_gate")
alarm.door_contact = RemoteGateJablotronDevice:new(_clu, "JAB0004", "door_contact")
registerObject("CLU510000001.SAT0003", alarm.pir_hall)
registerObject("CLU510000001.SAT0004", alarm.siren)
registerObject("CLU510000001.JAB0003", alarm.pg_gate)
registerObject("CLU510000001.JAB0004", alarm.door_contact)
registerObject("CLU510000001.SAT0001", alarm.satel_panel)
registerObject("CLU510000001.SAT0002", alarm.alarm_ground)
registerObject("CLU510000001.JAB0001", alarm.jablotron)
registerObject("CLU510000001.JAB0002", alarm.section_house)
`

// askedCommands records the commands that wait for the CLU's answer.
type askedCommands struct {
	mu   sync.Mutex
	list []string
}

func (a *askedCommands) send(ip, script string) (string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.list = append(a.list, script)
	return "0", nil
}

func (a *askedCommands) take() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := strings.Join(a.list, ";")
	a.list = nil
	return out
}

func gateAlarmEngine(t *testing.T, allowDisarm bool) (*LuaEngine, *sentCommands, *askedCommands) {
	t.Helper()
	engine := NewLuaEngine()
	t.Cleanup(engine.Close)
	sent, asked := &sentCommands{}, &askedCommands{}
	engine.SetCommandNoReplySender(sent.send)
	engine.SetCommandSender(asked.send)
	engine.SetGateAlarmAllowDisarm(allowDisarm)
	mustExec(t, engine, gateAlarmProxy)
	return engine, sent, asked
}

// try runs a statement and gives "ok" or the error.
func try(t *testing.T, engine *LuaEngine, stmt string) string {
	t.Helper()
	return mustExec(t, engine, `local ok, err = pcall(function() `+stmt+` end)
		if ok then return "ok" end
		return tostring(err)`)
}

// Registered under <CLU ID>.<object ID> with their names and types.
func TestGateAlarmRegistry(t *testing.T) {
	engine, _, _ := gateAlarmEngine(t, false)
	for path, want := range map[string]string{
		"SAT0001": "satel_panel GATE_SATEL", "SAT0002": "alarm_ground GATE_SATEL_ZONE",
		"SAT0003": "pir_hall GATE_SATEL_INPUT", "SAT0004": "siren GATE_SATEL_OUTPUT",
		"JAB0001": "jablotron GATE_JABLOTRON", "JAB0002": "section_house GATE_JABLOTRON_SECTION",
		"JAB0003": "pg_gate GATE_JABLOTRON_OUTPUT", "JAB0004": "door_contact GATE_JABLOTRON_DEVICE",
	} {
		got := mustExec(t, engine, `local o = _:get("CLU510000001.`+path+`") return o._name .. " " .. o._type`)
		if got != want {
			t.Errorf("%s: %q, want %q", path, got, want)
		}
	}
}

// Values follow the GATE's reports (the remote subscription) and fire the events.
func TestGateAlarmValuesAndEvents(t *testing.T) {
	engine, _, _ := gateAlarmEngine(t, false)
	mustExec(t, engine, `
		EV = {}
		local function rec(name) return function() table.insert(EV, name) end end
		alarm.satel_panel:onConnected(rec("connected")):onDisconnected(rec("disconnected"))
		alarm.alarm_ground:onArm(rec("arm")):onDisarm(rec("disarm"))
		alarm.alarm_ground:onChange(rec("zone change"))
		alarm.pir_hall:onSwitchOn(rec("violated")):onSwitchOff(rec("normal"))
		alarm.siren:onSwitchOn(rec("siren on")):onSwitchOff(rec("siren off"))
		alarm.section_house:onArm(rec("section arm")):onDisarm(rec("section disarm"))
		alarm.section_house:onEvent("OnStateChange", rec("section state"))
		alarm.pg_gate:onSwitchOn(rec("pg on")):onSwitchOff(rec("pg off"))
		alarm.door_contact:onSwitchOn(rec("door open")):onSwitchOff(rec("door closed"))
		RemoteSubscription.activeSubscriptions = {}
		RemoteSubscription.activeSubscriptions["192.168.1.4:7"] = { cluSerial = "CLU510000001", objects = {
			{ objId = "SAT0001", featureIndex = 0 }, { objId = "SAT0002", featureIndex = 0 },
			{ objId = "SAT0003", featureIndex = 0 }, { objId = "SAT0004", featureIndex = 0 },
			{ objId = "JAB0002", featureIndex = 0 }, { objId = "JAB0003", featureIndex = 0 },
			{ objId = "JAB0004", featureIndex = 0 },
		} }
		function report(...) RemoteSubscription.updateRegistry("192.168.1.4", 7, {...}) end
		function events() local s = table.concat(EV, ","); EV = {}; return s end`)

	mustExec(t, engine, `report(1, -1, 0, 0, 0, 0, 0)`)
	if got := mustExec(t, engine, `return events()`); got != "" {
		t.Errorf("the first report fired %q", got)
	}
	mustExec(t, engine, `report(1, 0, 0, 0, 0, 0, 0)`) // known: disarmed, not a disarm
	mustExec(t, engine, `report(0, 1, 1, 1, 1, 1, 1)`)
	mustExec(t, engine, `report(1, 0, 0, 0, 0, 0, 0)`)
	want := "zone change," +
		"disconnected,zone change,arm,violated,siren on,section state,section arm,pg on,door open," +
		"connected,zone change,disarm,normal,siren off,section state,section disarm,pg off,door closed"
	if got := mustExec(t, engine, `return events()`); got != want {
		t.Errorf("events\n%s\nwant\n%s", got, want)
	}
	if got := mustExec(t, engine, `return _:value("CLU510000001.SAT0002") .. " " .. tostring(alarm.alarm_ground:isArmed()) .. " " .. tostring(alarm.satel_panel:isConnected())`); got != "0 false true" {
		t.Errorf("values %q", got)
	}
}

// Arming and the outputs go to the GATE, also while disarming is not allowed.
func TestGateAlarmCommands(t *testing.T) {
	engine, sent, _ := gateAlarmEngine(t, false)
	for stmt, want := range map[string]string{
		`alarm.alarm_ground:armZone()`:              "SAT0002:execute(0)",
		`alarm.alarm_ground:set(0, 1)`:              "SAT0002:execute(0)",
		`alarm.siren:switchOn()`:                    "SAT0004:execute(0)",
		`alarm.siren:switchOff()`:                   "SAT0004:execute(1)",
		`alarm.siren:setValue(1)`:                   "SAT0004:execute(0)",
		`alarm.siren:set(0, 0)`:                     "SAT0004:execute(1)",
		`alarm.section_house:arm()`:                 "JAB0002:execute(0)",
		`alarm.section_house:armPartially()`:        "JAB0002:execute(1)",
		`alarm.section_house:set(0, 1)`:             "JAB0002:execute(0)",
		`alarm.pg_gate:switchOn()`:                  "JAB0003:execute(0)",
		`alarm.pg_gate:switchOff()`:                 "JAB0003:execute(1)",
		`Wire.execute("CLU510000001.SAT0002", 0)()`: "SAT0002:execute(0)",
	} {
		if got := try(t, engine, stmt); got != "ok" {
			t.Errorf("%s: %s", stmt, got)
		}
		if got := sent.take(); got != want {
			t.Errorf("%s sent %q, want %q", stmt, got, want)
		}
	}
}

// Every way to disarm a zone or a section, refused unless grenton.gateAlarm.allowDisarm.
var gateAlarmDisarms = map[string]string{
	`alarm.alarm_ground:disarmZone()`:             "SAT0002:execute(1)",
	`alarm.alarm_ground:execute(1)`:               "SAT0002:execute(1)",
	`alarm.alarm_ground:execute(1.0)`:             "SAT0002:execute(1)",
	`alarm.alarm_ground:set(0, 0)`:                "SAT0002:execute(1)",
	`alarm.alarm_ground:set(0, false)`:            "SAT0002:execute(1)",
	`RemoteObject.execute(alarm.alarm_ground, 1)`: "SAT0002:execute(1)",
	`alarm.alarm_ground:call("disarmZone")`:       "SAT0002:execute(1)",
	`_:get("CLU510000001.SAT0002"):execute(1)`:    "SAT0002:execute(1)",
	`Wire.execute("CLU510000001.SAT0002", 1)()`:   "SAT0002:execute(1)",
	`Wire.set("CLU510000001.SAT0002", 0, 0)()`:    "SAT0002:execute(1)",
	`alarm.section_house:disarm()`:                "JAB0002:execute(2)",
	`alarm.section_house:execute(2)`:              "JAB0002:execute(2)",
	`alarm.section_house:set(0, 0)`:               "JAB0002:execute(2)",
	`Wire.execute("CLU510000001.JAB0002", 2)()`:   "JAB0002:execute(2)",
}

func TestGateAlarmDisarmRefused(t *testing.T) {
	engine, sent, _ := gateAlarmEngine(t, false)
	for stmt := range gateAlarmDisarms {
		if got := try(t, engine, stmt); !strings.Contains(got, "grenton.gateAlarm.allowDisarm") {
			t.Errorf("%s: %s", stmt, got)
		}
		if got := sent.take(); got != "" {
			t.Errorf("%s sent %q", stmt, got)
		}
	}
	// replacing the Lua side of the setting does not allow disarming
	mustExec(t, engine, `__go_gate_alarm_allow_disarm = function() return true end
		RemoteGateAlarm.disarmAllowed = function() return true end`)
	if got := try(t, engine, `alarm.alarm_ground:disarmZone()`); !strings.Contains(got, "grenton.gateAlarm.allowDisarm") || sent.take() != "" {
		t.Errorf("after the global was replaced: %s", got)
	}
}

// Only the listed methods reach the GATE; no feature is written (they are its settings:
// address, port, encryption, Nr); a number that is no whole 0-9999 is refused. Whatever
// the disarm setting.
func TestGateAlarmOnlyListedCommands(t *testing.T) {
	engine, sent, asked := gateAlarmEngine(t, true)
	for _, stmt := range []string{
		// the connections are read-only
		`alarm.satel_panel:set(6, 0)`, `alarm.satel_panel:set(3, 7000)`, `alarm.satel_panel:set(5, 10)`,
		`alarm.satel_panel:execute(0)`, `alarm.jablotron:set(1, 60)`, `alarm.jablotron:execute(0)`,
		`Wire.set("CLU510000001.SAT0001", 6, 0)()`, `Wire.execute("CLU510000001.JAB0001", 0)()`,
		// Nr repoints an object
		`alarm.alarm_ground:set(1, 2)`, `alarm.pir_hall:set(1, 5)`, `alarm.siren:set(1, 5)`,
		`alarm.section_house:set(1, 3)`, `alarm.pg_gate:set(1, 9)`, `alarm.door_contact:set(1, 9)`,
		`Wire.set("CLU510000001.SAT0004", 1, 5)()`,
		// the value goes through the methods only
		`RemoteObject.set(alarm.alarm_ground, 0, 0)`, `RemoteObject.set(alarm.siren, 0, 1)`,
		`alarm.pir_hall:set(0, 1)`, `alarm.door_contact:set(0, 1)`,
		// methods no object has
		`alarm.alarm_ground:execute(2)`, `alarm.siren:execute(2)`, `alarm.pir_hall:execute(0)`,
		`alarm.section_house:execute(3)`, `alarm.door_contact:execute(0)`,
	} {
		if got := try(t, engine, stmt); got == "ok" {
			t.Errorf("%s went through", stmt)
		}
	}
	for _, idx := range []string{`"1"`, `math.huge`, `-1`, `0/0`, `1.5`, `10000`} {
		for _, stmt := range []string{`alarm.alarm_ground:execute(` + idx + `)`, `alarm.satel_panel:get(` + idx + `)`} {
			if got := try(t, engine, stmt); !strings.Contains(got, "takes a whole number 0-9999") {
				t.Errorf("%s: %s", stmt, got)
			}
		}
	}
	if s, a := sent.take(), asked.take(); s != "" || a != "" {
		t.Errorf("sent %q, asked %q", s, a)
	}
	// reading a setting that is no secret still works
	if got := try(t, engine, `alarm.satel_panel:get(5)`); got != "ok" || asked.take() != "SAT0001:get(5)" {
		t.Errorf("get(5): %s", got)
	}
}

// Home Assistant's switch (and HomeKit's) writes an exposed zone with h:setValue(v), which
// ends in set(0, v): off is refused without allowDisarm and sent with it.
func TestGateAlarmExposedSwitchDisarm(t *testing.T) {
	for _, allow := range []bool{false, true} {
		engine, sent, _ := gateAlarmEngine(t, allow)
		got := mustExec(t, engine, `local h = alarm.alarm_ground:expose()
			return tostring(h:setValue(0)) .. " " .. tostring(h:setValue(false))`)
		want, wantSent := "false false", ""
		if allow {
			want, wantSent = "true true", "SAT0002:execute(1);SAT0002:execute(1)"
		}
		if got != want || sent.take() != wantSent {
			t.Errorf("allow %v: %s", allow, got)
		}
	}
}

func TestGateAlarmDisarmAllowed(t *testing.T) {
	engine, sent, _ := gateAlarmEngine(t, true)
	for stmt, want := range gateAlarmDisarms {
		if got := try(t, engine, stmt); got != "ok" {
			t.Errorf("%s: %s", stmt, got)
		}
		if got := sent.take(); got != want {
			t.Errorf("%s sent %q, want %q", stmt, got, want)
		}
	}
	// the setting is read on every disarm: taken back, refused again
	engine.SetGateAlarmAllowDisarm(false)
	if got := try(t, engine, `alarm.alarm_ground:disarmZone()`); got == "ok" || sent.take() != "" {
		t.Errorf("after the setting was taken back: %s", got)
	}
}

// The features holding the panel's passwords, codes, key and address are never read or
// written, by any way in, whatever the disarm setting.
func TestGateAlarmSecretsNeverAsked(t *testing.T) {
	engine, sent, asked := gateAlarmEngine(t, true)
	mustExec(t, engine, `ASYNC = {}
		__go_send_command_async = function(ip, script) table.insert(ASYNC, script) return true end
		Wire.refresh = 0`)
	secrets := map[string][]int{"satel_panel": {2, 4, 7}, "alarm_ground": {2}, "siren": {2},
		"jablotron": {0}, "section_house": {2}, "pg_gate": {2}}
	ids := map[string]string{"satel_panel": "SAT0001", "alarm_ground": "SAT0002", "siren": "SAT0004",
		"jablotron": "JAB0001", "section_house": "JAB0002", "pg_gate": "JAB0003"}
	for obj, features := range secrets {
		for _, f := range features {
			n := string(rune('0' + f))
			for _, stmt := range []string{
				`alarm.` + obj + `:get(` + n + `)`,
				`alarm.` + obj + `:set(` + n + `, "x")`,
				`RemoteObject.get(alarm.` + obj + `, ` + n + `)`,
				`Wire.set("CLU510000001.` + ids[obj] + `", ` + n + `, "x")()`,
			} {
				if got := try(t, engine, stmt); !strings.Contains(got, "holds a secret") {
					t.Errorf("%s: %s", stmt, got)
				}
			}
			// a dashboard's read is asked in the background, past the proxy
			mustExec(t, engine, `local r = Wire.feature("CLU510000001.`+ids[obj]+`", `+n+`) r()`)
		}
	}
	if got := mustExec(t, engine, `return table.concat(ASYNC, ";")`); got != "" {
		t.Errorf("asked in the background: %s", got)
	}
	if s, a := sent.take(), asked.take(); s != "" || a != "" {
		t.Errorf("sent %q, asked %q", s, a)
	}
	// the other features are asked as before (the test sees an ask)
	mustExec(t, engine, `local r = Wire.feature("CLU510000001.SAT0001", 5) r()`)
	if got := mustExec(t, engine, `return table.concat(ASYNC, ";")`); got != "SAT0001:get(5)" {
		t.Errorf("a feature that is no secret: %q", got)
	}
	// Jablotron's value would be its admin code: none, and nothing to subscribe
	if got := mustExec(t, engine, `local j = alarm.jablotron
		return tostring(j:getValue()) .. " " .. #j:subscribeFeatures()`); got != "nil 0" {
		t.Errorf("jablotron %q", got)
	}
}

// exposeRegistry() exposes inputs and devices as binary sensors and outputs as switches;
// a zone or section only by expose(), the connections never.
func TestGateAlarmExposure(t *testing.T) {
	engine, sent, _ := gateAlarmEngine(t, false)
	mustExec(t, engine, `exposeRegistry()`)
	for path, want := range map[string]string{
		"SAT0003": "sensor", "JAB0004": "sensor", "SAT0004": "switch", "JAB0003": "switch",
		"SAT0002": "none", "JAB0002": "none", "SAT0001": "none", "JAB0001": "none",
	} {
		got := mustExec(t, engine, `local h = ExposedObjects:get("CLU510000001.`+path+`") return h and h.type or "none"`)
		if got != want {
			t.Errorf("%s exposed as %s, want %s", path, got, want)
		}
	}
	// alarm outputs are read-only in HomeKit, as the Satel integration's
	if got := mustExec(t, engine, `return tostring(ExposedObjects:get("CLU510000001.SAT0004").object._satel)`); got != "true" {
		t.Errorf("output _satel %s", got)
	}
	// a group without a type leaves the zone out too
	mustExec(t, engine, `RemoteGroup:new({ alarm.alarm_ground, alarm.siren }):expose()`)
	if got := mustExec(t, engine, `return tostring(alarm.alarm_ground:isExposed())`); got != "false" {
		t.Error("a group exposed the zone")
	}

	// by expose() a zone is a switch: on arms, off is refused like any disarm
	got := mustExec(t, engine, `local h = alarm.alarm_ground:expose()
		return h.type .. " " .. tostring(h:setValue(0)) .. " " .. tostring(h:setValue(1))`)
	if got != "switch false true" {
		t.Errorf("exposed zone %q", got)
	}
	if got := sent.take(); got != "SAT0002:execute(0)" {
		t.Errorf("sent %q", got)
	}
	if got := try(t, engine, `alarm.satel_panel:expose()`); !strings.Contains(got, "no expose type") {
		t.Errorf("the connection exposed: %s", got)
	}
}

// The guard's tables are read-only from Lua, and what a script changes on a class or an
// object (its tables, its guard, its ID) does not change what the guard refuses.
func TestGateAlarmGuardReadOnly(t *testing.T) {
	engine, sent, _ := gateAlarmEngine(t, false)
	for _, stmt := range []string{
		`RemoteGateSatelZone.DISARM[1] = nil`,
		`RemoteGateSatelZone.METHODS[5] = "X"`,
		`RemoteGateSatel.SECRET[4] = nil`,
		`setmetatable(RemoteGateSatelZone.DISARM, nil)`,
		`RemoteObject._setGuard(alarm.alarm_ground, function() return nil end)`,
	} {
		if got := try(t, engine, stmt); got == "ok" {
			t.Errorf("%s went through", stmt)
		}
	}
	mustExec(t, engine, `
		RemoteGateSatelZone.DISARM = {}
		RemoteGateJablotronSection.DISARM = {}
		RemoteGateSatel.SECRET = {}
		alarm.alarm_ground.DISARM = {}
		alarm.alarm_ground._refused = function() return nil end
		RemoteGateAlarm._refused = function() return nil end
		alarm.section_house.METHODS = { [9] = "X" }`)
	for _, stmt := range []string{
		`alarm.alarm_ground:disarmZone()`, `alarm.section_house:disarm()`, `alarm.section_house:execute(9)`,
		`alarm.satel_panel:get(4)`, `Wire.execute("CLU510000001.SAT0002", 1)()`,
	} {
		if got := try(t, engine, stmt); got == "ok" {
			t.Errorf("%s went through after the tables were replaced", stmt)
		}
	}
	// an output pointed at the zone would send DisarmZone as its SwitchOff
	mustExec(t, engine, `alarm.siren._objId = "SAT0002"`)
	if got := try(t, engine, `alarm.siren:switchOff()`); !strings.Contains(got, "ID or CLU changed") {
		t.Errorf("an object with another ID: %s", got)
	}
	if got := sent.take(); got != "" {
		t.Errorf("sent %q", got)
	}
	// reading the tables still works
	if got := mustExec(t, engine, `return RemoteGateSatelOutput.METHODS[0] .. " " .. RemoteGateJablotronOutput.METHODS[1]`); got != "SwitchOn SwitchOff" {
		t.Errorf("tables read as %q", got)
	}
}

// A refused disarm is logged, also when it came from Home Assistant's switch, which gets
// no error back.
func TestGateAlarmRefusedDisarmLogged(t *testing.T) {
	logs := captureLog(t)
	engine, _, _ := gateAlarmEngine(t, false)
	mustExec(t, engine, `alarm.alarm_ground:expose():setValue(0)`)
	if out := logs.String(); !strings.Contains(out, "GATE_SATEL_ZONE SAT0002: DisarmZone is refused") {
		t.Errorf("log:\n%s", out)
	}
}

// A state that passes through unknown (-1) still fires the change against the last known
// one: armed, unknown, disarmed is a disarm; armed, unknown, armed is nothing.
func TestGateAlarmEdgesThroughUnknown(t *testing.T) {
	engine, _, _ := gateAlarmEngine(t, false)
	mustExec(t, engine, `
		EV = {}
		alarm.alarm_ground:onArm(function() table.insert(EV, "arm") end)
		alarm.alarm_ground:onDisarm(function() table.insert(EV, "disarm") end)
		function feed(...) for _, v in ipairs({...}) do alarm.alarm_ground:updateState(v) end end
		function events() local s = table.concat(EV, ","); EV = {}; return s end`)
	for seq, want := range map[string]string{
		"1, -1, 0":        "disarm",
		"0, -1, 1":        "arm",
		"1, -1, 1":        "",
		"0, -1, -1, 0":    "",
		"-1, 1, -1, 0":    "disarm",
		"1, -1, 0, -1, 1": "disarm,arm",
	} {
		mustExec(t, engine, `alarm.alarm_ground._lastState = nil alarm.alarm_ground._lastKnown = nil EV = {}`)
		mustExec(t, engine, `feed(`+seq+`)`)
		if got := mustExec(t, engine, `return events()`); got != want {
			t.Errorf("%s: %q, want %q", seq, got, want)
		}
	}
}

// A zone whose state is unknown (-1) is "None" (unknown) to Home Assistant and has no on
// for the cloud, never off.
func TestGateAlarmUnknownStateNotOff(t *testing.T) {
	engine, _, _ := gateAlarmEngine(t, false)
	mustExec(t, engine, `
		_pub = {}
		HAStatePublisher.setup({
			publish = function(self, topic, payload) table.insert(_pub, { topic = topic, payload = payload }) end,
			subscribe = function() end, unsubscribe = function() end,
		})
		ZONE = alarm.alarm_ground:expose():remote()
		function haState()
			local found
			for _, p in ipairs(_pub) do
				if p.topic:find("/state", 1, true) and p.topic:find("sat0002", 1, true) then found = p.payload end
			end
			return tostring(found)
		end`)
	for _, c := range []struct {
		value, ha, cloud string
	}{
		{"1", "ON", "true"},
		{"-1", "None", "<nil>"},
		{"0", "OFF", "false"},
	} {
		mustExec(t, engine, `alarm.alarm_ground:updateState(`+c.value+`) HAStatePublisher.refresh()`)
		if got := mustExec(t, engine, `return haState()`); got != c.ha {
			t.Errorf("%s: Home Assistant %q, want %q", c.value, got, c.ha)
		}
		out, err := engine.Call("Cloud._clientStates", `["CLU510000001.SAT0002"]`)
		var states []map[string]any
		if err == nil {
			err = json.Unmarshal([]byte(out), &states)
		}
		if err != nil || len(states) != 1 || fmt.Sprint(states[0]["on"]) != c.cloud {
			t.Errorf("%s: cloud %s %v, want on %s", c.value, out, err, c.cloud)
		}
	}
}

// MCP's vclu_device_execute calls only the methods vclu_devices lists: the object's declared
// ones, never its raw get/set/execute, its events or internal functions.
func TestGateAlarmMCPOnlyListedMethods(t *testing.T) {
	engine, sent, _ := gateAlarmEngine(t, true)
	mustExec(t, engine, `alarm.alarm_ground:expose({ path = "alarm:zone" })
		local o = { _lastState = 0, get = function(s) return s._lastState end,
			onChange = function() return function() end end,
			ping = function(s) s.pinged = true return "pong" end }
		PLAIN = o
		expose(o, "switch", { path = "t:plain" })`)
	for _, m := range []string{"updateState", "_refused", "execute", "set", "get", "emit", "new", "call", "onEvent", "addTag"} {
		got := mustExec(t, engine, `return MCP.execute_method("alarm:zone", "`+m+`", "[1]")`)
		if !strings.Contains(got, "method not found") {
			t.Errorf("%s: %s", m, got)
		}
	}
	if got := sent.take(); got != "" {
		t.Errorf("sent %q", got)
	}
	if got := mustExec(t, engine, `return MCP.execute_method("alarm:zone", "armZone", nil)`); !strings.Contains(got, `"ok":true`) || sent.take() != "SAT0002:execute(0)" {
		t.Errorf("armZone: %s", got)
	}
	// the list says the same
	var dev struct{ Methods []string }
	if err := json.Unmarshal([]byte(mustExec(t, engine, `return MCP.get_device("alarm:zone")`)), &dev); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(dev.Methods, ","); got != "getValue,isArmed,armZone,disarmZone" {
		t.Errorf("methods %s", got)
	}
	// an object that declares no methods keeps its own public ones
	if got := mustExec(t, engine, `return MCP.execute_method("t:plain", "ping", nil)`); !strings.Contains(got, `"result":"pong"`) {
		t.Errorf("ping: %s", got)
	}
	if got := mustExec(t, engine, `return MCP.execute_method("t:plain", "get", "[0]")`); !strings.Contains(got, "method not found") {
		t.Errorf("get on a plain object: %s", got)
	}
}
