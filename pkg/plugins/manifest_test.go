package plugins

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/Domktorymysli/vclu/pkg/config"
)

func manifestWith(perms string) string {
	return `{"id": "x", "name": "X", "version": "1.0.0", "api": 2, "permissions": ` + perms + `}`
}

func readManifest(t *testing.T, raw string) (PluginManifest, error) {
	t.Helper()
	var m PluginManifest
	err := json.Unmarshal([]byte(raw), &m)
	return m, err
}

// Every permission is an object with its parameters; a wide one says why; a key or field this
// vclu does not know refuses the plugin as written for a newer vclu.
func TestManifestPermissions(t *testing.T) {
	for _, c := range []struct {
		perms, refusal string
	}{
		{`{}`, ""},
		{`{"internet": {"hosts": ["api.example.com", "*.supla.org"]}}`, ""},
		{`{"internet": {"hosts": ["*"], "why": "the server the user gives"}}`, ""},
		{`{"internet": {"hosts": ["*"]}}`, `any host ("*") needs "why"`},
		{`{"internet": true}`, `{"hosts": ["api.example.com"]}`},
		{`{"internet": ["api.example.com"]}`, `{"hosts"`},
		{`{"internet": {"hosts": []}}`, "at least one host"},
		{`{"internet": {"hosts": ["http://x.com"]}}`, "not a host"},
		{`{"lan": {"ports": [80, 8123]}}`, ""},
		{`{"lan": {"why": "devices on any port the user gives"}}`, ""},
		{`{"lan": true}`, `or "why" it needs any port`},
		{`{"lan": {}}`, `or "why" it needs any port`},
		{`{"lan": {"ports": []}}`, "at least one port"},
		{`{"lan": {"ports": [1883]}}`, "port 1883 belongs to the mqtt permission"},
		{`{"lan": {"ports": [1234]}}`, "belongs to the clu permission"},
		{`{"lan": {"ports": [1, 2, 3, 4, 5, 6, 7, 8, 9]}}`, "at most 8"},
		{`{"lan": {"ports": [80], "lanPorts": [80]}}`, "permissions.lan.lanPorts"},
		{`{"udp": {"ports": [7000], "broadcast": [7000]}}`, ""},
		{`{"udp": {"ports": [7000]}}`, ""},
		{`{"udp": {"ports": [7000], "broadcast": [7001]}}`, "7001 is not among its ports"},
		{`{"udp": {"ports": [1234]}}`, "belongs to the clu permission"},
		{`{"udp": {"ports": [69]}}`, "belongs to the tftp permission"},
		{`{"udp": true}`, `{"ports": [7000]}`},
		{`{"udp": {"broadcast": [7000]}}`, "at least one port"},
		{`{"tcp": {"ports": [23, 6668]}}`, ""},
		{`{"tcp": {"ports": [502]}}`, "port 502 belongs to the modbus permission"},
		{`{"tcp": {"ports": [23, 23]}}`, "twice"},
		{`{"tcp": {"ports": [0]}}`, "not a port"},
		{`{"tcp": true}`, `{"ports": [23]}`},
		{`{"mqtt": {"subscribe": ["zigbee2mqtt/#"], "publish": ["zigbee2mqtt/+/set"]}}`, ""},
		{`{"mqtt": {"subscribe": ["a/#"]}}`, ""},
		{`{"mqtt": {"why": "any topic the user picks"}}`, ""},
		{`{"mqtt": true}`, `or "why" it needs any topic`},
		{`{"mqtt": {"subscribe": ["a/#/b"]}}`, "not a topic filter"},
		{`{"modbus": true}`, ""},
		{`{"modbus": {"why": "meters"}}`, ""},
		{`{"clu": {"why": "sends the project to the CLU"}}`, ""},
		{`{"clu": true}`, `{"why": "..."}`},
		{`{"tftp": {}}`, `{"why": "..."}`},
		{`{"serial": {"why": "a meter on RS485"}}`, ""},
		{`{"grenton_raw": true}`, "needs a newer vclu"},
		{`{"gpio": true}`, "needs a newer vclu"},
		{`{"webhooks": {"why": "x"}}`, "needs a newer vclu"},
		{`{"tcp": {"ports": [23], "listen": true}}`, "needs a newer vclu"},
		{`{"lan": {"ports": [80], "why": "<b>x</b>"}}`, "without < or >"},
		{`{"lan": {"ports": [80], "why": {"pl": "urządzenia", "en": "devices"}}}`, ""},
		{`{"lan": {"ports": [80], "why": {"de": "x"}}}`, "language"},
		{`{"lan": false}`, ""},
		{`[]`, "permissions: an object"},
	} {
		_, err := readManifest(t, manifestWith(c.perms))
		if c.refusal == "" && err != nil {
			t.Errorf("%s: %v", c.perms, err)
		}
		if c.refusal != "" && (err == nil || !strings.Contains(err.Error(), c.refusal)) {
			t.Errorf("%s: %v, want %q", c.perms, err, c.refusal)
		}
	}
	if _, err := readManifest(t, manifestWith(`{"gpio": true}`)); !errors.Is(err, ErrNewerVclu) {
		t.Errorf("an unknown permission is ErrNewerVclu: %v", err)
	}
}

// What the permissions read as: ports, topics, why; lan without ports is any port.
func TestManifestPermissionsRead(t *testing.T) {
	m, err := readManifest(t, manifestWith(`{"lan": {"why": "x"}, "udp": {"ports": [7000, 7001], "broadcast": [7000]},
		"mqtt": {"subscribe": ["a/#"]}, "internet": {"hosts": ["API.example.com."]}, "clu": {"why": {"pl": "CLU", "en": "CLU"}}}`))
	if err != nil {
		t.Fatal(err)
	}
	p := *m.Permissions
	if !p.LAN || p.LANPorts != nil || !reflect.DeepEqual(p.UDPPorts, []int{7000, 7001}) || !reflect.DeepEqual(p.BroadcastPorts, []int{7000}) {
		t.Errorf("lan/udp: %+v", p)
	}
	if !reflect.DeepEqual(p.MQTTSubscribe, []string{"a/#"}) || p.MQTTPublish == nil || len(p.MQTTPublish) != 0 {
		t.Errorf("mqtt with subscribe only publishes nothing: %v %v", p.MQTTSubscribe, p.MQTTPublish)
	}
	if !reflect.DeepEqual([]string(p.Internet), []string{"api.example.com"}) || !p.CLU || p.Why["clu"].In("en") != "CLU" {
		t.Errorf("internet/clu: %+v", p)
	}
	if got := strings.Join(p.Names(), ","); got != "internet,lan,udp,mqtt,clu" {
		t.Errorf("names %q", got)
	}
	if got := strings.Join(p.Wide(), ","); got != "lan" {
		t.Errorf("wide %q", got)
	}
	if got := strings.Join(p.Warnings(), ","); got != "lan_internet,mqtt_internet,clu_internet" {
		t.Errorf("warnings %q", got)
	}
	// written back as declared, read again the same
	data, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	var again Permissions
	if err := json.Unmarshal(data, &again); err != nil {
		t.Fatalf("%s: %v", data, err)
	}
	if !reflect.DeepEqual(again.Names(), p.Names()) || !reflect.DeepEqual(again.UDPPorts, p.UDPPorts) || again.LANPorts != nil {
		t.Errorf("round trip %s: %+v", data, again)
	}
}

// The top of plugin.json: API 2 needs permissions; minVcluVersion (API 1) points to requires;
// other unknown keys are noted for the log, "x-" ones are the author's; API 1 reads as before.
func TestManifestTopLevel(t *testing.T) {
	if _, err := readManifest(t, `{"id": "x", "api": 2}`); err == nil || !strings.Contains(err.Error(), `needs "permissions"`) {
		t.Errorf("no permissions: %v", err)
	}
	if _, err := readManifest(t, `{"id": "x", "api": 2, "permissions": {}, "minVcluVersion": "1.12.0"}`); err == nil || !strings.Contains(err.Error(), "moved on plugin API 2") {
		t.Errorf("minVcluVersion: %v", err)
	}
	m, err := readManifest(t, `{"id": "x", "api": 2, "permissions": {}, "exports": {}, "x-mine": 1, "colour": "red"}`)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(m.UnknownKeys(), ","); got != "colour" {
		t.Errorf("unknown keys %q", got)
	}
	// plugin API 1: old fields and permissions as they were, nothing refused
	m, err = readManifest(t, `{"id": "x", "name": "X", "permissions": {"lan": true}, "tcp": {"ports": [23]}, "requires": {"vclu": "^1.0", "modules": ["http"]}}`)
	if err != nil || m.Permissions != nil {
		t.Errorf("API 1: %+v %v", m.Permissions, err)
	}
	if got := requestedPermissions(m); !got.LAN || !got.MQTT || !got.Modbus || got.LANPorts != nil {
		t.Errorf("API 1 asks for lan, mqtt and modbus on any port: %+v", got)
	}
	// a name in two languages
	m, err = readManifest(t, `{"id": "x", "api": 2, "permissions": {}, "name": {"pl": "Zakupy", "en": "Shopping"}}`)
	if err != nil || m.Name.In("en") != "Shopping" || m.Name.In("pl") != "Zakupy" {
		t.Errorf("name %+v %v", m.Name, err)
	}
	if _, err = readManifest(t, `{"id": "x", "api": 2, "permissions": {}, "name": "a\nb"}`); err == nil {
		t.Error("a name on two lines")
	}
}

// requires.vclu is ">=X.Y.Z", checked against this vclu; a dev build checks nothing.
func TestManifestRequiresVclu(t *testing.T) {
	defer SetVcluVersion("dev")
	for _, c := range []struct {
		requires, refusal string
	}{
		{`{"vclu": ">=1.13.0"}`, ""},
		{`{"vclu": ">= 1.12.4"}`, ""},
		{`{"vclu": "^1.13"}`, `written ">=X.Y.Z"`},
		{`{"vclu": ">=1.13.0", "modules": ["http"]}`, "needs a newer vclu"},
	} {
		_, err := readManifest(t, `{"id": "x", "api": 2, "permissions": {}, "requires": `+c.requires+`}`)
		if c.refusal == "" && err != nil {
			t.Errorf("%s: %v", c.requires, err)
		}
		if c.refusal != "" && (err == nil || !strings.Contains(err.Error(), c.refusal)) {
			t.Errorf("%s: %v, want %q", c.requires, err, c.refusal)
		}
	}
	m, _ := readManifest(t, `{"id": "x", "api": 2, "permissions": {}, "requires": {"vclu": ">=1.13.0"}}`)
	for v, ok := range map[string]bool{"1.13.0": true, "1.13.0-dev": true, "1.14.2": true, "2.0.0": true, "1.12.9": false, "0.9.0": false, "dev": true} {
		SetVcluVersion(v)
		if err := m.checkRequires(); (err == nil) != ok {
			t.Errorf("vclu %s: %v", v, err)
		} else if err != nil && !errors.Is(err, ErrNewerVclu) {
			t.Errorf("vclu %s: %v is not ErrNewerVclu", v, err)
		}
	}
}

// Actions are declared: name, description, typed params, confirm.
func TestManifestActions(t *testing.T) {
	m, err := readManifest(t, `{"id": "x", "api": 2, "permissions": {}, "actions": [
		{"name": "add", "description": {"pl": "Dodaj", "en": "Add"}, "params": {"item": {"type": "string", "required": true}}},
		{"name": "clear_all", "description": "Clear the list", "confirm": true}]}`)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(m.Actions.Names(), ","); got != "add,clear_all" || !m.Actions[1].Confirm || !m.Actions[0].Params["item"].Required {
		t.Errorf("actions %+v", m.Actions)
	}
	if got := requestedPermissions(m).Actions; !reflect.DeepEqual(got, []string{"add", "clear_all"}) {
		t.Errorf("requested actions %v", got)
	}
	for _, c := range []struct{ actions, refusal string }{
		{`[{"name": "Add", "description": "x"}]`, "lower-case letter"},
		{`[{"name": "add"}]`, "a description is required"},
		{`[{"name": "add", "description": "x"}, {"name": "add", "description": "y"}]`, "twice"},
		{`[{"name": "add", "description": "x", "params": {"n": {"type": "int"}}}]`, `"string", "number" or "boolean"`},
		{`[{"name": "add", "description": "x", "params": {"n": {"type": "number", "min": 1}}}]`, "needs a newer vclu"},
		{`[{"name": "add", "description": "x", "undo": true}]`, "needs a newer vclu"},
	} {
		_, err := readManifest(t, `{"id": "x", "api": 2, "permissions": {}, "actions": `+c.actions+`}`)
		if err == nil || !strings.Contains(err.Error(), c.refusal) {
			t.Errorf("%s: %v, want %q", c.actions, err, c.refusal)
		}
	}
}

// The wizard's form: a field type this vclu does not know refuses the plugin at download and load.
func TestManifestFormFieldTypes(t *testing.T) {
	dir := t.TempDir()
	write := func(name, data string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(data), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("plugin.json", `{"id": "x", "api": 2, "permissions": {}, "form": "form.json"}`)
	write("form.json", `{"settings": {"room": {"type": "colour"}}}`)
	if err := checkDownloadedManifest(filepath.Join(dir, "plugin.json")); !errors.Is(err, ErrNewerVclu) {
		t.Errorf("unknown field type: %v", err)
	}
	write("form.json", `{"settings": {"room": {"type": "host"}}}`)
	if err := checkDownloadedManifest(filepath.Join(dir, "plugin.json")); err != nil {
		t.Errorf("known field type: %v", err)
	}
}

// Install records what plugin.json asks for, never clu, tftp or serial; the names are the
// manifest's.
func TestPermissionNamesAndInstall(t *testing.T) {
	p := Permissions{LAN: true, UDP: true, TCP: true, CLU: true, TFTP: true, Serial: true, UDPPorts: []int{7000}, BroadcastPorts: []int{}, TCPPorts: []int{23}}
	if got := strings.Join(p.Names(), ","); got != "lan,udp,tcp,clu,tftp,serial" {
		t.Errorf("names %q", got)
	}
	if got := strings.Join(AllPermissions.Names(), ","); got != "internet,lan,udp,tcp,mqtt,modbus,clu,tftp,serial" {
		t.Errorf("all %q", got)
	}
	g := AllPermissions.Only([]string{"lan", "clu", "tftp", "serial", "bogus"}).Grants()
	if g.LAN == nil || !g.LAN.AnyPort || g.CLU != nil || g.TFTP != nil || g.Serial != nil || g.Internet != nil || g.TCP != nil {
		t.Errorf("install grants %+v", g)
	}
	g = p.Grants()
	if g.UDP == nil || !reflect.DeepEqual(g.UDP.Ports, ports(7000)) || g.UDP.Broadcast == nil || len(*g.UDP.Broadcast) != 0 ||
		g.TCP == nil || !reflect.DeepEqual(g.TCP.Ports, ports(23)) || g.Actions == nil {
		t.Errorf("ports recorded: udp %+v tcp %+v actions %v", g.UDP, g.TCP, g.Actions)
	}
	if got := strings.Join(p.Risky(), ","); got != "clu,tftp,serial" {
		t.Errorf("risky %q", got)
	}
}

// lan.ports cover HTTP and WebSockets in the house; any port is a grant of its own, a port an
// update adds waits, a list that goes away (any port) waits.
func TestLANPortsInTheGrant(t *testing.T) {
	listed := Permissions{LAN: true, LANPorts: []int{80}}
	g := listed.Grants()
	if g.LAN == nil || g.LAN.AnyPort || !reflect.DeepEqual(g.LAN.Ports, ports(80)) {
		t.Fatalf("install: %+v", g.LAN)
	}
	allowed, pending := Permissions{LAN: true, LANPorts: []int{80, 8123}}.allowedBy(g)
	if !allowed.LAN || !reflect.DeepEqual(allowed.LANPorts, []int{80}) || !reflect.DeepEqual(pending.LANPorts, []int{8123}) || !pending.LAN {
		t.Errorf("an update adding 8123: allowed %+v, pending %+v", allowed, pending)
	}
	allowed, pending = Permissions{LAN: true}.allowedBy(g)
	if !allowed.LAN || !reflect.DeepEqual(allowed.LANPorts, []int{80}) || !pending.LAN || pending.LANPorts != nil {
		t.Errorf("an update asking any port keeps 80, any port waits: allowed %+v, pending %+v", allowed, pending)
	}
	anyPort := Permissions{LAN: true}.Grants()
	if !anyPort.LAN.AnyPort || anyPort.LAN.Ports != nil {
		t.Fatalf("any port: %+v", anyPort.LAN)
	}
	if allowed, pending = (Permissions{LAN: true}).allowedBy(anyPort); allowed.LANPorts != nil || pending.LAN {
		t.Errorf("any port allowed: %+v %+v", allowed, pending)
	}
	if allowed, _ = listed.allowedBy(anyPort); !reflect.DeepEqual(allowed.LANPorts, []int{80}) {
		t.Errorf("narrowed by an update: %v", allowed.LANPorts)
	}
	// a grant from before ports covered HTTP is recorded from plugin.json at load
	l := permissionsLoader(t, nil, []config.InstalledPlugin{{ID: "@jan/shelly", Grants: &config.PluginGrants{LAN: &config.LANGrant{}}}})
	m := PluginManifest{API: 2, Permissions: &Permissions{LAN: true, LANPorts: []int{80, 8123}}}
	perms, limits, _, err := l.effectivePermissions("@jan/shelly", m)
	if err != nil || !reflect.DeepEqual(perms.LANPorts, []int{80, 8123}) || !reflect.DeepEqual(limits.LANPorts, []int{80, 8123}) {
		t.Errorf("an old grant: %v %v %v", perms.LANPorts, limits.LANPorts, err)
	}
	mc, err := config.ReadMasterConfig(l.serverDir)
	if err != nil {
		t.Fatal(err)
	}
	if lan := mc.Plugins.Installed[0].Grants.LAN; lan == nil || !reflect.DeepEqual(lan.Ports, ports(80, 8123)) {
		t.Errorf("recorded: %+v", lan)
	}
	if err := ValidateGrants(config.PluginGrants{LAN: &config.LANGrant{Ports: ports(0)}}); err == nil {
		t.Error("port 0 allowed")
	}
}

// udp: unicast ports and broadcast ports each recorded; one an update adds waits.
func TestUDPPortsInTheGrant(t *testing.T) {
	v1 := Permissions{UDP: true, UDPPorts: []int{7000}, BroadcastPorts: []int{7000}}
	g := v1.Grants()
	allowed, pending := Permissions{UDP: true, UDPPorts: []int{7000, 7001}, BroadcastPorts: []int{7000, 7001}}.allowedBy(g)
	if !reflect.DeepEqual(allowed.UDPPorts, []int{7000}) || !reflect.DeepEqual(pending.UDPPorts, []int{7001}) ||
		!reflect.DeepEqual(allowed.BroadcastPorts, []int{7000}) || !reflect.DeepEqual(pending.BroadcastPorts, []int{7001}) || !pending.UDP {
		t.Errorf("an update adding 7001: allowed %+v, pending %+v", allowed, pending)
	}
	if err := ValidateGrants(config.PluginGrants{UDP: &config.HostsGrant{Hosts: []string{"gree.local"}}}); err == nil || !strings.Contains(err.Error(), "udp") {
		t.Errorf("a name in udp hosts: %v", err)
	}
}

// MQTT topic filters: the user may narrow them; a filter an update adds waits; one the grant
// already covers does not.
func TestMQTTTopicsInTheGrant(t *testing.T) {
	for _, c := range []struct {
		b, a string
		want bool
	}{
		{"#", "a/b", true}, {"a/#", "a", true}, {"a/#", "a/b/c", true}, {"a/+", "a/b", true}, {"a/+", "a/+", true},
		{"a/+", "a/#", false}, {"a/b", "a/+", false}, {"a/b", "a/c", false}, {"a/+/c", "a/b/c", true}, {"a", "a/b", false},
	} {
		if got := filterCovers(c.b, c.a); got != c.want {
			t.Errorf("%s covers %s: %v", c.b, c.a, got)
		}
	}
	v1 := Permissions{MQTT: true, MQTTSubscribe: []string{"z2m/#"}, MQTTPublish: []string{"z2m/+/set"}}
	g := v1.Grants()
	if !reflect.DeepEqual(g.MQTT.Subscribe, []string{"z2m/#"}) {
		t.Fatalf("install: %+v", g.MQTT)
	}
	g.MQTT.Subscribe = []string{"z2m/lamp/#"} // the user narrowed it
	allowed, pending := v1.allowedBy(g)
	if !reflect.DeepEqual(allowed.MQTTSubscribe, []string{"z2m/lamp/#"}) || pending.MQTT {
		t.Errorf("narrowed: allowed %v, pending %+v", allowed.MQTTSubscribe, pending)
	}
	v2 := Permissions{MQTT: true, MQTTSubscribe: []string{"z2m/#", "tele/#"}, MQTTPublish: []string{"z2m/+/set"}}
	allowed, pending = v2.allowedBy(g)
	if !reflect.DeepEqual(allowed.MQTTSubscribe, []string{"z2m/lamp/#"}) || !reflect.DeepEqual(pending.MQTTSubscribe, []string{"tele/#"}) || !pending.MQTT {
		t.Errorf("an update adding tele/#: allowed %v, pending %+v", allowed.MQTTSubscribe, pending)
	}
	// any topic: recorded as "#"
	wide := Permissions{MQTT: true}
	if g := wide.Grants(); !reflect.DeepEqual(g.MQTT.Subscribe, []string{"#"}) || !reflect.DeepEqual(g.MQTT.Publish, []string{"#"}) {
		t.Errorf("any topic: %+v", g.MQTT)
	}
	// a grant never goes past plugin.json
	got := WithinRequest(config.PluginGrants{MQTT: &config.MQTTGrant{Subscribe: []string{"#", "z2m/a/#"}}}, v1)
	if !reflect.DeepEqual(got.MQTT.Subscribe, []string{"z2m/a/#"}) {
		t.Errorf("within the request: %v", got.MQTT.Subscribe)
	}
}

// Declared actions: those the user allowed run, one an update adds waits; a grant from before
// actions were recorded keeps those plugin.json lists.
func TestActionsInTheGrant(t *testing.T) {
	v1 := Permissions{Actions: []string{"add"}}
	g := v1.Grants()
	allowed, pending := Permissions{Actions: []string{"add", "clear_all"}}.allowedBy(g)
	if !reflect.DeepEqual(allowed.Actions, []string{"add"}) || !reflect.DeepEqual(pending.Actions, []string{"clear_all"}) {
		t.Errorf("an update adding clear_all: %v %v", allowed.Actions, pending.Actions)
	}
	l := permissionsLoader(t, nil, []config.InstalledPlugin{{ID: "@jan/list", Grants: &config.PluginGrants{}}})
	m := PluginManifest{API: 2, Permissions: &Permissions{}, Actions: ManifestActions{{Name: "add"}}}
	if perms, _, _, _ := l.effectivePermissions("@jan/list", m); !reflect.DeepEqual(perms.Actions, []string{"add"}) {
		t.Errorf("an old grant: %v", perms.Actions)
	}
	if got := WithinRequest(config.PluginGrants{Actions: &[]string{"add", "drop"}}, Permissions{Actions: []string{"add"}}); !reflect.DeepEqual(*got.Actions, []string{"add"}) {
		t.Errorf("within the request: %v", *got.Actions)
	}
}

// The user's lists reach the sandbox: hosts of every kind, modbus gateways, clu discovery.
func TestLimitsFromTheGrant(t *testing.T) {
	l := permissionsLoader(t, nil, []config.InstalledPlugin{{ID: "@jan/x", Grants: &config.PluginGrants{
		UDP:    &config.HostsGrant{Hosts: []string{"192.168.1.40"}, Ports: ports(7000), Broadcast: ports()},
		Modbus: &config.ModbusGrant{Hosts: []string{"192.168.0.9"}},
		CLU:    &config.CLUGrant{Hosts: []string{"192.168.0.10"}, Discovery: true},
	}}})
	m := PluginManifest{API: 2, Permissions: &Permissions{UDP: true, UDPPorts: []int{7000}, BroadcastPorts: []int{}, Modbus: true, CLU: true}}
	perms, limits, _, err := l.effectivePermissions("@jan/x", m)
	if err != nil || !perms.UDP || !perms.Modbus || !perms.CLU {
		t.Fatalf("permissions: %+v %v", perms, err)
	}
	data, _ := json.Marshal(limits)
	if string(data) != `{"internetHosts":[],"udpHosts":["192.168.1.40"],"modbusHosts":["192.168.0.9"],"cluHosts":["192.168.0.10"],"cluDiscovery":true}` {
		t.Errorf("limits: %s", data)
	}
}

// Installing grants only what the confirmation showed; the rest waits.
func TestSeenAtInstall(t *testing.T) {
	asks := Permissions{TCP: true, LAN: true, UDP: true, MQTT: true, TCPPorts: []int{22, 23}, LANPorts: []int{80, 8123},
		UDPPorts: []int{7000}, BroadcastPorts: []int{7000}, MQTTSubscribe: []string{"a/#", "b/#"}, MQTTPublish: []string{}, Actions: []string{"add", "drop"}}
	seen := Seen{TCPPorts: []int{23}, LANPorts: &[]int{80}, UDPPorts: []int{7000}, BroadcastPorts: []int{7000}, MQTTSubscribe: []string{"a/#"}, MQTTPublish: []string{}, Actions: []string{"add"}}
	g := asks.OnlySeen(seen).Grants()
	allowed, pending := asks.allowedBy(g)
	if !reflect.DeepEqual(allowed.TCPPorts, []int{23}) || !reflect.DeepEqual(pending.TCPPorts, []int{22}) {
		t.Errorf("tcp: allowed %v, pending %v", allowed.TCPPorts, pending.TCPPorts)
	}
	if !reflect.DeepEqual(allowed.LANPorts, []int{80}) || !reflect.DeepEqual(pending.LANPorts, []int{8123}) {
		t.Errorf("lan: allowed %v, pending %v", allowed.LANPorts, pending.LANPorts)
	}
	if !reflect.DeepEqual(allowed.MQTTSubscribe, []string{"a/#"}) || !reflect.DeepEqual(pending.MQTTSubscribe, []string{"b/#"}) {
		t.Errorf("mqtt: allowed %v, pending %v", allowed.MQTTSubscribe, pending.MQTTSubscribe)
	}
	if !reflect.DeepEqual(allowed.Actions, []string{"add"}) || !reflect.DeepEqual(pending.Actions, []string{"drop"}) {
		t.Errorf("actions: allowed %v, pending %v", allowed.Actions, pending.Actions)
	}
	// any port asked, a list shown: only those
	g = Permissions{LAN: true}.OnlySeen(Seen{LANPorts: &[]int{80}}).Grants()
	if g.LAN.AnyPort || !reflect.DeepEqual(g.LAN.Ports, ports(80)) {
		t.Errorf("any port, 80 shown: %+v", g.LAN)
	}
	// nothing shown: the permission granted, its ports all wait
	_, pending = asks.allowedBy(asks.OnlySeen(Seen{}).Grants())
	if !reflect.DeepEqual(pending.TCPPorts, []int{22, 23}) {
		t.Errorf("nothing shown: pending %v", pending.TCPPorts)
	}
}

// Grants without their lists recorded: pinned once to what the files ask for now, and kept
// from then on.
func TestGrantsMigratedOnFirstLoad(t *testing.T) {
	dir := t.TempDir()
	raw := `{"plugins": {"installed": [
		{"id": "@vclu/tasmota", "version": "1.0.0", "enabled": true, "api": 2, "grants": {"lan": {}}},
		{"id": "@vclu/zakupy", "version": "1.0.0", "enabled": true, "api": 2, "grants": {}},
		{"id": "@jan/clu", "version": "1.0.0", "enabled": true, "api": 2, "grants": {"clu": {"hosts": ["192.168.0.10"]}, "udp": {}}}]}}`
	if err := os.WriteFile(filepath.Join(dir, ".vclu.json"), []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	l := NewLoader(dir)
	tasmota := PluginManifest{API: 2, Permissions: &Permissions{LAN: true}}
	zakupy := PluginManifest{API: 2, Permissions: &Permissions{}, Actions: ManifestActions{{Name: "add"}, {Name: "clear_all", Confirm: true}}}
	clu := PluginManifest{API: 2, Permissions: &Permissions{CLU: true, UDP: true, UDPPorts: []int{7000}, BroadcastPorts: []int{7000}}}
	for id, m := range map[string]PluginManifest{"@vclu/tasmota": tasmota, "@vclu/zakupy": zakupy, "@jan/clu": clu} {
		if _, _, _, err := l.effectivePermissions(id, m); err != nil {
			t.Fatalf("%s: %v", id, err)
		}
	}
	mc, err := config.ReadMasterConfig(dir)
	if err != nil {
		t.Fatal(err)
	}
	by := map[string]*config.PluginGrants{}
	for _, p := range mc.Plugins.Installed {
		by[p.ID] = p.Grants
	}
	if g := by["@vclu/tasmota"]; g.LAN == nil || !g.LAN.AnyPort || g.LAN.Ports != nil {
		t.Errorf("tasmota lan %+v", g.LAN)
	}
	if g := by["@vclu/zakupy"]; g.Actions == nil || strings.Join(*g.Actions, ",") != "add,clear_all" {
		t.Errorf("zakupy actions %v", g.Actions)
	}
	if g := by["@jan/clu"]; g.CLU == nil || strings.Join(g.CLU.Hosts, ",") != "192.168.0.10" || g.UDP == nil ||
		!reflect.DeepEqual(g.UDP.Ports, ports(7000)) || !reflect.DeepEqual(g.UDP.Broadcast, ports(7000)) {
		t.Errorf("clu %+v udp %+v", g.CLU, g.UDP)
	}
	// pinned: an update adding an action waits
	zakupy.Actions = append(zakupy.Actions, ManifestAction{Name: "share"})
	if perms, _, _, _ := l.effectivePermissions("@vclu/zakupy", zakupy); strings.Join(perms.Actions, ",") != "add,clear_all" {
		t.Errorf("after an update: %v", perms.Actions)
	}
}
