package plugins

import (
	"encoding/json"
	"fmt"
	"log"
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
// {"internet": ["api.example.com"], "lan": true, "udp": true, "mqtt": true, "modbus": true}.
type Permissions struct {
	// Internet hosts (API 2; API 1 reaches any): nil none, ["*"] any
	Internet InternetHosts `json:"internet,omitempty"`
	LAN      bool          `json:"lan,omitempty"`    // HTTP to local addresses (the house's devices)
	UDP      bool          `json:"udp,omitempty"`    // UDP to local devices, broadcast only to the ports in "udp" (API 2)
	MQTT     bool          `json:"mqtt,omitempty"`   // subscribe and publish on the broker
	Modbus   bool          `json:"modbus,omitempty"` // Modbus requests, reads and writes
	// Only once the user allows them in the panel, never on install:
	GrentonRaw bool `json:"grenton_raw,omitempty"` // UDP frames and the cipher of CLUs
	TFTP       bool `json:"tftp,omitempty"`        // reading and replacing files on devices
	Serial     bool `json:"serial,omitempty"`      // a serial port the user assigns
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
		return Permissions{LAN: true, MQTT: true, Modbus: true}
	}
	if manifest.Permissions != nil {
		return *manifest.Permissions
	}
	return Permissions{}
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
var AllPermissions = Permissions{Internet: InternetHosts{"*"}, LAN: true, UDP: true, MQTT: true, Modbus: true, GrentonRaw: true, TFTP: true, Serial: true}

// Only keeps the permissions of p named in names.
func (p Permissions) Only(names []string) Permissions {
	has := func(n string) bool { return slices.Contains(names, n) }
	var internet InternetHosts
	if has("internet") {
		internet = p.Internet
	}
	return Permissions{
		Internet: internet,
		LAN:      p.LAN && has("lan"), UDP: p.UDP && has("udp"), MQTT: p.MQTT && has("mqtt"), Modbus: p.Modbus && has("modbus"),
		GrentonRaw: p.GrentonRaw && has("grenton_raw"), TFTP: p.TFTP && has("tftp"), Serial: p.Serial && has("serial"),
	}
}

// allowedBy: the part of p the grants allow, and what p asks for beyond them. Nil grants
// (installed by hand, or before grants existed) allow what installing would.
func (p Permissions) allowedBy(g *config.PluginGrants) (allowed, pending Permissions) {
	if g == nil {
		g = p.Grants()
	}
	allowed = Permissions{
		LAN: p.LAN && g.LAN != nil, UDP: p.UDP && g.UDP != nil, MQTT: p.MQTT && g.MQTT != nil, Modbus: p.Modbus && g.Modbus != nil,
		GrentonRaw: p.GrentonRaw && g.GrentonRaw != nil, TFTP: p.TFTP && g.TFTP != nil, Serial: p.Serial && g.Serial != nil,
	}
	pending = Permissions{
		LAN: p.LAN && g.LAN == nil, UDP: p.UDP && g.UDP == nil, MQTT: p.MQTT && g.MQTT == nil, Modbus: p.Modbus && g.Modbus == nil,
		GrentonRaw: p.GrentonRaw && g.GrentonRaw == nil, TFTP: p.TFTP && g.TFTP == nil, Serial: p.Serial && g.Serial == nil,
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
		g.LAN = &config.LANGrant{}
	}
	if p.UDP {
		g.UDP = &config.HostsGrant{}
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
func (l *Loader) effectivePermissions(pluginID string, manifest PluginManifest) (Permissions, Limits, bool) {
	official := l.isOfficial(pluginID)
	requested := requestedPermissions(manifest)
	grants := l.grants(pluginID)
	if manifestAPI(manifest) < 2 {
		log.Printf("[PLUGINS] %s is on plugin API 1: lan, mqtt and modbus without a declaration (\"api\": 2 in plugin.json limits it to its permissions)", pluginID)
		if allowed, _ := requested.allowedBy(grants); !slices.Equal(allowed.Names(), requested.Names()) || limitsOf(grants).narrows() {
			log.Printf("[PLUGINS] WARNING: %s is on plugin API 1: without lan or mqtt allowed in full in its settings it has no HTTP, HttpClient or MQTT global", pluginID)
		}
	}
	allowed, pending := requested.allowedBy(grants)
	if names := pending.Names(); len(names) > 0 {
		log.Printf("[PLUGINS] %s asks for %s, not allowed yet: it runs without it until allowed in the plugin's settings", pluginID, strings.Join(names, ", "))
	}
	limits := limitsOf(grants)
	if manifestAPI(manifest) >= 2 {
		limits.InternetHosts = append([]string{}, allowed.Internet...) // empty: no internet
	}
	return allowed, limits, official
}

// grants: what the user allowed the installed plugin, nil when the config does not say.
func (l *Loader) grants(pluginID string) *config.PluginGrants {
	mc, err := config.ReadMasterConfig(l.serverDir)
	if err != nil || mc == nil || mc.Plugins == nil {
		return nil
	}
	for _, installed := range mc.Plugins.Installed {
		if installed.ID == pluginID {
			return installed.Grants
		}
	}
	return nil
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
}

func (l *Loader) PermissionsView(p config.InstalledPlugin) PermissionsView {
	manifest, err := l.diskManifest(p.ID)
	if err != nil {
		return PermissionsView{}
	}
	requested := requestedPermissions(manifest)
	_, pending := requested.allowedBy(p.Grants)
	v := PermissionsView{Requested: requested, Pending: pending, Internet: requested.Internet, BroadcastPorts: manifest.BroadcastPorts()}
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
// from. An entry from before repositories were recorded takes an @vclu id only from the
// official one.
func FromRepository(installed config.InstalledPlugin, repo config.PluginRepository) bool {
	if installed.Repository != "" {
		return installed.Repository == repo.Name
	}
	namespace, _ := parseFullId(installed.ID)
	return namespace != "vclu" || IsOfficialRepo(repo)
}

func sameRepoURL(a, b string) bool {
	return normRepoURL(a) == normRepoURL(b)
}

func normRepoURL(u string) string {
	u = strings.ToLower(strings.TrimSpace(u))
	u = strings.TrimSuffix(u, "/")
	return strings.TrimSuffix(u, ".git")
}
