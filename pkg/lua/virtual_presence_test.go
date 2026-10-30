package lua

import (
	"strings"
	"testing"
	"time"

	"github.com/Domktorymysli/vclu/pkg/history"
)

// VirtualPresence: one presence per room from triggers, keep sources and a door, on a
// virtual clock. advance() moves the clock in seconds and fires the timers that are due.

const vpTimers = `
	VT = { list = {}, id = 0 }
	-- the core timer primitives on the virtual clock, as the MCP sandbox replaces them
	__go_setTimeout = function(fn, ms) VT.id = VT.id + 1; VT.list[VT.id] = { at = __go_time_ms() + ms, fn = fn }; return VT.id end
	__go_setInterval = function(fn, ms) VT.id = VT.id + 1; VT.list[VT.id] = { at = __go_time_ms() + ms, fn = fn, every = ms }; return VT.id end
	__go_clearTimer = function(id) VT.list[id] = nil end
	function fire()
		while true do
			local due = nil
			for id, tm in pairs(VT.list) do
				if tm.at <= __go_time_ms() and (due == nil or tm.at < VT.list[due].at or (tm.at == VT.list[due].at and id < due)) then due = id end
			end
			if due == nil then return end
			local tm = VT.list[due]
			if tm.every then tm.at = tm.at + tm.every else VT.list[due] = nil end
			tm.fn()
		end
	end
`

const vpHarness = vpTimers + `
	EV = { seq = {} }
	function ev() local s = table.concat(EV.seq, ""); EV.seq = {}; return s end
	function watchP(p)
		p:onSwitchOn(function() table.insert(EV.seq, "+") end)
		p:onSwitchOff(function() table.insert(EV.seq, "-") end)
		return p
	end
	-- value/reason/remaining/duration
	function st(p)
		return p:getValue() .. "/" .. tostring(p:getReason()) .. "/" .. p:getRemaining() .. "/" .. p:getDuration()
	end
	PIR = VirtualSensor:new("pir"); registerObject("virt.pir", PIR)
	TV = VirtualSensor:new("tv"); registerObject("virt.tv", TV)
	DOOR = VirtualSensor:new("door", { initial = 1 }); registerObject("virt.door", DOOR)
`

type vpEngine struct {
	*LuaEngine
	clk *stepClock
}

func newVPEngine(t *testing.T) *vpEngine {
	t.Helper()
	engine := NewLuaEngine()
	t.Cleanup(engine.Close)
	te := &vpEngine{engine, &stepClock{t: time.Date(2026, 10, 29, 20, 0, 0, 0, time.UTC)}}
	te.harness(t)
	return te
}

func (te *vpEngine) harness(t *testing.T) {
	t.Helper()
	SetClock(te.state, fixedClockFunc(te.clk.now))
	if _, err := te.Execute(vpHarness); err != nil {
		t.Fatalf("harness: %v", err)
	}
}

// advance moves the clock second by second, firing due timers.
func (te *vpEngine) advance(t *testing.T, sec int) {
	t.Helper()
	for i := 0; i < sec; i++ {
		te.clk.t = te.clk.t.Add(time.Second)
		mustExec(t, te.LuaEngine, `fire()`)
	}
}

func (te *vpEngine) expect(t *testing.T, what, script, want string) {
	t.Helper()
	if got := mustExec(t, te.LuaEngine, script); got != want {
		t.Errorf("%s: got %q, want %q", what, got, want)
	}
}

// A trigger starts presence; it goes off hold s after the last trigger ends; a trigger during
// the hold extends it without a second OnSwitchOn.
func TestVirtualPresence_TriggerAndHold(t *testing.T) {
	te := newVPEngine(t)
	mustExec(t, te.LuaEngine, `P = watchP(VirtualPresence:new("lazienka", { trigger = { "virt.pir" }, hold = 120 }))`)
	te.expect(t, "new", `return st(P) .. "|" .. P:getHold() .. "|" .. ev()`, "0/nil/0/0|120|")

	mustExec(t, te.LuaEngine, `PIR:updateState(1)`)
	te.expect(t, "trigger on", `return st(P) .. "|" .. tostring(P:isActive()) .. "|" .. ev()`, "1/trigger: virt.pir/0/0|true|+")
	te.advance(t, 30)
	mustExec(t, te.LuaEngine, `PIR:updateState(0)`)
	te.expect(t, "trigger off", `return st(P) .. "|" .. ev()`, "1/hold 120 s/120/30|")
	te.advance(t, 60)
	te.expect(t, "in hold", `return st(P)`, "1/hold 60 s/60/90")

	// a new trigger in the hold: no second OnSwitchOn, a full hold after it ends
	mustExec(t, te.LuaEngine, `PIR:updateState(1)`)
	te.expect(t, "again", `return st(P) .. "|" .. ev()`, "1/trigger: virt.pir/0/90|")
	te.advance(t, 10)
	mustExec(t, te.LuaEngine, `PIR:updateState(0)`)
	te.advance(t, 119)
	te.expect(t, "before the end", `return st(P) .. "|" .. ev()`, "1/hold 1 s/1/219|")
	te.advance(t, 1)
	te.expect(t, "after the hold", `return st(P) .. "|" .. tostring(P:isActive()) .. "|" .. ev()`, "0/nil/0/0|false|-")
	te.advance(t, 600)
	te.expect(t, "stays empty", `return st(P) .. "|" .. ev()`, "0/nil/0/0|")
}

// A keep source alone does not start presence; after a start it keeps it with no trigger.
func TestVirtualPresence_Keep(t *testing.T) {
	te := newVPEngine(t)
	mustExec(t, te.LuaEngine, `P = watchP(VirtualPresence:new("salon", { trigger = PIR, keep = { TV }, hold = 60 }))`)
	mustExec(t, te.LuaEngine, `TV:updateState(1)`)
	te.advance(t, 30)
	te.expect(t, "keep alone", `return st(P) .. "|" .. ev()`, "0/nil/0/0|")

	mustExec(t, te.LuaEngine, `PIR:updateState(1); PIR:updateState(0)`)
	te.expect(t, "started, held by the TV", `return st(P) .. "|" .. ev()`, "1/keep: virt.tv/0/0|+")
	te.advance(t, 3600)
	te.expect(t, "an hour still", `return st(P) .. "|" .. ev()`, "1/keep: virt.tv/0/3600|")
	mustExec(t, te.LuaEngine, `TV:updateState(0)`)
	te.expect(t, "TV off", `return st(P)`, "1/hold 60 s/60/3600")
	te.advance(t, 60)
	te.expect(t, "empty", `return st(P) .. "|" .. ev()`, "0/nil/0/0|-")

	// once empty, the TV switched on again does not start it
	mustExec(t, te.LuaEngine, `TV:updateState(0); TV:updateState(1)`)
	te.expect(t, "keep after the end", `return st(P) .. "|" .. ev()`, "0/nil/0/0|")
}

// Wasp in a box: a closed door keeps presence only after a trigger went on while it was
// closed (someone moved inside; one already on as it closed is someone leaving), until it opens, then the hold runs. Leaving and closing the
// door behind you lets the hold run out; a door closed on an empty room does not start it.
func TestVirtualPresence_Door(t *testing.T) {
	te := newVPEngine(t)
	mustExec(t, te.LuaEngine, `P = watchP(VirtualPresence:new("lazienka", { trigger = { PIR }, door = "virt.door", hold = 120 }))`)
	mustExec(t, te.LuaEngine, `DOOR:updateState(0)`)
	te.advance(t, 30)
	te.expect(t, "closed on an empty room", `return st(P) .. "|" .. ev()`, "0/nil/0/0|")
	mustExec(t, te.LuaEngine, `DOOR:updateState(1)`)

	// in, door closed, motion inside, then sitting still
	mustExec(t, te.LuaEngine, `PIR:updateState(1); PIR:updateState(0)`)
	mustExec(t, te.LuaEngine, `DOOR:updateState(0)`)
	te.expect(t, "closed, nobody moved yet", `return st(P) .. "|" .. ev()`, "1/hold 120 s/120/0|+")
	te.advance(t, 10)
	mustExec(t, te.LuaEngine, `PIR:updateState(1); PIR:updateState(0)`)
	te.expect(t, "motion inside: locked", `return st(P)`, "1/door/0/10")
	te.advance(t, 3600)
	te.expect(t, "an hour still", `return st(P) .. "|" .. ev()`, "1/door/0/3610|")
	mustExec(t, te.LuaEngine, `DOOR:updateState(1)`)
	te.expect(t, "door opens", `return st(P)`, "1/hold 120 s/120/3610")
	te.advance(t, 120)
	te.expect(t, "empty", `return st(P) .. "|" .. ev()`, "0/nil/0/0|-")

	// motion, out, the door closed behind: the hold runs out as without a door
	mustExec(t, te.LuaEngine, `PIR:updateState(1); PIR:updateState(0)`)
	te.advance(t, 5)
	mustExec(t, te.LuaEngine, `DOOR:updateState(0)`)
	te.expect(t, "left and closed", `return st(P) .. "|" .. ev()`, "1/hold 115 s/115/5|+")
	te.advance(t, 115)
	te.expect(t, "off after the hold", `return st(P) .. "|" .. ev()`, "0/nil/0/0|-")
	te.advance(t, 600)
	te.expect(t, "stays empty behind the closed door", `return st(P) .. "|" .. ev()`, "0/nil/0/0|")

	// still moving as the door closes (going out), no new motion after: not locked
	mustExec(t, te.LuaEngine, `DOOR:updateState(1); PIR:updateState(1)`)
	te.expect(t, "motion at the door", `return st(P) .. "|" .. ev()`, "1/trigger: virt.pir/0/0|+")
	mustExec(t, te.LuaEngine, `DOOR:updateState(0)`)
	te.advance(t, 3)
	mustExec(t, te.LuaEngine, `PIR:updateState(0)`)
	te.expect(t, "closed while moving", `return st(P)`, "1/hold 120 s/120/3")
	te.advance(t, 120)
	te.expect(t, "off after the hold", `return st(P) .. "|" .. ev()`, "0/nil/0/0|-")
	mustExec(t, te.LuaEngine, `DOOR:updateState(1); DOOR:updateState(0)`)

	// the lock ends with the door opening: closed again with nobody moving, the hold runs
	mustExec(t, te.LuaEngine, `PIR:updateState(1)`)
	te.expect(t, "motion behind the closed door", `return st(P) .. "|" .. ev()`, "1/trigger: virt.pir/0/0|+")
	mustExec(t, te.LuaEngine, `PIR:updateState(0); DOOR:updateState(1); DOOR:updateState(0)`)
	te.expect(t, "opened and closed again", `return st(P)`, "1/hold 120 s/120/0")
	te.advance(t, 120)
	te.expect(t, "off", `return st(P) .. "|" .. ev()`, "0/nil/0/0|-")
}

// Walking in with the detector still on and closing the door: still on doorGrace s after the
// door closed means someone inside, so the door holds it; off within the grace is leaving.
func TestVirtualPresence_DoorGrace(t *testing.T) {
	te := newVPEngine(t)
	mustExec(t, te.LuaEngine, `P = watchP(VirtualPresence:new("lazienka", { trigger = { PIR }, door = DOOR, hold = 120 }))`)
	// enter: on as the door closes, still on at 12 s, then sitting still
	mustExec(t, te.LuaEngine, `PIR:updateState(1); DOOR:updateState(0)`)
	te.advance(t, 12)
	mustExec(t, te.LuaEngine, `PIR:updateState(0)`)
	te.expect(t, "entered", `return st(P) .. "|" .. ev()`, "1/door/0/12|+")
	te.advance(t, 3600)
	te.expect(t, "an hour", `return st(P)`, "1/door/0/3612")
	mustExec(t, te.LuaEngine, `DOOR:updateState(1)`)
	te.advance(t, 120)
	te.expect(t, "door opened", `return st(P) .. "|" .. ev()`, "0/nil/0/0|-")

	// leave: on as the door closes, off after 3 s
	mustExec(t, te.LuaEngine, `PIR:updateState(1); DOOR:updateState(0)`)
	te.advance(t, 3)
	mustExec(t, te.LuaEngine, `PIR:updateState(0)`)
	te.expect(t, "leaving", `return st(P)`, "1/hold 120 s/120/3")
	te.advance(t, 120)
	te.expect(t, "left", `return st(P) .. "|" .. ev()`, "0/nil/0/0|+-")

	// a source without onChange, seen by the tick: its going on behind the closed door locks
	mustExec(t, te.LuaEngine, `
		DOOR:updateState(1)
		SRC = { v = 0, getValue = function(self) return self.v end }
		P2 = VirtualPresence:new("wc", { trigger = { SRC }, door = DOOR, hold = 60 })
		DOOR:updateState(0)`)
	te.advance(t, 20)
	mustExec(t, te.LuaEngine, `SRC.v = 1`)
	te.advance(t, 10)
	mustExec(t, te.LuaEngine, `SRC.v = 0`)
	te.advance(t, 10)
	te.expect(t, "tick-only trigger", `return st(P2)`, "1/door/0/10")
}

// The door lock ends when the door opens also under an override; setOverride(nil) with no
// override set changes nothing.
func TestVirtualPresence_OverrideAndDoor(t *testing.T) {
	te := newVPEngine(t)
	mustExec(t, te.LuaEngine, `P = watchP(VirtualPresence:new("lazienka", { trigger = { PIR }, door = DOOR, hold = 120 }))`)
	mustExec(t, te.LuaEngine, `DOOR:updateState(0); PIR:updateState(1); PIR:updateState(0)`)
	te.expect(t, "locked", `return st(P) .. "|" .. ev()`, "1/door/0/0|+")
	mustExec(t, te.LuaEngine, `P:setOverride(1); DOOR:updateState(1); DOOR:updateState(0); P:setOverride(nil)`)
	te.expect(t, "lock gone", `return st(P)`, "1/hold 120 s/120/0")
	te.advance(t, 120)
	te.expect(t, "empty", `return st(P) .. "|" .. ev()`, "0/nil/0/0|-")

	mustExec(t, te.LuaEngine, `P:setHold(60); DOOR:updateState(1); PIR:updateState(1); PIR:updateState(0)`)
	te.advance(t, 50)
	mustExec(t, te.LuaEngine, `P:setOverride(nil)`)
	te.expect(t, "nil again", `return st(P)`, "1/hold 10 s/10/50")
	te.advance(t, 10)
	te.expect(t, "empty after 60 s", `return st(P) .. "|" .. ev()`, "0/nil/0/0|+-")
}

// setOverride(1) forces presence, (0) forces empty, nil returns to the sensors; each with
// its events.
func TestVirtualPresence_Override(t *testing.T) {
	te := newVPEngine(t)
	mustExec(t, te.LuaEngine, `P = watchP(VirtualPresence:new("salon", { trigger = { PIR }, hold = 60 }))`)
	mustExec(t, te.LuaEngine, `P:setOverride(1)`)
	te.expect(t, "override 1", `return st(P) .. "|" .. ev()`, "1/override/0/0|+")
	mustExec(t, te.LuaEngine, `PIR:updateState(1); PIR:updateState(0)`)
	te.advance(t, 600)
	te.expect(t, "sensors ignored", `return st(P) .. "|" .. ev()`, "1/override/0/600|")

	mustExec(t, te.LuaEngine, `P:setOverride(0)`)
	te.expect(t, "override 0", `return st(P) .. "|" .. ev()`, "0/nil/0/0|-")
	mustExec(t, te.LuaEngine, `PIR:updateState(1); P:trigger()`)
	te.expect(t, "a trigger while forced empty", `return st(P) .. "|" .. ev()`, "0/nil/0/0|")

	// back to the sensors: the PIR is active, so presence starts
	mustExec(t, te.LuaEngine, `P:setOverride(nil)`)
	te.expect(t, "override nil", `return st(P) .. "|" .. ev()`, "1/trigger: virt.pir/0/0|+")
	mustExec(t, te.LuaEngine, `PIR:updateState(0)`)
	te.advance(t, 10)
	mustExec(t, te.LuaEngine, `P:setOverride(1)`)
	te.expect(t, "override 1 when on", `return st(P) .. "|" .. ev()`, "1/override/0/10|")
	// back with nothing holding it: the hold runs, then it goes off
	mustExec(t, te.LuaEngine, `P:setOverride(nil)`)
	te.expect(t, "nil after 1", `return st(P) .. "|" .. ev()`, "1/hold 60 s/60/10|")
	te.advance(t, 60)
	te.expect(t, "empty", `return st(P) .. "|" .. ev()`, "0/nil/0/0|-")

	if _, err := te.Execute(`P:setOverride(2)`); err == nil || !strings.Contains(err.Error(), "1, 0 or nil") {
		t.Errorf("setOverride(2) error = %v", err)
	}
}

// trigger(sec) from code starts or extends; setHold changes the next hold.
func TestVirtualPresence_TriggerFromCodeAndHold(t *testing.T) {
	te := newVPEngine(t)
	mustExec(t, te.LuaEngine, `P = watchP(VirtualPresence:new("hall", { trigger = { PIR }, hold = 120 }))`)
	mustExec(t, te.LuaEngine, `P:trigger(30)`)
	te.expect(t, "trigger(30)", `return st(P) .. "|" .. ev()`, "1/hold 30 s/30/0|+")
	te.advance(t, 10)
	mustExec(t, te.LuaEngine, `P:trigger(5)`) // shorter: no change
	te.expect(t, "trigger(5)", `return st(P)`, "1/hold 20 s/20/10")
	mustExec(t, te.LuaEngine, `P:trigger()`) // the hold
	te.expect(t, "trigger()", `return st(P) .. "|" .. ev()`, "1/hold 120 s/120/10|")
	te.advance(t, 120)
	te.expect(t, "empty", `return st(P) .. "|" .. ev()`, "0/nil/0/0|-")

	mustExec(t, te.LuaEngine, `P:setHold(15)`)
	mustExec(t, te.LuaEngine, `PIR:updateState(1); PIR:updateState(0)`)
	te.expect(t, "setHold", `return P:getHold() .. "|" .. st(P) .. "|" .. ev()`, "15|1/hold 15 s/15/0|+")
	te.advance(t, 15)
	te.expect(t, "short hold", `return st(P) .. "|" .. ev()`, "0/nil/0/0|-")

	// hold 0: off as the trigger ends; a failing listener does not stop it
	mustExec(t, te.LuaEngine, `P:setHold(0); P:onSwitchOn(function() error("boom") end)`)
	mustExec(t, te.LuaEngine, `PIR:updateState(1)`)
	te.expect(t, "listener failed", `return st(P) .. "|" .. ev()`, "1/trigger: virt.pir/0/0|+")
	mustExec(t, te.LuaEngine, `PIR:updateState(0)`)
	te.expect(t, "hold 0", `return st(P) .. "|" .. ev()`, "0/nil/0/0|-")

	for _, bad := range []string{`P:setHold(-1)`, `P:setHold("x")`, `P:trigger(-5)`} {
		if _, err := te.Execute(bad); err == nil || !strings.Contains(err.Error(), "seconds >= 0") {
			t.Errorf("%s: error = %v", bad, err)
		}
	}
}

// It starts empty after a reload, also with a trigger that is still active; a trigger counts
// once it changes, and a first report that fires no OnChange is taken by the tick.
func TestVirtualPresence_StartsEmpty(t *testing.T) {
	te := newVPEngine(t)
	mustExec(t, te.LuaEngine, `
		P = VirtualPresence:new("salon", { trigger = { PIR } })
		PIR:updateState(1)
		assert(P:getValue() == 1)`)
	if err := te.Reinitialize("CLU"); err != nil {
		t.Fatal(err)
	}
	te.harness(t)
	mustExec(t, te.LuaEngine, `
		KEPT = VirtualSensor:new("kept", { initial = 1 })
		P = watchP(VirtualPresence:new("salon", { trigger = { KEPT, PIR } }))`)
	te.advance(t, 30)
	te.expect(t, "after a reload", `return st(P) .. "|" .. ev()`, "0/nil/0/0|")
	mustExec(t, te.LuaEngine, `KEPT:updateState(0); KEPT:updateState(1)`)
	te.expect(t, "after a change", `return st(P) .. "|" .. ev()`, "1/trigger: kept/0/0|+")

	// a source that reports its unchanged value again does not arm the trigger
	mustExec(t, te.LuaEngine, `
		SRC = { v = 1, cbs = {} }
		function SRC:getValue() return self.v end
		function SRC:onChange(cb) table.insert(self.cbs, cb) end
		function SRC:report(v) self.v = v; for i, cb in ipairs(self.cbs) do cb(v) end end
		P3 = VirtualPresence:new("kuchnia", { trigger = { SRC } })
		SRC:report(1)`)
	te.expect(t, "same value again", `return P3:getValue()`, "0")
	mustExec(t, te.LuaEngine, `SRC:report(0); SRC:report(1)`)
	te.expect(t, "after a change", `return P3:getValue()`, "1")

	// a Grenton input: its first report fires no OnChange
	mustExec(t, te.LuaEngine, `
		local clu = { executeNoReply = function() end }
		DIN = RemoteSensor:new(clu, "DIN1", "pir_hall")
		P2 = VirtualPresence:new("hall", { trigger = { DIN } })
		DIN:updateState(1)`)
	te.expect(t, "first report", `return P2:getValue()`, "0")
	te.advance(t, 10)
	te.expect(t, "after the tick", `return P2:getValue()`, "1")
}

// A failing onChange or event handler skips none of the others nor the publish.
func TestVirtualPresence_FailingHandlerFirst(t *testing.T) {
	te := newVPEngine(t)
	te.SetHistoryStore(history.New(history.DefaultConfig(), te.clk.now))
	mustExec(t, te.LuaEngine, `
		LIGHT = { n = 0, on = false }
		P = VirtualPresence:new("salon", { trigger = { PIR }, hold = 0 })
		registerObject("room.salon", P)
		P:onChange(function(v) if v == 0 then error("onChange") end end)
		P:onSwitchOn(function() error("first on") end)
		P:onSwitchOn(function() LIGHT.on = true; LIGHT.n = LIGHT.n + 1 end)
		P:onSwitchOff(function() error("first off") end)
		P:onSwitchOff(function() LIGHT.on = false; LIGHT.n = LIGHT.n + 1 end)`)
	mustExec(t, te.LuaEngine, `PIR:updateState(1)`)
	te.expect(t, "on", `return P:getValue() .. "|" .. tostring(LIGHT.on) .. "|" .. P:lastChange().value`, "1|true|1")
	mustExec(t, te.LuaEngine, `PIR:updateState(0)`)
	te.expect(t, "off", `return P:getValue() .. "|" .. tostring(LIGHT.on) .. "|" .. LIGHT.n .. "|" .. P:lastChange().value`, "0|false|2|0")
}

// Unknown options, sources that do not resolve and bad values are errors at new().
func TestVirtualPresence_NewErrors(t *testing.T) {
	te := newVPEngine(t)
	cases := map[string]string{
		`VirtualPresence:new("a", { trigger = { PIR }, hodl = 60 })`:      "unknown option 'hodl'",
		`VirtualPresence:new("a", { trigger = { "virt.nope" } })`:         "trigger 'virt.nope' not found",
		`VirtualPresence:new("a", { keep = { PIR, "virt.gone" } })`:       "keep 'virt.gone' not found",
		`VirtualPresence:new("a", { door = "virt.brak" })`:                "door 'virt.brak' not found",
		`VirtualPresence:new("a", { trigger = { {} } })`:                  "trigger must be an object with getValue()",
		`VirtualPresence:new("a", { trigger = 5 })`:                       "trigger must be a list",
		`VirtualPresence:new("a", { trigger = { PIR }, hold = -1 })`:      "hold must be seconds >= 0",
		`VirtualPresence:new("a", { trigger = { PIR }, hold = "x" })`:     "hold must be seconds >= 0",
		`VirtualPresence:new("a", { trigger = { PIR }, doorGrace = -1 })`: "doorGrace must be seconds >= 0",
		`VirtualPresence:new("a", "x")`:                                   "opts must be a table",
	}
	for script, want := range cases {
		_, err := te.Execute(script)
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: error = %v, want %q", script, err, want)
		}
	}
	// a single object or path works as a list of one
	te.expect(t, "single", `return VirtualPresence:new("b", { trigger = "virt.pir", keep = TV, door = DOOR }):getValue()`, "0")
}

// Works with a SatelZone as a trigger, through the Satel event dispatch.
func TestVirtualPresence_SatelZone(t *testing.T) {
	engine := newSatelTestEngine(t)
	clk := &stepClock{t: time.Date(2026, 10, 29, 20, 0, 0, 0, time.UTC)}
	SetClock(engine.state, fixedClockFunc(clk.now))
	mustExec(t, engine, vpTimers)
	mustExec(t, engine, `P = VirtualPresence:new("salon", { trigger = { "satel.zone.1" }, hold = 60 })`)
	mustExec(t, engine, `Satel._dispatch({ type = "zone", number = 1, zone = { number = 1, violated = true } })`)
	if got := mustExec(t, engine, `return P:getValue() .. "/" .. tostring(P:getReason())`); got != "1/trigger: satel.zone.1" {
		t.Errorf("zone violated: %s", got)
	}
	mustExec(t, engine, `Satel._dispatch({ type = "zone", number = 1, zone = { number = 1, violated = false } })`)
	if got := mustExec(t, engine, `return P:getValue() .. "/" .. tostring(P:getReason())`); got != "1/hold 60 s" {
		t.Errorf("zone clear: %s", got)
	}
	clk.t = clk.t.Add(60 * time.Second)
	mustExec(t, engine, `fire()`)
	if got := mustExec(t, engine, `return P:getValue()`); got != "0" {
		t.Errorf("after the hold: %s", got)
	}
}

// expose(p, "occupancy"): a Home Assistant binary_sensor with device_class occupancy, ON/OFF.
func TestVirtualPresence_ExposeOccupancyHA(t *testing.T) {
	te := newVPEngine(t)
	got := mustExec(t, te.LuaEngine, `(function()
		_pub = {}
		HAStatePublisher.setup({
			publish = function(self, topic, payload) table.insert(_pub, { topic = topic, payload = payload }); return true end,
			subscribe = function() end, unsubscribe = function() end,
		})
		local function last(topic)
			local v
			for i, p in ipairs(_pub) do if p.topic == topic then v = p.payload end end
			return tostring(v)
		end
		P = VirtualPresence:new("lazienka", { trigger = { PIR } })
		local h = expose(P, "occupancy", { name = "Łazienka", path = "room:lazienka", area = "Łazienka" })
		local config
		for i, p in ipairs(_pub) do
			if p.topic:find("room_lazienka", 1, true) and p.topic:find("/config", 1, true) then config = p end
		end
		local state = "vclu/exposed/room/binary_sensor/lazienka/state"
		local a = last(state)
		PIR:updateState(1)
		return h.type .. "|" .. config.topic .. "|" .. config.payload .. "|" .. a .. "/" .. last(state)
	end)()`)
	parts := strings.SplitN(got, "|", 4)
	if len(parts) != 4 {
		t.Fatalf("unexpected %q", got)
	}
	if parts[0] != "occupancy" || !strings.Contains(parts[1], "/binary_sensor/") {
		t.Errorf("type/topic = %s %s", parts[0], parts[1])
	}
	for _, want := range []string{`"device_class":"occupancy"`, `"state_topic":"vclu/exposed/room/binary_sensor/lazienka/state"`} {
		if !strings.Contains(parts[2], want) {
			t.Errorf("discovery missing %s: %s", want, parts[2])
		}
	}
	if strings.Contains(parts[2], "command_topic") {
		t.Errorf("occupancy takes no commands: %s", parts[2])
	}
	if parts[3] != "OFF/ON" {
		t.Errorf("state before/after = %s, want OFF/ON", parts[3])
	}
}

// The class holds its public API only.
func TestVirtualPresence_OnlyPublicAPI(t *testing.T) {
	engine := NewLuaEngine()
	t.Cleanup(engine.Close)
	got := mustExec(t, engine, `(function()
		local public = { __index = true, new = true, set = true, getReason = true, getRemaining = true,
			getDuration = true, trigger = true, setOverride = true, setHold = true, getHold = true }
		local extra = {}
		for k in pairs(VirtualPresence) do
			if not public[k] then table.insert(extra, tostring(k)) end
		end
		table.sort(extra)
		return table.concat(extra, ",") .. "|" .. tostring(getmetatable(VirtualPresence).__index == VirtualSensor)
	end)()`)
	if got != "|true" {
		t.Errorf("VirtualPresence class: %s", got)
	}
}
