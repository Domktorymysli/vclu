package config

import (
	"os"
	"strings"
)

// UpdateCheckEnv set to "off" turns the update check off, whatever .vclu.json says.
const UpdateCheckEnv = "VCLU_UPDATE_CHECK"

// VCLUUpdateCheckConfig is the "updateCheck" section; a missing section or field means on.
type VCLUUpdateCheckConfig struct {
	Enabled *bool `json:"enabled"`
}

// UpdateCheckForcedOff reports VCLU_UPDATE_CHECK=off (CI, test rigs, demo images).
func UpdateCheckForcedOff() bool {
	return strings.EqualFold(strings.TrimSpace(os.Getenv(UpdateCheckEnv)), "off")
}

// UpdateCheckOn is the file's setting alone; a nil config means on.
func (c *VCLUConfig) UpdateCheckOn() bool {
	return c == nil || c.UpdateCheck == nil || c.UpdateCheck.Enabled == nil || *c.UpdateCheck.Enabled
}

// UpdateCheckEnabled reports whether vCLU may ask for new versions now. An unreadable
// .vclu.json counts as off: nothing is sent while the setting is unknown.
func UpdateCheckEnabled(cluDir string) bool {
	if UpdateCheckForcedOff() {
		return false
	}
	mc, err := ReadMasterConfig(cluDir)
	if err != nil {
		return false
	}
	return mc.UpdateCheckOn()
}
