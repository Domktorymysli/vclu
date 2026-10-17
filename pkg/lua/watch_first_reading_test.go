package lua

import (
	"testing"
	"time"

	"github.com/Domktorymysli/vclu/pkg/history"
	"github.com/Domktorymysli/vclu/pkg/plugins"
)

// StateBus:watch and the first reading of an object (#49): a watch fires on
// the changes the history records, integrations still get every reading.

// newHistoryEngineWith is newHistoryEngine with a custom store config; the
// clock is shared by the store and the Lua runtime.
func newHistoryEngineWith(t *testing.T, cfg history.Config, clk *stepClock) (*LuaEngine, *history.Store) {
	t.Helper()
	engine := NewLuaEngine()
	t.Cleanup(engine.Close)
	store := history.New(cfg, clk.now)
	engine.SetHistoryStore(store)
	SetClock(engine.state, fixedClockFunc(clk.now))
	return engine, store
}

// probe registers a CLU switch, a watch with opts and a raw state_changed
// listener; counters live in _G.c (gopher-lua detaches locals after a
// pcall-caught error).
const watchProbe = `
	local c = { fired = 0, events = 0, old = "none", oldValue = "none", baseline = "none" }
	_G.c = c
	local sw = RemoteSwitch:new({ executeNoReply = function() end }, "DIN1", "WEJSCIE")
	registerObject("CLU1.DIN1", sw)
	StateBus:getShared():on("state_changed", function(e)
		if e.path == "CLU1.DIN1" then c.events = c.events + 1; c.baseline = e.baseline end
	end)
`

// With history disabled nothing changed: the first reading is nil -> value
// and a { to = 1 } watch fires on it, also after a reload.
func TestWatch_HistoryDisabledFiresOnFirstReading(t *testing.T) {
	clk := &stepClock{t: time.Date(2026, 10, 7, 21, 0, 0, 0, time.UTC)}
	engine, _ := newHistoryEngineWith(t, history.Config{Enabled: false}, clk)
	script := watchProbe + `
		StateBus:watch("CLU1.DIN1", function(e) c.fired = c.fired + 1; c.old = e.old end, { to = 1 })
		_:sync("CLU1.DIN1", 1)
		assert(c.fired == 1 and c.old == nil, "first reading with history off: " .. c.fired .. " old " .. tostring(c.old))
		assert(c.events == 1 and c.baseline == nil, "no baseline flag with history off")
	`
	luaOK(t, engine, script)
	if err := engine.Reinitialize("CLU"); err != nil {
		t.Fatal(err)
	}
	luaOK(t, engine, script)
}

// expose(obj, type, { history = false }) keeps the object out of the history
// store, so there is no verdict: its first reading still fires a watch, on
// every reload. The documented limit of #49 (CHANGELOG): nothing is known.
func TestWatch_ExposedWithoutHistoryStillFiresOnFirstReading(t *testing.T) {
	engine, _, _ := newHistoryEngine(t)
	script := watchProbe + `
		expose(sw, "switch", { path = "salon.wejscie", history = false })
		StateBus:watch("salon.wejscie", function(e) c.fired = c.fired + 1; c.old = e.old end, { to = 1 })
		_:sync("CLU1.DIN1", 1)
		assert(sw:lastChange() == nil, "history = false must keep the object out of history")
	`
	luaOK(t, engine, script+`
		assert(c.fired == 1 and c.old == nil, "history=false, first reading: " .. c.fired)
	`)
	if err := engine.Reinitialize("CLU"); err != nil {
		t.Fatal(err)
	}
	luaOK(t, engine, script+`
		assert(c.fired == 1 and c.old == nil, "history=false, first reading after a reload: " .. c.fired)
	`)
}

// Changed while vCLU was not looking: known 0, first report after the reload
// is 1. The watch fires once with old = 0 (so { from = 0, to = 1 } matches),
// oldValue and lastChange() agree, and an identical second report is silent.
func TestWatch_ChangedDuringReloadFiresOnceWithKnownOld(t *testing.T) {
	engine, _, _ := newHistoryEngine(t)
	luaOK(t, engine, watchProbe+`
		_:sync("CLU1.DIN1", 1)
		_:sync("CLU1.DIN1", 0)
	`)
	if err := engine.Reinitialize("CLU"); err != nil {
		t.Fatal(err)
	}
	luaOK(t, engine, watchProbe+`
		StateBus:watch("CLU1.DIN1", function(e)
			c.fired = c.fired + 1; c.old = e.old; c.oldValue = e.oldValue
		end, { from = 0, to = 1 })
		_:sync("CLU1.DIN1", 1)
		assert(c.fired == 1, "from=0,to=1 after the reload: " .. c.fired)
		assert(c.old == 0 and c.oldValue == 0, "old " .. tostring(c.old) .. " oldValue " .. tostring(c.oldValue))
		assert(c.events == 1 and c.baseline == nil, "a real change is no baseline")
		local last = sw:lastChange()
		assert(last.old == 0 and last.value == 1 and last.origin == "system", "history: " .. tostring(last.old) .. ">" .. tostring(last.value))
		_:sync("CLU1.DIN1", 1)
		assert(c.fired == 1 and c.events == 1, "identical second report fired: " .. c.fired .. "/" .. c.events)
	`)
}

// A first report arriving after the 3-minute reload window: a CLU object
// (old nil) is still measured against the known value.
func TestWatch_FirstReportAfterReloadWindow(t *testing.T) {
	engine, _, clk := newHistoryEngine(t)
	luaOK(t, engine, watchProbe+`
		_:sync("CLU1.DIN1", 0)
		_:sync("CLU1.DIN1", 1)
	`)
	if err := engine.Reinitialize("CLU"); err != nil {
		t.Fatal(err)
	}
	clk.t = clk.t.Add(4 * time.Minute)
	luaOK(t, engine, watchProbe+`
		StateBus:watch("CLU1.DIN1", function(e) c.fired = c.fired + 1; c.old = e.old end)
		_:sync("CLU1.DIN1", 1)
		assert(c.fired == 0, "equal first report after the window fired")
		assert(c.events == 1 and c.baseline == true, "integrations get the baseline reading")
		_:sync("CLU1.DIN1", 0)
		assert(c.fired == 1 and c.old == 1, "change after the window: " .. c.fired .. " old " .. tostring(c.old))
	`)
}

// A virtual object restarts at its default after a reload; an automation
// setting it right back to the value it had is no change within the window
// (the { to = 1 } watch stays quiet), after the window it is a real change.
func TestWatch_VirtualDefaultSetBackAfterReload(t *testing.T) {
	engine, _, clk := newHistoryEngine(t)
	setup := `
		local c = { fired = 0, events = 0, old = "none", baseline = "none" }
		_G.c = c
		local v = VirtualSwitch:new("tryb")
		registerObject("virt.tryb", v)
		StateBus:getShared():on("state_changed", function(e)
			if e.path == "virt.tryb" then c.events = c.events + 1; c.baseline = e.baseline end
		end)
		StateBus:watch("virt.tryb", function(e) c.fired = c.fired + 1; c.old = e.old end, { to = 1 })
	`
	luaOK(t, engine, setup+`
		v:switchOn()
		assert(c.fired == 1 and c.old == 0, "first change of a virtual object: " .. c.fired)
	`)
	if err := engine.Reinitialize("CLU"); err != nil {
		t.Fatal(err)
	}
	luaOK(t, engine, setup+`
		v:switchOn()
		assert(c.fired == 0, "set back to the known value within the window fired")
		assert(c.events == 1 and c.baseline == true, "integrations still get the reading")
		v:switchOff()
		v:switchOn()
		assert(c.fired == 1, "the next real change: " .. c.fired)
	`)
	if err := engine.Reinitialize("CLU"); err != nil {
		t.Fatal(err)
	}
	clk.t = clk.t.Add(4 * time.Minute)
	luaOK(t, engine, setup+`
		v:switchOn()
		assert(c.fired == 1 and c.old == 0, "default -> value after the window: " .. c.fired)
	`)
}

// A persisted virtual object comes back with its value: nothing is published
// on restore, and the next change fires with the restored value as old.
func TestWatch_PersistedVirtualAfterReload(t *testing.T) {
	clk := &stepClock{t: time.Date(2026, 10, 7, 21, 0, 0, 0, time.UTC)}
	engine, _ := newHistoryEngineWith(t, history.DefaultConfig(), clk)
	engine.SetKVStoreManager(plugins.NewKVStoreManager(t.TempDir()))
	setup := `
		local c = { fired = 0, events = 0, old = "none" }
		_G.c = c
		local v = VirtualSwitch:new("tryb", { persist = true })
		registerObject("virt.tryb", v)
		StateBus:getShared():on("state_changed", function(e)
			if e.path == "virt.tryb" then c.events = c.events + 1 end
		end)
		StateBus:watch("virt.tryb", function(e) c.fired = c.fired + 1; c.old = e.old end)
	`
	luaOK(t, engine, setup+`
		v:switchOn()
	`)
	if err := engine.Reinitialize("CLU"); err != nil {
		t.Fatal(err)
	}
	luaOK(t, engine, setup+`
		assert(v:getValue() == 1, "persisted value restored")
		assert(c.events == 0, "restoring published an event")
		v:switchOn()
		assert(c.fired == 0 and c.events == 0, "same value published")
		v:switchOff()
		assert(c.fired == 1 and c.old == 1, "change after restore: " .. c.fired .. " old " .. tostring(c.old))
	`)
}

// A fresh process with history restored from disk: the first report equal
// to the restored value is quiet, a different one fires once with old = the
// restored value.
func TestWatch_RestoredFromDiskFiresOnlyOnDifference(t *testing.T) {
	cfg := history.DefaultConfig()
	cfg.Dir = t.TempDir()
	clk := &stepClock{t: time.Date(2026, 10, 7, 21, 0, 0, 0, time.UTC)}
	engine1, store1 := newHistoryEngineWith(t, cfg, clk)
	luaOK(t, engine1, watchProbe+`
		_:sync("CLU1.DIN1", 0)
		_:sync("CLU1.DIN1", 1)
	`)
	if err := store1.Flush(); err != nil {
		t.Fatal(err)
	}
	engine1.Close()

	clk.t = clk.t.Add(time.Minute)
	engine2, _ := newHistoryEngineWith(t, cfg, clk)
	luaOK(t, engine2, watchProbe+`
		StateBus:watch("CLU1.DIN1", function(e) c.fired = c.fired + 1; c.old = e.old end)
		_:sync("CLU1.DIN1", 1)
		assert(c.fired == 0, "report equal to the restored value fired")
		assert(c.events == 1 and c.baseline == true, "integrations get the reading after a restart")
		_:sync("CLU1.DIN1", 0)
		assert(c.fired == 1 and c.old == 1, "change since the restart: " .. c.fired .. " old " .. tostring(c.old))
		local last = sw:lastChange()
		assert(last.old == 1 and last.value == 0, "history after restore")
	`)
}

// "*", the name, the registry path and the expose path all skip a baseline
// and all fire on a real change.
func TestWatch_AllPathsHonourBaseline(t *testing.T) {
	engine, _, _ := newHistoryEngine(t)
	luaOK(t, engine, watchProbe+`
		expose(sw, "switch", { path = "salon.wejscie" })
		local n = { star = 0, name = 0, reg = 0, exp = 0 }
		_G.n = n
		StateBus:watch("*", function() n.star = n.star + 1 end)
		StateBus:watch("WEJSCIE", function() n.name = n.name + 1 end)
		StateBus:watch("CLU1.DIN1", function() n.reg = n.reg + 1 end)
		StateBus:watch("salon.wejscie", function() n.exp = n.exp + 1 end)
		_:sync("CLU1.DIN1", 1)
		assert(n.star == 0 and n.name == 0 and n.reg == 0 and n.exp == 0,
			string.format("baseline fired: *=%d name=%d reg=%d expose=%d", n.star, n.name, n.reg, n.exp))
		_:sync("CLU1.DIN1", 0)
		assert(n.star == 1 and n.name == 1 and n.reg == 1 and n.exp == 1,
			string.format("real change: *=%d name=%d reg=%d expose=%d", n.star, n.name, n.reg, n.exp))
	`)
}

// A known old value of any Lua type comes back through goValueToLua: the
// event carries it and an equal first reading is still recognised.
func TestWatch_KnownOldKeepsLuaType(t *testing.T) {
	engine, _, _ := newHistoryEngine(t)
	setup := `
		local c = { fired = 0, old = "none" }
		_G.c = c
		local clu = { executeNoReply = function() end }
		registerObject("CLU1.B", RemoteSwitch:new(clu, "B"))
		registerObject("CLU1.S", RemoteSwitch:new(clu, "S"))
		registerObject("CLU1.T", RemoteSwitch:new(clu, "T"))
		StateBus:watch("*", function(e) c.fired = c.fired + 1; c.old = e.old end)
	`
	luaOK(t, engine, setup+`
		_:sync("CLU1.B", false); _:sync("CLU1.B", true)
		_:sync("CLU1.S", "abc"); _:sync("CLU1.S", "def")
		_:sync("CLU1.T", { r = 1, g = 2 }); _:sync("CLU1.T", { r = 1, g = 3 })
		assert(c.fired == 3, "seeding: " .. c.fired)
	`)
	if err := engine.Reinitialize("CLU"); err != nil {
		t.Fatal(err)
	}
	luaOK(t, engine, setup+`
		_:sync("CLU1.B", true)
		_:sync("CLU1.S", "def")
		_:sync("CLU1.T", { r = 1, g = 3 })
		assert(c.fired == 0, "equal first readings fired: " .. c.fired)
	`)
	if err := engine.Reinitialize("CLU"); err != nil {
		t.Fatal(err)
	}
	luaOK(t, engine, setup+`
		_:sync("CLU1.B", false)
		assert(c.fired == 1 and c.old == true, "boolean known old: " .. tostring(c.old))
		_:sync("CLU1.S", "abc")
		assert(c.fired == 2 and c.old == "def", "string known old: " .. tostring(c.old))
		_:sync("CLU1.T", { r = 1, g = 4 })
		assert(c.fired == 3 and type(c.old) == "table" and c.old.r == 1 and c.old.g == 3,
			"table known old: " .. tostring(c.old))
	`)
}

// value -> nil -> value: clearing an object is a change and the value after
// nil is a plain change with old nil, not a first reading.
func TestWatch_NilValueIsAChange(t *testing.T) {
	engine, _, _ := newHistoryEngine(t)
	luaOK(t, engine, watchProbe+`
		StateBus:watch("CLU1.DIN1", function(e) c.fired = c.fired + 1; c.old = e.old end)
		_:sync("CLU1.DIN1", 1)
		sw:updateState(nil)
		assert(c.fired == 1 and c.old == 1 and sw:getValue() == nil, "clearing: " .. c.fired)
		_:sync("CLU1.DIN1", 2)
		assert(c.fired == 2 and c.old == nil, "value after nil: " .. c.fired .. " old " .. tostring(c.old))
		assert(c.events == 3 and c.baseline == nil, "events " .. c.events)
	`)
}

// A restart after more than MemoryHours: an input that changed while vCLU
// was down (a leak sensor, a door) fires its watch once, from the value
// known before the shutdown.
func TestWatch_RestartBeyondMemoryWindowFiresOnChange(t *testing.T) {
	clk := &stepClock{t: time.Date(2026, 10, 7, 21, 0, 0, 0, time.UTC)}
	cfg := history.DefaultConfig()
	cfg.Dir, cfg.FlushInterval = t.TempDir(), time.Hour
	store := history.New(cfg, clk.now)
	store.Record(history.Entry{Path: "CLU1.DIN1", Value: int64(0), Old: int64(1), Origin: "grenton", Actor: "clu:1"})
	if err := store.Close(time.Second); err != nil {
		t.Fatal(err)
	}

	clk.t = clk.t.Add(30 * time.Hour)
	engine := NewLuaEngine()
	t.Cleanup(engine.Close)
	store2 := history.New(cfg, clk.now)
	t.Cleanup(func() { _ = store2.Close(time.Second) })
	engine.SetHistoryStore(store2)
	SetClock(engine.state, fixedClockFunc(clk.now))
	luaOK(t, engine, `
		_G.c = { fired = 0, old = "none" }
		local sw = RemoteSwitch:new({ executeNoReply = function() end }, "DIN1", "WEJSCIE")
		registerObject("CLU1.DIN1", sw)
		StateBus:watch("CLU1.DIN1", function(e) c.fired = c.fired + 1; c.old = e.old end, { to = 1 })
		_:sync("CLU1.DIN1", 1)
		assert(c.fired == 1 and c.old == 0, "changed while down: fired " .. c.fired .. " old " .. tostring(c.old))
		_:sync("CLU1.DIN1", 0)
		_:sync("CLU1.DIN1", 1)
		assert(c.fired == 2, "a later change: " .. c.fired)
	`)
}
