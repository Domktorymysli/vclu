package plugins

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/Domktorymysli/vclu/pkg/config"
)

func ports(p ...int) *[]int { return &p }

// Installing records the ports plugin.json lists; a port an update adds waits for the user,
// those allowed before keep working, and one plugin.json no longer lists is not reached.
func TestPortsInTheGrant(t *testing.T) {
	asks := func(tcp, udp []int) Permissions {
		return Permissions{UDP: true, TCP: true, BroadcastPorts: udp, TCPPorts: tcp}
	}
	g := asks([]int{23}, []int{7000}).Grants()
	if g.TCP == nil || !reflect.DeepEqual(g.TCP.Ports, ports(23)) || g.UDP == nil || !reflect.DeepEqual(g.UDP.BroadcastPorts, ports(7000)) {
		t.Fatalf("install grants: tcp %+v, udp %+v", g.TCP, g.UDP)
	}

	allowed, pending := asks([]int{23, 6668}, []int{7000, 7001}).allowedBy(g)
	if !reflect.DeepEqual(allowed.TCPPorts, []int{23}) || !reflect.DeepEqual(pending.TCPPorts, []int{6668}) ||
		!reflect.DeepEqual(allowed.BroadcastPorts, []int{7000}) || !reflect.DeepEqual(pending.BroadcastPorts, []int{7001}) {
		t.Errorf("an update adding ports: allowed %+v, pending %+v", allowed, pending)
	}
	if !allowed.TCP || !allowed.UDP || strings.Join(pending.Names(), ",") != "udp,tcp" {
		t.Errorf("the permissions keep working and show what waits: allowed %+v, pending %v", allowed, pending.Names())
	}

	g.TCP.Ports, g.UDP.BroadcastPorts = ports(23, 6668), ports(7000, 7001)
	if _, pending = asks([]int{23, 6668}, []int{7000, 7001}).allowedBy(g); len(pending.Names()) > 0 {
		t.Errorf("allowed: pending %v", pending.Names())
	}
	allowed, _ = asks([]int{6668}, nil).allowedBy(g)
	if !reflect.DeepEqual(allowed.TCPPorts, []int{6668}) || allowed.BroadcastPorts != nil {
		t.Errorf("ports plugin.json no longer lists: %+v", allowed)
	}

	// a grant from before ports were recorded allows what plugin.json lists
	allowed, pending = asks([]int{23}, []int{7000}).allowedBy(&config.PluginGrants{TCP: &config.HostsGrant{}, UDP: &config.HostsGrant{}})
	if !reflect.DeepEqual(allowed.TCPPorts, []int{23}) || !reflect.DeepEqual(allowed.BroadcastPorts, []int{7000}) || len(pending.Names()) > 0 {
		t.Errorf("an old grant: allowed %+v, pending %v", allowed, pending.Names())
	}
	// a new permission shows its ports as waiting
	_, pending = asks([]int{23}, nil).allowedBy(&config.PluginGrants{})
	if !reflect.DeepEqual(pending.TCPPorts, []int{23}) || !pending.TCP {
		t.Errorf("tcp not granted: %+v", pending)
	}
	if got := asks([]int{23}, []int{7000}).Only([]string{"tcp"}); !reflect.DeepEqual(got.TCPPorts, []int{23}) || got.BroadcastPorts != nil {
		t.Errorf("confirmed tcp only: %+v", got)
	}
	for _, bad := range []*[]int{ports(0), ports(70000)} {
		if err := ValidateGrants(config.PluginGrants{TCP: &config.HostsGrant{Ports: bad}}); err == nil {
			t.Errorf("tcp ports %v allowed", *bad)
		}
	}
}

// The sandbox gets the allowed ports; an old grant gets the ports plugin.json lists recorded at
// load, so the next update adding one waits.
func TestPortsReachTheSandboxAndOldGrantsRecordThem(t *testing.T) {
	l := permissionsLoader(t, nil, []config.InstalledPlugin{
		{ID: "@jan/tuya", Grants: &config.PluginGrants{TCP: &config.HostsGrant{Ports: ports(6668)}}},
		{ID: "@jan/gree", Grants: &config.PluginGrants{UDP: &config.HostsGrant{}}},
	})
	tuya := PluginManifest{API: 2, Permissions: &Permissions{TCP: true}, TCP: &ManifestTCP{Ports: []int{6668, 23}}}
	if perms, _, _ := l.effectivePermissions("@jan/tuya", tuya); !reflect.DeepEqual(perms.TCPPorts, []int{6668}) {
		t.Errorf("tuya ports: %v", perms.TCPPorts)
	}

	gree := PluginManifest{API: 2, Permissions: &Permissions{UDP: true}, UDP: &ManifestUDP{BroadcastPorts: []int{7000}}}
	if perms, _, _ := l.effectivePermissions("@jan/gree", gree); !reflect.DeepEqual(perms.BroadcastPorts, []int{7000}) {
		t.Errorf("gree ports from an old grant: %v", perms.BroadcastPorts)
	}
	mc, err := config.ReadMasterConfig(l.serverDir)
	if err != nil {
		t.Fatal(err)
	}
	if g := mc.Plugins.Installed[1].Grants; g.UDP == nil || !reflect.DeepEqual(g.UDP.BroadcastPorts, ports(7000)) {
		t.Fatalf("recorded: %+v", g.UDP)
	}
	gree.UDP.BroadcastPorts = []int{7000, 7001}
	if perms, _, _ := l.effectivePermissions("@jan/gree", gree); !reflect.DeepEqual(perms.BroadcastPorts, []int{7000}) {
		t.Errorf("after an update adding 7001: %v", perms.BroadcastPorts)
	}

	// the panel's view lists what waits
	dir := filepath.Join(l.pluginsDir, "jan--tuya", "1.0.0")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "plugin.json"), []byte(`{"id":"tuya","version":"1.0.0","api":2,"permissions":{"tcp":true},"tcp":{"ports":[6668,23]}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	v := l.PermissionsView(mc.Plugins.Installed[0])
	if !reflect.DeepEqual(v.TCPPorts, []int{6668, 23}) || !reflect.DeepEqual(v.Pending.TCPPorts, []int{23}) || strings.Join(v.Pending.Names(), ",") != "tcp" {
		t.Errorf("view: ports %v, pending %v %v", v.TCPPorts, v.Pending.TCPPorts, v.Pending.Names())
	}
}
