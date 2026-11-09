package history

import (
	"bytes"
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// captureLog collects the standard logger's output for the test.
func captureLog(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	prev, flags := log.Writer(), log.Flags()
	log.SetOutput(&buf)
	t.Cleanup(func() { log.SetOutput(prev); log.SetFlags(flags) })
	return &buf
}

func TestLowDisk_RotatesBeyondSevenDaysOnly(t *testing.T) {
	dir := t.TempDir()
	c := &clock{t: time.Date(2026, 11, 9, 12, 0, 0, 0, time.UTC)}
	s := diskStoreAt(t, dir, c) // retentionDays 90
	for i := 0; i <= 20; i++ {
		day := c.t.AddDate(0, 0, -i).Format(dayLayout)
		if err := os.WriteFile(filepath.Join(dir, day+fileSuffix), []byte("{}\n"), filePerm); err != nil {
			t.Fatal(err)
		}
	}
	logs := captureLog(t)

	if err := s.Flush(); err != nil {
		t.Fatal(err)
	}
	if n := len(s.disk.days()); n != 21 {
		t.Fatalf("disk not low: %d files left, want all 21", n)
	}

	s.SetDiskLow(true)
	if err := s.Flush(); err != nil {
		t.Fatal(err)
	}
	days := s.disk.days()
	if len(days) != 8 || days[0] != "2026-11-02" || days[7] != "2026-11-09" {
		t.Fatalf("left %v, want today and the 7 days before it", days)
	}
	if got := strings.Count(logs.String(), "disk nearly full"); got != 1 {
		t.Fatalf("logged %d times, want once:\n%s", got, logs)
	}

	// the next day the 8th-oldest file goes without a new line; today's stays
	c.t = c.t.AddDate(0, 0, 1)
	if err := s.Flush(); err != nil {
		t.Fatal(err)
	}
	if days := s.disk.days(); len(days) != 7 || days[0] != "2026-11-03" {
		t.Fatalf("next day left %v", days)
	}
	if got := strings.Count(logs.String(), "disk nearly full"); got != 1 {
		t.Errorf("logged %d times, want once", got)
	}

	// a new low-disk episode is logged again
	s.SetDiskLow(false)
	s.SetDiskLow(true)
	c.t = c.t.AddDate(0, 0, 1)
	if err := s.Flush(); err != nil {
		t.Fatal(err)
	}
	if got := strings.Count(logs.String(), "disk nearly full"); got != 2 {
		t.Errorf("second episode: logged %d times, want 2", got)
	}
}

func TestLowDisk_NeverTodayNorWithinRetention(t *testing.T) {
	dir := t.TempDir()
	c := &clock{t: time.Date(2026, 11, 9, 0, 30, 0, 0, time.UTC)}
	cfg := DefaultConfig()
	cfg.Dir, cfg.RetentionDays = dir, 3
	s := New(cfg, c.now)
	for i := 0; i <= 5; i++ {
		day := c.t.AddDate(0, 0, -i).Format(dayLayout)
		if err := os.WriteFile(filepath.Join(dir, day+fileSuffix), []byte("{}\n"), filePerm); err != nil {
			t.Fatal(err)
		}
	}
	s.SetDiskLow(true)
	logs := captureLog(t)
	if err := s.Flush(); err != nil {
		t.Fatal(err)
	}
	// a shorter retention wins; low disk never keeps less
	if days := s.disk.days(); len(days) != 4 || days[3] != "2026-11-09" {
		t.Fatalf("left %v", days)
	}
	if strings.Contains(logs.String(), "disk nearly full") {
		t.Error("nothing removed for low disk, yet logged")
	}
}

func TestWrittenLast24h(t *testing.T) {
	dir := t.TempDir()
	c := &clock{t: time.Date(2026, 11, 9, 6, 0, 0, 0, time.UTC)}
	s := diskStoreAt(t, dir, c)
	write := func(day string, n int) {
		if err := os.WriteFile(filepath.Join(dir, day+fileSuffix), bytes.Repeat([]byte("x"), n), filePerm); err != nil {
			t.Fatal(err)
		}
	}
	if got := s.WrittenLast24h(); got != 0 {
		t.Fatalf("empty: %d", got)
	}
	write("2026-11-09", 100)
	if got := s.WrittenLast24h(); got != 100 {
		t.Fatalf("today only: %d, want 100", got)
	}
	write("2026-11-08", 2400)
	write("2026-11-07", 99999)
	// 06:00: today plus the last 18 h of yesterday
	if got := s.WrittenLast24h(); got != 100+1800 {
		t.Fatalf("got %d, want %d", got, 100+1800)
	}
	if got := New(DefaultConfig(), c.now).WrittenLast24h(); got != 0 {
		t.Errorf("memory only: %d", got)
	}
}
