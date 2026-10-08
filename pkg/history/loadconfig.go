package history

import (
	"path/filepath"
	"time"

	"github.com/Domktorymysli/vclu/pkg/config"
)

// LoadConfig reads the history section of .vclu.json in cluDir; absent
// fields keep defaults. Files go to cluDir/history. On a read error the
// defaults are returned with the error.
func LoadConfig(cluDir string) (Config, error) {
	cfg := DefaultConfig()
	cfg.Dir = filepath.Join(cluDir, "history")
	mc, err := config.ReadMasterConfig(cluDir)
	if err != nil || mc == nil || mc.History == nil {
		return cfg, err
	}
	h := mc.History
	if h.Enabled != nil {
		cfg.Enabled = *h.Enabled
	}
	cfg.MemoryHours = h.MemoryHours
	cfg.MaxEntriesPerObject = h.MaxEntriesPerObject
	cfg.MaxEntriesTotal = h.MaxEntriesTotal
	if h.DiskEnabled != nil {
		cfg.DiskEnabled = *h.DiskEnabled
	}
	cfg.RetentionDays = h.RetentionDays
	cfg.MaxDiskMB = h.MaxDiskMB
	cfg.FlushInterval = time.Duration(h.FlushIntervalSeconds) * time.Second
	return normalize(cfg), nil
}
