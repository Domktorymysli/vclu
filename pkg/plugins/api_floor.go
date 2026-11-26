package plugins

import (
	"errors"
	"fmt"
	"log"

	"github.com/Domktorymysli/vclu/pkg/config"
)

// ErrAPIDowngrade: a version on a lower plugin API than the installed one; it would drop
// the limits of the newer API, so it is installed only from scratch.
var ErrAPIDowngrade = errors.New("lowers the plugin API")

// APIDowngrade is the error for an update from API from to API to.
func APIDowngrade(pluginID string, from, to int) error {
	return fmt.Errorf("%s: this version %w from %d to %d, which would drop its limits; uninstall it and install it again if you want it", pluginID, ErrAPIDowngrade, from, to)
}

// installedEntry: the plugin's entry in .vclu.json, if installed.
func (l *Loader) installedEntry(pluginID string) (config.InstalledPlugin, bool) {
	mc, err := config.ReadMasterConfig(l.serverDir)
	if err != nil || mc == nil || mc.Plugins == nil {
		return config.InstalledPlugin{}, false
	}
	for _, inst := range mc.Plugins.Installed {
		if inst.ID == pluginID {
			return inst, true
		}
	}
	return config.InstalledPlugin{}, false
}

// APIFloor: the lowest plugin API an update of an installed plugin may have, the recorded
// one or, for an entry from before it was recorded, that of the files on disk; 0 when not installed.
func (l *Loader) APIFloor(pluginID string) int {
	inst, ok := l.installedEntry(pluginID)
	if !ok {
		return 0
	}
	if inst.API > 0 {
		return inst.API
	}
	if m, err := l.diskManifest(pluginID); err == nil {
		return manifestAPI(m)
	}
	return 0
}

// DiskAPI: the plugin API of the files on disk.
func (l *Loader) DiskAPI(pluginID string) (int, error) {
	m, err := l.diskManifest(pluginID)
	if err != nil {
		return 0, err
	}
	return manifestAPI(m), nil
}

// recordAPI: the installed entry keeps the highest plugin API it ran on.
func (l *Loader) recordAPI(pluginID string, api int) {
	err := config.UpdateMasterConfig(l.serverDir, func(mc *config.VCLUConfig) error {
		if mc.Plugins == nil {
			return nil
		}
		for i := range mc.Plugins.Installed {
			if inst := &mc.Plugins.Installed[i]; inst.ID == pluginID && inst.API < api {
				inst.API = api
			}
		}
		return nil
	})
	if err != nil {
		log.Printf("[PLUGINS] WARNING: plugin API of %s not recorded: %v", pluginID, err)
	}
}

// pinOlderEntries: an entry from before addresses and APIs were recorded gets its repository's
// address (unless its files came from another) and the API of its files on the first load.
func (l *Loader) pinOlderEntries(mc *config.VCLUConfig) {
	type pin struct {
		url string
		api int
	}
	pins := map[string]pin{}
	for _, inst := range mc.Plugins.Installed {
		if !validPluginID(inst.ID) {
			continue
		}
		var p pin
		if inst.RepositoryURL == "" && inst.Repository != "" {
			if repo := l.findRepository(mc.Plugins.Repositories, inst.Repository); repo != nil {
				if ok, _ := l.FromSource(inst, *repo); ok {
					p.url = normRepoURL(repo.URL)
				}
			}
		}
		if inst.API == 0 {
			if api, err := l.DiskAPI(inst.ID); err == nil && api > 0 {
				p.api = api
			}
		}
		if p != (pin{}) {
			pins[inst.ID] = p
		}
	}
	if len(pins) == 0 {
		return
	}
	err := config.UpdateMasterConfig(l.serverDir, func(mc *config.VCLUConfig) error {
		if mc.Plugins == nil {
			return nil
		}
		for i := range mc.Plugins.Installed {
			inst := &mc.Plugins.Installed[i]
			p, ok := pins[inst.ID]
			if !ok {
				continue
			}
			if inst.RepositoryURL == "" && p.url != "" {
				inst.RepositoryURL = p.url
			}
			if p.api > inst.API {
				inst.API = p.api
			}
		}
		return nil
	})
	if err != nil {
		log.Printf("[PLUGINS] WARNING: repository addresses of installed plugins not recorded: %v", err)
	}
}
