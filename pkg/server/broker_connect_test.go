package server

import (
	"testing"

	lua "github.com/yuin/gopher-lua"
)

func TestBrokerConnectScriptKeepsCredentials(t *testing.T) {
	L := lua.NewState()
	defer L.Close()
	if err := L.DoString(`
		got = {}
		MQTT = {}
		function MQTT:setHost() return self end
		function MQTT:setPort(p) got.port = p; return self end
		function MQTT:setClientId() return self end
		function MQTT:setCredentials(u, p) got.user = u; got.pass = p; return self end
		function MQTT:connect() return true end
		HAIntegration = { setup = function() end }
	`); err != nil {
		t.Fatal(err)
	}
	user, pass := `ad"min`, "p\\a\"ss\n]]--"
	if err := L.DoString(brokerConnectScript(1883, user, pass)); err != nil {
		t.Fatalf("script: %v", err)
	}
	got := L.GetGlobal("got").(*lua.LTable)
	if u, p := got.RawGetString("user").String(), got.RawGetString("pass").String(); u != user || p != pass {
		t.Errorf("credentials reached Lua as %q / %q, want %q / %q", u, p, user, pass)
	}
	if port := got.RawGetString("port").String(); port != "1883" {
		t.Errorf("port %s", port)
	}
}
