package satel

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"time"
)

// ScanFileName is the file in the data dir holding the last successful scan.
const ScanFileName = "satel_scan.json"

// SavedScan is the last successful scan, kept so the device picker can be
// edited later without rescanning (a scan takes over a minute on ETHM-1).
type SavedScan struct {
	ScannedAt time.Time   `json:"scannedAt"`
	Result    *ScanResult `json:"result"`
}

// SaveScan writes the scan result to dir atomically (temp file + rename).
func SaveScan(dir string, res *ScanResult, at time.Time) error {
	data, err := json.MarshalIndent(SavedScan{ScannedAt: at, Result: res}, "", "  ")
	if err != nil {
		return err
	}
	path := filepath.Join(dir, ScanFileName)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// LoadScan reads the last saved scan; (nil, nil) if none was saved yet.
func LoadScan(dir string) (*SavedScan, error) {
	data, err := os.ReadFile(filepath.Join(dir, ScanFileName))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var s SavedScan
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, err
	}
	if s.Result == nil {
		return nil, errors.New("satel: saved scan has no result")
	}
	return &s, nil
}
