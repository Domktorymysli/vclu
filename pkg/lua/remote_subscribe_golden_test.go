package lua

import (
	"os"
	"strings"
	"testing"
)

// Switches, dimmers, covers and LEDs on two CLUs: subscribe,
// renew, a failed renew, an offline CLU, no session left, destroyAll. Every message (blocking
// B, fire-and-forget N, with its spacing), timeout (T) and interval (I) in order.
const mixedSubscribeScenario = `
SENT = {}
FAIL = { sid = nil, ip = nil }
local function rec(kind, ip, script, delay) SENT[#SENT+1] = kind .. "|" .. ip .. "|" .. tostring(delay) .. "|" .. script end
__go_send_command = function(ip, script, delay)
	rec("B", ip, script, delay)
	if script:match(":get%(17%)$") then if FAIL.ip == ip then return nil, "timeout" end return '"05.15.12-2607"', nil end
	local sid = script:match("clientRegister%('.-', %d+, (%d+),")
	if sid then
		if FAIL.ip == ip then return nil, "timeout" end
		return "clientReport:" .. sid .. ":{0}", nil
	end
	return "ok", nil
end
__go_send_command_noreply = function(ip, script)
	rec("N", ip, script, nil)
	if FAIL.renew and script:match("clientRegister") and ip == FAIL.renew then return false, "boom" end
	return true, nil
end
__go_send_command_async = nil
__vclu_ip = "192.168.0.100"
_timeouts = {}
EventBus:getShared().setTimeout = function(self, fn, ms)
	table.insert(_timeouts, fn); SENT[#SENT+1] = "T|" .. tostring(ms); return #_timeouts
end
INTERVALS = {}
EventBus:getShared().setInterval = function(self, fn, ms) INTERVALS[#INTERVALS+1] = fn; SENT[#SENT+1] = "I|" .. ms; return #INTERVALS end
EventBus:getShared().clearInterval = function(self, id) INTERVALS[id] = function() end end
function runTimeouts() while #_timeouts > 0 do table.remove(_timeouts, 1)() end end

A = CLU_REMOTE:new(220000592, "192.168.0.5", EventBus:getShared())
B = CLU_REMOTE:new(220000777, "192.168.0.6", EventBus:getShared())
local wl = {}
local function add(clu, cname, cls, id) local o = cls:new(clu, id, id); registerObject(cname .. "." .. id, o); wl[cname .. "." .. id] = true end
for i = 1, 30 do add(A, "CLU220000592", RemoteSwitch, string.format("DOU%04d", i)) end
for i = 1, 12 do add(A, "CLU220000592", RemoteLight, string.format("DIM%04d", i)) end
for i = 1, 10 do add(A, "CLU220000592", RemoteCover, string.format("ROL%04d", i)) end
for i = 1, 3 do add(A, "CLU220000592", RemoteRGBLight, string.format("LED%04d", i)) end
for i = 1, 9 do add(B, "CLU220000777", RemoteSwitch, string.format("DOU%04d", i)) end
RemoteSubscription.config.enabled = true
RemoteSubscription.config.whitelist = wl
RemoteSubscription.config.batchSize = 4
RemoteSubscription.config.batchDelay = 50
RemoteSubscription.activeSubscriptions = {}
RemoteSubscription.autoSubscribe(); runTimeouts()
SENT[#SENT+1] = "--renew"
RemoteSubscription.refreshAll(); runTimeouts()
SENT[#SENT+1] = "--startRenew"
RemoteSubscription.startRenewTimer()
SENT[#SENT+1] = "--renewfail"
FAIL.renew = "192.168.0.6"
INTERVALS[#INTERVALS](); runTimeouts()
FAIL.renew = nil
SENT[#SENT+1] = "--offline B"
FAIL.ip = "192.168.0.6"
RemoteSubscription.autoSubscribe(); runTimeouts()
SENT[#SENT+1] = "--all offline"
FAIL.ip = "192.168.0.5"
RemoteSubscription.autoSubscribe(); runTimeouts()
FAIL.ip = nil
SENT[#SENT+1] = "--tick no sessions"
INTERVALS[#INTERVALS](); runTimeouts()
SENT[#SENT+1] = "--destroyAll"
RemoteSubscription.destroyAll()
return table.concat(SENT, "\n")
`

// Without a thermostat nothing of #67 shows: the sequence is byte for byte the one of
// main at 31e73b2 (the golden file was recorded from it, after #109 dropped the 64-entry cap).
func TestRemoteSubscribeMixedSameAsBefore67(t *testing.T) {
	want, err := os.ReadFile("pkg/lua/testdata/remote_subscribe_31e73b2.golden")
	if err != nil {
		t.Fatal(err)
	}
	engine := NewLuaEngine()
	t.Cleanup(engine.Close)
	got, err := engine.Execute(mixedSubscribeScenario)
	if err != nil {
		t.Fatal(err)
	}
	g, w := strings.Split(got, "\n"), strings.Split(string(want), "\n")
	for i := 0; i < len(g) || i < len(w); i++ {
		var gl, wl string
		if i < len(g) {
			gl = g[i]
		}
		if i < len(w) {
			wl = w[i]
		}
		if gl != wl {
			t.Fatalf("line %d:\n got %s\nwant %s", i+1, gl, wl)
		}
	}
}
