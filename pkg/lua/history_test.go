package lua

import (
	"testing"
	"time"

	"github.com/Domktorymysli/vclu/pkg/history"
)

type stepClock struct{ t time.Time }

func (c *stepClock) now() time.Time { return c.t }

func newHistoryEngine(t *testing.T) (*LuaEngine, *history.Store, *stepClock) {
	t.Helper()
	engine := NewLuaEngine()
	t.Cleanup(engine.Close)
	clk := &stepClock{t: time.Date(2026, 10, 7, 21, 0, 0, 0, time.UTC)}
	store := history.New(history.DefaultConfig(), clk.now)
	engine.SetHistoryStore(store)
	SetClock(engine.state, fixedClockFunc(clk.now))
	return engine, store, clk
}

type fixedClockFunc func() time.Time

func (f fixedClockFunc) Now() time.Time { return f() }

func luaOK(t *testing.T, engine *LuaEngine, script string) {
	t.Helper()
	res, err := engine.Execute("local ok, err = pcall(function()\n" + script + "\nend)\nreturn ok and \"ok\" or tostring(err)")
	if err != nil {
		t.Fatal(err)
	}
	if res != "ok" {
		t.Fatal(res)
	}
}

func TestHistory_ObjectMethods(t *testing.T) {
	engine, _, clk := newHistoryEngine(t)
	luaOK(t, engine, `
		local sw = RemoteSwitch:new({ executeNoReply = function() end }, "DOU1")
		registerObject("CLU1.DOU1", sw)
		_:sync("CLU1.DOU1", 0)   -- first reading: baseline, no entry
		assert(sw:lastChange() == nil, "first reading recorded")
		assert(sw:since() == nil, "since without a change")
		assert(#sw:history() == 0)
		_:sync("CLU1.DOU1", 1)
	`)
	clk.t = clk.t.Add(90 * time.Second)
	luaOK(t, engine, `
		local sw = _:get("CLU1.DOU1")
		local last = sw:lastChange()
		assert(last and last.old == 0 and last.value == 1 and last.id ~= "", "lastChange")
		assert(sw:since() == 90, "since=" .. tostring(sw:since()))
		_:sync("CLU1.DOU1", 0)
		local h = sw:history()
		assert(#h == 2 and h[1].value == 0 and h[2].value == 1, "history newest first")
		assert(#sw:history(1) == 1)
	`)
}

// A watcher sees the change it is called for already in lastChange(), and a
// change it makes is recorded after its cause.
func TestHistory_RecordedBeforeWatchers(t *testing.T) {
	engine, store, _ := newHistoryEngine(t)
	luaOK(t, engine, `
		local a, b = VirtualSwitch:new("a"), VirtualSwitch:new("b")
		registerObject("virt.a", a)
		registerObject("virt.b", b)
		StateBus:watch("virt.a", function(e)
			local last = a:lastChange()
			assert(last and last.id == e.id, "watcher ran before the change was recorded")
			_:sync("virt.b", 1)
		end)
		_:sync("virt.a", 1)
	`)
	all := store.Query(history.Query{})
	if len(all) != 2 || all[0].Path != "virt.b" || all[1].Path != "virt.a" {
		t.Fatalf("order: %+v", all)
	}
}

func TestHistory_Watch(t *testing.T) {
	engine, _, _ := newHistoryEngine(t)
	luaOK(t, engine, `
		local s = VirtualSwitch:new("s")
		registerObject("virt.s", s)
		-- counters live in a table: gopher-lua detaches locals from closures
		-- after an error caught by pcall in a still-running function
		local c = { seen = {}, ons = 0, all = 0 }
		local stop = StateBus:watch("virt.s", function(e) table.insert(c.seen, e.old .. ">" .. e.value) end)
		StateBus:watch("virt.s", function() c.ons = c.ons + 1 end, { to = 1 })
		StateBus:watch("virt.s", function() error("broken watcher") end)
		StateBus:watch("*", function() c.all = c.all + 1 end)
		StateBus:watch("virt.later", function(e) c.later = e.value end)

		_:sync("virt.s", 1)
		_:sync("virt.s", 0)
		stop()
		_:sync("virt.s", 1)
		assert(table.concat(c.seen, ",") == "0>1,1>0", "seen " .. table.concat(c.seen, ","))
		assert(c.ons == 2, "to filter: " .. c.ons)
		assert(c.all == 3, "* watcher: " .. c.all)

		local v = VirtualSwitch:new("later")
		registerObject("virt.later", v)
		_:sync("virt.later", 1)
		assert(c.later == 1, "watch must work for objects created later")

		local ok, err = pcall(StateBus.watch, StateBus, "virt.s", function() end, { colour = "x" })
		assert(not ok and tostring(err):find("unknown option"), "unknown options must be rejected")
		ok, err = pcall(StateBus.watch, StateBus, "virt.s", function() end, { runAs = 5 })
		assert(not ok and tostring(err):find("runAs"), "runAs must be a string")
	`)
}

func TestHistory_WatchByNameAndRegistryPath(t *testing.T) {
	engine, _, _ := newHistoryEngine(t)
	luaOK(t, engine, `
		local sw = RemoteSwitch:new({ executeNoReply = function() end }, "DOU9", "LAMPA")
		registerObject("CLU1.DOU9", sw)
		expose(sw, "switch", { path = "salon.lampa" })
		local byName, byReg, byExpose = 0, 0, 0
		StateBus:watch("LAMPA", function() byName = byName + 1 end)
		StateBus:watch("CLU1.DOU9", function() byReg = byReg + 1 end)
		StateBus:watch("salon.lampa", function() byExpose = byExpose + 1 end)
		_:sync("CLU1.DOU9", 0) -- first reading: no change
		_:sync("CLU1.DOU9", 1)
		_:sync("CLU1.DOU9", 0)
		assert(byName == 2 and byReg == 2 and byExpose == 2,
			string.format("name=%d reg=%d expose=%d", byName, byReg, byExpose))
		assert(sw:lastChange().value == 0, "history under the expose path")
	`)
}

func TestHistory_WithoutStore(t *testing.T) {
	engine := NewLuaEngine()
	defer engine.Close()
	luaOK(t, engine, `
		local s = VirtualSwitch:new("s")
		registerObject("virt.s", s)
		local got
		StateBus:watch("virt.s", function(e) got = e end)
		_:sync("virt.s", 1)
		assert(got and got.id ~= nil and got.atMs > 0, "events still flow without a store")
		assert(s:lastChange() == nil and s:since() == nil and #s:history() == 0)
	`)
}

// History survives a Lua reload: the store lives in Go.
func TestHistory_SurvivesReinitialize(t *testing.T) {
	engine, store, _ := newHistoryEngine(t)
	luaOK(t, engine, `
		local s = VirtualSwitch:new("s")
		registerObject("virt.s", s)
		_:sync("virt.s", 1)
	`)
	if err := engine.Reinitialize("CLU"); err != nil {
		t.Fatal(err)
	}
	luaOK(t, engine, `
		local s = VirtualSwitch:new("s")
		registerObject("virt.s", s)
		local last = s:lastChange()
		assert(last and last.value == 1, "history lost on reload")
	`)
	if st := store.Status(); st.MemoryEntries != 1 {
		t.Errorf("entries %d", st.MemoryEntries)
	}
}

func TestHistory_WatchByNameCreatedLaterAndAfterOff(t *testing.T) {
	engine, _, _ := newHistoryEngine(t)
	luaOK(t, engine, `
		local c = { n = 0, m = 0 }
		StateBus:watch("LATE_LAMP", function() c.n = c.n + 1 end)
		local sw = RemoteSwitch:new({ executeNoReply = function() end }, "DOU5", "LATE_LAMP")
		registerObject("CLU1.DOU5", sw)
		_:sync("CLU1.DOU5", 0) -- first reading: no change
		_:sync("CLU1.DOU5", 1)
		_:sync("CLU1.DOU5", 0)
		assert(c.n == 2, "watch by name of a later object: " .. c.n)

		StateBus:getShared():off("state_changed")
		StateBus:watch("CLU1.DOU5", function() c.m = c.m + 1 end)
		_:sync("CLU1.DOU5", 1)
		assert(c.m == 1, "dispatcher not re-attached after off: " .. c.m)
	`)
}

// A reload re-creates CLU objects without a value; their first report is no
// change for StateBus:watch unless it differs from the value known before.
func TestHistory_WatchSkipsFirstReadingAfterReload(t *testing.T) {
	engine, _, _ := newHistoryEngine(t)
	setup := `
		local c = { fired = 0, old = "none" }
		_G.c = c
		local sw = RemoteSwitch:new({ executeNoReply = function() end }, "DIN1", "WEJSCIE")
		registerObject("CLU1.DIN1", sw)
		StateBus:watch("CLU1.DIN1", function(e) c.fired = c.fired + 1; c.old = e.old end, { to = 1 })
	`
	luaOK(t, engine, setup+`
		_:sync("CLU1.DIN1", 1)
		assert(c.fired == 0, "first reading ever fired the watch")
		_:sync("CLU1.DIN1", 0)
		_:sync("CLU1.DIN1", 1)
		assert(c.fired == 1, "a real change: " .. c.fired)
	`)
	if err := engine.Reinitialize("CLU"); err != nil {
		t.Fatal(err)
	}
	luaOK(t, engine, setup+`
		_:sync("CLU1.DIN1", 1)
		assert(c.fired == 0, "the same value after a reload fired the watch")
		_:sync("CLU1.DIN1", 0)
	`)
	if err := engine.Reinitialize("CLU"); err != nil {
		t.Fatal(err)
	}
	luaOK(t, engine, setup+`
		_:sync("CLU1.DIN1", 1)
		assert(c.fired == 1 and c.old == 0, "a change during the reload: " .. c.fired .. " old " .. tostring(c.old))
	`)
}
