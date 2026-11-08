package lua

import "testing"

// Authorization must be decided on the identity of the object that was actually
// found, never on a string rebuilt from attacker-controlled input.
//
// An exposed object is found by its exposed topic and its access is checked on the path
// it was exposed under; the {base} segment and the id must name it exactly.

const spoofSetup = mqttBroker + `
	_writes = 0
	function _mk()
		return {
			_type = "DOUT", _lastState = 0,
			get = function(s) return s._lastState end,
			set = function(s, _, v) _writes = _writes + 1; s._lastState = v end,
			execute = function(s, m) _writes = _writes + 1; s._lastState = (m == 1) and 1 or 0 end,
			onChange = function(s, cb) return function() end end,
		}
	end
	-- Registered the way om.lua registers real objects.
	_gate = _mk()
	registerObject("CLU221.GARAGE", _gate)
	expose(_gate, "switch", { name = "Brama" })
	AccessControl.set("mqtt", "CLU221.GARAGE", "hidden")
	-- ok:err of a direct call, for the report
	function _send(topic)
		local ok, err = HACommands.handleExposedMessage(topic, "ON")
		return tostring(ok) .. ":" .. tostring(err)
	end
`

func spoofEngine(t *testing.T, extra string) *LuaEngine {
	t.Helper()
	engine := NewLuaEngine()
	t.Cleanup(engine.Close)
	if _, err := engine.Execute(spoofSetup + extra); err != nil {
		t.Fatalf("setup failed: %v", err)
	}
	return engine
}

func expectNoWrites(t *testing.T, engine *LuaEngine) {
	t.Helper()
	if got := mustExec(t, engine, `tostring(_writes)`); got != "0" {
		t.Errorf("SECURITY: device written %s time(s)", got)
	}
}

// The honest exposed topic of a hidden object is refused; its old registry topic is
// taken by nothing.
func TestAccessSpoof_HonestTopicIsBlocked(t *testing.T) {
	engine := spoofEngine(t, "")

	if got := mustExec(t, engine, `_send("vclu/exposed/vclu/switch/clu221_garage/set")`); got != "false:access denied: CLU221.GARAGE" {
		t.Errorf("hidden object on its exposed topic = %s, want access denied", got)
	}
	if got := mustExec(t, engine, `deliver("vclu/exposed/vclu/switch/clu221_garage/set", "ON") .. "|" ..
		deliver("vclu/clu221/switch/garage/set", "ON")`); got != "1|0" {
		t.Errorf("handlers for the exposed|registry topic = %s, want 1|0", got)
	}
	expectNoWrites(t, engine)
}

// The old attack (any CLU id) and its exposed analogues (another base, the id in
// another case) reach nothing.
func TestAccessSpoof_ForgedCluIdMustNotBypassAccess(t *testing.T) {
	engine := spoofEngine(t, "")

	for _, topic := range []string{
		"vclu/exposed/zzz/switch/clu221_garage/set",
		"vclu/exposed/vclu/switch/CLU221_GARAGE/set",
		"vclu/exposed/vclu/switch/garage/set",
	} {
		if got := mustExec(t, engine, `_send("`+topic+`")`); got != "false:exposed object not found" {
			t.Errorf("SECURITY: %s = %s, want exposed object not found", topic, got)
		}
	}
	if got := mustExec(t, engine, `deliver("vclu/zzz/switch/garage/set", "ON")`); got != "0" {
		t.Errorf("SECURITY: a forged registry topic reached %s handler(s)", got)
	}
	expectNoWrites(t, engine)
	if got := mustExec(t, engine, `tostring(_gate._lastState)`); got != "0" {
		t.Errorf("SECURITY: device state changed to %s via a forged topic", got)
	}
}

// Readonly resists the same tricks, not just hidden.
func TestAccessSpoof_ForgedCluIdAgainstReadonly(t *testing.T) {
	engine := spoofEngine(t, `
		_boiler = _mk()
		registerObject("CLU221.BOILER", _boiler)
		expose(_boiler, "switch", { name = "Piec" })
		AccessControl.set("mqtt", "CLU221.BOILER", "readonly")
	`)

	if got := mustExec(t, engine, `_send("vclu/exposed/vclu/switch/clu221_boiler/set")`); got != "false:access denied: CLU221.BOILER" {
		t.Errorf("readonly object on its exposed topic = %s, want access denied", got)
	}
	if got := mustExec(t, engine, `_send("vclu/exposed/whatever/switch/clu221_boiler/set")`); got != "false:exposed object not found" {
		t.Errorf("SECURITY: forged base = %s, want exposed object not found", got)
	}
	if got := mustExec(t, engine, `deliver("vclu/whatever/switch/boiler/set", "ON")`); got != "0" {
		t.Errorf("SECURITY: a forged registry topic reached %s handler(s)", got)
	}
	expectNoWrites(t, engine)
}

// An object reachable only through the Lua global table is not exposed, so no topic
// reaches it.
func TestAccessSpoof_GlobalOnlyObjectIsDenied(t *testing.T) {
	engine := spoofEngine(t, `ORPHAN = _mk()`)

	if got := mustExec(t, engine, `_send("vclu/exposed/vclu/switch/orphan/set")`); got != "false:exposed object not found" {
		t.Errorf("SECURITY: global-only object = %s, want exposed object not found", got)
	}
	if got := mustExec(t, engine, `deliver("vclu/any/switch/orphan/set", "ON")`); got != "0" {
		t.Errorf("SECURITY: a registry topic reached %s handler(s)", got)
	}
	expectNoWrites(t, engine)
}

// Control through the exposed topic of a full-access object keeps working; its old
// registry topic does not.
func TestAccessSpoof_LegitimateCommandStillWorks(t *testing.T) {
	engine := spoofEngine(t, `
		_lamp = _mk()
		registerObject("CLU221.LAMP", _lamp)
		expose(_lamp, "switch", { name = "Lampa" })
		AccessControl.set("mqtt", "CLU221.LAMP", "full")
	`)

	if got := mustExec(t, engine, `deliver("vclu/clu221/switch/lamp/set", "ON") .. "|" .. _writes`); got != "0|0" {
		t.Errorf("registry topic: handlers|writes = %s, want 0|0", got)
	}
	if got := mustExec(t, engine, `deliver("vclu/exposed/vclu/switch/clu221_lamp/set", "ON") .. "|" .. _writes .. "|" .. _lamp._lastState`); got != "1|1|1" {
		t.Errorf("exposed topic: handlers|writes|state = %s, want 1|1|1", got)
	}
}

// Production shape: om.lua registers each object under CLU.ID *and* leaves it as a
// Lua global. Hiding it by its registry path cannot be defeated through any topic.
func TestAccessSpoof_ProductionShape_RegistryPlusGlobal(t *testing.T) {
	engine := spoofEngine(t, `
		DOU6550 = _mk()
		registerObject("CLU212345678.DOU6550", DOU6550)
		exposeRegistry()
		AccessControl.set("mqtt", "CLU212345678.DOU6550", "hidden")
	`)

	if got := mustExec(t, engine, `_send("vclu/exposed/vclu/switch/clu212345678_dou6550/set")`); got != "false:access denied: CLU212345678.DOU6550" {
		t.Errorf("hidden object on its exposed topic = %s, want access denied", got)
	}
	if got := mustExec(t, engine, `_send("vclu/exposed/anything/switch/clu212345678_dou6550/set")`); got != "false:exposed object not found" {
		t.Errorf("SECURITY: forged base = %s, want exposed object not found", got)
	}
	if got := mustExec(t, engine, `deliver("vclu/clu212345678/switch/dou6550/set", "ON") .. "|" ..
		deliver("vclu/anything/switch/dou6550/set", "ON")`); got != "0|0" {
		t.Errorf("SECURITY: registry topics reached handlers: %s, want 0|0", got)
	}
	expectNoWrites(t, engine)
}
