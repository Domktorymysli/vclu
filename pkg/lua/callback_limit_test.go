package lua

// Contract (#114): a callback that runs longer than the limit is stopped, and the
// rest of the runtime keeps working.

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Domktorymysli/vclu/pkg/metrics"
	lua "github.com/yuin/gopher-lua"
)

const testLimit = 300 * time.Millisecond

// lockedLog collects the log while a test runs.
type lockedLog struct {
	mu sync.Mutex
	b  strings.Builder
}

func (l *lockedLog) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *lockedLog) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

func captureLog(t *testing.T) *lockedLog {
	t.Helper()
	buf := &lockedLog{}
	prev := log.Writer()
	log.SetOutput(buf)
	t.Cleanup(func() { log.SetOutput(prev) })
	return buf
}

// waitFor polls cond; generous, CI with -race is slow.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func waitLua(t *testing.T, engine *LuaEngine, expr, want string) {
	t.Helper()
	waitFor(t, expr+" == "+want, func() bool {
		got, err := engine.Execute("return tostring(" + expr + ")")
		return err == nil && got == want
	})
}

// startLimited runs a loop with the given limit; __test_started() in Lua signals started.
func startLimited(t *testing.T, limit time.Duration) (*LuaEngine, chan struct{}) {
	t.Helper()
	engine := NewLuaEngine()
	engine.SetCallbackTimeout(limit)
	started := make(chan struct{}, 16)
	engine.state.SetGlobal("__test_started", engine.state.NewFunction(func(L *lua.LState) int {
		select {
		case started <- struct{}{}:
		default:
		}
		return 0
	}))
	stop := make(chan struct{})
	go engine.RunLoop(stop)
	for engineState(engine.engineState.Load()) != stateRunning {
		time.Sleep(time.Millisecond)
	}
	t.Cleanup(func() {
		close(stop)
		engine.Close()
	})
	return engine, started
}

func waitStarted(t *testing.T, started chan struct{}) {
	t.Helper()
	select {
	case <-started:
	case <-time.After(20 * time.Second):
		t.Fatal("callback never started")
	}
}

func TestContract_CallbackLimit_TimerStopped(t *testing.T) {
	logs := captureLog(t)
	engine, started := startLimited(t, testLimit)

	mustExec(t, engine, `
		c = { n = 0 }
		setTimeout(function() __test_started() while true do end end, 1)
		setTimeout(function() c.n = c.n + 1 end, 50)
	`)
	waitStarted(t, started)

	// queued while the loop runs, answered once it is stopped
	if got := mustExec(t, engine, `c.queued = true return "ran"`); got != "ran" {
		t.Fatalf("work item queued meanwhile: got %q", got)
	}
	waitLua(t, engine, "c.n", "1")

	out := logs.String()
	if !strings.Contains(out, "Stopped timer") || !strings.Contains(out, "300ms callback time limit") {
		t.Errorf("log does not name the timer and the limit:\n%s", out)
	}
	if strings.Contains(out, "KILLED") {
		t.Errorf("misleading KILLED message still logged:\n%s", out)
	}
}

func TestContract_CallbackLimit_EventHandlerStopsTheRestOfItsWorkItem(t *testing.T) {
	engine, _ := startLimited(t, testLimit)

	mustExec(t, engine, `
		c = {}
		EventBus:getShared():on("t114:loop", function() while true do end end)
		EventBus:getShared():on("t114:loop", function() c.second = true end)
		EventBus:getShared():on("t114:ok", function() c.ok = true end)
	`)
	_, err := engine.Execute(`EventBus:getShared():emit("t114:loop") c.after = true`)
	if err == nil || !strings.Contains(err.Error(), "callback time limit") {
		t.Fatalf("emit with a looping handler: err = %v", err)
	}
	// one deadline per work item: the handlers after the looping one do not run
	if got := mustExec(t, engine, `return tostring(c.second) .. "," .. tostring(c.after)`); got != "nil,nil" {
		t.Errorf("after the stop: second handler, rest of item = %s, want nil,nil", got)
	}
	mustExec(t, engine, `EventBus:getShared():emit("t114:ok")`)
	if got := mustExec(t, engine, `return tostring(c.ok)`); got != "true" {
		t.Errorf("next event after a stop: c.ok = %s", got)
	}
}

func TestContract_CallbackLimit_HTTPHandler500(t *testing.T) {
	engine, _ := startLimited(t, testLimit)

	mustExec(t, engine, `
		HTTPServer:on("/t114/loop", "GET", function(req) while true do end end)
		HTTPServer:on("/t114/ok", "GET", function(req) return { status = 200, body = "fine" } end)
	`)
	get := func(path string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		engine.httpServerManager.handleRequest(rec, httptest.NewRequest(http.MethodGet, path, nil))
		return rec
	}
	if rec := get("/t114/loop"); rec.Code != http.StatusInternalServerError {
		t.Fatalf("looping handler: status %d, want 500", rec.Code)
	}
	if rec := get("/t114/ok"); rec.Code != http.StatusOK || rec.Body.String() != "fine" {
		t.Errorf("handler after a stop: %d %q", rec.Code, rec.Body.String())
	}
}

func TestContract_CallbackLimit_PcallDoesNotSwallowTheStop(t *testing.T) {
	engine, _ := startLimited(t, testLimit)
	mustExec(t, engine, `c = {}`)

	for _, script := range []string{
		`pcall(function() while true do end end) c.after = true`,
		`while true do pcall(function() end) end`,
		`while true do pcall(error, "x") end`,
		`while true do pcall(function() while true do end end) end`,
		`xpcall(function() while true do end end, function(e) return e end) c.after = true`,
	} {
		_, err := engine.Execute(script)
		if err == nil || !strings.Contains(err.Error(), "callback time limit") {
			t.Errorf("%s: err = %v, want stopped", script, err)
		}
	}
	if got := mustExec(t, engine, `return tostring(c.after)`); got != "nil" {
		t.Errorf("code after a stopped pcall ran: c.after = %s", got)
	}
}

// Decision: a stopped interval keeps its schedule.
func TestContract_CallbackLimit_IntervalKeepsTicking(t *testing.T) {
	engine, _ := startLimited(t, testLimit)
	mustExec(t, engine, `
		c = { n = 0 }
		setInterval(function()
			c.n = c.n + 1
			if c.n == 1 then while true do end end
		end, 20)
	`)
	waitLua(t, engine, "c.n >= 3", "true")
}

func TestContract_CallbackLimit_SyncHTTPEndsAtTheLimit(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/fast" {
			_, _ = w.Write([]byte("ok"))
			return
		}
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))
	t.Cleanup(srv.Close)
	t.Cleanup(func() { close(release) })

	logs := captureLog(t)
	engine, _ := startLimited(t, testLimit)

	// under the limit: as before
	mustExec(t, engine, `
		c = {}
		setTimeout(function()
			local r = HTTP:new():get("`+srv.URL+`/fast")
			c.status, c.body = r.status, r.body
		end, 1)
	`)
	waitLua(t, engine, "c.body", "ok")
	if got := mustExec(t, engine, `return tostring(c.status)`); got != "200" {
		t.Errorf("fast request: status %s", got)
	}

	// a hanging server: the request ends with the callback, not after its 30 s timeout
	mustExec(t, engine, `
		setTimeout(function()
			HTTP:new():get("`+srv.URL+`/hang")
			c.after = true
		end, 1)
	`)
	waitFor(t, "the stop of the hanging request", func() bool {
		return strings.Contains(logs.String(), "Stopped timer")
	})
	if got := mustExec(t, engine, `return tostring(c.after)`); got != "nil" {
		t.Errorf("callback went on after its limit: c.after = %s", got)
	}
}

func TestContract_CallbackLimit_ExecuteWithTimeoutKeepsItsOwn(t *testing.T) {
	engine, _ := startLimited(t, 5*time.Second)
	_, err := engine.ExecuteWithTimeout(`while true do end`, 200*time.Millisecond)
	if err == nil || !strings.Contains(err.Error(), "timed out after 200ms") {
		t.Errorf("shorter own timeout: err = %v", err)
	}

	engine2, _ := startLimited(t, 100*time.Millisecond)
	out, err := engine2.ExecuteAsWithTimeout("mcp", "agent", "", `local t = os.clock() + 0.4 while os.clock() < t do end return 7`, 20*time.Second)
	if err != nil || out != "7" {
		t.Errorf("longer own timeout cut by the limit: %q, %v", out, err)
	}
	// its error is its own, not the limit's
	_, err = engine2.ExecuteAsWithTimeout("mcp", "agent", "", `local t = os.clock() + 0.4 while os.clock() < t do end error("own")`, 20*time.Second)
	if err == nil || !strings.Contains(err.Error(), "own") || strings.Contains(err.Error(), "callback time limit") {
		t.Errorf("error after the limit under its own timeout: %v", err)
	}
	// the limit is back for the next item
	if _, err := engine2.Execute(`while true do end`); err == nil || !strings.Contains(err.Error(), "callback time limit") {
		t.Errorf("item after ExecuteWithTimeout: err = %v", err)
	}
}

func TestCallbackLimit_OwnTimeoutRestoresPrevious(t *testing.T) {
	L := lua.NewState()
	defer L.Close()
	var outer context.Context
	stopped, err := runLimited(L, time.Minute, func() error {
		outer = L.Context()
		_, restore := withOwnTimeout(L, time.Second)
		if L.Context() == outer {
			t.Error("own timeout did not replace the context")
		}
		restore()
		if L.Context() != outer {
			t.Error("previous context not restored")
		}
		return nil
	})
	if stopped || err != nil || outer == nil {
		t.Fatalf("stopped=%v err=%v outer=%v", stopped, err, outer)
	}
	if L.Context() != nil {
		t.Error("context left on the state after runLimited")
	}
}

func TestContract_CallbackLimit_ModuleLoading(t *testing.T) {
	dir := createTestModulesDir(t, map[string]string{
		"a_loop.lua": `while true do end`,
		"b_ok.lua":   `_bLoaded = true`,
	})
	check := func(t *testing.T, engine *LuaEngine) {
		results := engine.LoadUserModules(dir)
		byName := map[string]ModuleLoadResult{}
		for _, r := range results {
			byName[r.Name] = r
		}
		if r := byName["a_loop.lua"]; r.OK || !strings.Contains(r.Error, "callback time limit") {
			t.Errorf("looping module: %+v", r)
		}
		if r := byName["b_ok.lua"]; !r.OK {
			t.Errorf("module after it: %+v", r)
		}
		if got := mustExec(t, engine, `return tostring(_bLoaded)`); got != "true" {
			t.Errorf("_bLoaded = %s", got)
		}
	}
	t.Run("start", func(t *testing.T) {
		engine := NewLuaEngine()
		defer engine.Close()
		engine.SetCallbackTimeout(testLimit)
		check(t, engine)
	})
	t.Run("reload", func(t *testing.T) {
		engine, _ := startLimited(t, testLimit)
		check(t, engine)
	})
}

func TestContract_CallbackLimit_PluginTimerNamesPlugin(t *testing.T) {
	logs := captureLog(t)
	engine, _ := startLimited(t, testLimit)
	mustExec(t, engine, `
		PluginSandbox:create("@test/looper")
		local _, e = PluginSandbox:execute("@test/looper", [[
			setTimeout(1, function() while true do end end)
		]])
		if e then error(e) end
	`)
	waitFor(t, "the plugin's timer stopped", func() bool {
		return strings.Contains(logs.String(), "(plugin @test/looper)")
	})
	if out := logs.String(); !strings.Contains(out, "300ms callback time limit") {
		t.Errorf("log does not name the limit:\n%s", out)
	}
}

func TestContract_CallbackLimit_ModuleTimerNamesItsLine(t *testing.T) {
	logs := captureLog(t)
	dir := createTestModulesDir(t, map[string]string{
		"t114_spin.lua": "\nafter(1, function()\n  while true do end\nend)\n",
	})
	engine, _ := startLimited(t, testLimit)
	engine.LoadUserModules(dir)
	waitFor(t, "the module's timer stopped", func() bool {
		return strings.Contains(logs.String(), "Stopped timer")
	})
	if out := logs.String(); !strings.Contains(out, "t114_spin.lua:2)") {
		t.Errorf("log does not name the module line:\n%s", out)
	}
}

func TestCallbackLimit_StateUsableAfterStop(t *testing.T) {
	engine, started := startLimited(t, testLimit)
	top := func() string {
		got, err := engine.executeFast("top", func(L *lua.LState) (string, error) {
			return fmt.Sprint(L.GetTop()), nil
		})
		if err != nil {
			t.Fatal(err)
		}
		return got
	}
	before := top()
	mustExec(t, engine, `setTimeout(function() __test_started() StateContext.push({ origin = "x" }) while true do end end, 1)`)
	waitStarted(t, started)
	if after := top(); after != before {
		t.Errorf("stack top after a stop: %s, before %s", after, before)
	}
	checks := map[string]string{
		`return 1 + 1`: "2",
		`local ok, e = pcall(error, "boom") return tostring(ok) .. tostring(e:find("boom") ~= nil)`:                                              "falsetrue",
		`local ok, e = pcall(coroutine.wrap(function() error("wx") end)) return tostring(ok) .. tostring(e:find("wx") ~= nil)`:                   "falsetrue",
		`local co = coroutine.create(function() error("cx") end) local ok, e = coroutine.resume(co) return tostring(ok) .. coroutine.status(co)`: "falsedead",
		`return tostring(StateContext.depth())`: "0",
		`local co = coroutine.wrap(function(a) local b = coroutine.yield(a + 1) return b * 2 end)
		 return co(1) .. "," .. co(5)`: "2,10",
	}
	for script, want := range checks {
		if got := mustExec(t, engine, script); got != want {
			t.Errorf("%s = %q, want %q", script, got, want)
		}
	}
	mustExec(t, engine, `c = {} setTimeout(function() c.timer = true end, 1)`)
	waitLua(t, engine, "c.timer", "true")
}

func TestContract_CallbackLimit_Coroutines(t *testing.T) {
	engine, _ := startLimited(t, testLimit)

	// created in one callback, resumed in later ones: not dead with the first one's deadline
	mustExec(t, engine, `
		c = { steps = {} }
		co = coroutine.create(function()
			for i = 1, 3 do c.steps[#c.steps + 1] = i coroutine.yield() end
		end)
		wrapped = coroutine.wrap(function()
			for i = 1, 3 do c.wrapped = i coroutine.yield() end
		end)
		coroutine.resume(co) wrapped()
	`)
	for i := 0; i < 2; i++ {
		mustExec(t, engine, `
			local ok, e = coroutine.resume(co)
			if not ok then error(e) end
			wrapped()
		`)
	}
	if got := mustExec(t, engine, `return #c.steps .. "," .. c.wrapped`); got != "3,3" {
		t.Errorf("resumed across work items: %s, want 3,3", got)
	}

	// a loop inside a coroutine made in an earlier item is stopped with the item resuming it
	mustExec(t, engine, `spin = coroutine.wrap(function() while true do end end)`)
	if _, err := engine.Execute(`spin()`); err == nil || !strings.Contains(err.Error(), "callback time limit") {
		t.Errorf("loop in a coroutine: err = %v", err)
	}
	mustExec(t, engine, `loopco = coroutine.create(function() while true do end end)`)
	if _, err := engine.Execute(`coroutine.resume(loopco) c.after = true`); err == nil || !strings.Contains(err.Error(), "callback time limit") {
		t.Errorf("loop in a resumed coroutine: err = %v", err)
	}
}

func TestCallbackLimit_MetricGetterStopped(t *testing.T) {
	logs := captureLog(t)
	engine := NewLuaEngine()
	defer engine.Close()
	engine.SetCallbackTimeout(testLimit)
	engine.SetMetricsCollector(metrics.NewCollector(t.TempDir()))
	mustExec(t, engine, `metric("t114:loop", function() while true do end end, { interval = 1 })`)

	engine.collectMetrics()
	if out := logs.String(); !strings.Contains(out, "Stopped metric collection") {
		t.Errorf("looping metric getter not stopped:\n%s", out)
	}
	if got := mustExec(t, engine, `return 1 + 1`); got != "2" {
		t.Errorf("state after a stopped getter: %s", got)
	}
}

func TestCallbackLimit_Setting(t *testing.T) {
	engine := NewLuaEngine()
	defer engine.Close()
	for _, c := range []struct{ set, want time.Duration }{
		{0, DefaultCallbackTimeout},
		{5 * time.Second, 5 * time.Second},
		{-1, 0},
		{-time.Hour, 0},
	} {
		engine.SetCallbackTimeout(c.set)
		if got := engine.callbackTimeout(); got != c.want {
			t.Errorf("SetCallbackTimeout(%v): limit %v, want %v", c.set, got, c.want)
		}
	}
	if got := limitText(DefaultCallbackTimeout); got != "60s" {
		t.Errorf("limit in the log: %s", got)
	}
	// no limit: nothing is stopped
	engine.SetCallbackTimeout(-1)
	if got := mustExec(t, engine, `local t = os.clock() + 0.05 while os.clock() < t do end return "done"`); got != "done" {
		t.Errorf("without a limit: %s", got)
	}
}

// go test -run XXX -bench CallbackLimit ./pkg/lua
func BenchmarkCallbackLimit(b *testing.B) {
	L := lua.NewState()
	defer L.Close()
	compute, err := L.LoadString(`local s = 0 for i = 1, 100000 do s = s + i % 7 end return s`)
	if err != nil {
		b.Fatal(err)
	}
	empty, _ := L.LoadString(`return 1`)
	call := func(fn *lua.LFunction) func() error {
		return func() error { return L.CallByParam(lua.P{Fn: fn, NRet: 0, Protect: true}) }
	}
	for _, c := range []struct {
		name  string
		fn    *lua.LFunction
		limit time.Duration
	}{
		{"compute/none", compute, 0},
		{"compute/limit", compute, time.Minute},
		{"empty/none", empty, 0},
		{"empty/limit", empty, time.Minute},
	} {
		b.Run(c.name, func(b *testing.B) {
			run := call(c.fn)
			for i := 0; i < b.N; i++ {
				if _, err := runLimited(L, c.limit, run); err != nil {
					b.Fatal(err)
				}
			}
		})
	}

	// a typical callback: an event with a few handlers on the full runtime
	engine := NewLuaEngine()
	defer engine.Close()
	R := engine.state
	if err := R.DoString(`
		bench = { n = 0, log = {} }
		for i = 1, 3 do
			EventBus:getShared():on("bench", function(v) bench.n = bench.n + v bench.log[i] = tostring(v) end)
		end
	`); err != nil {
		b.Fatal(err)
	}
	emit, _ := R.LoadString(`for i = 1, 20 do EventBus:getShared():emit("bench", i) end`)
	for _, limit := range []time.Duration{0, time.Minute} {
		name := "events/none"
		if limit > 0 {
			name = "events/limit"
		}
		b.Run(name, func(b *testing.B) {
			run := func() error { return R.CallByParam(lua.P{Fn: emit, NRet: 0, Protect: true}) }
			for i := 0; i < b.N; i++ {
				if _, err := runLimited(R, limit, run); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
