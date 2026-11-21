package web

import (
	"log"
	"sync"
	"time"

	"github.com/Domktorymysli/vclu/pkg/notify"
)

// lockout locks a key after Tries wrong ones in a row: Wait, then twice as long each time up
// to WaitMax; a right one clears it. Set the fields before the first use.
type lockout struct {
	Tries         int
	Wait, WaitMax time.Duration
	Forget        time.Duration                        // an unlocked count this long without tries is dropped; 0 keeps it
	OnLock        func(key string, wait time.Duration) // a lock started, outside the lock's mutex

	mu  sync.Mutex
	m   map[string]*lockCount
	now func() time.Time
}

type lockCount struct {
	n, locks int
	inFlight int // tries begun and not yet counted
	until    time.Time
	last     time.Time // the last try
}

const (
	lockTries  = 5 // of the PIN and the login
	lockoutMax = 1024
	// lockoutBusyWait is the answer to a try over the ones left while others are checked
	lockoutBusyWait = time.Second
)

// dashPinLocks lock a dashboard's PIN for everyone after five wrong ones in a row, as a
// device's PIN is (Cloud.lua): 30 s, then twice as long each time, up to an hour; the right
// PIN clears it. Per person alone, many people (or addresses) would guess in parallel.
var dashPinLocks = newPinLockout()

func newPinLockout() *lockout {
	return &lockout{Tries: lockTries, Wait: 30 * time.Second, WaitMax: time.Hour, m: map[string]*lockCount{}, now: time.Now,
		OnLock: func(key string, wait time.Duration) {
			log.Printf("[Dashboard] %s: %d wrong PINs in a row, locked for %s", key, lockTries, wait)
		}}
}

// newLoginLockout locks the panel's login per address the same way, up to 15 minutes; a count
// is forgotten after an hour without tries.
func (s *Server) newLoginLockout() *lockout {
	return &lockout{Tries: lockTries, Wait: 30 * time.Second, WaitMax: 15 * time.Minute, Forget: time.Hour,
		m: map[string]*lockCount{}, now: time.Now,
		OnLock: func(ip string, wait time.Duration) {
			log.Printf("[AUTH] %s: %d failed logins in a row, locked for %s", ip, lockTries, wait)
			s.notifyLock(notify.MsgLockLogin, "login:"+ip, map[string]string{"ip": ip}, wait)
		}}
}

// locked is whether key takes no tries now.
func (p *lockout) locked(key string) bool { return p.remaining(key) > 0 }

// remaining is how long key stays locked, 0 when it is not.
func (p *lockout) remaining(key string) time.Duration {
	p.mu.Lock()
	defer p.mu.Unlock()
	if l := p.entry(key, false); l != nil {
		return max(l.until.Sub(p.now()), 0)
	}
	return 0
}

// begin reserves a try of key, to be counted by result: tries checked at the same time
// never pass the ones left before the lock. It gives the wait when no try is left now.
func (p *lockout) begin(key string) time.Duration {
	p.mu.Lock()
	defer p.mu.Unlock()
	l := p.entry(key, true)
	now := p.now()
	if wait := l.until.Sub(now); wait > 0 {
		return wait
	}
	if l.n+l.inFlight >= p.Tries {
		return lockoutBusyWait
	}
	l.inFlight++
	l.last = now
	return 0
}

// result counts a try: a right one clears key, the last wrong one in a row locks it. It gives
// the lock this try started, 0 for none.
func (p *lockout) result(key string, ok bool) time.Duration {
	wait := p.count(key, ok)
	if wait > 0 && p.OnLock != nil {
		p.OnLock(key, wait)
	}
	return wait
}

func (p *lockout) count(key string, ok bool) time.Duration {
	p.mu.Lock()
	defer p.mu.Unlock()
	if ok {
		delete(p.m, key)
		return 0
	}
	l := p.entry(key, true)
	l.inFlight = max(l.inFlight-1, 0)
	l.last = p.now()
	if l.n++; l.n >= p.Tries {
		wait := min(p.Wait<<min(l.locks, 20), p.WaitMax)
		l.n, l.locks, l.until = 0, l.locks+1, p.now().Add(wait)
		return wait
	}
	return 0
}

// release gives back a try begun and not counted (the check failed).
func (p *lockout) release(key string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if l := p.m[key]; l != nil {
		l.inFlight = max(l.inFlight-1, 0)
	}
}

// entry is key's count, made when create; one past Forget is dropped first. Call under mu.
func (p *lockout) entry(key string, create bool) *lockCount {
	now := p.now()
	l := p.m[key]
	if l != nil && p.stale(l, now) {
		delete(p.m, key)
		l = nil
	}
	if l != nil || !create {
		return l
	}
	if len(p.m) >= lockoutMax { // made-up ids must not grow it without end: drop the unlocked
		for k, v := range p.m {
			if !now.Before(v.until) && v.inFlight == 0 {
				delete(p.m, k)
			}
		}
	}
	l = &lockCount{}
	p.m[key] = l
	return l
}

func (p *lockout) stale(l *lockCount, now time.Time) bool {
	return p.Forget > 0 && l.inFlight == 0 && !now.Before(l.until) && now.Sub(l.last) >= p.Forget
}
