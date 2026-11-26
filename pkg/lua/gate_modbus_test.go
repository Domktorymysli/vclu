package lua

import (
	"strings"
	"testing"
)

// A GATE MODBUS's objects as the OM import generates their proxy (omparser.GenerateProxy;
// TestGenerateProxyGateModbus there checks the same lines).
const gateModbusProxy = `
CLU500000001 = CLU_REMOTE:new(500000001, 0xC0A8010A, system:getEventBus())
local _clu = CLU500000001
modbus = {}
modbus.temp = RemoteGateModbusSensor:new(_clu, "MOD0001", "temp", 203)
modbus.mode = RemoteGateModbusNumber:new(_clu, "MOD0002", "mode", 200)
modbus.alarm = RemoteGateModbusBinarySensor:new(_clu, "MOD0003", "alarm", 205)
modbus.relay = RemoteGateModbusSwitch:new(_clu, "MOD0004", "relay", 203)
modbus.old = RemoteGateModbusSensor:new(_clu, "MOD0005", "old", 200)
modbus.value = RemoteGateModbusNumber:new(_clu, "MOD0006", "value", 201)
modbus.coil = RemoteGateModbusSwitch:new(_clu, "MOD0007", "coil", 200)
registerObject("CLU500000001.MOD0001", modbus.temp)
registerObject("CLU500000001.MOD0002", modbus.mode)
registerObject("CLU500000001.MOD0003", modbus.alarm)
registerObject("CLU500000001.MOD0004", modbus.relay)
registerObject("CLU500000001.MOD0005", modbus.old)
registerObject("CLU500000001.MOD0006", modbus.value)
registerObject("CLU500000001.MOD0007", modbus.coil)
`

func gateModbusEngine(t *testing.T) (*LuaEngine, *sentCommands, *askedCommands) {
	t.Helper()
	engine := NewLuaEngine()
	t.Cleanup(engine.Close)
	sent, asked := &sentCommands{}, &askedCommands{}
	engine.SetCommandNoReplySender(sent.send)
	engine.SetCommandSender(asked.send)
	mustExec(t, engine, gateModbusProxy)
	return engine, sent, asked
}

// The Sync list follows each class's value feature, and nothing else.
func TestGateModbusSubscribedFeature(t *testing.T) {
	engine, _, _ := gateModbusEngine(t)
	for obj, want := range map[string]string{
		"temp": "22", "mode": "14", "alarm": "19", "relay": "22", "old": "14", "value": "20", "coil": "14",
	} {
		got := mustExec(t, engine, `return table.concat(modbus.`+obj+`:subscribeFeatures(), ",")`)
		if got != want {
			t.Errorf("%s follows %q, want %q", obj, got, want)
		}
	}
	if got := try(t, engine, `RemoteGateModbusSensor:new(CLU500000001, "MOD0009", "x", 202)`); !strings.Contains(got, "is not a Modbus object") {
		t.Errorf("class 202: %s", got)
	}
}

// Values follow the GATE's reports, ModbusValue's string too, and fire the events.
func TestGateModbusValuesAndEvents(t *testing.T) {
	engine, _, _ := gateModbusEngine(t)
	mustExec(t, engine, `
		EV = {}
		local function rec(name) return function() table.insert(EV, name) end end
		modbus.temp:onChange(rec("temp"))
		modbus.alarm:onSwitchOn(rec("alarm on")):onSwitchOff(rec("alarm off"))
		modbus.relay:onSwitchOn(rec("relay on")):onSwitchOff(rec("relay off"))
		RemoteSubscription.activeSubscriptions = {}
		RemoteSubscription.activeSubscriptions["192.168.1.10:7"] = { cluSerial = "CLU500000001", objects = {
			{ objId = "MOD0001", featureIndex = 22 }, { objId = "MOD0003", featureIndex = 19 },
			{ objId = "MOD0004", featureIndex = 22 }, { objId = "MOD0006", featureIndex = 20 },
		} }
		function report(...) RemoteSubscription.updateRegistry("192.168.1.10", 7, {...}) end
		function events() local s = table.concat(EV, ","); EV = {}; return s end`)

	mustExec(t, engine, `report(215, 0, 0, "12.5")`)
	if got := mustExec(t, engine, `return events()`); got != "" {
		t.Errorf("the first report fired %q", got)
	}
	mustExec(t, engine, `report(216, 1, 1, "13")`)
	mustExec(t, engine, `report(216, 0, 0, "13")`)
	if got, want := mustExec(t, engine, `return events()`), "temp,alarm on,relay on,alarm off,relay off"; got != want {
		t.Errorf("events %q, want %q", got, want)
	}
	if got := mustExec(t, engine, `return _:value("CLU500000001.MOD0001") .. " " .. modbus.value:getValue() .. " " .. type(modbus.value:getValue())`); got != "216 13 number" {
		t.Errorf("values %q", got)
	}
}

// A switch and a number write by their class's way: SetValue (set 14) on Modbus v1/v2,
// WriteValue (execute 1, a string) on the others.
func TestGateModbusWrites(t *testing.T) {
	engine, sent, _ := gateModbusEngine(t)
	for stmt, want := range map[string]string{
		`modbus.mode:setValue(3)`:     "MOD0002:set(14, 3)",
		`modbus.mode:set(0, 2)`:       "MOD0002:set(14, 2)",
		`modbus.value:setValue(21.5)`: `MOD0006:execute(1, "21.5")`,
		`modbus.relay:switchOn()`:     `MOD0004:execute(1, "1")`,
		`modbus.relay:set(0, 0)`:      `MOD0004:execute(1, "0")`,
		`modbus.coil:switchOn()`:      "MOD0007:set(14, 1)",
	} {
		if got := try(t, engine, stmt); got != "ok" {
			t.Errorf("%s: %s", stmt, got)
			continue
		}
		if got := sent.take(); got != want {
			t.Errorf("%s sent %q, want %q", stmt, got, want)
		}
	}
	if got := try(t, engine, `modbus.mode:setValue("x")`); !strings.Contains(got, "takes a finite number") {
		t.Errorf("a number from a string: %s", got)
	}
}

// Only the value and the error features are read; no setting is written; a read-only
// register is never written, by any way in.
func TestGateModbusGuard(t *testing.T) {
	engine, sent, asked := gateModbusEngine(t)
	mustExec(t, engine, `ASYNC = {}
		__go_send_command_async = function(ip, script) table.insert(ASYNC, script) return true end
		Wire.refresh = 0`)
	for stmt, want := range map[string]string{
		`modbus.temp:get(8)`:                         "is not one vclu reads",
		`modbus.temp:get(27)`:                        "is not one vclu reads",
		`RemoteObject.get(modbus.mode, 2)`:           "is not one vclu reads",
		`modbus.temp:set(22, 1)`:                     "read-only Modbus register",
		`modbus.temp:execute(1, "5")`:                "read-only Modbus register",
		`modbus.alarm:set(0, 1)`:                     "read-only Modbus register",
		`modbus.old:set(14, 1)`:                      "read-only Modbus register",
		`modbus.mode:set(2, 900)`:                    "is a setting of the GATE",
		`modbus.mode:execute(1, "3")`:                "is not one vclu sends",
		`modbus.relay:set(8, 3)`:                     "is a setting of the GATE",
		`modbus.relay:execute(0)`:                    "is not one vclu sends",
		`modbus.relay:get("1) x(")`:                  "whole number",
		`Wire.set("CLU500000001.MOD0001", 22, 5)()`:  "read-only Modbus register",
		`Wire.set("CLU500000001.MOD0002", 2, 900)()`: "is a setting of the GATE",
	} {
		if got := try(t, engine, stmt); !strings.Contains(got, want) {
			t.Errorf("%s: %s, want %q", stmt, got, want)
		}
	}
	// a dashboard's read past the proxy asks the guard too
	mustExec(t, engine, `local r = Wire.feature("CLU500000001.MOD0001", 8) r()`)
	if got := mustExec(t, engine, `return table.concat(ASYNC, ";")`); got != "" {
		t.Errorf("asked in the background: %s", got)
	}
	if s, a := sent.take(), asked.take(); s != "" || a != "" {
		t.Errorf("sent %q, asked %q", s, a)
	}
	if got := try(t, engine, `modbus.relay._objId = "MOD0001"; modbus.relay:switchOn()`); !strings.Contains(got, "changed after it was created") {
		t.Errorf("a changed ID: %s", got)
	}
	// the value and the error code are read
	for stmt, want := range map[string]string{
		`modbus.temp:get(22)`: "MOD0001:get(22)", `modbus.temp:get(25)`: "MOD0001:get(25)",
		`modbus.mode:get(13)`: "MOD0002:get(13)", `modbus.alarm:get(21)`: "MOD0003:get(21)",
	} {
		if got := try(t, engine, stmt); got != "ok" {
			t.Errorf("%s: %s", stmt, got)
		}
		if got := asked.take(); got != want {
			t.Errorf("%s asked %q", stmt, got)
		}
	}
}

// exposeRegistry() exposes sensors as read-only numbers and binary sensors like a DIN; a
// switch or a number only by expose().
func TestGateModbusExposure(t *testing.T) {
	engine, sent, _ := gateModbusEngine(t)
	mustExec(t, engine, `exposeRegistry()`)
	for path, want := range map[string]string{
		"MOD0001": "number true", "MOD0003": "sensor false", "MOD0005": "number true",
		"MOD0002": "none", "MOD0004": "none", "MOD0006": "none", "MOD0007": "none",
	} {
		got := mustExec(t, engine, `local h = ExposedObjects:get("CLU500000001.`+path+`")
			return h and (h.type .. " " .. tostring(h.options.readonly == true)) or "none"`)
		if got != want {
			t.Errorf("%s exposed as %q, want %q", path, got, want)
		}
	}
	if got := mustExec(t, engine, `return tostring(ExposedObjects:get("CLU500000001.MOD0001"):setValue(5))`); got != "false" {
		t.Errorf("a sensor took a write: %s", got)
	}
	// by expose() a switch writes
	got := mustExec(t, engine, `local h = modbus.relay:expose() return h.type .. " " .. tostring(h:setValue(1))`)
	if got != "switch true" {
		t.Errorf("exposed relay %q", got)
	}
	if got := sent.take(); got != `MOD0004:execute(1, "1")` {
		t.Errorf("sent %q", got)
	}
}

// Wire.value reads a Modbus object's value feature: from the Sync list when the object is on
// it, else asked by that feature, never feature 0.
func TestGateModbusWireValue(t *testing.T) {
	engine, _, _ := gateModbusEngine(t)
	mustExec(t, engine, `ASYNC = {}
		__go_send_command_async = function(ip, script) table.insert(ASYNC, script) return true end
		Wire.refresh = 0
		modbus.temp:updateState(215)`)
	// off the Sync list: asked by its value feature
	mustExec(t, engine, `READ = Wire.value("CLU500000001.MOD0001") READ()`)
	if got := mustExec(t, engine, `local s = table.concat(ASYNC, ";") ASYNC = {} return s`); got != "MOD0001:get(22)" {
		t.Errorf("asked %q", got)
	}
	// on the Sync list: the registry's value, nothing asked
	mustExec(t, engine, `RemoteSubscription.config = { enabled = true, whitelist = { ["CLU500000001.MOD0001"] = true } }`)
	if got := mustExec(t, engine, `return tostring(Wire.value("CLU500000001.MOD0001")()) .. " " .. tostring(Wire.feature("CLU500000001.MOD0001", 22)())`); got != "215 215" {
		t.Errorf("read %q", got)
	}
	if got := mustExec(t, engine, `return table.concat(ASYNC, ";")`); got != "" {
		t.Errorf("asked %q while on the Sync list", got)
	}
}

// The guard checks the value written too: a finite number (a string of one for WriteValue),
// 0 or 1 on a switch; a switch takes 1/true/"1"/"on" and 0/false/"0"/"off" only.
func TestGateModbusWriteValues(t *testing.T) {
	engine, sent, _ := gateModbusEngine(t)
	for _, stmt := range []string{
		`RemoteObject.set(modbus.mode, 14, 0/0)`,
		`RemoteObject.set(modbus.mode, 14, math.huge)`,
		`RemoteObject.set(modbus.mode, 14, {})`,
		`RemoteObject.set(modbus.mode, 14, "3")`,
		`modbus.value:execute(1, 1/0)`,
		`modbus.value:execute(1, "nan")`,
		`modbus.value:execute(1, "1) x(")`,
		`modbus.value:execute(1, {})`,
		`modbus.value:execute(1)`,
		`modbus.value:execute(1, "1", "2")`,
		`modbus.relay:execute(1, "2")`,
		`RemoteObject.set(modbus.coil, 14, 5)`,
		`Wire.execute("CLU500000001.MOD0006", 1, 0/0)()`,
		`Wire.set("CLU500000001.MOD0002", 14, -1/0)()`,
		`modbus.relay:setValue("x")`,
		`modbus.relay:setValue(2)`,
		`modbus.mode:setValue(1/0)`,
		`modbus.mode:setValue(-1/0)`,
		`modbus.value:setValue(0/0)`,
	} {
		if got := try(t, engine, stmt); got == "ok" {
			t.Errorf("%s: written", stmt)
		}
	}
	if s := sent.take(); s != "" {
		t.Errorf("sent %q", s)
	}
	for stmt, want := range map[string]string{
		`modbus.relay:setValue("on")`:  `MOD0004:execute(1, "1")`,
		`modbus.relay:setValue("OFF")`: `MOD0004:execute(1, "0")`,
		`modbus.relay:setValue("1")`:   `MOD0004:execute(1, "1")`,
		`modbus.relay:setValue(false)`: `MOD0004:execute(1, "0")`,
		`modbus.coil:setValue(true)`:   "MOD0007:set(14, 1)",
	} {
		if got := try(t, engine, stmt); got != "ok" {
			t.Errorf("%s: %s", stmt, got)
		}
		if got := sent.take(); got != want {
			t.Errorf("%s sent %q, want %q", stmt, got, want)
		}
	}
}
