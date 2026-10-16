package lua

import (
	"strings"
	"testing"
)

// A cover published with expose() is announced to Home Assistant on HA's scale
// (0 = closed .. 100 = open). vCLU counts 0 = open, so commands and reports are
// converted, and a Grenton roller is moved by its own methods: a write to its
// feature 0 (State, read-only) moves nothing (#36).

const haExposedCoverSetup = `
	_pub = {}
	HAStatePublisher.setup({
		publish = function(self, topic, payload) table.insert(_pub, { topic = topic, payload = payload }); return true end,
		subscribe = function() end, unsubscribe = function() end,
	})
	_sent = {}
	local clu = { executeNoReply = function(s, i, cmd) table.insert(_sent, cmd) end }
	_roller = RemoteCover:new(clu, "ROL1", "salon")
	_roller._hasPosition = true -- the OM import marks class 24
	_old = RemoteCover:new(clu, "ROL2", "strych")
	_virtual = VirtualCover:new("taras")
	expose(_roller, "cover", { name = "Salon", path = "t:salon" })
	expose(_old, "cover", { name = "Strych", path = "t:strych" })
	expose(_virtual, "cover", { name = "Taras", path = "t:taras" })

	function _last(topic)
		local v
		for _, p in ipairs(_pub) do if p.topic == topic then v = p.payload end end
		return tostring(v)
	end
`

func TestHAExposedCoverCommands(t *testing.T) {
	engine := NewLuaEngine()
	defer engine.Close()
	if _, err := engine.Execute(haExposedCoverSetup); err != nil {
		t.Fatalf("setup failed: %v", err)
	}

	// A position from HA reaches setPosition on vCLU's scale, clamped and rounded;
	// RemoteCover converts it once more to Grenton's (SetPosition 0 = closed).
	got := mustExec(t, engine, `(function()
		local t = "vclu/exposed/t/cover/salon/position/set"
		for _, p in ipairs({ "42", "0", "100", "150", "-5", "69.6" }) do
			HACommands.handleExposedMessage(t, p)
		end
		return table.concat(_sent, ";") .. "|" .. tostring(_roller:getPosition())
	end)()`)
	want := "ROL1:execute(10, 42, 91);ROL1:execute(10, 0, 91);ROL1:execute(10, 100, 91);" +
		"ROL1:execute(10, 100, 91);ROL1:execute(10, 0, 91);ROL1:execute(10, 70, 91)|nil"
	if got != want {
		t.Errorf("position commands\n got: %s\nwant: %s", got, want)
	}

	refused := mustExec(t, engine, `(function()
		_sent = {}
		local ok, err = HACommands.handleExposedMessage("vclu/exposed/t/cover/salon/position/set", "abc")
		return tostring(ok) .. "|" .. tostring(err) .. "|" .. #_sent
	end)()`)
	if refused != "false|invalid position value|0" {
		t.Errorf("non-number position = %s, want false|invalid position value|0", refused)
	}

	// OPEN/CLOSE/STOP call the roller's methods and leave its value (the position) alone.
	for _, c := range []struct{ path, want string }{
		{"salon", "ROL1:execute(10, 100, 91)=1/nil,ROL1:execute(10, 0, 91)=2/nil,ROL1:execute(3)=0/nil"},
		{"strych", "ROL2:execute(0, 0)=1/1,ROL2:execute(1, 0)=2/2,ROL2:execute(3)=0/0"},
	} {
		moves := mustExec(t, engine, `(function()
			local out, obj = {}, ExposedObjects:get("t:`+c.path+`").object
			for _, p in ipairs({ "OPEN", "CLOSE", "STOP" }) do
				_sent = {}
				HACommands.handleExposedMessage("vclu/exposed/t/cover/`+c.path+`/set", p)
				table.insert(out, table.concat(_sent, ";") .. "=" .. tostring(obj:getState()) ..
					"/" .. tostring(obj._lastState))
			end
			return table.concat(out, ",")
		end)()`)
		if moves != c.want {
			t.Errorf("%s OPEN/CLOSE/STOP\n got: %s\nwant: %s", c.path, moves, c.want)
		}
	}

	// A VirtualCover: 70 % open in HA is 30 on vCLU's scale; OPEN and CLOSE set the position.
	virtual := mustExec(t, engine, `(function()
		local out = {}
		HACommands.handleExposedMessage("vclu/exposed/t/cover/taras/position/set", "70")
		table.insert(out, _virtual:getPosition())
		HACommands.handleExposedMessage("vclu/exposed/t/cover/taras/set", "CLOSE")
		table.insert(out, _virtual:getPosition())
		HACommands.handleExposedMessage("vclu/exposed/t/cover/taras/set", "OPEN")
		table.insert(out, _virtual:getPosition())
		return table.concat(out, ",")
	end)()`)
	if virtual != "30,100,0" {
		t.Errorf("virtual cover positions = %s, want 30,100,0", virtual)
	}
}

func TestHAExposedCoverPublishes(t *testing.T) {
	engine := NewLuaEngine()
	defer engine.Close()
	if _, err := engine.Execute(haExposedCoverSetup); err != nil {
		t.Fatalf("setup failed: %v", err)
	}

	payload := findDiscovery(t, engine, "t_salon")
	for _, want := range []string{`"position_open":100`, `"position_closed":0`} {
		if !strings.Contains(payload, want) {
			t.Errorf("discovery lacks %s: %s", want, payload)
		}
	}

	// vCLU position 30 (30 % closed) is 70 % open in HA; fully closed is 0 and closed.
	got := mustExec(t, engine, `(function()
		local out = {}
		for _, p in ipairs({ 30, 100, 0 }) do
			_pub = {}
			_roller:updateState(p)
			table.insert(out, _last("vclu/exposed/t/cover/salon/state") .. "/" ..
				_last("vclu/exposed/t/cover/salon/position"))
		end
		_pub = {}
		_virtual:setPosition(30)
		table.insert(out, _last("vclu/exposed/t/cover/taras/state") .. "/" ..
			_last("vclu/exposed/t/cover/taras/position"))
		return table.concat(out, ",")
	end)()`)
	if got != "open/70,closed/0,open/100,open/70" {
		t.Errorf("published state/position = %s, want open/70,closed/0,open/100,open/70", got)
	}

	// A roller without a position: its value is the movement, shown as the state;
	// nothing is a position.
	old := mustExec(t, engine, `(function()
		local out = {}
		for _, m in ipairs({ 2, 0, 1 }) do
			_pub = {}
			_old:updateState(m)
			table.insert(out, _last("vclu/exposed/t/cover/strych/state") .. "/" ..
				_last("vclu/exposed/t/cover/strych/position"))
		end
		return table.concat(out, ",")
	end)()`)
	if old != "closing/nil,stopped/nil,opening/nil" {
		t.Errorf("roller without a position published %s, want closing/nil,stopped/nil,opening/nil", old)
	}
}

// A Grenton roller's movement (feature 0) is not its value, yet HA shows it: opening and
// closing while it moves, open/closed from the position when it stops, also when the
// position did not change.
func TestHAExposedCoverMovement(t *testing.T) {
	engine := NewLuaEngine()
	defer engine.Close()
	if _, err := engine.Execute(haExposedCoverSetup); err != nil {
		t.Fatalf("setup failed: %v", err)
	}

	got := mustExec(t, engine, `(function()
		local out = {}
		local function state() return _last("vclu/exposed/t/cover/salon/state") end
		local function report(movement, pct)
			_pub = {}
			_roller:fromFeature(RemoteCover.FEATURE_STATE, movement)
			if pct then _roller:updateState(_roller:fromFeature(RemoteCover.FEATURE_POSITION, pct)) end
			table.insert(out, state())
		end
		report(0, 50)  -- stopped half open
		report(2, 40)  -- moving down
		report(2, 30)
		report(0)      -- stopped, the position report did not change
		_pub = {}
		HACommands.handleExposedMessage("vclu/exposed/t/cover/salon/set", "OPEN")
		table.insert(out, state()) -- optimistic, before the CLU reports
		report(0)
		return table.concat(out, ",")
	end)()`)
	if got != "open,closing,closing,open,opening,open" {
		t.Errorf("states = %s, want open,closing,closing,open,opening,open", got)
	}

	// Once unexposed, a movement publishes nothing; exposing again does not double it.
	again := mustExec(t, engine, `(function()
		ExposedObjects:get("t:salon"):unexpose()
		_pub = {}
		_roller:fromFeature(RemoteCover.FEATURE_STATE, 1)
		local n1 = #_pub
		expose(_roller, "cover", { name = "Salon", path = "t:salon" })
		_roller:fromFeature(RemoteCover.FEATURE_STATE, 0)
		_pub = {}
		_roller:fromFeature(RemoteCover.FEATURE_STATE, 2)
		local n2 = 0
		for _, p in ipairs(_pub) do if p.topic == "vclu/exposed/t/cover/salon/state" then n2 = n2 + 1 end end
		return n1 .. "|" .. n2
	end)()`)
	if again != "0|1" {
		t.Errorf("unexposed/re-exposed publishes = %s, want 0|1", again)
	}
}

// The scale conversion on its own: HA counts 0 = closed .. 100 = open, vCLU 0 = open.
func TestHAExposedCoverScale(t *testing.T) {
	engine := NewLuaEngine()
	defer engine.Close()
	if _, err := engine.Execute(haExposedCoverSetup); err != nil {
		t.Fatalf("setup failed: %v", err)
	}

	for _, c := range []struct{ payload, want string }{
		{"0", "100"}, {"100", "0"}, {"42", "58"}, {"69.6", "30"}, {"69.4", "31"},
		{"150", "0"}, {"-5", "100"}, {" 42 ", "58"},
		{"abc", "nil"}, {"", "nil"}, {"nan", "nil"}, {"inf", "nil"}, {"1e999", "nil"},
	} {
		got := mustExec(t, engine, `tostring(HAStatePublisher.coverPositionFromHA("`+c.payload+`"))`)
		if got != c.want {
			t.Errorf("coverPositionFromHA(%q) = %s, want %s", c.payload, got, c.want)
		}
	}

	got := mustExec(t, engine, `(function()
		_virtual:setPosition(30)
		_roller:updateState(0)
		_plain = { _lastState = 25, get = function(s) return s._lastState end,
			set = function(s, _, v) s._lastState = v end, onChange = function() return function() end end }
		expose(_plain, "cover", { path = "t:plain" })
		_broken = { getPosition = function() error("boom") end, onChange = function() return function() end end }
		expose(_broken, "cover", { path = "t:broken" })
		local out = {}
		for _, p in ipairs({ "t:taras", "t:salon", "t:strych", "t:plain", "t:broken" }) do
			table.insert(out, tostring(HAStatePublisher.exposedCoverPosition(ExposedObjects:get(p))))
		end
		return table.concat(out, ",")
	end)()`)
	// fully open roller (vCLU 0) is 100 in HA; no position and a failing getPosition give nil
	if got != "70,100,nil,75,nil" {
		t.Errorf("exposedCoverPosition = %s, want 70,100,nil,75,nil", got)
	}
}

// A position sent from HA comes back from the roller's report as the same number.
func TestHAExposedCoverRoundTrip(t *testing.T) {
	engine := NewLuaEngine()
	defer engine.Close()
	if _, err := engine.Execute(haExposedCoverSetup); err != nil {
		t.Fatalf("setup failed: %v", err)
	}

	got := mustExec(t, engine, `(function()
		local out = {}
		for _, ha in ipairs({ 42, 0, 100, 7 }) do
			_sent, _pub = {}, {}
			HACommands.handleExposedMessage("vclu/exposed/t/cover/salon/position/set", tostring(ha))
			-- the CLU reports feature 7 with the percentage it was sent
			local grenton = tonumber(_sent[1]:match("execute%(10, (%d+)"))
			_roller:updateState(_roller:fromFeature(RemoteCover.FEATURE_POSITION, grenton))
			table.insert(out, ha .. ">" .. grenton .. ">" .. _last("vclu/exposed/t/cover/salon/position"))
		end
		return table.concat(out, ",")
	end)()`)
	if got != "42>42>42,0>0>0,100>100>100,7>7>7" {
		t.Errorf("HA > Grenton > HA = %s", got)
	}
}

// A roller without a position only moves: HA gets no slider and a position is refused.
func TestHAExposedCoverWithoutPosition(t *testing.T) {
	engine := NewLuaEngine()
	defer engine.Close()
	if _, err := engine.Execute(haExposedCoverSetup); err != nil {
		t.Fatalf("setup failed: %v", err)
	}

	payload := findDiscovery(t, engine, "t_strych")
	if payload == "" || strings.Contains(payload, "position") {
		t.Errorf("discovery of a roller without a position = %s", payload)
	}
	if got := mustExec(t, engine, `(function()
		for _, p in ipairs(_pub) do
			if p.topic == "vclu/exposed/t/cover/strych/position" then return "[" .. p.payload .. "]" end
		end
		return "none"
	end)()`); got != "[]" {
		t.Errorf("retained position of a roller without one not cleared: %s", got)
	}
	if got := findDiscovery(t, engine, "t_salon"); !strings.Contains(got, "set_position_topic") {
		t.Errorf("roller with a position lost its slider: %s", got)
	}

	refused := mustExec(t, engine, `(function()
		_sent = {}
		local ok, err = HACommands.handleExposedMessage("vclu/exposed/t/cover/strych/position/set", "40")
		return tostring(ok) .. "|" .. tostring(err) .. "|" .. #_sent
	end)()`)
	if refused != "false|cover has no position|0" {
		t.Errorf("position on a roller without one = %s, want false|cover has no position|0", refused)
	}
}

// An object without cover methods is written through setValue on vCLU's scale;
// one with only execute() still stops.
func TestHAExposedCoverFallbacks(t *testing.T) {
	engine := NewLuaEngine()
	defer engine.Close()
	if _, err := engine.Execute(haExposedCoverSetup); err != nil {
		t.Fatalf("setup failed: %v", err)
	}

	got := mustExec(t, engine, `(function()
		_plain = { _lastState = 50, get = function(s) return s._lastState end,
			set = function(s, _, v) s._lastState = v end,
			execute = function(s, ...) table.insert(_sent, "execute(" .. table.concat({...}, ", ") .. ")") end,
			onChange = function() return function() end end }
		expose(_plain, "cover", { path = "t:plain" })
		local out, t = {}, "vclu/exposed/t/cover/plain/"
		for _, c in ipairs({ { "position/set", "70" }, { "set", "OPEN" }, { "set", "CLOSE" } }) do
			HACommands.handleExposedMessage(t .. c[1], c[2])
			table.insert(out, tostring(_plain._lastState))
		end
		_sent = {}
		HACommands.handleExposedMessage(t .. "set", "STOP")
		table.insert(out, table.concat(_sent, ";"))
		return table.concat(out, ",")
	end)()`)
	if got != "30,0,100,execute(3)" {
		t.Errorf("fallbacks = %s, want 30,0,100,execute(3)", got)
	}
}

// A cover set to Readonly for MQTT refuses commands before any method runs.
func TestHAExposedCoverReadonly(t *testing.T) {
	engine := NewLuaEngine()
	defer engine.Close()
	if _, err := engine.Execute(haExposedCoverSetup); err != nil {
		t.Fatalf("setup failed: %v", err)
	}

	got := mustExec(t, engine, `(function()
		AccessControl.set("mqtt", "t:salon", "readonly")
		_sent = {}
		local out = {}
		for _, c in ipairs({ { "position/set", "40" }, { "set", "OPEN" }, { "set", "STOP" } }) do
			local ok = HACommands.handleExposedMessage("vclu/exposed/t/cover/salon/" .. c[1], c[2])
			table.insert(out, tostring(ok))
		end
		return table.concat(out, ",") .. "|" .. #_sent .. "|" .. tostring(_roller:getState())
	end)()`)
	if got != "false,false,false|0|nil" {
		t.Errorf("readonly cover = %s, want false,false,false|0|nil", got)
	}
}
