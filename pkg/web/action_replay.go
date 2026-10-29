package web

import (
	"context"
	"regexp"
	"sync"
	"time"
)

// A phone on a weak signal may send an action, lose the answer and send it again. A client that
// names its action (`actionId`) gets the first answer back for a repeat instead of running the
// action twice: a gate gets one pulse however many times the request arrives.

const (
	actionReplayTTL = 2 * time.Minute
	actionReplayMax = 2048
)

var actionIDRe = regexp.MustCompile(`^[A-Za-z0-9_-]{8,64}$`)

type actionAnswer struct {
	done   chan struct{} // closed once status and body are set
	status int
	body   []byte
	at     time.Time
}

type actionReplays struct {
	mu   sync.Mutex
	byID map[string]*actionAnswer
	now  func() time.Time
}

var actionReplay = &actionReplays{byID: make(map[string]*actionAnswer), now: time.Now}

// claim returns the earlier answer for key (waiting while it is still running), or, for a key
// seen for the first time, nil and finish, which the caller must call with its answer. An
// earlier answer with status 0 is one that did not run, or a wait the request gave up.
func (r *actionReplays) claim(ctx context.Context, key string) (prev *actionAnswer, finish func(status int, body []byte)) {
	r.mu.Lock()
	now := r.now()
	if a, ok := r.byID[key]; ok && (a.at.IsZero() || now.Sub(a.at) < actionReplayTTL) {
		r.mu.Unlock()
		select {
		case <-a.done:
			return a, nil
		case <-ctx.Done():
			return &actionAnswer{}, nil
		}
	}
	r.prune(now)
	a := &actionAnswer{done: make(chan struct{})}
	r.byID[key] = a
	r.mu.Unlock()
	return nil, func(status int, body []byte) {
		r.mu.Lock()
		if status == 0 {
			delete(r.byID, key) // did not run: a repeat tries again
		} else {
			a.status, a.body, a.at = status, body, r.now()
		}
		r.mu.Unlock()
		close(a.done)
	}
}

// lookup returns the answer for key (waiting while it is still running) without claiming it:
// nil when no action ran under it within the TTL.
func (r *actionReplays) lookup(ctx context.Context, key string) *actionAnswer {
	r.mu.Lock()
	a, ok := r.byID[key]
	fresh := ok && (a.at.IsZero() || r.now().Sub(a.at) < actionReplayTTL)
	r.mu.Unlock()
	if !fresh {
		return nil
	}
	select {
	case <-a.done:
		if a.status == 0 {
			return nil
		}
		return a
	case <-ctx.Done():
		return nil
	}
}

// prune drops expired answers and, past the cap, the oldest finished ones. Called with mu held.
func (r *actionReplays) prune(now time.Time) {
	for k, a := range r.byID {
		if !a.at.IsZero() && now.Sub(a.at) >= actionReplayTTL {
			delete(r.byID, k)
		}
	}
	for len(r.byID) >= actionReplayMax {
		oldest := ""
		for k, a := range r.byID {
			if !a.at.IsZero() && (oldest == "" || a.at.Before(r.byID[oldest].at)) {
				oldest = k
			}
		}
		if oldest == "" {
			return // all still running: at most one per request in flight
		}
		delete(r.byID, oldest)
	}
}
