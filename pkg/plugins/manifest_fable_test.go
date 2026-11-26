package plugins

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/Domktorymysli/vclu/pkg/config"
)

// A field spelled in another case ("Ports", "WHY", "Confirm") is not the one this vclu reads:
// the manifest is refused, not read case-insensitively as encoding/json would.
func TestFableManifestFieldCaseExact(t *testing.T) {
	for _, raw := range []string{
		manifestWith(`{"lan": {"Ports": [80]}}`),
		manifestWith(`{"internet": {"Hosts": ["a.example.com"]}}`),
		manifestWith(`{"lan": {"ports": [80], "WHY": "x"}}`),
		manifestWith(`{"udp": {"ports": [7000], "Broadcast": [7000]}}`),
		manifestWith(`{"mqtt": {"Subscribe": ["a/#"]}}`),
		`{"id": "x", "name": "X", "version": "1.0.0", "api": 2, "permissions": {}, "actions": [{"name": "a", "description": "d", "Confirm": true}]}`,
		`{"id": "x", "name": "X", "version": "1.0.0", "api": 2, "permissions": {}, "actions": [{"name": "a", "description": "d", "params": {"p": {"Type": "string"}}}]}`,
		`{"id": "x", "name": "X", "version": "1.0.0", "api": 2, "permissions": {}, "requires": {"Vclu": ">=1.0.0"}}`,
	} {
		if _, err := readManifest(t, raw); err == nil {
			t.Errorf("%s: accepted", raw)
		}
	}
}

// A key written twice is a malformed manifest, not "the last one wins".
func TestFableManifestDuplicateKeysRefused(t *testing.T) {
	for _, raw := range []string{
		manifestWith(`{"lan": {"ports": [80]}, "lan": {"why": "any port"}}`),
		manifestWith(`{"lan": {"ports": [80], "ports": [443]}}`),
		manifestWith(`{"internet": {"hosts": ["a.example.com"], "hosts": ["*"], "why": "x"}}`),
		`{"id": "x", "name": "X", "version": "1.0.0", "api": 2, "permissions": {}, "actions": [{"name": "a", "description": "d", "confirm": true, "confirm": false}]}`,
	} {
		if _, err := readManifest(t, raw); err == nil {
			t.Errorf("%s: accepted", raw)
		}
	}
}

// requires.vclu with a number strconv cannot read is refused, not read as 0.
func TestFableRequiresVcluHugeNumber(t *testing.T) {
	SetVcluVersion("1.13.0")
	t.Cleanup(func() { SetVcluVersion("") })
	raw := `{"id": "x", "name": "X", "version": "1.0.0", "api": 2, "permissions": {}, "requires": {"vclu": ">=99999999999999999999.0.0"}}`
	if _, err := readManifest(t, raw); err == nil {
		t.Error("accepted")
	}
	// pre-releases and ranges are not the contract
	for _, v := range []string{">=1.13.0-beta", ">=1.13", "^1.13.0", "1.13.0", ">=1.13.0 <2.0.0"} {
		if _, err := readManifest(t, `{"id": "x", "name": "X", "version": "1.0.0", "api": 2, "permissions": {}, "requires": {"vclu": "`+v+`"}}`); err == nil {
			t.Errorf("%q accepted", v)
		}
	}
	// this vclu is as new as asked, or newer
	for _, c := range []struct {
		have, need string
		ok         bool
	}{{"1.13.0", ">=1.13.0", true}, {"1.13.0-dev", ">=1.13.0", true}, {"v1.14.2", ">=1.13.9", true}, {"1.13.0", ">=1.13.1", false}, {"2.0.0", ">=1.99.99", true}, {"dev", ">=9.0.0", true}} {
		SetVcluVersion(c.have)
		m, err := readManifest(t, `{"id": "x", "name": "X", "version": "1.0.0", "api": 2, "permissions": {}, "requires": {"vclu": "`+c.need+`"}}`)
		if err != nil {
			t.Fatal(err)
		}
		if got := m.checkRequires() == nil; got != c.ok {
			t.Errorf("have %s need %s: ok %v, want %v (%v)", c.have, c.need, got, c.ok, m.checkRequires())
		}
		if !c.ok && !errors.Is(m.checkRequires(), ErrNewerVclu) {
			t.Errorf("have %s need %s: not ErrNewerVclu", c.have, c.need)
		}
	}
}

// A dashboard with a field this vclu does not know is written for a newer one.
func TestFableDashboardsUnknownFieldRefused(t *testing.T) {
	raw := `{"id": "x", "name": "X", "version": "1.0.0", "api": 2, "permissions": {}, "dashboards": [{"id": "lista", "name": "L", "icon": "cart"}]}`
	_, err := readManifest(t, raw)
	if err == nil || !errors.Is(err, ErrNewerVclu) {
		t.Errorf("err %v", err)
	}
	// what zakupy declares still reads
	if _, err := readManifest(t, `{"id": "x", "name": "X", "version": "1.0.0", "api": 2, "permissions": {}, "dashboards": [{"id": "lista", "name": {"pl": "Zakupy", "en": "Shopping"}, "description": {"pl": "a", "en": "b"}}]}`); err != nil {
		t.Error(err)
	}
}

// A form field with a key this vclu does not know (a constraint it would not enforce) is
// written for a newer one; the official forms read.
func TestFableFormUnknownFieldKeyRefused(t *testing.T) {
	dir := t.TempDir()
	write := func(form string) {
		_ = os.WriteFile(filepath.Join(dir, "plugin.json"), []byte(`{"id": "x", "name": "X", "version": "1.0.0", "api": 2, "permissions": {}, "form": "form.json"}`), 0o644)
		_ = os.WriteFile(filepath.Join(dir, "form.json"), []byte(form), 0o644)
	}
	write(`{"settings": {"host": {"type": "string", "pattern": "^[0-9.]+$"}}}`)
	if _, _, err := ReadForm(dir); err == nil || !errors.Is(err, ErrNewerVclu) {
		t.Errorf("pattern: %v", err)
	}
	write(`{"settings": {"host": {"type": "string"}}, "devices": {"fields": {"ip": {"type": "host"}}, "paged": true}}`)
	if _, _, err := ReadForm(dir); err == nil || !errors.Is(err, ErrNewerVclu) {
		t.Errorf("devices.paged: %v", err)
	}
	write(`{"settings": {"host": {"type": "string"}}, "wizard": {}}`)
	if _, _, err := ReadForm(dir); err == nil || !errors.Is(err, ErrNewerVclu) {
		t.Errorf("wizard: %v", err)
	}
	for _, f := range []string{"tasmota", "frigate", "modbus-gateway"} {
		raw, err := os.ReadFile(filepath.Join(officialPluginsDir(t), f, "form.json"))
		if err != nil {
			continue
		}
		if _, err := ParseForm(raw); err != nil {
			t.Errorf("%s/form.json: %v", f, err)
		}
	}
}

func officialPluginsDir(t *testing.T) string {
	dir := os.Getenv("VCLU_PLUGINS_DIR")
	if dir == "" {
		t.Skip("set VCLU_PLUGINS_DIR to the plugins directory of a vclu-plugins checkout")
	}
	return dir
}

// Every official plugin.json on the new format reads, asks for what it says, and pins to
// the same grants on install.
func TestFableOfficialManifestsRead(t *testing.T) {
	dir := officialPluginsDir(t)
	entries, _ := os.ReadDir(dir)
	n := 0
	for _, e := range entries {
		raw, err := os.ReadFile(filepath.Join(dir, e.Name(), "plugin.json"))
		if err != nil {
			continue
		}
		var m PluginManifest
		if err := json.Unmarshal(raw, &m); err != nil {
			t.Errorf("%s: %v", e.Name(), err)
			continue
		}
		if len(m.UnknownKeys()) > 0 {
			t.Errorf("%s: unknown keys %v", e.Name(), m.UnknownKeys())
		}
		if err := checkFormFile(filepath.Join(dir, e.Name()), m); err != nil {
			t.Errorf("%s: %v", e.Name(), err)
		}
		n++
	}
	if n == 0 {
		t.Fatal("no manifests")
	}
}

// Grants written by the previous vclu: udp's broadcastPorts narrowed where a plugin may
// broadcast; the new field keeps it, so the first load does not record plugin.json's list
// over the user's.
func TestFableMigrationKeepsOldBroadcastPorts(t *testing.T) {
	var g config.PluginGrants
	if err := json.Unmarshal([]byte(`{"udp": {"hosts": ["192.168.1.0/24"], "broadcastPorts": []}}`), &g); err != nil {
		t.Fatal(err)
	}
	if g.UDP == nil || g.UDP.Broadcast == nil || len(*g.UDP.Broadcast) != 0 {
		t.Fatalf("broadcastPorts lost: %+v", g.UDP)
	}
	requested := Permissions{UDP: true, UDPPorts: []int{7000}, BroadcastPorts: []int{7000}}
	allowed, pending := requested.allowedBy(&g)
	if len(allowed.BroadcastPorts) != 0 || !slices.Equal(pending.BroadcastPorts, []int{7000}) {
		t.Errorf("allowed %v pending %v", allowed.BroadcastPorts, pending.BroadcastPorts)
	}
	// the new name wins when both are written
	if err := json.Unmarshal([]byte(`{"udp": {"broadcast": [7000], "broadcastPorts": []}}`), &g); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(*g.UDP.Broadcast, []int{7000}) {
		t.Errorf("broadcast %v", *g.UDP.Broadcast)
	}
}

// Pi 2's {"lan": {"websocketPorts": []}}: the old lan allowed HTTP on any port and WebSockets
// on none; the new lan.ports [80] allows both on 80. Not the same set, so the first load
// records plugin.json's ports: a design decision (a WebSocket on a port HTTP already reached).
func TestFableMigrationWebsocketPortsDesign(t *testing.T) {
	var g config.PluginGrants
	if err := json.Unmarshal([]byte(`{"lan": {"websocketPorts": []}}`), &g); err != nil {
		t.Fatal(err)
	}
	requested := Permissions{LAN: true, LANPorts: []int{80}}
	if !unpinned(requested, &g) {
		t.Fatal("not pinned from plugin.json")
	}
	t.Skip("design: websocketPorts [] becomes lan.ports from plugin.json (HTTP narrowed to them, WebSockets widened to them)")
}

// Grants the user saves never reach past plugin.json: hosts, ports, topic filters and
// actions beyond it are dropped or ignored.
func TestFableGrantsWithinManifest(t *testing.T) {
	m, err := readManifest(t, `{"id": "x", "name": "X", "version": "1.0.0", "api": 2,
		"permissions": {"internet": {"hosts": ["api.example.com"]}, "lan": {"ports": [80]}, "udp": {"ports": [7000]}, "tcp": {"ports": [23]},
		"mqtt": {"subscribe": ["z2m/#"], "publish": ["z2m/+/set"]}},
		"actions": [{"name": "add", "description": "d"}]}`)
	if err != nil {
		t.Fatal(err)
	}
	requested := requestedPermissions(m)
	ports := func(p ...int) *[]int { return &p }
	g := WithinRequest(config.PluginGrants{
		Internet: &config.InternetGrant{Hosts: []string{"*"}},
		LAN:      &config.LANGrant{Ports: ports(80, 443), AnyPort: true},
		UDP:      &config.HostsGrant{Ports: ports(7000, 7001), Broadcast: ports(7000)},
		TCP:      &config.HostsGrant{Ports: ports(23, 22)},
		MQTT:     &config.MQTTGrant{Subscribe: []string{"#", "z2m/+"}, Publish: []string{"z2m/+/set", "z2m/+/get"}},
		Modbus:   &config.ModbusGrant{},
		CLU:      &config.CLUGrant{},
		Actions:  &[]string{"add", "clear_all"},
	}, requested)
	allowed, _ := requested.allowedBy(&g)
	if !slices.Equal(allowed.Internet, InternetHosts{"api.example.com"}) || !slices.Equal(allowed.LANPorts, []int{80}) ||
		!slices.Equal(allowed.UDPPorts, []int{7000}) || len(allowed.BroadcastPorts) != 0 || !slices.Equal(allowed.TCPPorts, []int{23}) ||
		!slices.Equal(allowed.MQTTSubscribe, []string{"z2m/+"}) || !slices.Equal(allowed.MQTTPublish, []string{"z2m/+/set"}) ||
		allowed.Modbus || allowed.CLU || !slices.Equal(allowed.Actions, []string{"add"}) {
		t.Errorf("allowed %+v", allowed)
	}
	// what the panel sent back as seen cannot add to it either
	seen := requested.OnlySeen(Seen{LANPorts: ports(80, 443), UDPPorts: []int{7000, 7001}, TCPPorts: []int{22, 23},
		MQTTSubscribe: []string{"#"}, MQTTPublish: []string{"#"}, Actions: []string{"add", "clear_all"}}).OnlyHostsSeen([]string{"*"})
	if !slices.Equal(seen.Internet, InternetHosts{"api.example.com"}) || !slices.Equal(seen.LANPorts, []int{80}) || !slices.Equal(seen.UDPPorts, []int{7000}) ||
		!slices.Equal(seen.TCPPorts, []int{23}) || !slices.Equal(seen.MQTTSubscribe, []string{"z2m/#"}) || !slices.Equal(seen.Actions, []string{"add"}) {
		t.Errorf("seen %+v", seen)
	}
}

// Topic filters: a grant narrowed by the user is allowed only as far as plugin.json asks;
// "+" never covers "#", and an empty level is a level.
func TestFableTopicFilterCovers(t *testing.T) {
	for _, c := range []struct {
		wide, narrow string
		covers       bool
	}{
		{"#", "$SYS/#", true}, {"a/#", "a", true}, {"a/#", "a/b/c", true}, {"a/+", "a/#", false}, {"a/+", "a/b", true},
		{"a/+", "a/", true}, {"a/b", "a/b/", false}, {"a//b", "a/+/b", false}, {"a/+/b", "a//b", true}, {"+", "#", false},
		{"a/b", "a/+", false}, {"a", "a/#", false},
	} {
		if got := filterCovers(c.wide, c.narrow); got != c.covers {
			t.Errorf("filterCovers(%q, %q) = %v", c.wide, c.narrow, got)
		}
	}
	for _, bad := range []string{"", "a/#/b", "a+", "#a", "a/b#"} {
		if validTopicFilter(bad) {
			t.Errorf("%q valid", bad)
		}
	}
}

// Texts shown in the panel: control characters refused, markup left to the panel's escaping,
// why without < and >.
func TestFableManifestTexts(t *testing.T) {
	if _, err := readManifest(t, `{"id": "x", "name": "a\u0007b", "version": "1.0.0", "api": 2, "permissions": {}}`); err == nil {
		t.Error("control character in name accepted")
	}
	if _, err := readManifest(t, manifestWith(`{"lan": {"why": "<b>x</b>"}}`)); err == nil || !strings.Contains(err.Error(), "without <") {
		t.Errorf("why with markup: %v", err)
	}
	if _, err := readManifest(t, manifestWith(`{"lan": {"why": {"pl": "", "en": ""}}}`)); err == nil {
		t.Error("empty why accepted")
	}
	if _, err := readManifest(t, manifestWith(`{"lan": {"why": {"de": "x"}}}`)); err == nil {
		t.Error("why in another language accepted")
	}
}

// An install from before (Pi 2: lan recorded without ports, no actions recorded) loads a
// plugin on the new manifest: the first load records plugin.json's ports and actions as
// allowed, the update after that waits for anything new.
func TestFableMigrationPinsThenWaits(t *testing.T) {
	var old config.PluginGrants
	if err := json.Unmarshal([]byte(`{"lan": {"websocketPorts": []}}`), &old); err != nil {
		t.Fatal(err)
	}
	l := permissionsLoader(t, nil, []config.InstalledPlugin{{ID: "@vclu/zakupy", Grants: &old}})
	m := PluginManifest{API: 2, Permissions: &Permissions{LAN: true, LANPorts: []int{80}},
		Actions: ManifestActions{{Name: "add", Description: LocalText{Text: "d"}}, {Name: "clear_all", Description: LocalText{Text: "c"}, Confirm: true}}}
	perms, limits, _, err := l.effectivePermissions("@vclu/zakupy", m)
	if err != nil || !perms.LAN || !slices.Equal(perms.LANPorts, []int{80}) || !slices.Equal(perms.Actions, []string{"add", "clear_all"}) || !slices.Equal(limits.LANPorts, []int{80}) {
		t.Fatalf("first load: %+v %+v %v", perms, limits, err)
	}
	g, err := l.grants("@vclu/zakupy")
	if err != nil || g.LAN == nil || g.LAN.Ports == nil || !slices.Equal(*g.LAN.Ports, []int{80}) || g.LAN.AnyPort || g.Actions == nil || len(*g.Actions) != 2 {
		t.Fatalf("recorded: %+v %v", g, err)
	}
	// the next version lists a port and an action more: they wait, the rest runs
	m.Permissions.LANPorts = []int{80, 8080}
	m.Actions = append(m.Actions, ManifestAction{Name: "share", Description: LocalText{Text: "s"}})
	perms, limits, _, err = l.effectivePermissions("@vclu/zakupy", m)
	if err != nil || !slices.Equal(perms.LANPorts, []int{80}) || !slices.Equal(perms.Actions, []string{"add", "clear_all"}) || !slices.Equal(limits.LANPorts, []int{80}) {
		t.Errorf("after the update: %+v %+v %v", perms, limits, err)
	}
	_, pending := requestedPermissions(m).allowedBy(g)
	if !slices.Equal(pending.LANPorts, []int{8080}) || !slices.Equal(pending.Actions, []string{"share"}) {
		t.Errorf("pending %+v", pending)
	}
}

// Texts shown in the install dialog refuse format characters that change how they read.
func TestPlainTextRefusesFormatCharacters(t *testing.T) {
	for _, s := range []string{"Chmura\u202eTuya", "a\u200bb", "x\u2066y"} {
		if plainText(s, 200) {
			t.Errorf("%q accepted", s)
		}
	}
	if !plainText("Chmura Tuya, ąę", 200) {
		t.Error("plain text refused")
	}
}
