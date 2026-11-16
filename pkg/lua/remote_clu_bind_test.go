package lua

import (
	"strings"
	"testing"
	"time"

	lua "github.com/yuin/gopher-lua"
)

// Async commands wait in QUEUE until deliver() sends them to the fake CLU and answers, as Go
// does off the run loop; BLOCKING counts the commands sent with the blocking sender. The CLU
// answers CLU221000290:get(0) with its uptime (UPTIME there) and runs OM scripts HaZmiana and
// Inna, which count their runs and note the feature they see (SEEN).
const bindHarness = `
QUEUE = {}
BLOCKING = { n = 0 }
NOW = { t = 1000000 }
os.time = function() return NOW.t end
local send = __go_send_command
__go_send_command = function(ip, script, delay)
	BLOCKING.n = BLOCKING.n + 1
	return send(ip, script, delay)
end
__go_send_command_async = function(ip, script, id)
	QUEUE[#QUEUE + 1] = { ip = ip, script = script, id = id }
	return true
end
function deliver()
	while #QUEUE > 0 do
		local q = table.remove(QUEUE, 1)
		local result, err = send(q.ip, q.script)
		__vclu_command_reply(q.id, result or "", err)
	end
end
-- a plugin-like source: reports a value, also the same one again, also nil
SRC = { cbs = {} }
function SRC:onChange(cb)
	self.cbs[#self.cbs + 1] = cb
	return function()
		for i, c in ipairs(self.cbs) do
			if c == cb then table.remove(self.cbs, i) return end
		end
	end
end
function SRC:report(v)
	for _, cb in ipairs(self.cbs) do cb(v) end
end
`

const bindCLU = `
UPTIME = 1000
RUNS = { HaZmiana = 0, Inna = 0 }
SEEN = {}
CLU221000290 = { get = function(self, i) if i == 0 then return UPTIME end end }
function HaZmiana() RUNS.HaZmiana = RUNS.HaZmiana + 1; SEEN[#SEEN + 1] = tostring(getVar("ha_pralka")) end
function Inna() RUNS.Inna = RUNS.Inna + 1 end
`

func newBindEngine(t *testing.T) (*LuaEngine, *fakeCLU) {
	t.Helper()
	engine, clu := newFeaturesEngine(t)
	if err := clu.L.DoString(bindCLU); err != nil {
		t.Fatal(err)
	}
	mustExec(t, engine, bindHarness)
	return engine, clu
}

// writes returns the scripts sent to the CLU that write a feature.
func (c *fakeCLU) writes() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []string
	for _, s := range c.sent {
		if strings.Contains(s, "setVar(") {
			out = append(out, s)
		}
	}
	return out
}

func (c *fakeCLU) runs(script string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return int(lua.LVAsNumber(c.L.GetField(c.L.GetGlobal("RUNS"), script)))
}

func (c *fakeCLU) exec(t *testing.T, code string) {
	t.Helper()
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.L.DoString(code); err != nil {
		t.Fatal(err)
	}
}

func globalString(c *fakeCLU, name string) string {
	return c.global(name).String()
}

// bind writes the value it has at once, then on every change; a report of the same value
// again writes nothing. Nothing goes through the blocking sender.
func TestCLUBindWritesOnChange(t *testing.T) {
	engine, clu := newBindEngine(t)
	mustExec(t, engine, `
balkon = VirtualNumber:new("balkon", { initial = 12.5 })
CLU221000290:bind("ha_temp_balkon", balkon)
deliver()`)
	if got := globalString(clu, "ha_temp_balkon"); got != "12.5" {
		t.Fatalf("after bind the CLU has %s, want 12.5", got)
	}
	mustExec(t, engine, `balkon:setValue(13) deliver() balkon:setValue(13) deliver()`)
	if got := globalString(clu, "ha_temp_balkon"); got != "13" {
		t.Errorf("after a change the CLU has %s, want 13", got)
	}
	mustExec(t, engine, `
CLU221000290:bind("ha_src", SRC)
SRC:report("on") deliver() SRC:report("on") deliver() SRC:report("on") deliver()`)
	if got := globalString(clu, "ha_src"); got != "on" {
		t.Errorf("ha_src = %s, want on", got)
	}
	if w := clu.writes(); len(w) != 3 {
		t.Errorf("writes = %d, want 3 (bind, change, first report): %q", len(w), w)
	}
	if got := mustExec(t, engine, `return BLOCKING.n`); got != "0" {
		t.Errorf("blocking sends = %s, want 0", got)
	}
}

// A change returns at once while the CLU takes its time to answer; the write arrives later.
func TestCLUBindDoesNotBlock(t *testing.T) {
	clu := newFakeCLU(t)
	engine := NewLuaEngine()
	t.Cleanup(engine.Close)
	slow := func(ip, script string) (string, error) {
		time.Sleep(300 * time.Millisecond)
		return clu.send(ip, script)
	}
	engine.SetCommandSender(slow)
	mustExec(t, engine, `
CLU221000290 = CLU_REMOTE:new(221000290, 0xC0A80003, EventBus:getShared())
v = VirtualNumber:new("v", { initial = 1 })`)
	start := time.Now()
	mustExec(t, engine, `CLU221000290:bind("cecha", v) v:setValue(2)`)
	if d := time.Since(start); d > 150*time.Millisecond {
		t.Errorf("bind and a change took %v, the CLU answers in 300 ms", d)
	}
	deadline := time.Now().Add(5 * time.Second)
	for globalString(clu, "cecha") != "2" {
		if time.Now().After(deadline) {
			t.Fatalf("the CLU has cecha = %s, want 2", globalString(clu, "cecha"))
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// Changes while a write is on its way wait for its answer and go as one write with the
// last value; the CLU never gets two writes at once.
func TestCLUBindCoalesces(t *testing.T) {
	engine, clu := newBindEngine(t)
	got := mustExec(t, engine, `
v = VirtualNumber:new("v", { initial = 1 })
CLU221000290:bind("cecha", v)
local n1 = #QUEUE
v:setValue(2) v:setValue(3) v:setValue(4)
local n2 = #QUEUE
deliver()
return n1 .. "," .. n2`)
	if got != "1,1" {
		t.Errorf("queued writes = %s, want 1,1", got)
	}
	if w := clu.writes(); len(w) != 2 || !strings.Contains(w[1], `setVar("cecha", 4)`) {
		t.Errorf("writes = %q, want 1 then 4", w)
	}
}

// script runs the OM script after the write caused by a change, with the new value already
// in the feature; the write at bind runs no script. Two features with one script run it once.
func TestCLUBindScriptAfterChange(t *testing.T) {
	engine, clu := newBindEngine(t)
	mustExec(t, engine, `
pralka = VirtualText:new("pralka", { initial = "off" })
CLU221000290:bind("ha_pralka", pralka, { script = "HaZmiana" })
deliver()`)
	if n := clu.runs("HaZmiana"); n != 0 {
		t.Errorf("script runs after bind = %d, want 0", n)
	}
	mustExec(t, engine, `pralka:setValue("running") deliver()`)
	if n := clu.runs("HaZmiana"); n != 1 {
		t.Fatalf("script runs after a change = %d, want 1", n)
	}
	if seen := clu.global("SEEN").(*lua.LTable).RawGetInt(1).String(); seen != "running" {
		t.Errorf("the script saw %s, want running", seen)
	}
	mustExec(t, engine, `
a = VirtualNumber:new("a", { initial = 0 })
CLU221000290:bind("ha_a", a, { script = "HaZmiana" })
deliver()
pralka:setValue("done") a:setValue(1)
deliver()`)
	if n := clu.runs("HaZmiana"); n != 3 {
		t.Errorf("script runs = %d, want 3 (one per write with a change)", n)
	}
}

// When the CLU restarts (its uptime goes back) every bound feature is written again and no
// script runs; the same happens when it answers again after not answering.
func TestCLUBindRewritesAfterRestart(t *testing.T) {
	engine, clu := newBindEngine(t)
	mustExec(t, engine, `
pralka = VirtualText:new("pralka", { initial = "running" })
temp = VirtualNumber:new("temp", { initial = 21.5 })
CLU221000290:bind("ha_pralka", pralka, { script = "HaZmiana" })
CLU221000290:bind("ha_temp", temp, { script = "Inna" })
deliver()
CLU_REMOTE._checkBound() deliver()`)
	clu.exec(t, `UPTIME = 1030`)
	mustExec(t, engine, `NOW.t = NOW.t + 30 CLU_REMOTE._checkBound() deliver()`)
	if w := clu.writes(); len(w) != 2 {
		t.Fatalf("writes before the restart = %d, want 2 (one per bind): %q", len(w), w)
	}

	// OM sends the configuration: features back at their OM values, uptime from 0
	clu.exec(t, `ha_pralka = nil ha_temp = 0 UPTIME = 3`)
	mustExec(t, engine, `NOW.t = NOW.t + 30 CLU_REMOTE._checkBound() deliver()`)
	if got := globalString(clu, "ha_pralka") + "," + globalString(clu, "ha_temp"); got != "running,21.5" {
		t.Errorf("after the restart the CLU has %s, want running,21.5", got)
	}
	if n := clu.runs("HaZmiana") + clu.runs("Inna"); n != 0 {
		t.Errorf("scripts run on the write after a restart: %d", n)
	}

	// a check that finds the uptime going on writes nothing
	n := len(clu.writes())
	clu.exec(t, `UPTIME = 33`)
	mustExec(t, engine, `NOW.t = NOW.t + 30 CLU_REMOTE._checkBound() deliver()`)
	if len(clu.writes()) != n {
		t.Errorf("a check without a restart wrote: %q", clu.writes()[n:])
	}

	// no answer, then an answer again: written again, also without a restart seen
	clu.mu.Lock()
	clu.down = true
	clu.mu.Unlock()
	mustExec(t, engine, `NOW.t = NOW.t + 30 CLU_REMOTE._checkBound() deliver()`)
	clu.mu.Lock()
	clu.down = false
	clu.mu.Unlock()
	clu.exec(t, `ha_pralka = nil UPTIME = 93`)
	mustExec(t, engine, `NOW.t = NOW.t + 30 CLU_REMOTE._checkBound() deliver()`)
	if got := globalString(clu, "ha_pralka"); got != "running" {
		t.Errorf("after the CLU answered again it has %s, want running", got)
	}
	if n := clu.runs("HaZmiana") + clu.runs("Inna"); n != 0 {
		t.Errorf("scripts run on a write again: %d", n)
	}
}

// A write the CLU does not answer is not repeated on every change; the last value goes
// once the CLU answers again, with the script of the change that was not written.
func TestCLUBindWaitsForCLU(t *testing.T) {
	engine, clu := newBindEngine(t)
	mustExec(t, engine, `
pralka = VirtualText:new("pralka", { initial = "off" })
CLU221000290:bind("ha_pralka", pralka, { script = "HaZmiana" })
deliver()
CLU_REMOTE._checkBound() deliver()`)
	clu.mu.Lock()
	clu.down = true
	clu.mu.Unlock()
	mustExec(t, engine, `pralka:setValue("a") deliver() pralka:setValue("b") deliver() pralka:setValue("c") deliver()`)
	if w := clu.writes(); len(w) != 2 {
		t.Errorf("writes while the CLU is down = %d, want 2 (bind, first change): %q", len(w), w)
	}
	clu.mu.Lock()
	clu.down = false
	clu.mu.Unlock()
	clu.exec(t, `UPTIME = 1030`)
	mustExec(t, engine, `NOW.t = NOW.t + 30 CLU_REMOTE._checkBound() deliver()`)
	if got := globalString(clu, "ha_pralka"); got != "c" {
		t.Errorf("the CLU has %s, want c", got)
	}
	if n := clu.runs("HaZmiana"); n != 1 {
		t.Errorf("script runs = %d, want 1", n)
	}
}

// nil is not written: the feature keeps its last value.
func TestCLUBindNilNotWritten(t *testing.T) {
	engine, clu := newBindEngine(t)
	mustExec(t, engine, `
CLU221000290:bind("ha_src", SRC)
deliver()
SRC:report(5) deliver()
SRC:report(nil) deliver()`)
	if w := clu.writes(); len(w) != 1 {
		t.Errorf("writes = %q, want only 5", w)
	}
	if got := globalString(clu, "ha_src"); got != "5" {
		t.Errorf("ha_src = %s, want 5", got)
	}
}

// The function bind returns removes the binding: no write on a change or after a restart,
// and the uptime check stops with the last binding.
func TestCLUBindUnbind(t *testing.T) {
	engine, clu := newBindEngine(t)
	mustExec(t, engine, `
v = VirtualNumber:new("v", { initial = 1 })
stop = CLU221000290:bind("cecha", v)
deliver()
CLU_REMOTE._checkBound() deliver()
stop() stop()
v:setValue(2) deliver()`)
	clu.exec(t, `cecha = nil UPTIME = 3`)
	got := mustExec(t, engine, `NOW.t = NOW.t + 30 CLU_REMOTE._checkBound() deliver()
return tostring(CLU_REMOTE._bindTimer) .. "," .. #v._cbs`)
	if got != "nil,0" {
		t.Errorf("timer, callbacks after unbind = %s, want nil,0", got)
	}
	if w := clu.writes(); len(w) != 1 {
		t.Errorf("writes = %q, want only the one at bind", w)
	}

	// binding the feature again replaces the binding, it does not add one
	mustExec(t, engine, `
w = VirtualNumber:new("w", { initial = 7 })
CLU221000290:bind("cecha", v)
CLU221000290:bind("cecha", w)
deliver()
v:setValue(3) deliver()`)
	if got := globalString(clu, "cecha"); got != "7" {
		t.Errorf("cecha = %s, want 7 (the second binding)", got)
	}
}

// Names must be Lua identifiers and the object must have onChange; nothing is sent otherwise.
func TestCLUBindChecksArguments(t *testing.T) {
	engine, clu := newBindEngine(t)
	mustExec(t, engine, `v = VirtualNumber:new("v", { initial = 1 })`)
	for call, want := range map[string]string{
		`CLU221000290:bind('x") os.exit() --', v)`:                     "Lua identifier",
		`CLU221000290:bind("1abc", v)`:                                 "Lua identifier",
		`CLU221000290:bind(17, v)`:                                     "Lua identifier",
		`CLU221000290:bind("ok", v, { script = "HaZmiana()" })`:        "Lua identifier",
		`CLU221000290:bind("ok", v, { script = "a b" })`:               "Lua identifier",
		`CLU221000290:bind("ok", v, { script = 'x") hacked = 1 --' })`: "Lua identifier",
		`CLU221000290:bind("ok", v, { script = 5 })`:                   "Lua identifier",
		`CLU221000290:bind("ok", v, "HaZmiana")`:                       "options must be a table",
		`CLU221000290:bind("ok", 5)`:                                   "onChange",
		`CLU221000290:bind("ok", {})`:                                  "onChange",
	} {
		_, err := engine.Execute(call)
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: error = %v, want %q", call, err, want)
		}
	}
	mustExec(t, engine, `deliver()`)
	if len(clu.sent) != 0 {
		t.Errorf("sent to the CLU: %q", clu.sent)
	}
}

// A string value cannot leave its literal, as with set.
func TestCLUBindEscapes(t *testing.T) {
	engine, clu := newBindEngine(t)
	mustExec(t, engine, `t = VirtualText:new("t", { initial = "" }) CLU221000290:bind("cecha", t) deliver()`)
	for _, v := range []string{`a"b`, `a\`, `a\"); setVar("hacked", 1); x = ("`, "line1\nline2\x00end", `"]] .. hacked = 1 .. [["`} {
		engine.SetGlobal("V", v)
		mustExec(t, engine, `t:setValue(V) deliver()`)
		if s := clu.global("cecha"); s.Type() != lua.LTString || string(s.(lua.LString)) != v {
			t.Errorf("value %q: the CLU has %q, script %s", v, s, clu.last())
		}
	}
	if v := clu.global("hacked"); v != lua.LNil {
		t.Errorf("a value ran code on the CLU: hacked = %v", v)
	}
}

// Many long values go in several scripts, each one fits a CLU packet; a value that never
// fits is left out with a warning.
func TestCLUBindSplitsLongWrites(t *testing.T) {
	engine, clu := newBindEngine(t)
	mustExec(t, engine, `
for i = 1, 8 do
	local o = VirtualText:new("t" .. i, { initial = string.rep(tostring(i), 300) })
	CLU221000290:bind("cecha" .. i, o)
end
deliver()
huge = VirtualText:new("huge", { initial = string.rep("x", 2000) })
CLU221000290:bind("duza", huge)
deliver()`)
	for i := 1; i <= 8; i++ {
		if got := globalString(clu, "cecha"+string(rune('0'+i))); got != strings.Repeat(string(rune('0'+i)), 300) {
			t.Errorf("cecha%d not written", i)
		}
	}
	for _, w := range clu.writes() {
		if len(w) > 1400 {
			t.Errorf("a write of %d characters", len(w))
		}
	}
	if clu.global("duza") != lua.LNil {
		t.Errorf("a value over the packet was sent")
	}
	if got := mustExec(t, engine, `return table.concat(WARN, "|")`); !strings.Contains(got, "bind(duza)") {
		t.Errorf("no warning for the long value: %s", got)
	}
}

// Bindings do not survive a reload: an object of the same name changes, the CLU restarts,
// and nothing is written; no uptime timer is left.
func TestCLUBindReload(t *testing.T) {
	engine, clu := newBindEngine(t)
	baseline := engine.GetTimerStats().ActiveTimers
	mustExec(t, engine, `
v = VirtualNumber:new("v", { initial = 1 })
CLU221000290:bind("cecha", v)
deliver()`)
	if n := engine.GetTimerStats().ActiveTimers; n != baseline+1 {
		t.Errorf("active timers with a binding = %d, want %d", n, baseline+1)
	}
	if err := engine.Reinitialize("CLU"); err != nil {
		t.Fatal(err)
	}
	engine.SetCommandSender(clu.send)
	mustExec(t, engine, `CLU221000290 = CLU_REMOTE:new(221000290, 0xC0A80003, EventBus:getShared())`)
	mustExec(t, engine, bindHarness)
	clu.exec(t, `cecha = nil UPTIME = 3`)
	got := mustExec(t, engine, `
v = VirtualNumber:new("v", { initial = 1 })
v:setValue(5)
NOW.t = NOW.t + 60
CLU_REMOTE._checkBound() deliver()
return tostring(CLU_REMOTE._bindTimer) .. "," .. tostring(next(CLU_REMOTE._bound))`)
	if got != "nil,nil" {
		t.Errorf("timer, bindings after reload = %s, want nil,nil", got)
	}
	if w := clu.writes(); len(w) != 1 {
		t.Errorf("writes = %q, want only the one before the reload", w)
	}
	if n := engine.GetTimerStats().ActiveTimers; n != baseline {
		t.Errorf("active timers after reload = %d, want %d", n, baseline)
	}
}

// In the MCP sandbox (no async sender) a write is recorded as a command without an answer,
// and the uptime check sends nothing.
func TestCLUBindSandbox(t *testing.T) {
	engine, _ := newBindEngine(t)
	got := mustExec(t, engine, `
__go_send_command_async = nil
REC = {}
__go_send_command_noreply = function(ip, script) REC[#REC + 1] = script end
__go_send_command = function(ip, script) REC[#REC + 1] = "blocking " .. script end
v = VirtualNumber:new("v", { initial = 1 })
CLU221000290:bind("cecha", v)
v:setValue(2)
CLU_REMOTE._checkBound()
return table.concat(REC, "|")`)
	want := `(function() setVar("cecha", 1) end)()|(function() setVar("cecha", 2) end)()`
	if got != want {
		t.Errorf("recorded:\n got %s\nwant %s", got, want)
	}
}
