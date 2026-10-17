package lua

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Domktorymysli/vclu/pkg/camera"
	"github.com/Domktorymysli/vclu/pkg/history"
	"github.com/Domktorymysli/vclu/pkg/metrics"
	"github.com/Domktorymysli/vclu/pkg/plugins"
	lua "github.com/yuin/gopher-lua"
)

// state_changed ids: random per process plus a counter shared by all Lua reloads.
var (
	stateChangeIDPrefix = rand.Text()[:10]
	stateChangeSeq      atomic.Uint64
)

// engineState represents the lifecycle state of the Lua engine.
type engineState int32

const (
	stateInit     engineState = iota // before RunLoop — Set*Manager allowed, executeDirect fallback
	stateRunning                     // RunLoop active — submitToLoop routes through channels
	stateStopping                    // stopChan fired — draining queues
	stateClosed                      // Close() complete
	stateFailed                      // RunLoop panic recovered
)

func (s engineState) String() string {
	switch s {
	case stateInit:
		return "init"
	case stateRunning:
		return "running"
	case stateStopping:
		return "stopping"
	case stateClosed:
		return "closed"
	case stateFailed:
		return "failed"
	default:
		return "unknown"
	}
}

// luaWorkKind classifies work items for queue routing.
type luaWorkKind int

const (
	workFast      luaWorkKind = iota // quick reads: dashboard, GetGlobal, registry
	workSlow                         // arbitrary scripts: Execute, doFile
	workLifecycle                    // Reinitialize, Close
)

func (k luaWorkKind) String() string {
	switch k {
	case workFast:
		return "fast"
	case workSlow:
		return "slow"
	case workLifecycle:
		return "lifecycle"
	default:
		return "unknown"
	}
}

// luaWorkItem represents a unit of work to be executed on the RunLoop goroutine.
type luaWorkItem struct {
	name       string                              // "Execute", "CallLuaFunction:MCP.list_devices", etc.
	kind       luaWorkKind                         // for metrics routing
	fn         func(L *lua.LState) (string, error) // the work
	resultCh   chan<- luaResult                    // response channel (buffered 1)
	submitTime time.Time                           // for latency tracking
}

// luaResult is the response from a work item execution.
type luaResult struct {
	value string
	err   error
}

// CommandSender is a function that sends a command to a remote CLU
type CommandSender func(targetIP string, script string) (string, error)

// CommandNoReplySender sends a command without waiting for a response (fire-and-forget).
// Used for clientRegister (re-registration) and clientDestroy where blocking is unnecessary.
type CommandNoReplySender func(targetIP string, script string) error

// HomeKitStateHandler is a callback for HomeKit state changes
type HomeKitStateHandler func(path string, value float64, objType string)

// ClientPushHandler is a callback to signal immediate client report send
type ClientPushHandler func()

// LuaEngine represents a Lua execution engine
type LuaEngine struct {
	state               *lua.LState
	runtimePath         string
	cluDataPath         string
	timerManager        *TimerManager
	httpManager         *HTTPManager
	httpServerManager   *HTTPServerManager
	mqttManager         *MQTTManager
	modbusManager       *ModbusManager
	kvManager           *plugins.KVStoreManager
	gpioManager         *GPIOManager
	satelManager        *SatelManager
	metricsCollector    *metrics.Collector
	historyStore        *history.Store
	cameraManager       *camera.Manager
	commandSender       CommandSender
	commandNoReply      CommandNoReplySender
	homeKitStateHandler HomeKitStateHandler
	clientPushHandler   ClientPushHandler
	customGlobals       map[string]interface{}    // Globals to restore after Reinitialize
	scriptCache         map[string]*lua.LFunction // compiled script cache (cleared on Reinitialize)
	mu                  sync.RWMutex              // protects state in stateInit; unused in stateRunning

	// Outcome of the last LoadUserModules call, so reload/promote can report
	// modules that failed to load instead of reporting a blanket success.
	lastModuleLoad []ModuleLoadResult
	moduleLoadMu   sync.RWMutex

	// Work queue channels — used in stateRunning for serialized LState access
	fastQueue   chan luaWorkItem // buffered 64: workFast + workLifecycle
	slowQueue   chan luaWorkItem // buffered 256: workSlow
	engineState atomic.Int32     // engineState enum
	drainDone   chan struct{}    // closed on shutdown — unblocks waiting submitters
	drainOnce   sync.Once        // drainDone is closed from several paths
	loopDone    chan struct{}    // closed when RunLoop has returned

	// Runtime busy tracking for runtime:busy_percent metric
	busyTimeNs   atomic.Int64 // nanoseconds spent in callbacks since start
	lastBusySnap int64        // busyTimeNs snapshot at last metric collection
	lastSnapTime time.Time    // wall time at last metric collection

	// Event loop lag tracking
	maxLagNs atomic.Int64 // max lag observed since last collection
}

// NewLuaEngine creates a new Lua engine with CLU name
func NewLuaEngine() *LuaEngine {
	return NewLuaEngineWithCLU("VCLU")
}

// NewLuaEngineWithRuntimePath creates a new Lua engine with specific runtime path
func NewLuaEngineWithRuntimePath(cluName string, runtimePath string) *LuaEngine {
	le := &LuaEngine{
		runtimePath: runtimePath,
		fastQueue:   make(chan luaWorkItem, 64),
		slowQueue:   make(chan luaWorkItem, 256),
		drainDone:   make(chan struct{}),
		loopDone:    make(chan struct{}),
	}
	// engineState defaults to stateInit (zero value)

	if err := le.initializeLuaState(cluName); err != nil {
		log.Printf("WARNING: Failed to initialize Lua state: %v", err)
	}

	return le
}

// initializeLuaState creates and configures a fresh Lua state
// This is the common initialization used by both NewLuaEngineWithRuntimePath and Reinitialize
func (le *LuaEngine) initializeLuaState(cluName string) error {
	L := lua.NewState()

	// Keep os and package libraries for CLU runtime
	// os.time, os.date needed by CLU modules
	// package.path, require() needed for module loading

	// Disable only truly dangerous file I/O
	L.SetGlobal("dofile", lua.LNil)   // Disable file execution
	L.SetGlobal("loadfile", lua.LNil) // Disable file loading

	L.SetGlobal("_VCLU_RUNTIME_PATH", lua.LString(le.runtimePath))
	L.SetGlobal("_CLU", lua.LString(cluName))

	// Create managers (or reset existing ones)
	if le.timerManager != nil {
		le.timerManager.Stop()
		le.timerManager.ClearAll()
	}
	le.timerManager = NewTimerManager()

	le.httpManager = NewHTTPManager()

	// Create HTTP server manager (but don't stop existing - preserve across reinit)
	if le.httpServerManager == nil {
		le.httpServerManager = NewHTTPServerManager()
	}

	// Preserve MQTT manager across reinit (connections survive reload)
	if le.mqttManager == nil {
		le.mqttManager = NewMQTTManager()
	}

	// Preserve Modbus manager across reinit (pooled sockets survive reload)
	if le.modbusManager == nil {
		le.modbusManager = NewModbusManager()
	}

	// Register safe functions (including timer functions)
	registerSafeFunctions(L)
	registerTimerFunctions(L, le.timerManager)
	registerHTTPFunctions(L, le.httpManager)
	registerHTTPServerFunctions(L, le.httpServerManager)
	registerMQTTFunctions(L, le.mqttManager)
	registerModbusFunctions(L, le.modbusManager)
	registerParserFunctions(L)
	registerKVFunctions(L, le.kvManager)
	registerGPIOFunctions(L, le.gpioManager)
	registerSatelFunctions(L, le.satelManager)
	registerMetricsFunctions(L, le.metricsCollector)
	registerHistoryFunctions(L, le.historyStore)
	registerCameraFunctions(L, le.cameraManager)

	// Load the Go-compatible bootstrap (this loads SYSTEM, GATE, OBJECT, CLU, etc.)
	bootstrapPath := filepath.Join(le.runtimePath, "bootstrap_go.lua")
	log.Printf("[LUA] loading runtime from: %s", bootstrapPath)

	if err := L.DoFile(bootstrapPath); err != nil {
		log.Printf("[LUA] failed to load bootstrap: %v", err)

		// Fallback to basic VirtualCLU if bootstrap fails
		vclu := CreateVirtualCLU(L, cluName)
		L.SetGlobal(cluName, vclu)
	} else {
		log.Printf("[LUA] runtime loaded")
	}

	le.state = L
	return nil
}

// NewLuaEngineWithCLU creates a new Lua engine with specific CLU name
func NewLuaEngineWithCLU(cluName string) *LuaEngine {
	runtimePath := findRuntimePath()
	return NewLuaEngineWithRuntimePath(cluName, runtimePath)
}

// findRuntimePath tries to find the Lua runtime directory
func findRuntimePath() string {
	// 1. Check environment variable
	if envPath := os.Getenv("VCLU_RUNTIME_PATH"); envPath != "" {
		return envPath
	}

	// 2. Try relative to executable
	if execPath, err := os.Executable(); err == nil {
		execDir := filepath.Dir(execPath)
		candidates := []string{
			filepath.Join(execDir, "runtime"), // Deployed location: /opt/vclu/runtime
			filepath.Join(execDir, "pkg", "lua", "runtime"),
			filepath.Join(execDir, "..", "pkg", "lua", "runtime"),
		}
		for _, path := range candidates {
			if _, err := os.Stat(filepath.Join(path, "bootstrap_go.lua")); err == nil {
				log.Printf("[LUA] found runtime at: %s", path)
				return path
			}
		}
	}

	// 3. Try relative to source file (for development)
	if _, filename, _, ok := runtime.Caller(0); ok {
		srcDir := filepath.Dir(filename)
		path := filepath.Join(srcDir, "runtime")
		if _, err := os.Stat(filepath.Join(path, "bootstrap_go.lua")); err == nil {
			return path
		}
	}

	// 4. Try common development paths
	candidates := []string{
		"./pkg/lua/runtime",
		"../pkg/lua/runtime",
		"../../pkg/lua/runtime",
	}
	for _, path := range candidates {
		if _, err := os.Stat(filepath.Join(path, "bootstrap_go.lua")); err == nil {
			absPath, _ := filepath.Abs(path)
			return absPath
		}
	}

	// Fallback to default
	return "./pkg/lua/runtime"
}

// registerTimerFunctions registers Go timer functions in Lua
func registerTimerFunctions(L *lua.LState, tm *TimerManager) {
	// __go_setTimeout(callback, periodMs) -> timerId
	L.SetGlobal("__go_setTimeout", L.NewFunction(func(L *lua.LState) int {
		callback := L.CheckFunction(1)
		periodMs := L.CheckInt(2)
		id := tm.SetTimeout(callback, periodMs)
		L.Push(lua.LNumber(id))
		return 1
	}))

	// __go_setInterval(callback, periodMs) -> timerId
	L.SetGlobal("__go_setInterval", L.NewFunction(func(L *lua.LState) int {
		callback := L.CheckFunction(1)
		periodMs := L.CheckInt(2)
		id := tm.SetInterval(callback, periodMs)
		L.Push(lua.LNumber(id))
		return 1
	}))

	// __go_clearTimer(timerId)
	L.SetGlobal("__go_clearTimer", L.NewFunction(func(L *lua.LState) int {
		id := L.CheckInt(1)
		tm.ClearTimer(id)
		return 0
	}))

	// __go_state_change_id() -> process-unique id, survives Lua reloads
	L.SetGlobal("__go_state_change_id", L.NewFunction(func(L *lua.LState) int {
		L.Push(lua.LString(fmt.Sprintf("%s-%d", stateChangeIDPrefix, stateChangeSeq.Add(1))))
		return 1
	}))

	// __go_time_ms() -> current wall clock time in milliseconds
	L.SetGlobal("__go_time_ms", L.NewFunction(func(L *lua.LState) int {
		ms := clockNow(L).UnixMilli()
		L.Push(lua.LNumber(ms))
		return 1
	}))
}

// doFile executes a Lua file via the work queue (or mutex fallback in stateInit).
// gopher-lua is NOT thread-safe — all LState access must be serialized.
func (le *LuaEngine) doFile(path string) error {
	_, err := le.executeSlow("doFile:"+filepath.Base(path), func(L *lua.LState) (string, error) {
		return "", L.DoFile(path)
	})
	return err
}

// doFileAs loads a file with a context: loading never runs as the request
// that triggered a reload, and timers the file creates keep this context.
// The context lives only inside this work item.
func (le *LuaEngine) doFileAs(origin, actor, path string) error {
	_, err := le.executeSlow("doFile:"+filepath.Base(path), func(L *lua.LState) (string, error) {
		defer withStateContext(L, origin, actor, "")()
		return "", L.DoFile(path)
	})
	return err
}

// loadModuleAs runs a top-level module with RunModule, acting as actor.
func (le *LuaEngine) loadModuleAs(origin, actor, path, name string, before map[string]bool) (skipped bool, err error) {
	_, err = le.executeSlow("doFile:"+filepath.Base(path), func(L *lua.LState) (string, error) {
		defer withStateContext(L, origin, actor, "")()
		skipped, err = RunModule(L, path, name, before)
		return "", err
	})
	return skipped, err
}

// LoadedModules returns the names in package.loaded.
func LoadedModules(L *lua.LState) map[string]bool {
	names := map[string]bool{}
	if loaded, ok := L.GetField(L.GetGlobal("package"), "loaded").(*lua.LTable); ok {
		loaded.ForEach(func(k, _ lua.LValue) {
			if s, ok := k.(lua.LString); ok {
				names[string(s)] = true
			}
		})
	}
	return names
}

// RunModule runs the file at path like require(name): not again when a
// require since the `before` snapshot loaded it, and a later require gets
// its result. A name known before (a runtime module) stays as it was: the
// file runs, but does not replace the module.
func RunModule(L *lua.LState, path, name string, before map[string]bool) (skipped bool, err error) {
	loaded, _ := L.GetField(L.GetGlobal("package"), "loaded").(*lua.LTable)
	// require turns dots into slashes: a file with a dot in its name is never
	// what require(name) loaded
	requirable := loaded != nil && !before[name] && !strings.Contains(name[strings.LastIndex(name, "/")+1:], ".")
	if requirable {
		v := loaded.RawGetString(name)
		if v == lua.LNil {
			v = loaded.RawGetString(strings.ReplaceAll(name, "/", "."))
		}
		// a require that failed leaves gopher-lua's sentinel (userdata): the
		// file has not run, it gets its turn and reports its own error
		if _, failed := v.(*lua.LUserData); v != lua.LNil && !failed {
			return true, nil
		}
	}
	fn, err := L.LoadFile(path)
	if err != nil {
		return false, err
	}
	L.Push(fn)
	if err := L.PCall(0, 1, nil); err != nil {
		return false, err
	}
	ret := L.Get(-1)
	L.Pop(1)
	if ret == lua.LNil || ret == lua.LFalse {
		ret = lua.LTrue
	}
	if requirable {
		if v := loaded.RawGetString(name); v == lua.LNil || v.Type() == lua.LTUserData {
			loaded.RawSetString(name, ret)
		}
	}
	return false, nil
}

// LoadCLUData loads CLU data files from the specified directory
// The directory should contain: om.lua, user.lua (and main.lua for checkAlive serial)
//
// We do NOT execute main.lua directly because it contains a hardware CLU main loop
// (repeat SYSTEM.Loop() until 1==2) that would block forever in vCLU.
// Instead we load om.lua and user.lua ourselves, then call SYSTEM.Init().
func (le *LuaEngine) LoadCLUData(dataPath string) error {
	le.cluDataPath = dataPath

	// Check if Lua state is initialized
	if le.state == nil {
		return fmt.Errorf("Lua engine not initialized - call Reinitialize() first")
	}

	// Add CLU data path to package.path so require() works
	absPath, err := filepath.Abs(dataPath)
	if err != nil {
		return fmt.Errorf("failed to get absolute path: %w", err)
	}

	// Update Lua package.path
	packagePath := fmt.Sprintf("%s/?.lua;", absPath)
	_, err = le.Execute(fmt.Sprintf(`package.path = "%s" .. package.path`, packagePath))
	if err != nil {
		return fmt.Errorf("failed to load lua package: %w", err)
	}

	log.Printf("[LUA] loading CLU data from: %s", absPath)

	// 1. Load user.lua (defines user functions/variables referenced by om.lua events)
	_, _ = le.ExecuteAs("system", "system:vclu", "", `
		local ok, err = pcall(function() require "user" end)
		if not ok then
			Logger:getShared():warn("[LUA] user.lua skipped: " .. tostring(err))
		end
	`)

	// 2. Load om.lua (creates CLU objects, modules, IO, event bindings) - optional
	// Must use doFile() — holds mutex to prevent race with ProcessTimers/RunLoop
	omPath := filepath.Join(absPath, "om.lua")
	if _, statErr := os.Stat(omPath); statErr == nil {
		if err := le.doFileAs("system", "system:vclu", omPath); err != nil {
			return fmt.Errorf("om.lua failed: %w", err)
		}
	} else {
		log.Printf("[LUA] om.lua not found — running without Grenton objects")
	}

	// 3. Update checkAlive with serial from main.lua (if present)
	le.loadCheckAliveFromMainLua(absPath)

	// 4. Initialize SYSTEM (fires EVENT_ON_INIT)
	if _, err := le.ExecuteAs("system", "system:vclu", "", "SYSTEM.Init()"); err != nil {
		return fmt.Errorf("SYSTEM.Init failed: %w", err)
	}

	log.Printf("[LUA] CLU data loaded")
	return nil
}

// loadCheckAliveFromMainLua reads main.lua to extract the checkAlive serial hex
// and redefines the global checkAlive() function with it.
// main.lua is NOT executed — only parsed for the return value.
func (le *LuaEngine) loadCheckAliveFromMainLua(absPath string) {
	mainPath := filepath.Join(absPath, "main.lua")
	content, err := os.ReadFile(mainPath)
	if err != nil {
		return
	}

	// Extract serial from: return "0d2c9586" or return "true"
	for _, line := range strings.Split(string(content), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "return \"") && strings.HasSuffix(line, "\"") {
			serial := strings.TrimPrefix(line, "return \"")
			serial = strings.TrimSuffix(serial, "\"")
			if serial != "" {
				le.Execute(fmt.Sprintf(`function checkAlive() return %q end`, serial))
				return
			}
		}
	}
}

// ModuleLoadResult reports the outcome of loading a single user module file.
// A failed module is not fatal — the rest still load — but callers (reload,
// draft promote) must be able to see the failure instead of assuming success.
type ModuleLoadResult struct {
	Name  string `json:"name"`
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
}

// LastModuleLoad returns the result of the most recent LoadUserModules call.
// Used by reload/promote to report modules that failed to load.
func (le *LuaEngine) LastModuleLoad() []ModuleLoadResult {
	le.moduleLoadMu.RLock()
	defer le.moduleLoadMu.RUnlock()
	out := make([]ModuleLoadResult, len(le.lastModuleLoad))
	copy(out, le.lastModuleLoad)
	return out
}

// setLastModuleLoad stores the report and returns an independent copy, so a
// caller mutating the returned slice cannot write into the retained one.
func (le *LuaEngine) setLastModuleLoad(results []ModuleLoadResult) []ModuleLoadResult {
	le.moduleLoadMu.Lock()
	le.lastModuleLoad = results
	le.moduleLoadMu.Unlock()

	out := make([]ModuleLoadResult, len(results))
	copy(out, results)
	return out
}

// LoadUserModules auto-loads .lua files from the modules/ directory (flat, not recursive).
// init.lua is loaded first (if present), then remaining .lua files sorted alphabetically.
// Subdirectories are NOT auto-loaded — users must require() them manually from their files.
// A file that init.lua already required is not run again.
// Should be called AFTER LoadProxyFiles so remote objects are available.
//
// Returns one result per attempted file. The result is also retained and readable
// via LastModuleLoad() so a reload can report failures to its caller.
func (le *LuaEngine) LoadUserModules(serverDir string) []ModuleLoadResult {
	modulesDir := filepath.Join(serverDir, "modules")

	// Check if modules directory exists
	if _, err := os.Stat(modulesDir); os.IsNotExist(err) {
		return le.setLastModuleLoad(nil)
	}

	// Add modules directory to package.path for require()
	absModulesPath, err := filepath.Abs(modulesDir)
	if err != nil {
		log.Printf("[LUA] failed to get modules path: %v", err)
		return le.setLastModuleLoad([]ModuleLoadResult{
			{Name: "modules/", OK: false, Error: fmt.Sprintf("failed to resolve modules path: %v", err)},
		})
	}

	// package.path is anchored ON modules/, so require() paths are relative to it:
	// modules/dashboards/home.lua is require("dashboards/home") or require("dashboards.home").
	modulesPackagePath := fmt.Sprintf("%s/?.lua;%s/?/init.lua;", absModulesPath, absModulesPath)
	_, err = le.Execute(fmt.Sprintf(`package.path = "%s" .. package.path`, modulesPackagePath))
	if err != nil {
		log.Printf("[LUA] failed to update package.path: %v", err)
		return le.setLastModuleLoad([]ModuleLoadResult{
			{Name: "modules/", OK: false, Error: fmt.Sprintf("failed to set package.path: %v", err)},
		})
	}

	// Read flat directory entries (no recursion into subdirectories)
	entries, err := os.ReadDir(modulesDir)
	if err != nil {
		log.Printf("[LUA] failed to read modules dir: %v", err)
		return le.setLastModuleLoad([]ModuleLoadResult{
			{Name: "modules/", OK: false, Error: fmt.Sprintf("failed to read modules dir: %v", err)},
		})
	}

	var initFile string
	var otherFiles []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasSuffix(strings.ToLower(name), ".lua") {
			continue
		}
		if name == "init.lua" {
			initFile = name
		} else {
			otherFiles = append(otherFiles, name)
		}
	}

	// Build ordered list: init.lua first, then alphabetically
	var files []string
	if initFile != "" {
		files = append(files, initFile)
	}
	files = append(files, otherFiles...)

	if len(files) == 0 {
		return le.setLastModuleLoad(nil)
	}

	log.Printf("[LUA] loading user modules: %s", strings.Join(files, ", "))

	// what require() knew before any user file ran: the runtime's modules
	var before map[string]bool
	_, _ = le.executeSlow("doFile:snapshot", func(L *lua.LState) (string, error) {
		before = LoadedModules(L)
		return "", nil
	})

	results := make([]ModuleLoadResult, 0, len(files))
	loaded := 0
	for _, name := range files {
		fullPath := filepath.Join(modulesDir, name)
		module := strings.TrimSuffix(name, ".lua")
		// a module's own timers act as that module from now on
		var err error
		if name == initFile {
			err = le.doFileAs("lua", "automation:module:"+module, fullPath)
		} else {
			if before[module] {
				log.Printf("[LUA] modules/%s has a runtime module's name: require(%q) keeps the runtime one", name, module)
			}
			var skipped bool
			if skipped, err = le.loadModuleAs("lua", "automation:module:"+module, fullPath, module, before); skipped && err == nil {
				log.Printf("[LUA] modules/%s already loaded by require(%q)", name, module)
			}
		}
		if err != nil {
			log.Printf("[LUA] failed to load modules/%s: %v", name, err)
			results = append(results, ModuleLoadResult{Name: name, OK: false, Error: err.Error()})
		} else {
			loaded++
			results = append(results, ModuleLoadResult{Name: name, OK: true})
		}
	}

	if loaded < len(files) {
		log.Printf("[LUA] ERROR: loaded %d/%d user module(s) — %d FAILED", loaded, len(files), len(files)-loaded)
	} else {
		log.Printf("[LUA] loaded %d/%d user module(s)", loaded, len(files))
	}

	return le.setLastModuleLoad(results)
}

// LoadProxyFiles loads all proxy_*.lua files from the imports directory
// These are generated from uploaded OM files and register remote devices
func (le *LuaEngine) LoadProxyFiles(importsDir string) error {
	if le.state == nil {
		return fmt.Errorf("Lua engine not initialized")
	}
	// Check if imports directory exists
	if _, err := os.Stat(importsDir); os.IsNotExist(err) {
		return nil
	}

	// Find all proxy_*.lua files
	pattern := filepath.Join(importsDir, "proxy_*.lua")
	files, err := filepath.Glob(pattern)
	if err != nil {
		return fmt.Errorf("failed to list proxy files: %w", err)
	}

	if len(files) == 0 {
		return nil
	}

	for _, file := range files {
		if err := le.doFileAs("system", "system:vclu", file); err != nil {
			log.Printf("[LUA] failed to load %s: %v", filepath.Base(file), err)
			// Continue loading other files
		}
	}
	return nil
}

// formatLuaError formats a Lua error with better readability
// gopher-lua errors typically include: <filename>:<line>: <message>
func formatLuaError(context string, err error) error {
	errStr := err.Error()
	log.Printf("[LUA] error loading %s: %s", context, errStr)
	return fmt.Errorf("Lua error in %s: %s", context, errStr)
}

// Init initializes the CLU by calling SYSTEM.Init()
// This fires the OnInit event which runs the initialization code from om.lua
func (le *LuaEngine) Init() error {
	_, err := le.Execute("SYSTEM.Init()")
	if err != nil {
		return fmt.Errorf("failed to initialize CLU: %w", err)
	}
	return nil
}

// registerSafeFunctions registers safe Lua functions
func registerSafeFunctions(L *lua.LState) {
	// Register print function (logs to console)
	// Also stores output in __vclu_print_output__ for checkAlive() compatibility
	// (OM changed checkAlive from "return X" to "print(X)")
	L.SetGlobal("print", L.NewFunction(func(L *lua.LState) int {
		args := make([]interface{}, L.GetTop())
		for i := 1; i <= L.GetTop(); i++ {
			args[i-1] = L.Get(i).String()
		}
		log.Println(args...)

		// Store first argument as print output (for checkAlive compatibility)
		if L.GetTop() > 0 {
			L.SetGlobal("__vclu_print_output__", L.Get(1))
		}
		return 0
	}))

	// Note: checkAlive() and SYSTEM are now loaded from bootstrap.lua
	// We don't override them here anymore
}

// Execute executes a Lua script and returns the result as a string.
// Supports both "return X" and "print(X)" style functions (for checkAlive compatibility).
// Thread-safe: routed through workQueue in stateRunning, mutex in stateInit.
func (le *LuaEngine) Execute(script string) (string, error) {
	return le.executeSlow("Execute", func(L *lua.LState) (string, error) {
		return executeOnState(L, script)
	})
}

// executeOnState runs a Lua script directly on the given LState.
// This is the core logic extracted from Execute — callable from work items
// and from OnLoop methods without going through the queue.
func executeOnState(L *lua.LState, script string) (string, error) {
	// Clear any previous print output
	L.SetGlobal("__vclu_print_output__", lua.LNil)

	// Strip "return " prefix if present - wrapper already adds return
	scriptExpr := strings.TrimSpace(script)
	if strings.HasPrefix(scriptExpr, "return ") {
		scriptExpr = strings.TrimPrefix(scriptExpr, "return ")
	}

	// Three forms, tried in order: an expression whose value is returned, a
	// chunk with its own return, and the raw script. Only compilation picks the
	// form; the chosen one runs exactly once, so a runtime error is never
	// retried (a retry could repeat a command already sent to a CLU).
	forms := []string{
		fmt.Sprintf("__vclu_result__ = (function() return %s\nend)()", scriptExpr),
		fmt.Sprintf("__vclu_result__ = (function() %s\nend)()", script),
		script,
	}
	var fn *lua.LFunction
	var compileErr error
	for _, form := range forms {
		if fn, compileErr = L.LoadString(form); compileErr == nil {
			break
		}
	}
	if compileErr != nil {
		return "", fmt.Errorf("failed to execute Lua script: %w", compileErr)
	}
	L.SetGlobal("__vclu_result__", lua.LNil)
	L.Push(fn)
	if err := L.PCall(0, 0, nil); err != nil {
		return "", fmt.Errorf("failed to execute Lua script: %w", err)
	}

	// Get the result from the global variable
	result := L.GetGlobal("__vclu_result__")
	L.SetGlobal("__vclu_result__", lua.LNil) // Clean up

	// If result is nil, check if there was print output (for checkAlive compatibility)
	// OM changed checkAlive() from "return X" to "print(X)"
	resultStr := luaValueToString(result)
	if resultStr == "nil" {
		printOutput := L.GetGlobal("__vclu_print_output__")
		L.SetGlobal("__vclu_print_output__", lua.LNil)
		if printOutput != lua.LNil {
			return luaValueToString(printOutput), nil
		}
	}

	return resultStr, nil
}

// ExecuteCached compiles a Lua script once and caches the resulting function.
// Subsequent calls with the same script skip parsing and compilation entirely.
// Thread-safe: routed through fast workQueue in stateRunning.
// Cache is cleared on Reinitialize().
func (le *LuaEngine) ExecuteCached(script string) (string, error) {
	return le.executeFast("ExecuteCached", func(L *lua.LState) (string, error) {
		// Lookup or compile
		fn, ok := le.scriptCache[script]
		if !ok {
			// Compile once: wrap as function returning expression
			chunk := fmt.Sprintf("return %s", strings.TrimPrefix(strings.TrimSpace(script), "return "))
			var err error
			fn, err = L.LoadString(chunk)
			if err != nil {
				// Retry as statement block
				fn, err = L.LoadString(script)
				if err != nil {
					return "", fmt.Errorf("compile failed: %w", err)
				}
			}
			if le.scriptCache == nil {
				le.scriptCache = make(map[string]*lua.LFunction)
			}
			le.scriptCache[script] = fn
		}

		if err := L.CallByParam(lua.P{
			Fn:      fn,
			NRet:    1,
			Protect: true,
		}); err != nil {
			return "", fmt.Errorf("ExecuteCached: %w", err)
		}

		ret := L.Get(-1)
		L.Pop(1)
		return luaValueToString(ret), nil
	})
}

// CallLuaFunction calls a pre-compiled Lua function by name with string arguments.
// Supports dotted paths like "Foo.bar". Returns ("", nil) if root table doesn't exist.
// Thread-safe: routed through fast workQueue in stateRunning.
func (le *LuaEngine) CallLuaFunction(funcPath string, args ...string) (string, error) {
	return le.executeFast("CallLuaFunction:"+funcPath, func(L *lua.LState) (string, error) {
		return callLuaFunctionOnState(L, funcPath, args...)
	})
}

// callLuaFunctionOnState resolves and calls a Lua function by dotted path with string args.
// Callable directly from OnLoop methods without going through the queue.
func callLuaFunctionOnState(L *lua.LState, funcPath string, args ...string) (string, error) {
	parts := strings.Split(funcPath, ".")
	fn := L.GetGlobal(parts[0])

	// If root global is nil, function not loaded yet (early startup) — return silently
	if fn == lua.LNil {
		return "", nil
	}

	for i := 1; i < len(parts); i++ {
		tbl, ok := fn.(*lua.LTable)
		if !ok {
			return "", fmt.Errorf("%s is not a table", strings.Join(parts[:i], "."))
		}
		fn = tbl.RawGetString(parts[i])
	}

	lfn, ok := fn.(*lua.LFunction)
	if !ok {
		return "", fmt.Errorf("%s is not a function", funcPath)
	}

	luaArgs := make([]lua.LValue, len(args))
	for i, a := range args {
		luaArgs[i] = lua.LString(a)
	}

	if err := L.CallByParam(lua.P{
		Fn:      lfn,
		NRet:    1,
		Protect: true,
	}, luaArgs...); err != nil {
		return "", fmt.Errorf("CallLuaFunction %s: %w", funcPath, err)
	}

	ret := L.Get(-1)
	L.Pop(1)
	return luaValueToString(ret), nil
}

// CallLuaFunctionLValues calls a Lua function with pre-built LValue arguments.
// Lowest-level call — avoids string-to-LValue conversion.
// Thread-safe: routed through fast workQueue in stateRunning.
func (le *LuaEngine) CallLuaFunctionLValues(funcPath string, args ...lua.LValue) (string, error) {
	return le.executeFast("CallLuaFunctionLV:"+funcPath, func(L *lua.LState) (string, error) {
		parts := strings.Split(funcPath, ".")
		fn := L.GetGlobal(parts[0])
		if fn == lua.LNil {
			return "", nil
		}
		for i := 1; i < len(parts); i++ {
			tbl, ok := fn.(*lua.LTable)
			if !ok {
				return "", fmt.Errorf("%s is not a table", strings.Join(parts[:i], "."))
			}
			fn = tbl.RawGetString(parts[i])
		}

		lfn, ok := fn.(*lua.LFunction)
		if !ok {
			return "", fmt.Errorf("%s is not a function", funcPath)
		}

		if err := L.CallByParam(lua.P{
			Fn:      lfn,
			NRet:    1,
			Protect: true,
		}, args...); err != nil {
			return "", fmt.Errorf("CallLuaFunctionLValues %s: %w", funcPath, err)
		}

		ret := L.Get(-1)
		L.Pop(1)
		return luaValueToString(ret), nil
	})
}

// NewLuaTable creates a new LTable and returns it. Used by callers that need
// to build Lua tables in Go and pass them via CallLuaFunctionLValues.
func (le *LuaEngine) NewLuaTable() *lua.LTable {
	return new(lua.LTable)
}

// ExecuteWithTimeout runs a Lua script with a timeout using context cancellation.
// gopher-lua checks context at VM instruction boundaries, so infinite loops are caught.
// Thread-safe: routed through slow workQueue in stateRunning.
func (le *LuaEngine) ExecuteWithTimeout(script string, timeout time.Duration) (string, error) {
	return le.executeSlow("ExecuteTimeout", func(L *lua.LState) (string, error) {
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()

		L.SetContext(ctx)
		defer L.RemoveContext()

		out, err := executeOnState(L, script)
		if err != nil && ctx.Err() != nil {
			return "", fmt.Errorf("execution timed out after %v", timeout)
		}
		return out, err
	})
}

// Call invokes a named Lua function with Go arguments, safely converting types.
// Supports dotted names like "MCP.list_devices".
// Thread-safe: routed through fast workQueue in stateRunning.
func (le *LuaEngine) Call(funcName string, args ...any) (string, error) {
	return le.executeFast("Call:"+funcName, func(L *lua.LState) (string, error) {
		return callOnState(L, funcName, args)
	})
}

// CallAs is Call with a StateContext {origin, actor, correlationId} for the
// duration of the call (see ExecuteAs).
func (le *LuaEngine) CallAs(origin, actor, correlationID, funcName string, args ...any) (string, error) {
	return le.executeFast("CallAs:"+funcName, func(L *lua.LState) (string, error) {
		defer withStateContext(L, origin, actor, correlationID)()
		return callOnState(L, funcName, args)
	})
}

func callOnState(L *lua.LState, funcName string, args []any) (string, error) {
	{
		var fn lua.LValue
		parts := strings.Split(funcName, ".")
		fn = L.GetGlobal(parts[0])
		for _, part := range parts[1:] {
			if tbl, ok := fn.(*lua.LTable); ok {
				fn = tbl.RawGetString(part)
			} else {
				return "", fmt.Errorf("cannot resolve function: %s", funcName)
			}
		}

		lfn, ok := fn.(*lua.LFunction)
		if !ok {
			return "", fmt.Errorf("'%s' is not a function", funcName)
		}

		largs := make([]lua.LValue, len(args))
		for i, arg := range args {
			largs[i] = goToLuaValue(L, arg)
		}

		if err := L.CallByParam(lua.P{
			Fn:      lfn,
			NRet:    1,
			Protect: true,
		}, largs...); err != nil {
			return "", fmt.Errorf("Lua call %s failed: %w", funcName, err)
		}

		ret := L.Get(-1)
		L.Pop(1)
		return luaValueToString(ret), nil
	}
}

// SetGlobal sets a global variable in the Lua state.
// Also stores the value to restore after Reinitialize.
// Thread-safe: routed through fast workQueue in stateRunning.
func (le *LuaEngine) SetGlobal(name string, value interface{}) {
	le.executeFast("SetGlobal:"+name, func(L *lua.LState) (string, error) {
		// Store for restoration after Reinitialize (accessed only from loop goroutine)
		if le.customGlobals == nil {
			le.customGlobals = make(map[string]interface{})
		}
		le.customGlobals[name] = value
		L.SetGlobal(name, goToLuaValue(L, value))
		return "", nil
	})
}

// GetGlobal gets a global variable from the Lua state.
// Thread-safe: routed through fast workQueue in stateRunning.
func (le *LuaEngine) GetGlobal(name string) string {
	result, _ := le.executeFast("GetGlobal:"+name, func(L *lua.LState) (string, error) {
		val := L.GetGlobal(name)
		return luaValueToString(val), nil
	})
	return result
}

// StartTimers starts the timer manager goroutine
func (le *LuaEngine) StartTimers() {
	le.timerManager.Start()
}

// StopTimers stops the timer manager goroutine
func (le *LuaEngine) StopTimers() {
	le.timerManager.Stop()
}

// addBusyTime atomically adds elapsed callback time for runtime_busy_percent metric.
func (le *LuaEngine) addBusyTime(d time.Duration) {
	le.busyTimeNs.Add(d.Nanoseconds())
}

// processTimersOnLoop processes pending timer callbacks directly on the RunLoop goroutine.
// No mutex needed — called only from RunLoop which owns the LState.
func (le *LuaEngine) processTimersOnLoop() int {
	if le.state == nil || le.timerManager == nil {
		return 0
	}

	count := 0
	for {
		timerID := le.timerManager.GetPendingTimerID()
		if timerID == 0 {
			break
		}

		callback := le.timerManager.GetCallback(timerID)
		if callback == nil || callback == lua.LNil {
			le.timerManager.CleanupTimer(timerID)
			continue
		}

		if fn, ok := callback.(*lua.LFunction); ok {
			resetStateContext(le.state)
			start := time.Now()
			err := le.state.CallByParam(lua.P{
				Fn:      fn,
				NRet:    0,
				Protect: true,
			})
			elapsed := time.Since(start)
			le.addBusyTime(elapsed)

			if err != nil {
				log.Printf("[TIMER] Error executing timer %d callback: %v", timerID, err)
			}

			if elapsed > 10*time.Second {
				log.Printf("[TIMER] KILLED callback (timer %d): took %v — consider debugging", timerID, elapsed)
			} else if elapsed > 2*time.Second {
				log.Printf("[TIMER] Slow callback (timer %d): took %v", timerID, elapsed)
			}

			le.timerManager.MarkFired(timerID)
		}

		le.timerManager.CleanupTimer(timerID)
		count++
	}
	return count
}

// ProcessTimers is the public wrapper for tests (stateInit with mutex fallback).
// In stateRunning, RunLoop calls processTimersOnLoop directly.
func (le *LuaEngine) ProcessTimers() int {
	le.mu.Lock()
	defer le.mu.Unlock()
	return le.processTimersOnLoop()
}

// GetTimerStats returns current timer statistics
func (le *LuaEngine) GetTimerStats() TimerStats {
	return le.timerManager.Stats()
}

// PluginStats represents statistics for a single plugin
type PluginStats struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	Version       string `json:"version"`
	Timers        int    `json:"timers"`
	Sensors       int    `json:"sensors"`
	Controls      int    `json:"controls"`
	Subscriptions int    `json:"subscriptions"`
	Exposed       int    `json:"exposed"`
}

// AllPluginStats represents aggregated stats from all plugins
type AllPluginStats struct {
	Plugins     map[string]PluginStats `json:"plugins"`
	Totals      PluginTotals           `json:"totals"`
	LuaMemoryKB float64                `json:"lua_memory_kb"`
}

// PluginTotals represents totals across all plugins
type PluginTotals struct {
	Count         int `json:"count"`
	Timers        int `json:"timers"`
	Sensors       int `json:"sensors"`
	Controls      int `json:"controls"`
	Subscriptions int `json:"subscriptions"`
	Exposed       int `json:"exposed"`
}

// GetPluginStats returns statistics from all loaded plugins
func (le *LuaEngine) GetPluginStats() (*AllPluginStats, error) {
	// Execute Plugin.allStats() and get JSON result
	result, err := le.Execute(`JSON:encode(Plugin.allStats())`)
	if err != nil {
		return nil, fmt.Errorf("failed to get plugin stats: %w", err)
	}

	if result == "" || result == "nil" {
		return &AllPluginStats{
			Plugins: make(map[string]PluginStats),
		}, nil
	}

	// Parse the JSON result
	var raw struct {
		Plugins map[string]PluginStats `json:"plugins"`
		Totals  PluginTotals           `json:"totals"`
		Lua     struct {
			MemoryKB float64 `json:"memoryKB"`
		} `json:"lua"`
	}

	if err := json.Unmarshal([]byte(result), &raw); err != nil {
		return nil, fmt.Errorf("failed to parse plugin stats: %w", err)
	}

	return &AllPluginStats{
		Plugins:     raw.Plugins,
		Totals:      raw.Totals,
		LuaMemoryKB: raw.Lua.MemoryKB,
	}, nil
}

// GetPluginCount returns the number of active plugins
func (le *LuaEngine) GetPluginCount() int {
	result, err := le.Execute(`#Plugin.list()`)
	if err != nil {
		return 0
	}
	var count int
	fmt.Sscanf(result, "%d", &count)
	return count
}

// ClearAllTimers cancels all active timers
func (le *LuaEngine) ClearAllTimers() {
	le.timerManager.ClearAll()
}

// --- Work Queue Infrastructure ---

// submitToLoop sends a work item to the given queue and waits for the result.
// In stateInit (before RunLoop), falls back to executeDirect with mutex.
// The waitCh parameter is the bidirectional channel the caller retains for reading.
func (le *LuaEngine) submitToLoop(queue chan luaWorkItem, item luaWorkItem, waitCh <-chan luaResult) (string, error) {
	s := engineState(le.engineState.Load())
	switch s {
	case stateStopping, stateClosed, stateFailed:
		return "", fmt.Errorf("Lua engine is %s", s)
	case stateInit:
		return le.executeDirect(item.fn)
	}

	// stateRunning — submit to channel
	timer := time.NewTimer(30 * time.Second)
	defer timer.Stop()

	select {
	case queue <- item:
	case <-timer.C:
		return "", fmt.Errorf("Lua engine overloaded: %s queue full for 30s", item.kind)
	case <-le.drainDone:
		return "", fmt.Errorf("Lua engine closed while submitting")
	}

	select {
	case res := <-waitCh:
		return res.value, res.err
	case <-le.drainDone:
		return "", fmt.Errorf("Lua engine closed while waiting for result")
	}
}

// executeDirect runs fn under mutex — only allowed in stateInit (before RunLoop).
func (le *LuaEngine) executeDirect(fn func(L *lua.LState) (string, error)) (string, error) {
	le.mu.Lock()
	defer le.mu.Unlock()
	if le.state == nil {
		return "", fmt.Errorf("Lua engine is closed")
	}
	return fn(le.state)
}

// executeFast submits a fast work item (quick reads, dashboard, registry).
func (le *LuaEngine) executeFast(name string, fn func(L *lua.LState) (string, error)) (string, error) {
	ch := make(chan luaResult, 1)
	item := luaWorkItem{
		name: name, kind: workFast, fn: fn,
		resultCh: ch, submitTime: time.Now(),
	}
	return le.submitToLoop(le.fastQueue, item, ch)
}

// executeSlow submits a slow work item (arbitrary scripts, doFile).
func (le *LuaEngine) executeSlow(name string, fn func(L *lua.LState) (string, error)) (string, error) {
	ch := make(chan luaResult, 1)
	item := luaWorkItem{
		name: name, kind: workSlow, fn: fn,
		resultCh: ch, submitTime: time.Now(),
	}
	return le.submitToLoop(le.slowQueue, item, ch)
}

// processWorkItemOnLoop executes a work item on the RunLoop goroutine.
// MUST be called only from the RunLoop goroutine.
func (le *LuaEngine) processWorkItemOnLoop(item luaWorkItem) {
	if le.state == nil {
		item.resultCh <- luaResult{err: fmt.Errorf("Lua engine state is nil")}
		return
	}

	start := time.Now()
	var value string
	var err error

	// Recover from panics — single crash must NOT kill the loop
	func() {
		defer func() {
			if r := recover(); r != nil {
				err = fmt.Errorf("panic in work item %q: %v", item.name, r)
				log.Printf("[RUNTIME] %v\n%s", err, debug.Stack())
			}
		}()
		resetStateContext(le.state)
		value, err = item.fn(le.state)
	}()

	elapsed := time.Since(start)
	le.addBusyTime(elapsed)

	// Log queue latency for slow items
	queueLatency := start.Sub(item.submitTime)
	if queueLatency > 100*time.Millisecond {
		log.Printf("[RUNTIME] Work item %q waited %.0fms in queue", item.name, float64(queueLatency.Milliseconds()))
	}

	item.resultCh <- luaResult{value: value, err: err}
}

// drainQueueOnLoop processes up to max items from a queue. Non-blocking.
// MUST be called only from the RunLoop goroutine.
func (le *LuaEngine) drainQueueOnLoop(queue chan luaWorkItem, max int) {
	for i := 0; i < max; i++ {
		select {
		case item := <-queue:
			le.processWorkItemOnLoop(item)
		default:
			return
		}
	}
}

// closeDrain closes drainDone once: shutdown, a RunLoop panic during shutdown
// and Close in stateInit may all get there.
func (le *LuaEngine) closeDrain() {
	le.drainOnce.Do(func() { close(le.drainDone) })
}

// drainAllQueuesOnShutdown closes drainDone and returns errors to all pending items.
func (le *LuaEngine) drainAllQueuesOnShutdown() {
	le.closeDrain()
	shutdownErr := fmt.Errorf("Lua engine is shutting down")
	for {
		select {
		case item := <-le.fastQueue:
			item.resultCh <- luaResult{err: shutdownErr}
		case item := <-le.slowQueue:
			item.resultCh <- luaResult{err: shutdownErr}
		default:
			return
		}
	}
}

// drainAllQueuesWithError returns the given error to all pending items.
func (le *LuaEngine) drainAllQueuesWithError(err error) {
	for {
		select {
		case item := <-le.fastQueue:
			item.resultCh <- luaResult{err: err}
		case item := <-le.slowQueue:
			item.resultCh <- luaResult{err: err}
		default:
			return
		}
	}
}

// RunLoop runs the main event loop, processing timers, HTTP server requests, and work queues.
// This blocks until Stop is called.
func (le *LuaEngine) RunLoop(stopChan <-chan struct{}) {
	le.engineState.Store(int32(stateRunning))
	defer close(le.loopDone)

	// Recover from catastrophic panics — transition to stateFailed
	defer func() {
		if r := recover(); r != nil {
			log.Printf("[RUNTIME] FATAL: RunLoop panic: %v\n%s", r, debug.Stack())
			le.engineState.Store(int32(stateFailed))
			le.closeDrain()
			le.drainAllQueuesWithError(fmt.Errorf("RunLoop crashed: %v", r))
		}
	}()

	le.StartTimers()
	defer le.StopTimers()

	// Register runtime metrics
	if le.metricsCollector != nil {
		le.metricsCollector.Register("runtime:busy_percent", "%", 10)
		le.metricsCollector.Register("runtime:loop_lag_ms", "ms", 10)
	}
	le.lastSnapTime = time.Now()
	le.lastBusySnap = le.busyTimeNs.Load()

	// Watchdog: detect stuck runtime
	var lastTick atomic.Int64
	lastTick.Store(time.Now().UnixMilli())

	go func() {
		watchdog := time.NewTicker(5 * time.Second)
		defer watchdog.Stop()
		for {
			select {
			case <-stopChan:
				return
			case <-watchdog.C:
				stuckMs := time.Now().UnixMilli() - lastTick.Load()
				if stuckMs > 10000 {
					log.Printf("[WATCHDOG] Runtime stuck for %.1fs", float64(stuckMs)/1000)
				}
			}
		}
	}()

	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()

	metricsTicker := time.NewTicker(1 * time.Second)
	defer metricsTicker.Stop()

	busyTicker := time.NewTicker(10 * time.Second)
	defer busyTicker.Stop()

	gcTicker := time.NewTicker(30 * time.Second)
	defer gcTicker.Stop()

	var lastTickTime time.Time

	for {
		select {
		case <-stopChan:
			le.engineState.Store(int32(stateStopping))
			le.drainAllQueuesOnShutdown()
			flushPersist(le.state)
			le.engineState.Store(int32(stateClosed))
			return

		// Fast queue gets its own select case for immediate processing
		case item := <-le.fastQueue:
			le.processWorkItemOnLoop(item)

		case <-ticker.C:
			now := time.Now()
			lastTick.Store(now.UnixMilli())

			// Measure event loop lag (actual delta - expected 10ms)
			if !lastTickTime.IsZero() {
				lagNs := now.Sub(lastTickTime).Nanoseconds() - int64(10*time.Millisecond)
				if lagNs > 0 {
					for {
						old := le.maxLagNs.Load()
						if lagNs <= old || le.maxLagNs.CompareAndSwap(old, lagNs) {
							break
						}
					}
				}
			}
			lastTickTime = now

			// Priority drain: fast first, then slow
			le.drainQueueOnLoop(le.fastQueue, 32)
			le.drainQueueOnLoop(le.slowQueue, 8)

			le.processTimersOnLoop()
			le.processHTTPServerOnLoop()
			le.processGPIOOnLoop()

			// Post-event: drain fast again
			le.drainQueueOnLoop(le.fastQueue, 16)

		case <-metricsTicker.C:
			le.collectMetricsOnLoop()
		case <-busyTicker.C:
			le.collectBusyPercentOnLoop()
		case <-gcTicker.C:
			le.luaGCStep()
		}
	}
}

// luaGCStep triggers garbage collection off the event loop.
// NOTE: gopher-lua's collectgarbage() ignores arguments and calls runtime.GC()
// (a full stop-the-world GC). Running it on the event loop blocks ticks for
// 100-300ms+ on ARM v6 as heap grows. Instead we call runtime.GC() in a
// separate goroutine so the event loop stays responsive.
func (le *LuaEngine) luaGCStep() {
	go runtime.GC()
}

// collectBusyPercentOnLoop computes runtime_busy_percent and ingests it into the metrics collector.
// Called from RunLoop.
func (le *LuaEngine) collectBusyPercentOnLoop() {
	if le.metricsCollector == nil {
		return
	}
	now := time.Now()
	currentNs := le.busyTimeNs.Load()
	deltaNs := currentNs - le.lastBusySnap
	wallNs := now.Sub(le.lastSnapTime).Nanoseconds()

	le.lastBusySnap = currentNs
	le.lastSnapTime = now

	if wallNs <= 0 {
		return
	}

	pct := float64(deltaNs) / float64(wallNs) * 100
	if pct > 100 {
		pct = 100
	}

	// Collect max loop lag and reset
	lagNs := le.maxLagNs.Swap(0)
	lagMs := float64(lagNs) / 1e6

	le.metricsCollector.Ingest([]metrics.CollectResult{
		{Path: "runtime:busy_percent", Value: pct},
		{Path: "runtime:loop_lag_ms", Value: lagMs},
	})

	// Also record in AppMetrics for stats page charts
	if le.mqttManager != nil {
		if m := le.mqttManager.GetMetrics(); m != nil {
			m.RecordLuaBusy(pct, lagMs)
		}
	}

	if pct > 80 {
		log.Printf("[RUNTIME] High load: runtime busy %.0f%% (last 10s)", pct)
	}
	if lagMs > 1000 {
		log.Printf("[RUNTIME] High event loop lag: %.0fms (last 10s)", lagMs)
	}
}

// collectMetricsOnLoop polls due metrics via Lua getters and ingests the results.
// Called from RunLoop — uses direct LState access (no queue).
func (le *LuaEngine) collectMetricsOnLoop() {
	if le.metricsCollector == nil {
		return
	}
	due := le.metricsCollector.DueMetrics()
	if len(due) == 0 {
		return
	}

	pathsJSON, _ := json.Marshal(due)

	raw, err := callLuaFunctionOnState(le.state, "Metrics._collectJSON", string(pathsJSON))
	if err != nil {
		return
	}

	var results []metrics.CollectResult
	if json.Unmarshal([]byte(raw), &results) == nil && len(results) > 0 {
		le.metricsCollector.Ingest(results)
	}
}

// collectMetrics is the public wrapper (for tests or pre-loop usage).
func (le *LuaEngine) collectMetrics() {
	le.mu.Lock()
	defer le.mu.Unlock()
	le.collectMetricsOnLoop()
}

// processHTTPServerOnLoop processes pending HTTP server requests on the RunLoop goroutine.
// No mutex needed — called only from RunLoop which owns the LState.
func (le *LuaEngine) processHTTPServerOnLoop() int {
	if le.state == nil || le.httpServerManager == nil {
		return 0
	}

	count := 0
	for {
		req := le.httpServerManager.GetPendingRequest()
		if req == nil {
			break
		}

		reqJSON, _ := json.Marshal(req)

		start := time.Now()
		// an HTTP request to a Lua endpoint (plugin, module) comes from outside
		resetStateContext(le.state)
		restore := withStateContext(le.state, "api", "service:http", "")
		_, err := executeOnState(le.state, fmt.Sprintf(`HTTPServer:_handleRequest(%q)`, string(reqJSON)))
		restore()
		elapsed := time.Since(start)
		le.addBusyTime(elapsed)

		if err != nil {
			log.Printf("[HTTP-SERVER] Error executing handler: %v", err)
			le.httpServerManager.Respond(req.ID, 500, "Internal Server Error", nil)
		}

		if elapsed > 2*time.Second {
			log.Printf("[HTTP-SERVER] Slow handler for %s: took %v", req.Path, elapsed)
		} else if elapsed > 500*time.Millisecond {
			log.Printf("[HTTP-SERVER] Slow handler for %s: took %v", req.Path, elapsed)
		}

		count++
	}
	return count
}

// ProcessHTTPServer is the public wrapper for tests (stateInit with mutex fallback).
func (le *LuaEngine) ProcessHTTPServer() int {
	le.mu.Lock()
	defer le.mu.Unlock()
	return le.processHTTPServerOnLoop()
}

// Close closes the Lua engine.
// If RunLoop is running, it handles shutdown via stopChan.
// If stateInit (RunLoop never started), we close directly.
func (le *LuaEngine) Close() {
	prev := engineState(le.engineState.Load())
	if prev == stateClosed || prev == stateFailed {
		return
	}

	// RunLoop owns the state until it returns: wait for it (it drains the
	// queues and flushes persist) before closing the state underneath it
	if prev == stateRunning || prev == stateStopping {
		select {
		case <-le.loopDone:
		case <-time.After(5 * time.Second):
			log.Printf("[LUA] Close: RunLoop did not stop within 5 s")
		}
	}

	// If stateInit (RunLoop never started), close drainDone and transition directly
	if prev == stateInit {
		le.engineState.Store(int32(stateClosed))
		le.closeDrain()
	}
	// In stateRunning/stateStopping, RunLoop handles drain + state transitions via stopChan.

	// Stop managers, close Lua state
	le.mu.Lock()
	state := le.state
	le.state = nil
	le.mu.Unlock()

	if le.timerManager != nil {
		le.timerManager.Stop()
	}
	if le.mqttManager != nil {
		le.mqttManager.Close()
	}
	if le.modbusManager != nil {
		le.modbusManager.Close()
	}
	if le.httpServerManager != nil {
		le.httpServerManager.Stop()
	}
	if le.gpioManager != nil {
		le.gpioManager.Close()
	}
	if le.satelManager != nil {
		le.satelManager.Close()
	}
	if state != nil {
		if prev == stateInit {
			flushPersist(state) // RunLoop never ran, nobody flushed
		}
		state.Close()
	}
}

// GetMQTTStats returns current MQTT statistics
func (le *LuaEngine) GetMQTTStats() MQTTStats {
	if le.mqttManager == nil {
		return MQTTStats{}
	}
	return le.mqttManager.Stats()
}

// Reinitialize reinitializes the Lua engine with a fresh state.
// In stateRunning, routed through fastQueue (lifecycle). In stateInit, runs directly.
func (le *LuaEngine) Reinitialize(cluName string) error {
	// Drop the previous load report up front. A reload can fail before it reaches
	// LoadUserModules, and a stale report of past successes would then let callers
	// conclude the modules are fine while the runtime is in fact broken.
	le.setLastModuleLoad(nil)

	_, err := le.executeFast("Reinitialize", func(L *lua.LState) (string, error) {
		return "", le.reinitializeInner(cluName)
	})
	return err
}

// reinitializeInner performs the actual reinitialization.
// MUST be called on the RunLoop goroutine (or under mutex in stateInit).
func (le *LuaEngine) reinitializeInner(cluName string) error {
	// Close existing state if any
	if le.state != nil {
		flushPersist(le.state)
		le.state.Close()
		le.state = nil
	}
	le.scriptCache = nil // invalidate compiled script cache

	log.Printf("[LUA] reinitializing...")

	// Use shared initialization (handles timer/http/mqtt managers and bootstrap)
	if err := le.initializeLuaState(cluName); err != nil {
		return fmt.Errorf("failed to reinitialize Lua state: %w", err)
	}

	// Start timer manager after reinitialization
	le.timerManager.Start()

	// Restore custom globals (like __vclu_ip)
	for name, value := range le.customGlobals {
		le.state.SetGlobal(name, goToLuaValue(le.state, value))
	}

	// Re-register __go_send_command if commandSender is set
	if le.commandSender != nil {
		sender := le.commandSender
		le.state.SetGlobal("__go_send_command", le.state.NewFunction(func(L *lua.LState) int {
			targetIP := L.CheckString(1)
			script := L.CheckString(2)
			delayMs := L.OptInt(3, 0)

			result, err := sender(targetIP, script)
			if err != nil {
				L.Push(lua.LNil)
				L.Push(lua.LString(err.Error()))
				return 2
			}

			if delayMs > 0 {
				time.Sleep(time.Duration(delayMs) * time.Millisecond)
			}

			L.Push(lua.LString(result))
			L.Push(lua.LNil)
			return 2
		}))
	}

	if le.commandSender != nil {
		le.registerSendAsync(le.state)
	}

	// Re-register __go_send_command_noreply if noReply sender is set
	if le.commandNoReply != nil {
		noReply := le.commandNoReply
		le.state.SetGlobal("__go_send_command_noreply", le.state.NewFunction(func(L *lua.LState) int {
			targetIP := L.CheckString(1)
			script := L.CheckString(2)
			if err := noReply(targetIP, script); err != nil {
				L.Push(lua.LFalse)
				L.Push(lua.LString(err.Error()))
				return 2
			}
			L.Push(lua.LTrue)
			L.Push(lua.LNil)
			return 2
		}))
	}

	// Re-register __go_homekit_notify if homeKitStateHandler is set
	if le.homeKitStateHandler != nil {
		handler := le.homeKitStateHandler
		le.state.SetGlobal("__go_homekit_notify", le.state.NewFunction(func(L *lua.LState) int {
			path := L.CheckString(1)
			value := L.CheckNumber(2)
			objType := L.OptString(3, "")
			handler(path, float64(value), objType)
			return 0
		}))
	}

	// Re-register __go_client_push_notify if clientPushHandler is set
	if le.clientPushHandler != nil {
		handler := le.clientPushHandler
		le.state.SetGlobal("__go_client_push_notify", le.state.NewFunction(func(L *lua.LState) int {
			handler()
			return 0
		}))
	}

	// Re-register metrics bridge if collector is set
	if le.metricsCollector != nil {
		registerMetricsFunctions(le.state, le.metricsCollector)
	}
	registerHistoryFunctions(le.state, le.historyStore)
	if le.historyStore != nil {
		le.historyStore.BeginReload()
	}

	log.Printf("[LUA] reinitialized")
	return nil
}

// luaValueToString converts a Lua value to a string representation
func luaValueToString(val lua.LValue) string {
	switch v := val.(type) {
	case lua.LString:
		return string(v)
	case lua.LNumber:
		return fmt.Sprintf("%v", float64(v))
	case lua.LBool:
		return fmt.Sprintf("%v", bool(v))
	case *lua.LNilType:
		return "nil"
	case *lua.LTable:
		// For tables, return a simple representation
		return "{...}"
	default:
		return val.String()
	}
}

// goToLuaValue converts a Go value to a Lua value
func goToLuaValue(L *lua.LState, val interface{}) lua.LValue {
	switch v := val.(type) {
	case string:
		return lua.LString(v)
	case int:
		return lua.LNumber(v)
	case float64:
		return lua.LNumber(v)
	case bool:
		return lua.LBool(v)
	case nil:
		return lua.LNil
	default:
		return lua.LString(fmt.Sprintf("%v", v))
	}
}

// SetCommandSender sets the function used to send commands to remote CLUs.
// Must be called before RunLoop (stateInit).
func (le *LuaEngine) SetCommandSender(sender CommandSender) {
	le.executeFast("SetCommandSender", func(L *lua.LState) (string, error) {
		le.commandSender = sender

		L.SetGlobal("__go_send_command", L.NewFunction(func(L *lua.LState) int {
			targetIP := L.CheckString(1)
			script := L.CheckString(2)
			delayMs := L.OptInt(3, 0)

			if le.commandSender == nil {
				L.Push(lua.LNil)
				L.Push(lua.LString("command sender not configured"))
				return 2
			}

			result, err := le.commandSender(targetIP, script)
			if err != nil {
				L.Push(lua.LNil)
				L.Push(lua.LString(err.Error()))
				return 2
			}

			if delayMs > 0 {
				time.Sleep(time.Duration(delayMs) * time.Millisecond)
			}

			L.Push(lua.LString(result))
			L.Push(lua.LNil)
			return 2
		}))
		le.registerSendAsync(L)
		return "", nil
	})
}

// registerSendAsync registers __go_send_command_async(ip, script, id): the command is
// sent off the run loop and its answer comes back as __vclu_command_reply(id, result, err),
// a global looked up when the answer arrives, so a reload in between does no harm.
func (le *LuaEngine) registerSendAsync(L *lua.LState) {
	L.SetGlobal("__go_send_command_async", L.NewFunction(func(L *lua.LState) int {
		targetIP := L.CheckString(1)
		script := L.CheckString(2)
		id := L.CheckString(3)
		sender := le.commandSender
		if sender == nil {
			L.Push(lua.LFalse)
			return 1
		}
		go func() {
			result, err := sender(targetIP, script)
			errStr := ""
			if err != nil {
				errStr = err.Error()
			}
			_, _ = le.executeFast("CommandReply", func(L *lua.LState) (string, error) {
				fn := L.GetGlobal("__vclu_command_reply")
				if fn == lua.LNil {
					return "", nil
				}
				errVal := lua.LValue(lua.LNil)
				if errStr != "" {
					errVal = lua.LString(errStr)
				}
				return "", L.CallByParam(lua.P{Fn: fn, NRet: 0, Protect: true},
					lua.LString(id), lua.LString(result), errVal)
			})
		}()
		L.Push(lua.LTrue)
		return 1
	}))
}

// SetCommandNoReplySender sets the fire-and-forget command sender.
// Must be called before RunLoop (stateInit).
func (le *LuaEngine) SetCommandNoReplySender(sender CommandNoReplySender) {
	le.executeFast("SetCommandNoReplySender", func(L *lua.LState) (string, error) {
		le.commandNoReply = sender

		L.SetGlobal("__go_send_command_noreply", L.NewFunction(func(L *lua.LState) int {
			targetIP := L.CheckString(1)
			script := L.CheckString(2)

			if le.commandNoReply == nil {
				L.Push(lua.LFalse)
				L.Push(lua.LString("no-reply sender not configured"))
				return 2
			}

			err := le.commandNoReply(targetIP, script)
			if err != nil {
				L.Push(lua.LFalse)
				L.Push(lua.LString(err.Error()))
				return 2
			}

			L.Push(lua.LTrue)
			L.Push(lua.LNil)
			return 2
		}))
		return "", nil
	})
}

// SetHomeKitStateHandler sets the callback for HomeKit state change notifications.
func (le *LuaEngine) SetHomeKitStateHandler(handler HomeKitStateHandler) {
	le.executeFast("SetHomeKitStateHandler", func(L *lua.LState) (string, error) {
		le.homeKitStateHandler = handler

		L.SetGlobal("__go_homekit_notify", L.NewFunction(func(L *lua.LState) int {
			path := L.CheckString(1)
			value := L.CheckNumber(2)
			objType := L.OptString(3, "")

			if le.homeKitStateHandler != nil {
				le.homeKitStateHandler(path, float64(value), objType)
			}
			return 0
		}))
		return "", nil
	})
}

// SetClientPushHandler sets the callback for immediate client report notifications.
func (le *LuaEngine) SetClientPushHandler(handler ClientPushHandler) {
	le.executeFast("SetClientPushHandler", func(L *lua.LState) (string, error) {
		le.clientPushHandler = handler

		L.SetGlobal("__go_client_push_notify", L.NewFunction(func(L *lua.LState) int {
			if le.clientPushHandler != nil {
				le.clientPushHandler()
			}
			return 0
		}))
		return "", nil
	})
}

// SetKVStoreManager sets the KV store manager for plugin persistence.
func (le *LuaEngine) SetKVStoreManager(kvManager *plugins.KVStoreManager) {
	le.executeFast("SetKVStoreManager", func(L *lua.LState) (string, error) {
		le.kvManager = kvManager
		registerKVFunctions(L, kvManager)
		return "", nil
	})
}

// SetGPIOManager sets the GPIO manager for hardware pin access.
func (le *LuaEngine) SetGPIOManager(gm *GPIOManager) {
	le.executeFast("SetGPIOManager", func(L *lua.LState) (string, error) {
		le.gpioManager = gm
		registerGPIOFunctions(L, gm)
		// Start GPIO polling timer — bootstrap_go.lua check ran before
		// SetGPIOManager was called, so __go_gpio_poll was nil at that time.
		if err := L.DoString(`
			if __go_gpio_poll and gpio and gpio.pollChanges then
				EventBus:getShared():setInterval(function()
					gpio.pollChanges()
				end, 50)
			end
		`); err != nil {
			log.Printf("[GPIO] failed to start polling timer: %v", err)
		}
		return "", nil
	})
}

// SetMetricsCollector sets the metrics collector for dashboard charts.
func (le *LuaEngine) SetMetricsCollector(mc *metrics.Collector) {
	le.executeFast("SetMetricsCollector", func(L *lua.LState) (string, error) {
		le.metricsCollector = mc
		registerMetricsFunctions(L, mc)
		if err := L.DoString("Metrics._syncToGo()"); err != nil {
			log.Printf("[metrics] sync existing metrics to Go: %v", err)
		}
		return "", nil
	})
}

// SetCameraManager sets the camera manager for snapshot proxy.
func (le *LuaEngine) SetCameraManager(cm *camera.Manager) {
	le.executeFast("SetCameraManager", func(L *lua.LState) (string, error) {
		le.cameraManager = cm
		registerCameraFunctions(L, cm)
		if err := L.DoString("Camera._syncToGo()"); err != nil {
			log.Printf("[camera] sync existing cameras to Go: %v", err)
		}
		return "", nil
	})
}

// processGPIOOnLoop polls GPIO inputs — called from RunLoop ticker.
func (le *LuaEngine) processGPIOOnLoop() {
	if le.gpioManager == nil {
		return
	}
	le.gpioManager.PollInputs()
}

// ProcessGPIO is the public wrapper for tests.
func (le *LuaEngine) ProcessGPIO() {
	le.processGPIOOnLoop()
}

// GetMQTTManager returns the MQTT manager instance.
func (le *LuaEngine) GetMQTTManager() *MQTTManager {
	return le.mqttManager
}

// GetRuntimePath returns the Lua runtime path (for sandbox engine).
func (le *LuaEngine) GetRuntimePath() string {
	return le.runtimePath
}

// SetHTTPRequestCallback sets the callback for HTTP request metrics (for plugins).
func (le *LuaEngine) SetHTTPRequestCallback(fn func(hasError bool)) {
	// httpManager is goroutine-safe — no need to route through LState queue
	if le.httpManager != nil {
		le.httpManager.SetOnRequest(fn)
	}
}
