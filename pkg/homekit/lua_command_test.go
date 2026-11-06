package homekit

import (
	"testing"
	"time"

	"github.com/Domktorymysli/vclu/pkg/accesscontrol"
	"github.com/Domktorymysli/vclu/pkg/lua"
	"github.com/brutella/hap/characteristic"
)

// The write script runs on the real Lua engine: an exposed object is switched,
// a missing one is reported (and the tile reverted), not silently ignored.
func TestCommandOnRealLua(t *testing.T) {
	engine := lua.NewLuaEngine()
	defer engine.Close()
	if _, err := engine.Execute(`
		sw = VirtualSwitch:new("hk")
		expose(sw, "switch", { name = "Lampa", path = "hk:lamp" })
	`); err != nil {
		t.Fatal(err)
	}
	s := &Server{luaExecutor: engine}

	w := s.createExposedAccessory(ExposedObject{Path: "hk:lamp", Type: "switch", Name: "Lampa", Value: float64(0)}, false)
	findChar(w.Accessory, characteristic.TypeOn).SetValueRequest(true, homeAppRequest())
	if v, err := engine.Execute(`return tostring(sw:getValue())`); err != nil || v != "1" {
		t.Fatalf("exposed object not switched: %q %v", v, err)
	}

	gone := s.createExposedAccessory(ExposedObject{Path: "hk:gone", Type: "switch", Name: "Nie ma", Value: float64(0)}, false)
	on := findChar(gone.Accessory, characteristic.TypeOn)
	if s.command("hk:gone", "h:setValue(1)", func(v float64) { on.SetValueRequest(v > 0, nil) }) {
		t.Fatal("write to a missing object reported success")
	}
}

// A target from the Home app reaches setSetpoint; in set point mode the tile keeps it (the
// CLU reports TargetTemp later), in holiday mode it shows the target in effect again.
func TestThermostatTargetFromHomeApp(t *testing.T) {
	engine, s := newClimateBridge(t, nil)
	startNotifier(t, engine, s)
	th := thermostatOf(t, s, "CLU1.THE1")
	exec(t, engine, `report(1, 0, 21, 21, 20.4)`)
	waitShown(t, "after a report", th, [4]any{20.4, 21.0, heat, heat})
	exec(t, engine, `_calls = {}`)

	if _, code := th.target.SetValueRequest(22.5, homeAppRequest()); code != 0 {
		t.Fatalf("target write refused: %d", code)
	}
	if got := exec(t, engine, `table.concat(_calls, ";")`); got != "THE1:set(3, 22.5)" {
		t.Errorf("CLU calls %q, want the set point THE1:set(3, 22.5)", got)
	}
	waitShown(t, "the set point before the CLU reports it", th, [4]any{20.4, 22.5, heat, heat})
	s.refreshAllStates(t.Context())
	if v := th.target.Value(); v != 22.5 {
		t.Errorf("a refresh before the report shows %v, want 22.5", v)
	}

	// holiday: the set point is stored, the target stays the holiday one
	exec(t, engine, `report(1, 1, 22.5, 17, 20.4)`)
	waitShown(t, "holiday", th, [4]any{20.4, 17.0, heat, heat})
	exec(t, engine, `_calls = {}`)
	th.target.SetValueRequest(23.0, homeAppRequest())
	if got := exec(t, engine, `table.concat(_calls, ";")`); got != "THE1:set(3, 23)" {
		t.Errorf("CLU calls %q, want THE1:set(3, 23)", got)
	}
	waitShown(t, "holiday after a write", th, [4]any{20.4, 17.0, heat, heat})

	// the virtual thermostat: its own clamp (default max 35) and its target at once
	vt := thermostatOf(t, s, "hk:vt")
	vt.target.SetValueRequest(26.0, homeAppRequest())
	if got := exec(t, engine, `tostring(VT:getSetpoint())`); got != "26" {
		t.Errorf("virtual thermostat set point %s, want 26", got)
	}
	if v := vt.target.Value(); v != 26.0 {
		t.Errorf("virtual thermostat target shown %v, want 26", v)
	}
}

// The mode is shown, not set (as in Home Assistant): a mode from the Home app sends nothing
// and the tile shows the real one again.
func TestThermostatModeFromHomeAppIsPutBack(t *testing.T) {
	engine, s := newClimateBridge(t, nil)
	th := thermostatOf(t, s, "CLU1.THE1")
	exec(t, engine, `report(1, 0, 21, 21, 20.4); _calls = {}`)
	s.refreshAllStates(t.Context())

	if _, code := th.tgtMode.SetValueRequest(off, homeAppRequest()); code != 0 {
		t.Fatalf("mode write answered %d", code)
	}
	if got := exec(t, engine, `table.concat(_calls, ";") .. "|" .. tostring(TH:getState())`); got != "|1" {
		t.Errorf("a mode from the Home app: CLU calls|state %q, want nothing sent and state 1", got)
	}
	if v := th.tgtMode.Value(); v != heat {
		t.Errorf("target mode %v after the write, want heat back", v)
	}
	if _, code := th.tgtMode.SetValueRequest(characteristic.TargetHeatingCoolingStateAuto, homeAppRequest()); code == 0 {
		t.Error("auto accepted: the thermostat has only off and heat")
	}
}

// Read-only (Decision: still a Thermostat, so the target is seen): the target and the mode
// lose PairedWrite; read-only set later (profile, Lua) is refused where it executes and put back.
func TestReadonlyThermostatRefusesTarget(t *testing.T) {
	mgr := accesscontrol.NewManager(t.TempDir())
	engine, s := newClimateBridge(t, mgr)
	exec(t, engine, `report(1, 0, 21, 21, 20.4); _calls = {}`)
	s.refreshAllStates(t.Context())

	ro := thermostatOf(t, s, "hk:biuro")
	for name, c := range map[string]*characteristic.C{"target": ro.target, "mode": ro.tgtMode} {
		if hasPerm(c, characteristic.PermissionWrite) {
			t.Errorf("read-only %s is writable", name)
		}
	}
	if _, code := ro.target.SetValueRequest(22.0, homeAppRequest()); code == 0 {
		t.Error("read-only thermostat accepted a target")
	}

	th := thermostatOf(t, s, "CLU1.THE1")
	mgr.SetAccess(accesscontrol.IntegrationHomeKit, "CLU1.THE1", accesscontrol.AccessReadonly)
	th.target.SetValueRequest(23.0, homeAppRequest())
	if v := th.target.Value(); v != 21.0 {
		t.Errorf("target %v after a refused write, want 21 back", v)
	}
	mgr.SetAccess(accesscontrol.IntegrationHomeKit, "CLU1.THE1", accesscontrol.AccessFull)
	exec(t, engine, `ExposedObjects:get("CLU1.THE1"):readonly()`)
	th.target.SetValueRequest(24.0, homeAppRequest())
	if v := th.target.Value(); v != 21.0 {
		t.Errorf("target %v after a write refused in Lua, want 21 back", v)
	}
	if got := exec(t, engine, `table.concat(_calls, ";") .. "|" .. tostring(TH:getSetpoint())`); got != "|21" {
		t.Errorf("a read-only thermostat got CLU calls|set point %q, want nothing and 21", got)
	}
}

// A target written while Home app writes are paused (Lua reload) is not sent and is put back.
func TestThermostatWriteDuringPauseReverts(t *testing.T) {
	engine, s := newClimateBridge(t, nil)
	th := thermostatOf(t, s, "CLU1.THE1")
	exec(t, engine, `report(1, 0, 21, 21, 20.4); _calls = {}`)
	s.refreshAllStates(t.Context())
	s.SetInitializing(true)
	defer s.SetInitializing(false)

	th.target.SetValueRequest(24.0, homeAppRequest())
	for deadline := time.Now().Add(5 * time.Second); th.target.Value() != 21.0; time.Sleep(5 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatalf("target %v during a pause, want 21 back", th.target.Value())
		}
	}
	if got := exec(t, engine, `table.concat(_calls, ";")`); got != "" {
		t.Errorf("sent during a pause: %q", got)
	}
}
