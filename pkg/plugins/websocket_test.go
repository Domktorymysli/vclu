package plugins

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/Domktorymysli/vclu/pkg/config"
)

// "websocket" in plugin.json: whole ports, at most 8, each once, none of another permission.
func TestManifestWebSocketLANPorts(t *testing.T) {
	for _, c := range []struct {
		ws, refusal string
	}{
		{`{"lanPorts": [80, 8123]}`, ""},
		{`{}`, ""},
		{`{"lanPorts": [0]}`, "not a port"},
		{`{"lanPorts": [70000]}`, "not a port"},
		{`{"lanPorts": [1883]}`, "mqtt permission"},
		{`{"lanPorts": [502]}`, "modbus permission"},
		{`{"lanPorts": [1234]}`, "grenton_raw permission"},
		{`{"lanPorts": [80, 80]}`, "twice"},
		{`{"lanPorts": [1, 2, 3, 4, 5, 6, 7, 8, 9]}`, "at most 8"},
		{`{"ports": [80]}`, "unknown field"},
	} {
		var m PluginManifest
		err := json.Unmarshal([]byte(`{"id": "shelly", "api": 2, "permissions": {"lan": true}, "websocket": `+c.ws+`}`), &m)
		if c.refusal == "" && err != nil {
			t.Errorf("%s: %v", c.ws, err)
		}
		if c.refusal != "" && (err == nil || !strings.Contains(err.Error(), c.refusal)) {
			t.Errorf("%s: %v, want %q", c.ws, err, c.refusal)
		}
	}
	for _, c := range []struct {
		manifest string
		ok       bool
	}{
		{`{"id": "shelly", "api": 2, "permissions": {"lan": true}, "websocket": {"lanPorts": [80]}}`, true},
		{`{"id": "shelly", "websocket": {"lanPorts": [80]}}`, true}, // API 1 asks for lan
		{`{"id": "shelly", "api": 2, "permissions": {"internet": ["x.example.com"]}, "websocket": {"lanPorts": [80]}}`, false},
	} {
		path := filepath.Join(t.TempDir(), "plugin.json")
		if err := os.WriteFile(path, []byte(c.manifest), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := checkDownloadedManifest(path); c.ok != (err == nil) {
			t.Errorf("%s: %v", c.manifest, err)
		}
	}
}

// The lan grant records the WebSocket ports like tcp's: a new one waits, the sandbox gets the allowed ones.
func TestWebSocketPortsInTheLANGrant(t *testing.T) {
	m := PluginManifest{API: 2, Permissions: &Permissions{LAN: true}, WebSocket: &ManifestWebSocket{LANPorts: []int{80}}}
	requested := requestedPermissions(m)
	g := requested.Grants()
	if g.LAN == nil || !reflect.DeepEqual(g.LAN.WebSocketPorts, ports(80)) {
		t.Fatalf("install grant: %+v", g.LAN)
	}
	m.WebSocket.LANPorts = []int{80, 8123}
	allowed, pending := requestedPermissions(m).allowedBy(g)
	if !allowed.LAN || !reflect.DeepEqual(allowed.WebSocketPorts, []int{80}) || !reflect.DeepEqual(pending.WebSocketPorts, []int{8123}) ||
		strings.Join(pending.Names(), ",") != "lan" {
		t.Errorf("an update adding 8123: allowed %+v, pending %+v", allowed, pending)
	}
	if got := (Permissions{Internet: InternetHosts{"x.example.com"}}).Only([]string{"internet"}); got.WebSocketPorts != nil {
		t.Errorf("without lan: %+v", got)
	}
	if err := ValidateGrants(config.PluginGrants{LAN: &config.LANGrant{WebSocketPorts: ports(0)}}); err == nil {
		t.Error("port 0 allowed")
	}

	l := permissionsLoader(t, nil, []config.InstalledPlugin{{ID: "@jan/shelly", Grants: &config.PluginGrants{LAN: &config.LANGrant{}}}})
	if perms, _, _, _ := l.effectivePermissions("@jan/shelly", m); !reflect.DeepEqual(perms.WebSocketPorts, []int{80, 8123}) {
		t.Errorf("an old grant: %v", perms.WebSocketPorts)
	}
	mc, err := config.ReadMasterConfig(l.serverDir)
	if err != nil {
		t.Fatal(err)
	}
	if lan := mc.Plugins.Installed[0].Grants.LAN; lan == nil || !reflect.DeepEqual(lan.WebSocketPorts, ports(80, 8123)) {
		t.Errorf("recorded: %+v", lan)
	}
}
