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

// installedEntry: the plugin's entry in .vclu.json, if installed; an error when the file
// cannot be read, which callers treat as the narrower case, never as "not installed".
func (l *Loader) installedEntry(pluginID string) (config.InstalledPlugin, bool, error) {
	mc, err := config.ReadMasterConfig(l.serverDir)
	if err != nil {
		return config.InstalledPlugin{}, false, err
	}
	if mc == nil || mc.Plugins == nil {
		return config.InstalledPlugin{}, false, nil
	}
	for _, inst := range mc.Plugins.Installed {
		if inst.ID == pluginID {
			return inst, true, nil
		}
	}
	return config.InstalledPlugin{}, false, nil
}

// APIFloor: the lowest plugin API an update of an installed plugin may have, the recorded
// one or, for an entry from before it was recorded, that of the files on disk; 0 when not installed.
// An unreadable configuration is an error: the floor is not known, so nothing is downloaded.
func (l *Loader) APIFloor(pluginID string) (int, error) {
	inst, ok, err := l.installedEntry(pluginID)
	if err != nil {
		return 0, fmt.Errorf("plugin %s: %w", pluginID, err)
	}
	if !ok {
		return 0, nil
	}
	if inst.API > 0 {
		return inst.API, nil
	}
	if m, err := l.diskManifest(pluginID); err == nil {
		return manifestAPI(m), nil
	}
	return 0, nil
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

// recordStarted: the version whose init succeeded, the previousVersion of the next update.
func (l *Loader) recordStarted(pluginID, version string) {
	err := config.UpdateMasterConfig(l.serverDir, func(mc *config.VCLUConfig) error {
		if mc.Plugins == nil {
			return nil
		}
		for i := range mc.Plugins.Installed {
			if inst := &mc.Plugins.Installed[i]; inst.ID == pluginID {
				inst.StartedVersion = version
			}
		}
		return nil
	})
	if err != nil {
		log.Printf("[PLUGINS] WARNING: started version of %s not recorded: %v", pluginID, err)
	}
}

// pinOlderEntries: an entry from before addresses and APIs were recorded gets its repository's
// address (unless its files came from another) and the API of its files on the first load; one
// from before started versions were recorded, the version of its files (its next update then
// tells onInit the version before it, even if it does not run before that).
func (l *Loader) pinOlderEntries(mc *config.VCLUConfig) {
	type pin struct {
		url     string
		api     int
		started string
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
		if inst.StartedVersion == "" {
			if m, err := l.diskManifest(inst.ID); err == nil && m.Version != "" {
				p.started = m.Version
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
			if inst.StartedVersion == "" && p.started != "" {
				inst.StartedVersion = p.started
			}
		}
		return nil
	})
	if err != nil {
		log.Printf("[PLUGINS] WARNING: repository addresses of installed plugins not recorded: %v", err)
	}
}
