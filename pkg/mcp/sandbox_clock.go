package mcp

import (
	"container/heap"
	"context"
	"fmt"
	"time"

	vclulua "github.com/Domktorymysli/vclu/pkg/lua"
	lua "github.com/yuin/gopher-lua"
)

// Same limits as the production TimerManager (pkg/lua/timers.go).
const (
	sandboxMinTimeoutMs  = vclulua.MinTimeoutMs
	sandboxMinIntervalMs = vclulua.MinIntervalMs
	// maxCallbacksPerAdvance stops a runaway timer loop within one advance step;
	// a 1 s interval over a whole day still fits.
	maxCallbacksPerAdvance = 100000
	// maxCallbacksPerScenario bounds CPU spent by all advance steps together.
	maxCallbacksPerScenario = 300000
)

// sandboxClock is the virtual time of one scenario; it only moves on advance.
type sandboxClock struct{ now time.Time }

func (c *sandboxClock) Now() time.Time { return c.now }

type sandboxTimer struct {
	id       int
	deadline time.Time
	period   time.Duration
	interval bool
	fn       *lua.LFunction
	index    int
}

// timerHeap orders timers by (deadline, id) so equal deadlines fire in creation order.
type timerHeap []*sandboxTimer

func (h timerHeap) Len() int { return len(h) }
func (h timerHeap) Less(i, j int) bool {
	if h[i].deadline.Equal(h[j].deadline) {
		return h[i].id < h[j].id
	}
	return h[i].deadline.Before(h[j].deadline)
}
func (h timerHeap) Swap(i, j int) {
	h[i], h[j] = h[j], h[i]
	h[i].index, h[j].index = i, j
}
func (h *timerHeap) Push(x any) {
	t := x.(*sandboxTimer)
	t.index = len(*h)
	*h = append(*h, t)
}
func (h *timerHeap) Pop() any {
	old := *h
	t := old[len(old)-1]
	*h = old[:len(old)-1]
	t.index = -1
	return t
}

// sandboxTimers replaces __go_setTimeout/__go_setInterval/__go_clearTimer:
// timers fire only when the scenario advances the virtual clock.
type sandboxTimers struct {
	ctx    context.Context // stops advance when the scenario times out
	budget *int            // callbacks left for the whole scenario
	clock  *sandboxClock
	queue  timerHeap
	byID   map[int]*sandboxTimer
	nextID int
}

func newSandboxTimers(clock *sandboxClock) *sandboxTimers {
	budget := maxCallbacksPerScenario
	return &sandboxTimers{ctx: context.Background(), budget: &budget, clock: clock, byID: map[int]*sandboxTimer{}, nextID: 1}
}

// install registers the timer functions and the virtual clock in L.
// os.time()/os.date() without a time argument read the virtual clock.
func (st *sandboxTimers) install(L *lua.LState) error {
	vclulua.SetClock(L, st.clock)
	add := func(interval bool, minMs int) *lua.LFunction {
		return L.NewFunction(func(L *lua.LState) int {
			fn := L.CheckFunction(1)
			ms := L.CheckInt(2)
			if len(st.byID) >= vclulua.MaxTimers {
				L.Push(lua.LNumber(-1)) // production rejects the timer the same way
				return 1
			}
			ms = min(max(ms, minMs), vclulua.MaxIntervalMs)
			L.Push(lua.LNumber(st.add(fn, time.Duration(ms)*time.Millisecond, interval)))
			return 1
		})
	}
	L.SetGlobal("__go_setTimeout", add(false, sandboxMinTimeoutMs))
	L.SetGlobal("__go_setInterval", add(true, sandboxMinIntervalMs))
	L.SetGlobal("__go_clearTimer", L.NewFunction(func(L *lua.LState) int {
		st.clear(L.CheckInt(1))
		return 0
	}))
	L.SetGlobal("__go_time_ms", L.NewFunction(func(L *lua.LState) int {
		L.Push(lua.LNumber(st.clock.now.UnixMilli()))
		return 1
	}))
	return L.DoString(`
		local stdTime, stdDate = os.time, os.date
		os.time = function(t)
			if t == nil then return math.floor(__go_time_ms() / 1000) end
			return stdTime(t)
		end
		os.date = function(format, t)
			if t == nil then t = math.floor(__go_time_ms() / 1000) end
			return stdDate(format or "%c", t)
		end
	`)
}

func (st *sandboxTimers) add(fn *lua.LFunction, period time.Duration, interval bool) int {
	t := &sandboxTimer{
		id:       st.nextID,
		deadline: st.clock.now.Add(period),
		period:   period,
		interval: interval,
		fn:       fn,
	}
	st.nextID++
	st.byID[t.id] = t
	heap.Push(&st.queue, t)
	return t.id
}

func (st *sandboxTimers) clear(id int) {
	t, ok := st.byID[id]
	if !ok {
		return
	}
	delete(st.byID, id)
	if t.index >= 0 {
		heap.Remove(&st.queue, t.index)
	}
}

// advance moves the clock by d, firing every timer due in that window in
// (deadline, id) order, including ones created or rescheduled by callbacks.
// Callback errors are reported through onErr and do not stop the step.
func (st *sandboxTimers) advance(L *lua.LState, d time.Duration, onErr func(error)) error {
	target := st.clock.now.Add(d)
	fired := 0
	for st.queue.Len() > 0 && !st.queue[0].deadline.After(target) {
		if err := st.ctx.Err(); err != nil {
			return err
		}
		if fired >= maxCallbacksPerAdvance {
			st.clock.now = target
			return fmt.Errorf("advance stopped after %d timer callbacks (runaway timer loop?)", fired)
		}
		if *st.budget <= 0 {
			st.clock.now = target
			return fmt.Errorf("scenario stopped: %d timer callbacks in total", maxCallbacksPerScenario)
		}
		*st.budget--
		t := heap.Pop(&st.queue).(*sandboxTimer)
		if t.deadline.After(st.clock.now) {
			st.clock.now = t.deadline
		}
		if t.interval {
			t.deadline = t.deadline.Add(t.period)
			heap.Push(&st.queue, t)
		} else {
			delete(st.byID, t.id)
		}
		fired++
		if err := L.CallByParam(lua.P{Fn: t.fn, NRet: 0, Protect: true}); err != nil {
			onErr(fmt.Errorf("timer %d: %w", t.id, err))
		}
	}
	st.clock.now = target
	return nil
}
