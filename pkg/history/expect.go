package history

import (
	"sync"
	"time"
)

// Expectation is a state change a command will cause later, done by the
// device itself: switchOn(60000) means the CLU reports 0 in a minute. The
// report that matches it inherits the command's origin and actor.
type Expectation struct {
	Path          string
	Value         any
	DueAtMs       int64
	EarlyMs       int64 // the report may come this much before DueAtMs
	LateMs        int64 // ... or this much after (reports can be delayed)
	Origin        string
	Actor         string
	CorrelationID string
}

const maxExpectations = 1000

// Tracker holds pending expectations; it lives in Go, so a Lua reload does
// not forget a light that an automation switched on for a minute.
type Tracker struct {
	mu    sync.Mutex
	items []Expectation
	now   func() time.Time
}

// NewTracker creates a Tracker; now is its clock (nil = system clock).
func NewTracker(now func() time.Time) *Tracker {
	if now == nil {
		now = time.Now
	}
	return &Tracker{now: now}
}

// Expect registers an expectation; a newer one for the same path replaces
// the older (switchOn again extends the minute).
func (t *Tracker) Expect(e Expectation) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.prune(t.now().UnixMilli())
	for i := range t.items {
		if t.items[i].Path == e.Path {
			t.items = append(t.items[:i], t.items[i+1:]...)
			break
		}
	}
	if len(t.items) >= maxExpectations {
		t.items = t.items[1:]
	}
	t.items = append(t.items, e)
}

// Match returns the expectation for path when the reported change is the
// expected one within its window. Any reported change of path removes its
// expectation: one that does not match means something else changed the
// device first (a person at the wall), and the expectation is stale.
func (t *Tracker) Match(path string, value any, atMs int64) (Expectation, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.prune(atMs)
	for i, e := range t.items {
		if e.Path != path {
			continue
		}
		t.items = append(t.items[:i], t.items[i+1:]...)
		if equal(e.Value, value) && atMs >= e.DueAtMs-e.EarlyMs && atMs <= e.DueAtMs+e.LateMs {
			return e, true
		}
		return Expectation{}, false
	}
	return Expectation{}, false
}

// Forget drops the expectation for path (the command was cancelled by another one).
func (t *Tracker) Forget(path string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	for i := range t.items {
		if t.items[i].Path == path {
			t.items = append(t.items[:i], t.items[i+1:]...)
			return
		}
	}
}

func (t *Tracker) prune(nowMs int64) {
	kept := t.items[:0]
	for _, e := range t.items {
		if nowMs <= e.DueAtMs+e.LateMs {
			kept = append(kept, e)
		}
	}
	t.items = kept
}
