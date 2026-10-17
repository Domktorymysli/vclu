package lua

import (
	"context"
	"crypto/rand"
	"fmt"
	"log"
	"time"

	"github.com/Domktorymysli/vclu/pkg/history"
	lua "github.com/yuin/gopher-lua"
)

// RegisterHistoryFunctions installs the history bridge in L (MCP sandbox).
func RegisterHistoryFunctions(L *lua.LState, store *history.Store) {
	registerHistoryFunctions(L, store)
}

// registerHistoryFunctions bridges StateChange and the history API in Lua to
// the Go history.Store. Without a store the functions are absent and
// StateChange falls back to its own ids; nothing else depends on them.
func registerHistoryFunctions(L *lua.LState, store *history.Store) {
	if store == nil {
		return
	}

	// __go_history_record({path, value, old, origin, actor, correlationId})
	// -> id, atMs, change (false: a first reading), known old value or nil,
	// first (nothing was known about the path)
	L.SetGlobal("__go_history_record", L.NewFunction(func(L *lua.LState) int {
		t := L.CheckTable(1)
		e := history.Entry{
			Path:          lua.LVAsString(t.RawGetString("path")),
			Origin:        lua.LVAsString(t.RawGetString("origin")),
			Actor:         lua.LVAsString(t.RawGetString("actor")),
			CorrelationID: lua.LVAsString(t.RawGetString("correlationId")),
		}
		var err error
		if e.Value, err = LuaToGo(t.RawGetString("value")); err == nil {
			e.Old, err = LuaToGo(t.RawGetString("old"))
		}
		if err != nil {
			// The change still happens and is published; only history skips it
			log.Printf("[HISTORY] %s not recorded: %v", e.Path, err)
			id, atMs := store.Stamp()
			L.Push(lua.LString(id))
			L.Push(lua.LNumber(atMs))
			return 2
		}
		r := store.RecordChange(e)
		L.Push(lua.LString(r.ID))
		L.Push(lua.LNumber(r.AtMs))
		L.Push(lua.LBool(r.Change))
		if r.Known {
			L.Push(goValueToLua(L, r.Old))
		} else {
			L.Push(lua.LNil)
		}
		L.Push(lua.LBool(r.First))
		return 5
	}))

	// __go_expect({path, value, dueAtMs, earlyMs, lateMs, origin, actor, correlationId})
	L.SetGlobal("__go_expect", L.NewFunction(func(L *lua.LState) int {
		t := L.CheckTable(1)
		value, err := LuaToGo(t.RawGetString("value"))
		if err != nil {
			return 0
		}
		store.Expectations.Expect(history.Expectation{
			Path:          lua.LVAsString(t.RawGetString("path")),
			Value:         value,
			DueAtMs:       int64(lua.LVAsNumber(t.RawGetString("dueAtMs"))),
			EarlyMs:       int64(lua.LVAsNumber(t.RawGetString("earlyMs"))),
			LateMs:        int64(lua.LVAsNumber(t.RawGetString("lateMs"))),
			Origin:        lua.LVAsString(t.RawGetString("origin")),
			Actor:         lua.LVAsString(t.RawGetString("actor")),
			CorrelationID: lua.LVAsString(t.RawGetString("correlationId")),
		})
		return 0
	}))

	// __go_expect_match(path, value) -> {origin, actor, correlationId} or nil
	L.SetGlobal("__go_expect_match", L.NewFunction(func(L *lua.LState) int {
		value, err := LuaToGo(L.Get(2))
		if err != nil {
			L.Push(lua.LNil)
			return 1
		}
		_, atMs := store.Stamp()
		e, ok := store.Expectations.Match(L.CheckString(1), value, atMs)
		if !ok {
			L.Push(lua.LNil)
			return 1
		}
		t := L.CreateTable(0, 3)
		t.RawSetString("origin", lua.LString(e.Origin))
		t.RawSetString("actor", lua.LString(e.Actor))
		if e.CorrelationID != "" {
			t.RawSetString("correlationId", lua.LString(e.CorrelationID))
		}
		L.Push(t)
		return 1
	}))

	// __go_expect_forget(path)
	L.SetGlobal("__go_expect_forget", L.NewFunction(func(L *lua.LState) int {
		store.Expectations.Forget(L.CheckString(1))
		return 0
	}))

	// __go_history_last(path) -> entry table or nil
	L.SetGlobal("__go_history_last", L.NewFunction(func(L *lua.LState) int {
		e, ok := store.Last(L.CheckString(1))
		if !ok {
			L.Push(lua.LNil)
			return 1
		}
		L.Push(entryToLua(L, e))
		return 1
	}))

	// __go_history_query(path, limit) -> array of entries, newest first
	L.SetGlobal("__go_history_query", L.NewFunction(func(L *lua.LState) int {
		entries := store.Query(history.Query{Path: L.OptString(1, ""), Limit: L.OptInt(2, 0)})
		t := L.CreateTable(len(entries), 0)
		for _, e := range entries {
			t.Append(entryToLua(L, e))
		}
		L.Push(t)
		return 1
	}))
}

func entryToLua(L *lua.LState, e history.Entry) *lua.LTable {
	t := L.CreateTable(0, 8)
	t.RawSetString("id", lua.LString(e.ID))
	t.RawSetString("path", lua.LString(e.Path))
	t.RawSetString("value", goValueToLua(L, e.Value))
	t.RawSetString("old", goValueToLua(L, e.Old))
	t.RawSetString("atMs", lua.LNumber(e.AtMs))
	t.RawSetString("origin", lua.LString(e.Origin))
	t.RawSetString("actor", lua.LString(e.Actor))
	if e.CorrelationID != "" {
		t.RawSetString("correlationId", lua.LString(e.CorrelationID))
	}
	return t
}

// SetHistoryStore connects the history store; call before user modules load.
// The bridge is registered again after every Lua reinitialization.
func (le *LuaEngine) SetHistoryStore(store *history.Store) {
	le.executeFast("SetHistoryStore", func(L *lua.LState) (string, error) {
		le.historyStore = store
		registerHistoryFunctions(L, store)
		return "", nil
	})
}

// ExecuteAs runs script with a StateContext {origin, actor, correlationId}
// for its whole duration, in one work item of the Lua loop; changes it makes
// carry that context. An empty correlationID gets a fresh one.
func (le *LuaEngine) ExecuteAs(origin, actor, correlationID, script string) (string, error) {
	return le.executeSlow("ExecuteAs", func(L *lua.LState) (string, error) {
		defer withStateContext(L, origin, actor, correlationID)()
		return executeOnState(L, script)
	})
}

// ExecuteAsWithTimeout is ExecuteAs with the timeout of ExecuteWithTimeout.
func (le *LuaEngine) ExecuteAsWithTimeout(origin, actor, correlationID, script string, timeout time.Duration) (string, error) {
	return le.executeSlow("ExecuteAsTimeout", func(L *lua.LState) (string, error) {
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()
		L.SetContext(ctx)
		defer L.RemoveContext()
		defer withStateContext(L, origin, actor, correlationID)()

		out, err := executeOnState(L, script)
		if err != nil && ctx.Err() != nil {
			return "", fmt.Errorf("execution timed out after %v", timeout)
		}
		return out, err
	})
}

// WithStateContext is withStateContext for the MCP sandbox.
func WithStateContext(L *lua.LState, origin, actor, correlationID string) func() {
	return withStateContext(L, origin, actor, correlationID)
}

// withStateContext pushes the context (built as a table, never interpolated
// into code) and returns the function restoring the previous stack depth.
func withStateContext(L *lua.LState, origin, actor, correlationID string) func() {
	sc, ok := L.GetGlobal("StateContext").(*lua.LTable)
	if !ok {
		return func() {}
	}
	if correlationID == "" {
		correlationID = NewCorrelationID()
	}
	call := func(name string, args ...lua.LValue) lua.LValue {
		if err := L.CallByParam(lua.P{Fn: L.GetField(sc, name), NRet: 1, Protect: true}, args...); err != nil {
			log.Printf("[STATE] StateContext.%s: %v", name, err)
			return lua.LNil
		}
		ret := L.Get(-1)
		L.Pop(1)
		return ret
	}
	depth := call("depth")
	t := L.NewTable()
	t.RawSetString("origin", lua.LString(origin))
	t.RawSetString("actor", lua.LString(actor))
	t.RawSetString("correlationId", lua.LString(correlationID))
	call("push", t)
	return func() {
		// a timed-out script leaves a cancelled context; restore needs a live
		// one, then the caller's context goes back (the sandbox relies on it)
		ctx := L.RemoveContext()
		call("truncate", depth)
		if ctx != nil {
			L.SetContext(ctx)
		}
	}
}

// flushPersist writes pending persist() changes before the state goes away
// (reload, shutdown); otherwise the last second of changes would be lost.
func flushPersist(L *lua.LState) {
	if L == nil {
		return
	}
	p, ok := L.GetGlobal("Persist").(*lua.LTable)
	if !ok {
		return
	}
	if fn, ok := L.GetField(p, "flushAll").(*lua.LFunction); ok {
		if err := L.CallByParam(lua.P{Fn: fn, NRet: 0, Protect: true}); err != nil {
			log.Printf("[PERSIST] flush: %v", err)
		}
	}
}

// resetStateContext empties the context stack before a work item or timer
// callback: a context belongs to one item, and a push left unpaired by user
// code must not leak into unrelated work.
func resetStateContext(L *lua.LState) {
	sc, ok := L.GetGlobal("StateContext").(*lua.LTable)
	if !ok {
		return
	}
	if fn, ok := L.GetField(sc, "reset").(*lua.LFunction); ok {
		if err := L.CallByParam(lua.P{Fn: fn, NRet: 0, Protect: true}); err != nil {
			log.Printf("[STATE] StateContext.reset: %v", err)
		}
	}
}

// NewCorrelationID returns an id linking a request with the changes it causes.
func NewCorrelationID() string {
	return "c-" + rand.Text()[:12]
}
