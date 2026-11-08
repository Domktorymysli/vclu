package lua

import (
	"strings"
	"testing"
)

// Only exposed objects are on MQTT, under vclu/exposed/.

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

// HACommands subscribes only under vclu/exposed/: a command on vclu/{clu}/... reaches no
// handler and changes nothing.
func TestMqttRegistryTopics_OnlyExposedSubscribed(t *testing.T) {
	engine := NewLuaEngine()
	defer engine.Close()
	if _, err := engine.Execute(thermostatsSetup); err != nil {
		t.Fatalf("setup failed: %v", err)
	}
	got := mustExec(t, engine, `(function()
		local other = {}
		for _, s in ipairs(_subs) do
			if s.cb and s.pattern:sub(1, 13) ~= "vclu/exposed/" then other[#other + 1] = s.pattern end
		end
		return #_subs .. "|" .. table.concat(other, ";")
	end)()`)
	if got == "0|" || !strings.HasSuffix(got, "|") {
		t.Errorf("subscriptions|outside vclu/exposed/ = %s, want some and none outside", got)
	}
	got = mustExec(t, engine, `(function()
		_calls = {}
		local n = deliver("vclu/clu1/switch/dou1/set", "ON")
		return n .. "|" .. #_calls .. "|" .. tostring(DOU:getValue())
	end)()`)
	if got != "0|0|nil" {
		t.Errorf("vclu/clu1/switch/dou1/set: handlers|CLU calls|DOUT = %s, want 0|0|nil", got)
	}
	mustExec(t, engine, `VT:stop()`)
}

// A thermostat exposed read-only: its command topics change neither its set point nor its state.
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

// A blind from the OM import reaches Home Assistant as at start: exposeRegistry() exposes
// it, discovery carries the tilt topics under vclu/exposed/ and a tilt command moves the
// slats (HA 100 = open = 0°).
func TestMqttRegistryTopics_ImportedBlindTilt(t *testing.T) {
	engine := NewLuaEngine()
	defer engine.Close()
	if _, err := engine.Execute(mqttBroker + `
		_calls = {}
		local clu = {
			address = "192.168.0.3",
			execute = function(s, i, cmd) table.insert(_calls, cmd) end,
			executeNoReply = function(s, i, cmd) table.insert(_calls, cmd) end,
		}
		ROL = RemoteCover:new(clu, "ROL1", "salon")
		ROL._hasPosition = true
		ROL._hasTilt = true
		registerObject("CLU1.ROL1", ROL)
		exposeRegistry()
	`); err != nil {
		t.Fatalf("setup failed: %v", err)
	}
	disc := mustExec(t, engine, `(function()
		for _, p in ipairs(_pub) do
			if p.topic:find("/config", 1, true) and tostring(p.payload):find("clu1_rol1", 1, true) then
				return tostring(p.payload)
			end
		end
		return ""
	end)()`)
	for _, want := range []string{
		`"tilt_status_topic":"vclu/exposed/vclu/cover/clu1_rol1/tilt"`,
		`"tilt_command_topic":"vclu/exposed/vclu/cover/clu1_rol1/tilt/set"`,
	} {
		if !strings.Contains(disc, want) {
			t.Errorf("discovery lacks %s: %s", want, disc)
		}
	}
	got := mustExec(t, engine, `(function()
		_calls = {}
		local n = deliver("vclu/exposed/vclu/cover/clu1_rol1/tilt/set", "100")
		return n .. "|" .. table.concat(_calls, ";")
	end)()`)
	if got != "1|ROL1:execute(9, 0)" {
		t.Errorf("tilt 100: handlers|CLU calls = %s, want 1|ROL1:execute(9, 0)", got)
	}
}
