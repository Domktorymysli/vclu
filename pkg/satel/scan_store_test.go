package satel

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestScanStoreRoundTrip(t *testing.T) {
	dir := t.TempDir()

	if saved, err := LoadScan(dir); saved != nil || err != nil {
		t.Fatalf("no file yet: got %v, %v; want nil, nil", saved, err)
	}

	at := time.Date(2026, 10, 4, 18, 51, 54, 0, time.UTC)
	res := &ScanResult{
		PanelType:  "INTEGRA 128-WRL LEON",
		Zones:      []ScanDevice{{Number: 2, Name: "PIR WIATROŁAP", Kind: 1}},
		Partitions: []ScanDevice{{Number: 1, Name: "Parter"}},
		Outputs:    []ScanDevice{{Number: 121, Name: "SŁABY AKUMULATOR"}},
	}
	if err := SaveScan(dir, res, at); err != nil {
		t.Fatalf("SaveScan: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, ScanFileName+".tmp")); !os.IsNotExist(err) {
		t.Errorf("temp file left behind: %v", err)
	}

	saved, err := LoadScan(dir)
	if err != nil || saved == nil {
		t.Fatalf("LoadScan: %v, %v", saved, err)
	}
	if !saved.ScannedAt.Equal(at) || saved.Result.PanelType != res.PanelType ||
		saved.Result.Zones[0].Name != "PIR WIATROŁAP" || saved.Result.Outputs[0].Number != 121 {
		t.Errorf("round trip mismatch: %+v", saved)
	}
}

func TestScanStoreCorruptFile(t *testing.T) {
	dir := t.TempDir()
	for _, content := range []string{"{not json", `{"scannedAt":"2026-10-04T18:51:54Z"}`} {
		if err := os.WriteFile(filepath.Join(dir, ScanFileName), []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
		if saved, err := LoadScan(dir); err == nil {
			t.Errorf("LoadScan(%q) = %v, want an error", content, saved)
		}
	}
}
