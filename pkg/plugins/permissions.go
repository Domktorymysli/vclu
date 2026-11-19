package plugins

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
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

// Permissions are what a plugin may reach beyond HTTP to the internet, declared in
// plugin.json as "permissions": {"lan": true, "mqtt": true, "modbus": true}.
type Permissions struct {
	LAN    bool `json:"lan,omitempty"`    // HTTP to local addresses (the house's devices)
	MQTT   bool `json:"mqtt,omitempty"`   // subscribe and publish on the broker
	Modbus bool `json:"modbus,omitempty"` // Modbus requests, reads and writes
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
// permissions existed; API 2 for what it declares, nothing beyond the internet without the field.
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
	if p.LAN {
		out = append(out, "lan")
	}
	if p.MQTT {
		out = append(out, "mqtt")
	}
	if p.Modbus {
		out = append(out, "modbus")
	}
	return out
}

// allowedBy: the part of p the grants allow, and what p asks for beyond them. Nil grants
// (installed by hand, or before grants existed) allow what is asked.
func (p Permissions) allowedBy(g *config.PluginGrants) (allowed, pending Permissions) {
	if g == nil {
		return p, Permissions{}
	}
	allowed = Permissions{LAN: p.LAN && g.LAN != nil, MQTT: p.MQTT && g.MQTT != nil, Modbus: p.Modbus && g.Modbus != nil}
	pending = Permissions{LAN: p.LAN && g.LAN == nil, MQTT: p.MQTT && g.MQTT == nil, Modbus: p.Modbus && g.Modbus == nil}
	return allowed, pending
}

// Grants turns permissions into what the user allows when installing a plugin: each in full.
func (p Permissions) Grants() *config.PluginGrants {
	g := &config.PluginGrants{}
	if p.LAN {
		g.LAN = &config.LANGrant{}
	}
	if p.MQTT {
		g.MQTT = &config.MQTTGrant{}
	}
	if p.Modbus {
		g.Modbus = &config.ModbusGrant{}
	}
	return g
}

// Limits narrow allowed permissions: nil lists allow any host or topic, an empty one none.
type Limits struct {
	LANHosts      []string `json:"lanHosts,omitzero"`
	MQTTSubscribe []string `json:"mqttSubscribe,omitzero"`
	MQTTPublish   []string `json:"mqttPublish,omitzero"`
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
	if g.MQTT != nil {
		l.MQTTSubscribe, l.MQTTPublish = g.MQTT.Subscribe, g.MQTT.Publish
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
		if allowed, _ := requested.allowedBy(grants); allowed != requested || limitsOf(grants).narrows() {
			log.Printf("[PLUGINS] WARNING: %s is on plugin API 1: its HTTP and MQTT globals are not limited by the permissions allowed in its settings", pluginID)
		}
	}
	allowed, pending := requested.allowedBy(grants)
	if names := pending.Names(); len(names) > 0 {
		log.Printf("[PLUGINS] %s asks for %s, not allowed yet: it runs without it until allowed in the plugin's settings", pluginID, strings.Join(names, ", "))
	}
	return allowed, limitsOf(grants), official
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

// RequestedPermissions: what the plugin's files on disk ask for.
func (l *Loader) RequestedPermissions(pluginID string) (Permissions, error) {
	data, err := os.ReadFile(filepath.Join(l.resolvePluginDir(pluginID), "plugin.json"))
	if err != nil {
		return Permissions{}, err
	}
	var manifest PluginManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return Permissions{}, err
	}
	return requestedPermissions(manifest), nil
}

// PendingPermissions: what the installed plugin asks for and the user has not allowed.
func (l *Loader) PendingPermissions(p config.InstalledPlugin) []string {
	requested, err := l.RequestedPermissions(p.ID)
	if err != nil {
		return nil
	}
	_, pending := requested.allowedBy(p.Grants)
	return pending.Names()
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
				return repo.Default && sameRepoURL(repo.URL, config.OfficialPluginsRepoURL)
			}
		}
		return false
	}
	return false
}

func sameRepoURL(a, b string) bool {
	norm := func(u string) string {
		u = strings.ToLower(strings.TrimSpace(u))
		u = strings.TrimSuffix(u, "/")
		return strings.TrimSuffix(u, ".git")
	}
	return norm(a) == norm(b)
}
