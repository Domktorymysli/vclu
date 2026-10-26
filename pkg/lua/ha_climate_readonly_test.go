package lua

import "testing"

// A read-only climate is shown in Home Assistant and refuses a target on the command path,
// whether read-only comes from expose() or from the dashboard's access level (#61).

// expose(..., { readonly = true }) holds on the handle too, for every integration.
func TestClimateReadonlySetValue(t *testing.T) {
	engine := NewLuaEngine()
	defer engine.Close()
	if _, err := engine.Execute(haClimateSetup); err != nil {
		t.Fatalf("setup failed: %v", err)
	}
	got := mustExec(t, engine, `(function()
		local h = expose(TH, "climate", { name = "Salon", readonly = true })
		return tostring(h:setValue(24)) .. "|" .. #_calls
	end)()`)
	if got != "false|0" {
		t.Errorf("setValue on a read-only climate = %s, want false|0", got)
	}
}

func TestHAClimateReadonly(t *testing.T) {
	for _, c := range []struct{ name, setup, err string }{
		{"expose option", `expose(TH, "climate", { name = "Salon", readonly = true })`, "object is readonly"},
		{"access level", `AccessControl.set("mqtt", "CLU1.THE1", "readonly"); expose(TH, "climate", { name = "Salon" })`,
			"access denied: CLU1.THE1"},
	} {
		t.Run(c.name, func(t *testing.T) {
			engine := NewLuaEngine()
			defer engine.Close()
			if _, err := engine.Execute(haClimateSetup); err != nil {
				t.Fatalf("setup failed: %v", err)
			}
			mustExec(t, engine, c.setup)

			d := climateDiscovery(t, engine)
			if _, ok := d["temperature_command_topic"]; ok {
				t.Errorf("read-only climate announces temperature_command_topic: %v", d)
			}
			if _, ok := d["temperature_state_topic"]; !ok {
				t.Errorf("read-only climate lacks temperature_state_topic: %v", d)
			}

			got := mustExec(t, engine, `(function()
				report(1, 0, 21, 21, 20.4)
				local ok, err = HACommands.handleExposedMessage(BASE .. "/target_temp/set", "24")
				return tostring(ok) .. ":" .. tostring(err) .. "|" .. #_calls .. "|" .. tostring(TH:getSetpoint()) ..
					"|" .. _last(BASE .. "/target_temp")
			end)()`)
			if want := "false:" + c.err + "|0|21|21"; got != want {
				t.Errorf("command on a read-only climate: ok:err|CLU calls|set point|target = %s, want %s", got, want)
			}
		})
	}
}
