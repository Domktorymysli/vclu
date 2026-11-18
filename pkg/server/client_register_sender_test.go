package server

import (
	"fmt"
	"net"
	"strings"
	"testing"

	"github.com/Domktorymysli/vclu/pkg/commands"
	"github.com/Domktorymysli/vclu/pkg/lua"
)

// A client that registers with address 0 and port 0 gets its reports where it sent the
// request from, as on a CLU: the answer with current values and every report after a change.
func TestClientRegisterZeroAddressReportsToSender(t *testing.T) {
	s, app := setCluIPSetup(t)
	engine := lua.NewLuaEngine()
	t.Cleanup(engine.Close)
	s.luaEngine = engine
	if _, err := engine.Execute(`lamp = { value = 1, get = function(self, i) return self.value end }`); err != nil {
		t.Fatal(err)
	}

	script := func(sc string) {
		req := &commands.LuaScriptRequest{IPAddress: net.IPv4(127, 0, 0, 1), SessionID: 0x2a, Script: sc}
		send(t, s, app, s.projectCipherKey, string(req.AsBytes()), false)
	}

	script("SYSTEM:clientRegister(0,0,77,{{lamp,0}})")
	if got := answer(t, app, s.projectCipherKey); !strings.Contains(got, "clientReport:77:{1}") {
		t.Fatalf("register answered %q, want clientReport:77:{1}", got)
	}
	s.sendPendingClientReports()
	_ = answer(t, app, s.projectCipherKey) // first report after registering

	if _, err := engine.Execute(`lamp.value = 0`); err != nil {
		t.Fatal(err)
	}
	s.sendPendingClientReports()
	if got := answer(t, app, s.projectCipherKey); !strings.HasSuffix(got, ":clientReport:77:{0}") {
		t.Fatalf("after a change the client got %q, want a report with {0}", got)
	}

	script("SYSTEM:clientDestroy(0,0,77)")
	_ = answer(t, app, s.projectCipherKey)
	if _, err := engine.Execute(`lamp.value = 1`); err != nil {
		t.Fatal(err)
	}
	s.sendPendingClientReports()
	if got := answer(t, app, s.projectCipherKey); got != "" {
		t.Fatalf("after clientDestroy(0,0,...) the client still got %q", got)
	}
}

// A client that gives its own address, as Object Manager does, gets reports after a change, not
// only the answer to registering.
func TestClientRegisterGivenAddressGetsReports(t *testing.T) {
	s, om := setCluIPSetup(t)
	engine := lua.NewLuaEngine()
	t.Cleanup(engine.Close)
	s.luaEngine = engine
	port := om.LocalAddr().(*net.UDPAddr).Port
	if _, err := engine.Execute(fmt.Sprintf(`
		lamp = { value = 1, get = function(self, i) return self.value end }
		SYSTEM:clientRegister("127.0.0.1", %d, 78, {{lamp,0}})`, port)); err != nil {
		t.Fatal(err)
	}
	s.sendPendingClientReports()
	_ = answer(t, om, s.projectCipherKey)

	if _, err := engine.Execute(`lamp.value = 0`); err != nil {
		t.Fatal(err)
	}
	s.sendPendingClientReports()
	if got := answer(t, om, s.projectCipherKey); !strings.HasSuffix(got, ":clientReport:78:{0}") {
		t.Fatalf("after a change the client got %q, want a report with {0}", got)
	}
}

// An address given in the call stays as it is.
func TestWithSenderAddressKeepsGivenAddress(t *testing.T) {
	from := &net.UDPAddr{IP: net.IPv4(192, 168, 1, 20), Port: 50123}
	for in, want := range map[string]string{
		`SYSTEM:clientRegister(0,0,5,{{lamp,0}})`:                `SYSTEM:clientRegister("192.168.1.20",50123,5,{{lamp,0}})`,
		`SYSTEM:clientDestroy( 0 , 0 ,5)`:                        `SYSTEM:clientDestroy("192.168.1.20",50123,5)`,
		`SYSTEM:clientRegister("192.168.1.9",1234,5,{{lamp,0}})`: `SYSTEM:clientRegister("192.168.1.9",1234,5,{{lamp,0}})`,
		`SYSTEM:clientRegister(0,0,5,{}) lamp:set(0,0,1)`:        `SYSTEM:clientRegister("192.168.1.20",50123,5,{}) lamp:set(0,0,1)`,
		`lamp:set(0,0,1)`: `lamp:set(0,0,1)`,
	} {
		if got := withSenderAddress(in, from); got != want {
			t.Errorf("%s\n  got  %s\n  want %s", in, got, want)
		}
	}
}
