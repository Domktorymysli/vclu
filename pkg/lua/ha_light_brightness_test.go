package lua

import (
	"strings"
	"testing"
)

// A DOUT exposed as "light" is on/off: no brightness in HA discovery or MCP caps,
// and HA's ON writes 1. An object with getBrightness keeps its slider.
func TestExposedLightBrightnessOnlyWhenDimmable(t *testing.T) {
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

		local function mk(dim)
			local o = {
				_type = "DOUT", _lastState = 0,
				get = function(s) return s._lastState end,
				set = function(s, i, v) s._lastState = v end,
				onChange = function(s, cb) return function() end end,
			}
			if dim then o.getBrightness = function(s) return s._lastState * 100 end end
			return o
		end
		_dout = mk(false)
		expose(_dout, "light", { name = "Biuro", path = "t:dout" })
		expose(mk(true), "light", { name = "Dim", path = "t:dim" })
	`
	if _, err := engine.Execute(setup); err != nil {
		t.Fatalf("setup failed: %v", err)
	}

	config := func(id string) string {
		return mustExec(t, engine, `(function()
			for _, p in ipairs(_pub) do
				if p.topic:find("`+id+`", 1, true) and p.topic:find("/config", 1, true) then
					return p.payload
				end
			end
			return ""
		end)()`)
	}
	if c := config("t_dout"); c == "" || strings.Contains(c, "brightness") {
		t.Errorf("DOUT light discovery must not have brightness: %q", c)
	}
	if c := config("t_dim"); !strings.Contains(c, "brightness_command_topic") {
		t.Errorf("dimmable light discovery lost brightness: %q", c)
	}

	cleared := mustExec(t, engine, `(function()
		for _, p in ipairs(_pub) do
			if p.topic:find("/light/dout/brightness", 1, true) and p.payload == "" then
				return "yes"
			end
		end
		return "no"
	end)()`)
	if cleared != "yes" {
		t.Errorf("DOUT light: stale retained brightness not cleared")
	}

	caps := mustExec(t, engine, `(function()
		local d = ExposedObjects:getHandleCapabilities(ExposedObjects:get("t:dout"))
		local m = ExposedObjects:getHandleCapabilities(ExposedObjects:get("t:dim"))
		return tostring(d.brightness) .. "," .. tostring(m.brightness)
	end)()`)
	if caps != "nil,true" {
		t.Errorf("caps brightness dout,dim = %s, want nil,true", caps)
	}

	got := mustExec(t, engine, `(function()
		local cfg
		for _, p in ipairs(_pub) do
			if p.topic:find("t_dout", 1, true) and p.topic:find("/config", 1, true) then cfg = p.payload end
		end
		local topic = cfg:match('"command_topic":"([^"]+)"'):gsub("\\/", "/")
		HACommands.handleExposedMessage(topic, "ON")
		local on = _dout._lastState
		local ok = HACommands.handleExposedMessage(topic:gsub("/set$", "/brightness/set"), "37")
		return tostring(on) .. "," .. tostring(ok) .. "," .. tostring(_dout._lastState)
	end)()`)
	if got != "1,false,1" {
		t.Errorf("ON / brightness on a DOUT light = %s, want 1,false,1", got)
	}
}

// A dimmer's value is 0..1 and HA sends brightness as 0..100: ON keeps the
// last brightness (switchOn), the slider goes through setBrightness, and an
// object without those methods gets value/100.
func TestHADimmerScale(t *testing.T) {
	engine := NewLuaEngine()
	defer engine.Close()

	setup := `
		_pub = {}
		_fake = {
			publish = function(self, topic, payload) table.insert(_pub, { topic = topic, payload = payload }) end,
			subscribe = function() end, unsubscribe = function() end,
		}
		HAStatePublisher.setup(_fake)
		_calls = {}
		local function mk(methods)
			local o = {
				_type = "DIMMER", _lastState = 0,
				get = function(s) return s._lastState end,
				set = function(s, i, v) table.insert(_calls, "set " .. v); s._lastState = v end,
				onChange = function(s, cb) return function() end end,
				getBrightness = function(s) return s._lastState * 100 end,
			}
			if methods then
				o.setBrightness = function(s, p) table.insert(_calls, "bri " .. p); s._lastState = p / 100 end
				o.switchOn = function(s) table.insert(_calls, "on"); s._lastState = 0.4 end
				o.switchOff = function(s) table.insert(_calls, "off"); s._lastState = 0 end
			end
			return o
		end
		_dim = mk(true)
		_plain = mk(false)
		expose(_dim, "dimmer", { name = "Dim", path = "t:dim" })
		expose(_plain, "light", { name = "Plain", path = "t:plain" })
		function _topic(id)
			for _, p in ipairs(_pub) do
				if p.topic:find(id, 1, true) and p.topic:find("/config", 1, true) then
					return (p.payload:match('"command_topic":"([^"]+)"'):gsub("\\/", "/"))
				end
			end
		end
	`
	if _, err := engine.Execute(setup); err != nil {
		t.Fatalf("setup failed: %v", err)
	}

	got := mustExec(t, engine, `(function()
		local d, p = _topic("t_dim"), _topic("t_plain")
		local bd, bp = d:gsub("/set$", "/brightness/set"), p:gsub("/set$", "/brightness/set")
		HACommands.handleExposedMessage(d, "ON")
		HACommands.handleExposedMessage(bd, "37")
		HACommands.handleExposedMessage(d, "ON")  -- HA repeats ON after a brightness change
		HACommands.handleExposedMessage(d, "OFF")
		HACommands.handleExposedMessage(bp, "50")
		HACommands.handleExposedMessage(p, "OFF")
		HACommands.handleExposedMessage(p, "ON")
		return table.concat(_calls, ",")
	end)()`)
	if got != "on,bri 37,off,set 0.5,set 0,set 1" {
		t.Errorf("calls = %s", got)
	}
}
