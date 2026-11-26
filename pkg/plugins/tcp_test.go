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

// "tcp" in plugin.json: whole ports 1-65535, at most 8, each once, none of another permission.
func TestManifestTCPPorts(t *testing.T) {
	for _, c := range []struct {
		tcp, refusal string
	}{
		{`{"ports": [23]}`, ""},
		{`{"ports": [6668, 23]}`, ""},
		{`{"ports": "23"}`, "cannot unmarshal"},
		{`{"ports": [23.5]}`, "cannot unmarshal"},
		{`{"ports": [0]}`, "not a port"},
		{`{"ports": [70000]}`, "not a port"},
		{`{"ports": [1234]}`, "port 1234 belongs to the grenton_raw permission"},
		{`{"ports": [69]}`, "port 69 belongs to the tftp permission"},
		{`{"ports": [502]}`, "port 502 belongs to the modbus permission"},
		{`{"ports": [1883]}`, "port 1883 belongs to the mqtt permission"},
		{`{"ports": [8883]}`, "port 8883 belongs to the mqtt permission"},
		{`{"ports": [23, 23]}`, "twice"},
		{`{"ports": [1, 2, 3, 4, 5, 6, 7, 8, 9]}`, "at most 8"},
		{`{"broadcastPorts": [23]}`, "unknown field"},
	} {
		var m PluginManifest
		err := json.Unmarshal([]byte(`{"id": "tv", "api": 2, "permissions": {"tcp": true}, "tcp": `+c.tcp+`}`), &m)
		if c.refusal == "" && err != nil {
			t.Errorf("%s: %v", c.tcp, err)
		}
		if c.refusal != "" && (err == nil || !strings.Contains(err.Error(), c.refusal)) {
			t.Errorf("%s: %v, want %q", c.tcp, err, c.refusal)
		}
	}
}

// "tcp" goes with the tcp permission on API 2, and the permission with ports, at download
// and at load.
func TestManifestTCPNeedsThePermissionAndPorts(t *testing.T) {
	for _, c := range []struct {
		manifest, missing string
	}{
		{`{"id": "tv", "api": 2, "permissions": {"tcp": true}, "tcp": {"ports": [23]}}`, ""},
		{`{"id": "tv", "api": 2, "permissions": {"tcp": true}}`, `"tcp": {"ports"`},
		{`{"id": "tv", "api": 2, "permissions": {"tcp": true}, "tcp": {}}`, `"tcp": {"ports"`},
		{`{"id": "tv", "api": 2, "permissions": {"lan": true}, "tcp": {"ports": [23]}}`, `"tcp": true`},
		{`{"id": "tv", "tcp": {"ports": [23]}}`, `"tcp": true`}, // API 1 has no tcp
	} {
		path := filepath.Join(t.TempDir(), "plugin.json")
		if err := os.WriteFile(path, []byte(c.manifest), 0o600); err != nil {
			t.Fatal(err)
		}
		err := checkDownloadedManifest(path)
		if c.missing == "" && err != nil {
			t.Errorf("%s: %v", c.manifest, err)
		}
		if c.missing != "" && (err == nil || !strings.Contains(err.Error(), c.missing)) {
			t.Errorf("%s: %v, want %q", c.manifest, err, c.missing)
		}
	}
}

// tcp is granted on install like lan and udp, narrowed by hosts, and listed after udp.
func TestTCPPermission(t *testing.T) {
	p := Permissions{LAN: true, UDP: true, TCP: true, GrentonRaw: true}
	if got := strings.Join(p.Names(), ","); got != "lan,udp,tcp,grenton_raw" {
		t.Errorf("names %q", got)
	}
	if got := p.Only([]string{"lan"}); got.TCP {
		t.Error("tcp kept when not confirmed")
	}
	if got := p.Only([]string{"tcp"}); !got.TCP || got.LAN || got.UDP {
		t.Errorf("only tcp: %+v", got)
	}
	if g := p.Grants(); g.TCP == nil || g.GrentonRaw != nil {
		t.Errorf("install grants: %+v", g)
	}
	allowed, pending := p.allowedBy(&config.PluginGrants{LAN: &config.LANGrant{}})
	if allowed.TCP || !pending.TCP {
		t.Errorf("tcp not granted: allowed %+v, pending %+v", allowed, pending)
	}
	allowed, pending = p.allowedBy(&config.PluginGrants{TCP: &config.HostsGrant{}})
	if !allowed.TCP || pending.TCP {
		t.Errorf("tcp granted: allowed %+v, pending %+v", allowed, pending)
	}
	if err := ValidateGrants(config.PluginGrants{TCP: &config.HostsGrant{Hosts: []string{"tv.local"}}}); err == nil || !strings.Contains(err.Error(), "tcp") {
		t.Errorf("a name in tcp hosts: %v", err)
	}
}

// The user's tcp hosts reach the sandbox; ports only for a plugin that asks for tcp.
func TestTCPLimitsAndPorts(t *testing.T) {
	l := permissionsLoader(t, nil, []config.InstalledPlugin{{ID: "@jan/tv", Grants: &config.PluginGrants{
		TCP: &config.HostsGrant{Hosts: []string{"192.168.1.41"}},
	}}})
	m := PluginManifest{API: 2, Permissions: &Permissions{TCP: true}, TCP: &ManifestTCP{Ports: []int{23}}}
	perms, limits, _ := l.effectivePermissions("@jan/tv", m)
	if !perms.TCP {
		t.Errorf("permissions: %+v", perms)
	}
	data, _ := json.Marshal(limits)
	if string(data) != `{"internetHosts":[],"tcpHosts":["192.168.1.41"]}` {
		t.Errorf("limits: %s", data)
	}
	if got := m.TCPPorts(); !reflect.DeepEqual(got, []int{23}) {
		t.Errorf("ports %v", got)
	}
	m.Permissions = &Permissions{LAN: true}
	if got := m.TCPPorts(); got != nil {
		t.Errorf("ports without tcp: %v", got)
	}
}
