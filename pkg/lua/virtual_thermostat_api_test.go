package lua

import "testing"

// The class holds its public API only: the clock, the tick and the release hook are internal.
func TestVirtualThermostat_OnlyPublicAPI(t *testing.T) {
	engine := NewLuaEngine()
	t.Cleanup(engine.Close)
	got := mustExec(t, engine, `(function()
		local public = { __index = true, new = true, set = true, CHANGE_EVENTS = true, getValue = true,
			getTarget = true, getSetpoint = true, setSetpoint = true, getState = true, getMode = true,
			setMode = true, start = true, stop = true, getFault = true, getDirection = true }
		local extra = {}
		for k in pairs(VirtualThermostat) do
			if not public[k] then table.insert(extra, tostring(k)) end
		end
		table.sort(extra)
		return table.concat(extra, ",")
	end)()`)
	if got != "" {
		t.Errorf("VirtualThermostat has internals on the class: %s", got)
	}
}
