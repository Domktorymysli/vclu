package plugins

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/Domktorymysli/vclu/pkg/config"
)

// Plugin API versions this vCLU runs. "api" in plugin.json names the contract a plugin
// was written for; a manifest without it is API 1, the sandbox before permissions.
//   - 1: lan, mqtt and modbus for every plugin, events and expose paths with any name,
//     the HTTP, HttpClient and MQTT globals.
//   - 2: only the permissions plugin.json declares, events and expose paths under the
//     plugin's prefix, HTTP and MQTT only through the plugin API.
//
// Isolation (own standard tables, read-only objects, own instance and timers) holds on
// every version.
const (
	PluginAPIMin = 1
	PluginAPIMax = 2
)

// Permissions are what a plugin may reach, as plugin.json's "permissions" declares them
// (manifest.go reads and writes the JSON).
type Permissions struct {
	// Internet hosts (API 2; API 1 reaches any): nil none, ["*"] any
	Internet InternetHosts
	LAN      bool // HTTP and WebSockets to local addresses (the house's devices)
	UDP      bool // UDP to local devices, on UDPPorts, broadcast on BroadcastPorts
	TCP      bool // TCP to local devices, on TCPPorts
	MQTT     bool // subscribe and publish on the broker
	Modbus   bool // Modbus requests, reads and writes
	// Only once the user allows them in the panel, never on install:
	CLU    bool // UDP to CLUs, their cipher and requests through the registry
	TFTP   bool // reading and replacing files on devices
	Serial bool // a serial port the user assigns

	LANPorts       []int    // with lan: the local ports; nil any
	UDPPorts       []int    // with udp
	BroadcastPorts []int    // with udp, among UDPPorts
	TCPPorts       []int    // with tcp
	MQTTSubscribe  []string // with mqtt: topic filters; nil (both) any topic
	MQTTPublish    []string //
	Actions        []string // the declared actions agents and voice may call
	// Why: what the plugin says it needs a permission for, by its name
	Why map[string]LocalText
}

func manifestAPI(m PluginManifest) int {
	if m.API == 0 {
		return 1
	}
	return m.API
}

// checkAPI: the version the plugin runs on, or why it does not load here.
func (l *Loader) checkAPI(pluginID string, m PluginManifest) (int, error) {
	api := manifestAPI(m)
	if api < PluginAPIMin {
		return 0, fmt.Errorf("plugin %s: plugin API %d is not supported (this vCLU runs %d to %d)", pluginID, api, PluginAPIMin, PluginAPIMax)
	}
	if api > PluginAPIMax {
		return 0, fmt.Errorf("plugin %s needs plugin API %d; this vCLU runs up to %d, update vCLU", pluginID, api, PluginAPIMax)
	}
	if api < 2 && l.strict() {
		return 0, fmt.Errorf("plugin %s is on plugin API %d; strict mode loads only API 2 or later (\"api\": 2 and \"permissions\" in plugin.json)", pluginID, api)
	}
	return api, nil
}

func (l *Loader) strict() bool {
	mc, err := config.ReadMasterConfig(l.serverDir)
	return err == nil && mc != nil && mc.Plugins != nil && mc.Plugins.Strict
}

// requestedPermissions: what the plugin asks for. API 1 asks for everything, as before
// permissions existed (and any internet host, outside the grants); API 2 for what it declares.
func requestedPermissions(manifest PluginManifest) Permissions {
	if manifestAPI(manifest) < 2 {
		return Permissions{LAN: true, MQTT: true, Modbus: true}
	}
	var p Permissions
	if manifest.Permissions != nil {
		p = manifest.Permissions.clone()
	}
	p.Actions = manifest.Actions.Names()
	return p
}

func (p Permissions) clone() Permissions {
	c := p
	c.Internet = slices.Clone(p.Internet)
	c.LANPorts, c.UDPPorts, c.BroadcastPorts, c.TCPPorts = slices.Clone(p.LANPorts), slices.Clone(p.UDPPorts), slices.Clone(p.BroadcastPorts), slices.Clone(p.TCPPorts)
	c.MQTTSubscribe, c.MQTTPublish, c.Actions = slices.Clone(p.MQTTSubscribe), slices.Clone(p.MQTTPublish), slices.Clone(p.Actions)
	return c
}

// Names lists the permissions set in p, in a fixed order.
func (p Permissions) Names() []string {
	var out []string
	for _, n := range []struct {
		name string
		on   bool
	}{{"internet", len(p.Internet) > 0}, {"lan", p.LAN}, {"udp", p.UDP}, {"tcp", p.TCP}, {"mqtt", p.MQTT},
		{"modbus", p.Modbus}, {"clu", p.CLU}, {"tftp", p.TFTP}, {"serial", p.Serial}} {
		if n.on {
			out = append(out, n.name)
		}
	}
	return out
}

// Wide lists the permissions p asks for without a narrowing list: any internet host, any
// local port, any MQTT topic.
func (p Permissions) Wide() []string {
	var out []string
	if p.Internet.Any() {
		out = append(out, "internet")
	}
	if p.LAN && p.LANPorts == nil {
		out = append(out, "lan")
	}
	if p.MQTT && p.MQTTSubscribe == nil && p.MQTTPublish == nil {
		out = append(out, "mqtt")
	}
	return out
}

// AllPermissions: every permission a plugin can ask for, as the panel lists them.
var AllPermissions = Permissions{Internet: InternetHosts{"*"}, LAN: true, UDP: true, TCP: true, MQTT: true, Modbus: true, CLU: true, TFTP: true, Serial: true}

// Only keeps the permissions of p named in names.
func (p Permissions) Only(names []string) Permissions {
	has := func(n string) bool { return slices.Contains(names, n) }
	out := p.clone()
	if !has("internet") {
		out.Internet = nil
	}
	out.LAN, out.UDP, out.TCP, out.MQTT, out.Modbus = p.LAN && has("lan"), p.UDP && has("udp"), p.TCP && has("tcp"), p.MQTT && has("mqtt"), p.Modbus && has("modbus")
	out.CLU, out.TFTP, out.Serial = p.CLU && has("clu"), p.TFTP && has("tftp"), p.Serial && has("serial")
	if !out.LAN {
		out.LANPorts = nil
	}
	if !out.UDP {
		out.UDPPorts, out.BroadcastPorts = nil, nil
	}
	if !out.TCP {
		out.TCPPorts = nil
	}
	if !out.MQTT {
		out.MQTTSubscribe, out.MQTTPublish = nil, nil
	}
	return out
}

// splitPorts: the asked ports among those granted, and the rest; nil granted (not recorded
// yet) allows them all.
func splitPorts(asked []int, granted *[]int) (allowed, pending []int) {
	if granted == nil {
		return slices.Clone(asked), nil
	}
	allowed = []int{}
	for _, p := range asked {
		if slices.Contains(*granted, p) {
			allowed = append(allowed, p)
		} else {
			pending = append(pending, p)
		}
	}
	return allowed, pending
}

// splitStrings is splitPorts for action names.
func splitStrings(asked []string, granted *[]string) (allowed, pending []string) {
	if granted == nil {
		return slices.Clone(asked), nil
	}
	allowed = []string{}
	for _, s := range asked {
		if slices.Contains(*granted, s) {
			allowed = append(allowed, s)
		} else {
			pending = append(pending, s)
		}
	}
	return allowed, pending
}

// splitTopics: the granted filters within the asked ones (the user may have narrowed them),
// and the asked filters the grant does not touch at all (new in an update). Nil asked is any
// topic ("#"); nil granted (not recorded yet) allows what is asked.
func splitTopics(asked, granted []string) (allowed, pending []string) {
	ask := asked
	if ask == nil {
		ask = []string{"#"}
	}
	if granted == nil {
		return slices.Clone(asked), nil
	}
	allowed = []string{}
	for _, g := range granted {
		if slices.ContainsFunc(ask, func(a string) bool { return filterCovers(a, g) }) {
			allowed = append(allowed, g)
		}
	}
	for _, a := range ask {
		touched := slices.ContainsFunc(granted, func(g string) bool { return filterCovers(g, a) || filterCovers(a, g) })
		if !touched {
			pending = append(pending, a)
		}
	}
	return allowed, pending
}

// filterCovers: every topic filter a matches is matched by b.
func filterCovers(b, a string) bool {
	bl, al := strings.Split(b, "/"), strings.Split(a, "/")
	for i, x := range bl {
		if x == "#" {
			return true
		}
		if i >= len(al) {
			return false
		}
		y := al[i]
		if y == "#" {
			return false
		}
		if x == "+" {
			continue
		}
		if y == "+" || x != y {
			return false
		}
	}
	return len(al) == len(bl)
}

// allowedBy: the part of p the grants allow, and what p asks for beyond them. Nil grants
// (installed by hand, or before grants existed) allow what installing would.
func (p Permissions) allowedBy(g *config.PluginGrants) (allowed, pending Permissions) {
	if g == nil {
		g = p.Grants()
	}
	allowed = Permissions{
		LAN: p.LAN && g.LAN != nil, UDP: p.UDP && g.UDP != nil, TCP: p.TCP && g.TCP != nil, MQTT: p.MQTT && g.MQTT != nil, Modbus: p.Modbus && g.Modbus != nil,
		CLU: p.CLU && g.CLU != nil, TFTP: p.TFTP && g.TFTP != nil, Serial: p.Serial && g.Serial != nil, Why: p.Why,
	}
	pending = Permissions{
		LAN: p.LAN && g.LAN == nil, UDP: p.UDP && g.UDP == nil, TCP: p.TCP && g.TCP == nil, MQTT: p.MQTT && g.MQTT == nil, Modbus: p.Modbus && g.Modbus == nil,
		CLU: p.CLU && g.CLU == nil, TFTP: p.TFTP && g.TFTP == nil, Serial: p.Serial && g.Serial == nil, Why: p.Why,
	}
	// ports, topics and actions allowed before keep working; a new one waits, and the permission with it
	switch {
	case allowed.LAN && p.LANPorts == nil: // any port
		if g.LAN.AnyPort || g.LAN.Ports == nil {
			allowed.LANPorts = nil
		} else {
			allowed.LANPorts, pending.LAN = slices.Clone(*g.LAN.Ports), true // pending.LANPorts nil: any port waits
		}
	case allowed.LAN:
		if g.LAN.AnyPort {
			allowed.LANPorts = slices.Clone(p.LANPorts)
		} else {
			allowed.LANPorts, pending.LANPorts = splitPorts(p.LANPorts, g.LAN.Ports)
			pending.LAN = len(pending.LANPorts) > 0
		}
	case pending.LAN:
		pending.LANPorts = slices.Clone(p.LANPorts)
	}
	if allowed.UDP {
		allowed.UDPPorts, pending.UDPPorts = splitPorts(p.UDPPorts, g.UDP.Ports)
		allowed.BroadcastPorts, pending.BroadcastPorts = splitPorts(p.BroadcastPorts, g.UDP.Broadcast)
		pending.UDP = len(pending.UDPPorts) > 0 || len(pending.BroadcastPorts) > 0
	} else if pending.UDP {
		pending.UDPPorts, pending.BroadcastPorts = slices.Clone(p.UDPPorts), slices.Clone(p.BroadcastPorts)
	}
	if allowed.TCP {
		allowed.TCPPorts, pending.TCPPorts = splitPorts(p.TCPPorts, g.TCP.Ports)
		pending.TCP = len(pending.TCPPorts) > 0
	} else if pending.TCP {
		pending.TCPPorts = slices.Clone(p.TCPPorts)
	}
	if allowed.MQTT {
		var sub, pub []string
		allowed.MQTTSubscribe, sub = splitTopics(p.mqttAsk(p.MQTTSubscribe), g.MQTT.Subscribe)
		allowed.MQTTPublish, pub = splitTopics(p.mqttAsk(p.MQTTPublish), g.MQTT.Publish)
		if len(sub) > 0 || len(pub) > 0 {
			pending.MQTT, pending.MQTTSubscribe, pending.MQTTPublish = true, nonNilStrings(sub), nonNilStrings(pub)
		}
	} else if pending.MQTT {
		pending.MQTTSubscribe, pending.MQTTPublish = slices.Clone(p.MQTTSubscribe), slices.Clone(p.MQTTPublish)
	}
	allowed.Actions, pending.Actions = splitStrings(p.Actions, g.Actions)
	// hosts allowed before keep working; a new one, or "*", waits
	var granted InternetHosts
	if g.Internet != nil {
		granted, _ = NormalizeInternetHosts(g.Internet.Hosts) // a bad list allows none
	}
	if p.Internet.Any() && !granted.Any() {
		allowed.Internet, pending.Internet = granted, InternetHosts{"*"}
		return allowed, pending
	}
	for _, h := range p.Internet {
		if granted.covers(h) {
			allowed.Internet = append(allowed.Internet, h)
		} else {
			pending.Internet = append(pending.Internet, h)
		}
	}
	return allowed, pending
}

// mqttAsk: a direction's filters as asked; with neither list declared any topic (nil).
func (p Permissions) mqttAsk(list []string) []string {
	if p.MQTTSubscribe == nil && p.MQTTPublish == nil {
		return nil
	}
	return nonNilStrings(list)
}

// Grants turns permissions into what installing a plugin allows: each as declared, except
// clu, tftp and serial, which wait for the user in the panel.
func (p Permissions) Grants() *config.PluginGrants {
	g := &config.PluginGrants{}
	if len(p.Internet) > 0 {
		g.Internet = &config.InternetGrant{Hosts: slices.Clone([]string(p.Internet))}
	}
	if p.LAN {
		g.LAN = &config.LANGrant{}
		if p.LANPorts == nil {
			g.LAN.AnyPort = true
		} else {
			ports := append([]int{}, p.LANPorts...)
			g.LAN.Ports = &ports
		}
	}
	if p.UDP {
		ports, broadcast := append([]int{}, p.UDPPorts...), append([]int{}, p.BroadcastPorts...)
		g.UDP = &config.HostsGrant{Ports: &ports, Broadcast: &broadcast}
	}
	if p.TCP {
		ports := append([]int{}, p.TCPPorts...)
		g.TCP = &config.HostsGrant{Ports: &ports}
	}
	if p.MQTT {
		g.MQTT = &config.MQTTGrant{Subscribe: topicsGrant(p.mqttAsk(p.MQTTSubscribe)), Publish: topicsGrant(p.mqttAsk(p.MQTTPublish))}
	}
	if p.Modbus {
		g.Modbus = &config.ModbusGrant{}
	}
	actions := append([]string{}, p.Actions...)
	g.Actions = &actions
	return g
}

// topicsGrant: the filters recorded for a direction, "#" for any topic.
func topicsGrant(asked []string) []string {
	if asked == nil {
		return []string{"#"}
	}
	return append([]string{}, asked...)
}

// Limits narrow allowed permissions: nil lists allow any host or topic, an empty one none;
// SerialPorts are the only ports the plugin may open. LANPorts nil: any local port.
type Limits struct {
	InternetHosts []string `json:"internetHosts,omitzero"` // set on API 2 from the allowed hosts
	LANHosts      []string `json:"lanHosts,omitzero"`
	LANPorts      []int    `json:"lanPorts,omitzero"`
	UDPHosts      []string `json:"udpHosts,omitzero"`
	MQTTSubscribe []string `json:"mqttSubscribe,omitzero"`
	MQTTPublish   []string `json:"mqttPublish,omitzero"`
	ModbusHosts   []string `json:"modbusHosts,omitzero"`
	TCPHosts      []string `json:"tcpHosts,omitzero"`
	CLUHosts      []string `json:"cluHosts,omitzero"`
	CLUDiscovery  bool     `json:"cluDiscovery,omitzero"` // broadcasts with CLUHosts set
	TFTPHosts     []string `json:"tftpHosts,omitzero"`
	SerialPorts   []string `json:"serialPorts,omitzero"`
}

func (l Limits) narrows() bool {
	return l.LANHosts != nil || l.LANPorts != nil || l.MQTTSubscribe != nil || l.MQTTPublish != nil
}

func limitsOf(g *config.PluginGrants) Limits {
	var l Limits
	if g == nil {
		return l
	}
	if g.LAN != nil {
		l.LANHosts = g.LAN.Hosts
	}
	if g.UDP != nil {
		l.UDPHosts = g.UDP.Hosts
	}
	if g.TCP != nil {
		l.TCPHosts = g.TCP.Hosts
	}
	if g.Modbus != nil {
		l.ModbusHosts = g.Modbus.Hosts
	}
	if g.CLU != nil {
		l.CLUHosts, l.CLUDiscovery = g.CLU.Hosts, g.CLU.Discovery
	}
	if g.TFTP != nil {
		l.TFTPHosts = g.TFTP.Hosts
	}
	if g.Serial != nil {
		l.SerialPorts = g.Serial.Ports
	}
	return l
}

// effectivePermissions: what the plugin may reach, how the user narrowed it, and whether
// it is official (that names its events): what it asks for, as far as the user allowed it.
func (l *Loader) effectivePermissions(pluginID string, manifest PluginManifest) (Permissions, Limits, bool, error) {
	official := l.isOfficial(pluginID)
	requested := requestedPermissions(manifest)
	grants, err := l.grants(pluginID)
	if errors.Is(err, errNotInstalled) {
		// an entry removed meanwhile: nothing says what the user allowed it
		return Permissions{}, Limits{}, false, fmt.Errorf("plugin %s is not installed: it does not run", pluginID)
	}
	if err != nil {
		// without the grants the plugin would get all it asks for: it does not run until they are readable
		return Permissions{}, Limits{}, false, fmt.Errorf("plugin %s: its permissions cannot be read: %w", pluginID, err)
	}
	if manifestAPI(manifest) < 2 {
		log.Printf("[PLUGINS] %s is on plugin API 1: lan, mqtt and modbus without a declaration (\"api\": 2 in plugin.json limits it to its permissions)", pluginID)
		if allowed, _ := requested.allowedBy(grants); !slices.Equal(allowed.Names(), requested.Names()) || limitsOf(grants).narrows() {
			log.Printf("[PLUGINS] WARNING: %s is on plugin API 1: without lan or mqtt allowed in full in its settings it has no HTTP, HttpClient or MQTT global", pluginID)
		}
	}
	if grants != nil {
		if l.pinGrant(pluginID, requested, grants) {
			if grants, err = l.grants(pluginID); err != nil {
				return Permissions{}, Limits{}, false, fmt.Errorf("plugin %s: its permissions cannot be read: %w", pluginID, err)
			}
		}
	}
	allowed, pending := requested.allowedBy(grants)
	var waiting []string
	for _, name := range pending.Names() {
		if !slices.Contains(allowed.Names(), name) {
			waiting = append(waiting, name)
		}
	}
	if len(waiting) > 0 {
		log.Printf("[PLUGINS] %s asks for %s, not allowed yet: it runs without it until allowed in the plugin's settings", pluginID, strings.Join(waiting, ", "))
	}
	if more := pending.moreThanAllowed(allowed); more != "" {
		log.Printf("[PLUGINS] %s asks for %s, not allowed yet: it uses what was allowed before until then", pluginID, more)
	}
	limits := limitsOf(grants)
	if manifestAPI(manifest) >= 2 {
		limits.InternetHosts = append([]string{}, allowed.Internet...) // empty: no internet
	}
	limits.LANPorts = allowed.LANPorts
	if allowed.MQTT {
		limits.MQTTSubscribe, limits.MQTTPublish = allowed.MQTTSubscribe, allowed.MQTTPublish
	}
	return allowed, limits, official, nil
}

// moreThanAllowed names what waits within permissions already allowed: ports, topic filters
// and actions an update added.
func (p Permissions) moreThanAllowed(allowed Permissions) string {
	var parts []string
	if allowed.LAN && p.LAN {
		if p.LANPorts == nil {
			parts = append(parts, "lan on any port")
		} else {
			parts = append(parts, fmt.Sprintf("lan ports %v", p.LANPorts))
		}
	}
	if allowed.UDP && len(p.UDPPorts) > 0 {
		parts = append(parts, fmt.Sprintf("udp ports %v", p.UDPPorts))
	}
	if allowed.UDP && len(p.BroadcastPorts) > 0 {
		parts = append(parts, fmt.Sprintf("udp broadcast ports %v", p.BroadcastPorts))
	}
	if allowed.TCP && len(p.TCPPorts) > 0 {
		parts = append(parts, fmt.Sprintf("tcp ports %v", p.TCPPorts))
	}
	if allowed.MQTT && (len(p.MQTTSubscribe) > 0 || len(p.MQTTPublish) > 0) {
		parts = append(parts, fmt.Sprintf("mqtt topics %v %v", p.MQTTSubscribe, p.MQTTPublish))
	}
	if len(p.Actions) > 0 {
		parts = append(parts, fmt.Sprintf("actions %v", p.Actions))
	}
	return strings.Join(parts, ", ")
}

// errNotInstalled: the configuration lists plugins and this is not one of them.
var errNotInstalled = errors.New("not installed")

// grants: what the user allowed the installed plugin, nil when the config does not say
// (installed by hand, or before grants); an error when .vclu.json cannot be read, or lists
// plugins without this one.
func (l *Loader) grants(pluginID string) (*config.PluginGrants, error) {
	mc, err := config.ReadMasterConfig(l.serverDir)
	if err != nil {
		return nil, err
	}
	if mc == nil || mc.Plugins == nil {
		return nil, nil // no configuration yet
	}
	for _, inst := range mc.Plugins.Installed {
		if inst.ID == pluginID {
			return inst.Grants, nil
		}
	}
	return nil, errNotInstalled
}

func (l *Loader) diskManifest(pluginID string) (PluginManifest, error) {
	var manifest PluginManifest
	data, err := os.ReadFile(filepath.Join(l.resolvePluginDir(pluginID), "plugin.json"))
	if err != nil {
		return manifest, err
	}
	err = json.Unmarshal(data, &manifest)
	return manifest, err
}

// RequestedPermissions: what the plugin's files on disk ask for.
func (l *Loader) RequestedPermissions(pluginID string) (Permissions, error) {
	manifest, err := l.diskManifest(pluginID)
	if err != nil {
		return Permissions{}, err
	}
	return requestedPermissions(manifest), nil
}

// PendingPermissions: what the installed plugin asks for and the user has not allowed.
func (l *Loader) PendingPermissions(p config.InstalledPlugin) []string {
	return l.PermissionsView(p).Pending.Names()
}

// PermissionsView is an installed plugin's permissions as the panel shows them: what it asks
// for (Internet ["*"] on API 1, which reaches any host) and what of it waits for the user.
type PermissionsView struct {
	API                int
	Requested, Pending Permissions
	Internet           []string
	Actions            ManifestActions
}

func (l *Loader) PermissionsView(p config.InstalledPlugin) PermissionsView {
	manifest, err := l.diskManifest(p.ID)
	if err != nil {
		return PermissionsView{}
	}
	requested := requestedPermissions(manifest)
	_, pending := requested.allowedBy(p.Grants)
	v := PermissionsView{API: manifestAPI(manifest), Requested: requested, Pending: pending, Internet: requested.Internet, Actions: manifest.Actions}
	if manifestAPI(manifest) < 2 {
		v.Internet = []string{"*"}
	}
	return v
}

// recordGrants: an installed plugin without grants keeps what its files on disk ask for, so
// an update that asks for more does not get it without the user.
func (l *Loader) recordGrants(pluginID string) {
	requested, err := l.RequestedPermissions(pluginID)
	if err != nil {
		return
	}
	err = config.UpdateMasterConfig(l.serverDir, func(mc *config.VCLUConfig) error {
		if mc.Plugins == nil {
			return nil
		}
		for i := range mc.Plugins.Installed {
			if mc.Plugins.Installed[i].ID == pluginID && mc.Plugins.Installed[i].Grants == nil {
				mc.Plugins.Installed[i].Grants = requested.Grants()
			}
		}
		return nil
	})
	if err != nil {
		log.Printf("[PLUGINS] WARNING: permissions of %s not recorded: %v", pluginID, err)
	}
}

// recordEmptyGrants: an entry without grants whose files had to be fetched again allows none of
// what the fetched files ask for; the user allows it in the panel.
func (l *Loader) recordEmptyGrants(pluginID string) {
	err := config.UpdateMasterConfig(l.serverDir, func(mc *config.VCLUConfig) error {
		if mc.Plugins == nil {
			return nil
		}
		for i := range mc.Plugins.Installed {
			if mc.Plugins.Installed[i].ID == pluginID && mc.Plugins.Installed[i].Grants == nil {
				mc.Plugins.Installed[i].Grants = &config.PluginGrants{}
			}
		}
		return nil
	})
	if err != nil {
		log.Printf("[PLUGINS] WARNING: permissions of %s not recorded: %v", pluginID, err)
	}
}

// PinPorts records the ports, topic filters and actions of requested (the files on disk) for
// grants from before they were recorded, before an update may list others.
func (l *Loader) PinPorts(pluginID string, requested Permissions) {
	g, err := l.grants(pluginID)
	if err != nil || g == nil {
		return
	}
	l.pinGrant(pluginID, requested, g)
}

// unpinned: the lists of g not recorded yet for what requested asks.
func unpinned(requested Permissions, g *config.PluginGrants) bool {
	return requested.LAN && g.LAN != nil && g.LAN.Ports == nil && !g.LAN.AnyPort ||
		requested.UDP && g.UDP != nil && (g.UDP.Ports == nil || g.UDP.Broadcast == nil) ||
		requested.TCP && g.TCP != nil && g.TCP.Ports == nil ||
		requested.MQTT && g.MQTT != nil && (g.MQTT.Subscribe == nil || g.MQTT.Publish == nil) ||
		g.Actions == nil
}

// pinGrant: a grant from before its lists were recorded keeps what plugin.json asks for now,
// so an update adding a port, topic filter or action waits for the user; true when it wrote.
func (l *Loader) pinGrant(pluginID string, requested Permissions, g *config.PluginGrants) bool {
	if !unpinned(requested, g) {
		return false
	}
	full := requested.Grants()
	err := config.UpdateMasterConfig(l.serverDir, func(mc *config.VCLUConfig) error {
		if mc.Plugins == nil {
			return nil
		}
		for i := range mc.Plugins.Installed {
			ig := mc.Plugins.Installed[i].Grants
			if mc.Plugins.Installed[i].ID != pluginID || ig == nil {
				continue
			}
			if requested.LAN && ig.LAN != nil && ig.LAN.Ports == nil && !ig.LAN.AnyPort {
				ig.LAN.Ports, ig.LAN.AnyPort = full.LAN.Ports, full.LAN.AnyPort
			}
			if requested.UDP && ig.UDP != nil {
				if ig.UDP.Ports == nil {
					ig.UDP.Ports = full.UDP.Ports
				}
				if ig.UDP.Broadcast == nil {
					ig.UDP.Broadcast = full.UDP.Broadcast
				}
			}
			if requested.TCP && ig.TCP != nil && ig.TCP.Ports == nil {
				ig.TCP.Ports = full.TCP.Ports
			}
			if requested.MQTT && ig.MQTT != nil {
				if ig.MQTT.Subscribe == nil {
					ig.MQTT.Subscribe = full.MQTT.Subscribe
				}
				if ig.MQTT.Publish == nil {
					ig.MQTT.Publish = full.MQTT.Publish
				}
			}
			if ig.Actions == nil {
				ig.Actions = full.Actions
			}
		}
		return nil
	})
	if err != nil {
		log.Printf("[PLUGINS] WARNING: the permission lists of %s not recorded: %v", pluginID, err)
		return false
	}
	return true
}

// isOfficial: the configuration says where the plugin came from; its id alone does
// not, since any repository can list an @vclu id.
func (l *Loader) isOfficial(pluginID string) bool {
	mc, err := config.ReadMasterConfig(l.serverDir)
	if err != nil || mc == nil || mc.Plugins == nil {
		return false
	}
	namespace, _ := parseFullId(pluginID)
	for _, installed := range mc.Plugins.Installed {
		if installed.ID != pluginID {
			continue
		}
		if installed.Repository == "" {
			return namespace == "vclu"
		}
		for _, repo := range mc.Plugins.Repositories {
			if repo.Name == installed.Repository {
				return IsOfficialRepo(repo)
			}
		}
		return false
	}
	return false
}

// IsOfficialRepo: the default repository at the official URL.
func IsOfficialRepo(repo config.PluginRepository) bool {
	return repo.Default && sameRepoURL(repo.URL, config.OfficialPluginsRepoURL)
}

// FromRepository is whether repo may update the installed plugin: only the repository it came
// from, by its address once recorded. An entry from before repositories were recorded takes an
// @vclu id only from the official one.
func FromRepository(installed config.InstalledPlugin, repo config.PluginRepository) bool {
	if installed.RepositoryURL != "" {
		return sameRepoURL(installed.RepositoryURL, repo.URL)
	}
	if installed.Repository != "" {
		return installed.Repository == repo.Name
	}
	namespace, _ := parseFullId(installed.ID)
	return namespace != "vclu" || IsOfficialRepo(repo)
}

// SourceMoved: repo has the name the plugin was installed from, at another address.
func SourceMoved(installed config.InstalledPlugin, repo config.PluginRepository) bool {
	return installed.Repository == repo.Name && installed.RepositoryURL != "" && !sameRepoURL(installed.RepositoryURL, repo.URL)
}

// RepoURL: a repository address as recorded for an installed plugin.
func RepoURL(u string) string { return normRepoURL(u) }

// FromSource is FromRepository with, for an entry from before addresses were recorded, the
// address the installed files were downloaded from (their .complete marker); moved is set
// when the name matches but the address does not.
func (l *Loader) FromSource(installed config.InstalledPlugin, repo config.PluginRepository) (ok, moved bool) {
	if !FromRepository(installed, repo) {
		return false, SourceMoved(installed, repo)
	}
	if installed.RepositoryURL == "" {
		if from := l.downloadedFrom(installed.ID); from != "" && !sameRepoURL(from, repo.URL) {
			return false, true
		}
	}
	return true, false
}

// downloadedFrom: the repository address in the .complete marker of the installed files, "" when
// there is none (untagged or installed by hand).
func (l *Loader) downloadedFrom(pluginID string) string {
	data, err := os.ReadFile(filepath.Join(l.resolvePluginDir(pluginID), ".complete"))
	if err != nil {
		return ""
	}
	_, url, ok := strings.Cut(string(data), "\n")
	if !ok {
		return ""
	}
	return strings.TrimSpace(url)
}

func sameRepoURL(a, b string) bool {
	return normRepoURL(a) == normRepoURL(b)
}

// normRepoURL: scheme and host in lower case; the path as given, except on GitHub, whose
// owner/repo ignore case. A trailing / or .git is dropped.
func normRepoURL(u string) string {
	u = strings.TrimSpace(u)
	if pu, err := url.Parse(u); err == nil && pu.Scheme != "" && pu.Host != "" {
		pu.Scheme, pu.Host = strings.ToLower(pu.Scheme), strings.ToLower(pu.Host)
		if caseFreePath[pu.Hostname()] {
			pu.Path, pu.RawPath = strings.ToLower(pu.Path), ""
		}
		u = pu.String()
	}
	u = strings.TrimSuffix(u, "/")
	return strings.TrimSuffix(u, ".git")
}

// caseFreePath: hosts whose repository paths ignore case.
var caseFreePath = map[string]bool{"github.com": true, "www.github.com": true, "raw.githubusercontent.com": true}
