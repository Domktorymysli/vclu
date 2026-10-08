package metrics

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestSanitizePath(t *testing.T) {
	tests := []struct {
		input, expected string
	}{
		{"salda:outsideAir", "salda.outsideAir"},
		{"simple", "simple"},
		{"a/b", "a.b"},
		{"a\\b", "a.b"},
		{"a..b", "ab"},
	}
	for _, tt := range tests {
		got := sanitizePath(tt.input)
		if got != tt.expected {
			t.Errorf("sanitizePath(%q) = %q, want %q", tt.input, got, tt.expected)
		}
	}
}

func TestAppendAndReadFile(t *testing.T) {
	dir := t.TempDir()
	now := time.Now().Unix()

	samples := []Sample{
		{Timestamp: now - 10, Value: 21.3},
		{Timestamp: now - 5, Value: 21.4},
		{Timestamp: now, Value: 21.5},
	}

	appendToFile(dir, "temp:outside", samples)

	// Read all
	got := readFiles(dir, "temp:outside", now-20)
	if len(got) != 3 {
		t.Fatalf("expected 3 samples, got %d", len(got))
	}
	if got[0].Value != 21.3 {
		t.Errorf("expected first value 21.3, got %f", got[0].Value)
	}

	// Read with since filter
	got = readFiles(dir, "temp:outside", now-4)
	if len(got) != 1 {
		t.Fatalf("expected 1 sample since %d, got %d", now-4, len(got))
	}
}

func TestAppendMultipleBatches(t *testing.T) {
	dir := t.TempDir()
	now := time.Now().Unix()

	appendToFile(dir, "x", []Sample{{Timestamp: now, Value: 1.0}})
	appendToFile(dir, "x", []Sample{{Timestamp: now + 1, Value: 2.0}})

	got := readFiles(dir, "x", 0)
	if len(got) != 2 {
		t.Fatalf("expected 2 samples after two appends, got %d", len(got))
	}
}

func TestReadNonexistentMetric(t *testing.T) {
	dir := t.TempDir()
	got := readFiles(dir, "nope", 0)
	if len(got) != 0 {
		t.Errorf("expected empty result for nonexistent metric, got %d", len(got))
	}
}

func TestRotate(t *testing.T) {
	dir := t.TempDir()

	// Create metric directory with old and new files
	metricDir := filepath.Join(dir, "temp")
	os.MkdirAll(metricDir, 0755)

	old := time.Now().AddDate(0, 0, -10).Format("2006-01-02") + ".ts"
	recent := time.Now().Format("2006-01-02") + ".ts"

	os.WriteFile(filepath.Join(metricDir, old), []byte("1 21.0\n"), 0644)
	os.WriteFile(filepath.Join(metricDir, recent), []byte("2 22.0\n"), 0644)

	rotate(dir, 7)

	// Old file should be deleted
	if _, err := os.Stat(filepath.Join(metricDir, old)); !os.IsNotExist(err) {
		t.Error("old file should have been deleted by rotation")
	}

	// Recent file should remain
	if _, err := os.Stat(filepath.Join(metricDir, recent)); err != nil {
		t.Error("recent file should not have been deleted")
	}
}

func TestFlushAndQueryFromDisk(t *testing.T) {
	dir := t.TempDir()
	c := NewCollector(dir, WithRingSize(3))

	c.Register("m", "", 1)

	// Ingest 5 samples — ring only holds 3
	for i := 0; i < 5; i++ {
		c.Ingest([]CollectResult{{Path: "m", Value: float64(i * 10)}})
	}

	// Flush to disk (writes all 5 from writeBuf)
	c.flush()

	// Ring has [20, 30, 40], disk has [0, 10, 20, 30, 40]
	// Query since 0 should merge and return all 5 (dedup)
	samples := c.Query("m", 0)
	if len(samples) != 5 {
		t.Fatalf("expected 5 samples after flush+query, got %d", len(samples))
	}
}

func TestDailyFileName(t *testing.T) {
	tm := time.Date(2026, 3, 5, 14, 30, 0, 0, time.UTC)
	got := dailyFileName(tm)
	if got != "2026-03-05.ts" {
		t.Errorf("expected 2026-03-05.ts, got %s", got)
	}
}
