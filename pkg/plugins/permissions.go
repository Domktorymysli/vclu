package plugins

import (
	"log"
	"strings"

	"github.com/Domktorymysli/vclu/pkg/config"
)

// Permissions are what a plugin may reach beyond HTTP to the internet, declared in
// plugin.json as "permissions": {"lan": true, "mqtt": true, "modbus": true}.
type Permissions struct {
	LAN    bool `json:"lan,omitempty"`    // HTTP to local addresses (the house's devices)
	MQTT   bool `json:"mqtt,omitempty"`   // subscribe and publish on the broker
	Modbus bool `json:"modbus,omitempty"` // Modbus requests, reads and writes
}

// effectivePermissions: what the manifest declares, and whether the plugin is
// official (that also names its events). A plugin without the field keeps everything
// only when it is official (installed from the official repository, or by hand in the
// @vclu namespace), until the official manifests declare it.
func (l *Loader) effectivePermissions(pluginID string, manifest PluginManifest) (Permissions, bool) {
	official := l.isOfficial(pluginID)
	if manifest.Permissions != nil {
		return *manifest.Permissions, official
	}
	if official {
		log.Printf("[PLUGINS] WARNING: %s declares no permissions in plugin.json; as an official plugin it keeps lan, mqtt and modbus", pluginID)
		return Permissions{LAN: true, MQTT: true, Modbus: true}, true
	}
	log.Printf("[PLUGINS] %s declares no permissions in plugin.json: HTTP to the internet only", pluginID)
	return Permissions{}, false
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
