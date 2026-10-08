package lua

import (
	"fmt"
	"testing"
	"time"

	"github.com/Domktorymysli/vclu/pkg/plugins"
)

func newPersistEngine(t *testing.T) (*LuaEngine, string) {
	t.Helper()
	dir := t.TempDir()
	engine := NewLuaEngine()
	t.Cleanup(engine.Close)
	engine.SetKVStoreManager(plugins.NewKVStoreManager(dir))
	return engine, dir
}

func TestPersist_ProxyAndValidation(t *testing.T) {
	engine, _ := newPersistEngine(t)
	luaOK(t, engine, `
		local st = persist("wiatrolap", { pauseUntil = 0, mode = "auto" })
		assert(st.pauseUntil == 0 and st.mode == "auto", "defaults")
		st.pauseUntil = 123           -- existing key: must still go through __newindex
		assert(st.pauseUntil == 123 and st:get("pauseUntil") == 123)
		st:set("count", 2)
		local t = st:toTable()
		assert(t.count == 2 and t.mode == "auto", "toTable")
		t.count = 99
		assert(st.count == 2, "toTable must return a copy")
		assert(persist("wiatrolap") == st, "one proxy per name")
		assert(rawget(st, "pauseUntil") == nil, "data must not live in the proxy")

		for _, bad in ipairs({ "", "a/b", "..", "a..b", ".x", string.rep("a", 65), 5 }) do
			assert(not pcall(persist, bad), "accepted name " .. tostring(bad))
		end
		assert(not pcall(persist, "x", { save = 1 }), "reserved default accepted")
		assert(not pcall(function() st.toTable = 1 end), "reserved key assigned")
	`)
}

// State written before a reload is there after it, also when the debounce
// timer had no chance to run: the reload flushes it.
func TestPersist_SurvivesReinitialize(t *testing.T) {
	engine, _ := newPersistEngine(t)
	luaOK(t, engine, `
		local st = persist("wiatrolap", { pauseUntil = 0 })
		st.pauseUntil = 1791400000
	`)
	if err := engine.Reinitialize("CLU"); err != nil {
		t.Fatal(err)
	}
	luaOK(t, engine, `
		local st = persist("wiatrolap", { pauseUntil = 0, added = "x" })
		assert(st.pauseUntil == 1791400000, "pauseUntil lost: " .. tostring(st.pauseUntil))
		assert(st.added == "x", "new default must be merged in")
	`)
}

// A process restart reads the KV file written by the flush at shutdown.
func TestPersist_SurvivesRestart(t *testing.T) {
	dir := t.TempDir()
	engine := NewLuaEngine()
	engine.SetKVStoreManager(plugins.NewKVStoreManager(dir))
	luaOK(t, engine, `persist("energy", { total = 0 }).total = 42`)
	engine.Close()

	engine2 := NewLuaEngine()
	defer engine2.Close()
	engine2.SetKVStoreManager(plugins.NewKVStoreManager(dir))
	luaOK(t, engine2, `assert(persist("energy", { total = 0 }).total == 42, "not saved at shutdown")`)
}

func TestPersist_CorruptStateFallsBackToDefaults(t *testing.T) {
	engine, _ := newPersistEngine(t)
	luaOK(t, engine, `
		__go_kv_set("module:broken", "state", "not a table")
		local st = persist("broken", { a = 1 })
		assert(st.a == 1, "defaults")
		assert(Persist.errors["module:broken"], "error must be recorded")
	`)
}

// With the loop running, a change reaches KV after the debounce on its own.
func TestPersist_DebouncedWrite(t *testing.T) {
	dir := t.TempDir()
	engine := NewLuaEngine()
	kv := plugins.NewKVStoreManager(dir)
	engine.SetKVStoreManager(kv)
	stop, done := make(chan struct{}), make(chan struct{})
	go func() { defer close(done); engine.RunLoop(stop) }()
	defer func() { close(stop); <-done; engine.Close() }()
	for engineState(engine.engineState.Load()) != stateRunning {
		time.Sleep(time.Millisecond)
	}
	if _, err := engine.Execute(`
		Persist.DEBOUNCE_MS = 50
		local st = persist("deb", { n = 0 })
		for i = 1, 10 do st.n = i end
	`); err != nil {
		t.Fatal(err)
	}
	if _, ok := kv.GetStore("module:deb").Get("state"); ok {
		t.Fatal("written before the debounce")
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if v, ok := kv.GetStore("module:deb").Get("state"); ok {
			if m, _ := v.(map[string]interface{}); m != nil && fmt.Sprint(m["n"]) == "10" {
				return
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("debounced write never reached KV with the last value")
}

func TestPersist_Hardening(t *testing.T) {
	engine, _ := newPersistEngine(t)
	luaOK(t, engine, `
		local a = persist("Shared", { x = 1 })
		local b = persist("shared", { y = 2 })
		assert(a == b, "names are case-insensitive")
		assert(b.x == 1 and b.y == 2, "second caller's defaults must be merged in")
		assert(not pcall(function() a.f = function() end end), "function accepted")
		assert(not pcall(function() a[1] = 5 end), "numeric key accepted")
		assert(not pcall(persist, "z", { g = print }), "function default accepted")
		a.flag = false
		assert(a.flag == false, "false must be a value, not missing")
	`)
}

// Production shutdown: Server.Stop cancels the loop and calls Close at once.
// A change made just before must still reach KV.
func TestPersist_FlushOnProductionShutdown(t *testing.T) {
	for i := 0; i < 10; i++ {
		dir := t.TempDir()
		engine := NewLuaEngine()
		engine.SetKVStoreManager(plugins.NewKVStoreManager(dir))
		stop := make(chan struct{})
		go engine.RunLoop(stop)
		for engineState(engine.engineState.Load()) != stateRunning {
			time.Sleep(time.Millisecond)
		}
		if _, err := engine.Execute(`persist("shutdown", { n = 0 }).n = 7`); err != nil {
			t.Fatal(err)
		}
		close(stop)
		engine.Close()

		next := NewLuaEngine()
		next.SetKVStoreManager(plugins.NewKVStoreManager(dir))
		luaOK(t, next, `assert(persist("shutdown", { n = 0 }).n == 7, "lost at shutdown")`)
		next.Close()
	}
}
