package lua

import "testing"

// A roller from the OM import (the registry path, not expose()) is announced to
// Home Assistant with position_open 100, position_closed 0. vCLU counts 0 = open,
// Grenton's SetPosition 0 = closed (Object Manager device-interfaces), so a position
// from HA reaches the roller unchanged, clamped and rounded.
func TestRegistryCoverPosition(t *testing.T) {
	engine := NewLuaEngine()
	defer engine.Close()

	setup := `
		_sent = {}
		local clu = { executeNoReply = function(s, i, cmd) table.insert(_sent, cmd) end }
		_roller = RemoteCover:new(clu, "ROL1", "roller")
		_roller._hasPosition = true -- the OM import marks class 24
		registerObject("CLU1.ROL1", _roller)
	`
	if _, err := engine.Execute(setup); err != nil {
		t.Fatalf("setup failed: %v", err)
	}

	got := mustExec(t, engine, `(function()
		local t = "vclu/clu1/cover/rol1/position/set"
		for _, p in ipairs({ "30", "0", "100", "150", "-5", "69.6" }) do
			local ok, err = HACommands.handleMessage(t, p)
			if not ok then table.insert(_sent, "refused " .. tostring(err)) end
		end
		return table.concat(_sent, ";")
	end)()`)
	want := "ROL1:execute(10, 30, 91);ROL1:execute(10, 0, 91);ROL1:execute(10, 100, 91);" +
		"ROL1:execute(10, 100, 91);ROL1:execute(10, 0, 91);ROL1:execute(10, 70, 91)"
	if got != want {
		t.Errorf("position commands sent\n got: %s\nwant: %s", got, want)
	}

	// OPEN/CLOSE go to SetPosition 100 % / 0 %, STOP to Stop; the movement is kept
	// aside, the value (the position) stays as it was.
	moves := mustExec(t, engine, `(function()
		local out, t = {}, "vclu/clu1/cover/rol1/set"
		for _, p in ipairs({ "OPEN", "CLOSE", "STOP" }) do
			_sent = {}
			HACommands.handleMessage(t, p)
			table.insert(out, table.concat(_sent, ";") .. "=" .. tostring(_roller:getState()) ..
				"/" .. tostring(_roller._lastState))
		end
		return table.concat(out, ",")
	end)()`)
	if moves != "ROL1:execute(10, 100, 91)=1/nil,ROL1:execute(10, 0, 91)=2/nil,ROL1:execute(3)=0/nil" {
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

// A ROLLER_SHUTTER is subscribed to feature 0 (State) and feature 7 (Position). The
// position becomes the value on vCLU's scale, the movement is kept aside, and Home
// Assistant gets the position on its own scale with open/closed derived from it.
func TestRegistryCoverReportsPosition(t *testing.T) {
	engine := NewLuaEngine()
	defer engine.Close()

	setup := `
		_scripts = {}
		__vclu_ip = "192.168.0.100"
		__go_send_command = function(ip, script, delay)
			table.insert(_scripts, script)
			local sid = script:match("clientRegister%('.-', %d+, (%d+),")
			return "clientReport:" .. sid .. ":{0,30,1}", nil
		end
		local clu = { address = "192.168.0.3", executeNoReply = function() end }
		_roller = RemoteCover:new(clu, "ROL1", "salon")
		_roller._hasPosition = true -- the OM import marks class 24
		registerObject("CLU1.ROL1", _roller)
		_old = RemoteCover:new(clu, "ROL2", "strych")
		registerObject("CLU1.ROL2", _old)

		_published = {}
		HAStatePublisher.setup({
			publish = function(self, topic, payload)
				if topic:match("/cover/") then _published[topic] = payload end
				return true
			end,
		})

		RemoteSubscription.config.enabled = true
		RemoteSubscription.config.whitelist = { ["CLU1.ROL1"] = true, ["CLU1.ROL2"] = true }
		RemoteSubscription.config.batchSize = 4
		RemoteSubscription.config.batchDelay = 0
		RemoteSubscription.activeSubscriptions = {}
	`
	if _, err := engine.Execute(setup); err != nil {
		t.Fatalf("setup failed: %v", err)
	}

	subs := mustExec(t, engine, `(function()
		RemoteSubscription.autoSubscribe()
		local s = table.concat(_scripts, "|")
		return s:match("{{.*}}")
	end)()`)
	// pairs() order over the whitelist is not fixed: check each object's entries
	for _, want := range []string{"{ROL1, 0}, {ROL1, 7}", "{ROL2, 0}"} {
		if !contains(subs, want) {
			t.Errorf("subscription %s lacks %s", subs, want)
		}
	}
	if contains(subs, "{ROL2, 7}") {
		t.Errorf("ROLLER_SH subscribed to a position: %s", subs)
	}

	// Drive reports directly in a known entry order: position 30% open while moving down.
	got := mustExec(t, engine, `(function()
		RemoteSubscription.activeSubscriptions = {}
		RemoteSubscription.activeSubscriptions["192.168.0.3:5"] = {
			cluSerial = "CLU1",
			objects = {
				{ objId = "ROL1", featureIndex = 0 }, { objId = "ROL1", featureIndex = 7 },
				{ objId = "ROL2", featureIndex = 0 },
			},
		}
		local out = {}
		local function report(values)
			_published = {}
			RemoteSubscription.updateRegistry("192.168.0.3", 5, values)
			table.insert(out, string.format("%s/%s %s/%s old=%s",
				tostring(_roller:getPosition()), tostring(_roller:getState()),
				tostring(_published["vclu/clu1/cover/rol1/state"]),
				tostring(_published["vclu/clu1/cover/rol1/position"]),
				tostring(_old:getState())))
		end
		report({ 2, 40, 1 })
		report({ 0, 0, 0 })
		report({ 0, 100, 0 })
		return table.concat(out, ";")
	end)()`)
	want := "40/2 open/40 old=1;0/0 closed/0 old=0;100/0 open/100 old=0"
	if got != want {
		t.Errorf("reports\n got: %s\nwant: %s", got, want)
	}
}

// A roller with a position opens and closes through SetPosition: MoveUp/MoveDown stop a
// moving roller instead (Object Manager device-interfaces), so a second OPEN would stop it.
// With a time, or without a position, MoveUp/MoveDown stay.
func TestRemoteCoverOpenClose(t *testing.T) {
	engine := NewLuaEngine()
	defer engine.Close()

	got := mustExec(t, engine, `(function()
		local sent = {}
		local clu = { executeNoReply = function(s, i, cmd) table.insert(sent, cmd) end }
		local r = RemoteCover:new(clu, "ROL1", "salon")
		r._hasPosition = true
		local old = RemoteCover:new(clu, "ROL2", "strych")
		r:open(); r:open(); r:close(); r:open(5000); r:close(0)
		old:open(); old:close(3000)
		return table.concat(sent, ";")
	end)()`)
	want := "ROL1:execute(10, 100, 91);ROL1:execute(10, 100, 91);ROL1:execute(10, 0, 91);" +
		"ROL1:execute(0, 5000);ROL1:execute(10, 0, 91);ROL2:execute(0, 0);ROL2:execute(1, 3000)"
	if got != want {
		t.Errorf("commands\n got: %s\nwant: %s", got, want)
	}
}
