package lua

import (
	"strings"
	"testing"
)

// lib/cct: a warm and a cold dimmer as one colour temperature, on a virtual clock.
const cctHarness = `
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
	TIMERS.clearTimer = function(self, id) T.q[id] = nil end
	function eq(label, got, want)
		if got ~= want then error(label .. ": got " .. tostring(got) .. ", want " .. tostring(want), 2) end
	end
	function near(label, got, want)
		if math.abs(got - want) > 1e-6 then error(label .. ": got " .. tostring(got) .. ", want " .. tostring(want), 2) end
	end
	cct = require("lib/cct")
`

func TestCCTMixKeepsBrightnessAndKelvin(t *testing.T) {
	engine := NewLuaEngine()
	defer engine.Close()
	mustExec(t, engine, cctHarness+`
		W, C = VirtualDimmer:new('cieply'), VirtualDimmer:new('zimny')
		F = cct.mix(W, C, { warm = 2700, cold = 6500 })

		eq("off: no kelvin yet", F.getKelvin(), nil)
		eq("off", F.isOn(), false)
		eq("off brightness", F.getBrightness(), 0)

		-- a colour temperature set while off is kept and sends nothing
		F.setKelvin(4600)
		eq("kept", F.getKelvin(), 4600)
		eq("still off", W:getValue() + C:getValue(), 0)

		-- switchOn: full brightness at the kept kelvin; the cold channel 50 ms later
		F.switchOn()
		near("warm at once", W:getValue(), 0.5)
		eq("cold waits", C:getValue(), 0)
		eq("reads what is being sent", F.getKelvin(), 4600)
		T.advance(50)
		near("cold", C:getValue(), 0.5)
		eq("brightness", F.getBrightness(), 100)
		eq("on", F.isOn(), true)

		-- brightness keeps the ratio, kelvin keeps the brightness
		F.setBrightness(40)
		T.advance(100)
		near("warm 40%", W:getValue(), 0.2)
		near("cold 40%", C:getValue(), 0.2)
		F.setKelvin(2700)
		T.advance(100)
		near("all warm", W:getValue(), 0.4)
		eq("no cold", C:getValue(), 0)
		eq("kelvin", F.getKelvin(), 2700)
		eq("brightness kept", F.getBrightness(), 40)

		-- out of range clamps to the channels' ends
		F.setKelvin(9000)
		T.advance(100)
		eq("all cold", W:getValue(), 0)
		near("cold 40", C:getValue(), 0.4)
		eq("clamped", F.getKelvin(), 6500)

		-- off and on again: the same level and kelvin; brightness 0 is off too
		F.switchOff()
		T.advance(100)
		eq("off", F.isOn(), false)
		eq("kelvin kept off", F.getKelvin(), 6500)
		F.switchOn()
		T.advance(100)
		near("back on cold", C:getValue(), 0.4)
		eq("back on warm", W:getValue(), 0)
		F.setBrightness(0)
		T.advance(100)
		eq("brightness 0 is off", F.isOn(), false)
		F.switchOn()
		T.advance(100)
		near("back from 0", C:getValue(), 0.4)

		-- a change made elsewhere shows: 3/4 warm is 3650 K
		W:setValue(0.3); C:setValue(0.1)
		eq("from the channels", F.getKelvin(), 3650)
		eq("sum", F.getBrightness(), 40)
		-- both at full: brightness stops at 100; a lit light shows at least 1%
		W:setValue(1); C:setValue(1)
		eq("capped", F.getBrightness(), 100)
		W:setValue(0.003); C:setValue(0)
		eq("at least 1%", F.getBrightness(), 1)
	`)
}

// A scene and a wall switch change the channels; a poll that saw the scene keeps its level and
// kelvin, so switching on from the dashboard brings them back.
func TestCCTMixRemembersOutsideChange(t *testing.T) {
	engine := NewLuaEngine()
	defer engine.Close()
	mustExec(t, engine, cctHarness+`
		W, C = VirtualDimmer:new('cieply'), VirtualDimmer:new('zimny')
		F = cct.mix(W, C)
		W:setValue(0.2); C:setValue(0.6)
		eq("scene seen by a poll", F.getKelvin(), 5550)
		eq("scene brightness", F.getBrightness(), 80)
		W:setValue(0); C:setValue(0)
		eq("wall switch off", F.isOn(), false)
		eq("kelvin while off", F.getKelvin(), 5550)
		F.switchOn()
		T.advance(100)
		near("warm back", W:getValue(), 0.2)
		near("cold back", C:getValue(), 0.6)
		eq("kelvin back", F.getKelvin(), 5550)
		eq("brightness back", F.getBrightness(), 80)
	`)
}

// On 0..255 channels a dim light keeps the kelvin it was given: 2% of 3000 K leaves the cold
// channel at 0, and 100% again is 3000 K, not 2700 K.
func TestCCTMixSteppedChannels(t *testing.T) {
	engine := NewLuaEngine()
	defer engine.Close()
	mustExec(t, engine, cctHarness+`
		local function stepped()
			local o = { v = 0 }
			function o:getValue() return self.v end
			function o:setValue(x) self.v = math.floor(x * 255 + 0.5) / 255 end
			return o
		end
		W, C = stepped(), stepped()
		F = cct.mix(W, C)
		F.setBrightness(100)
		T.advance(100)
		F.setKelvin(3000)
		T.advance(100)
		eq("3000 K", F.getKelvin(), 3000)
		F.setBrightness(2)
		T.advance(100)
		eq("cold rounds to 0", C:getValue(), 0)
		eq("still 3000 K", F.getKelvin(), 3000)
		eq("2%", F.getBrightness(), 2)
		F.setBrightness(100)
		T.advance(100)
		eq("100% at 3000 K", F.getKelvin(), 3000)
		near("cold back", C:getValue(), 20 / 255)
	`)
}

// Commands to a CLU: only a channel that changes is sent, every command at least 50 ms after the
// previous one, and a new command replaces values still waiting.
func TestCCTMixSpacesCommands(t *testing.T) {
	engine := NewLuaEngine()
	defer engine.Close()
	got := mustExec(t, engine, cctHarness+`
		SENT = {}
		local clu = { executeNoReply = function(self, n, s) SENT[#SENT + 1] = T.now .. ' ' .. s end }
		W, C = RemoteLight:new(clu, 'DIM1', 'cieply'), RemoteLight:new(clu, 'DIM2', 'zimny')
		F = cct.mix(W, C)
		eq("before a report", F.getBrightness(), nil)
		eq("before a report", F.isOn(), nil)
		F.setBrightness(50)
		F.setKelvin(6500)
		T.advance(20)
		F.setKelvin(6000)
		F.setKelvin(6500)
		T.advance(200)
		F.setKelvin(6500)
		T.advance(200)
		return table.concat(SENT, ';')
	`)
	// 0.25/0.25 was replaced before its cold command; the last setKelvin changes nothing
	want := "0 DIM1:set(0, 0.25);50 DIM1:set(0, 0);100 DIM2:set(0, 0.5)"
	if got != want {
		t.Errorf("sent %q, want %q", got, want)
	}
}

func TestCCTMixErrors(t *testing.T) {
	cases := []struct{ call, want string }{
		{`cct.mix(nil, VirtualDimmer:new('c'))`, "the warm channel must be a dimmer"},
		{`cct.mix(VirtualDimmer:new('w'), {})`, "the cold channel must be a dimmer"},
		{`cct.mix(VirtualDimmer:new('w'), VirtualDimmer:new('c'), { hot = 1 })`, `unknown option "hot"`},
		{`cct.mix(VirtualDimmer:new('w'), VirtualDimmer:new('c'), { warm = 6500, cold = 2700 })`, "below cold"},
		{`cct.mix(VirtualDimmer:new('w'), VirtualDimmer:new('c'), { warm = '2700' })`, "must be numbers"},
	}
	for _, c := range cases {
		engine := NewLuaEngine()
		_, err := engine.Execute("local cct = require('lib/cct')\nlocal ok, err = pcall(function()\n  " + c.call + "\nend)\nerror(err, 0)")
		engine.Close()
		if err == nil || !strings.Contains(err.Error(), "<string>:3: ") || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: %v", c.call, err)
		}
	}
}

// The mix is what section:colorTemp takes: the dashboard drives both channels.
func TestCCTMixOnDashboard(t *testing.T) {
	engine := NewLuaEngine()
	defer engine.Close()
	mustExec(t, engine, cctHarness+`
		_dashboards = {}
		W, C = VirtualDimmer:new('cieply'), VirtualDimmer:new('zimny')
		Dashboard.new('m'):section('Biuro'):colorTemp('biuro', 'Biuro', cct.mix(W, C)):build()
	`)
	var res map[string]interface{}
	for _, a := range []string{`"set", 80`, `"setKelvin", 3650`} {
		dashJSON(t, engine, `Dashboard.API.executeAction("m", "biuro", `+a+`)`, &res)
		if res["success"] != true {
			t.Fatalf("%s: %v", a, res)
		}
		mustExec(t, engine, `T.advance(200)`)
	}
	var vals map[string]map[string]interface{}
	dashJSON(t, engine, `Dashboard.API.getValues("m")`, &vals)
	if v := vals["biuro"]; v["kelvin"] != 3650.0 || v["brightness"] != 80.0 || v["on"] != true {
		t.Errorf("values = %v", v)
	}
	if got := mustExec(t, engine, `return string.format('%.2f %.2f', W:getValue(), C:getValue())`); got != "0.60 0.20" {
		t.Errorf("channels = %s", got)
	}
}
