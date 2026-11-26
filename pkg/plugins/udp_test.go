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

// "udp" in plugin.json: whole ports 1-65535, at most 8, each once, never a CLU port.
func TestManifestUDPBroadcastPorts(t *testing.T) {
	for _, c := range []struct {
		udp, refusal string
	}{
		{`{"broadcastPorts": [7000]}`, ""},
		{`{"broadcastPorts": [7000, 4387]}`, ""},
		{`{}`, ""},
		{`{"broadcastPorts": "7000"}`, "cannot unmarshal"},
		{`{"broadcastPorts": [7000.5]}`, "cannot unmarshal"},
		{`{"broadcastPorts": [0]}`, "not a port"},
		{`{"broadcastPorts": [70000]}`, "not a port"},
		{`{"broadcastPorts": [1234]}`, "CLU port"},
		{`{"broadcastPorts": [69]}`, "CLU port"},
		{`{"broadcastPorts": [7000, 7000]}`, "twice"},
		{`{"broadcastPorts": [1, 2, 3, 4, 5, 6, 7, 8, 9]}`, "at most 8"},
		{`{"ports": [7000]}`, "unknown field"},
		{`[7000]`, "cannot unmarshal"},
	} {
		var m PluginManifest
		err := json.Unmarshal([]byte(`{"id": "gree", "api": 2, "permissions": {"udp": true}, "udp": `+c.udp+`}`), &m)
		if c.refusal == "" && err != nil {
			t.Errorf("%s: %v", c.udp, err)
		}
		if c.refusal != "" && (err == nil || !strings.Contains(err.Error(), c.refusal)) {
			t.Errorf("%s: %v, want %q", c.udp, err, c.refusal)
		}
	}
}

// "udp" goes with the udp permission on API 2, at download and at load.
func TestManifestUDPNeedsThePermission(t *testing.T) {
	for _, c := range []struct {
		manifest string
		ok       bool
	}{
		{`{"id": "gree", "api": 2, "permissions": {"udp": true}, "udp": {"broadcastPorts": [7000]}}`, true},
		{`{"id": "gree", "api": 2, "permissions": {"udp": true}}`, true},
		{`{"id": "gree", "api": 2, "permissions": {"lan": true}, "udp": {"broadcastPorts": [7000]}}`, false},
		{`{"id": "gree", "api": 2, "udp": {"broadcastPorts": [7000]}}`, false},
		{`{"id": "gree", "udp": {"broadcastPorts": [7000]}}`, false}, // API 1 has no udp
	} {
		path := filepath.Join(t.TempDir(), "plugin.json")
		if err := os.WriteFile(path, []byte(c.manifest), 0o600); err != nil {
			t.Fatal(err)
		}
		err := checkDownloadedManifest(path)
		if c.ok != (err == nil) {
			t.Errorf("%s: %v", c.manifest, err)
		}
		if err != nil && !strings.Contains(err.Error(), `"udp": true`) {
			t.Errorf("%s: %v does not say what is missing", c.manifest, err)
		}
	}
}

// udp is granted on install like lan, narrowed by hosts, and listed after lan.
func TestUDPPermission(t *testing.T) {
	p := Permissions{LAN: true, UDP: true, GrentonRaw: true}
	if got := strings.Join(p.Names(), ","); got != "lan,udp,grenton_raw" {
		t.Errorf("names %q", got)
	}
	if got := strings.Join(AllPermissions.Names(), ","); got != "internet,lan,udp,tcp,mqtt,modbus,grenton_raw,tftp,serial" {
		t.Errorf("all %q", got)
	}
	if got := p.Only([]string{"lan"}); got.UDP {
		t.Error("udp kept when not confirmed")
	}
	if got := p.Only([]string{"udp"}); !got.UDP || got.LAN {
		t.Errorf("only udp: %+v", got)
	}
	g := p.Grants()
	if g.UDP == nil || g.GrentonRaw != nil {
		t.Errorf("install grants: %+v", g)
	}
	allowed, pending := p.allowedBy(&config.PluginGrants{LAN: &config.LANGrant{}})
	if allowed.UDP || !pending.UDP {
		t.Errorf("udp not granted: allowed %+v, pending %+v", allowed, pending)
	}
	allowed, pending = p.allowedBy(&config.PluginGrants{UDP: &config.HostsGrant{}})
	if !allowed.UDP || pending.UDP {
		t.Errorf("udp granted: allowed %+v, pending %+v", allowed, pending)
	}
	if err := ValidateGrants(config.PluginGrants{UDP: &config.HostsGrant{Hosts: []string{"gree.local"}}}); err == nil || !strings.Contains(err.Error(), "udp") {
		t.Errorf("a name in udp hosts: %v", err)
	}
}

// The user's udp hosts reach the sandbox; broadcast ports only for a plugin that asks for udp.
func TestUDPLimitsAndBroadcastPorts(t *testing.T) {
	l := permissionsLoader(t, nil, []config.InstalledPlugin{{ID: "@jan/gree", Grants: &config.PluginGrants{
		UDP: &config.HostsGrant{Hosts: []string{"192.168.1.40"}},
	}}})
	m := PluginManifest{API: 2, Permissions: &Permissions{UDP: true}, UDP: &ManifestUDP{BroadcastPorts: []int{7000}}}
	perms, limits, _ := l.effectivePermissions("@jan/gree", m)
	if !perms.UDP {
		t.Errorf("permissions: %+v", perms)
	}
	data, _ := json.Marshal(limits)
	if string(data) != `{"internetHosts":[],"udpHosts":["192.168.1.40"]}` {
		t.Errorf("limits: %s", data)
	}
	if got := m.BroadcastPorts(); !reflect.DeepEqual(got, []int{7000}) {
		t.Errorf("broadcast ports %v", got)
	}
	m.Permissions = &Permissions{LAN: true}
	if got := m.BroadcastPorts(); got != nil {
		t.Errorf("broadcast ports without udp: %v", got)
	}
}
