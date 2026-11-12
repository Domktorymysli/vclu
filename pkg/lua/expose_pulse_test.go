package lua

import (
	"strings"
	"testing"
)

// A virtual clock under TIMERS (what the pulse's off uses).
const pulseClock = `
	T = { now = 0, q = {}, seq = 0 }
	function T.advance(ms)
		local target = T.now + ms
		while true do
			local best, bid
			for id, t in pairs(T.q) do
				if t.at <= target and (not best or t.at < best.at or (t.at == best.at and id < bid)) then best, bid = t, id end
			end
			if not best then break end
			T.q[bid] = nil
			T.now = best.at
			best.fn()
		end
		T.now = target
	end
	TIMERS.setTimeout = function(self, ms, fn)
		T.seq = T.seq + 1
		T.q[T.seq] = { at = T.now + ms, fn = fn }
		return T.seq
	end
`

// pulse is only for a switch, in whole milliseconds from 100 to 60000; false takes it off.
func TestExposePulseOptionChecked(t *testing.T) {
	engine, _, _ := newHistoryEngine(t)
	for _, bad := range []string{
		`expose(VirtualSwitch:new("a"), "light", { pulse = 1000 })`,
		`expose(VirtualSwitch:new("a"), "switch", { pulse = 50 })`,
		`expose(VirtualSwitch:new("a"), "switch", { pulse = 60001 })`,
		`expose(VirtualSwitch:new("a"), "switch", { pulse = 1.5 })`,
		`expose(VirtualSwitch:new("a"), "switch", { pulse = "1000" })`,
		`expose(VirtualSwitch:new("a"), "switch", { pulse = true })`,
		`expose(VirtualSwitch:new("a"), "light"):update({ pulse = 1000 })`,
	} {
		if _, err := engine.Execute(bad); err == nil || !strings.Contains(err.Error(), "pulse") {
			t.Errorf("%s: err = %v", bad, err)
		}
	}
	luaOK(t, engine, `
		local h = expose(VirtualSwitch:new("ok"), "switch", { pulse = 100 })
		h:update({ pulse = 60000 })
		h:update({ pulse = false })`)
}

// "on" pulses through setValue (Home Assistant, HomeKit, bots): on, then off by itself after
// pulse ms; "off" does nothing; a pulse asked while one runs is ignored; a readonly one refuses.
func TestExposePulseSetValue(t *testing.T) {
	engine, _, _ := newHistoryEngine(t)
	luaOK(t, engine, pulseClock+`
		G = VirtualSwitch:new("brama")
		H = expose(G, "switch", { id = "brama", pulse = 1000 })`)
	state := func() string { return mustExec(t, engine, `tostring(G:getValue())`) }

	if v := mustExec(t, engine, `tostring(H:setValue(1))`); v != "true" || state() != "1" {
		t.Fatalf("on: %s, state %s", v, state())
	}
	luaOK(t, engine, `T.advance(500); H:setValue(0)`)
	if state() != "1" {
		t.Errorf("off during the pulse switched it off: %s", state())
	}
	luaOK(t, engine, `T.advance(499)`)
	if state() != "1" {
		t.Errorf("off before the pulse ended: %s", state())
	}
	luaOK(t, engine, `T.advance(1)`)
	if state() != "0" {
		t.Errorf("still on after the pulse: %s", state())
	}

	// a second "on" while the first pulse runs: ignored, the off stays at 1000 ms
	luaOK(t, engine, `H:setValue(1); T.advance(600); G:setValue(0); H:setValue(1)`)
	if state() != "0" {
		t.Errorf("a second pulse during the first one ran: %s", state())
	}
	luaOK(t, engine, `T.advance(400); H:setValue(1)`)
	if state() != "1" {
		t.Errorf("a pulse after the first ended did not run: %s", state())
	}
	luaOK(t, engine, `T.advance(1000)`)

	luaOK(t, engine, `R = expose(VirtualSwitch:new("ro"), "switch", { id = "ro", pulse = 1000, readonly = true })`)
	if v := mustExec(t, engine, `tostring(R:setValue(1))`); v != "false" {
		t.Errorf("readonly pulse switch: setValue = %s", v)
	}
}

// A Grenton DOUT pulses with its own SwitchOn(time): one command, the CLU switches it off.
func TestExposePulseRemoteSwitch(t *testing.T) {
	engine, _, _ := newHistoryEngine(t)
	luaOK(t, engine, pulseClock+`
		SENT = {}
		local clu = { executeNoReply = function(self, n, s) SENT[#SENT + 1] = s end,
			execute = function(self, n, s) SENT[#SENT + 1] = s end }
		D = RemoteSwitch:new(clu, "DOU7823", "Domofon")
		H = expose(D, "switch", { id = "domofon", pulse = 1000 })
		H:setValue(1)
		T.advance(1000)`)
	sent := mustExec(t, engine, `table.concat(SENT, "\n")`)
	if !strings.Contains(sent, "DOU7823:execute(1, 1000)") || strings.Count(sent, "DOU7823") != 1 {
		t.Errorf("sent to the CLU:\n%s", sent)
	}
}

// Through the cloud: on, open and close pulse, off does nothing, the snapshot says pulse; bots
// cannot call the object's own methods.
func TestExposePulseCloud(t *testing.T) {
	engine, _, _ := newHistoryEngine(t)
	c := commander{t, engine}
	luaOK(t, engine, pulseClock+`
		G = VirtualSwitch:new("brama")
		registerObject("virt.brama", G)
		expose(G, "switch", { pulse = 1000 }):remote()
		L = VirtualSwitch:new("lampa")
		registerObject("virt.lampa", L)
		expose(L, "switch"):remote()`)
	list := mustExec(t, engine, `JSON.encode(ExposedObjects:remoteList())`)
	if !strings.Contains(list, `"pulse":true`) || strings.Count(list, `"pulse"`) != 1 {
		t.Errorf("remoteList = %s", list)
	}
	for _, action := range []string{"open", "close", "on"} {
		if a := c.run("virt.brama", action, "[]", false, ""); !a.OK {
			t.Fatalf("%s = %+v", action, a)
		}
		if v := mustExec(t, engine, `tostring(G:getValue())`); v != "1" {
			t.Errorf("%s: state %s", action, v)
		}
		luaOK(t, engine, `T.advance(1000)`)
		if v := mustExec(t, engine, `tostring(G:getValue())`); v != "0" {
			t.Errorf("%s: still on after the pulse", action)
		}
	}
	if a := c.run("virt.brama", "off", "[]", false, ""); !a.OK {
		t.Errorf("off = %+v", a)
	}
	if a := c.run("virt.lampa", "open", "[]", false, ""); a.Reason != "invalid" {
		t.Errorf("open on a plain switch = %+v", a)
	}
	if out := mustExec(t, engine, `MCP.execute_method("virt.brama", "switchOn", nil)`); !strings.Contains(out, "only pulses") {
		t.Errorf("bot calling switchOn = %s", out)
	}
}
