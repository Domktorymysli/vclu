package homekit

import (
	"slices"
	"testing"
	"time"

	"github.com/Domktorymysli/vclu/pkg/accesscontrol"
	"github.com/Domktorymysli/vclu/pkg/lua"
	"github.com/brutella/hap/characteristic"
)

// A thermostat exposed as climate is a Thermostat in Apple Home (#62): current and target
// temperature and the mode as Home Assistant shows them; a target from the Home app goes to
// setSetpoint unless it is read-only.

const climateSetup = `
	_calls = {}
	local clu = {
		address = "192.168.0.3",
		execute = function(s, i, cmd) table.insert(_calls, cmd) end,
		executeNoReply = function(s, i, cmd) table.insert(_calls, cmd) end,
	}
	TH = RemoteThermostat:new(clu, "THE1", "aSalon")
	registerObject("CLU1.THE1", TH)
	RemoteSubscription.activeSubscriptions = {}
	local objs = {}
	for _, f in ipairs(TH:subscribeFeatures()) do objs[#objs + 1] = { objId = "THE1", featureIndex = f } end
	RemoteSubscription.activeSubscriptions["192.168.0.3:5"] = { cluSerial = "CLU1", objects = objs }
	-- a report as the CLU sends it: State, Mode, PointValue, TargetTemp, CurrentTemp
	function report(state, mode, point, target, current)
		RemoteSubscription.updateRegistry("192.168.0.3", 5, { state, mode, point, target, current })
	end
	expose(TH, "climate", { name = "Salon", min = 15, max = 25, step = 0.5 })
	expose(RemoteThermostat:new(clu, "THE2", "aBiuro"), "climate", { name = "Biuro", path = "hk:biuro", readonly = true })

	VT_TEMP = VirtualNumber:new("t_sypialnia")
	VT = VirtualThermostat:new("sypialnia", { source = VT_TEMP, output = VirtualSwitch:new("klima"),
		direction = "cool", setpoint = 24, holiday = 28 })
	expose(VT, "climate", { name = "Sypialnia", path = "hk:vt" })
	expose(VirtualSwitch:new("lampa"), "switch", { name = "Lampa", path = "hk:lampa" })
`

func newClimateBridge(t *testing.T, mgr *accesscontrol.Manager) (*lua.LuaEngine, *Server) {
	t.Helper()
	engine := lua.NewLuaEngine()
	t.Cleanup(engine.Close)
	exec(t, engine, climateSetup)
	s := &Server{luaExecutor: engine, accessManager: mgr}
	objs, err := s.fetchExposed()
	if err != nil {
		t.Fatal(err)
	}
	s.accessories = s.buildAccessories(objs)
	return engine, s
}

// thermostat gives the characteristics of a Thermostat accessory by type.
type thermostat struct {
	current, target, curMode, tgtMode, units *characteristic.C
}

func thermostatOf(t *testing.T, s *Server, path string) thermostat {
	t.Helper()
	w := s.accessories[path]
	if w == nil {
		t.Fatalf("%s has no accessory", path)
	}
	th := thermostat{
		current: findChar(w.Accessory, characteristic.TypeCurrentTemperature),
		target:  findChar(w.Accessory, characteristic.TypeTargetTemperature),
		curMode: findChar(w.Accessory, characteristic.TypeCurrentHeatingCoolingState),
		tgtMode: findChar(w.Accessory, characteristic.TypeTargetHeatingCoolingState),
		units:   findChar(w.Accessory, characteristic.TypeTemperatureDisplayUnits),
	}
	if th.current == nil || th.target == nil || th.curMode == nil || th.tgtMode == nil || th.units == nil {
		t.Fatalf("%s is not a Thermostat: %+v", path, th)
	}
	return th
}

// shown is current/target/mode as the Home app reads them.
func (th thermostat) shown() [4]any {
	return [4]any{th.current.Value(), th.target.Value(), th.curMode.Value(), th.tgtMode.Value()}
}

// startNotifier delivers state changes from Lua to the accessories as the bridge does.
func startNotifier(t *testing.T, engine *lua.LuaEngine, s *Server) {
	t.Helper()
	engine.SetHomeKitStateHandler(s.NotifyStateChange)
	wake := make(chan struct{}, 1)
	s.wake = wake
	go s.runNotifier(t.Context(), wake)
}

func waitShown(t *testing.T, what string, th thermostat, want [4]any) {
	t.Helper()
	for deadline := time.Now().Add(5 * time.Second); th.shown() != want; time.Sleep(5 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatalf("%s: current/target/mode/target mode = %v, want %v", what, th.shown(), want)
		}
	}
}

const (
	off  = characteristic.TargetHeatingCoolingStateOff
	heat = characteristic.TargetHeatingCoolingStateHeat
	cool = characteristic.TargetHeatingCoolingStateCool
)

func TestThermostatAccessoryAfterReport(t *testing.T) {
	engine, s := newClimateBridge(t, nil)
	startNotifier(t, engine, s)
	th := thermostatOf(t, s, "CLU1.THE1")

	if th.target.MinVal != 15.0 || th.target.MaxVal != 25.0 || th.target.StepVal != 0.5 {
		t.Errorf("target range %v..%v step %v, want the expose options 15..25 step 0.5", th.target.MinVal, th.target.MaxVal, th.target.StepVal)
	}
	for _, c := range []*characteristic.C{th.tgtMode, th.curMode} {
		if !slices.Equal(c.ValidVals, []int{off, heat}) {
			t.Errorf("valid modes %v, want off and heat", c.ValidVals)
		}
	}
	if th.units.Value() != characteristic.TemperatureDisplayUnitsCelsius {
		t.Errorf("display units %v, want Celsius", th.units.Value())
	}

	exec(t, engine, `report(1, 0, 21, 21, 20.4)`)
	waitShown(t, "after a report", th, [4]any{20.4, 21.0, heat, heat})
	exec(t, engine, `report(0, 0, 21, 21, 20.4)`)
	waitShown(t, "stopped", th, [4]any{20.4, 21.0, off, off})
	exec(t, engine, `report(1, 0, 21, 21, 20.9)`)
	waitShown(t, "the temperature", th, [4]any{20.9, 21.0, heat, heat})
}

// A target that changes alone (holiday mode, the schedule) reaches the Home app; the
// temperature is the same, so there is no state change for it.
func TestThermostatTargetAloneUpdatesHome(t *testing.T) {
	engine, s := newClimateBridge(t, nil)
	startNotifier(t, engine, s)
	th := thermostatOf(t, s, "CLU1.THE1")
	exec(t, engine, `report(1, 0, 21, 21, 20.4)`)
	waitShown(t, "after a report", th, [4]any{20.4, 21.0, heat, heat})

	exec(t, engine, `report(1, 1, 21, 17, 20.4)`)
	waitShown(t, "holiday", th, [4]any{20.4, 17.0, heat, heat})
	exec(t, engine, `report(1, 2, 21, 19.5, 20.4)`)
	waitShown(t, "schedule", th, [4]any{20.4, 19.5, heat, heat})

	vt := thermostatOf(t, s, "hk:vt")
	exec(t, engine, `VT_TEMP:setValue(25)`)
	waitShown(t, "virtual thermostat", vt, [4]any{25.0, 24.0, cool, cool})
	exec(t, engine, `VT:setMode(1)`)
	waitShown(t, "virtual thermostat on holiday", vt, [4]any{25.0, 28.0, cool, cool})
	exec(t, engine, `VT:stop()`)
	waitShown(t, "virtual thermostat stopped", vt, [4]any{25.0, 28.0, off, off})

	// kept from HomeKit: nothing is shown for it
	if n := exec(t, engine, `(function()
		local n = { v = 0 }
		__go_homekit_notify = function(path) if path == "hk:kept" then n.v = n.v + 1 end end
		local k = VirtualThermostat:new("kept", { source = VirtualNumber:new("t_kept"), output = VirtualSwitch:new("o_kept") })
		expose(k, "climate", { name = "Kept", path = "hk:kept", homekit = false })
		k:setMode(1)
		return tostring(n.v)
	end)()`); n != "0" {
		t.Errorf("a thermostat kept from HomeKit notified %s times", n)
	}
}

func TestThermostatRangeWithinHomeKit(t *testing.T) {
	for _, c := range []struct {
		in           ExposedClimate
		lo, hi, step float64
	}{
		{ExposedClimate{Min: 5, Max: 35, Step: 0.5}, 5, 35, 0.5},
		{ExposedClimate{Min: 0, Max: 60, Step: 1}, 5, 38, 1},
		{ExposedClimate{Min: 40, Max: 45, Step: 0.5}, 5, 38, 0.5}, // nothing left: HomeKit's own
		{ExposedClimate{Min: 20, Max: 18, Step: 0.5}, 5, 38, 0.5},
		{ExposedClimate{Min: 18, Max: 22, Step: 0}, 18, 22, 0.1},
		{ExposedClimate{Min: 18, Max: 19, Step: 5}, 18, 19, 1},
	} {
		if lo, hi, step := c.in.targetRange(); lo != c.lo || hi != c.hi || step != c.step {
			t.Errorf("%+v: %v..%v step %v, want %v..%v step %v", c.in, lo, hi, step, c.lo, c.hi, c.step)
		}
	}
}

// Only a thermostat (getTarget and setSetpoint) exposed as climate becomes one.
func TestClimateWithoutThermostatIsLeftOut(t *testing.T) {
	engine := lua.NewLuaEngine()
	defer engine.Close()
	exec(t, engine, `expose(VirtualNumber:new("n"), "climate", { name = "N", path = "hk:n" })`)
	s := &Server{luaExecutor: engine}
	objs, err := s.fetchExposed()
	if err != nil || len(objs) != 1 {
		t.Fatalf("exposed %+v %v", objs, err)
	}
	if objs[0].Climate != nil || s.createExposedAccessory(objs[0], false) != nil {
		t.Errorf("a number exposed as climate became a thermostat: %+v", objs[0].Climate)
	}
}

// A thermostat added to a bridge in use gets a new id; the others keep theirs.
func TestThermostatKeepsOtherAccessoryIDs(t *testing.T) {
	dir := t.TempDir()
	engine := lua.NewLuaEngine()
	t.Cleanup(engine.Close)
	exec(t, engine, `expose(VirtualSwitch:new("lampa"), "switch", { name = "Lampa", path = "hk:lampa" })
		expose(VirtualNumber:new("temp"), "temperature", { name = "Temp", path = "hk:temp" })`)
	build := func() (map[string]uint64, string) {
		s := &Server{luaExecutor: engine, aids: loadAIDStore(dir)}
		objs, err := s.fetchExposed()
		if err != nil {
			t.Fatal(err)
		}
		ids := map[string]uint64{}
		for p, w := range s.buildAccessories(objs) {
			ids[p] = w.Accessory.Id
		}
		if err := s.aidStore().save(); err != nil {
			t.Fatal(err)
		}
		return ids, s.signatureOf(objs)
	}
	before, sig := build()

	exec(t, engine, `expose(VirtualThermostat:new("salon", { source = VirtualNumber:new("t"), output = VirtualSwitch:new("o") }),
		"climate", { name = "Salon", path = "hk:th" })`)
	after, sig2 := build()
	if len(after) != 3 || after["hk:th"] == 0 {
		t.Fatalf("accessories after a thermostat was added: %v", after)
	}
	for p, id := range before {
		if after[p] != id {
			t.Errorf("%s: id %d, was %d", p, after[p], id)
		}
		if after["hk:th"] == id {
			t.Errorf("the thermostat took the id of %s", p)
		}
	}
	if sig == sig2 {
		t.Error("adding a thermostat did not change the signature: the bridge would not rebuild")
	}

	// a new range is new metadata for the Home app: the bridge rebuilds
	exec(t, engine, `ExposedObjects:get("hk:th").options.max = 28`)
	if _, sig3 := build(); sig3 == sig2 {
		t.Error("a new target range did not change the signature")
	}
}
