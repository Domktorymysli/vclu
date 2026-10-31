package lua

import (
	"strings"
	"testing"
)

// Contract: a secret in a URL never reaches the log; the HTTP helper logs every URL it calls.
func TestPluginLogRedactsURLSecrets(t *testing.T) {
	engine := NewLuaEngine()
	defer engine.Close()

	for _, c := range []struct{ in, secret, keep string }{
		{"HTTP POST https://api.telegram.org/bot123456789:AAFakeTokenForTestsOnly_0000000000/getUpdates (attempt 1)",
			"AAFakeTokenForTestsOnly_0000000000", "/getUpdates (attempt 1)"},
		{"HTTP GET https://admin:s3cret@192.168.0.132/status.html", "s3cret", "admin:***@192.168.0.132/status.html"},
		{"HTTP GET https://api.example.com/v1?token=abc123&q=1", "abc123", "q=1"},
		// not secrets: clock times, ports, object paths
		{"at 23:49:50 from 192.168.0.50:8081 widgety:termostat", "", "at 23:49:50 from 192.168.0.50:8081 widgety:termostat"},
	} {
		got, err := engine.Execute(`return require("PluginHelpers").redactSecrets(` + luaQuote(c.in) + `)`)
		if err != nil {
			t.Fatal(err)
		}
		if c.secret != "" && strings.Contains(got, c.secret) {
			t.Errorf("secret left in %q", got)
		}
		if !strings.Contains(got, c.keep) {
			t.Errorf("%q lost %q", got, c.keep)
		}
	}
}
