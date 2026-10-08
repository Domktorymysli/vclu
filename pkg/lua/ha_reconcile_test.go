package lua

import (
	"testing"
)

// TestHADiscoveryReconcile verifies that HAStatePublisher.reconcile() clears
// stale retained discovery for every registry object we do NOT currently expose,
// while leaving exposed objects' discovery untouched.
//
// Regression guard for the bug where a restart left the full registry exposed in
// HA: orphaned discovery configs (e.g. Hidden DIN/AnalogIN) persisted on the
// broker forever because nothing ever cleaned them up.
func TestHADiscoveryReconcile(t *testing.T) {
	engine := NewLuaEngine()
	defer engine.Close()

	setup := `
		_testPub = {}
		_fakeMqtt = {
			publish = function(self, topic, payload, qos, retain)
				table.insert(_testPub, { topic = topic, payload = payload, retain = retain })
			end,
		}

		local function mkObj(t)
			return {
				_type = t,
				_lastState = 0,
				get = function(self, idx) return self._lastState end,
				set = function(self, idx, v) self._lastState = v end,
				getType = function(self) return self._type end,
				onChange = function(self, cb) return function() end end,
			}
		end

		-- One object we expose, two we do not (a DIN and an AnalogIN — the
		-- classic orphans that used to stick around in HA).
		registerObject("CLU1.DOU0001", mkObj("DOUT"))
		registerObject("CLU1.DIN0001", mkObj("DIN"))
		registerObject("CLU1.ANA0001", mkObj("AnalogIN"))

		HAStatePublisher.enabled = true
		HAStatePublisher.mqttInstance = _fakeMqtt

		-- Expose only the DOUT. (No HA setup, so this does not itself publish.)
		expose(_:get("CLU1.DOU0001"), "switch", { name = "Lampa" })

		HAStatePublisher.reconcile()

		_componentCount = #(require("TypeMap").getHAComponents())
	`
	if _, err := engine.Execute(setup); err != nil {
		t.Fatalf("setup failed: %v", err)
	}

	// Every publish must be an empty retained payload (a delete).
	allDeletes := `(function()
		for _, p in ipairs(_testPub) do
			if p.payload ~= "" or p.retain ~= true then
				return "bad:"..tostring(p.topic).."="..tostring(p.payload).."/"..tostring(p.retain)
			end
		end
		return "OK"
	end)()`
	if got := mustExec(t, engine, allDeletes); got != "OK" {
		t.Fatalf("non-delete publish found: %s", got)
	}

	// helper to count topics containing a substring
	if _, err := engine.Execute(`function _testPub_count(sub) local n=0 for _,p in ipairs(_testPub) do if p.topic:find(sub,1,true) then n=n+1 end end return sub, n end`); err != nil {
		t.Fatalf("helper define failed: %v", err)
	}

	// Exposed object: its CURRENT component (switch) must NOT be cleared, but its
	// other (stale) components may be. Non-exposed objects must be cleared on every
	// HA component.
	checks := map[string]string{
		// the exposed switch's kept config must not be deleted
		`(function() for _, p in ipairs(_testPub) do if p.topic == "homeassistant/switch/vclu_clu1_dou0001/config" then return "CLEARED_KEPT" end end return "OK" end)()`: "OK",
		// the DIN was cleared on every component
		`tostring(select(2, _testPub_count("din0001")) == _componentCount)`: "true",
		// the AnalogIN was cleared on every component
		`tostring(select(2, _testPub_count("ana0001")) == _componentCount)`: "true",
	}
	for script, want := range checks {
		if got := mustExec(t, engine, script); got != want {
			t.Errorf("check %q = %q, want %q", script, got, want)
		}
	}
}

func mustExec(t *testing.T, engine *LuaEngine, script string) string {
	t.Helper()
	res, err := engine.Execute(script)
	if err != nil {
		t.Fatalf("exec %q failed: %v", script, err)
	}
	return res
}
