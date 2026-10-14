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

// Contract: the CLU answers a subscription renewal (clientRegister) with the session's current
// values. vCLU applies that answer like a pushed clientReport, so a state whose report was lost
// is corrected by the next renewal instead of staying wrong.
func TestRenewalAnswerCorrectsLostReport(t *testing.T) {
	key := cipher.NewCipherKey([]byte("0123456789abcdef"), []byte("fedcba9876543210"))
	port := fakeCLU(t, key, func(script string) string {
		if strings.HasPrefix(script, "SYSTEM:clientRegister(") {
			return "resp:127.0.0.1:00000000:clientReport:16:{0}"
		}
		return "resp:127.0.0.1:00000000:nil"
	})

	engine := lua.NewLuaEngine()
	defer engine.Close()
	s := &Server{localIP: net.IPv4(127, 0, 0, 1), projectCipherKey: key, luaEngine: engine, cluPort: port}

	// The light was on; the report that it went off was lost.
	if _, err := engine.Execute(`
		_ev = {}
		StateBus:getShared():on("state_changed", function(e) table.insert(_ev, e) end)
		local light = RemoteSwitch:new({ address = "127.0.0.1", executeNoReply = function() end }, "DOU1")
		_registry["CLU9.DOU1"] = light
		RemoteSubscription.activeSubscriptions["127.0.0.1:16"] = { cluSerial = "CLU9", objects = { { objId = "DOU1" } } }
		RemoteSubscription.updateRegistry("127.0.0.1", 16, { 1 })
		_ev = {}
	`); err != nil {
		t.Fatal(err)
	}

	// An answer that is not a clientReport changes nothing.
	if err := s.SendCommandNoReply("127.0.0.1", "SYSTEM:clientDestroy('127.0.0.1', 4344, 99)"); err != nil {
		t.Fatal(err)
	}
	if err := s.SendCommandNoReply("127.0.0.1", "SYSTEM:clientRegister('127.0.0.1', 4344, 16, {{DOU1, 0}})"); err != nil {
		t.Fatal(err)
	}

	var got string
	for deadline := time.Now().Add(3 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		if got, _ = engine.Execute(`tostring(_registry["CLU9.DOU1"]:getValue())`); got == "0" {
			break
		}
	}
	if got != "0" {
		t.Fatalf("state after renewal answer = %s, want 0 (the answer was not applied)", got)
	}
	ev, err := engine.Execute(`(function()
		local out = {}
		for _, e in ipairs(_ev) do out[#out + 1] = e.path .. " " .. tostring(e.old) .. "->" .. tostring(e.value) .. " " .. e.origin .. " " .. e.actor end
		return table.concat(out, ";")
	end)()`)
	if err != nil {
		t.Fatal(err)
	}
	if ev != "CLU9.DOU1 1->0 grenton device:CLU9" {
		t.Errorf("state changes = %q, want one change 1->0 from the CLU", ev)
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
