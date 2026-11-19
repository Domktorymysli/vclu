package plugins

import (
	"fmt"
	"log"
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

// effectivePermissions: what the plugin may reach, and whether it is official (that
// names its events). API 1 keeps everything, as before permissions existed; API 2
// gets what it declares, and nothing beyond the internet without the field.
func (l *Loader) effectivePermissions(pluginID string, manifest PluginManifest) (Permissions, bool) {
	official := l.isOfficial(pluginID)
	if manifestAPI(manifest) < 2 {
		log.Printf("[PLUGINS] %s is on plugin API 1: lan, mqtt and modbus without a declaration (\"api\": 2 in plugin.json limits it to its permissions)", pluginID)
		return Permissions{LAN: true, MQTT: true, Modbus: true}, official
	}
	if manifest.Permissions != nil {
		return *manifest.Permissions, official
	}
	return Permissions{}, official
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
