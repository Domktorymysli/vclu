package lua

import (
	"log"
	"sync"
	"time"

	glua "github.com/yuin/gopher-lua"
)

// Timer limits to prevent system abuse
const (
	MaxTimers     = 1000                // Maximum number of active timers
	MinIntervalMs = 10                  // Minimum interval for repeating timers (10ms)
	MinTimeoutMs  = 1                   // Minimum timeout (1ms)
	MaxIntervalMs = 24 * 60 * 60 * 1000 // Maximum interval (24 hours)
)

// TimerType defines whether a timer is one-shot or repeating
type TimerType int

const (
	TimerTypeTimeout  TimerType = 0 // One-shot timer
	TimerTypeInterval TimerType = 1 // Repeating timer
)

// Timer represents a registered timer
type Timer struct {
	ID       int
	Type     TimerType
	Period   time.Duration
	Callback glua.LValue // Lua function to call
	NextFire time.Time
	Active   bool
	Pending  bool // True if timer is queued but not yet executed (prevents re-queuing)
}

// TimerStats holds timer statistics
type TimerStats struct {
	ActiveTimers   int
	TotalCreated   int
	TotalFired     int
	TotalCancelled int
	Rejected       int // Rejected due to limits
}

// TimerManager manages timers for the Lua engine
type TimerManager struct {
	mu        sync.Mutex
	clock     Clock
	timers    map[int]*Timer
	nextID    int
	fireQueue chan int // Queue of timer IDs to fire
	stopChan  chan struct{}
	running   bool

	// Statistics
	stats TimerStats
}

// NewTimerManager creates a new timer manager
func NewTimerManager() *TimerManager {
	return &TimerManager{
		clock:     RealClock{},
		timers:    make(map[int]*Timer),
		nextID:    1,
		fireQueue: make(chan int, 1000), // Buffer for timer events (increased from 100)
		stopChan:  make(chan struct{}),
	}
}

// Stats returns current timer statistics
func (tm *TimerManager) Stats() TimerStats {
	tm.mu.Lock()
	defer tm.mu.Unlock()
	tm.stats.ActiveTimers = len(tm.timers)
	return tm.stats
}

// Start begins the timer processing goroutine
func (tm *TimerManager) Start() {
	tm.mu.Lock()
	if tm.running {
		tm.mu.Unlock()
		return
	}
	tm.running = true
	tm.mu.Unlock()

	go tm.timerLoop()
}

// Stop stops the timer processing goroutine
func (tm *TimerManager) Stop() {
	tm.mu.Lock()
	if !tm.running {
		tm.mu.Unlock()
		return
	}
	tm.running = false
	tm.mu.Unlock()

	close(tm.stopChan)
}

// timerLoop is the main timer processing goroutine
func (tm *TimerManager) timerLoop() {
	ticker := time.NewTicker(10 * time.Millisecond) // Check every 10ms
	defer ticker.Stop()

	for {
		select {
		case <-tm.stopChan:
			return
		case <-ticker.C:
			tm.checkTimers()
		}
	}
}

// checkTimers checks all timers and queues any that are due
func (tm *TimerManager) checkTimers() {
	tm.mu.Lock()
	defer tm.mu.Unlock()

	now := tm.clock.Now()
	for id, timer := range tm.timers {
		if !timer.Active || timer.Pending {
			continue
		}

		if now.After(timer.NextFire) || now.Equal(timer.NextFire) {
			// Timer is due - queue it for firing
			select {
			case tm.fireQueue <- id:
				timer.Pending = true
			default:
				// Queue full, skip this tick
			}

			if timer.Type == TimerTypeInterval {
				timer.NextFire = now.Add(timer.Period)
			} else {
				timer.Active = false
			}
		}
	}
}

// SetTimeout creates a one-shot timer
// Returns timer ID on success, or -1 if limits exceeded
func (tm *TimerManager) SetTimeout(callback glua.LValue, periodMs int) int {
	tm.mu.Lock()
	defer tm.mu.Unlock()

	// Check timer limit
	if len(tm.timers) >= MaxTimers {
		log.Printf("[TIMER] REJECTED SetTimeout: too many timers (%d)", len(tm.timers))
		tm.stats.Rejected++
		return -1
	}

	// Enforce minimum timeout
	if periodMs < MinTimeoutMs {
		periodMs = MinTimeoutMs
	}

	// Enforce maximum
	if periodMs > MaxIntervalMs {
		periodMs = MaxIntervalMs
	}

	id := tm.nextID
	tm.nextID++

	tm.timers[id] = &Timer{
		ID:       id,
		Type:     TimerTypeTimeout,
		Period:   time.Duration(periodMs) * time.Millisecond,
		Callback: callback,
		NextFire: tm.clock.Now().Add(time.Duration(periodMs) * time.Millisecond),
		Active:   true,
	}

	tm.stats.TotalCreated++
	return id
}

// SetInterval creates a repeating timer
// Returns timer ID on success, or -1 if limits exceeded
func (tm *TimerManager) SetInterval(callback glua.LValue, periodMs int) int {
	tm.mu.Lock()
	defer tm.mu.Unlock()

	// Check timer limit
	if len(tm.timers) >= MaxTimers {
		log.Printf("[TIMER] REJECTED SetInterval: too many timers (%d)", len(tm.timers))
		tm.stats.Rejected++
		return -1
	}

	// Enforce minimum interval (prevent CPU abuse)
	if periodMs < MinIntervalMs {
		log.Printf("[TIMER] SetInterval: period %dms too fast, using minimum %dms", periodMs, MinIntervalMs)
		periodMs = MinIntervalMs
	}

	// Enforce maximum
	if periodMs > MaxIntervalMs {
		periodMs = MaxIntervalMs
	}

	id := tm.nextID
	tm.nextID++

	tm.timers[id] = &Timer{
		ID:       id,
		Type:     TimerTypeInterval,
		Period:   time.Duration(periodMs) * time.Millisecond,
		Callback: callback,
		NextFire: tm.clock.Now().Add(time.Duration(periodMs) * time.Millisecond),
		Active:   true,
	}

	tm.stats.TotalCreated++
	return id
}

// ClearTimer cancels a timer
func (tm *TimerManager) ClearTimer(id int) {
	tm.mu.Lock()
	defer tm.mu.Unlock()

	if timer, ok := tm.timers[id]; ok {
		timer.Active = false
		delete(tm.timers, id)
		tm.stats.TotalCancelled++
	}
}

// ClearAll cancels all timers (for reinit/shutdown)
func (tm *TimerManager) ClearAll() {
	tm.mu.Lock()
	defer tm.mu.Unlock()

	tm.timers = make(map[int]*Timer)
}

// GetCallback returns the callback for a timer (for execution)
func (tm *TimerManager) GetCallback(id int) glua.LValue {
	tm.mu.Lock()
	defer tm.mu.Unlock()

	if timer, ok := tm.timers[id]; ok {
		return timer.Callback
	}
	return glua.LNil
}

// MarkFired increments the fired counter and clears pending flag
// (called after callback execution)
func (tm *TimerManager) MarkFired(id int) {
	tm.mu.Lock()
	defer tm.mu.Unlock()
	tm.stats.TotalFired++

	// Clear pending flag so interval timer can be queued again
	if timer, ok := tm.timers[id]; ok {
		timer.Pending = false
	}
}

// CleanupTimer removes a one-shot timer after it fires
func (tm *TimerManager) CleanupTimer(id int) {
	tm.mu.Lock()
	defer tm.mu.Unlock()

	if timer, ok := tm.timers[id]; ok {
		if timer.Type == TimerTypeTimeout {
			delete(tm.timers, id)
		}
	}
}

// GetPendingTimerID returns the next timer ID to fire, or 0 if none
// This is non-blocking
func (tm *TimerManager) GetPendingTimerID() int {
	select {
	case id := <-tm.fireQueue:
		return id
	default:
		return 0
	}
}
