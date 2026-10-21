package lua

import (
	"strings"
	"testing"
)

// A CLU (CLU_REMOTE) that answers get(17) with FW.version (or an error when nil), a
// thermostat and a switch on the Sync list. Every message sent is in SENT, in order; async
// gets are answered at once from VALUES, the poll's spacing goes through stubbed timeouts.
const firmwareHarness = `
SENT = {}
FW = { version = "04.08.05", fail = false }
VALUES = { [3] = "21", [6] = "1", [8] = "0", [12] = "21.5", [14] = "20.4" }
POLLFAIL = { n = 0 }
LOG = {}
local function answer(script)
	if script:match("^CLU220000592:get%(17%)$") then
		if FW.version == nil then return nil, "timeout" end
		return '"' .. FW.version .. '"', nil
	end
	local f = script:match("^THE0855:get%((%d+)%)$")
	if f then
		if POLLFAIL.n > 0 then POLLFAIL.n = POLLFAIL.n - 1; return nil, "timeout" end
		return VALUES[tonumber(f)], nil
	end
	local sid = script:match("clientRegister%('.-', %d+, (%d+),")
	if sid then return "clientReport:" .. sid .. ":{0}", nil end
	return "ok", nil
end
__go_send_command = function(ip, script, delay)
	SENT[#SENT + 1] = script
	return answer(script)
end
__go_send_command_noreply = function(ip, script)
	SENT[#SENT + 1] = script
	return true, nil
end
__go_send_command_async = function(ip, script, id)
	SENT[#SENT + 1] = script
	local result, err = answer(script)
	__vclu_command_reply(id, result or "", err)
	return true
end
__vclu_ip = "192.168.0.100"

local logger = Logger:getShared()
local info = logger.info
logger.info = function(self, fmt, ...)
	local ok, line = pcall(string.format, tostring(fmt), ...)
	LOG[#LOG + 1] = ok and line or tostring(fmt)
	return info(self, fmt, ...)
end

_timeouts, _timeoutDelays = {}, {}
EventBus:getShared().setTimeout = function(self, fn, ms)
	table.insert(_timeouts, fn)
	table.insert(_timeoutDelays, ms)
	return #_timeouts
end
EventBus:getShared().setInterval = function() return 1 end
function runTimeouts()
	while #_timeouts > 0 do table.remove(_timeouts, 1)() end
end

CLU220000592 = CLU_REMOTE:new(220000592, "192.168.0.5", EventBus:getShared())
CLU220000592._omFirmware = { type = "00000004", version = "00000198" }
TH = RemoteThermostat:new(CLU220000592, "THE0855", "aSalon")
SW = RemoteSwitch:new(CLU220000592, "DOU0001", "lamp")
registerObject("CLU220000592.THE0855", TH)
registerObject("CLU220000592.DOU0001", SW)
EV = { settings = 0, changes = {} }
TH:onEvent("OnSettingsChange", function() EV.settings = EV.settings + 1 end)
StateBus:getShared():on("state_changed", function(e)
	if e.path == "CLU220000592.THE0855" then
		EV.changes[#EV.changes + 1] = tostring(e.value) .. "/" .. tostring(e.origin) .. "/" .. tostring(e.actor)
	end
end)

RemoteSubscription.config.enabled = true
RemoteSubscription.config.whitelist = { ["CLU220000592.THE0855"] = true, ["CLU220000592.DOU0001"] = true }
RemoteSubscription.config.batchSize = 8
RemoteSubscription.config.batchDelay = 50
RemoteSubscription.activeSubscriptions = {}
RemoteSubscription.polled = {}

function registered()
	local out = {}
	for _, s in ipairs(SENT) do
		if s:match("clientRegister") then out[#out + 1] = s end
	end
	return table.concat(out, "\n")
end
function sentLike(pattern)
	local out = {}
	for _, s in ipairs(SENT) do
		if s:match(pattern) then out[#out + 1] = s end
	end
	return table.concat(out, ",")
end
function logged(pattern)
	local n = 0
	for _, l in ipairs(LOG) do if l:match(pattern) then n = n + 1 end end
	return n
end
`

func newFirmwareEngine(t *testing.T) *LuaEngine {
	t.Helper()
	engine := NewLuaEngine()
	t.Cleanup(engine.Close)
	if _, err := engine.Execute(firmwareHarness); err != nil {
		t.Fatalf("harness: %v", err)
	}
	return engine
}

// A remote object can tell its CLU's firmware: the CLU answers get(17) when its objects
// are subscribed; nil before that.
func TestRemoteFirmwareFromGet17(t *testing.T) {
	engine := newFirmwareEngine(t)
	got := mustExec(t, engine, `
		local before = tostring(TH._clu:firmware())
		FW.version = "05.15.12-2607"
		RemoteSubscription.autoSubscribe()
		return before .. "|" .. tostring(TH._clu:firmware()) .. "|" .. sentLike("^CLU220000592:get")`)
	if got != "nil|05.15.12-2607|CLU220000592:get(17)" {
		t.Errorf("firmware before|after|asked = %q", got)
	}
	// Asked once: a second subscribe keeps the answer
	got = mustExec(t, engine, `SENT = {}; RemoteSubscription.autoSubscribe(); return sentLike("^CLU220000592:get")`)
	if got != "" {
		t.Errorf("second autoSubscribe asked again: %q", got)
	}
}

// 5.x: the thermostat is subscribed with its v2 features as before, nothing is polled,
// and one log line names the CLU, its firmware and the plan.
func TestRemoteFirmware5xSubscribesV2(t *testing.T) {
	engine := newFirmwareEngine(t)
	got := mustExec(t, engine, `
		CLU220000592._omFirmware = { type = "00000003", version = "000001f6" }
		FW.version = "05.15.12-2607"
		RemoteSubscription.autoSubscribe()
		RemoteSubscription.autoSubscribe()
		return registered() .. "#" .. tostring(next(RemoteSubscription.polled)) .. "#" ..
			logged("^%[RemoteSub%] CLU CLU220000592 firmware 05%.15%.12%-2607 %(om%.lua 3/1f6%): THERMOSTAT v2, subscribe$")`)
	parts := strings.Split(got, "#")
	for _, f := range []string{"{THE0855, 6}", "{THE0855, 8}", "{THE0855, 3}", "{THE0855, 12}", "{THE0855, 14}", "{DOU0001, 0}"} {
		if !strings.Contains(parts[0], f) {
			t.Errorf("registered script missing %s:\n%s", f, parts[0])
		}
	}
	if parts[1] != "nil" {
		t.Errorf("polled = %s, want none", parts[1])
	}
	if parts[2] != "1" {
		t.Errorf("plan logged %s times, want once", parts[2])
	}
}

// 4.x (CLU v1.0): the thermostat is not registered at all, the switch still is; the
// thermostat is polled with get, one message per feature, each spaced by batchDelay.
// The values land as a subscription's would: value, target, settings events, state_changed
// from the CLU.
func TestRemoteFirmware4xPollsThermostat(t *testing.T) {
	engine := newFirmwareEngine(t)
	got := mustExec(t, engine, `
		RemoteSubscription.autoSubscribe()
		local reg = registered()
		SENT = {}
		runTimeouts()
		local delays = {}
		for _, d in ipairs(_timeoutDelays) do delays[#delays + 1] = tostring(d) end
		return reg .. "#" .. sentLike("^THE0855:get") .. "#" .. table.concat(delays, ",") .. "#" ..
			tostring(TH:getValue()) .. "/" .. tostring(TH:getTarget()) .. "/" .. tostring(TH:getSetpoint()) .. "/" ..
			tostring(TH:getState()) .. "/" .. tostring(TH:getMode()) .. "#" .. EV.settings .. "#" ..
			table.concat(EV.changes, ",") .. "#" ..
			logged("^%[RemoteSub%] CLU CLU220000592 firmware 04%.08%.05 %(om%.lua 4/198%): THERMOSTAT v2, poll$")`)
	parts := strings.Split(got, "#")
	if len(parts) != 7 {
		t.Fatalf("parts = %d in %q", len(parts), got)
	}
	if strings.Contains(parts[0], "THE0855") {
		t.Errorf("thermostat registered on 4.x:\n%s", parts[0])
	}
	if !strings.Contains(parts[0], "{DOU0001, 0}") {
		t.Errorf("switch not registered:\n%s", parts[0])
	}
	if parts[1] != "THE0855:get(6),THE0855:get(8),THE0855:get(3),THE0855:get(12),THE0855:get(14)" {
		t.Errorf("polled reads = %s", parts[1])
	}
	// first poll after batchDelay, then one timeout between reads
	if parts[2] != "50,50,50,50,50,50" {
		t.Errorf("poll spacing = %s", parts[2])
	}
	if parts[3] != "20.4/21.5/21/1/0" {
		t.Errorf("value/target/setpoint/state/mode = %s", parts[3])
	}
	if parts[4] != "4" {
		t.Errorf("OnSettingsChange = %s, want 4", parts[4])
	}
	if parts[5] != "20.4/grenton/device:CLU220000592" {
		t.Errorf("state_changed = %s", parts[5])
	}
	if parts[6] != "1" {
		t.Errorf("plan logged %s times, want once", parts[6])
	}

	// A later read reports a change like a subscription would; equal values publish nothing
	got = mustExec(t, engine, `
		EV.changes = {}; EV.settings = 0
		VALUES[14] = "20.9"
		RemoteSubscription.pollAll(); runTimeouts()
		return table.concat(EV.changes, ",") .. "#" .. EV.settings`)
	if got != "20.9/grenton/device:CLU220000592#0" {
		t.Errorf("second poll changes#settings = %q", got)
	}
}

// Unknown firmware (no answer to get(17)) is the safe side: nothing of the thermostat is
// registered, it is polled. The poll asks the firmware first; once a 5.x answer comes,
// the objects are subscribed again, now with the thermostat.
func TestRemoteFirmwareUnknownPollsUntilKnown(t *testing.T) {
	engine := newFirmwareEngine(t)
	got := mustExec(t, engine, `
		CLU220000592._omFirmware = { type = "00000003", version = "000001f6" }
		FW.version = nil
		RemoteSubscription.autoSubscribe()
		local reg = registered()
		local polled = tostring(RemoteSubscription.polled["CLU220000592.THE0855"] ~= nil)
		local unknown = logged("firmware unknown, no answer to get%(17%) %(om%.lua 3/1f6%): THERMOSTAT v2, poll$")
		SENT = {}
		runTimeouts() -- get(17) unanswered: the CLU is skipped this run
		local quiet = table.concat(SENT, ",")
		SENT = {}
		FW.version = "05.15.12-2607"
		RemoteSubscription.pollAll(); runTimeouts()
		return reg .. "#" .. polled .. "#" .. unknown .. "#" .. quiet .. "#" .. registered() .. "#" ..
			tostring(next(RemoteSubscription.polled))`)
	parts := strings.Split(got, "#")
	if len(parts) != 6 {
		t.Fatalf("parts = %d in %q", len(parts), got)
	}
	if strings.Contains(parts[0], "THE0855") {
		t.Errorf("thermostat registered with unknown firmware:\n%s", parts[0])
	}
	if parts[1] != "true" || parts[2] != "1" {
		t.Errorf("polled = %s, unknown logged = %s", parts[1], parts[2])
	}
	if parts[3] != "CLU220000592:get(17)" {
		t.Errorf("poll with unknown firmware sent %q, want only get(17)", parts[3])
	}
	if !strings.Contains(parts[4], "{THE0855, 14}") {
		t.Errorf("thermostat not subscribed after 5.x answered:\n%s", parts[4])
	}
	if parts[5] != "nil" {
		t.Errorf("still polled after subscribe: %s", parts[5])
	}
}

// A CLU that stops answering a poll (a restart) has its firmware forgotten; the next poll
// asks get(17) again before reading.
func TestRemoteFirmwareAskedAgainAfterCLUComesBack(t *testing.T) {
	engine := newFirmwareEngine(t)
	got := mustExec(t, engine, `
		RemoteSubscription.autoSubscribe(); runTimeouts()
		POLLFAIL.n = 1
		SENT = {}
		RemoteSubscription.pollAll(); runTimeouts()
		local failed = table.concat(SENT, ",") .. "/" .. tostring(TH._clu:firmware())
		SENT = {}
		RemoteSubscription.pollAll(); runTimeouts()
		return failed .. "#" .. table.concat(SENT, ",")`)
	want := "THE0855:get(6)/nil#CLU220000592:get(17),THE0855:get(6),THE0855:get(8),THE0855:get(3),THE0855:get(12),THE0855:get(14)"
	if got != want {
		t.Errorf("got  %s\nwant %s", got, want)
	}
}

// om.lua FwType 3 fv197 has Thermostat v1 (no CurrentTemp); on 4.x it is polled without 14.
// A proxy imported before the firmware table marks v1 with _version = 1.
func TestRemoteFirmwareV1Features(t *testing.T) {
	engine := newFirmwareEngine(t)
	got := mustExec(t, engine, `
		CLU220000592._omFirmware = { type = "00000003", version = "00000197" }
		FW.version = "04.07.02"
		RemoteSubscription.autoSubscribe(); SENT = {}; runTimeouts()
		local polledV1 = sentLike("^THE0855:get")
		local legacy = RemoteThermostat:new(CLU_REMOTE:new(1, "192.168.0.6", EventBus:getShared()), "THE1")
		legacy._version = 1
		legacy._clu:_setFirmware("05.01.00")
		local v, follow = legacy:_plan()
		return polledV1 .. "#" .. table.concat(legacy:subscribeFeatures(), ",") .. "/" .. v .. "/" .. follow`)
	if got != "THE0855:get(6),THE0855:get(8),THE0855:get(3),THE0855:get(12)#6,8,3,12/1/subscribe" {
		t.Errorf("got %q", got)
	}
}
