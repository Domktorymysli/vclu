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

// Permissions are what a plugin may reach, declared in plugin.json as "permissions":
// {"internet": ["api.example.com"], "lan": true, "udp": true, "tcp": true, "mqtt": true, "modbus": true}.
type Permissions struct {
	// Internet hosts (API 2; API 1 reaches any): nil none, ["*"] any
	Internet InternetHosts `json:"internet,omitempty"`
	LAN      bool          `json:"lan,omitempty"`    // HTTP to local addresses (the house's devices)
	UDP      bool          `json:"udp,omitempty"`    // UDP to local devices, broadcast only to the ports in "udp" (API 2)
	TCP      bool          `json:"tcp,omitempty"`    // TCP to local devices, only to the ports in "tcp" (API 2)
	MQTT     bool          `json:"mqtt,omitempty"`   // subscribe and publish on the broker
	Modbus   bool          `json:"modbus,omitempty"` // Modbus requests, reads and writes
	// Only once the user allows them in the panel, never on install:
	GrentonRaw bool `json:"grenton_raw,omitempty"` // UDP frames and the cipher of CLUs
	TFTP       bool `json:"tftp,omitempty"`        // reading and replacing files on devices
	Serial     bool `json:"serial,omitempty"`      // a serial port the user assigns
	// Ports from plugin.json's "udp" and "tcp", with those permissions (requestedPermissions)
	BroadcastPorts []int `json:"-"`
	TCPPorts       []int `json:"-"`
	WebSocketPorts []int `json:"-"` // "websocket": {"lanPorts"}, with lan
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
// permissions existed (and any internet host, outside the grants); API 2 for what it declares, nothing without the field.
func requestedPermissions(manifest PluginManifest) Permissions {
	if manifestAPI(manifest) < 2 {
		return Permissions{LAN: true, MQTT: true, Modbus: true, WebSocketPorts: manifest.webSocketPorts()}
	}
	if manifest.Permissions == nil {
		return Permissions{}
	}
	p := *manifest.Permissions
	p.BroadcastPorts, p.TCPPorts, p.WebSocketPorts = nil, nil, nil
	if p.LAN {
		p.WebSocketPorts = manifest.webSocketPorts()
	}
	if p.UDP && manifest.UDP != nil {
		p.BroadcastPorts = slices.Clone(manifest.UDP.BroadcastPorts)
	}
	if p.TCP && manifest.TCP != nil {
		p.TCPPorts = slices.Clone(manifest.TCP.Ports)
	}
	return p
}

func (m PluginManifest) webSocketPorts() []int {
	if m.WebSocket == nil {
		return nil
	}
	return slices.Clone(m.WebSocket.LANPorts)
}

// Names lists the permissions set in p, in a fixed order.
func (p Permissions) Names() []string {
	var out []string
	if len(p.Internet) > 0 {
		out = append(out, "internet")
	}
	if p.LAN {
		out = append(out, "lan")
	}
	if p.UDP {
		out = append(out, "udp")
	}
	if p.TCP {
		out = append(out, "tcp")
	}
	if p.MQTT {
		out = append(out, "mqtt")
	}
	if p.Modbus {
		out = append(out, "modbus")
	}
	if p.GrentonRaw {
		out = append(out, "grenton_raw")
	}
	if p.TFTP {
		out = append(out, "tftp")
	}
	if p.Serial {
		out = append(out, "serial")
	}
	return out
}

// AllPermissions: every permission a plugin can ask for, as the panel lists them.
var AllPermissions = Permissions{Internet: InternetHosts{"*"}, LAN: true, UDP: true, TCP: true, MQTT: true, Modbus: true, GrentonRaw: true, TFTP: true, Serial: true}

// Only keeps the permissions of p named in names.
func (p Permissions) Only(names []string) Permissions {
	has := func(n string) bool { return slices.Contains(names, n) }
	var internet InternetHosts
	if has("internet") {
		internet = p.Internet
	}
	out := Permissions{
		Internet: internet,
		LAN:      p.LAN && has("lan"), UDP: p.UDP && has("udp"), TCP: p.TCP && has("tcp"), MQTT: p.MQTT && has("mqtt"), Modbus: p.Modbus && has("modbus"),
		GrentonRaw: p.GrentonRaw && has("grenton_raw"), TFTP: p.TFTP && has("tftp"), Serial: p.Serial && has("serial"),
	}
	if out.UDP {
		out.BroadcastPorts = p.BroadcastPorts
	}
	if out.TCP {
		out.TCPPorts = p.TCPPorts
	}
	if out.LAN {
		out.WebSocketPorts = p.WebSocketPorts
	}
	return out
}

// splitPorts: the asked ports among those granted, and the rest; nil granted (allowed
// before ports were recorded) allows them all.
func splitPorts(asked []int, granted *[]int) (allowed, pending []int) {
	if granted == nil {
		return slices.Clone(asked), nil
	}
	for _, p := range asked {
		if slices.Contains(*granted, p) {
			allowed = append(allowed, p)
		} else {
			pending = append(pending, p)
		}
	}
	return allowed, pending
}

// allowedBy: the part of p the grants allow, and what p asks for beyond them. Nil grants
// (installed by hand, or before grants existed) allow what installing would.
func (p Permissions) allowedBy(g *config.PluginGrants) (allowed, pending Permissions) {
	if g == nil {
		g = p.Grants()
	}
	allowed = Permissions{
		LAN: p.LAN && g.LAN != nil, UDP: p.UDP && g.UDP != nil, TCP: p.TCP && g.TCP != nil, MQTT: p.MQTT && g.MQTT != nil, Modbus: p.Modbus && g.Modbus != nil,
		GrentonRaw: p.GrentonRaw && g.GrentonRaw != nil, TFTP: p.TFTP && g.TFTP != nil, Serial: p.Serial && g.Serial != nil,
	}
	pending = Permissions{
		LAN: p.LAN && g.LAN == nil, UDP: p.UDP && g.UDP == nil, TCP: p.TCP && g.TCP == nil, MQTT: p.MQTT && g.MQTT == nil, Modbus: p.Modbus && g.Modbus == nil,
		GrentonRaw: p.GrentonRaw && g.GrentonRaw == nil, TFTP: p.TFTP && g.TFTP == nil, Serial: p.Serial && g.Serial == nil,
	}
	// ports allowed before keep working; a new one waits, and the permission with it
	if allowed.LAN {
		allowed.WebSocketPorts, pending.WebSocketPorts = splitPorts(p.WebSocketPorts, g.LAN.WebSocketPorts)
		pending.LAN = len(pending.WebSocketPorts) > 0
	} else if pending.LAN {
		pending.WebSocketPorts = slices.Clone(p.WebSocketPorts)
	}
	if allowed.UDP {
		allowed.BroadcastPorts, pending.BroadcastPorts = splitPorts(p.BroadcastPorts, g.UDP.BroadcastPorts)
		pending.UDP = len(pending.BroadcastPorts) > 0
	} else if pending.UDP {
		pending.BroadcastPorts = slices.Clone(p.BroadcastPorts)
	}
	if allowed.TCP {
		allowed.TCPPorts, pending.TCPPorts = splitPorts(p.TCPPorts, g.TCP.Ports)
		pending.TCP = len(pending.TCPPorts) > 0
	} else if pending.TCP {
		pending.TCPPorts = slices.Clone(p.TCPPorts)
	}
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

// Grants turns permissions into what installing a plugin allows: each in full, except
// grenton_raw, tftp and serial, which wait for the user in the panel.
func (p Permissions) Grants() *config.PluginGrants {
	g := &config.PluginGrants{}
	if len(p.Internet) > 0 {
		g.Internet = &config.InternetGrant{Hosts: slices.Clone([]string(p.Internet))}
	}
	if p.LAN {
		ports := append([]int{}, p.WebSocketPorts...)
		g.LAN = &config.LANGrant{WebSocketPorts: &ports}
	}
	if p.UDP {
		ports := append([]int{}, p.BroadcastPorts...)
		g.UDP = &config.HostsGrant{BroadcastPorts: &ports}
	}
	if p.TCP {
		ports := append([]int{}, p.TCPPorts...)
		g.TCP = &config.HostsGrant{Ports: &ports}
	}
	if p.MQTT {
		g.MQTT = &config.MQTTGrant{}
	}
	if p.Modbus {
		g.Modbus = &config.ModbusGrant{}
	}
	return g
}

// Limits narrow allowed permissions: nil lists allow any host or topic, an empty one none;
// SerialPorts are the only ports the plugin may open.
type Limits struct {
	InternetHosts   []string `json:"internetHosts,omitzero"` // set on API 2 from the allowed hosts
	LANHosts        []string `json:"lanHosts,omitzero"`
	UDPHosts        []string `json:"udpHosts,omitzero"`
	TCPHosts        []string `json:"tcpHosts,omitzero"`
	MQTTSubscribe   []string `json:"mqttSubscribe,omitzero"`
	MQTTPublish     []string `json:"mqttPublish,omitzero"`
	GrentonRawHosts []string `json:"grentonRawHosts,omitzero"`
	TFTPHosts       []string `json:"tftpHosts,omitzero"`
	SerialPorts     []string `json:"serialPorts,omitzero"`
}

func (l Limits) narrows() bool {
	return l.LANHosts != nil || l.MQTTSubscribe != nil || l.MQTTPublish != nil
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
	if g.MQTT != nil {
		l.MQTTSubscribe, l.MQTTPublish = g.MQTT.Subscribe, g.MQTT.Publish
	}
	if g.GrentonRaw != nil {
		l.GrentonRawHosts = g.GrentonRaw.Hosts
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
		l.recordPorts(pluginID, requested, grants)
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
	for _, p := range []struct {
		name  string
		ok    bool
		ports []int
	}{{"udp broadcast", allowed.UDP, pending.BroadcastPorts}, {"tcp", allowed.TCP, pending.TCPPorts}, {"WebSocket", allowed.LAN, pending.WebSocketPorts}} {
		if p.ok && len(p.ports) > 0 {
			log.Printf("[PLUGINS] %s asks for %s ports %v, not allowed yet: it uses those allowed before until then", pluginID, p.name, p.ports)
		}
	}
	limits := limitsOf(grants)
	if manifestAPI(manifest) >= 2 {
		limits.InternetHosts = append([]string{}, allowed.Internet...) // empty: no internet
	}
	return allowed, limits, official, nil
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

// PermissionsView is an installed plugin's permissions as the panel shows them.
type PermissionsView struct {
	Requested, Pending Permissions
	// Internet: the hosts the plugin asks for; ["*"] on API 1, which reaches any
	Internet []string
	// BroadcastPorts: with udp, the ports it may broadcast to
	BroadcastPorts []int
	// TCPPorts: with tcp, the ports it connects to
	TCPPorts []int
	// WebSocketPorts: with lan, the local ports its WebSockets reach
	WebSocketPorts []int
}

func (l *Loader) PermissionsView(p config.InstalledPlugin) PermissionsView {
	manifest, err := l.diskManifest(p.ID)
	if err != nil {
		return PermissionsView{}
	}
	requested := requestedPermissions(manifest)
	_, pending := requested.allowedBy(p.Grants)
	v := PermissionsView{Requested: requested, Pending: pending, Internet: requested.Internet, BroadcastPorts: requested.BroadcastPorts, TCPPorts: requested.TCPPorts, WebSocketPorts: requested.WebSocketPorts}
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

// PinPorts records the ports of requested (the files on disk) for grants from before ports
// were recorded, before an update may list others.
func (l *Loader) PinPorts(pluginID string, requested Permissions) {
	g, err := l.grants(pluginID)
	if err != nil || g == nil {
		return
	}
	l.recordPorts(pluginID, requested, g)
}

// recordPorts: a lan, udp or tcp grant from before ports were recorded keeps the ports plugin.json
// lists now, so an update adding one waits for the user.
func (l *Loader) recordPorts(pluginID string, requested Permissions, g *config.PluginGrants) {
	udp := requested.UDP && g.UDP != nil && g.UDP.BroadcastPorts == nil
	tcp := requested.TCP && g.TCP != nil && g.TCP.Ports == nil
	ws := requested.LAN && g.LAN != nil && g.LAN.WebSocketPorts == nil
	if !udp && !tcp && !ws {
		return
	}
	err := config.UpdateMasterConfig(l.serverDir, func(mc *config.VCLUConfig) error {
		if mc.Plugins == nil {
			return nil
		}
		for i := range mc.Plugins.Installed {
			ig := mc.Plugins.Installed[i].Grants
			if mc.Plugins.Installed[i].ID != pluginID || ig == nil {
				continue
			}
			if udp && ig.UDP != nil && ig.UDP.BroadcastPorts == nil {
				ports := append([]int{}, requested.BroadcastPorts...)
				ig.UDP.BroadcastPorts = &ports
			}
			if tcp && ig.TCP != nil && ig.TCP.Ports == nil {
				ports := append([]int{}, requested.TCPPorts...)
				ig.TCP.Ports = &ports
			}
			if ws && ig.LAN != nil && ig.LAN.WebSocketPorts == nil {
				ports := append([]int{}, requested.WebSocketPorts...)
				ig.LAN.WebSocketPorts = &ports
			}
		}
		return nil
	})
	if err != nil {
		log.Printf("[PLUGINS] WARNING: ports of %s not recorded: %v", pluginID, err)
	}
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
