package lua

import (
	"strings"
	"testing"
)

// A CLU that answers get(17), but not with a version (fakeclu answers nil), is up: its
// thermostat is polled every run, and the log says the answer was not a version (#130).
func TestRemoteFirmwareNotVersionAnswerPolls(t *testing.T) {
	engine := newFirmwareEngine(t)
	got := mustExec(t, engine, `
		CLU220000592._omFirmware = { type = "00000003", version = "000001f6" }
		FW.raw = "nil"
		RemoteSubscription.autoSubscribe()
		local reg = registered()
		local logLine = logged("firmware unknown, answer to get%(17%) is not a version %(om%.lua 3/1f6%): THERMOSTAT v2, poll$") ..
			"/" .. logged("no answer to get%(17%)")
		SENT = {}
		runTimeouts()
		local first = table.concat(SENT, ",")
		local values = tostring(TH:getValue()) .. "/" .. tostring(TH:getTarget())
		SENT = {}
		RemoteSubscription.pollAll(); runTimeouts()
		return reg .. "#" .. logLine .. "#" .. first .. "#" .. values .. "#" .. table.concat(SENT, ",")`)
	parts := strings.Split(got, "#")
	if len(parts) != 5 {
		t.Fatalf("parts = %d in %q", len(parts), got)
	}
	if strings.Contains(parts[0], "THE0855") {
		t.Errorf("thermostat registered with a non-version answer:\n%s", parts[0])
	}
	if parts[1] != "1/0" {
		t.Errorf("not-a-version/no-answer log lines = %s, want 1/0", parts[1])
	}
	reads := "CLU220000592:get(17),THE0855:get(6),THE0855:get(8),THE0855:get(3),THE0855:get(12),THE0855:get(14)"
	if parts[2] != reads {
		t.Errorf("first poll sent %s\nwant %s", parts[2], reads)
	}
	if parts[3] != "20.4/21.5" {
		t.Errorf("current/target = %s, want 20.4/21.5", parts[3])
	}
	if parts[4] != reads {
		t.Errorf("second poll sent %s\nwant %s", parts[4], reads)
	}
}

// A CLU that does not answer get(17) is still skipped for the run, and the log says so;
// once it answers with a non-version, the reads go out and the log line changes.
func TestRemoteFirmwareNoAnswerSkipsThenNotVersionPolls(t *testing.T) {
	engine := newFirmwareEngine(t)
	got := mustExec(t, engine, `
		FW.version = nil
		RemoteSubscription.autoSubscribe()
		SENT = {}
		runTimeouts()
		local skipped = table.concat(SENT, ",") .. "/" .. tostring(TH:getValue())
		local noAnswer = logged("firmware unknown, no answer to get%(17%) %(om%.lua 4/198%): THERMOSTAT v2, poll$")
		FW.raw = "nil"
		SENT = {}
		RemoteSubscription.pollAll(); runTimeouts()
		return skipped .. "#" .. noAnswer .. "#" .. table.concat(SENT, ",") .. "#" ..
			logged("firmware unknown, answer to get%(17%) is not a version %(om%.lua 4/198%): THERMOSTAT v2, poll$")`)
	want := "CLU220000592:get(17)/nil#1#" +
		"CLU220000592:get(17),THE0855:get(6),THE0855:get(8),THE0855:get(3),THE0855:get(12),THE0855:get(14)#1"
	if got != want {
		t.Errorf("got  %s\nwant %s", got, want)
	}
}
