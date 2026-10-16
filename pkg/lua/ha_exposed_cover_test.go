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
		{"salon", "ROL1:execute(0, 0)=1/nil,ROL1:execute(1, 0)=2/nil,ROL1:execute(3)=0/nil"},
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

	// A roller without a position: its value is the movement, nothing is a position.
	old := mustExec(t, engine, `(function()
		_pub = {}
		_old:updateState(2)
		return _last("vclu/exposed/t/cover/strych/state") .. "/" ..
			_last("vclu/exposed/t/cover/strych/position")
	end)()`)
	if old != "nil/nil" {
		t.Errorf("roller without a position published %s, want nil/nil", old)
	}
}
