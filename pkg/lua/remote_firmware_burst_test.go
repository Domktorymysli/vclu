package lua

import (
	"strings"
	"testing"
)

// Three switches in two sessions next to a polled thermostat (4.x CLU), first poll done.
const burstSetup = `
	RemoteSubscription.config.batchSize = 2
	for i = 2, 3 do
		local sw = RemoteSwitch:new(CLU220000592, "DOU000" .. i)
		registerObject("CLU220000592.DOU000" .. i, sw)
		RemoteSubscription.config.whitelist["CLU220000592.DOU000" .. i] = true
	end
	RemoteSubscription.autoSubscribe(); runTimeouts()
	function kinds()
		local out = {}
		for _, s in ipairs(SENT) do
			if s:match("clientRegister") then out[#out + 1] = "R"
			elseif s:match("^THE0855:get") then out[#out + 1] = "G" end
		end
		return table.concat(out)
	end
`

// A poll that comes while a renew chain runs waits for it: no read between two renewals.
func TestRemoteFirmwarePollWaitsForRenew(t *testing.T) {
	engine := newFirmwareEngine(t)
	mustExec(t, engine, burstSetup)
	got := mustExec(t, engine, `
		SENT = {}
		RemoteSubscription.refreshAll()  -- first renewal sent, the second waits for a timeout
		RemoteSubscription.pollAll()
		runTimeouts()
		return kinds()`)
	if got != "RRGGGGG" {
		t.Errorf("sends = %s, want RRGGGGG (renewals, then the reads)", got)
	}
}

// A renew that comes while a poll chain runs waits for it: no renewal between two reads.
func TestRemoteFirmwareRenewWaitsForPoll(t *testing.T) {
	engine := newFirmwareEngine(t)
	mustExec(t, engine, burstSetup)
	got := mustExec(t, engine, `
		SENT = {}
		RemoteSubscription.pollAll()     -- first read sent, the next waits for a timeout
		RemoteSubscription.refreshAll()
		runTimeouts()
		return kinds()`)
	if got != "GGGGGRR" {
		t.Errorf("sends = %s, want GGGGGRR (reads, then the renewals)", got)
	}
}

// The first poll after autoSubscribe does not follow its clientDestroy series at once.
func TestRemoteFirmwareFirstPollWaits(t *testing.T) {
	engine := newFirmwareEngine(t)
	got := mustExec(t, engine, `
		RemoteSubscription.autoSubscribe()
		RemoteSubscription.autoSubscribe() -- this one destroys the first one's sessions
		return tostring(_timeoutDelays[#_timeoutDelays])`)
	if got != "1000" {
		t.Errorf("first poll after %s ms, want 1000", got)
	}
	if !strings.Contains(mustExec(t, engine, `return sentLike("clientDestroy")`), "clientDestroy") {
		t.Error("second autoSubscribe destroyed nothing; the test does not cover the series")
	}
}
