package lua

import "testing"

// A thermostat polled on a 4.x CLU (#67) and exposed as a climate (#61): exposing sends
// nothing to the CLU, each poll that brings new values publishes the current and target
// temperature, and a target from HA reaches the CLU as set(3, v).
func TestRemoteFirmwarePolledClimatePublishes(t *testing.T) {
	engine := newFirmwareEngine(t)
	mustExec(t, engine, `
		_pub = {}
		HAStatePublisher.setup({
			publish = function(self, topic, payload) table.insert(_pub, { topic = topic, payload = payload }); return true end,
			subscribe = function() end, unsubscribe = function() end,
		})
		HACommands.setup({ subscribe = function() end })
		BASE = "vclu/exposed/vclu/climate/clu220000592_the0855"
		function _last(suffix)
			local v
			for _, p in ipairs(_pub) do if p.topic == BASE .. "/" .. suffix then v = p.payload end end
			return tostring(v)
		end`)

	// Synced first: the thermostat is polled, not registered
	got := mustExec(t, engine, `
		RemoteSubscription.autoSubscribe()
		local polled = tostring(RemoteSubscription.polled["CLU220000592.THE0855"] ~= nil)
		runTimeouts()
		return polled .. "|" .. sentLike("THE0855.*clientRegister") .. sentLike("clientRegister.*THE0855")`)
	if got != "true|" {
		t.Fatalf("polled|registered thermostat = %q, want true|", got)
	}

	// Exposing talks to no CLU
	got = mustExec(t, engine, `
		SENT = {}
		expose(TH, "climate", { name = "Salon" })
		HAStatePublisher.refresh()
		return tostring(#SENT) .. "|" .. registered()`)
	if got != "0|" {
		t.Errorf("messages|clientRegister sent by expose = %q, want 0|", got)
	}

	// A poll with new values publishes them
	got = mustExec(t, engine, `
		_pub = {}
		VALUES[12] = "22.5"; VALUES[14] = "21.1"
		RemoteSubscription.pollAll()
		runTimeouts()
		return _last("current_temp") .. "/" .. _last("target_temp") .. "|" .. sentLike("^THE0855:get") .. "|" .. registered()`)
	want := "21.1/22.5|THE0855:get(6),THE0855:get(8),THE0855:get(3),THE0855:get(12),THE0855:get(14)|"
	if got != want {
		t.Errorf("after a poll: current/target|reads|registered\n got: %s\nwant: %s", got, want)
	}

	// A target from HA is a set point on the CLU
	got = mustExec(t, engine, `
		SENT = {}
		local ok, err = HACommands.handleExposedMessage(BASE .. "/target_temp/set", "23")
		return tostring(ok) .. tostring(err and (":" .. err) or "") .. "|" .. table.concat(SENT, ";")`)
	if got != "true|THE0855:set(3, 23)" {
		t.Errorf("target command = %q, want true|THE0855:set(3, 23)", got)
	}
}
