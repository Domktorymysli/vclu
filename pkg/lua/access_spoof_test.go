package lua

import "testing"

// Authorization must be decided on the identity of the object that was actually
// found, never on a string rebuilt from attacker-controlled input.
//
// HACommands.findObject resolves an object through several fallbacks — including
// bare objectId and the Lua global table — none of which involve the cluId segment
// of the topic. The access check, however, used to rebuild the path as
// UPPER(cluId).."."..UPPER(objectId). Since the MQTT subscription is vclu/+/+/+/set,
// cluId is whatever the publisher puts there: sending to a made-up CLU id found the
// same device but looked up access for a path that does not exist, which falls
// through to the profile default (normally full). One mosquitto_pub then operated a
// device marked hidden.

const spoofSetup = `
	_writes = 0
	_gate = {
		_type = "DOUT", _lastState = 0,
		get = function(s) return s._lastState end,
		set = function(s, _, v) _writes = _writes + 1; s._lastState = v end,
		execute = function(s, m) _writes = _writes + 1; s._lastState = (m == 1) and 1 or 0 end,
		onChange = function(s, cb) return function() end end,
	}
	-- Registered the way om.lua registers real objects.
	registerObject("CLU221.GARAGE", _gate)
	AccessControl.set("mqtt", "CLU221.GARAGE", "hidden")
`

// The honest topic must be refused — this is the baseline the spoof is compared to.
func TestAccessSpoof_HonestTopicIsBlocked(t *testing.T) {
	engine := NewLuaEngine()
	defer engine.Close()

	if _, err := engine.Execute(spoofSetup + `
		_ok = HACommands.handleMessage("vclu/clu221/switch/garage/set", "ON")
	`); err != nil {
		t.Fatalf("setup failed: %v", err)
	}

	if got := mustExec(t, engine, `tostring(_ok)`); got != "false" {
		t.Errorf("hidden object accepted a command on its own topic (ok=%s)", got)
	}
	if got := mustExec(t, engine, `tostring(_writes)`); got != "0" {
		t.Errorf("hidden object was written %s time(s)", got)
	}
}

// The attack: same object, arbitrary CLU id in the topic.
func TestAccessSpoof_ForgedCluIdMustNotBypassAccess(t *testing.T) {
	engine := NewLuaEngine()
	defer engine.Close()

	if _, err := engine.Execute(spoofSetup + `
		-- "zzz" is not a real CLU; findObject still resolves the device by objectId.
		_ok = HACommands.handleMessage("vclu/zzz/switch/garage/set", "ON")
	`); err != nil {
		t.Fatalf("setup failed: %v", err)
	}

	if got := mustExec(t, engine, `tostring(_ok)`); got != "false" {
		t.Errorf("SECURITY: a forged CLU id in the topic bypassed access control (ok=%s)", got)
	}
	if got := mustExec(t, engine, `tostring(_writes)`); got != "0" {
		t.Errorf("SECURITY: hidden device was operated %s time(s) via a forged topic", got)
	}
	if got := mustExec(t, engine, `tostring(_gate._lastState)`); got != "0" {
		t.Errorf("SECURITY: device state changed to %s via a forged topic", got)
	}
}

// Readonly must resist the same trick, not just hidden.
func TestAccessSpoof_ForgedCluIdAgainstReadonly(t *testing.T) {
	engine := NewLuaEngine()
	defer engine.Close()

	if _, err := engine.Execute(`
		_writes = 0
		_boiler = {
			_type = "DOUT", _lastState = 0,
			get = function(s) return s._lastState end,
			set = function(s, _, v) _writes = _writes + 1; s._lastState = v end,
			execute = function(s, m) _writes = _writes + 1; s._lastState = (m == 1) and 1 or 0 end,
			onChange = function(s, cb) return function() end end,
		}
		registerObject("CLU221.BOILER", _boiler)
		AccessControl.set("mqtt", "CLU221.BOILER", "readonly")
		_ok = HACommands.handleMessage("vclu/whatever/switch/boiler/set", "ON")
	`); err != nil {
		t.Fatalf("setup failed: %v", err)
	}

	if got := mustExec(t, engine, `tostring(_ok)`); got != "false" {
		t.Errorf("SECURITY: forged CLU id bypassed readonly (ok=%s)", got)
	}
	if got := mustExec(t, engine, `tostring(_writes)`); got != "0" {
		t.Errorf("SECURITY: readonly device operated %s time(s) via a forged topic", got)
	}
}

// An object reachable only through the Lua global table has no registry path, so
// there is nothing to authorize against. That must deny, not fall through to a
// default — an unidentifiable object cannot be granted access.
func TestAccessSpoof_GlobalOnlyObjectIsDenied(t *testing.T) {
	engine := NewLuaEngine()
	defer engine.Close()

	if _, err := engine.Execute(`
		_writes = 0
		ORPHAN = {
			_type = "DOUT", _lastState = 0,
			get = function(s) return s._lastState end,
			set = function(s, _, v) _writes = _writes + 1; s._lastState = v end,
			execute = function(s, m) _writes = _writes + 1; s._lastState = (m == 1) and 1 or 0 end,
			onChange = function(s, cb) return function() end end,
		}
		_ok = HACommands.handleMessage("vclu/any/switch/orphan/set", "ON")
	`); err != nil {
		t.Fatalf("setup failed: %v", err)
	}

	if got := mustExec(t, engine, `tostring(_ok)`); got != "false" {
		t.Errorf("SECURITY: object resolved only via _G was operated without any access identity (ok=%s)", got)
	}
	if got := mustExec(t, engine, `tostring(_writes)`); got != "0" {
		t.Errorf("SECURITY: global-only object written %s time(s)", got)
	}
}

// Control through the correct topic on a full-access object must keep working.
func TestAccessSpoof_LegitimateCommandStillWorks(t *testing.T) {
	engine := NewLuaEngine()
	defer engine.Close()

	if _, err := engine.Execute(`
		_writes = 0
		_lamp = {
			_type = "DOUT", _lastState = 0,
			get = function(s) return s._lastState end,
			set = function(s, _, v) _writes = _writes + 1; s._lastState = v end,
			execute = function(s, m) _writes = _writes + 1; s._lastState = (m == 1) and 1 or 0 end,
			onChange = function(s, cb) return function() end end,
		}
		registerObject("CLU221.LAMP", _lamp)
		AccessControl.set("mqtt", "CLU221.LAMP", "full")
		_ok = HACommands.handleMessage("vclu/clu221/switch/lamp/set", "ON")
	`); err != nil {
		t.Fatalf("setup failed: %v", err)
	}

	if got := mustExec(t, engine, `tostring(_ok)`); got != "true" {
		t.Errorf("legitimate command refused (ok=%s)", got)
	}
	if got := mustExec(t, engine, `tostring(_writes)`); got != "1" {
		t.Errorf("expected 1 write, got %s", got)
	}
}

// Production shape: om.lua registers each object under CLU.ID *and* leaves it as a
// Lua global. Hiding it by its registry path must not be defeatable by naming it
// through any other topic.
func TestAccessSpoof_ProductionShape_RegistryPlusGlobal(t *testing.T) {
	engine := NewLuaEngine()
	defer engine.Close()

	if _, err := engine.Execute(`
		_writes = 0
		DOU6550 = {
			_type = "DOUT", _lastState = 0,
			get = function(s) return s._lastState end,
			set = function(s, _, v) _writes = _writes + 1; s._lastState = v end,
			execute = function(s, m) _writes = _writes + 1; s._lastState = (m == 1) and 1 or 0 end,
			onChange = function(s, cb) return function() end end,
		}
		registerObject("CLU212345678.DOU6550", DOU6550)
		AccessControl.set("mqtt", "CLU212345678.DOU6550", "hidden")

		_okHonest = HACommands.handleMessage("vclu/clu212345678/switch/dou6550/set", "ON")
		_okForged = HACommands.handleMessage("vclu/anything/switch/dou6550/set", "ON")
	`); err != nil {
		t.Fatalf("setup failed: %v", err)
	}

	if got := mustExec(t, engine, `tostring(_okHonest)`); got != "false" {
		t.Errorf("hidden object accepted its own topic (ok=%s)", got)
	}
	if got := mustExec(t, engine, `tostring(_okForged)`); got != "false" {
		t.Errorf("SECURITY: forged topic reached a hidden object via the Lua global (ok=%s)", got)
	}
	if got := mustExec(t, engine, `tostring(_writes)`); got != "0" {
		t.Errorf("SECURITY: hidden gate operated %s time(s)", got)
	}
}
