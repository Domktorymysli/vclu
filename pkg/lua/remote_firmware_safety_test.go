package lua

import (
	"strings"
	"testing"
)

// Only an answer shaped like a firmware version (major.minor.patch) counts: anything else
// (a number that happens to have a dot, an error text) leaves the firmware unknown, so a
// thermostat on that CLU stays polled and is never registered.
func TestRemoteFirmwareOnlyVersionAnswersCount(t *testing.T) {
	cases := map[string]string{
		"04.08.05":      "04.08.05",
		"05.15.12-2607": "05.15.12-2607",
		`"05.15.12"`:    "05.15.12",
		"21.5":          "nil",
		"1.0":           "nil",
		"4":             "nil",
		"":              "nil",
		"nil":           "nil",
		"5.1.x":         "nil",
	}
	engine := newFirmwareEngine(t)
	for answer, want := range cases {
		got := mustExec(t, engine, `return tostring(CLU220000592:_setFirmware(`+luaQuote(answer)+`))`)
		if got != want {
			t.Errorf("_setFirmware(%q) = %s, want %s", answer, got, want)
		}
	}
}

// A reply to an async command is matched by its id alone, and ids start again at 1 in a
// fresh Lua state: a poll's answer from before a reload can land on the firmware question
// of the new state. The value read ("21.5") must not be taken as firmware 21.x, which
// would register the thermostat on a 4.x CLU.
func TestRemoteFirmwareStrayReplyKeepsThermostatPolled(t *testing.T) {
	engine := newFirmwareEngine(t)
	got := mustExec(t, engine, `
		FW.version = nil
		RemoteSubscription.autoSubscribe()
		-- the poll asks get(17) asynchronously; the CLU's answer has not come yet
		local ids = {}
		local answerAsync = __go_send_command_async
		__go_send_command_async = function(ip, script, id)
			if #ids > 0 then return answerAsync(ip, script, id) end
			SENT[#SENT + 1] = script
			ids[#ids + 1] = id
			return true
		end
		SENT = {}
		RemoteSubscription.pollAll(); runTimeouts()
		-- a stray answer (a temperature) arrives under that id
		__vclu_command_reply(ids[1], "21.5", nil)
		runTimeouts()
		return tostring(CLU220000592:firmware()) .. "#" .. registered()`)
	parts := strings.SplitN(got, "#", 2)
	if parts[0] != "nil" {
		t.Errorf("firmware after a stray reply = %s, want nil", parts[0])
	}
	if strings.Contains(parts[1], "THE0855") {
		t.Errorf("thermostat registered after a stray reply:\n%s", parts[1])
	}
}

// The id of an async request differs between Lua states, so an answer that arrives after a
// reload (Go hands it to the new state's __vclu_command_reply) matches nothing.
func TestRemoteAsyncIDsDifferAcrossLuaStates(t *testing.T) {
	firstID := func() string {
		return mustExec(t, newFirmwareEngine(t), `
			local got
			__go_send_command_async = function(ip, script, id) got = id; return true end
			CLU220000592:executeAsync("CLU220000592:get(17)", function() end)
			return got`)
	}
	a, b := firstID(), firstID()
	if a == b {
		t.Errorf("first async id is %q in both Lua states", a)
	}
}

func luaQuote(s string) string {
	return `"` + strings.ReplaceAll(strings.ReplaceAll(s, `\`, `\\`), `"`, `\"`) + `"`
}
