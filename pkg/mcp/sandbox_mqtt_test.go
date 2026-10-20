package mcp

import (
	"fmt"
	"strings"
	"testing"
)

const testTasmotaModule = `
local tasmota = require("lib/tasmota")
LAMPKA = tasmota.switch("tasmota_75088A")
expose(LAMPKA, "light", { path = "tasmota.biurko", name = "Lampka" })
local plug = tasmota.sensor("tasmota_gniazdko", "ENERGY.Power", { unit = "W" })
expose(plug, "number", { path = "tasmota.gniazdko_moc", name = "Gniazdko moc",
	readonly = true, unit = "W", device_class = "power", state_class = "measurement" })
`

// An agent checks a Tasmota module in the sandbox: a message from the device changes the
// object with the device as the actor, a change of the object
// (a set step, as any change not from the device) shows as a blocked publish.
func TestScenario_MQTTMessageReachesSubscription(t *testing.T) {
	se, ws, _ := newTestSandbox(t)
	writeDraft(t, ws, "modules/tasmota.lua", testTasmotaModule)
	res := runScenario(t, se, []string{"modules/tasmota.lua"}, "2026-10-19T23:00:00+02:00", `[
		{"mqtt": "stat/tasmota_75088A/POWER", "payload": "ON"},
		{"expect_state": {"tasmota.biurko": 1}},
		{"expect_history": {"path": "tasmota.biurko", "changes": [{"old": 0, "value": 1, "origin": "mqtt", "actor": "device:tasmota_75088A"}]}},
		{"mqtt": "tele/tasmota_gniazdko/SENSOR", "payload": "{\"ENERGY\":{\"Power\":40.5}}"},
		{"expect_state": {"tasmota.gniazdko_moc": 40.5}},
		{"set": "tasmota.biurko", "value": 0}
	]`)
	requireSuccess(t, res)
	var published []string
	for _, c := range res.BlockedCalls {
		if c.Type == "mqtt.publish" {
			published = append(published, fmt.Sprintf("%v=%v", c.Args["arg2"], c.Args["arg3"]))
		}
	}
	got := strings.Join(published, " ")
	want := "cmnd/tasmota_75088A/STATE= cmnd/tasmota_gniazdko/STATE= cmnd/tasmota_gniazdko/Status=10 cmnd/tasmota_75088A/POWER=OFF"
	if got != want {
		t.Errorf("published: %s", got)
	}
}

func TestParseSteps_MQTT(t *testing.T) {
	bad := map[string]string{
		`[{"mqtt": "a/b"}]`:                            "mqtt needs a topic and a payload",
		`[{"mqtt": "", "payload": "x"}]`:               "mqtt needs a topic and a payload",
		`[{"advance": 1, "payload": "x"}]`:             "payload is only allowed with mqtt",
		`[{"mqtt": "a", "payload": "", "advance": 1}]`: "exactly one",
	}
	for js, want := range bad {
		if _, err := ParseSteps([]byte(js)); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: err = %v, want %q", js, err, want)
		}
	}
	if _, err := ParseSteps([]byte(`[{"mqtt": "a/b", "payload": ""}]`)); err != nil {
		t.Errorf("an empty payload is a payload: %v", err)
	}
}

// an error in the module's MQTT callback must show in the result, not only in a log line.
func TestScenario_MQTTCallbackErrorIsReported(t *testing.T) {
	se, ws, _ := newTestSandbox(t)
	writeDraft(t, ws, "modules/zly.lua", `
MQTT:subscribe("stat/x/POWER", function(_, payload) error("bad payload " .. payload) end)
`)
	res := runScenario(t, se, []string{"modules/zly.lua"}, "2026-10-19T23:00:00+02:00", `[
		{"mqtt": "stat/x/POWER", "payload": "ON"}
	]`)
	if res.Success || !strings.Contains(strings.Join(res.Errors, "\n"), "bad payload") {
		t.Errorf("success=%v errors=%v", res.Success, res.Errors)
	}
}

// after a reload step the module's new subscriptions get messages.
func TestScenario_MQTTAfterReload(t *testing.T) {
	se, ws, _ := newTestSandbox(t)
	writeDraft(t, ws, "modules/tasmota.lua", testTasmotaModule)
	res := runScenario(t, se, []string{"modules/tasmota.lua"}, "2026-10-19T23:00:00+02:00", `[
		{"reload": true},
		{"mqtt": "stat/tasmota_75088A/POWER", "payload": "ON"},
		{"expect_state": {"tasmota.biurko": 1}}
	]`)
	requireSuccess(t, res)
}

// the step reaches a module's own client (MQTT:new) as well
func TestScenario_MQTTSecondClient(t *testing.T) {
	se, ws, _ := newTestSandbox(t)
	writeDraft(t, ws, "modules/zigbee.lua", `
local zb = MQTT:new("zigbee")
LAMPA_ZB = VirtualSwitch:new("lampa_zb")
expose(LAMPA_ZB, "switch", { path = "zb.lampa" })
zb:subscribe("zigbee2mqtt/lampa", function(_, payload) LAMPA_ZB:setValue(payload == "ON" and 1 or 0) end)
`)
	res := runScenario(t, se, []string{"modules/zigbee.lua"}, "2026-10-19T23:00:00+02:00", `[
		{"mqtt": "zigbee2mqtt/lampa", "payload": "ON"},
		{"expect_state": {"zb.lampa": 1}}
	]`)
	requireSuccess(t, res)
}
