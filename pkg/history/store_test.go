package history

import (
	"fmt"
	"math"
	"strings"
	"testing"
	"time"
)

type clock struct{ t time.Time }

func (c *clock) now() time.Time { return c.t }

func newStore(cfg Config) (*Store, *clock) {
	c := &clock{t: time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)}
	return New(cfg, c.now), c
}

func values(entries []Entry) string {
	s := ""
	for _, e := range entries {
		s += fmt.Sprintf("%v>%v ", e.Old, e.Value)
	}
	return s
}

func TestStore_RecordAndQuery(t *testing.T) {
	s, c := newStore(DefaultConfig())
	s.Record(Entry{Path: "a", Old: 0, Value: 1})
	c.t = c.t.Add(time.Second)
	id, atMs := s.Record(Entry{Path: "a", Old: 1, Value: 0})
	s.Record(Entry{Path: "b", Old: 5, Value: 6})

	last, ok := s.Last("a")
	if !ok || last.ID != id || last.AtMs != atMs || last.Value != 0 || last.Schema != SchemaVersion {
		t.Fatalf("Last = %+v", last)
	}
	if last.Origin != "unknown" || last.Actor != "unknown" {
		t.Errorf("defaults: %+v", last)
	}
	if got := values(s.Query(Query{Path: "a"})); got != "1>0 0>1 " {
		t.Errorf("newest first: %s", got)
	}
	if got := len(s.Query(Query{})); got != 3 {
		t.Errorf("all paths: %d", got)
	}
	if got := s.Query(Query{Path: "missing"}); len(got) != 0 {
		t.Errorf("missing path: %v", got)
	}
	if got := values(s.Query(Query{Path: "a", Since: atMs})); got != "1>0 " {
		t.Errorf("since: %s", got)
	}
	if got := values(s.Query(Query{Path: "a", Until: atMs})); got != "0>1 " {
		t.Errorf("until: %s", got)
	}
}

func TestStore_IDsUnique(t *testing.T) {
	s, _ := newStore(DefaultConfig())
	seen := map[string]bool{}
	for i := 0; i < 1000; i++ {
		id, _ := s.Record(Entry{Path: "a", Old: i, Value: i + 1})
		if seen[id] {
			t.Fatalf("duplicate id %s", id)
		}
		seen[id] = true
	}
}

func TestStore_FirstReadings(t *testing.T) {
	s, _ := newStore(DefaultConfig())
	s.Record(Entry{Path: "a", Old: nil, Value: 0}) // startup: baseline
	if _, ok := s.Last("a"); ok {
		t.Fatal("first reading must not be an entry")
	}
	s.Record(Entry{Path: "a", Old: 0, Value: 1})
	s.Record(Entry{Path: "a", Old: nil, Value: 1}) // Lua reload, same value
	if got := values(s.Query(Query{Path: "a"})); got != "0>1 " {
		t.Fatalf("reload with the same value: %s", got)
	}
	s.Record(Entry{Path: "a", Old: nil, Value: 0, Origin: "lua", Actor: "x"}) // changed while not looking
	last, _ := s.Last("a")
	if last.Old != 1 || last.Value != 0 || last.Origin != "system" || last.Actor != "system:startup" {
		t.Fatalf("reconciled entry: %+v", last)
	}
}

func TestStore_PerObjectLimitKeepsQuietObjects(t *testing.T) {
	s, _ := newStore(Config{Enabled: true, MaxEntriesPerObject: 100, MaxEntriesTotal: 1000})
	s.Record(Entry{Path: "quiet", Old: 0, Value: 1})
	for i := 0; i < 500; i++ {
		s.Record(Entry{Path: "noisy", Old: i, Value: i + 1})
	}
	if got := len(s.Query(Query{Path: "noisy", Limit: 1000})); got != 100 {
		t.Errorf("noisy kept %d, want 100", got)
	}
	if _, ok := s.Last("quiet"); !ok {
		t.Error("a chatty sensor pushed out a quiet object")
	}
	if st := s.Status(); st.MemoryEntries != 101 || st.Objects != 2 || st.Dropped != 400 {
		t.Errorf("status %+v", st)
	}
}

func TestStore_GlobalLimit(t *testing.T) {
	s, _ := newStore(Config{Enabled: true, MaxEntriesPerObject: 100, MaxEntriesTotal: 150})
	for i := 0; i < 100; i++ {
		s.Record(Entry{Path: "a", Old: i, Value: i + 1})
	}
	for i := 0; i < 100; i++ {
		s.Record(Entry{Path: "b", Old: i, Value: i + 1})
	}
	a, b := len(s.Query(Query{Path: "a", Limit: 1000})), len(s.Query(Query{Path: "b", Limit: 1000}))
	if a+b != 150 || b != 100 {
		t.Errorf("a=%d b=%d, want the oldest (a) dropped first", a, b)
	}
}

func TestStore_ExpiresByAge(t *testing.T) {
	s, c := newStore(Config{Enabled: true, MemoryHours: 1})
	s.Record(Entry{Path: "a", Old: 0, Value: 1})
	c.t = c.t.Add(30 * time.Minute)
	s.Record(Entry{Path: "a", Old: 1, Value: 0})
	c.t = c.t.Add(45 * time.Minute)
	if got := values(s.Query(Query{Path: "a"})); got != "1>0 " {
		t.Errorf("after 75 min: %s", got)
	}
	if st := s.Status(); st.OldestInMemory != c.t.Add(-45*time.Minute).UnixMilli() {
		t.Errorf("oldest %d", st.OldestInMemory)
	}
	c.t = c.t.Add(time.Hour)
	if _, ok := s.Last("a"); ok {
		t.Error("entry older than MemoryHours still returned")
	}
}

func TestStore_Disabled(t *testing.T) {
	s, _ := newStore(Config{Enabled: false})
	id, atMs := s.Record(Entry{Path: "a", Old: 0, Value: 1})
	if id == "" || atMs == 0 {
		t.Error("disabled store must still assign id and atMs")
	}
	if got := s.Query(Query{}); len(got) != 0 {
		t.Errorf("disabled store returned %v", got)
	}
}

func TestStore_QueryLimit(t *testing.T) {
	s, _ := newStore(DefaultConfig())
	for i := 0; i < 2000; i++ {
		s.Record(Entry{Path: "a", Old: i, Value: i + 1})
	}
	if got := len(s.Query(Query{Path: "a"})); got != DefaultQueryLimit {
		t.Errorf("default limit %d", got)
	}
	if got := len(s.Query(Query{Path: "a", Limit: 5000})); got != MaxQueryLimit {
		t.Errorf("max limit %d", got)
	}
}

func TestStore_TableValues(t *testing.T) {
	s, _ := newStore(DefaultConfig())
	v := map[string]any{"r": int64(1)}
	s.Record(Entry{Path: "rgb", Old: nil, Value: v})
	s.Record(Entry{Path: "rgb", Old: nil, Value: map[string]any{"r": int64(1)}})
	if _, ok := s.Last("rgb"); ok {
		t.Error("equal tables after reload must not be an entry")
	}
}

func TestStore_CompactsTombstones(t *testing.T) {
	s, _ := newStore(Config{Enabled: true, MaxEntriesPerObject: 100, MaxEntriesTotal: 1000})
	s.Record(Entry{Path: "quiet", Old: 0, Value: 1}) // old live entry at the head of order
	for i := 0; i < 20000; i++ {
		s.Record(Entry{Path: "busy", Old: i, Value: i + 1})
	}
	if len(s.order) > 2*(s.total+1) {
		t.Fatalf("order holds %d entries for %d live ones", len(s.order), s.total)
	}
	if got := len(s.Query(Query{Limit: 1000})); got != 101 {
		t.Errorf("all-paths query after compaction: %d", got)
	}
	if _, ok := s.Last("quiet"); !ok {
		t.Error("compaction lost a live entry")
	}
}

func TestStore_NilIsARealValue(t *testing.T) {
	s, _ := newStore(DefaultConfig())
	s.Record(Entry{Path: "a", Old: nil, Value: 1}) // baseline
	s.Record(Entry{Path: "a", Old: 1, Value: nil})
	s.Record(Entry{Path: "a", Old: nil, Value: 2, Origin: "lua"})
	last, _ := s.Last("a")
	if last.Old != nil || last.Value != 2 || last.Origin != "lua" {
		t.Fatalf("value -> nil -> value is a normal change: %+v", last)
	}
}

// After a Lua reload a Satel zone restarts at 0 and the panel reports 1 again.
func TestStore_ReloadFromDefaultIsNoChange(t *testing.T) {
	s, _ := newStore(DefaultConfig())
	s.Record(Entry{Path: "zone", Old: 0, Value: 1}) // real change
	s.BeginReload()
	s.Record(Entry{Path: "zone", Old: 0, Value: 1}) // default 0 -> real 1
	if got := len(s.Query(Query{Path: "zone"})); got != 1 {
		t.Fatalf("entries %d, want 1", got)
	}
	s.Record(Entry{Path: "zone", Old: 1, Value: 0})
	if got := len(s.Query(Query{Path: "zone"})); got != 2 {
		t.Fatalf("a real change after that must be stored: %d", got)
	}
}

// Only the first change after a reload is compared with the known value: a
// zone that silently went back to 0 during the reload and is violated hours
// later is a real change.
func TestStore_KnownValueOnlyRightAfterReload(t *testing.T) {
	s, _ := newStore(DefaultConfig())
	s.Record(Entry{Path: "zone", Old: 0, Value: 1})
	s.BeginReload()
	s.Record(Entry{Path: "zone", Old: 1, Value: 0}) // first after reload: real 1 -> 0
	s.Record(Entry{Path: "zone", Old: 0, Value: 1}) // later violation
	if got := values(s.Query(Query{Path: "zone"})); got != "0>1 1>0 0>1 " {
		t.Fatalf("history %s", got)
	}
}

// The truly silent case: after the reload the zone object starts at 0
// without any event, and the zone is violated an hour later.
func TestStore_SilentObjectAfterReloadWindow(t *testing.T) {
	s, c := newStore(DefaultConfig())
	s.Record(Entry{Path: "zone", Old: 0, Value: 1})
	s.BeginReload()
	c.t = c.t.Add(time.Hour)
	s.Record(Entry{Path: "zone", Old: 0, Value: 1})
	if got := values(s.Query(Query{Path: "zone"})); got != "0>1 0>1 " {
		t.Fatalf("violation an hour after a silent reload lost: %s", got)
	}
}

func TestStore_FirstChangeAfterReloadGetsKnownOld(t *testing.T) {
	s, _ := newStore(DefaultConfig())
	s.Record(Entry{Path: "kwh", Old: 12345.0, Value: 12345.4})
	s.BeginReload()
	s.Record(Entry{Path: "kwh", Old: 0.0, Value: 12345.6})
	last, _ := s.Last("kwh")
	if last.Old != 12345.4 {
		t.Fatalf("old = %v, want the value from before the reload", last.Old)
	}
}

func TestStore_NilToNilIgnored(t *testing.T) {
	s, _ := newStore(DefaultConfig())
	s.Record(Entry{Path: "a", Old: 1, Value: nil})
	s.Record(Entry{Path: "a", Old: nil, Value: nil})
	if got := len(s.Query(Query{Path: "a"})); got != 1 {
		t.Fatalf("entries %d", got)
	}
}

func TestBoundValue_EscapingAndUnencodable(t *testing.T) {
	ctrl := strings.Repeat("\x01", maxValueSize/2) // 6x when escaped
	if v, _ := boundValue(ctrl).(string); !strings.HasPrefix(v, "<value too large") {
		t.Errorf("escaped size not checked: %d bytes kept", len(v))
	}
	a := boundValue(map[string]any{"t": math.NaN(), "n": 1.0})
	b := boundValue(map[string]any{"t": math.NaN(), "n": 2.0})
	if a == b {
		t.Errorf("two different unencodable tables got the same marker %v", a)
	}
}
