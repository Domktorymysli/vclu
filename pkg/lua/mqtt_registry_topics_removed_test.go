package lua

import "testing"

// Only exposed objects are on MQTT: the registry topics (vclu/{clu}/{component}/{obj}/set
// and .../state) are gone, whatever object or component a topic names.

// mqttBroker stands in for the broker: publishes land in _pub, and deliver() hands a
// message to every subscription whose pattern matches, returning how many took it.
const mqttBroker = `
	_pub = {}
	_subs = {}
	_broker = {
		publish = function(self, topic, payload) table.insert(_pub, { topic = topic, payload = payload }); return true end,
		subscribe = function(self, pattern, cb) table.insert(_subs, { pattern = pattern, cb = cb }) end,
		unsubscribe = function() end,
	}
	HAStatePublisher.setup(_broker)
	HACommands.setup(_broker)
	function deliver(topic, payload)
		local n = 0
		for _, s in ipairs(_subs) do
			if s.cb and MQTT._topicMatches(nil, topic, s.pattern) then
				n = n + 1
				s.cb(topic, payload)
			end
		end
		return n
	end
	-- publishes whose topic contains text (plain find)
	function publishedWith(text)
		local out = {}
		for _, p in ipairs(_pub) do
			if p.topic:find(text, 1, true) then out[#out + 1] = p.topic .. "=" .. tostring(p.payload) end
		end
		return table.concat(out, ";")
	end
`

// thermostatsSetup: a Grenton DOUT, a Grenton thermostat (RemoteThermostat) and a
// VirtualThermostat, all in the registry and none exposed. CLU calls land in _calls.
const thermostatsSetup = mqttBroker + `
	_calls = {}
	local clu = {
		address = "192.168.0.3",
		execute = function(s, i, cmd) table.insert(_calls, cmd) end,
		executeNoReply = function(s, i, cmd) table.insert(_calls, cmd) end,
	}
	DOU = RemoteSwitch:new(clu, "DOU1", "lampa")
	registerObject("CLU1.DOU1", DOU)
	TH = RemoteThermostat:new(clu, "THE1", "aSalon")
	registerObject("CLU1.THE1", TH)
	TEMP, HEATER = VirtualNumber:new("t"), VirtualSwitch:new("h")
	VT = VirtualThermostat:new("salon", { source = TEMP, output = HEATER, setpoint = 21 })
	registerObject("CLU1.VT1", VT)
	TEMP:setValue(20.2) -- below the set point: VT heats
	function vtState()
		return VT:getState() .. "/" .. VT:getSetpoint() .. "/" .. HEATER:getValue()
	end
`

// legacyTopics are the shapes the registry handler took, for each object.
var legacyTopics = []struct{ topic, payload string }{
	{"vclu/clu1/switch/dou1/set", "ON"},
	{"vclu/clu1/switch/the1/set", "OFF"},
	{"vclu/clu1/number/the1/set", "85"},
	{"vclu/x/cover/vt1/set", "STOP"},
	{"vclu/clu1/cover/vt1/set", "STOP"},
	{"vclu/clu1/switch/vt1/set", "OFF"},
	{"vclu/x/number/vt1/set", "85"},
	{"vclu/clu1/light/dou1/brightness/set", "40"},
	{"vclu/clu1/cover/vt1/position/set", "0"},
	{"vclu/clu1/cover/vt1/tilt/set", "0"},
}

func TestMqttRegistryTopics_CommandChangesNothing(t *testing.T) {
	engine := NewLuaEngine()
	defer engine.Close()
	if _, err := engine.Execute(thermostatsSetup); err != nil {
		t.Fatalf("setup failed: %v", err)
	}
	before := mustExec(t, engine, `vtState()`)
	if before != "1/21/1" {
		t.Fatalf("VirtualThermostat before = %s, want 1/21/1 (on, 21, heating)", before)
	}
	for _, c := range legacyTopics {
		got := mustExec(t, engine, `(function()
			_calls = {}
			local n = deliver("`+c.topic+`", "`+c.payload+`")
			return n .. "|" .. #_calls .. "|" .. tostring(DOU:getValue()) .. "|" .. vtState()
		end)()`)
		if got != "0|0|nil|"+before {
			t.Errorf("%s %s: handlers|CLU calls|DOUT|VT = %s, want 0|0|nil|%s", c.topic, c.payload, got, before)
		}
	}
	mustExec(t, engine, `VT:stop()`)
}

// A thermostat shared read-only: neither its exposed command topic nor a registry topic
// changes its set point or state.
func TestMqttRegistryTopics_ReadonlyVirtualThermostat(t *testing.T) {
	engine := NewLuaEngine()
	defer engine.Close()
	if _, err := engine.Execute(thermostatsSetup); err != nil {
		t.Fatalf("setup failed: %v", err)
	}
	got := mustExec(t, engine, `(function()
		local h = expose(VT, "climate", { name = "Salon", readonly = true })
		local base = "vclu/exposed/vclu/climate/clu1_vt1"
		local out = { h.type, vtState() }
		local ok, err = HACommands.handleExposedMessage(base .. "/target_temp/set", "25")
		table.insert(out, tostring(ok) .. ":" .. tostring(err))
		table.insert(out, deliver(base .. "/target_temp/set", "25") .. ":" .. vtState())
		table.insert(out, deliver(base .. "/set", "OFF") .. ":" .. vtState())
		return table.concat(out, ",")
	end)()`)
	if want := "climate,1/21/1,false:object is readonly,1:1/21/1,1:1/21/1"; got != want {
		t.Errorf("readonly climate: %s, want %s", got, want)
	}
	for _, c := range legacyTopics {
		got := mustExec(t, engine, `deliver("`+c.topic+`", "`+c.payload+`") .. "|" .. vtState()`)
		if got != "0|1/21/1" {
			t.Errorf("%s %s on a readonly climate = %s, want 0|1/21/1", c.topic, c.payload, got)
		}
	}
	mustExec(t, engine, `VT:stop()`)
}

// expose(..., {mqtt = false}) keeps the object off MQTT: its exposed topic is refused.
func TestMqttRegistryTopics_ExposedMqttFalseRefused(t *testing.T) {
	engine := NewLuaEngine()
	defer engine.Close()
	if _, err := engine.Execute(thermostatsSetup); err != nil {
		t.Fatalf("setup failed: %v", err)
	}
	got := mustExec(t, engine, `(function()
		expose(DOU, "switch", { name = "Lampa", mqtt = false })
		_calls = {}
		local ok, err = HACommands.handleExposedMessage("vclu/exposed/vclu/switch/clu1_dou1/set", "ON")
		local n = deliver("vclu/exposed/vclu/switch/clu1_dou1/set", "ON")
		return tostring(ok) .. ":" .. tostring(err) .. "|" .. n .. "|" .. #_calls .. "|" .. tostring(DOU:getValue())
	end)()`)
	if want := "false:object is not on MQTT|1|0|nil"; got != want {
		t.Errorf("mqtt=false: ok:err|handlers|CLU calls|value = %s, want %s", got, want)
	}
	mustExec(t, engine, `VT:stop()`)
}

// A registry object that is not exposed publishes nothing on MQTT when it changes; an
// exposed one publishes on vclu/exposed/....
func TestMqttRegistryTopics_NotExposedPublishesNothing(t *testing.T) {
	engine := NewLuaEngine()
	defer engine.Close()
	if _, err := engine.Execute(thermostatsSetup + `
		DOU2 = RemoteSwitch:new({ execute = function() end }, "DOU2", "kinkiet")
		registerObject("CLU1.DOU2", DOU2)
		expose(DOU2, "switch", { name = "Kinkiet" })
	`); err != nil {
		t.Fatalf("setup failed: %v", err)
	}
	got := mustExec(t, engine, `(function()
		_pub = {}
		DOU:updateState(1)
		TEMP:setValue(22.5) -- VT switches off and its current temperature changes
		DOU2:updateState(1)
		return publishedWith("dou1") .. "|" .. publishedWith("vt1") .. "|" .. publishedWith("the1") .. "|" ..
			publishedWith("dou2")
	end)()`)
	if want := "|||vclu/exposed/vclu/switch/clu1_dou2/state=ON"; got != want {
		t.Errorf("published dou1|vt1|the1|dou2 = %s, want %s", got, want)
	}
	if got := mustExec(t, engine, `tostring(DOU:getValue()) .. "/" .. HEATER:getValue()`); got != "1/0" {
		t.Errorf("objects did not change (%s): the test proves nothing", got)
	}
	mustExec(t, engine, `VT:stop()`)
}
