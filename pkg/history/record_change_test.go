package history

import (
	"testing"
	"time"
)

// RecordChange verdicts (#49): what StateBus:watch gets through
// __go_history_record.

func TestRecordChange_DisabledIsAlwaysAChange(t *testing.T) {
	s, _ := newStore(Config{Enabled: false})
	r := s.RecordChange(Entry{Path: "a", Old: nil, Value: 1}) // first reading
	if !r.Change || r.Known || r.ID == "" || r.AtMs == 0 {
		t.Fatalf("disabled store: %+v, want Change without Known", r)
	}
	s.BeginReload()
	if r := s.RecordChange(Entry{Path: "a", Old: nil, Value: 1}); !r.Change || r.Known {
		t.Fatalf("disabled store after a reload: %+v", r)
	}
}

func TestRecordChange_FirstReadingVerdicts(t *testing.T) {
	s, _ := newStore(DefaultConfig())
	if r := s.RecordChange(Entry{Path: "a", Old: nil, Value: 1}); r.Change || r.Known {
		t.Fatalf("very first reading: %+v, want no change", r)
	}
	if r := s.RecordChange(Entry{Path: "a", Old: 1, Value: 0}); !r.Change || r.Known {
		t.Fatalf("real change: %+v", r)
	}
	s.BeginReload()
	if r := s.RecordChange(Entry{Path: "a", Old: nil, Value: 0}); r.Change || r.Known {
		t.Fatalf("same value after a reload: %+v", r)
	}
	s.BeginReload()
	r := s.RecordChange(Entry{Path: "a", Old: nil, Value: 1})
	if !r.Change || !r.Known || r.Old != 0 {
		t.Fatalf("changed during the reload: %+v, want Change from known 0", r)
	}
	last, _ := s.Last("a")
	if last.Old != 0 || last.Value != 1 || last.Origin != "system" {
		t.Fatalf("history disagrees with the verdict: %+v", last)
	}
	// a second identical report is nothing for the history either
	if r := s.RecordChange(Entry{Path: "a", Old: 1, Value: 1}); r.Known {
		t.Fatalf("identical report got a known old: %+v", r)
	}
}

// A first report that comes after the 3-minute reload window: a report with
// old == nil is still measured against the known value (the nil path has no
// window), while an object restarting from a default (old 0) is a plain change.
func TestRecordChange_AfterReloadWindow(t *testing.T) {
	s, c := newStore(DefaultConfig())
	s.RecordChange(Entry{Path: "clu", Old: nil, Value: 0})
	s.RecordChange(Entry{Path: "clu", Old: 0, Value: 1})
	s.RecordChange(Entry{Path: "virt", Old: 0, Value: 1})
	s.BeginReload()
	c.t = c.t.Add(reloadWindowMs*time.Millisecond + time.Second)

	if r := s.RecordChange(Entry{Path: "clu", Old: nil, Value: 1}); r.Change || r.Known {
		t.Fatalf("equal nil-old report after the window: %+v, want no change", r)
	}
	s.BeginReload()
	c.t = c.t.Add(reloadWindowMs*time.Millisecond + time.Second)
	if r := s.RecordChange(Entry{Path: "clu", Old: nil, Value: 0}); !r.Change || !r.Known || r.Old != 1 {
		t.Fatalf("different nil-old report after the window: %+v, want Change from known 1", r)
	}
	// a virtual object silently back at its default 0, switched on an hour
	// later: a real change, the known 1 does not swallow it
	if r := s.RecordChange(Entry{Path: "virt", Old: 0, Value: 1}); !r.Change || r.Known {
		t.Fatalf("default -> value after the window: %+v, want a plain change", r)
	}
}

// Within the window an object restarting from its default and set back to
// the value it had before the reload is no change (and no watch).
func TestRecordChange_DefaultBackToKnownWithinWindow(t *testing.T) {
	s, c := newStore(DefaultConfig())
	s.RecordChange(Entry{Path: "virt", Old: 0, Value: 1})
	s.BeginReload()
	c.t = c.t.Add(time.Second)
	if r := s.RecordChange(Entry{Path: "virt", Old: 0, Value: 1}); r.Change || r.Known {
		t.Fatalf("default 0 -> known 1 within the window: %+v, want no change", r)
	}
	if r := s.RecordChange(Entry{Path: "virt", Old: 1, Value: 0}); !r.Change || r.Known {
		t.Fatalf("the next change is real: %+v", r)
	}
}

// A restart restores the known values from disk: the first report equal to
// the restored value is no change, a different one is a change from it.
func TestRecordChange_RestoredFromDisk(t *testing.T) {
	dir := t.TempDir()
	cfg := DefaultConfig()
	cfg.Dir = dir
	c := &clock{t: time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)}
	s1 := New(cfg, c.now)
	s1.RecordChange(Entry{Path: "a", Old: nil, Value: 0})
	s1.RecordChange(Entry{Path: "a", Old: 0, Value: 1})
	s1.RecordChange(Entry{Path: "b", Old: nil, Value: "x"})
	s1.RecordChange(Entry{Path: "b", Old: "x", Value: "y"})
	if err := s1.Flush(); err != nil {
		t.Fatal(err)
	}

	c.t = c.t.Add(time.Minute)
	s2 := New(cfg, c.now)
	if r := s2.RecordChange(Entry{Path: "a", Old: nil, Value: 1}); r.Change || r.Known {
		t.Fatalf("first report equal to the restored value: %+v", r)
	}
	s3 := New(cfg, c.now)
	r := s3.RecordChange(Entry{Path: "a", Old: nil, Value: 0})
	// JSON brings numbers back as float64; equal() and the Lua bridge cope
	if !r.Change || !r.Known || r.Old != float64(1) {
		t.Fatalf("first report different from the restored value: %+v, want Change from known 1", r)
	}
	if r := s3.RecordChange(Entry{Path: "b", Old: nil, Value: "x"}); !r.Change || !r.Known || r.Old != "y" {
		t.Fatalf("restored string: %+v", r)
	}
	// a virtual object at its default right after the restart
	s4 := New(cfg, c.now)
	if r := s4.RecordChange(Entry{Path: "a", Old: 0, Value: 1}); r.Change || r.Known {
		t.Fatalf("default 0 -> restored 1 after a restart: %+v, want no change", r)
	}
}

func TestRecordChange_KnownOldKeepsItsType(t *testing.T) {
	s, _ := newStore(DefaultConfig())
	table := map[string]any{"r": int64(1), "g": int64(2)}
	cases := []struct {
		path          string
		before, after any
	}{
		{"bool", true, false},
		{"str", "abc", "def"},
		{"tbl", table, map[string]any{"r": int64(1), "g": int64(3)}},
	}
	for _, tc := range cases {
		s.RecordChange(Entry{Path: tc.path, Old: nil, Value: "seed"})
		s.RecordChange(Entry{Path: tc.path, Old: "seed", Value: tc.before})
	}
	s.BeginReload()
	for _, tc := range cases {
		if r := s.RecordChange(Entry{Path: tc.path, Old: nil, Value: tc.before}); r.Change {
			t.Errorf("%s: equal first reading is a change: %+v", tc.path, r)
		}
	}
	s.BeginReload()
	for _, tc := range cases {
		r := s.RecordChange(Entry{Path: tc.path, Old: nil, Value: tc.after})
		if !r.Change || !r.Known || !equal(r.Old, tc.before) {
			t.Errorf("%s: %+v, want Change from known %v", tc.path, r, tc.before)
		}
	}
}

// value -> nil -> value is a normal change with old nil, not a first reading.
func TestRecordChange_NilValueRoundTrip(t *testing.T) {
	s, _ := newStore(DefaultConfig())
	s.RecordChange(Entry{Path: "a", Old: nil, Value: 1})
	if r := s.RecordChange(Entry{Path: "a", Old: 1, Value: nil}); !r.Change || r.Known {
		t.Fatalf("clearing the value: %+v", r)
	}
	if r := s.RecordChange(Entry{Path: "a", Old: nil, Value: 2}); !r.Change || r.Known {
		t.Fatalf("value after nil: %+v, want a plain change with old nil", r)
	}
}

// An object whose last change is older than the memory window keeps its
// value through a restart (known.json): a change while vCLU was down is a
// change from it, the same value is none.
func TestRecordChange_KnownBeyondMemoryWindow(t *testing.T) {
	now := time.Date(2026, 10, 7, 21, 0, 0, 0, time.UTC)
	clock := func() time.Time { return now }
	cfg := DefaultConfig()
	cfg.Dir, cfg.FlushInterval = t.TempDir(), time.Hour
	s := New(cfg, clock)
	s.Record(Entry{Path: "a", Old: 1, Value: 0})
	s.Record(Entry{Path: "b", Old: "x", Value: "y"})
	if err := s.Close(time.Second); err != nil {
		t.Fatal(err)
	}

	now = now.Add(30 * time.Hour)
	s2 := New(cfg, clock)
	if r := s2.RecordChange(Entry{Path: "a", Old: nil, Value: 0}); r.Change {
		t.Fatalf("same value 30 h later: %+v", r)
	}
	if r := s2.RecordChange(Entry{Path: "b", Old: nil, Value: "z"}); !r.Change || !r.Known || r.Old != "y" {
		t.Fatalf("changed while down: %+v, want a change from y", r)
	}
	s2.Record(Entry{Path: "c", Old: nil, Value: true})
	if err := s2.Close(time.Second); err != nil {
		t.Fatal(err)
	}

	// the snapshot follows the changes
	now = now.Add(30 * time.Hour)
	s3 := New(cfg, clock)
	defer func() { _ = s3.Close(time.Second) }()
	if r := s3.RecordChange(Entry{Path: "b", Old: nil, Value: "z"}); r.Change {
		t.Fatalf("b unchanged since the last run: %+v", r)
	}
	if r := s3.RecordChange(Entry{Path: "c", Old: nil, Value: false}); !r.Change || r.Old != true {
		t.Fatalf("c changed while down: %+v", r)
	}
}
