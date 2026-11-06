package lua

import (
	"encoding/json"
	"fmt"
	"testing"
)

// What the HomeKit bridge reads of a thermostat exposed as climate (#62): the target's range
// and step from the expose options, the mode and the target as Home Assistant shows them.

type homekitClimate struct {
	Min, Max, Step  float64
	Active, Mode    string
	Current, Target *float64
}

func homekitClimateOf(t *testing.T, engine *LuaEngine, path string) *homekitClimate {
	t.Helper()
	var objs []struct {
		Path    string          `json:"path"`
		Climate *homekitClimate `json:"climate"`
	}
	if err := json.Unmarshal([]byte(mustExec(t, engine, `ExposedObjects:toJSON({ homekit = true })`)), &objs); err != nil {
		t.Fatal(err)
	}
	for _, o := range objs {
		if o.Path == path {
			return o.Climate
		}
	}
	t.Fatalf("%s not in the exposed list", path)
	return nil
}

func fmtClimate(c *homekitClimate) string {
	if c == nil {
		return "none"
	}
	n := func(v *float64) any {
		if v == nil {
			return "nil"
		}
		return *v
	}
	b, _ := json.Marshal([]any{c.Min, c.Max, c.Step, c.Active, c.Mode, n(c.Current), n(c.Target)})
	return string(b)
}

func TestHomeKitExposedListHasClimate(t *testing.T) {
	engine := NewLuaEngine()
	defer engine.Close()
	if _, err := engine.Execute(haClimateSetup); err != nil {
		t.Fatalf("setup failed: %v", err)
	}
	mustExec(t, engine, `(function()
		expose(TH, "climate", { name = "Salon", min = "16", max = 24, step = 0.5 })
		local vt = VirtualThermostat:new("sypialnia", { source = VirtualNumber:new("t"), output = VirtualSwitch:new("o"),
			direction = "cool", setpoint = 24 })
		expose(vt, "climate", { name = "Sypialnia", path = "t:vt" })
		expose(VirtualNumber:new("n"), "climate", { name = "N", path = "t:n" })
	end)()`)

	for _, c := range []struct{ what, path, script, want string }{
		{"before a report", "CLU1.THE1", ``, `[16,24,0.5,"heat","","nil","nil"]`},
		{"set point mode", "CLU1.THE1", `report(1, 0, 21, 21, 20.4)`, `[16,24,0.5,"heat","heat",20.4,21]`},
		{"holiday", "CLU1.THE1", `report(1, 1, 21, 17, 20.4)`, `[16,24,0.5,"heat","heat",20.4,17]`},
		{"stopped", "CLU1.THE1", `report(0, 1, 21, 17, 20.4)`, `[16,24,0.5,"heat","off",20.4,17]`},
		{"virtual, cooling", "t:vt", ``, `[5,35,0.5,"cool","cool","nil",24]`},
		{"not a thermostat", "t:n", ``, `none`},
	} {
		if c.script != "" {
			mustExec(t, engine, c.script)
		}
		if got := fmtClimate(homekitClimateOf(t, engine, c.path)); got != c.want {
			t.Errorf("%s: climate %s, want %s", c.what, got, c.want)
		}
	}
}

// Home Assistant and HomeKit show the same target and mode once the CLU has reported; in set
// point mode HomeKit shows the set point already, before TargetTemp comes back.
func TestHomeKitClimateAgreesWithHA(t *testing.T) {
	engine := NewLuaEngine()
	defer engine.Close()
	if _, err := engine.Execute(haClimateSetup); err != nil {
		t.Fatalf("setup failed: %v", err)
	}
	mustExec(t, engine, `expose(TH, "climate", { name = "Salon" })`)
	for _, r := range []string{`report(1, 0, 21, 21, 20.4)`, `report(1, 1, 21, 17, 20.4)`, `report(0, 2, 21, 19, 20.4)`} {
		mustExec(t, engine, r)
		c := homekitClimateOf(t, engine, "CLU1.THE1")
		ha := mustExec(t, engine, `_last(BASE .. "/target_temp") .. "/" .. _last(BASE .. "/mode")`)
		if c == nil || c.Target == nil {
			t.Fatalf("%s: no target for HomeKit", r)
		}
		if hk := fmt.Sprintf("%g/%s", *c.Target, c.Mode); hk != ha {
			t.Errorf("%s: HomeKit %s, Home Assistant %s", r, hk, ha)
		}
	}

	mustExec(t, engine, `report(1, 0, 21, 21, 20.4); ExposedObjects:get("CLU1.THE1"):setValue(22)`)
	if c := homekitClimateOf(t, engine, "CLU1.THE1"); c.Target == nil || *c.Target != 22 {
		t.Errorf("set point mode after a write: HomeKit target %v, want the set point 22", fmtClimate(c))
	}
}
