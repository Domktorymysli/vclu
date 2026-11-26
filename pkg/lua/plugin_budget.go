package lua

import (
	"context"
	"fmt"
	"log"
	"path/filepath"
	"strings"
	"sync"
	"time"

	lua "github.com/yuin/gopher-lua"
)

// A plugin's code shares the run loop with everything else, so it gets less of one callback
// than a module: its time in a callback is sampled from the Lua stack, and over the budget
// the plugin's frames raise until they are gone, while the module's code goes on.
const (
	DefaultPluginCallBudget = 2 * time.Second  // plugin code in one callback
	DefaultPluginLoadBudget = 10 * time.Second // its init.lua and init
	pluginOverrunsToSuspend = 3                // overruns in pluginOverrunWindow suspend it
	pluginOverrunWindow     = 10 * time.Minute
	budgetSampleMask        = 4095 // instructions between samples, minus one
)

var closedChan = func() chan struct{} { c := make(chan struct{}); close(c); return c }()

// PluginUsage is a plugin's time on the run loop since it was loaded, and its overruns.
type PluginUsage struct {
	CPU       time.Duration
	Since     time.Time
	Overruns  int  // in all
	Suspended bool // over the budget too often; reloading it does not resume it
}

type pluginUsage struct {
	PluginUsage
	recent  []time.Time
	loading bool
}

// pluginBudget: the plugins' environments (what tells their frames) and their usage.
type pluginBudget struct {
	mu          sync.Mutex
	envs        map[*lua.LTable]string
	byID        map[string]*lua.LTable
	usage       map[string]*pluginUsage
	call, load  time.Duration
	runtimePath string
	onSuspend   func(pluginID string)
	proxyReads  map[*lua.FunctionProto]int // the sandbox's proxy read methods -> their owner upvalue
}

func newPluginBudget(runtimePath string) *pluginBudget {
	return &pluginBudget{
		envs: map[*lua.LTable]string{}, byID: map[string]*lua.LTable{}, usage: map[string]*pluginUsage{},
		call: DefaultPluginCallBudget, load: DefaultPluginLoadBudget, runtimePath: filepath.Clean(runtimePath),
		proxyReads: map[*lua.FunctionProto]int{},
	}
}

// budgets: the budget of each engine's state, for runLimited and the context helpers.
var budgets sync.Map // *lua.LState -> *pluginBudget

func budgetOf(L *lua.LState) *pluginBudget {
	if b, ok := budgets.Load(L); ok {
		return b.(*pluginBudget)
	}
	return nil
}

// active: some plugin is registered, so callbacks are sampled.
func (b *pluginBudget) active() bool {
	if b == nil {
		return false
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.envs) > 0
}

// reset forgets the environments of a closed state; usage and suspensions stay.
func (b *pluginBudget) reset() {
	b.mu.Lock()
	b.envs, b.byID = map[*lua.LTable]string{}, map[string]*lua.LTable{}
	b.proxyReads = map[*lua.FunctionProto]int{}
	b.mu.Unlock()
}

func (b *pluginBudget) register(id string, env *lua.LTable) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if old := b.byID[id]; old != nil {
		delete(b.envs, old)
	}
	b.envs[env], b.byID[id] = id, env
	u := b.usage[id]
	if u == nil {
		u = &pluginUsage{}
		b.usage[id] = u
	}
	u.CPU, u.Since, u.loading = 0, time.Now(), false
}

func (b *pluginBudget) forget(id string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if env := b.byID[id]; env != nil {
		delete(b.envs, env)
	}
	delete(b.byID, id)
}

func (b *pluginBudget) setLoading(id string, loading bool) {
	b.mu.Lock()
	if u := b.usage[id]; u != nil {
		u.loading = loading
	}
	b.mu.Unlock()
}

// resume clears a suspension and the overruns that led to it.
func (b *pluginBudget) resume(id string) {
	b.mu.Lock()
	if u := b.usage[id]; u != nil {
		u.Suspended, u.recent = false, nil
	}
	b.mu.Unlock()
}

func (b *pluginBudget) snapshot() map[string]PluginUsage {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make(map[string]PluginUsage, len(b.usage))
	for id, u := range b.usage {
		out[id] = u.PluginUsage
	}
	return out
}

// charge adds d to the plugin; the budget it has in this callback, and whether it is suspended.
func (b *pluginBudget) charge(id string, d time.Duration) (time.Duration, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	u := b.usage[id]
	if u == nil {
		return b.call, false
	}
	u.CPU += d
	if u.loading {
		return b.load, u.Suspended
	}
	return b.call, u.Suspended
}

// overrun counts one; the pluginOverrunsToSuspend-th in the window suspends the plugin.
func (b *pluginBudget) overrun(id string, limit time.Duration) {
	b.mu.Lock()
	u := b.usage[id]
	if u == nil {
		b.mu.Unlock()
		return
	}
	now := time.Now()
	u.Overruns++
	recent := u.recent[:0]
	for _, t := range u.recent {
		if now.Sub(t) < pluginOverrunWindow {
			recent = append(recent, t)
		}
	}
	u.recent = append(recent, now)
	suspend := !u.Suspended && len(u.recent) >= pluginOverrunsToSuspend
	if suspend {
		u.Suspended = true
	}
	n, fn := len(u.recent), b.onSuspend
	b.mu.Unlock()
	log.Printf("[PLUGINS] %s: callback over %s, stopped (%d of %d in %d min)", id, limitText(limit), n, pluginOverrunsToSuspend, int(pluginOverrunWindow.Minutes()))
	if suspend {
		log.Printf("[PLUGINS] %s suspended: over its time limit %d times in %d min; the plugins page turns it on again", id, n, int(pluginOverrunWindow.Minutes()))
		if fn != nil {
			go fn(id)
		}
	}
}

// owner: the plugin whose code runs, the innermost plugin frame unless a module's frame
// comes first (a module's callback the plugin calls is the module's time); "" otherwise.
func (b *pluginBudget) owner(L *lua.LState) string {
	b.mu.Lock()
	defer b.mu.Unlock()
	for level := 0; ; level++ {
		dbg, ok := L.GetStack(level)
		if !ok {
			return ""
		}
		f, err := L.GetInfo("Sf", dbg, lua.LNil)
		fn, _ := f.(*lua.LFunction)
		if err != nil || fn == nil {
			continue
		}
		if id, ok := b.envs[fn.Env]; ok {
			return id
		}
		if !fn.IsG && b.isModule(dbg.Source) {
			return ""
		}
	}
}

// isModule: a frame from a file outside the runtime (modules/, user.lua); scripts run
// through the engine ("<string>") and the runtime pass the time on to their caller.
func (b *pluginBudget) isModule(source string) bool {
	if source == "" || strings.HasPrefix(source, "<") || b.runtimePath != "" && strings.HasPrefix(source, b.runtimePath) {
		return false
	}
	return strings.HasSuffix(source, ".lua")
}

// onStack: a frame of the plugin is on L's stack.
func (b *pluginBudget) onStack(L *lua.LState, id string) bool {
	b.mu.Lock()
	env := b.byID[id]
	b.mu.Unlock()
	if env == nil {
		return false
	}
	for level := 0; ; level++ {
		dbg, ok := L.GetStack(level)
		if !ok {
			return false
		}
		if f, err := L.GetInfo("f", dbg, lua.LNil); err == nil {
			if fn, ok := f.(*lua.LFunction); ok && fn.Env == env {
				return true
			}
		}
	}
}

// budgetRun: one top-level callback, where each plugin's time is counted.
type budgetRun struct {
	b       *pluginBudget
	n       uint32
	last    time.Time
	prev    string // owner at the last sample: time between two samples of one owner is its
	spent   map[string]time.Duration
	charged time.Duration // by the samples and the timed calls in all (a timed call takes it off)
	tripped string
	err     error
	raising bool
}

func (b *pluginBudget) newRun() *budgetRun {
	return &budgetRun{b: b, last: time.Now(), spent: map[string]time.Duration{}}
}

// sample charges the time since the last one; true when the owner just went over its budget.
func (r *budgetRun) sample(L *lua.LState) bool {
	now := time.Now()
	d := now.Sub(r.last)
	r.last = now
	owner := r.b.owner(L)
	same := owner != "" && owner == r.prev
	r.prev = owner
	if owner == "" {
		return false
	}
	if !same {
		d = 0
	}
	return r.add(owner, d)
}

// add charges d to owner; true when it just went over its budget.
func (r *budgetRun) add(owner string, d time.Duration) bool {
	limit, suspended := r.b.charge(owner, d)
	r.spent[owner] += d
	r.charged += d
	if !suspended && r.spent[owner] <= limit {
		return false
	}
	r.tripped = owner
	r.err = fmt.Errorf("%s: callback over %s, stopped", owner, limitText(limit))
	if suspended {
		r.err = fmt.Errorf("%s is suspended: it went over its time limit", owner)
	} else {
		r.b.overrun(owner, limit)
	}
	return true
}

// goCall charges the time of one Go call to the plugin whose frame made it: it ran between
// two instructions, where the samples see nothing, and a callback of a few instructions is
// never sampled at all.
func (r *budgetRun) goCall(L *lua.LState, d time.Duration) {
	if r.tripped != "" || d <= 0 {
		return
	}
	owner := r.b.owner(L)
	if owner == "" {
		return
	}
	r.last, r.prev = time.Now(), owner
	if r.add(owner, d) {
		r.raising = true
	}
}

// timedGoFunction: fn, with each call's time (less what the samples charged inside it, a
// gsub's Lua callback) charged to the plugin calling it.
func timedGoFunction(fn lua.LGFunction) lua.LGFunction {
	return func(L *lua.LState) int {
		bc, ok := L.Context().(*budgetCtx)
		if !ok {
			return fn(L)
		}
		before, start := bc.run.charged, time.Now()
		defer func() { bc.run.goCall(L, time.Since(start)-(bc.run.charged-before)) }()
		return fn(L)
	}
}

// timedFunction: f as timedGoFunction makes it, with its upvalues; a Lua function stays as it is.
func timedFunction(L *lua.LState, f *lua.LFunction) *lua.LFunction {
	if !f.IsG {
		return f
	}
	ups := make([]lua.LValue, len(f.Upvalues))
	for i, u := range f.Upvalues {
		ups[i] = u.Value()
	}
	return L.NewClosure(timedGoFunction(f.GFunction), ups...)
}

// budgetCtx: the callback's context with the plugins' budget. gopher-lua asks Done before
// every instruction: every budgetSampleMask+1 instructions it samples, and once a plugin went
// over, it is done while that plugin's frames are on the stack. Only the Lua goroutine may
// use it (baseContext for Go code).
type budgetCtx struct {
	context.Context
	L    *lua.LState
	run  *budgetRun
	done <-chan struct{} // the parent's, asked once: Done runs before every instruction
}

func newBudgetCtx(L *lua.LState, parent context.Context, run *budgetRun) *budgetCtx {
	return &budgetCtx{Context: parent, L: L, run: run, done: parent.Done()}
}

func (c *budgetCtx) Done() <-chan struct{} {
	r := c.run
	if r.tripped != "" {
		r.raising = r.b.onStack(c.L, r.tripped)
		if r.raising {
			return closedChan
		}
		return c.done
	}
	r.n++
	if r.n&budgetSampleMask == 0 && r.sample(c.L) {
		r.raising = true
		return closedChan
	}
	return c.done
}

func (c *budgetCtx) Err() error {
	if c.run.raising {
		return c.run.err
	}
	return c.Context.Err()
}

// baseContext: ctx without the budget, for Go code that may use it on another goroutine.
func baseContext(ctx context.Context) context.Context {
	if bc, ok := ctx.(*budgetCtx); ok {
		return bc.Context
	}
	return ctx
}

// withBudget puts the budget on ctx: the run of an outer budget context, a new one for a
// top-level callback; ctx itself when no plugin is registered.
func withBudget(L *lua.LState, ctx, outer context.Context) context.Context {
	if bc, ok := outer.(*budgetCtx); ok {
		return newBudgetCtx(L, ctx, bc.run)
	}
	if b := budgetOf(L); b.active() {
		return newBudgetCtx(L, ctx, b.newRun())
	}
	return ctx
}

// registerPluginBudgetFunctions: PluginSandbox registers each plugin's environment, and the
// loader marks its load (the larger budget).
func registerPluginBudgetFunctions(L *lua.LState, b *pluginBudget) {
	budgets.Store(L, b)
	// __go_plugin_budget_register(pluginId, env)
	L.SetGlobal("__go_plugin_budget_register", L.NewFunction(func(L *lua.LState) int {
		b.register(L.CheckString(1), L.CheckTable(2))
		return 0
	}))
	// __go_plugin_budget_forget(pluginId): on cleanup
	L.SetGlobal("__go_plugin_budget_forget", L.NewFunction(func(L *lua.LState) int {
		b.forget(L.CheckString(1))
		return 0
	}))
	// __go_plugin_budget_loading(pluginId, bool): its init.lua and init run under the load budget
	L.SetGlobal("__go_plugin_budget_loading", L.NewFunction(func(L *lua.LState) int {
		b.setLoading(L.CheckString(1), L.ToBool(2))
		return 0
	}))
	// __go_plugin_timed(fn) -> fn with its time charged to the plugin calling it (the sandbox's string library)
	L.SetGlobal("__go_plugin_timed", L.NewFunction(func(L *lua.LState) int {
		L.Push(timedFunction(L, L.CheckFunction(1)))
		return 1
	}))
	// __go_proxy_read_method(fn): fn is made like every read method of a read-only proxy
	L.SetGlobal("__go_proxy_read_method", L.NewFunction(func(L *lua.LState) int {
		fn := L.CheckFunction(1)
		for i, name := range fn.Proto.DbgUpvalues {
			if name == "owner" {
				b.mu.Lock()
				b.proxyReads[fn.Proto] = i
				b.mu.Unlock()
				return 0
			}
		}
		L.RaiseError("__go_proxy_read_method: no owner upvalue")
		return 0
	}))
	// __go_clu_caller() -> viaProxy, pluginId | nil: whose request to a CLU this is
	L.SetGlobal("__go_clu_caller", L.NewFunction(func(L *lua.LState) int {
		proxy, owner := b.cluCaller(L)
		L.Push(lua.LBool(proxy))
		L.Push(owner)
		return 2
	}))
}

// cluCaller: a read through a proxy is its owner's (false: not known), from the stack, so an
// error or a stopped callback leaves nothing behind. Else the plugin whose frame comes first,
// before a module's frame or a tail call (which hides who called).
func (b *pluginBudget) cluCaller(L *lua.LState) (bool, lua.LValue) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for level := 0; ; level++ {
		dbg, ok := L.GetStack(level)
		if !ok {
			break
		}
		f, err := L.GetInfo("f", dbg, lua.LNil)
		fn, _ := f.(*lua.LFunction)
		if err != nil || fn == nil || fn.IsG {
			continue
		}
		if i, ok := b.proxyReads[fn.Proto]; ok && i < len(fn.Upvalues) {
			owner := fn.Upvalues[i].Value()
			if owner.Type() != lua.LTString {
				owner = lua.LNil
			}
			return true, owner
		}
	}
	for level := 0; ; level++ {
		dbg, ok := L.GetStack(level)
		if !ok {
			return false, lua.LNil
		}
		f, err := L.GetInfo("Sf", dbg, lua.LNil)
		fn, _ := f.(*lua.LFunction)
		if err != nil || fn == nil || fn.IsG {
			continue
		}
		if id, ok := b.envs[fn.Env]; ok {
			return false, lua.LString(id)
		}
		if dbg.What == "tail" || b.isModule(dbg.Source) {
			return false, lua.LNil
		}
	}
}

// SetPluginTimeBudget: plugin code's time in one callback and at load; 0 keeps the default.
func (le *LuaEngine) SetPluginTimeBudget(call, load time.Duration) {
	b := le.pluginBudget
	b.mu.Lock()
	if call > 0 {
		b.call = call
	}
	if load > 0 {
		b.load = load
	}
	b.mu.Unlock()
}

// SetOnPluginSuspend: called, on its own goroutine, when a plugin went over its time limit
// too often; the caller unloads it and keeps it off until the user turns it on.
func (le *LuaEngine) SetOnPluginSuspend(fn func(pluginID string)) {
	b := le.pluginBudget
	b.mu.Lock()
	b.onSuspend = fn
	b.mu.Unlock()
}

// PluginUsage: each plugin's time on the run loop and overruns.
func (le *LuaEngine) PluginUsage() map[string]PluginUsage {
	return le.pluginBudget.snapshot()
}

// ResumePlugin clears a plugin's suspension; loading it is the caller's.
func (le *LuaEngine) ResumePlugin(pluginID string) {
	le.pluginBudget.resume(pluginID)
}
