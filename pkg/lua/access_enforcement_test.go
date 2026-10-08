package lua

import (
	"testing"
)

// Access enforcement, end to end.
//
// Access control decides who may operate the house, so it is tested at the point
// where a write actually happens — not where an entity is advertised. Discovery,
// HomeKit permissions and dashboards only describe what a UI should show; a
// retained discovery message, a stale entity or a direct MQTT publish all reach
// the command handler without passing any of them.
//
// The bug these tests pin down: the level set per object in the UI was only ever
// snapshotted into options.readonly at expose time, under an AND across MQTT and
// HomeKit. An object set readonly for MQTT alone therefore stayed fully
// controllable. Enforcement now resolves access live, per integration, on every
// command.

// accessTestSetup registers a switch in the registry, exposes it, and gives the
// test a write counter so "was it actually written" is observable.
const accessTestSetup = `
	_writes = 0
	_dev = {
		_type = "DOUT", _lastState = 0,
		get = function(s) return s._lastState end,
		set = function(s, _, v) _writes = _writes + 1; s._lastState = v end,
		onChange = function(s, cb) return function() end end,
	}
	registerObject("CLU.DOU0001", _dev)

	_pub = {}
	_fake = {
		publish = function(self, topic, payload) table.insert(_pub, {topic=topic, payload=payload}) end,
		subscribe = function() end, unsubscribe = function() end,
	}
	HAStatePublisher.setup(_fake)

	expose(_dev, "switch", { name = "Test", path = "CLU.DOU0001" })

	-- Both command entry points for the same object.
	function _sendExposed()
		return HACommands.handleExposedMessage("vclu/exposed/vclu/switch/clu_dou0001/set", "ON")
	end
	function _sendRegistry()
		return HACommands.handleMessage("vclu/clu/switch/dou0001/set", "ON")
	end
`

func setupAccessTest(t *testing.T, extra string) *LuaEngine {
	t.Helper()
	engine := NewLuaEngine()
	t.Cleanup(engine.Close)
	if _, err := engine.Execute(accessTestSetup + extra); err != nil {
		engine.Close()
		t.Fatalf("setup failed: %v", err)
	}
	return engine
}

func writes(t *testing.T, e *LuaEngine) string {
	t.Helper()
	return mustExec(t, e, `tostring(_writes)`)
}

// The exact regression: readonly for MQTT only. Previously the AND across MQTT and
// HomeKit meant this object was never marked readonly and stayed controllable.
func TestAccess_ReadonlyForMqttOnly_BlocksMqttCommand(t *testing.T) {
	e := setupAccessTest(t, `
		AccessControl.set("mqtt", "CLU.DOU0001", "readonly")
		-- homekit deliberately left at full: the integrations are independent
		_okExposed = _sendExposed()
		_okRegistry = _sendRegistry()
	`)

	if got := mustExec(t, e, `tostring(_okExposed)`); got != "false" {
		t.Errorf("exposed command path allowed a write on an MQTT-readonly object (ok=%s)", got)
	}
	if got := mustExec(t, e, `tostring(_okRegistry)`); got != "false" {
		t.Errorf("registry command path allowed a write on an MQTT-readonly object (ok=%s)", got)
	}
	if got := writes(t, e); got != "0" {
		t.Errorf("object was written %s time(s) despite MQTT readonly", got)
	}
	if got := mustExec(t, e, `tostring(AccessControl.get("homekit", "CLU.DOU0001"))`); got != "full" {
		t.Errorf("HomeKit access should be untouched by an MQTT-only setting, got %q", got)
	}
}

// Access must be resolved live. Changing the level after the object was exposed has
// to take effect on the very next command, with no re-expose and no reload.
func TestAccess_LevelChangeTakesEffectImmediately(t *testing.T) {
	e := setupAccessTest(t, `
		_okBefore = _sendExposed()
		AccessControl.set("mqtt", "CLU.DOU0001", "readonly")
		_okAfter = _sendExposed()
	`)

	if got := mustExec(t, e, `tostring(_okBefore)`); got != "true" {
		t.Fatalf("command was refused while access was still full (ok=%s)", got)
	}
	if got := mustExec(t, e, `tostring(_okAfter)`); got != "false" {
		t.Errorf("command accepted after the object was set readonly — access is snapshotted, not live (ok=%s)", got)
	}
	if got := writes(t, e); got != "1" {
		t.Errorf("expected exactly the one pre-readonly write, got %s", got)
	}
}

// The reverse direction: lifting readonly must restore control, so the guard cannot
// silently latch into "deny everything".
func TestAccess_LiftingReadonlyRestoresControl(t *testing.T) {
	e := setupAccessTest(t, `
		AccessControl.set("mqtt", "CLU.DOU0001", "readonly")
		_okBlocked = _sendExposed()
		AccessControl.set("mqtt", "CLU.DOU0001", "full")
		_okAllowed = _sendExposed()
	`)

	if got := mustExec(t, e, `tostring(_okBlocked)`); got != "false" {
		t.Errorf("readonly did not block (ok=%s)", got)
	}
	if got := mustExec(t, e, `tostring(_okAllowed)`); got != "true" {
		t.Errorf("control not restored after lifting readonly (ok=%s)", got)
	}
	if got := writes(t, e); got != "1" {
		t.Errorf("expected exactly one write (the allowed one), got %s", got)
	}
}

// Hidden is stricter than readonly and must also stop writes, not merely hide the
// object from discovery.
func TestAccess_HiddenBlocksCommand(t *testing.T) {
	e := setupAccessTest(t, `
		AccessControl.set("mqtt", "CLU.DOU0001", "hidden")
		_okExposed = _sendExposed()
		_okRegistry = _sendRegistry()
	`)

	if got := mustExec(t, e, `tostring(_okExposed)`); got != "false" {
		t.Errorf("exposed command path allowed a write on a hidden object (ok=%s)", got)
	}
	if got := mustExec(t, e, `tostring(_okRegistry)`); got != "false" {
		t.Errorf("registry command path allowed a write on a hidden object (ok=%s)", got)
	}
	if got := writes(t, e); got != "0" {
		t.Errorf("hidden object was written %s time(s)", got)
	}
}

// Full access must still work — a guard that blocks everything is not a fix.
func TestAccess_FullAllowsCommand(t *testing.T) {
	e := setupAccessTest(t, `
		AccessControl.set("mqtt", "CLU.DOU0001", "full")
		_ok = _sendExposed()
	`)

	if got := mustExec(t, e, `tostring(_ok)`); got != "true" {
		t.Errorf("full access refused a command (ok=%s)", got)
	}
	if got := writes(t, e); got != "1" {
		t.Errorf("expected 1 write, got %s", got)
	}
}

// The expose() option is an independent source of readonly and must keep working
// on its own, even when AccessControl says full.
func TestAccess_ExposeOptionReadonlyIndependentOfAccessControl(t *testing.T) {
	e := setupAccessTest(t, `
		AccessControl.set("mqtt", "CLU.DOU0001", "full")
		expose(_dev, "switch", { name = "Test", path = "CLU.DOU0001", readonly = true })
		_ok = _sendExposed()
	`)

	if got := mustExec(t, e, `tostring(_ok)`); got != "false" {
		t.Errorf("expose(readonly=true) did not block while AccessControl was full (ok=%s)", got)
	}
	if got := writes(t, e); got != "0" {
		t.Errorf("object written %s time(s) despite expose(readonly=true)", got)
	}
}

// Access reached through a profile assignment (rather than a direct per-object set)
// must be enforced the same way — that is the path real deployments use.
func TestAccess_ProfileAssignedReadonlyBlocksCommand(t *testing.T) {
	e := setupAccessTest(t, `
		AccessControl.loadProfiles(
			{ guests = { default = "readonly", objects = {} } },
			{ mqtt = { "guests" } }
		)
		_level = AccessControl.get("mqtt", "CLU.DOU0001")
		_ok = _sendExposed()
	`)

	if got := mustExec(t, e, `tostring(_level)`); got != "readonly" {
		t.Fatalf("profile assignment did not resolve to readonly, got %q", got)
	}
	if got := mustExec(t, e, `tostring(_ok)`); got != "false" {
		t.Errorf("command allowed despite a readonly profile assignment (ok=%s)", got)
	}
	if got := writes(t, e); got != "0" {
		t.Errorf("object written %s time(s) under a readonly profile", got)
	}
}

// With multiple profiles the resolver takes the most permissive level; enforcement
// must act on that resolved value, not on any single profile.
func TestAccess_MultiProfileMostPermissiveIsEnforced(t *testing.T) {
	e := setupAccessTest(t, `
		AccessControl.loadProfiles(
			{
				guests = { default = "readonly", objects = {} },
				admins = { default = "full",     objects = {} },
			},
			{ mqtt = { "guests", "admins" } }
		)
		_level = AccessControl.get("mqtt", "CLU.DOU0001")
		_ok = _sendExposed()
	`)

	if got := mustExec(t, e, `tostring(_level)`); got != "full" {
		t.Fatalf("multi-profile resolution should be most-permissive, got %q", got)
	}
	if got := mustExec(t, e, `tostring(_ok)`); got != "true" {
		t.Errorf("command refused although the resolved level is full (ok=%s)", got)
	}
}

// exposeRegistry() must no longer stamp options.readonly. The flag was a snapshot
// that went stale; enforcement has to come from the live level instead.
func TestAccess_ExposeRegistryDoesNotSnapshotReadonly(t *testing.T) {
	engine := NewLuaEngine()
	defer engine.Close()

	script := `
		_writes = 0
		_dev = {
			_type = "DOUT", _lastState = 0,
			get = function(s) return s._lastState end,
			set = function(s, _, v) _writes = _writes + 1; s._lastState = v end,
			onChange = function(s, cb) return function() end end,
		}
		registerObject("CLU.DOU0002", _dev)

		AccessControl.set("mqtt",    "CLU.DOU0002", "readonly")
		AccessControl.set("homekit", "CLU.DOU0002", "readonly")
		exposeRegistry()

		_handle = ExposedObjects:get("CLU.DOU0002")
		_ok = HACommands.handleExposedMessage("vclu/exposed/vclu/switch/clu_dou0002/set", "ON")
	`
	if _, err := engine.Execute(script); err != nil {
		t.Fatalf("setup failed: %v", err)
	}

	if got := mustExec(t, engine, `tostring(_handle ~= nil)`); got != "true" {
		t.Fatal("exposeRegistry did not expose the object")
	}
	if got := mustExec(t, engine, `tostring(_handle.options.readonly)`); got != "nil" {
		t.Errorf("exposeRegistry still snapshots options.readonly (=%s); access must be resolved live", got)
	}
	// The point: no snapshot, yet the write is still refused.
	if got := mustExec(t, engine, `tostring(_ok)`); got != "false" {
		t.Errorf("write allowed on a readonly object after removing the snapshot (ok=%s)", got)
	}
	if got := mustExec(t, engine, `tostring(_writes)`); got != "0" {
		t.Errorf("object written %s time(s) despite being readonly", got)
	}
}
