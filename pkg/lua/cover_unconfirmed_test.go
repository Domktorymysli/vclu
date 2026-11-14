package lua

import "testing"

// A roller already at its end does not move and its CLU reports nothing, so open() there
// assumes no movement; a movement the CLU never confirms ends after a timeout (#224).
// Timers are kept in T and fired by hand.
const coverTimers = `
	T = { list = {}, id = 0 }
	__go_setTimeout = function(fn, ms) T.id = T.id + 1; T.list[T.id] = { fn = fn, ms = ms }; return T.id end
	__go_clearTimer = function(id) T.list[id] = nil end
	function pending()
		local n = 0
		for _, tm in pairs(T.list) do
			if tm.ms ~= RemoteCover.UNCONFIRMED_MS then error("timeout " .. tm.ms) end
			n = n + 1
		end
		return n
	end
	function fire()
		local due = T.list; T.list = {}
		for _, tm in pairs(due) do tm.fn() end
	end
	local clu = { address = "192.168.0.3", executeNoReply = function() end }
	_roller = RemoteCover:new(clu, "ROL1", "salon")
	_roller._hasPosition = true
	registerObject("CLU1.ROL1", _roller)
	_old = RemoteCover:new(clu, "ROL2", "strych")
	registerObject("CLU1.ROL2", _old)
	MOVES = {}
	_roller:onEvent("OnMovementChange", function(m) table.insert(MOVES, tostring(m)) end)
	function moves() local s = table.concat(MOVES, ","); MOVES = {}; return s end
	-- a CLU report of feature 0 (movement) and 7 (position) of ROL1, feature 0 of ROL2
	RemoteSubscription.activeSubscriptions = {}
	RemoteSubscription.activeSubscriptions["192.168.0.3:5"] = {
		cluSerial = "CLU1",
		objects = {
			{ objId = "ROL1", featureIndex = 0 }, { objId = "ROL1", featureIndex = 7 },
			{ objId = "ROL2", featureIndex = 0 },
		},
	}
	function report(movement, position, old)
		RemoteSubscription.updateRegistry("192.168.0.3", 5, { movement, position, old })
	end
	function st(r) return tostring(r:getState()) .. "/" .. tostring(r:getPosition()) end
`

func coverEngine(t *testing.T) *LuaEngine {
	t.Helper()
	engine := NewLuaEngine()
	t.Cleanup(engine.Close)
	if _, err := engine.Execute(coverTimers); err != nil {
		t.Fatalf("setup failed: %v", err)
	}
	return engine
}

func TestRemoteCoverAtEndDoesNotMove(t *testing.T) {
	engine := coverEngine(t)
	got := mustExec(t, engine, `(function()
		local out = {}
		report(0, 100, 0); moves()
		_roller:open()
		table.insert(out, st(_roller) .. " " .. moves() .. " " .. pending())
		_roller:open(3000)
		table.insert(out, st(_roller) .. " " .. moves() .. " " .. pending())
		report(0, 0, 0); moves()
		_roller:close()
		table.insert(out, st(_roller) .. " " .. moves() .. " " .. pending())
		return table.concat(out, ";")
	end)()`)
	if want := "0/100  0;0/100  0;0/0  0"; got != want {
		t.Errorf("at the end\n got: %s\nwant: %s", got, want)
	}
}

func TestRemoteCoverMovementConfirmedOrTimedOut(t *testing.T) {
	engine := coverEngine(t)
	got := mustExec(t, engine, `(function()
		local out = {}
		report(0, 40, 0); moves()
		-- confirmed: the CLU reports the movement, then its end
		_roller:open()
		table.insert(out, st(_roller) .. " " .. moves() .. " " .. pending())
		report(1, 60, 0)
		table.insert(out, st(_roller) .. " " .. moves() .. " " .. pending())
		report(0, 100, 0)
		table.insert(out, st(_roller) .. " " .. moves())
		-- never confirmed: the timeout ends it
		_roller:close()
		table.insert(out, st(_roller) .. " " .. moves() .. " " .. pending())
		fire()
		table.insert(out, st(_roller) .. " " .. moves() .. " " .. pending())
		-- stop() leaves no timeout behind; a second open() keeps one
		_roller:close(); _roller:stop()
		table.insert(out, st(_roller) .. " " .. moves() .. " " .. pending())
		_roller:close(); _roller:close()
		table.insert(out, moves() .. " " .. pending())
		return table.concat(out, ";")
	end)()`)
	want := "1/40 1 1;1/60  0;0/100 0;2/100 2 1;0/100 0 0;0/100 2,0 0;2 1"
	if got != want {
		t.Errorf("movement\n got: %s\nwant: %s", got, want)
	}
}

// Without a position the movement is the value: the same timeout ends it.
func TestRemoteCoverWithoutPositionTimesOut(t *testing.T) {
	engine := coverEngine(t)
	got := mustExec(t, engine, `(function()
		local out = {}
		_old:open()
		table.insert(out, tostring(_old:getState()) .. " " .. pending())
		fire()
		table.insert(out, tostring(_old:getState()) .. " " .. pending())
		_old:close()
		report(0, 40, 2)
		table.insert(out, tostring(_old:getState()) .. " " .. pending())
		return table.concat(out, ";")
	end)()`)
	if want := "1 1;0 0;2 0"; got != want {
		t.Errorf("without a position\n got: %s\nwant: %s", got, want)
	}
}

// Home Assistant sees an open roller stay open after open(), and a timed-out movement end.
func TestHACoverAtEndStaysOpen(t *testing.T) {
	engine := coverEngine(t)
	got := mustExec(t, engine, `(function()
		__vclu_ip = "192.168.0.100"
		_published = {}
		HAStatePublisher.setup({
			publish = function(self, topic, payload)
				if topic:match("/cover/") then _published[topic] = payload end
				return true
			end,
		})
		expose(_roller, "cover", {})
		local topic = "vclu/exposed/vclu/cover/clu1_rol1/state"
		local out = {}
		report(0, 100, 0)
		table.insert(out, tostring(_published[topic]))
		_published = {}
		_roller:open()
		table.insert(out, tostring(_published[topic]))
		report(0, 40, 0)
		_roller:open()
		table.insert(out, tostring(_published[topic]))
		fire()
		table.insert(out, tostring(_published[topic]))
		return table.concat(out, ";")
	end)()`)
	if want := "open;nil;opening;open"; got != want {
		t.Errorf("Home Assistant\n got: %s\nwant: %s", got, want)
	}
}
