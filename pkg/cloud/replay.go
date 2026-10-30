package cloud

import (
	"sync"
	"time"
)

// replays keeps the answers to requests the cloud names by id (cmd, notify_action), so each runs
// at most once: a repeat that arrives while the first still runs waits for its answer, one that
// arrives later gets the stored answer. Modelled on pkg/web/action_replay.go.
type replays[T any] struct {
	mu   sync.Mutex
	byID map[string]*replay[T]
	ttl  time.Duration
	max  int
}

type replay[T any] struct {
	done chan struct{} // closed once res is set
	res  T
	at   time.Time // when it finished; zero while it runs
}

func newReplays[T any](ttl time.Duration, max int) *replays[T] {
	return &replays[T]{byID: map[string]*replay[T]{}, ttl: ttl, max: max}
}

// claim returns the entry for id and whether this is its first arrival. The first caller must
// call finish; any other waits on e.done and then reads e.res.
func (r *replays[T]) claim(id string) (e *replay[T], first bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	now := time.Now()
	if e, ok := r.byID[id]; ok && (e.at.IsZero() || now.Sub(e.at) < r.ttl) {
		return e, false
	}
	r.prune(now)
	e = &replay[T]{done: make(chan struct{})}
	r.byID[id] = e
	return e, true
}

// finish sets the answer and wakes the repeats waiting for it. keep false forgets the id
// afterwards (the request did not run, e.g. over a limit), so a later repeat is tried again.
func (r *replays[T]) finish(id string, e *replay[T], res T, keep bool) {
	r.mu.Lock()
	e.res, e.at = res, time.Now()
	if !keep && r.byID[id] == e {
		delete(r.byID, id)
	}
	r.mu.Unlock()
	close(e.done)
}

// prune drops expired answers and, past the cap, the oldest finished ones. Called with mu held.
func (r *replays[T]) prune(now time.Time) {
	for id, e := range r.byID {
		if !e.at.IsZero() && now.Sub(e.at) >= r.ttl {
			delete(r.byID, id)
		}
	}
	for len(r.byID) >= r.max {
		oldest := ""
		for id, e := range r.byID {
			if !e.at.IsZero() && (oldest == "" || e.at.Before(r.byID[oldest].at)) {
				oldest = id
			}
		}
		if oldest == "" {
			return // all still running; the in-flight limits bound them
		}
		delete(r.byID, oldest)
	}
}
