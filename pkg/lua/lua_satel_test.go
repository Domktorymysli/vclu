package lua

import (
	"testing"

	"github.com/Domktorymysli/vclu/pkg/satel"
)

// newSatelTestEngine creates an engine with Lua-side stubs for the
// __go_satel_* bridge (no Go manager needed) and runs Satel.setup().
func newSatelTestEngine(t *testing.T) *LuaEngine {
	t.Helper()
	engine := NewLuaEngine()
	t.Cleanup(func() { engine.Close() })

	_, err := engine.Execute(`
		__satel_calls = {}
		__satel_events = {}

		__go_satel_config = function()
			return '{"zones":[1,2],"partitions":[1],"outputs":[5],"tempZones":[7],"allowDisarm":false,"armMode":1}'
		end
		__go_satel_status = function()
			return '{"connected":true,"panelType":"INTEGRA 128"}'
		end
		__go_satel_get_zone = function(n)
			return '{"number":' .. n .. ',"violated":false,"tamper":false,"alarm":false,"bypass":false}'
		end
		__go_satel_get_partition = function(n)
			return '{"number":' .. n .. ',"armed":false,"alarm":false}'
		end
		__go_satel_get_output = function(n)
			return '{"number":' .. n .. ',"active":false}'
		end
		__go_satel_get_temp = function(n) return nil end
		__go_satel_arm = function(n, mode)
			table.insert(__satel_calls, {op="arm", n=n, mode=mode})
			return true
		end
		__go_satel_force_arm = function(n, mode)
			table.insert(__satel_calls, {op="force_arm", n=n, mode=mode})
			return true
		end
		__go_satel_disarm = function(n)
			table.insert(__satel_calls, {op="disarm", n=n})
			return false, "satel: disarm/clear alarm not allowed by configuration"
		end
		__go_satel_clear_alarm = function(n)
			return false, "satel: disarm/clear alarm not allowed by configuration"
		end
		__go_satel_set_output = function(n, on)
			table.insert(__satel_calls, {op="set_output", n=n, on=on})
			return true
		end
		__go_satel_poll_event = function()
			if #__satel_events > 0 then return table.remove(__satel_events, 1) end
			return nil
		end

		Satel._config = nil
		Satel.setup()
	`)
	if err != nil {
		t.Fatalf("satel stub setup failed: %v", err)
	}
	return engine
}

// expectLua runs a single Lua expression and asserts its string result.
func expectLua(t *testing.T, engine *LuaEngine, expr, want string) {
	t.Helper()
	got, err := engine.Execute(expr)
	if err != nil {
		t.Fatalf("Execute(%q) failed: %v", expr, err)
	}
	if got != want {
		t.Errorf("Execute(%q) = %q, want %q", expr, got, want)
	}
}

func TestSatelSetupCreatesObjects(t *testing.T) {
	engine := newSatelTestEngine(t)

	expectLua(t, engine, `_registry["satel.zone.1"] ~= nil`, "true")
	expectLua(t, engine, `_registry["satel.zone.2"] ~= nil`, "true")
	expectLua(t, engine, `_registry["satel.zone.1"]._type`, "DIN")
	expectLua(t, engine, `_registry["satel.output.5"]._type`, "DOUT")
	expectLua(t, engine, `_registry["satel.partition.1"]._type`, "DOUT")
	expectLua(t, engine, `_registry["satel.partition.1.alarm"]._type`, "DIN")
	expectLua(t, engine, `_registry["satel.temp.7"]._type`, "ONE_WIRE")

	// Accessors return the same objects
	expectLua(t, engine, `Satel.zone(1) == _registry["satel.zone.1"]`, "true")
	expectLua(t, engine, `Satel.partition(1) == _registry["satel.partition.1"]`, "true")

	// setup() is idempotent
	expectLua(t, engine, `(function() local z = Satel.zone(1); Satel.setup(); return Satel.zone(1) == z end)()`, "true")

	// Status passthrough
	expectLua(t, engine, `tostring(Satel.isConnected())`, "true")
}

func TestSatelZoneEventUpdatesObject(t *testing.T) {
	engine := newSatelTestEngine(t)

	_, err := engine.Execute(`
		__zone_changes = {}
		Satel.zone(1):onChange(function(newVal) table.insert(__zone_changes, newVal) end)
		__bus_events = 0
		EventBus:getShared():on("satel.zone.1.OnChange", function() __bus_events = __bus_events + 1 end)

		table.insert(__satel_events,
			'{"type":"zone","number":1,"zone":{"number":1,"violated":true,"tamper":false,"alarm":false,"bypass":false}}')
		Satel.pollEvents()
	`)
	if err != nil {
		t.Fatalf("event dispatch failed: %v", err)
	}

	expectLua(t, engine, `Satel.zone(1):getValue()`, "1")
	expectLua(t, engine, `Satel.zone(1):isActive()`, "true")
	expectLua(t, engine, `#__zone_changes`, "1")
	expectLua(t, engine, `__bus_events`, "1")

	// Alarm flag + OnAlarm event
	_, err = engine.Execute(`
		__alarm_fired = false
		Satel.zone(1):onAlarm(function() __alarm_fired = true end)
		table.insert(__satel_events,
			'{"type":"zone","number":1,"zone":{"number":1,"violated":true,"tamper":true,"alarm":true,"bypass":false}}')
		Satel.pollEvents()
	`)
	if err != nil {
		t.Fatalf("alarm event failed: %v", err)
	}
	expectLua(t, engine, `tostring(Satel.zone(1):isAlarm())`, "true")
	expectLua(t, engine, `tostring(Satel.zone(1):isTamper())`, "true")
	expectLua(t, engine, `tostring(__alarm_fired)`, "true")
}

func TestSatelOutputControlOptimistic(t *testing.T) {
	engine := newSatelTestEngine(t)

	_, err := engine.Execute(`Satel.output(5):switchOn()`)
	if err != nil {
		t.Fatalf("switchOn failed: %v", err)
	}
	expectLua(t, engine, `__satel_calls[1].op`, "set_output")
	expectLua(t, engine, `tostring(__satel_calls[1].on)`, "true")
	expectLua(t, engine, `Satel.output(5):getValue()`, "1") // optimistic

	// Poll confirmation flips it back if the panel disagrees
	_, err = engine.Execute(`
		table.insert(__satel_events, '{"type":"output","number":5,"output":{"number":5,"active":false}}')
		Satel.pollEvents()
	`)
	if err != nil {
		t.Fatalf("output event failed: %v", err)
	}
	expectLua(t, engine, `Satel.output(5):getValue()`, "0")
}

func TestSatelPartitionArmDisarm(t *testing.T) {
	engine := newSatelTestEngine(t)

	// Arm uses the configured default mode (1 from config stub), NOT optimistic.
	_, err := engine.Execute(`Satel.partition(1):arm()`)
	if err != nil {
		t.Fatalf("arm failed: %v", err)
	}
	expectLua(t, engine, `__satel_calls[1].op`, "arm")
	expectLua(t, engine, `__satel_calls[1].mode`, "1")
	expectLua(t, engine, `Satel.partition(1):getValue()`, "0") // waits for panel confirmation

	// Panel confirms arming; companion alarm sensor follows the alarm flag.
	_, err = engine.Execute(`
		table.insert(__satel_events,
			'{"type":"partition","number":1,"partition":{"number":1,"armed":true,"alarm":false}}')
		Satel.pollEvents()
	`)
	if err != nil {
		t.Fatalf("partition event failed: %v", err)
	}
	expectLua(t, engine, `Satel.partition(1):getValue()`, "1")
	expectLua(t, engine, `tostring(Satel.partition(1):isArmed())`, "true")

	// Disarm is blocked by config (stub mirrors the Go-side guard).
	expectLua(t, engine, `(function() local ok = Satel.partition(1):disarm(); return tostring(ok) end)()`, "false")

	// Alarm event raises the companion sensor.
	_, err = engine.Execute(`
		table.insert(__satel_events,
			'{"type":"partition","number":1,"partition":{"number":1,"armed":true,"alarm":true}}')
		Satel.pollEvents()
	`)
	if err != nil {
		t.Fatalf("alarm event failed: %v", err)
	}
	expectLua(t, engine, `Satel.partitionAlarm(1):getValue()`, "1")
	expectLua(t, engine, `tostring(Satel.partition(1):isAlarm())`, "true")
}

func TestSatelNameAndTemperatureEvents(t *testing.T) {
	engine := newSatelTestEngine(t)

	_, err := engine.Execute(`
		table.insert(__satel_events, '{"type":"name","kind":"zone","number":1,"message":"Czujka Salon"}')
		table.insert(__satel_events, '{"type":"temperature","number":7,"temp":21.5}')
		Satel.pollEvents()
	`)
	if err != nil {
		t.Fatalf("events failed: %v", err)
	}
	expectLua(t, engine, `Satel.zone(1)._name`, "Czujka Salon")
	expectLua(t, engine, `Satel.temp(7):getValue()`, "21.5")
}

// A panel name fills an auto-exposed object, but a name the user gave in
// expose() stays (names come again on every reconnect).
func TestSatelPanelNameKeepsUserName(t *testing.T) {
	engine := newSatelTestEngine(t)
	if _, err := engine.Execute(`
		Satel.zone(1):expose({ path = "satel.zone.1", name = "satel.zone.1" })
		Satel.zone(2):expose({ path = "satel.zone.2", name = "Hol" })
		table.insert(__satel_events, '{"type":"name","kind":"zone","number":1,"message":"PIR HOL"}')
		table.insert(__satel_events, '{"type":"name","kind":"zone","number":2,"message":"PIR WIATROLAP"}')
		Satel.pollEvents()
	`); err != nil {
		t.Fatalf("events failed: %v", err)
	}
	expectLua(t, engine, `Satel.zone(1):getExposeHandle().options.name`, "PIR HOL")
	expectLua(t, engine, `Satel.zone(2):getExposeHandle().options.name`, "Hol")
}

// TestSatelManagerBridge wires a real SatelManager (panel not started, so no
// network I/O) and verifies the Go bridge: object creation via SetSatelManager,
// JSON shapes of the Go getters, and the disconnected-control guard.
func TestSatelManagerBridge(t *testing.T) {
	engine := NewLuaEngine()
	defer engine.Close()

	panel := satel.New(satel.Config{Host: "127.0.0.1", Port: 1})
	sm := NewSatelManager(panel, SatelOptions{
		Zones:      []int{1, 3},
		Partitions: []int{1},
		Outputs:    []int{2},
		ArmMode:    0,
	})
	engine.SetSatelManager(sm)

	// SetSatelManager must have run Satel.setup()
	expectLua(t, engine, `_registry["satel.zone.1"] ~= nil`, "true")
	expectLua(t, engine, `_registry["satel.zone.3"] ~= nil`, "true")
	expectLua(t, engine, `_registry["satel.partition.1"] ~= nil`, "true")
	expectLua(t, engine, `_registry["satel.output.2"] ~= nil`, "true")

	// Go-side getters decode cleanly in Lua
	expectLua(t, engine, `tostring(Satel.isConnected())`, "false")
	expectLua(t, engine, `Satel.getZone(1).number`, "1")
	expectLua(t, engine, `tostring(Satel.getZone(1).violated)`, "false")
	expectLua(t, engine, `Satel.getPartition(1).number`, "1")
	expectLua(t, engine, `tostring(Satel.getOutput(2).active)`, "false")
	expectLua(t, engine, `tostring(Satel.getTemperature(7))`, "nil")

	// Control while disconnected is rejected with an error message
	expectLua(t, engine, `(function() local ok, err = Satel.armPartition(1); return tostring(ok) end)()`, "false")
	expectLua(t, engine, `(function() local ok, err = Satel.setOutput(2, true); return tostring(ok) end)()`, "false")

	// Disarm guard comes from Go (AllowDisarm=false in panel config)
	expectLua(t, engine,
		`(function() local ok, err = Satel.disarmPartition(1); return tostring(ok) .. "|" .. tostring(err ~= nil) end)()`,
		"false|true")
}

func TestSatelConnectionEventRefreshesState(t *testing.T) {
	engine := newSatelTestEngine(t)

	// Cache says zone 2 is violated; connection event must trigger refreshAll.
	_, err := engine.Execute(`
		__go_satel_get_zone = function(n)
			local violated = (n == 2) and "true" or "false"
			return '{"number":' .. n .. ',"violated":' .. violated .. ',"tamper":false,"alarm":false,"bypass":false}'
		end
		__conn_event = nil
		EventBus:getShared():on("satel.connection", function(data) __conn_event = data end)
		table.insert(__satel_events, '{"type":"connection","connected":true,"panelType":"INTEGRA 128"}')
		Satel.pollEvents()
	`)
	if err != nil {
		t.Fatalf("connection event failed: %v", err)
	}
	expectLua(t, engine, `Satel.zone(2):getValue()`, "1")
	expectLua(t, engine, `Satel.zone(1):getValue()`, "0")
	expectLua(t, engine, `tostring(__conn_event.connected)`, "true")
	expectLua(t, engine, `Satel.panelType`, "INTEGRA 128")
}
