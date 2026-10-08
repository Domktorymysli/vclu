// Package history keeps the state change history of vCLU objects: in memory
// for the hot window and in daily JSONL files on disk (stage C3).
//
// The Store is the single owner of history data (ai/AUTOMATION_API_PLAN.md,
// stage C1): Lua records every state change through a bridge, while Lua
// automations, MCP and REST read from it. It survives Lua reloads. Rings are
// per object, so a chatty sensor cannot push out a rarely changing one; the
// global limit and the age limit still win during an event storm.
package history

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

// SchemaVersion is the version of the Entry format.
const SchemaVersion = 1

// Entry is one recorded state change.
type Entry struct {
	Schema        int    `json:"schema"`
	ID            string `json:"id"`
	Path          string `json:"path"`
	Value         any    `json:"value"`
	Old           any    `json:"old"`
	AtMs          int64  `json:"atMs"` // UTC Unix milliseconds
	Origin        string `json:"origin"`
	Actor         string `json:"actor"`
	CorrelationID string `json:"correlationId,omitempty"`
}

// Config holds the memory and disk limits.
type Config struct {
	Enabled             bool
	MemoryHours         int
	MaxEntriesPerObject int
	MaxEntriesTotal     int

	// Disk: Dir empty or DiskEnabled false keeps history in memory only
	DiskEnabled   bool
	Dir           string
	RetentionDays int
	MaxDiskMB     int
	FlushInterval time.Duration
}

// DefaultConfig keeps 24 h for the measured load: the busiest Satel zone
// peaked at ~260 changes an hour, the whole house at ~1000 an hour. On disk
// that is a few MB a day, so 90 days fit well under 1 GB.
func DefaultConfig() Config {
	return Config{
		Enabled: true, MemoryHours: 24, MaxEntriesPerObject: 10000, MaxEntriesTotal: 50000,
		DiskEnabled: true, RetentionDays: 90, MaxDiskMB: 1024, FlushInterval: 5 * time.Minute,
	}
}

// maxPending bounds the entries waiting for disk: when writes keep failing,
// the oldest are dropped (counted in Status.LostEntries); control of the
// house is never blocked by history.
const maxPending = 10000

// maxEntriesTotalCap bounds the memory of the rings on a small board.
const maxEntriesTotalCap = 200000

// Query selects entries; zero fields mean "no constraint". Limit is capped at MaxQueryLimit.
type Query struct {
	Path  string
	Since int64 // atMs, inclusive
	Until int64 // atMs, exclusive
	Limit int
}

// reloadWindowMs bounds the post-reload comparison with known values: CLU
// reports and the Satel refresh come within seconds, energy counters within
// about a minute.
const reloadWindowMs = 3 * 60 * 1000

const (
	DefaultQueryLimit = 100
	MaxQueryLimit     = 1000
)

// Status describes what the Store holds.
type Status struct {
	Enabled        bool  `json:"enabled"`
	MemoryEntries  int   `json:"memoryEntries"`
	Objects        int   `json:"objects"`
	OldestInMemory int64 `json:"oldestInMemory,omitempty"` // atMs
	Dropped        int64 `json:"dropped"`                  // removed from memory by limits before their age expired

	DiskEnabled    bool   `json:"diskEnabled"`
	DiskUsageBytes int64  `json:"diskUsageBytes"`
	DiskOldestDay  string `json:"diskOldestDay,omitempty"` // UTC day of the oldest file
	DiskOverLimit  bool   `json:"diskOverLimit"`
	PendingWrites  int    `json:"pendingWrites"`
	LostEntries    int64  `json:"lostEntries"`    // never written: pending overflow
	SkippedRecords int64  `json:"skippedRecords"` // over 64 KiB or not serializable
	LastFlushAt    int64  `json:"lastFlushAt,omitempty"`
	LastFlushError string `json:"lastFlushError,omitempty"`
}

// Store is safe for concurrent use.
type Store struct {
	mu         sync.Mutex
	cfg        Config
	now        func() time.Time
	rings      map[string]*ring
	order      []*Entry // all entries in record order, for the global limit
	total      int
	tombstones int // removed entries still in order
	dropped    int64
	// known is the last value seen per path, entry or not: a first reading
	// (old == nil, e.g. after a Lua reload) is compared with it
	known map[string]any
	// epoch counts Lua reloads; pathEpoch is the epoch of a path's last record
	epoch      int
	reloadAtMs int64
	pathEpoch  map[string]int

	idPrefix string
	seq      atomic.Uint64

	// Expected changes of devices acting on earlier commands (switchOn(ms))
	Expectations *Tracker

	disk           *diskStore
	pending        []Entry
	lost, skipped  int64
	lastFlushAt    int64
	lastFlushError string
	overLimit      bool
	flushMu        sync.Mutex // one flush at a time
	stop           chan struct{}
	workerDone     chan struct{}
}

type ring struct{ entries []*Entry }

// New creates a Store. now is its clock (nil = system clock); the sandbox
// passes its virtual clock.
func New(cfg Config, now func() time.Time) *Store {
	if now == nil {
		now = time.Now
	}
	s := &Store{
		cfg:          normalize(cfg),
		now:          now,
		rings:        map[string]*ring{},
		known:        map[string]any{},
		pathEpoch:    map[string]int{},
		idPrefix:     rand.Text()[:10],
		Expectations: NewTracker(now),
	}
	if s.cfg.DiskEnabled && s.cfg.Dir != "" {
		s.disk = &diskStore{dir: s.cfg.Dir}
		s.restore()
	}
	return s
}

// restore loads the hot window from the newest files, so a restart starts
// with the last MemoryHours of history and the last known value of every
// object (a first reading equal to it is then no change).
//
// Memory stays within the ring limits: only the newest MaxEntriesTotal
// entries of the window are held (files are in time order), the last value
// of every object is tracked on the way.
func (s *Store) restore() {
	since := s.now().Add(-time.Duration(s.cfg.MemoryHours) * time.Hour).UnixMilli()
	tail := make([]Entry, 0, min(s.cfg.MaxEntriesTotal, 4096))
	next := 0 // oldest slot once tail is full
	lastAt := map[string]int64{}
	known := map[string]any{}
	err := s.disk.scanRange(since, 0, func(e Entry) {
		if at, ok := lastAt[e.Path]; !ok || e.AtMs >= at {
			lastAt[e.Path], known[e.Path] = e.AtMs, e.Value
		}
		if len(tail) < s.cfg.MaxEntriesTotal {
			tail = append(tail, e)
			return
		}
		tail[next] = e
		next = (next + 1) % len(tail)
	})
	if err != nil {
		s.lastFlushError = "restore: " + err.Error()
	}
	sort.SliceStable(tail, func(i, j int) bool { return tail[i].AtMs < tail[j].AtMs })
	seen := make(map[string]bool, len(tail))
	s.mu.Lock()
	defer s.mu.Unlock()
	s.known = known
	for i := range tail {
		e := tail[i]
		if seen[e.ID] {
			continue // a retried batch may have been written twice
		}
		seen[e.ID] = true
		s.insert(&e)
	}
	s.expire(s.now().UnixMilli())
	// a restart is a reload too: objects start from defaults (a zone at 0)
	// and their first change is measured against the restored value
	s.epoch++
	s.reloadAtMs = s.now().UnixMilli()
}

func normalize(cfg Config) Config {
	d := DefaultConfig()
	if cfg.MemoryHours <= 0 {
		cfg.MemoryHours = d.MemoryHours
	}
	if cfg.MaxEntriesPerObject <= 0 {
		cfg.MaxEntriesPerObject = d.MaxEntriesPerObject
	}
	if cfg.MaxEntriesTotal <= 0 {
		cfg.MaxEntriesTotal = d.MaxEntriesTotal
	}
	if cfg.MaxEntriesTotal > maxEntriesTotalCap {
		cfg.MaxEntriesTotal = maxEntriesTotalCap
	}
	if cfg.MaxEntriesTotal < cfg.MaxEntriesPerObject {
		cfg.MaxEntriesTotal = cfg.MaxEntriesPerObject
	}
	if cfg.RetentionDays <= 0 {
		cfg.RetentionDays = d.RetentionDays
	}
	if cfg.MaxDiskMB <= 0 {
		cfg.MaxDiskMB = d.MaxDiskMB
	}
	if cfg.FlushInterval <= 0 {
		cfg.FlushInterval = d.FlushInterval
	}
	return cfg
}

// Record assigns id and atMs to a change and stores it. It always returns an
// id and atMs, also when history is disabled or nothing is stored.
//
// The first change of an object within 3 minutes after BeginReload is
// measured against its value from before the reload (objects restart from
// defaults). A first
// reading (old == nil: startup, Lua reload, new object) is not a change: without a known value it only sets the baseline, with the same
// value it is ignored, and with a different one it is stored once as a
// change from the known value with origin "system" (who changed it while
// vCLU was not looking is unknown).
func (s *Store) Record(e Entry) (id string, atMs int64) {
	id, atMs = s.Stamp()

	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.cfg.Enabled {
		return id, atMs
	}
	e.Value, e.Old = boundValue(e.Value), boundValue(e.Old)
	prev, seen := s.known[e.Path]
	s.known[e.Path] = e.Value
	// Only within the reload window: an object that stayed silent after the
	// reload and changes later is a real change, whatever its known value
	firstSinceReload := s.pathEpoch[e.Path] != s.epoch && atMs-s.reloadAtMs <= reloadWindowMs
	s.pathEpoch[e.Path] = s.epoch
	if e.Old == nil && e.Value == nil {
		return id, atMs
	}
	// After a Lua reload objects restart from a default (Satel zone 0, counter
	// 0): their first change is measured against the last known value
	if firstSinceReload && seen && e.Old != nil {
		if equal(prev, e.Value) {
			return id, atMs
		}
		e.Old = prev
	}
	// prev == nil: the object really was nil (value -> nil -> value), a normal change
	if e.Old == nil && (!seen || prev != nil) {
		if !seen || equal(prev, e.Value) {
			return id, atMs
		}
		e.Old, e.Origin, e.Actor, e.CorrelationID = prev, "system", "system:startup", ""
	}

	e.Schema, e.ID, e.AtMs = SchemaVersion, id, atMs
	if e.Origin == "" {
		e.Origin = "unknown"
	}
	if e.Actor == "" {
		e.Actor = "unknown"
	}
	if s.disk != nil {
		if len(s.pending) >= maxPending {
			s.pending = s.pending[1:]
			s.lost++
		}
		s.pending = append(s.pending, e)
	}
	s.insert(&e)
	s.expire(atMs)
	return id, atMs
}

// insert adds an entry to its ring and the global order, then applies the
// per-object and total limits. Caller holds s.mu.
func (s *Store) insert(entry *Entry) {
	r := s.rings[entry.Path]
	if r == nil {
		r = &ring{}
		s.rings[entry.Path] = r
	}
	r.entries = append(r.entries, entry)
	s.order = append(s.order, entry)
	s.total++

	if len(r.entries) > s.cfg.MaxEntriesPerObject {
		s.removeOldestOf(r)
		s.dropped++
		s.tombstones++
		// A busy object behind an old live entry of another one would keep
		// its removed entries in order until that entry expires
		if s.tombstones > s.total {
			s.compact()
		}
	}
	for s.total > s.cfg.MaxEntriesTotal {
		s.removeGlobalOldest()
		s.dropped++
	}
}

// BeginReload marks a Lua reload: the next change of each object is compared
// with its value from before the reload.
func (s *Store) BeginReload() {
	s.mu.Lock()
	s.epoch++
	s.reloadAtMs = s.now().UnixMilli()
	s.mu.Unlock()
}

// Stamp returns a new id and the current atMs without storing anything.
func (s *Store) Stamp() (id string, atMs int64) {
	return fmt.Sprintf("%s-%d", s.idPrefix, s.seq.Add(1)), s.now().UnixMilli()
}

// equal compares decoded Lua values; tables are compared deeply, and numbers
// by value: Lua gives int64 for whole numbers, JSON from disk float64.
func equal(a, b any) bool {
	return reflect.DeepEqual(numbersAsFloat(a), numbersAsFloat(b))
}

func numbersAsFloat(v any) any {
	switch x := v.(type) {
	case int:
		return float64(x)
	case int64:
		return float64(x)
	case []any:
		out := make([]any, len(x))
		for i, e := range x {
			out[i] = numbersAsFloat(e)
		}
		return out
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, e := range x {
			out[k] = numbersAsFloat(e)
		}
		return out
	}
	return v
}

// removeOldestOf drops the oldest entry of r; order is compacted lazily.
func (s *Store) removeOldestOf(r *ring) {
	old := r.entries[0]
	r.entries[0] = nil
	r.entries = r.entries[1:]
	old.ID = "" // tombstone for s.order
	s.total--
}

// compact drops tombstones from order.
func (s *Store) compact() {
	live := make([]*Entry, 0, s.total)
	for _, e := range s.order {
		if e.ID != "" {
			live = append(live, e)
		}
	}
	s.order, s.tombstones = live, 0
}

func (s *Store) removeGlobalOldest() {
	for len(s.order) > 0 {
		e := s.order[0]
		s.order[0] = nil
		s.order = s.order[1:]
		if e.ID == "" {
			s.tombstones--
			continue // already removed from its ring
		}
		r := s.rings[e.Path]
		r.entries[0] = nil
		r.entries = r.entries[1:]
		s.total--
		return
	}
}

// expire drops entries older than MemoryHours.
func (s *Store) expire(nowMs int64) {
	cutoff := nowMs - int64(s.cfg.MemoryHours)*int64(time.Hour/time.Millisecond)
	for len(s.order) > 0 {
		e := s.order[0]
		if e.ID == "" {
			s.order[0] = nil
			s.order = s.order[1:]
			s.tombstones--
			continue
		}
		if e.AtMs >= cutoff {
			return
		}
		s.removeGlobalOldest()
	}
}

// Last returns the most recent entry of path.
func (s *Store) Last(path string) (Entry, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.expire(s.now().UnixMilli())
	r := s.rings[path]
	if r == nil || len(r.entries) == 0 {
		return Entry{}, false
	}
	return *r.entries[len(r.entries)-1], true
}

// Query returns matching entries, newest first.
func (s *Store) Query(q Query) []Entry {
	limit := q.Limit
	if limit <= 0 {
		limit = DefaultQueryLimit
	}
	if limit > MaxQueryLimit {
		limit = MaxQueryLimit
	}
	match := func(e *Entry) bool {
		return e.ID != "" && (q.Since == 0 || e.AtMs >= q.Since) && (q.Until == 0 || e.AtMs < q.Until)
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.expire(s.now().UnixMilli())
	src := s.order
	if q.Path != "" {
		r := s.rings[q.Path]
		if r == nil {
			return []Entry{}
		}
		src = r.entries
	}
	out := []Entry{}
	for i := len(src) - 1; i >= 0 && len(out) < limit; i-- {
		if match(src[i]) {
			out = append(out, *src[i])
		}
	}
	return out
}

// Status reports the current content of the Store.
func (s *Store) Status() Status {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.expire(s.now().UnixMilli())
	st := Status{
		Enabled: s.cfg.Enabled, MemoryEntries: s.total, Dropped: s.dropped,
		DiskEnabled: s.disk != nil, PendingWrites: len(s.pending), LostEntries: s.lost,
		SkippedRecords: s.skipped, LastFlushAt: s.lastFlushAt, LastFlushError: s.lastFlushError,
		DiskOverLimit: s.overLimit,
	}
	for _, r := range s.rings {
		if len(r.entries) > 0 {
			st.Objects++
		}
	}
	for _, e := range s.order {
		if e.ID != "" {
			st.OldestInMemory = e.AtMs
			break
		}
	}
	if disk := s.disk; disk != nil {
		s.mu.Unlock()
		st.DiskUsageBytes, st.DiskOldestDay = disk.usage()
		s.mu.Lock()
	}
	return st
}

// maxValueSize bounds old and value of one entry, so a record fits the
// 64 KiB line limit on disk and a huge Lua table is not kept in memory.
const maxValueSize = 16 * 1024

// boundValue replaces a value over maxValueSize with a short marker; the
// hash keeps two different large values different for change detection.
func boundValue(v any) any {
	switch x := v.(type) {
	case nil, bool, int, int64, float64:
		return v
	case string:
		// JSON escaping grows a string up to 6x (\u00XX, invalid UTF-8)
		if len(x) <= maxValueSize/6 {
			return v
		}
	}
	b, err := json.Marshal(v)
	if err != nil {
		// e.g. NaN inside a table; hash the content so later changes still count
		sum := sha256.Sum256([]byte(fmt.Sprintf("%v", v)))
		return fmt.Sprintf("<unencodable value: %T, sha256 %x>", v, sum[:6])
	}
	if len(b) <= maxValueSize {
		return v
	}
	sum := sha256.Sum256(b)
	return fmt.Sprintf("<value too large: %d bytes, sha256 %x>", len(b), sum[:6])
}
