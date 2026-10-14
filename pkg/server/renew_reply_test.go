package server

import (
	"net"
	"strings"
	"testing"
	"time"

	"github.com/Domktorymysli/vclu/pkg/cipher"
	"github.com/Domktorymysli/vclu/pkg/commands"
	"github.com/Domktorymysli/vclu/pkg/lua"
)

// fakeCLU answers each command on a loopback UDP port with answer(script), the way a CLU
// answers on its command port. Returns the port.
func fakeCLU(t *testing.T, key *cipher.CipherKey, answer func(script string) string) int {
	t.Helper()
	conn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	go func() {
		buf := make([]byte, BufferSize)
		for {
			n, from, err := conn.ReadFromUDP(buf)
			if err != nil {
				return
			}
			plain, err := key.Decrypt(buf[:n])
			if err != nil {
				continue
			}
			req, err := commands.ParseLuaScriptRequest(plain)
			if err != nil {
				continue
			}
			out, err := key.Encrypt([]byte(answer(strings.TrimSpace(req.Script))))
			if err != nil {
				continue
			}
			_, _ = conn.WriteToUDP(out, from)
		}
	}()
	return conn.LocalAddr().(*net.UDPAddr).Port
}

// renewSetup wires a Lua engine to a fake CLU that answers a renewal of session 16 with {0} and
// clientDestroy with a report of {0} too. The light CLU9.DOU1 is on (1) in vCLU.
func renewSetup(t *testing.T) *lua.LuaEngine {
	t.Helper()
	key := cipher.NewCipherKey([]byte("0123456789abcdef"), []byte("fedcba9876543210"))
	port := fakeCLU(t, key, func(script string) string {
		return "resp:127.0.0.1:00000000:clientReport:16:{0}"
	})

	engine := lua.NewLuaEngine()
	t.Cleanup(engine.Close)
	s := &Server{localIP: net.IPv4(127, 0, 0, 1), projectCipherKey: key, luaEngine: engine, cluPort: port}
	engine.SetCommandNoReplySender(s.SendCommandNoReply)

	if _, err := engine.Execute(`
		_ev = {}
		StateBus:getShared():on("state_changed", function(e) table.insert(_ev, e) end)
		light = RemoteSwitch:new({ address = "127.0.0.1", executeNoReply = function() end }, "DOU1")
		registerObject("CLU9.DOU1", light)
		RemoteSubscription.activeSubscriptions["127.0.0.1:16"] = { cluSerial = "CLU9", objects = { { objId = "DOU1", attrId = 0 } } }
		RemoteSubscription.updateRegistry("127.0.0.1", 16, { 1 })
		_ev = {}
	`); err != nil {
		t.Fatal(err)
	}
	return engine
}

// stateChanges lists the state changes since setup as "path old->new origin actor".
func stateChanges(t *testing.T, engine *lua.LuaEngine) string {
	t.Helper()
	ev, err := engine.Execute(`(function()
		local out = {}
		for _, e in ipairs(_ev) do out[#out + 1] = e.path .. " " .. tostring(e.old) .. "->" .. tostring(e.value) .. " " .. e.origin .. " " .. e.actor end
		return table.concat(out, ";")
	end)()`)
	if err != nil {
		t.Fatal(err)
	}
	return ev
}

// waitAnswered waits until the renewal answer was handled (renewMarks cleared) or the read timed out.
func waitAnswered(t *testing.T, engine *lua.LuaEngine) {
	t.Helper()
	for deadline := time.Now().Add(3 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		if got, _ := engine.Execute(`tostring(RemoteSubscription.activeSubscriptions["127.0.0.1:16"].renewMarks == nil)`); got == "true" {
			return
		}
	}
	t.Fatal("the renewal answer was not handled within 3 s")
}

// Contract: the CLU answers a subscription renewal (clientRegister) with the session's current
// values. vCLU applies that answer like a report, so a state whose report was lost is corrected
// by the next renewal instead of staying wrong.
func TestRenewalAnswerCorrectsLostReport(t *testing.T) {
	engine := renewSetup(t)

	// The report that the light went off was lost; the renewal's answer says {0}.
	if _, err := engine.Execute(`assert(RemoteSubscription.refresh("127.0.0.1", 16))`); err != nil {
		t.Fatal(err)
	}
	waitAnswered(t, engine)

	if got, _ := engine.Execute(`tostring(light:getValue())`); got != "0" {
		t.Fatalf("state after renewal answer = %s, want 0 (the answer was not applied)", got)
	}
	if ev := stateChanges(t, engine); ev != "CLU9.DOU1 1->0 grenton device:CLU9" {
		t.Errorf("state changes = %q, want one change 1->0 from the CLU", ev)
	}
}

// The answer is older than anything published after the renewal was sent: a light switched on
// meanwhile stays on, with no flicker in events or history.
func TestRenewalAnswerKeepsNewerLocalWrite(t *testing.T) {
	engine := renewSetup(t)
	if _, err := engine.Execute(`
		light:updateState(0)
		_ev = {}
		assert(RemoteSubscription.refresh("127.0.0.1", 16))
		light:switchOn()
	`); err != nil {
		t.Fatal(err)
	}
	waitAnswered(t, engine)

	if got, _ := engine.Execute(`tostring(light:getValue())`); got != "1" {
		t.Fatalf("state = %s, want 1 (an older renewal answer reverted a newer write)", got)
	}
	if ev := stateChanges(t, engine); strings.Count(ev, "CLU9.DOU1") != 1 || !strings.Contains(ev, "0->1") {
		t.Errorf("state changes = %q, want only the local 0->1", ev)
	}
}

// Only a renewal's answer is applied: a clientReport answering any other command is not read.
func TestOnlyRenewalAnswersAreApplied(t *testing.T) {
	engine := renewSetup(t)
	if _, err := engine.Execute(`
		assert(__go_send_command_noreply("127.0.0.1", "SYSTEM:clientDestroy('127.0.0.1', 4344, 16)"))
		RemoteSubscription.activeSubscriptions["127.0.0.1:16"].renewMarks = { 0 }
	`); err != nil {
		t.Fatal(err)
	}
	time.Sleep(300 * time.Millisecond)
	if got, _ := engine.Execute(`tostring(light:getValue())`); got != "1" {
		t.Fatalf("state = %s, want 1 (an answer to clientDestroy was applied)", got)
	}
	// Without a pending renewal, an answer for the session is ignored too.
	if _, err := engine.Execute(`
		RemoteSubscription.activeSubscriptions["127.0.0.1:16"].renewMarks = nil
		RemoteSubscription.applyRenewAnswer("127.0.0.1", 16, { 0 })
	`); err != nil {
		t.Fatal(err)
	}
	if got, _ := engine.Execute(`tostring(light:getValue())`); got != "1" {
		t.Fatalf("state = %s, want 1 (an answer with no renewal pending was applied)", got)
	}
}

func TestParseClientReportMessage(t *testing.T) {
	ip, sid, values, ok := parseClientReportMessage("resp:192.168.0.3:00000000:clientReport:16:{1,0,0,0}\r\n")
	if !ok || ip != "192.168.0.3" || sid != 16 || values != "{1,0,0,0}" {
		t.Errorf("report: got %q %d %q %v", ip, sid, values, ok)
	}
	for _, msg := range []string{
		"resp:192.168.0.3:00ab12cd:nil",
		"resp:192.168.0.3:00ab12cd:true",
		"resp:192.168.0.3:00000000:clientReport:x:{1}",
		"req:192.168.0.3:00000000:clientReport:16:{1}",
		"",
	} {
		if _, _, _, ok := parseClientReportMessage(msg); ok {
			t.Errorf("%q parsed as a clientReport", msg)
		}
	}
}
