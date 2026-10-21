package lua

import (
	"strings"
	"testing"
)

// A CLU whose only Sync objects are polled has no session; the renew tick must not take
// that for a lost subscription and run a full autoSubscribe (blocking sends) every TTL/2.
func TestRemoteFirmwareRenewTickSkipsPolledOnly(t *testing.T) {
	engine := newFirmwareEngine(t)
	got := mustExec(t, engine, `
		RemoteSubscription.config.whitelist = { ["CLU220000592.THE0855"] = true }
		RemoteSubscription.autoSubscribe(); runTimeouts()
		RemoteSubscription.startRenewTimer()
		local renew = INTERVALS[#INTERVALS]
		local before = logged("Starting auto%-subscribe")
		BLOCKING = {}
		renew(); renew()
		return (logged("Starting auto%-subscribe") - before) .. "#" .. #BLOCKING`)
	if got != "0#0" {
		t.Errorf("full re-subscribes#blocking sends on renew ticks = %s, want 0#0", got)
	}
}

// A CLU with subscribable objects but no session (offline at start) is still retried by
// the renew tick.
func TestRemoteFirmwareRenewTickRetriesOfflineSubscribable(t *testing.T) {
	engine := newFirmwareEngine(t)
	got := mustExec(t, engine, `
		RemoteSubscription.config.whitelist = { ["CLU220000592.DOU0001"] = true }
		OFFLINE.on = true
		RemoteSubscription.autoSubscribe()
		RemoteSubscription.startRenewTimer()
		local renew = INTERVALS[#INTERVALS]
		OFFLINE.on = false
		renew()
		return tostring(next(RemoteSubscription.activeSubscriptions))`)
	if got != "192.168.0.5:2" {
		t.Errorf("session after the renew tick = %s, want 192.168.0.5:2", got)
	}
}

// An offline CLU costs one blocking get(17) per Lua state, not one per autoSubscribe;
// later the poll chain asks without blocking.
func TestRemoteFirmwareBlockingGet17OncePerCLU(t *testing.T) {
	engine := newFirmwareEngine(t)
	got := mustExec(t, engine, `
		FW.version = nil
		RemoteSubscription.config.whitelist = { ["CLU220000592.THE0855"] = true }
		RemoteSubscription.autoSubscribe(); runTimeouts()
		RemoteSubscription.autoSubscribe(); runTimeouts()
		RemoteSubscription.autoSubscribe(); runTimeouts()
		local n = 0
		for _, b in ipairs(BLOCKING) do if b:match("get%(17%)") then n = n + 1 end end
		return n .. "#" .. sentLike("^CLU220000592:get%(17%)")`)
	parts := strings.SplitN(got, "#", 2)
	if parts[0] != "1" {
		t.Errorf("blocking get(17) = %s, want 1", parts[0])
	}
	if strings.Count(parts[1], "get(17)") < 2 {
		t.Errorf("poll did not ask get(17) without blocking: %s", parts[1])
	}
}

// The blocking get(17) phase stays inside autoSubscribe's 30 s budget: with CLUs that
// take 6 s each to time out, the ones past the budget are left to the poll chain.
func TestRemoteFirmwareGet17WithinBudget(t *testing.T) {
	engine := newFirmwareEngine(t)
	got := mustExec(t, engine, `
		CLOCK = { t = 1000 }
		local realTime = os.time
		os.time = function(t) if t then return realTime(t) end return CLOCK.t end
		local send = __go_send_command
		__go_send_command = function(ip, script, delay)
			if script:match(":get%(17%)$") then
				BLOCKING[#BLOCKING + 1] = ip .. " " .. script
				CLOCK.t = CLOCK.t + 6
				return nil, "timeout"
			end
			return send(ip, script, delay)
		end
		local wl = {}
		for i = 1, 8 do
			local clu = CLU_REMOTE:new(i, "192.168.1." .. i, EventBus:getShared())
			local path = "CLU" .. i .. ".THE000" .. i
			registerObject(path, RemoteThermostat:new(clu, "THE000" .. i))
			wl[path] = true
		end
		RemoteSubscription.config.whitelist = wl
		RemoteSubscription.autoSubscribe()
		os.time = realTime
		local n = 0
		for _, b in ipairs(BLOCKING) do if b:match("get%(17%)") then n = n + 1 end end
		return tostring(n)`)
	if got != "6" {
		t.Errorf("blocking get(17) within 30 s = %s, want 6 of 8", got)
	}
}

// A polled CLU that reports 5.x gets only its own objects subscribed: no clientDestroy,
// no full autoSubscribe, the other sessions stay.
func TestRemoteFirmwareUpgradeSubscribesOnlyThatCLU(t *testing.T) {
	engine := newFirmwareEngine(t)
	got := mustExec(t, engine, `
		CLU220000592._omFirmware = { type = "00000003", version = "000001f6" }
		FW.version = nil
		RemoteSubscription.autoSubscribe(); runTimeouts()
		local full = logged("Starting auto%-subscribe")
		SENT = {}
		FW.version = "05.15.12-2607"
		RemoteSubscription.pollAll(); runTimeouts()
		local keys = {}
		for k in pairs(RemoteSubscription.activeSubscriptions) do keys[#keys + 1] = k end
		table.sort(keys)
		return (logged("Starting auto%-subscribe") - full) .. "#" .. sentLike("clientDestroy") .. "#" ..
			registered() .. "#" .. table.concat(keys, ",") .. "#" .. tostring(next(RemoteSubscription.polled))`)
	parts := strings.Split(got, "#")
	if len(parts) != 5 {
		t.Fatalf("parts = %d in %q", len(parts), got)
	}
	if parts[0] != "0" || parts[1] != "" {
		t.Errorf("full re-subscribes = %s, destroys = %q; want 0 and none", parts[0], parts[1])
	}
	if !strings.Contains(parts[2], "{THE0855, 14}") || strings.Contains(parts[2], "DOU0001") {
		t.Errorf("registered on upgrade:\n%s", parts[2])
	}
	if parts[3] != "192.168.0.5:1,192.168.0.5:2" {
		t.Errorf("sessions = %s, want the switch's and the thermostat's", parts[3])
	}
	if parts[4] != "nil" {
		t.Errorf("still polled: %s", parts[4])
	}
}
