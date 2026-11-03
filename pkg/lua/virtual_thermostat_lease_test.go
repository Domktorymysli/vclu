package lua

import (
	"fmt"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Domktorymysli/vclu/pkg/history"
	"github.com/Domktorymysli/vclu/pkg/plugins"
)

// VirtualThermostat with lease (#144): a Grenton output is switched on for lease seconds and
// renewed while heating, so the CLU switches it off by itself when vCLU stops. A fake CLU
// keeps the relay with its own timer and can lose commands; timers run by their period on a
// virtual clock.

const leaseHarness = `
	TIMERS_V = { list = {} }
	__go_setInterval = function(fn, ms) table.insert(TIMERS_V.list, { fn = fn, ms = ms, due = __go_time_ms() + ms }); return #TIMERS_V.list end
	__go_setTimeout = function(fn, ms) table.insert(TIMERS_V.list, { fn = fn, due = __go_time_ms() + ms }); return #TIMERS_V.list end
	__go_clearTimer = function(id) if TIMERS_V.list[id] then TIMERS_V.list[id].fn = nil end end
	function fire()
		local t = __go_time_ms()
		for i, tm in ipairs(TIMERS_V.list) do
			if tm.fn and tm.due <= t then
				local fn = tm.fn
				if tm.ms then tm.due = math.max(tm.due + tm.ms, t + 1) else tm.fn = nil end
				fn()
			end
		end
	end
	-- the CLU: commands as sent, the relay with the CLU's own timer, lost commands by number
	CLU = { address = "192.168.0.5", sent = {}, drop = {}, relay = 0, selfOff = {} }
	function CLU:executeNoReply(m, script)
		local n = #self.sent + 1
		self.sent[n] = { at = __go_time_ms(), script = script }
		if self.drop[n] or self.dropAll then return end
		local method, ms = script:match("execute%((%d+), (%d+)")
		if method == "1" then
			self.relay, self.offAt = 1, nil
			if tonumber(ms) > 0 then self.offAt = __go_time_ms() + tonumber(ms) end
		elseif method == "2" then
			self.relay, self.offAt = 0, nil
		end
	end
	function cluCheck()
		if CLU.offAt and __go_time_ms() >= CLU.offAt then
			CLU.relay, CLU.offAt = 0, nil
			table.insert(CLU.selfOff, __go_time_ms())
		end
	end
	-- commands since the last call, as "script@seconds"
	SEEN = { n = 0 }
	function sent()
		local out = {}
		for i = SEEN.n + 1, #CLU.sent do
			table.insert(out, CLU.sent[i].script .. "@" .. ((CLU.sent[i].at - T0) / 1000))
		end
		SEEN.n = #CLU.sent
		return table.concat(out, ";")
	end
	T0 = __go_time_ms()
	RELAY = RemoteSwitch:new(CLU, "DOU1", "relay")
	registerObject("CLU1.DOU1", RELAY)
	TEMP = VirtualNumber:new("t")
	-- everything a renewal must not cause
	NOISE = { list = {} }
	StateBus:getShared():on("state_changed", function(e) if e.path == "CLU1.DOU1" then table.insert(NOISE.list, "bus " .. tostring(e.value)) end end)
	RELAY:onChange(function(v) table.insert(NOISE.list, "relay " .. tostring(v)) end)
	function noise() local s = table.concat(NOISE.list, ","); NOISE.list = {}; return s end
	function thermostat(opts)
		opts.source, opts.output = opts.source or TEMP, opts.output or RELAY
		TH = VirtualThermostat:new("salon", opts)
		TH:onChange(function(v) table.insert(NOISE.list, "th " .. tostring(v)) end)
		TH:onEvent("OnSettingsChange", function(f, v) table.insert(NOISE.list, "settings " .. f) end)
		return TH
	end
`

type leaseEngine struct {
	*LuaEngine
	clk   *stepClock
	store *history.Store
}

func newLeaseEngine(t *testing.T) *leaseEngine {
	t.Helper()
	engine := NewLuaEngine()
	t.Cleanup(engine.Close)
	engine.SetKVStoreManager(plugins.NewKVStoreManager(t.TempDir()))
	clk := &stepClock{t: time.Date(2026, 11, 3, 6, 0, 0, 0, time.UTC)}
	store := history.New(history.DefaultConfig(), clk.now)
	engine.SetHistoryStore(store)
	SetClock(engine.state, fixedClockFunc(clk.now))
	if _, err := engine.Execute(leaseHarness); err != nil {
		t.Fatalf("harness: %v", err)
	}
	return &leaseEngine{engine, clk, store}
}

// run moves the clock in 500 ms steps, firing due timers; with timers false the Lua loop
// does nothing (vCLU killed or stuck) and only the CLU's own timer runs.
func (le *leaseEngine) run(t *testing.T, d time.Duration, timers bool) {
	t.Helper()
	step := 500 * time.Millisecond
	for d > 0 {
		s := min(step, d)
		le.clk.t = le.clk.t.Add(s)
		d -= s
		if timers {
			mustExec(t, le.LuaEngine, `fire()`)
		}
		mustExec(t, le.LuaEngine, `cluCheck()`)
	}
}

func (le *leaseEngine) exec(t *testing.T, script string) string {
	t.Helper()
	return mustExec(t, le.LuaEngine, script)
}

// sendTimes returns the times (s from the start) of the commands in s, all of which must be want.
func sendTimes(t *testing.T, s, want string) []float64 {
	t.Helper()
	var at []float64
	for _, c := range strings.Split(s, ";") {
		script, sec, _ := strings.Cut(c, "@")
		if script != want {
			t.Fatalf("command %q in %q, want only %q", script, s, want)
		}
		v, err := strconv.ParseFloat(sec, 64)
		if err != nil {
			t.Fatal(err)
		}
		at = append(at, v)
	}
	return at
}

// Without lease the output gets the same commands as before: plain switchOn/switchOff, nothing
// in between, and no timer besides the tick.
func TestVirtualThermostatLease_WithoutLeaseUnchanged(t *testing.T) {
	le := newLeaseEngine(t)
	le.exec(t, `thermostat({ setpoint = 21 }); TEMP:setValue(20)`)
	if got := le.exec(t, `return sent() .. "|" .. #TIMERS_V.list`); got != "DOU1:execute(2, 0)@0;DOU1:execute(1, 0)@0|1" {
		t.Fatalf("start: %s, want off, plain on and one timer", got)
	}
	le.run(t, 10*time.Minute, true)
	if got := le.exec(t, `return sent()`); got != "" {
		t.Errorf("heating for 10 min without lease sent %q, want nothing", got)
	}
	le.exec(t, `TEMP:setValue(21.5)`)
	if got := le.exec(t, `return sent()`); got != "DOU1:execute(2, 0)@600" {
		t.Errorf("crossing: %q, want a plain off", got)
	}
}

// With lease = 90 heating sends switchOn(90000) and renews it every quarter of the lease, so
// a renewal always comes before a third of the time is left; the CLU never switches it off.
func TestVirtualThermostatLease_Renews(t *testing.T) {
	le := newLeaseEngine(t)
	le.exec(t, `thermostat({ setpoint = 21, lease = 90 })`)
	le.run(t, 7*time.Second, true) // switched on out of phase with the renewal timer
	le.exec(t, `TEMP:setValue(20)`)
	if got := le.exec(t, `return sent()`); got != "DOU1:execute(2, 0)@0;DOU1:execute(1, 90000)@7" {
		t.Fatalf("start: %q, want off, then on for 90 s", got)
	}
	le.run(t, 10*time.Minute, true)
	at := append([]float64{7}, sendTimes(t, le.exec(t, `return sent()`), "DOU1:execute(1, 90000)")...)
	if len(at) < 25 {
		t.Fatalf("%d renewals in 10 min, want one every 22.5 s", len(at)-1)
	}
	for i := 1; i < len(at); i++ {
		if gap := at[i] - at[i-1]; gap > 22.5 || gap <= 0 {
			t.Errorf("renewal at %v s, %v s after the one before: want at most 22.5 s (a quarter)", at[i], gap)
		}
	}
	if got := le.exec(t, `return CLU.relay .. "/" .. #CLU.selfOff`); got != "1/0" {
		t.Errorf("relay/switched off by the CLU = %s, want 1/0", got)
	}
}

// UDP loses commands: two lost renewals in a row keep the relay on. When vCLU stops (killed,
// stuck) the CLU switches the relay off within lease of the last command it got.
func TestVirtualThermostatLease_LostRenewalsAndDeadVCLU(t *testing.T) {
	le := newLeaseEngine(t)
	// commands 1 off, 2 on; the renewals 3-4 and 8-9 are lost
	le.exec(t, `CLU.drop = { [3] = true, [4] = true, [8] = true, [9] = true }
		thermostat({ setpoint = 21, lease = 90 }); TEMP:setValue(20)`)
	le.run(t, 10*time.Minute, true)
	if got := le.exec(t, `return CLU.relay .. "/" .. #CLU.selfOff .. "/" .. #CLU.sent`); !strings.HasPrefix(got, "1/0/") {
		t.Fatalf("two lost renewals in a row: relay/self-off/sent = %s, want the relay kept on", got)
	}
	// vCLU stops: nothing more is sent and the CLU switches off 90 s after the last command
	lastAt, _ := strconv.ParseFloat(le.exec(t, `return (CLU.sent[#CLU.sent].at - T0) / 1000`), 64)
	le.run(t, time.Duration((lastAt+89-600)*float64(time.Second)), false)
	if got := le.exec(t, `return CLU.relay`); got != "1" {
		t.Fatal("relay off before the lease ran out")
	}
	le.run(t, time.Second, false)
	got := le.exec(t, `return CLU.relay .. "/" .. #CLU.selfOff .. "/" .. ((CLU.selfOff[1] or 0) - T0) / 1000`)
	if got != "0/1/"+strconv.FormatFloat(lastAt+90, 'f', -1, 64) {
		t.Errorf("vCLU dead: relay/self-off/at = %s, want 0/1/%v (90 s after the last command)", got, lastAt+90)
	}
}

// A lost on or a CLU restarting with the relay off is put right by the next renewal; a lost off
// ends with the lease, as the relay was last switched on for it.
func TestVirtualThermostatLease_LostCommandAndCLURestart(t *testing.T) {
	le := newLeaseEngine(t)
	le.exec(t, `CLU.drop = { [2] = true }; thermostat({ setpoint = 21, lease = 90 }); TEMP:setValue(20)`)
	le.run(t, 22*time.Second, true)
	if got := le.exec(t, `return CLU.relay`); got != "0" {
		t.Fatalf("the on was lost: relay = %s", got)
	}
	le.run(t, time.Second, true)
	if got := le.exec(t, `return CLU.relay`); got != "1" {
		t.Fatalf("after the first renewal: relay = %s, want 1", got)
	}
	le.run(t, 5*time.Minute, true)
	le.exec(t, `CLU.relay, CLU.offAt = 0, nil`) // the CLU restarts with its outputs off
	le.run(t, 23*time.Second, true)
	if got := le.exec(t, `return CLU.relay`); got != "1" {
		t.Fatalf("after a CLU restart and a renewal: relay = %s, want 1", got)
	}
	// the off is lost: the CLU switches off when the last renewal's time runs out
	le.exec(t, `sent(); CLU.drop[#CLU.sent + 1] = true; TEMP:setValue(21.5)`)
	if got := le.exec(t, `return sent() .. "|" .. CLU.relay`); !strings.HasPrefix(got, "DOU1:execute(2, 0)@") || !strings.HasSuffix(got, "|1") {
		t.Fatalf("lost off: %q", got)
	}
	le.run(t, 90*time.Second, true)
	if got := le.exec(t, `return sent() .. "|" .. CLU.relay .. "/" .. #CLU.selfOff`); got != "|0/1" {
		t.Errorf("90 s after a lost off: %q, want nothing sent and the relay off by the CLU", got)
	}
}

// A renewal is not a change: no history entry, no event on the bus, no OnChange of the relay
// or the thermostat, no OnSettingsChange. The CLU's own switch-off is not taken for the
// thermostat's: a later change of the relay from Grenton is recorded as Grenton's.
func TestVirtualThermostatLease_RenewalIsNoChange(t *testing.T) {
	le := newLeaseEngine(t)
	le.exec(t, `thermostat({ setpoint = 21, lease = 90 }); TEMP:setValue(20.2)`)
	if got := le.exec(t, `sent(); return noise()`); got != "bus 0,th 20.2,relay 1,bus 1" {
		t.Fatalf("start: %q, want the relay's off and on and the reading", got)
	}
	before := len(le.store.Query(history.Query{Path: "CLU1.DOU1"}))
	le.run(t, 5*time.Minute, true)
	if n := len(sendTimes(t, le.exec(t, `return sent()`), "DOU1:execute(1, 90000)")); n < 10 {
		t.Fatalf("%d renewals in 5 min", n)
	}
	if got := le.exec(t, `return noise()`); got != "" {
		t.Errorf("renewals caused %q, want nothing", got)
	}
	if after := len(le.store.Query(history.Query{Path: "CLU1.DOU1"})); after != before {
		t.Errorf("history of the relay: %d entries after renewals, want %d", after, before)
	}

	// the loop stalls, the lease runs out: the CLU's report of it is Grenton's
	le.run(t, 90*time.Second, false)
	le.exec(t, `_:sync("CLU1.DOU1", 0)`)
	last, ok := le.store.Last("CLU1.DOU1")
	if !ok || fmt.Sprint(last.Value) != "0" || last.Origin != "grenton" {
		t.Errorf("the CLU's switch-off: %+v, want value 0 from grenton", last)
	}
	// the loop runs again and renews; a person switches the relay off in Grenton when that
	// renewal's time would have run out
	le.exec(t, `fire()`)
	le.run(t, 89*time.Second, false)
	le.exec(t, `_:sync("CLU1.DOU1", 0)`)
	if last, _ := le.store.Last("CLU1.DOU1"); last.Origin != "grenton" {
		t.Errorf("a change from Grenton 89 s after a renewal: %+v, want origin grenton", last)
	}
}

// Every switch-off is a plain switchOff() and ends the renewals: a crossing, stop(), a stale
// source, maxOn and the release before a reload.
func TestVirtualThermostatLease_OffEndsRenewals(t *testing.T) {
	for _, tc := range []struct {
		name, opts, off string
		wait            time.Duration
	}{
		{"crossing", ``, `TEMP:setValue(21.5)`, 0},
		{"stop", ``, `TH:stop()`, 0},
		{"stale", `staleAfter = 120`, ``, 90 * time.Second},
		{"maxOn", `maxOn = 300`, ``, 270 * time.Second},
		{"release", ``, `__vclu_release_thermostats()`, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			le := newLeaseEngine(t)
			le.exec(t, `thermostat({ setpoint = 21, lease = 90, `+tc.opts+` }); TEMP:setValue(20)`)
			le.run(t, 30*time.Second, true)
			le.exec(t, `sent()`)
			le.exec(t, tc.off)
			le.run(t, tc.wait, true)
			got := le.exec(t, `return sent()`)
			cmds := strings.Split(got, ";")
			if off := cmds[len(cmds)-1]; !strings.HasPrefix(off, "DOU1:execute(2, 0)@") {
				t.Fatalf("switch-off sent %q, want a plain off last", got)
			}
			for _, c := range cmds[:len(cmds)-1] {
				if !strings.HasPrefix(c, "DOU1:execute(1, 90000)@") {
					t.Fatalf("before the off: %q", got)
				}
			}
			le.run(t, 10*time.Minute, true)
			if got := le.exec(t, `return sent() .. "|" .. CLU.relay`); got != "|0" {
				t.Errorf("after the off: %q, want no renewal and the relay off", got)
			}
		})
	}
}

// A stop during the lease ends it, a start begins a new one with its renewals.
func TestVirtualThermostatLease_StopAndStart(t *testing.T) {
	le := newLeaseEngine(t)
	le.exec(t, `thermostat({ setpoint = 21, lease = 60 }); TEMP:setValue(20)`)
	le.run(t, 20*time.Second, true)
	le.exec(t, `sent(); TH:stop()`)
	le.run(t, 5*time.Minute, true)
	le.exec(t, `TH:start()`)
	if got := le.exec(t, `return sent()`); got != "DOU1:execute(2, 0)@20;DOU1:execute(1, 60000)@320" {
		t.Fatalf("stop, 5 min, start: %q", got)
	}
	le.run(t, time.Minute, true)
	if n := len(sendTimes(t, le.exec(t, `return sent()`), "DOU1:execute(1, 60000)")); n != 4 {
		t.Errorf("%d renewals in a minute after the start, want 4 (every 15 s)", n)
	}
}

// A dimmer is a Grenton output too: its own SwitchOn index with the time.
func TestVirtualThermostatLease_Dimmer(t *testing.T) {
	le := newLeaseEngine(t)
	le.exec(t, `thermostat({ output = RemoteLight:new(CLU, "DIM1"), setpoint = 21, lease = 30 }); TEMP:setValue(20)`)
	le.run(t, 8*time.Second, true)
	if got := le.exec(t, `return sent()`); got != "DIM1:execute(3, 0)@0;DIM1:execute(2, 30000)@0;DIM1:execute(2, 30000)@7.5" {
		t.Errorf("dimmer: %q", got)
	}
}

// lease on an output that cannot be switched on for a time, or below 30 s, is an error at
// new(); a Grenton output by its path is fine.
func TestVirtualThermostatLease_Validation(t *testing.T) {
	le := newLeaseEngine(t)
	got := le.exec(t, `(function()
		local out = {}
		local outputs = {
			{ "virtual", VirtualSwitch:new("v") },
			{ "table", { switchOn = function() end, switchOff = function() end } },
			{ "rgb", RemoteRGBLight:new(CLU, "LED1") },
			{ "channel", RemoteLedChannel:new(CLU, "LEC1") },
		}
		for i, o in ipairs(outputs) do
			local ok, err = pcall(VirtualThermostat.new, VirtualThermostat, "x", { source = TEMP, output = o[2], lease = 90 })
			table.insert(out, o[1] .. "=" .. tostring(ok) .. ":" .. tostring(not ok and err:find("Grenton output") ~= nil))
		end
		for i, l in ipairs({ 29, 0, -90, "x", 0/0, false }) do
			local ok, err = pcall(VirtualThermostat.new, VirtualThermostat, "x", { source = TEMP, output = RELAY, lease = l })
			table.insert(out, tostring(l) .. "=" .. tostring(ok) .. ":" .. tostring(not ok and err:find("lease must be seconds >= 30") ~= nil))
		end
		local ok = pcall(VirtualThermostat.new, VirtualThermostat, "x", { source = TEMP, output = "CLU1.DOU1", lease = 30 })
		table.insert(out, "path=" .. tostring(ok))
		return table.concat(out, " ")
	end)()`)
	for _, want := range []string{"virtual=false:true", "table=false:true", "rgb=false:true", "channel=false:true",
		"29=false:true", "0=false:true", "-90=false:true", "x=false:true", "NaN=false:true", "false=false:true", "path=true"} {
		if !strings.Contains(got, want) {
			t.Errorf("validation: %s, missing %s", got, want)
		}
	}
}

// A reload releases a leased relay with a plain off, as without lease.
func TestVirtualThermostatLease_Reload(t *testing.T) {
	module := strings.Replace(heatingModule, "setpoint = 21 }", "setpoint = 21, lease = 90 }", 1)
	dir := createTestModulesDir(t, map[string]string{"heating.lua": module})
	engine := NewLuaEngine()
	t.Cleanup(engine.Close)
	rec := &sentCommands{}
	engine.SetCommandNoReplySender(rec.send)
	engine.LoadUserModules(dir)
	if got := rec.take(); got != relayOff+";DOU1:execute(1, 90000)" {
		t.Fatalf("module start: sent %q, want off then on for 90 s", got)
	}
	if err := engine.Reinitialize("CLU"); err != nil {
		t.Fatal(err)
	}
	if got := rec.take(); got != relayOff {
		t.Errorf("sent on reload: %q, want %q", got, relayOff)
	}
}
