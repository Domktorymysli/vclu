package lua

import (
	"strings"
	"testing"
)

// TestHADiscoveryArea verifies that an object exposed with an `area` ends up in a
// per-room HA device carrying device.suggested_area (HA assigns areas per-device,
// not per-entity), and that the top-level entity suggested_area is NOT used.
func TestHADiscoveryArea(t *testing.T) {
	engine := NewLuaEngine()
	defer engine.Close()

	setup := `
		_pub = {}
		_fake = {
			publish = function(self, topic, payload, qos, retain)
				table.insert(_pub, { topic = topic, payload = payload })
			end,
			subscribe = function() end, unsubscribe = function() end,
		}
		HAStatePublisher.setup(_fake)

		local lamp = {
			_type = "DOUT", _lastState = 0,
			get = function(s) return s._lastState end,
			set = function() end,
			onChange = function(s, cb) return function() end end,
		}
		expose(lamp, "light", { name = "Kanapa", area = "Salon", path = "t:lamp" })
	`
	if _, err := engine.Execute(setup); err != nil {
		t.Fatalf("setup failed: %v", err)
	}

	payload := mustExec(t, engine, `(function()
		for _, p in ipairs(_pub) do
			if p.topic:find("t_lamp", 1, true) and p.topic:find("/config", 1, true) then
				return p.payload
			end
		end
		return ""
	end)()`)
	if payload == "" {
		t.Fatal("no discovery config published for the lamp")
	}
	for _, want := range []string{`"suggested_area":"Salon"`, `vclu_salon`} {
		if !strings.Contains(payload, want) {
			t.Errorf("discovery payload missing %q: %s", want, payload)
		}
	}
	// suggested_area must be inside the device block, not a stray top-level key
	// (a quick proxy: the only suggested_area occurrence sits after "device").
	if di := strings.Index(payload, `"device"`); di < 0 || strings.Index(payload, `"suggested_area"`) < di {
		t.Errorf("suggested_area should live in the device block: %s", payload)
	}
}

// TestHADiscoveryHiddenClears verifies that an object which becomes Hidden via
// AccessControl gets its HA discovery actively CLEARED (empty retained), even when
// it was manually expose()'d. Previously hidden objects were only skipped, leaving
// a stale (and controllable) retained config in HA.
func TestHADiscoveryHiddenClears(t *testing.T) {
	engine := NewLuaEngine()
	defer engine.Close()

	setup := `
		_pub = {}
		_fake = {
			publish = function(self, topic, payload, qos, retain)
				table.insert(_pub, { topic = topic, payload = payload })
			end,
			subscribe = function() end, unsubscribe = function() end,
		}
		HAStatePublisher.setup(_fake)

		local sw = {
			_type = "DOUT", _lastState = 1,
			get = function(s) return s._lastState end,
			set = function() end,
			onChange = function(s, cb) return function() end end,
		}
		expose(sw, "switch", { name = "Piec", path = "t:piec" })

		-- Now hide it via AccessControl (as the panel would) and re-evaluate.
		AccessControl.set("mqtt", "t:piec", "hidden")
		_pub = {}                 -- only look at what refresh() does
		HAStatePublisher.refresh()
	`
	if _, err := engine.Execute(setup); err != nil {
		t.Fatalf("setup failed: %v", err)
	}

	// After hiding + refresh, there must be NO non-empty (publishing) config for the
	// object, and at least one empty (clearing) config for its switch component.
	check := `(function()
		local clearedSwitch, published = false, false
		for _, p in ipairs(_pub) do
			if p.topic:find("t_piec", 1, true) and p.topic:find("/config", 1, true) then
				if p.payload == "" then
					if p.topic:find("/switch/", 1, true) then clearedSwitch = true end
				else
					published = true
				end
			end
		end
		if published then return "STILL_PUBLISHED" end
		if not clearedSwitch then return "SWITCH_NOT_CLEARED" end
		return "OK"
	end)()`
	if got := mustExec(t, engine, check); got != "OK" {
		t.Errorf("hidden object not cleared: %s", got)
	}
}

// TestHADiscoveryReadonlyAndNil guards two bugs surfaced once AccessControl
// started honouring "readonly"/"hidden":
//   - a readonly controllable object (switch) published HA discovery WITHOUT a
//     command_topic, which HA rejects ("required key not provided @ command_topic").
//     Such objects must be remapped to a read-only component (binary_sensor).
//   - a sensor with no reading published the string "nil" as state, which HA
//     rejects for numeric sensors. nil readings must be skipped.
func TestHADiscoveryReadonlyAndNil(t *testing.T) {
	engine := NewLuaEngine()
	defer engine.Close()

	setup := `
		_pub = {}
		_fake = {
			publish = function(self, topic, payload, qos, retain)
				table.insert(_pub, { topic = topic, payload = payload })
			end,
			subscribe = function() end,
			unsubscribe = function() end,
		}
		HAStatePublisher.setup(_fake)

		-- Readonly switch: must become a binary_sensor with no command_topic.
		local sw = {
			_type = "DOUT", _lastState = 1,
			get = function(s) return s._lastState end,
			set = function() end,
			onChange = function(s, cb) return function() end end,
		}
		expose(sw, "switch", { name = "Pompa", readonly = true, path = "t:sw" })

		-- Temperature with no reading: must NOT publish "nil".
		local temp = {
			get = function() return nil end,
			onChange = function(s, cb) return function() end end,
		}
		expose(temp, "temperature", { name = "Temp", path = "t:temp" })
	`
	if _, err := engine.Execute(setup); err != nil {
		t.Fatalf("setup failed: %v", err)
	}

	// No publish may carry the literal "nil" payload.
	nilCheck := `(function()
		for _, p in ipairs(_pub) do
			if p.payload == "nil" then return "NIL_PUBLISHED:"..p.topic end
		end
		return "OK"
	end)()`
	if got := mustExec(t, engine, nilCheck); got != "OK" {
		t.Errorf("nil value was published: %s", got)
	}

	// The readonly switch discovery must be a binary_sensor config with no command_topic.
	discoTopic := mustExec(t, engine, `(function()
		for _, p in ipairs(_pub) do
			if p.topic:find("t_sw", 1, true) and p.topic:find("/config", 1, true) then
				return p.topic
			end
		end
		return ""
	end)()`)
	if discoTopic == "" {
		t.Fatal("no discovery config published for readonly switch")
	}
	if !strings.Contains(discoTopic, "/binary_sensor/") {
		t.Errorf("readonly switch discovery topic = %q, want a binary_sensor config", discoTopic)
	}

	discoPayload := mustExec(t, engine, `(function()
		for _, p in ipairs(_pub) do
			if p.topic:find("t_sw", 1, true) and p.topic:find("/config", 1, true) then
				return p.payload
			end
		end
		return ""
	end)()`)
	if strings.Contains(discoPayload, "command_topic") {
		t.Errorf("readonly switch discovery must not contain command_topic: %s", discoPayload)
	}
	// Sanity: it should still carry a state_topic so HA shows the value.
	if !strings.Contains(discoPayload, "state_topic") {
		t.Errorf("readonly switch discovery missing state_topic: %s", discoPayload)
	}
}

// TestHADiscoveryWritableHasCommandTopic is the inverse guard: a NORMAL (non-readonly)
// writable object must keep its command_topic and its native component, so the readonly
// remap never accidentally strips control from controllable entities.
func TestHADiscoveryWritableHasCommandTopic(t *testing.T) {
	engine := NewLuaEngine()
	defer engine.Close()

	setup := `
		_pub = {}
		_fake = {
			publish = function(self, topic, payload, qos, retain)
				table.insert(_pub, { topic = topic, payload = payload })
			end,
			subscribe = function() end, unsubscribe = function() end,
		}
		HAStatePublisher.setup(_fake)
		local sw = {
			_type = "DOUT", _lastState = 0,
			get = function(s) return s._lastState end,
			set = function(s, i, v) s._lastState = v end,
			onChange = function(s, cb) return function() end end,
		}
		expose(sw, "switch", { name = "Brama", path = "t:gate" })
	`
	if _, err := engine.Execute(setup); err != nil {
		t.Fatalf("setup failed: %v", err)
	}

	topic := mustExec(t, engine, `(function()
		for _, p in ipairs(_pub) do
			if p.topic:find("t_gate", 1, true) and p.topic:find("/config", 1, true) then return p.topic end
		end
		return ""
	end)()`)
	payload := mustExec(t, engine, `(function()
		for _, p in ipairs(_pub) do
			if p.topic:find("t_gate", 1, true) and p.topic:find("/config", 1, true) then return p.payload end
		end
		return ""
	end)()`)
	if !strings.Contains(topic, "/switch/") {
		t.Errorf("writable switch should stay a switch component: %s", topic)
	}
	if !strings.Contains(payload, "command_topic") {
		t.Errorf("writable switch discovery must contain command_topic: %s", payload)
	}
}

// TestHADiscoveryEnergyClasses verifies that expose() passes device_class and
// state_class through to the HA discovery payload. The HA Energy dashboard only
// accepts sensors carrying both (device_class "energy", state_class
// "total_increasing"), and no built-in expose type sets them, so a virtual kWh
// meter exposed as a readonly number must be able to opt in explicitly.
func TestHADiscoveryEnergyClasses(t *testing.T) {
	engine := NewLuaEngine()
	defer engine.Close()

	setup := `
		_pub = {}
		_fake = {
			publish = function(self, topic, payload, qos, retain)
				table.insert(_pub, { topic = topic, payload = payload })
			end,
			subscribe = function() end, unsubscribe = function() end,
		}
		HAStatePublisher.setup(_fake)

		local meter = VirtualNumber:new("gridImport", { unit = "kWh", initial = 123.4 })
		expose(meter, "number", {
			name = "Pobor z sieci", path = "t:grid_import", unit = "kWh",
			readonly = true, device_class = "energy", state_class = "total_increasing",
		})
	`
	if _, err := engine.Execute(setup); err != nil {
		t.Fatalf("setup failed: %v", err)
	}

	payload := mustExec(t, engine, `(function()
		for _, p in ipairs(_pub) do
			if p.topic:find("grid_import", 1, true) and p.topic:find("/config", 1, true) then
				return p.payload
			end
		end
		return ""
	end)()`)
	if payload == "" {
		t.Fatal("no discovery config published for the energy meter")
	}
	for _, want := range []string{
		`"device_class":"energy"`,
		`"state_class":"total_increasing"`,
		`"unit_of_measurement":"kWh"`,
	} {
		if !strings.Contains(payload, want) {
			t.Errorf("discovery payload missing %q: %s", want, payload)
		}
	}
	// A readonly number must land on the sensor component, which is what the
	// Energy dashboard reads; a plain "number" entity would not qualify.
	topic := mustExec(t, engine, `(function()
		for _, p in ipairs(_pub) do
			if p.topic:find("grid_import", 1, true) and p.topic:find("/config", 1, true) then
				return p.topic
			end
		end
		return ""
	end)()`)
	if !strings.Contains(topic, "/sensor/") {
		t.Errorf("readonly energy meter should be a sensor component, got topic %q", topic)
	}
}
