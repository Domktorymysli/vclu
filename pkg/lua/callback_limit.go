package lua

import (
	"context"
	"fmt"
	"log"
	"sync/atomic"
	"time"

	lua "github.com/yuin/gopher-lua"
)

// DefaultCallbackTimeout is how long one callback on the run loop may run: above the
// synchronous waits it may contain (HTTP 30 s, a CLU answer 5 s). System work that
// blocks longer by design (autoSubscribe, LoadCLUData) runs without the limit.
const DefaultCallbackTimeout = 60 * time.Second

// callbackSeq counts top-level callbacks; Lua tells a guard left by a stopped one by it.
var callbackSeq atomic.Uint64

// SetCallbackTimeout sets the callback time limit: 0 the default, negative no limit.
func (le *LuaEngine) SetCallbackTimeout(d time.Duration) {
	if d < 0 {
		d = -1
	}
	le.callbackTimeoutNs.Store(int64(d))
}

// callbackTimeout is the limit in effect; 0 means no limit.
func (le *LuaEngine) callbackTimeout() time.Duration {
	switch d := time.Duration(le.callbackTimeoutNs.Load()); {
	case d == 0:
		return DefaultCallbackTimeout
	case d < 0:
		return 0
	default:
		return d
	}
}

// runLimited runs fn with L stopped once limit passes (gopher-lua checks the context
// between VM instructions). stopped is true when fn failed after the limit.
// Under an outer context (nested call) fn runs under that one.
func runLimited(L *lua.LState, limit time.Duration, fn func() error) (stopped bool, err error) {
	if L.Context() != nil {
		return false, fn()
	}
	callbackSeq.Add(1)
	if limit <= 0 {
		return false, fn()
	}
	ctx, cancel := context.WithTimeout(context.Background(), limit)
	L.SetContext(ctx)
	defer func() {
		L.RemoveContext()
		cancel()
	}()
	err = fn()
	// the context in effect at the end: __vclu_without_limit may have renewed an expired one
	cur := L.Context()
	return err != nil && cur != nil && cur.Err() != nil, err
}

// withOwnTimeout gives L a deadline of its own, not bound by the callback limit;
// the returned function puts the previous context back.
func withOwnTimeout(L *lua.LState, timeout time.Duration) (context.Context, func()) {
	prev := L.Context()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	L.SetContext(ctx)
	return ctx, func() {
		cancel()
		if prev != nil {
			L.SetContext(prev)
		} else {
			L.RemoveContext()
		}
	}
}

// registerCoroutineContext makes a coroutine run under the context of the code that
// resumes it: gopher-lua binds it at creation, so one created in a callback would die
// with that callback and one created at load would never be stopped.
func registerCoroutineContext(L *lua.LState) {
	co, ok := L.GetGlobal("coroutine").(*lua.LTable)
	if !ok {
		return
	}
	resume, ok1 := co.RawGetString("resume").(*lua.LFunction)
	wrap, ok2 := co.RawGetString("wrap").(*lua.LFunction)
	if !ok1 || !ok2 || !resume.IsG || !wrap.IsG {
		return
	}
	co.RawSetString("resume", L.NewFunction(func(L *lua.LState) int {
		if th, ok := L.Get(1).(*lua.LState); ok {
			shareContext(L, th)
		}
		return resume.GFunction(L)
	}))
	co.RawSetString("wrap", L.NewFunction(func(L *lua.LState) int {
		wrap.GFunction(L)
		aux, ok := L.Get(-1).(*lua.LFunction)
		if !ok || len(aux.Upvalues) != 1 {
			return 1
		}
		th, ok := aux.Upvalues[0].Value().(*lua.LState)
		if !ok {
			return 1
		}
		L.Pop(1)
		// the original reads the thread from upvalue 1 of the running function: this one's
		L.Push(L.NewClosure(func(L *lua.LState) int {
			shareContext(L, th)
			return aux.GFunction(L)
		}, th))
		return 1
	}))
}

// registerLimitFunctions: __vclu_without_limit(fn, ...) runs fn with no callback limit
// (system work with budgets of its own); __go_callback_seq() numbers the running callback.
func (le *LuaEngine) registerLimitFunctions(L *lua.LState) {
	L.SetGlobal("__vclu_without_limit", L.NewFunction(func(L *lua.LState) int {
		fn := L.CheckFunction(1)
		prev := L.RemoveContext()
		defer func() {
			if prev == nil {
				return
			}
			// the caller gets its limit anew: the time without one is not its own
			if limit := le.callbackTimeout(); prev.Err() != nil && limit > 0 {
				var cancel context.CancelFunc
				prev, cancel = context.WithTimeout(context.Background(), limit)
				time.AfterFunc(limit, cancel)
			}
			L.SetContext(prev)
		}()
		base := L.GetTop()
		L.Push(fn)
		for i := 2; i <= base; i++ {
			L.Push(L.Get(i))
		}
		L.Call(base-1, lua.MultRet)
		return L.GetTop() - base
	}))
	L.SetGlobal("__go_callback_seq", L.NewFunction(func(L *lua.LState) int {
		L.Push(lua.LNumber(callbackSeq.Load()))
		return 1
	}))
}

func shareContext(from, to *lua.LState) {
	if ctx := from.Context(); ctx != nil {
		to.SetContext(ctx)
	} else {
		to.RemoveContext()
	}
}

// logStopped reports a callback stopped at the limit.
func logStopped(name string, limit time.Duration, err error) {
	log.Printf("[RUNTIME] Stopped %s: ran over the %s callback time limit: %v", name, limitText(limit), err)
}

// limitText prints 60s, not 1m0s.
func limitText(d time.Duration) string {
	if d >= time.Second && d%time.Second == 0 {
		return fmt.Sprintf("%ds", d/time.Second)
	}
	return d.String()
}
