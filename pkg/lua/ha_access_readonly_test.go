package lua

import (
	"strings"
	"testing"
)

// An object set to "Readonly" for MQTT in the dashboard (AccessControl) must be
// treated as read-only by the Home Assistant integration.
//
// Before these tests, both the discovery generator and the command handler looked
// only at options.readonly — the flag from expose() — and ignored the AccessControl
// level entirely. A boiler marked Readonly in the UI was published to HA as a
// switch with a command_topic and could be switched on and off from HA.

const haReadonlySetup = `
	_pub = {}
	_fake = {
		publish = function(self, topic, payload, qos, retain)
			table.insert(_pub, { topic = topic, payload = payload })
		end,
		subscribe = function() end, unsubscribe = function() end,
	}
	HAStatePublisher.setup(_fake)

	_writes = 0
	_boiler = {
		_type = "DOUT", _lastState = 0,
		get = function(s) return s._lastState end,
		set = function(s, _, v) _writes = _writes + 1; s._lastState = v end,
		onChange = function(s, cb) return function() end end,
	}
`

// findDiscovery returns the retained discovery payload for a unique id fragment.
func findDiscovery(t *testing.T, engine *LuaEngine, idFragment string) string {
	t.Helper()
	return mustExec(t, engine, `(function()
		local found = ""
		for _, p in ipairs(_pub) do
			if p.topic:find("`+idFragment+`", 1, true) and p.topic:find("/config", 1, true)
			   and p.payload ~= "" then
				found = p.payload
			end
		end
		return found
	end)()`)
}

// AccessControl "readonly" must strip command_topic and remap the component, exactly
// as expose(..., {readonly=true}) does.
func TestHADiscovery_AccessControlReadonlyDropsCommandTopic(t *testing.T) {
	engine := NewLuaEngine()
	defer engine.Close()

	setup := haReadonlySetup + `
		AccessControl.set("mqtt", "t:boiler", "readonly")
		expose(_boiler, "switch", { name = "Piec", area = "Kotlownia", path = "t:boiler" })
	`
	if _, err := engine.Execute(setup); err != nil {
		t.Fatalf("setup failed: %v", err)
	}

	payload := findDiscovery(t, engine, "t_boiler")
	if payload == "" {
		t.Fatal("no discovery config published for the boiler")
	}
	if strings.Contains(payload, "command_topic") {
		t.Errorf("readonly object was published WITH a command_topic — HA will render a toggle: %s", payload)
	}
}

// The control that actually matters: even if a command reaches the topic — retained
// discovery, a stale HA entity, or a direct publish — it must be refused.
func TestHACommand_AccessControlReadonlyBlocksWrite(t *testing.T) {
	engine := NewLuaEngine()
	defer engine.Close()

	setup := haReadonlySetup + `
		AccessControl.set("mqtt", "t:boiler", "readonly")
		expose(_boiler, "switch", { name = "Piec", area = "Kotlownia", path = "t:boiler" })
		_ok, _err = HACommands.handleExposedMessage("vclu/exposed/t/switch/boiler/set", "ON")
	`
	if _, err := engine.Execute(setup); err != nil {
		t.Fatalf("setup failed: %v", err)
	}

	if got := mustExec(t, engine, `tostring(_ok)`); got != "false" {
		t.Errorf("command on a readonly object returned ok=%s, want false (err=%s)",
			got, mustExec(t, engine, `tostring(_err)`))
	}
	if got := mustExec(t, engine, `tostring(_writes)`); got != "0" {
		t.Errorf("readonly object was written %s time(s) — the boiler was switched", got)
	}
	if got := mustExec(t, engine, `tostring(_boiler._lastState)`); got != "0" {
		t.Errorf("boiler state changed to %s despite being readonly", got)
	}
}

// A full-access object must stay controllable — the guard must not block everything.
func TestHACommand_FullAccessStillWrites(t *testing.T) {
	engine := NewLuaEngine()
	defer engine.Close()

	setup := haReadonlySetup + `
		AccessControl.set("mqtt", "t:boiler", "full")
		expose(_boiler, "switch", { name = "Piec", area = "Kotlownia", path = "t:boiler" })
		_ok, _err = HACommands.handleExposedMessage("vclu/exposed/t/switch/boiler/set", "ON")
	`
	if _, err := engine.Execute(setup); err != nil {
		t.Fatalf("setup failed: %v", err)
	}

	if got := mustExec(t, engine, `tostring(_ok)`); got != "true" {
		t.Errorf("command on a full-access object returned ok=%s, want true (err=%s)",
			got, mustExec(t, engine, `tostring(_err)`))
	}
	if got := mustExec(t, engine, `tostring(_boiler._lastState)`); got != "1" {
		t.Errorf("full-access object was not switched, state=%s", got)
	}
}

// The expose() option must keep working on its own, with AccessControl left at full.
func TestHACommand_ExposeOptionReadonlyStillBlocks(t *testing.T) {
	engine := NewLuaEngine()
	defer engine.Close()

	setup := haReadonlySetup + `
		expose(_boiler, "switch", { name = "Piec", path = "t:boiler", readonly = true })
		_ok = HACommands.handleExposedMessage("vclu/exposed/t/switch/boiler/set", "ON")
	`
	if _, err := engine.Execute(setup); err != nil {
		t.Fatalf("setup failed: %v", err)
	}

	if got := mustExec(t, engine, `tostring(_ok)`); got != "false" {
		t.Errorf("expose(readonly=true) no longer blocks commands: ok=%s", got)
	}
	if got := mustExec(t, engine, `tostring(_writes)`); got != "0" {
		t.Errorf("object written %s time(s) despite expose(readonly=true)", got)
	}
}

// A readonly object must still REPORT. Read-only means "you cannot operate it",
// not "you cannot see it" — the boiler relay genuinely reflects whether the boiler
// is running, and that has to reach HA.
//
// Remapping switch -> binary_sensor changes only the discovery component; the state
// topic and the ON/OFF payload stay as they were, and HA's MQTT binary_sensor
// defaults are payload_on="ON" / payload_off="OFF". This test pins that chain.
func TestHAState_ReadonlyObjectStillPublishesOnAndOff(t *testing.T) {
	engine := NewLuaEngine()
	defer engine.Close()

	setup := haReadonlySetup + `
		AccessControl.set("mqtt", "t:boiler", "readonly")
		expose(_boiler, "switch", { name = "Piec", area = "Kotlownia", path = "t:boiler" })

		-- helper: last payload seen on the state topic
		function _lastState()
			local found = nil
			for _, p in ipairs(_pub) do
				if p.topic == "vclu/exposed/t/switch/boiler/state" then found = p.payload end
			end
			return found
		end

		_stateOff = _lastState()

		-- the relay closes (boiler starts) — simulate the device reporting 1
		_boiler._lastState = 1
		HAStatePublisher.refresh()
		_stateOn = _lastState()

		-- and opens again
		_boiler._lastState = 0
		HAStatePublisher.refresh()
		_stateOff2 = _lastState()
	`
	if _, err := engine.Execute(setup); err != nil {
		t.Fatalf("setup failed: %v", err)
	}

	if got := mustExec(t, engine, `tostring(_stateOff)`); got != "OFF" {
		t.Errorf("initial state payload = %q, want OFF", got)
	}
	if got := mustExec(t, engine, `tostring(_stateOn)`); got != "ON" {
		t.Errorf("readonly object did not report ON when it switched on (got %q) — "+
			"read-only must still report state", got)
	}
	if got := mustExec(t, engine, `tostring(_stateOff2)`); got != "OFF" {
		t.Errorf("state payload after switching off = %q, want OFF", got)
	}

	// And the entity it reports as must be the read-only component.
	payload := findDiscovery(t, engine, "t_boiler")
	if payload == "" {
		t.Fatal("no discovery config published")
	}
	if !strings.Contains(payload, `"state_topic":"vclu/exposed/t/switch/boiler/state"`) {
		t.Errorf("discovery points at a different state topic than the publisher writes: %s", payload)
	}
}
