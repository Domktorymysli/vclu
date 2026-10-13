package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"runtime/metrics"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/Domktorymysli/vclu/pkg/config"
	"github.com/Domktorymysli/vclu/pkg/history"
	vclulua "github.com/Domktorymysli/vclu/pkg/lua"
	"github.com/Domktorymysli/vclu/pkg/workspace"
	lua "github.com/yuin/gopher-lua"
	luaparse "github.com/yuin/gopher-lua/parse"
)

// sandboxTimeout is a var so tests can shorten it.
var sandboxTimeout = 5 * time.Second

// sandboxMemoryLimit stops a draft test whose heap grows by more than this:
// the sandbox shares the process with production, and a runaway loop would
// otherwise get vCLU killed by the OOM killer within seconds. A single Go call
// (e.g. table.concat of huge strings) can still overshoot it.
var sandboxMemoryLimit = defaultSandboxMemoryLimit()

// defaultSandboxMemoryLimit is 256 MB, or 15% of RAM on smaller machines
// (the peak can reach about twice the limit), but not below 64 MB.
func defaultSandboxMemoryLimit() uint64 {
	limit := uint64(256 << 20)
	if total := totalRAM(); total > 0 && total*15/100 < limit {
		limit = max(total*15/100, 64<<20)
	}
	return limit
}

// totalRAM reads MemTotal from /proc/meminfo; 0 when unknown.
func totalRAM() uint64 {
	data, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return 0
	}
	for _, line := range strings.Split(string(data), "\n") {
		if f := strings.Fields(line); len(f) >= 2 && f[0] == "MemTotal:" {
			kb, err := strconv.ParseUint(f[1], 10, 64)
			if err == nil {
				return kb << 10
			}
		}
	}
	return 0
}

var errSandboxMemory = errors.New("sandbox memory limit exceeded")

// heapBytes reads the heap size (garbage included) without stopping the world.
func heapBytes() uint64 { return readMetric("/memory/classes/heap/objects:bytes") }

// liveBytes is the heap the last GC found alive; allocations made during or
// after that GC do not count.
func liveBytes() uint64 { return readMetric("/gc/heap/live:bytes") }

// allocatedBytes is the total ever allocated on the heap.
func allocatedBytes() uint64 { return readMetric("/gc/heap/allocs:bytes") }

func readMetric(name string) uint64 {
	s := []metrics.Sample{{Name: name}}
	metrics.Read(s)
	if s[0].Value.Kind() != metrics.KindUint64 {
		return 0
	}
	return s[0].Value.Uint64()
}

func memoryError(limit uint64) error {
	return fmt.Errorf("%w (%d MB): the draft allocates without bound", errSandboxMemory, limit>>20)
}

// guardForcedGCs counts GCs forced by guardMemory (for tests).
var guardForcedGCs atomic.Int64

// guardMemory cancels ctx when the heap grows past limit over base (live
// bytes after a GC, see RunScenario). The heap metric counts garbage, so a
// crossing is confirmed with the live heap after a GC; after a false alarm
// the next forced GC waits 250 ms unless the heap passes twice the limit,
// which the normal GC cycle (heap up to about 2x live) does not reach.
func guardMemory(ctx context.Context, cancel context.CancelCauseFunc, limit, base uint64) {
	over := func(h uint64) bool { return h > base && h-base > limit }
	var quietUntil time.Time
	t := time.NewTicker(20 * time.Millisecond)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-t.C:
			h := heapBytes()
			if !over(h) || now.Before(quietUntil) && h-base < 2*limit {
				continue
			}
			// the draft keeps allocating during the GC and that counts as live:
			// subtract it, a real leak was over the limit before the GC
			a0 := allocatedBytes()
			runtime.GC()
			guardForcedGCs.Add(1)
			live, during := liveBytes(), allocatedBytes()-a0
			// during also counts the sweep and other goroutines; past 3x the
			// limit live alone decides
			if live > during && over(live-during) || live > base && live-base > 3*limit {
				cancel(errSandboxMemory)
				return
			}
			quietUntil = time.Now().Add(250 * time.Millisecond)
		}
	}
}

const (
	maxStateChanges    = 1000
	maxBlockedCalls    = 100
	maxCluCalls        = 1000
	maxSandboxLogLines = 500
)

// blockedFunctions is the list of Lua functions forbidden in draft code.
var blockedFunctions = []string{
	"os.execute", "os.remove", "os.rename", "os.exit",
	"io.open", "io.read", "io.write", "io.close", "io.popen",
	"loadfile", "dofile",
}

// SandboxEngine runs draft Lua code in an isolated environment.
type SandboxEngine struct {
	runtimePath string               // Lua runtime path (bootstrap_go.lua etc.)
	cluDataPath string               // CLU data path (a/ directory with om.lua, user.lua)
	serverDir   string               // server dir (for modules, proxy files, imports)
	ws          *workspace.Workspace // workspace for reading drafts
	// objectSource returns SandboxObjects.export() of the live runtime
	objectSource func() (string, error)
}

// NewSandboxEngine creates a new sandbox engine.
func NewSandboxEngine(runtimePath, cluDataPath, serverDir string, ws *workspace.Workspace) *SandboxEngine {
	return &SandboxEngine{
		runtimePath: runtimePath,
		cluDataPath: cluDataPath,
		serverDir:   serverDir,
		ws:          ws,
	}
}

// RunDraft executes one or more draft files in the sandbox and returns the result.
func (se *SandboxEngine) RunDraft(draftPaths []string, injectEvents []InjectEvent) (*SandboxResult, error) {
	return se.RunScenario(draftPaths, SandboxScenario{Events: injectEvents})
}

// RunScenario loads the drafts, injects events and runs the scenario steps.
func (se *SandboxEngine) RunScenario(draftPaths []string, sc SandboxScenario) (*SandboxResult, error) {
	start := time.Now()
	for _, p := range draftPaths {
		if err := se.ws.CheckDraftPath(p); err != nil {
			return nil, err
		}
	}

	result := &SandboxResult{
		Env:          "sandbox",
		StateChanges: []StateChange{},
		BlockedCalls: []BlockedCall{},
		CluCalls:     []CluCall{},
		Logs:         []string{},
		Errors:       []string{},
	}

	// Run with timeout and a memory guard
	tctx, cancelTimeout := context.WithTimeout(context.Background(), sandboxTimeout)
	defer cancelTimeout()
	ctx, cancel := context.WithCancelCause(tctx)
	defer cancel(nil)
	memLimit := sandboxMemoryLimit
	runtime.GC() // a base without leftover garbage, the same every run
	go guardMemory(ctx, cancel, memLimit, liveBytes())

	done := make(chan error, 1)
	go func() {
		done <- se.runScenario(ctx, draftPaths, sc, result, memLimit)
	}()

	select {
	case err := <-done:
		result.DurationMs = time.Since(start).Milliseconds()
		if errors.Is(context.Cause(ctx), errSandboxMemory) {
			err = memoryError(memLimit)
			debug.FreeOSMemory()
		}
		if err != nil {
			result.Errors = append(result.Errors, err.Error())
		} else {
			result.Success = len(result.Errors) == 0
		}
		return result, nil

	case <-ctx.Done():
		// The context aborts Lua and advance; result is only safe to read once
		// the goroutine has returned.
		timeoutErr := fmt.Sprintf("sandbox timeout (%s)", sandboxTimeout)
		if errors.Is(context.Cause(ctx), errSandboxMemory) {
			timeoutErr = memoryError(memLimit).Error()
			defer debug.FreeOSMemory()
		}
		select {
		case <-done:
			result.Errors = append(result.Errors, timeoutErr)
		case <-time.After(time.Second):
			result = &SandboxResult{Env: "sandbox", Errors: []string{timeoutErr}}
		}
		result.Success = false
		result.DurationMs = time.Since(start).Milliseconds()
		return result, nil
	}
}

// sandboxRun is one scenario: the virtual clock, KV and recorded calls outlive
// a reload step, the Lua state and its timers do not.
type sandboxRun struct {
	memLimit uint64 // heap growth that stops the scenario
	ctx      context.Context
	se       *SandboxEngine
	drafts   []string
	result   *SandboxResult
	clock    *sandboxClock
	kv       *sandboxKV
	objects  string // JSON array of object descriptors
	timezone string // datetime.setTimezone of the live runtime or the scenario
	budget   int    // timer callbacks left for the scenario
	// loggedErrors are Logger:error messages; new ones during a step fail it
	loggedErrors []string
	timers       *sandboxTimers
	history      *history.Store // per scenario; survives a reload step
	snapshot     map[string]any
	loads        int
}

// runScenario does the actual sandbox execution (called in goroutine).
func (se *SandboxEngine) runScenario(ctx context.Context, draftPaths []string, sc SandboxScenario, result *SandboxResult, memLimit uint64) error {
	at := time.Now()
	if !sc.At.IsZero() {
		at = sc.At
	}
	run := &sandboxRun{
		memLimit: memLimit,
		ctx:      ctx,
		budget:   maxCallbacksPerScenario,
		se:       se,
		drafts:   draftPaths,
		result:   result,
		clock:    &sandboxClock{now: at},
		kv:       newSandboxKV(),
	}
	run.history = history.New(history.DefaultConfig(), run.clock.Now)
	if err := run.resolveObjects(sc.Fixtures); err != nil {
		return err
	}
	if sc.Timezone != "" {
		run.timezone = sc.Timezone
	}

	L, err := run.load()
	if L != nil {
		defer func() { L.Close() }()
	}
	if err != nil {
		return err
	}

	// Inject events on EventBus
	cluMark := len(result.CluCalls)
	for _, evt := range sc.Events {
		run.loggedErrors = run.loggedErrors[:0]
		if err := se.injectEvent(L, evt); err != nil {
			result.Errors = append(result.Errors, fmt.Sprintf("event injection failed: %v", err))
		}
		for _, e := range run.loggedErrors {
			result.Errors = append(result.Errors, fmt.Sprintf("event %s: %s", evt.Name, e))
		}
	}

	if L, err = run.runSteps(L, sc.Steps, cluMark); err != nil {
		return err
	}

	// Compute state changes (diff snapshot vs current)
	result.StateChanges = se.diffState(L, run.snapshot)
	return nil
}

// load builds a fresh Lua state: bootstrap, om.lua, proxies, integration
// objects, then the drafts. The first load also takes the state snapshot.
func (run *sandboxRun) load() (*lua.LState, error) {
	se, result := run.se, run.result
	run.loads++

	// 1. Create isolated Lua state; the scenario timeout aborts running Lua
	L := lua.NewState()
	L.SetContext(run.ctx)

	// Disable dangerous globals
	L.SetGlobal("dofile", lua.LNil)
	L.SetGlobal("loadfile", lua.LNil)

	L.SetGlobal("_VCLU_RUNTIME_PATH", lua.LString(se.runtimePath))
	L.SetGlobal("_CLU", lua.LString("VCLU"))
	L.SetGlobal("_SANDBOX", lua.LTrue)

	// 2. Register sandbox-specific print (captures output)
	L.SetGlobal("print", L.NewFunction(func(L *lua.LState) int {
		args := make([]string, L.GetTop())
		for i := 1; i <= L.GetTop(); i++ {
			args[i-1] = L.Get(i).String()
		}
		msg := strings.Join(args, "\t")
		if len(result.Logs) < maxSandboxLogLines {
			result.Logs = append(result.Logs, msg)
		}
		return 0
	}))

	// 3. Virtual clock and timers: time stands still, timers fire only on advance
	run.timers = newSandboxTimers(run.clock)
	run.timers.ctx, run.timers.budget = run.ctx, &run.budget
	if err := run.timers.install(L); err != nil {
		return L, fmt.Errorf("sandbox clock: %w", err)
	}

	// Pure libraries (json, xml, datetime, color) as in production; no side effects
	vclulua.RegisterParserFunctions(L)
	vclulua.RegisterHistoryFunctions(L, run.history)

	// 4. Register blocked stubs for side-effect systems
	se.registerBlockedStubs(L, result, run.memLimit)

	// 5. In-memory KV, shared by every load of this scenario
	run.kv.install(L)

	// 6. Register parser stubs
	L.SetGlobal("__go_parse_config_txt", L.NewFunction(func(L *lua.LState) int {
		L.Push(lua.LString("{}"))
		return 1
	}))
	L.SetGlobal("__go_parse_om_file", L.NewFunction(func(L *lua.LState) int {
		L.Push(lua.LString("{}"))
		return 1
	}))

	// 7. Load bootstrap (initializes core Lua modules: Logger, EventBus, CLU, etc.)
	bootstrapPath := se.runtimePath + "/bootstrap_go.lua"
	if err := L.DoFile(bootstrapPath); err != nil {
		return L, fmt.Errorf("bootstrap failed: %w", err)
	}

	// MQTT is blocked here, so its 100 ms poller would only burn timer
	// callbacks on every advance step
	if err := L.DoString(`
		if MQTT and MQTT._pollTimerId then
			__go_clearTimer(MQTT._pollTimerId)
			MQTT._pollTimerId = nil
		end
	`); err != nil {
		return L, fmt.Errorf("sandbox MQTT poller: %w", err)
	}

	// 8. Logger prints, and print is captured above; __sandbox_log is for
	// messages from the sandbox itself
	L.SetGlobal("__sandbox_log", L.NewFunction(func(L *lua.LState) int {
		msg := L.CheckString(1)
		if len(result.Logs) < maxSandboxLogLines {
			result.Logs = append(result.Logs, msg)
		}
		return 0
	}))

	// 8b. Errors the runtime only logs (a failing EventBus/StateBus handler)
	// are collected so a scenario step that causes one fails. The list is
	// emptied before every step, so the cap applies per step.
	L.SetGlobal("__sandbox_error", L.NewFunction(func(L *lua.LState) int {
		if len(run.loggedErrors) < maxBlockedCalls {
			run.loggedErrors = append(run.loggedErrors, L.CheckString(1))
		}
		return 0
	}))
	if err := L.DoString(`
		-- Logger:error (class), Logger:getShared():error and log.error
		local function hook(owner, format)
			local orig = owner and rawget(owner, "error")
			if not orig then return end
			owner.error = function(...)
				local ok, msg = pcall(format, ...)
				__sandbox_error(ok and msg or tostring((select(2, ...))))
				return orig(...)
			end
		end
		local loggerFormat = function(self, message, ...) return self:format(message, ...) end
		hook(Logger, loggerFormat)
		local shared = Logger:getShared()
		if shared ~= Logger then hook(shared, loggerFormat) end
		hook(log, function(fmt, ...) return string.format(fmt, ...) end)
	`); err != nil {
		return L, fmt.Errorf("sandbox logger: %w", err)
	}

	// 9. package.path, user.lua, then om.lua: the order of LoadCLUData, since
	// om.lua event bindings may reference functions from user.lua
	packageScript := fmt.Sprintf(`package.path = "%s/?.lua;" .. package.path`, se.cluDataPath)
	if err := L.DoString(packageScript); err != nil {
		log.Printf("[SANDBOX] failed to set package.path: %v", err)
	}
	userScript := `
		local ok, err = pcall(function() require "user" end)
		if not ok then
			__sandbox_log("[WARN] user.lua skipped: " .. tostring(err))
		end
	`
	if err := L.DoString(userScript); err != nil {
		log.Printf("[SANDBOX] failed to load user.lua: %v", err)
	}

	// 10. Load om.lua (creates CLU objects — safe, in-memory only)
	omPath := se.cluDataPath + "/om.lua"
	if err := L.DoFile(omPath); err != nil {
		return L, fmt.Errorf("om.lua failed: %w", err)
	}

	// 11. Init SYSTEM (fires EVENT_ON_INIT — handlers run with blocked stubs)
	if err := L.DoString("SYSTEM.Init()"); err != nil {
		return L, fmt.Errorf("SYSTEM.Init failed: %w", err)
	}

	// 11b. Proxy files register CLU objects (CLU220000592.DOU4992...) as in
	// production; they are only read here, never regenerated.
	proxies, _ := filepath.Glob(filepath.Join(config.GetOMFilesDir(se.serverDir), "proxy_*.lua"))
	for _, p := range proxies {
		if err := L.DoFile(p); err != nil {
			result.Logs = append(result.Logs, fmt.Sprintf("[WARN] %s skipped: %v", filepath.Base(p), err))
		}
	}

	// 11c. Integration objects (satel.zone.N...) from the live snapshot or fixtures
	if err := run.loadObjects(L); err != nil {
		return L, err
	}

	// 11d. Zone of the live runtime (set by modules/init.lua there), so drafts
	// see the same local hour without loading init.lua
	if run.timezone != "" {
		fn := L.GetField(L.GetGlobal("datetime"), "setTimezone")
		if err := L.CallByParam(lua.P{Fn: fn, NRet: 0, Protect: true}, lua.LString(run.timezone)); err != nil {
			return L, fmt.Errorf("timezone %q: %w", run.timezone, err)
		}
	}

	// 12. Take state snapshot BEFORE loading draft (for state_changes diff)
	if run.snapshot == nil {
		run.snapshot = se.takeStateSnapshot(L)
	}

	// 13. Load draft files
	// package.path MUST mirror production (pkg/lua.LoadUserModules): the root is the
	// modules/ directory itself, so modules/dashboards/home.lua is require("dashboards/home").
	// Anchoring one level higher would make the sandbox accept require paths that fail in
	// production (and reject ones that work) — a green draft_test would prove nothing.
	//
	// Drafts come first so they shadow production; production is appended so a draft can
	// require() a module that exists only in production and has not been drafted.
	draftModulesDir := se.ws.GetDraftFullPath("modules")
	prodModulesDir := filepath.Join(se.serverDir, "modules")
	draftPackagePath := fmt.Sprintf(
		`package.path = "%s/?.lua;%s/?/init.lua;%s/?.lua;%s/?/init.lua;" .. package.path`,
		draftModulesDir, draftModulesDir, prodModulesDir, prodModulesDir)
	if err := L.DoString(draftPackagePath); err != nil {
		log.Printf("[SANDBOX] failed to set draft package.path: %v", err)
	}
	for _, dp := range run.drafts {
		// as in LuaEngine.LoadUserModules: a module acts as itself
		name := strings.TrimSuffix(filepath.Base(dp), ".lua")
		restore := vclulua.WithStateContext(L, "lua", "automation:module:"+name, "")
		err := L.DoFile(se.ws.GetDraftFullPath(dp))
		restore()
		if err != nil {
			return L, fmt.Errorf("draft %s load failed: %w", dp, err)
		}
	}
	return L, nil
}

// registerBlockedStubs registers __go_* stubs that log blocked calls.
func (se *SandboxEngine) registerBlockedStubs(L *lua.LState, result *SandboxResult, memLimit uint64) {
	blocked := func(callType string) *lua.LFunction {
		return L.NewFunction(func(L *lua.LState) int {
			if len(result.BlockedCalls) < maxBlockedCalls {
				args := make(map[string]any)
				for i := 1; i <= L.GetTop(); i++ {
					key := fmt.Sprintf("arg%d", i)
					args[key] = luaValueToGo(L.Get(i))
				}
				result.BlockedCalls = append(result.BlockedCalls, BlockedCall{
					Type: callType,
					Args: args,
				})
			}
			// Return error to caller
			L.Push(lua.LFalse)
			L.Push(lua.LString("blocked: sandbox mode"))
			return 2
		})
	}

	// MQTT stubs
	L.SetGlobal("__go_mqtt_connect", blocked("mqtt.connect"))
	L.SetGlobal("__go_mqtt_disconnect", blocked("mqtt.disconnect"))
	L.SetGlobal("__go_mqtt_publish", blocked("mqtt.publish"))
	L.SetGlobal("__go_mqtt_subscribe", blocked("mqtt.subscribe"))
	L.SetGlobal("__go_mqtt_unsubscribe", blocked("mqtt.unsubscribe"))
	L.SetGlobal("__go_mqtt_is_connected", L.NewFunction(func(L *lua.LState) int {
		L.Push(lua.LFalse)
		return 1
	}))
	L.SetGlobal("__go_mqtt_poll", L.NewFunction(func(L *lua.LState) int {
		L.Push(lua.LNil)
		return 1
	}))
	L.SetGlobal("__go_mqtt_stats", L.NewFunction(func(L *lua.LState) int {
		L.Push(lua.LString(`{"connected":false}`))
		return 1
	}))

	// GPIO stubs
	L.SetGlobal("__go_gpio_setup", blocked("gpio.setup"))
	L.SetGlobal("__go_gpio_write", blocked("gpio.write"))
	L.SetGlobal("__go_gpio_toggle", blocked("gpio.toggle"))
	L.SetGlobal("__go_gpio_release", blocked("gpio.release"))
	L.SetGlobal("__go_gpio_read", L.NewFunction(func(L *lua.LState) int {
		L.Push(lua.LNumber(0))
		L.Push(lua.LNil)
		return 2
	}))
	// No __go_gpio_poll: without it bootstrap skips the 50 ms input poller,
	// which would only burn timer callbacks on every advance step.
	L.SetGlobal("__go_gpio_stats", L.NewFunction(func(L *lua.LState) int {
		L.Push(lua.LString(`{"inputs":0,"outputs":0}`))
		return 1
	}))

	// HTTP client stubs
	L.SetGlobal("__go_http_request", blocked("http.request"))
	L.SetGlobal("__go_http_async_request", blocked("http.async_request"))
	L.SetGlobal("__go_http_get_async_response", L.NewFunction(func(L *lua.LState) int {
		L.Push(lua.LNil)
		return 1
	}))

	// HTTP server stubs
	L.SetGlobal("__go_http_server_start", blocked("http_server.start"))
	L.SetGlobal("__go_http_server_stop", blocked("http_server.stop"))
	L.SetGlobal("__go_http_server_is_running", L.NewFunction(func(L *lua.LState) int {
		L.Push(lua.LFalse)
		return 1
	}))
	L.SetGlobal("__go_http_server_add_endpoint", blocked("http_server.add_endpoint"))
	L.SetGlobal("__go_http_server_remove_endpoint", L.NewFunction(func(L *lua.LState) int {
		return 0
	}))
	L.SetGlobal("__go_http_server_poll", L.NewFunction(func(L *lua.LState) int {
		L.Push(lua.LNil)
		return 1
	}))
	L.SetGlobal("__go_http_server_respond", L.NewFunction(func(L *lua.LState) int {
		return 0
	}))
	L.SetGlobal("__go_http_server_get_callback", L.NewFunction(func(L *lua.LState) int {
		L.Push(lua.LNil)
		return 1
	}))

	// HomeKit stub
	L.SetGlobal("__go_homekit_notify", blocked("homekit.notify"))

	// CLU commands are recorded, not sent; a reply-expecting call gets nil
	record := func(L *lua.LState) int {
		if len(result.CluCalls) < maxCluCalls {
			result.CluCalls = append(result.CluCalls, CluCall{
				Target: L.CheckString(1),
				Script: L.CheckString(2),
			})
		} else {
			result.CluCallsDropped++
		}
		return 0
	}
	L.SetGlobal("__go_send_command", L.NewFunction(record))
	L.SetGlobal("__go_send_command_noreply", L.NewFunction(record))

	// Client push stub
	L.SetGlobal("__go_client_push_notify", L.NewFunction(func(L *lua.LState) int {
		return 0
	}))

	// One string.rep can allocate gigabytes before the memory guard looks;
	// a quarter of the budget leaves room for the next concatenation
	if strT, ok := L.GetGlobal("string").(*lua.LTable); ok {
		rep := strT.RawGetString("rep")
		strT.RawSetString("rep", L.NewFunction(func(L *lua.LState) int {
			if n := L.OptInt64(2, 0); n > 0 && int64(len(L.CheckString(1)))*n > int64(memLimit/4) {
				L.RaiseError("%v: string.rep of %d bytes", errSandboxMemory, int64(len(L.CheckString(1)))*n)
			}
			L.Insert(rep, 1)
			L.Call(L.GetTop()-1, lua.MultRet)
			return L.GetTop()
		}))
	}

	// Processes and file writes; reading files stays as in production
	if osT, ok := L.GetGlobal("os").(*lua.LTable); ok {
		for _, name := range []string{"execute", "exit", "remove", "rename", "tmpname", "setenv"} {
			osT.RawSetString(name, blocked("os."+name))
		}
	}
	if ioT, ok := L.GetGlobal("io").(*lua.LTable); ok {
		ioT.RawSetString("popen", blocked("io.popen"))
		ioT.RawSetString("tmpfile", blocked("io.tmpfile"))
		// passThrough calls orig with the caller's arguments, unless block says otherwise
		passThrough := func(name string, block func(L *lua.LState) bool) {
			orig, stub := ioT.RawGetString(name), blocked("io."+name)
			ioT.RawSetString(name, L.NewFunction(func(L *lua.LState) int {
				if block(L) {
					return stub.GFunction(L)
				}
				L.Insert(orig, 1)
				L.Call(L.GetTop()-1, lua.MultRet)
				return L.GetTop()
			}))
		}
		passThrough("open", func(L *lua.LState) bool { return strings.ContainsAny(L.OptString(2, "r"), "wa+") })
		passThrough("output", func(L *lua.LState) bool { _, name := L.Get(1).(lua.LString); return name })
	}
}

// takeStateSnapshot captures the current state of all registry objects.
func (se *SandboxEngine) takeStateSnapshot(L *lua.LState) map[string]any {
	snapshot := make(map[string]any)

	// Call _registry:toJSON() to get state
	err := L.DoString(`__sandbox_snapshot = _registry:toJSON()`)
	if err != nil {
		log.Printf("[SANDBOX] failed to take snapshot: %v", err)
		return snapshot
	}

	jsonStr := L.GetGlobal("__sandbox_snapshot")
	L.SetGlobal("__sandbox_snapshot", lua.LNil)

	if jsonStr == lua.LNil {
		return snapshot
	}

	// Parse the JSON array
	var devices []struct {
		Path  string `json:"path"`
		State any    `json:"state"`
	}
	if err := json.Unmarshal([]byte(jsonStr.String()), &devices); err != nil {
		log.Printf("[SANDBOX] failed to parse snapshot: %v", err)
		return snapshot
	}

	for _, d := range devices {
		snapshot[d.Path] = d.State
	}
	return snapshot
}

// diffState compares current state against the snapshot.
func (se *SandboxEngine) diffState(L *lua.LState, snapshot map[string]any) []StateChange {
	var changes []StateChange

	// Get current state
	current := se.takeStateSnapshot(L)

	paths := make([]string, 0, len(current))
	for path := range current {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	for _, path := range paths {
		newVal := current[path]
		oldVal, existed := snapshot[path]
		if !existed || !stateEqual(oldVal, newVal) {
			if len(changes) < maxStateChanges {
				changes = append(changes, StateChange{
					Path:     path,
					OldValue: oldVal,
					NewValue: newVal,
				})
			}
		}
	}

	return changes
}

// stateEqual compares two state values.
func stateEqual(a, b any) bool {
	// Compare as JSON for type-safe comparison
	ja, _ := json.Marshal(a)
	jb, _ := json.Marshal(b)
	return string(ja) == string(jb)
}

// injectEvent emits an event on the sandbox EventBus.
func (se *SandboxEngine) injectEvent(L *lua.LState, evt InjectEvent) error {
	// Convert data to Lua table
	dataJSON, err := json.Marshal(evt.Data)
	if err != nil {
		return fmt.Errorf("failed to marshal event data: %w", err)
	}

	script := fmt.Sprintf(`
		local data = JSON:decode(%q)
		EventBus:getShared():emit(%q, data)
	`, string(dataJSON), evt.Name)

	return L.DoString(script)
}

// luaValueToGo converts a Lua value to a Go value for JSON serialization.
// Cyclic or oversized tables come back as an error string, never recurse forever.
func luaValueToGo(val lua.LValue) any {
	v, err := vclulua.LuaToGo(val)
	if err != nil {
		return "<" + err.Error() + ">"
	}
	return v
}

// ValidateDraft checks Lua syntax and blocklist.
func ValidateDraft(content string) *ValidationReport {
	report := &ValidationReport{
		Valid:    true,
		Errors:   []string{},
		Warnings: []string{},
	}

	// 1. Parse AST
	_, err := luaparse.Parse(strings.NewReader(content), "draft")
	if err != nil {
		report.Valid = false
		report.Errors = append(report.Errors, fmt.Sprintf("syntax error: %v", err))
		return report // no point checking blocklist if syntax is broken
	}

	// 2. Check blocklist (simple string scan — catches both calls and references)
	for _, fn := range blockedFunctions {
		if strings.Contains(content, fn) {
			report.Valid = false
			report.Errors = append(report.Errors, fmt.Sprintf("forbidden function: %s", fn))
		}
	}

	// 3. Check for raw require (warn only — some requires are OK)
	if strings.Contains(content, "require") {
		// Allow require for modules in the project
		lines := strings.Split(content, "\n")
		for i, line := range lines {
			trimmed := strings.TrimSpace(line)
			if strings.Contains(trimmed, "require") && !strings.HasPrefix(trimmed, "--") {
				// Warn about require usage
				report.Warnings = append(report.Warnings, fmt.Sprintf("line %d: require() used — ensure module is in modules/ directory", i+1))
			}
		}
	}

	return report
}
