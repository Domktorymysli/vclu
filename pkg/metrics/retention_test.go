package metrics

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// A line the flush is still writing (no newline yet) is not read as a sample.
func TestReadSingleFileSkipsHalfWrittenLine(t *testing.T) {
	path := filepath.Join(t.TempDir(), "2026-10-30.ts")
	if err := os.WriteFile(path, []byte("100 1.5\n200 2.5\n300 12"), 0644); err != nil {
		t.Fatal(err)
	}
	got := readSingleFile(path, 0)
	if len(got) != 2 || got[1].Value != 2.5 {
		t.Errorf("samples = %v, want the two complete lines", got)
	}
	if err := os.WriteFile(path, []byte("100 1"), 0644); err != nil {
		t.Fatal(err)
	}
	if got := readSingleFile(path, 0); len(got) != 0 {
		t.Errorf("one half-written line = %v", got)
	}
}

// Kept 10 digits: a meter at 123456.78 kWh keeps its hundredths.
func TestAppendKeepsMeterDecimals(t *testing.T) {
	dir := t.TempDir()
	now := time.Now().Unix()
	appendToFile(dir, "e:m", []Sample{{Timestamp: now, Value: 123456.78}})
	if got := readFiles(dir, "e:m", now-1); len(got) != 1 || got[0].Value != 123456.78 {
		t.Errorf("read back %v", got)
	}
}

func TestRetainDays(t *testing.T) {
	for in, want := range map[int]int{0: 7, -1: 7, 1: 1, 31: 31, 366: 366, 367: 7} {
		if got := NewCollector(t.TempDir(), WithRetainDays(in)).RetainDays(); got != want {
			t.Errorf("WithRetainDays(%d) = %d, want %d", in, got, want)
		}
	}
	if got := NewCollector(t.TempDir()).RetainDays(); got != DefaultRetainDays {
		t.Errorf("default = %d", got)
	}
}

// A raw query reaches back at most the retention and answers with at most MaxPoints samples.
func TestQueryRangeCapsRetentionAndPoints(t *testing.T) {
	dir := t.TempDir()
	c := NewCollector(dir, WithRingSize(10), WithRetainDays(2))
	c.Register("t:x", "", 60)
	now := time.Now()
	var samples []Sample
	for ts := now.Add(-5 * 24 * time.Hour); ts.Before(now); ts = ts.Add(time.Minute) {
		samples = append(samples, Sample{Timestamp: ts.Unix(), Value: 1})
	}
	appendToFile(dir, "t:x", samples)
	got := c.QueryRange("t:x", 30*24*time.Hour)
	if len(got) == 0 || len(got) > MaxPoints {
		t.Fatalf("%d points, want 1..%d", len(got), MaxPoints)
	}
	if oldest := now.Add(-2*24*time.Hour - time.Minute).Unix(); got[0].Timestamp < oldest {
		t.Errorf("oldest %v is past the 2-day retention", time.Unix(got[0].Timestamp, 0))
	}
	if short := c.QueryRange("t:x", 10*time.Minute); len(short) < 9 || len(short) > 11 {
		t.Errorf("10 minutes = %d samples, want them raw", len(short))
	}
}

func TestDownsample(t *testing.T) {
	var in []Sample
	for i := 0; i < 10; i++ {
		in = append(in, Sample{Timestamp: int64(i), Value: float64(i)})
	}
	got := Downsample(in, 4)
	if fmt.Sprint(got) != "[{0 0.5} {2 3} {5 5.5} {7 8}]" {
		t.Errorf("Downsample = %v", got)
	}
	if len(Downsample(in, 20)) != 10 {
		t.Error("short series changed")
	}
}
