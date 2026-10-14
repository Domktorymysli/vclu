package lua

import "testing"

// A roller from the OM import (the registry path, not expose()) is announced to
// Home Assistant with position_open 100, position_closed 0. RemoteCover documents
// 0 = open, 100 = closed, so a position command reaches setPosition inverted and
// clamped; OPEN/CLOSE/STOP use the cover's own methods.
func TestRegistryCoverPosition(t *testing.T) {
	engine := NewLuaEngine()
	defer engine.Close()

	setup := `
		_sent = {}
		local clu = { executeNoReply = function(s, i, cmd) table.insert(_sent, cmd) end }
		_roller = RemoteCover:new(clu, "ROL1", "roller")
		registerObject("CLU1.ROL1", _roller)
	`
	if _, err := engine.Execute(setup); err != nil {
		t.Fatalf("setup failed: %v", err)
	}

	got := mustExec(t, engine, `(function()
		local t = "vclu/clu1/cover/rol1/position/set"
		for _, p in ipairs({ "30", "0", "100", "150", "-5" }) do
			local ok, err = HACommands.handleMessage(t, p)
			if not ok then table.insert(_sent, "refused " .. tostring(err)) end
		end
		return table.concat(_sent, ";")
	end)()`)
	want := "ROL1:execute(10, 70, 91);ROL1:execute(10, 100, 91);ROL1:execute(10, 0, 91);" +
		"ROL1:execute(10, 0, 91);ROL1:execute(10, 100, 91)"
	if got != want {
		t.Errorf("position commands sent\n got: %s\nwant: %s", got, want)
	}

	// OPEN/CLOSE/STOP go through open/close/stop, which also update the state.
	moves := mustExec(t, engine, `(function()
		local out, t = {}, "vclu/clu1/cover/rol1/set"
		for _, p in ipairs({ "OPEN", "CLOSE", "STOP" }) do
			_sent = {}
			HACommands.handleMessage(t, p)
			table.insert(out, table.concat(_sent, ";") .. "=" .. tostring(_roller._lastState))
		end
		return table.concat(out, ",")
	end)()`)
	if moves != "ROL1:execute(0, 0)=1,ROL1:execute(1, 0)=2,ROL1:execute(3)=0" {
		t.Errorf("OPEN/CLOSE/STOP = %s", moves)
	}

	// A non-number position is refused and sends nothing.
	refused := mustExec(t, engine, `(function()
		_sent = {}
		local ok, err = HACommands.handleMessage("vclu/clu1/cover/rol1/position/set", "abc")
		return tostring(ok) .. "|" .. tostring(err) .. "|" .. #_sent
	end)()`)
	if refused != "false|invalid position value|0" {
		t.Errorf("non-number position = %s, want false|invalid position value|0", refused)
	}
}
