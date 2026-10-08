package history

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func diskStoreAt(t *testing.T, dir string, c *clock) *Store {
	t.Helper()
	cfg := DefaultConfig()
	cfg.Dir = dir
	return New(cfg, c.now)
}

func TestDisk_WriteRestoreAcrossRestart(t *testing.T) {
	dir := t.TempDir()
	c := &clock{t: time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)}
	s := diskStoreAt(t, dir, c)
	s.Record(Entry{Path: "zone", Old: 0, Value: 1, Origin: "physical", Actor: "device:satel"})
	c.t = c.t.Add(time.Minute)
	s.Record(Entry{Path: "zone", Old: 1, Value: 0})
	if st := s.Status(); st.PendingWrites != 2 {
		t.Fatalf("pending %d", st.PendingWrites)
	}
	if err := s.Close(time.Second); err != nil {
		t.Fatal(err)
	}

	fi, err := os.Stat(filepath.Join(dir, "2026-10-07.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0600 {
		t.Errorf("file mode %v, want 0600", fi.Mode().Perm())
	}
	if di, _ := os.Stat(dir); di.Mode().Perm() != 0700 && di.Mode().Perm() != 0755 {
		t.Logf("dir mode %v", di.Mode().Perm()) // t.TempDir creates it; we only create missing dirs
	}

	c.t = c.t.Add(time.Hour)
	s2 := diskStoreAt(t, dir, c)
	got := s2.Query(Query{Path: "zone"})
	if values(got) != "1>0 0>1 " || got[1].Actor != "device:satel" {
		t.Fatalf("restored: %s %+v", values(got), got)
	}
	// the last value before the restart is known: a first reading equal to it is no change
	s2.Record(Entry{Path: "zone", Old: nil, Value: 0})
	if len(s2.Query(Query{Path: "zone"})) != 2 {
		t.Error("first reading after restart recorded although nothing changed")
	}
	s2.Record(Entry{Path: "zone", Old: nil, Value: 1})
	if last, _ := s2.Last("zone"); last.Old != 0 || last.Actor != "system:startup" {
		t.Errorf("change while vCLU was down: %+v", last)
	}
}

func TestDisk_TornLastLineIsSkipped(t *testing.T) {
	dir := t.TempDir()
	c := &clock{t: time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)}
	s := diskStoreAt(t, dir, c)
	s.Record(Entry{Path: "a", Old: 0, Value: 1})
	s.Flush()
	f, _ := os.OpenFile(filepath.Join(dir, "2026-10-07.jsonl"), os.O_APPEND|os.O_WRONLY, 0600)
	f.WriteString(`{"schema":1,"id":"x-9","path":"a","val`) // power cut mid-write
	f.Close()
	s2 := diskStoreAt(t, dir, c)
	if got := s2.Query(Query{Path: "a"}); len(got) != 1 {
		t.Fatalf("got %d entries", len(got))
	}
}

func TestDisk_BatchSplitAtMidnight(t *testing.T) {
	dir := t.TempDir()
	c := &clock{t: time.Date(2026, 10, 7, 23, 59, 59, 0, time.UTC)}
	s := diskStoreAt(t, dir, c)
	s.Record(Entry{Path: "a", Old: 0, Value: 1})
	c.t = c.t.Add(2 * time.Second)
	s.Record(Entry{Path: "a", Old: 1, Value: 0})
	s.Flush()
	for _, day := range []string{"2026-10-07", "2026-10-08"} {
		data, err := os.ReadFile(filepath.Join(dir, day+".jsonl"))
		if err != nil || strings.Count(string(data), "\n") != 1 {
			t.Errorf("%s: %q %v", day, data, err)
		}
	}
}

func TestDisk_Rotation(t *testing.T) {
	dir := t.TempDir()
	write := func(day string, size int) {
		os.WriteFile(filepath.Join(dir, day+".jsonl"), []byte(strings.Repeat("x", size)), 0600)
	}
	write("2026-06-01", 10) // older than 90 days
	write("2026-10-01", 600*1024)
	write("2026-10-02", 600*1024)
	write("2026-10-07", 2*1024*1024) // today, over the limit by itself
	d := &diskStore{dir: dir}
	now := time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)

	removed, over := d.rotate(now, 90, 1024*1024)
	if removed != 3 || !over {
		t.Fatalf("removed %d over %v", removed, over)
	}
	if got := d.days(); len(got) != 1 || got[0] != "2026-10-07" {
		t.Fatalf("left %v: today must never be deleted", got)
	}

	write("2026-10-05", 100)
	write("2026-10-06", 100)
	os.Remove(filepath.Join(dir, "2026-10-07.jsonl"))
	if removed, _ := d.rotate(now, 90, 1024*1024); removed != 0 {
		t.Errorf("files within both limits removed: %d", removed)
	}
}

func TestDisk_FailedWriteIsRetriedAndBounded(t *testing.T) {
	parent := t.TempDir()
	dir := filepath.Join(parent, "history")
	c := &clock{t: time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)}
	s := diskStoreAt(t, dir, c)
	os.WriteFile(dir, []byte("not a directory"), 0600) // writes fail
	s.Record(Entry{Path: "a", Old: 0, Value: 1})
	if err := s.Flush(); err == nil {
		t.Fatal("expected a write error")
	}
	st := s.Status()
	if st.PendingWrites != 1 || st.LastFlushError == "" {
		t.Fatalf("failed batch must stay pending with the error shown: %+v", st)
	}
	for i := 0; i < maxPending+5; i++ {
		s.Record(Entry{Path: "b", Old: i, Value: i + 1})
	}
	if st := s.Status(); st.PendingWrites != maxPending || st.LostEntries != 6 {
		t.Fatalf("pending %d lost %d", st.PendingWrites, st.LostEntries)
	}
	if got := len(s.Query(Query{Path: "a"})); got != 1 {
		t.Error("memory must keep working while the disk fails")
	}

	os.Remove(dir) // the disk comes back
	if err := s.Flush(); err != nil {
		t.Fatal(err)
	}
	if st := s.Status(); st.PendingWrites != 0 || st.LastFlushError != "" || st.LostEntries != 6 {
		t.Fatalf("after recovery: %+v", st)
	}
}

// A huge value is replaced by a marker when recorded: it is neither kept in
// memory nor dropped from the history, and two different huge values differ.
func TestDisk_OversizedValueBounded(t *testing.T) {
	dir := t.TempDir()
	c := &clock{t: time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)}
	s := diskStoreAt(t, dir, c)
	s.Record(Entry{Path: "big", Old: "", Value: strings.Repeat("x", 70*1024)})
	s.Record(Entry{Path: "big", Old: strings.Repeat("x", 70*1024), Value: strings.Repeat("y", 70*1024)})
	got := s.Query(Query{Path: "big"})
	if len(got) != 2 {
		t.Fatalf("entries %d, want 2", len(got))
	}
	v, _ := got[0].Value.(string)
	if !strings.HasPrefix(v, "<value too large: ") || len(v) > 100 || got[0].Value == got[1].Value {
		t.Fatalf("marker %q / %q", v, got[1].Value)
	}
	s.Flush()
	if st := s.Status(); st.SkippedRecords != 0 {
		t.Fatalf("skipped %d", st.SkippedRecords)
	}
	s2 := diskStoreAt(t, dir, c)
	if len(s2.Query(Query{Path: "big"})) != 2 {
		t.Error("bounded entries must be restored")
	}
}

// Restore holds at most MaxEntriesTotal entries however big the window is,
// and still knows the last value of an object seen only early in the window.
func TestDisk_RestoreBoundedByLimits(t *testing.T) {
	dir := t.TempDir()
	c := &clock{t: time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)}
	s := diskStoreAt(t, dir, c)
	s.Record(Entry{Path: "rare", Old: 0, Value: 1})
	for i := 0; i < 3000; i++ {
		c.t = c.t.Add(time.Second)
		s.Record(Entry{Path: fmt.Sprintf("busy%d", i%10), Old: i, Value: i + 1})
	}
	s.Close(time.Second)

	cfg := DefaultConfig()
	cfg.Dir, cfg.MaxEntriesPerObject, cfg.MaxEntriesTotal = dir, 100, 500
	s2 := New(cfg, c.now)
	if st := s2.Status(); st.MemoryEntries != 500 {
		t.Fatalf("restored %d entries, want 500", st.MemoryEntries)
	}
	// "rare" fell out of the tail but its value is known: 0 -> 1 is no change
	s2.Record(Entry{Path: "rare", Old: 0, Value: 1})
	if got := len(s2.Query(Query{Path: "rare"})); got != 0 {
		t.Errorf("rare: %d entries, want 0", got)
	}
}

// A corrupt line of several MB is skipped without stopping the read.
func TestDisk_HugeCorruptLineSkipped(t *testing.T) {
	dir := t.TempDir()
	c := &clock{t: time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)}
	s := diskStoreAt(t, dir, c)
	s.Record(Entry{Path: "a", Old: 0, Value: 1})
	s.Flush()
	f, _ := os.OpenFile(filepath.Join(dir, "2026-10-07.jsonl"), os.O_APPEND|os.O_WRONLY, 0600)
	f.WriteString(strings.Repeat("z", 5*1024*1024) + "\n")
	f.Close()
	s.Record(Entry{Path: "a", Old: 1, Value: 0})
	s.Flush()
	s2 := diskStoreAt(t, dir, c)
	if got := len(s2.Query(Query{Path: "a"})); got != 2 {
		t.Fatalf("entries %d, want 2", got)
	}
}

func TestDisk_ClearAndApply(t *testing.T) {
	dir := t.TempDir()
	c := &clock{t: time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)}
	s := diskStoreAt(t, dir, c)
	for i := 0; i < 50; i++ {
		s.Record(Entry{Path: "a", Old: i, Value: i + 1})
	}
	s.Flush()
	s.Record(Entry{Path: "a", Old: 50, Value: 51}) // pending

	cfg := s.Config()
	cfg.MaxEntriesPerObject = 10
	s.Apply(cfg)
	if got := len(s.Query(Query{Path: "a", Limit: 1000})); got != 10 {
		t.Errorf("smaller ring applied live: %d", got)
	}

	if err := s.ClearDisk(); err != nil {
		t.Fatal(err)
	}
	if st := s.Status(); st.DiskUsageBytes != 0 || st.PendingWrites != 0 || st.MemoryEntries != 10 {
		t.Fatalf("ClearDisk removes files and pending, keeps memory: %+v", st)
	}
	s.ClearMemory()
	if st := s.Status(); st.MemoryEntries != 0 {
		t.Fatalf("ClearMemory: %+v", st)
	}
}

func TestDisk_WorkerWritesOnInterval(t *testing.T) {
	dir := t.TempDir()
	cfg := DefaultConfig()
	cfg.Dir, cfg.FlushInterval = dir, 20*time.Millisecond
	s := New(cfg, nil)
	s.Start()
	defer s.Close(time.Second)
	s.Record(Entry{Path: "a", Old: 0, Value: 1})
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if st := s.Status(); st.PendingWrites == 0 && st.DiskUsageBytes > 0 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("worker did not write")
}

// After a restart a Satel zone starts at 0 and reports 0 -> 1 although it
// was 1 before: no entry (it used to be a fake change on every deploy).
func TestDisk_RestartWithDefaultValueIsNoChange(t *testing.T) {
	dir := t.TempDir()
	c := &clock{t: time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)}
	s := diskStoreAt(t, dir, c)
	s.Record(Entry{Path: "zone", Old: 0, Value: 1})
	s.Close(time.Second)
	c.t = c.t.Add(time.Minute)
	s2 := diskStoreAt(t, dir, c)
	s2.Record(Entry{Path: "zone", Old: 0, Value: 1})
	if got := len(s2.Query(Query{Path: "zone"})); got != 1 {
		t.Fatalf("entries %d, want 1", got)
	}
}

func TestDisk_TornLineDoesNotSwallowNextRecord(t *testing.T) {
	dir := t.TempDir()
	c := &clock{t: time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)}
	s := diskStoreAt(t, dir, c)
	s.Record(Entry{Path: "a", Old: 0, Value: 1})
	s.Flush()
	f, _ := os.OpenFile(filepath.Join(dir, "2026-10-07.jsonl"), os.O_APPEND|os.O_WRONLY, 0600)
	f.WriteString(`{"schema":1,"id":"torn","pa`) // power cut mid-write
	f.Write(make([]byte, 80*1024))               // zeros after an ext4 crash
	f.Close()
	s.Record(Entry{Path: "a", Old: 1, Value: 0})
	s.Record(Entry{Path: "b", Old: 0, Value: 1})
	s.Flush()
	s2 := diskStoreAt(t, dir, c)
	if a, b := len(s2.Query(Query{Path: "a"})), len(s2.Query(Query{Path: "b"})); a != 2 || b != 1 {
		t.Fatalf("after a torn line: a=%d b=%d, want 2 and 1", a, b)
	}
}

// Close is bounded even when the writer is stuck in a flush.
func TestDisk_CloseBoundedWhenWriterStuck(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Dir = t.TempDir()
	s := New(cfg, nil)
	s.Start()
	s.flushMu.Lock() // the worker's flush blocks as on a hung SD card
	defer s.flushMu.Unlock()
	s.Record(Entry{Path: "a", Old: 0, Value: 1})
	start := time.Now()
	if err := s.Close(100 * time.Millisecond); err == nil {
		t.Fatal("expected a timeout")
	}
	if d := time.Since(start); d > time.Second {
		t.Fatalf("Close took %v", d)
	}
}
