package lua

import (
	"testing"
)

// TestAccessControlProfileResolution verifies that AccessControl.get() (and thus
// isHidden/isReadonly/canControl) resolves access through the profiles assigned
// to an integration - not only the legacy _access map.
//
// Regression guard: the web Access UI stores per-object access as profiles
// (e.g. a "custom-mqtt" profile with per-object overrides). Before this fix get()
// ignored profiles entirely, so "Hidden" settings had no effect and
// exposeRegistry()/HA discovery exposed objects the user had hidden.
func TestAccessControlProfileResolution(t *testing.T) {
	engine := NewLuaEngine()
	defer engine.Close()

	setup := `
		AccessControl.load({})  -- no legacy per-object access
		AccessControl.loadProfiles(
			{
				["full-access"]  = { ["default"] = "full" },
				["custom-mqtt"]  = { ["default"] = "full", objects = {
					["CLU1.DIN0001"] = "hidden",
					["CLU1.DOU0002"] = "readonly",
				}},
			},
			{ mqtt = {"custom-mqtt"}, homekit = {"full-access"} }
		)
	`
	if _, err := engine.Execute(setup); err != nil {
		t.Fatalf("setup failed: %v", err)
	}

	cases := []struct {
		script string
		want   string
	}{
		// mqtt: DIN overridden hidden -> hidden
		{`tostring(AccessControl.isHidden("mqtt", "CLU1.DIN0001"))`, "true"},
		// mqtt: DOUT overridden readonly -> readonly (visible, not controllable)
		{`tostring(AccessControl.isReadonly("mqtt", "CLU1.DOU0002"))`, "true"},
		{`tostring(AccessControl.canControl("mqtt", "CLU1.DOU0002"))`, "false"},
		// mqtt: object with no override -> profile default full -> visible+controllable
		{`tostring(AccessControl.isHidden("mqtt", "CLU1.DOU0099"))`, "false"},
		{`tostring(AccessControl.canControl("mqtt", "CLU1.DOU0099"))`, "true"},
		// homekit: full-access profile -> everything full, including the DIN
		{`AccessControl.get("homekit", "CLU1.DIN0001")`, "full"},
		{`tostring(AccessControl.isHidden("homekit", "CLU1.DIN0001"))`, "false"},
	}
	for _, c := range cases {
		got, err := engine.Execute(c.script)
		if err != nil {
			t.Fatalf("exec %q failed: %v", c.script, err)
		}
		if got != c.want {
			t.Errorf("%s = %q, want %q", c.script, got, c.want)
		}
	}

	// Back-compat: legacy explicit _access must still win over profiles.
	if _, err := engine.Execute(`AccessControl.set("mqtt", "CLU1.DIN0001", "readonly")`); err != nil {
		t.Fatalf("set failed: %v", err)
	}
	if got, _ := engine.Execute(`AccessControl.get("mqtt", "CLU1.DIN0001")`); got != "readonly" {
		t.Errorf("legacy override: get = %q, want readonly", got)
	}
}

// TestExposeRegistryRespectsAccessHidden guards the core of the original bug:
// exposeRegistry() must NOT auto-expose an object that AccessControl marks hidden
// for both integrations, while still exposing non-hidden objects.
func TestExposeRegistryRespectsAccessHidden(t *testing.T) {
	engine := NewLuaEngine()
	defer engine.Close()

	script := `
		local function mk()
			return {
				_type = "DOUT", _lastState = 0,
				get = function(s) return 0 end,
				set = function() end,
				onChange = function(s, cb) return function() end end,
			}
		end
		registerObject("CLU.DOU0001", mk())   -- will be hidden
		registerObject("CLU.DOU0002", mk())   -- stays visible

		AccessControl.set("mqtt",    "CLU.DOU0001", "hidden")
		AccessControl.set("homekit", "CLU.DOU0001", "hidden")

		exposeRegistry()
	`
	if _, err := engine.Execute(script); err != nil {
		t.Fatalf("setup failed: %v", err)
	}

	if got, _ := engine.Execute(`tostring(ExposedObjects:get("CLU.DOU0001") == nil)`); got != "true" {
		t.Error("exposeRegistry() exposed an object hidden for both integrations")
	}
	if got, _ := engine.Execute(`tostring(ExposedObjects:get("CLU.DOU0002") ~= nil)`); got != "true" {
		t.Error("exposeRegistry() failed to expose a non-hidden object")
	}
}
