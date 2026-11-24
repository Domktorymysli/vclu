package plugins

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// SecretsFile holds the secret fields of plugin forms, apart from .vclu.json and from
// plugin storage: a plugin reads only its own, the panel never shows them.
const SecretsFile = "plugin_secrets.json"

var secretsMu sync.Mutex

func secretsPath(serverDir string) string { return filepath.Join(serverDir, SecretsFile) }

func readSecrets(serverDir string) (map[string]map[string]string, error) {
	all := map[string]map[string]string{}
	data, err := os.ReadFile(secretsPath(serverDir))
	if errors.Is(err, os.ErrNotExist) {
		return all, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(data, &all); err != nil {
		return nil, fmt.Errorf("%s: %w", SecretsFile, err)
	}
	return all, nil
}

// PluginSecrets returns the secrets of one plugin.
func PluginSecrets(serverDir, pluginID string) (map[string]string, error) {
	secretsMu.Lock()
	defer secretsMu.Unlock()
	all, err := readSecrets(serverDir)
	if err != nil {
		return nil, err
	}
	return all[pluginID], nil
}

// SetPluginSecrets stores changed secrets of a plugin: "" removes one; others are kept.
func SetPluginSecrets(serverDir, pluginID string, changed map[string]string) error {
	if len(changed) == 0 {
		return nil
	}
	return updateSecrets(serverDir, func(all map[string]map[string]string) {
		mine := all[pluginID]
		if mine == nil {
			mine = map[string]string{}
		}
		for k, v := range changed {
			if v == "" {
				delete(mine, k)
			} else {
				mine[k] = v
			}
		}
		if len(mine) == 0 {
			delete(all, pluginID)
		} else {
			all[pluginID] = mine
		}
	})
}

// DeletePluginSecrets forgets a plugin's secrets (uninstall).
func DeletePluginSecrets(serverDir, pluginID string) error {
	return updateSecrets(serverDir, func(all map[string]map[string]string) { delete(all, pluginID) })
}

func updateSecrets(serverDir string, change func(map[string]map[string]string)) error {
	secretsMu.Lock()
	defer secretsMu.Unlock()
	all, err := readSecrets(serverDir)
	if err != nil {
		return err
	}
	change(all)
	data, err := json.MarshalIndent(all, "", "  ")
	if err != nil {
		return err
	}
	tmp := secretsPath(serverDir) + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, secretsPath(serverDir))
}
