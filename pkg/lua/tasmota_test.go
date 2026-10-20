package lua

import "testing"

// Harness: MQTT messages go in through MQTT:_handleMessage, publishes are read from MQTT._outbox.
const tasmotaHarness = `
	tasmota = require("lib/tasmota")
	function msg(topic, payload) MQTT:_handleMessage(topic, payload, 0, false) end
	function sent()
		local out = {}
		for _, m in ipairs(MQTT._outbox) do out[#out + 1] = m.topic .. "=" .. m.payload end
		MQTT._outbox = {}
		return table.concat(out, " ")
	end
	function eq(label, got, want)
		if got ~= want then error(label .. ": got " .. tostring(got) .. ", want " .. tostring(want), 2) end
	end
`

func TestTasmotaSwitchStateFromDevice(t *testing.T) {
	runStateChange(t, tasmotaHarness+`
		MQTT._outbox = {}
		local sw = tasmota.switch("tasmota_75088A")
		eq("asked on start", sent(), "cmnd/tasmota_75088A/STATE=")

		msg("stat/tasmota_75088A/POWER", "ON")
		eq("POWER", sw:getValue(), 1)
		msg("stat/tasmota_75088A/RESULT", '{"POWER":"OFF"}')
		eq("RESULT", sw:getValue(), 0)
		msg("tele/tasmota_75088A/STATE", '{"Time":"2026-10-19T23:40:00","POWER":"ON","Wifi":{"RSSI":80}}')
		eq("STATE", sw:getValue(), 1)
		msg("stat/tasmota_75088A/RESULT", '{"POWER":"TOGGLE"}')
		eq("TOGGLE is not a state", sw:getValue(), 1)
		eq("a state from the device publishes nothing", sent(), "")

		eq("offline before LWT", tasmota.online("tasmota_75088A"), false)
		msg("tele/tasmota_75088A/LWT", "Online")
		eq("online", tasmota.online("tasmota_75088A"), true)
		eq("asked after LWT Online", sent(), "cmnd/tasmota_75088A/STATE=")
		msg("tele/tasmota_75088A/LWT", "Offline")
		eq("offline", tasmota.online("tasmota_75088A"), false)
		eq("nothing asked when offline", sent(), "")
	`)
}

func TestTasmotaSwitchCommandsOnce(t *testing.T) {
	runStateChange(t, tasmotaHarness+`
		local sw = tasmota.switch("tasmota_75088A")
		local two = tasmota.switch("tasmota_75088A", { relay = 2 })
		expose(sw, "switch", { path = "test:lampa" })
		MQTT._outbox = {}

		sw:switchOn()
		eq("from Lua", sent(), "cmnd/tasmota_75088A/POWER=ON")
		msg("stat/tasmota_75088A/RESULT", '{"POWER":"ON"}')
		eq("the answer is not sent back", sent(), "")

		HACommands.handleExposedMessage("vclu/exposed/test/switch/lampa/set", "OFF")
		eq("from Home Assistant", sent(), "cmnd/tasmota_75088A/POWER=OFF")
		eq("switched", sw:getValue(), 0)

		two:switchOn()
		eq("relay 2", sent(), "cmnd/tasmota_75088A/POWER2=ON")
		msg("stat/tasmota_75088A/POWER2", "OFF")
		eq("POWER2 from the device", two:getValue(), 0)
		eq("relay 1 untouched", sw:getValue(), 0)
		msg("stat/tasmota_75088A/POWER1", "ON")
		eq("POWER1 is relay 1", sw:getValue(), 1)
		eq("nothing sent back", sent(), "")
	`)
}

func TestTasmotaDeviceIsTheActor(t *testing.T) {
	runStateChange(t, tasmotaHarness+`
		local sw = tasmota.switch("tasmota_75088A")
		registerObject("tasmota.biurko", sw)
		takeEvents()
		msg("stat/tasmota_75088A/POWER", "ON")
		local e = takeEvents()[1]
		assert(e and e.origin == "mqtt" and e.actor == "device:tasmota_75088A" and e.correlationId,
			"press on the device: " .. tostring(e and e.actor))
		sw:switchOff()
		e = takeEvents()[1]
		assert(e and e.actor ~= "device:tasmota_75088A", "a command from Lua is not the device")
	`)
}

func TestTasmotaDimmer(t *testing.T) {
	runStateChange(t, tasmotaHarness+`
		local d = tasmota.dimmer("dim1")
		MQTT._outbox = {}
		msg("stat/dim1/RESULT", '{"POWER":"ON","Dimmer":40}')
		eq("on at 40", d:getValue(), 0.4)
		msg("stat/dim1/POWER", "OFF")
		eq("off", d:getValue(), 0)
		msg("tele/dim1/STATE", '{"POWER":"ON","Dimmer":40}')
		eq("on again", d:getValue(), 0.4)
		eq("nothing sent back", sent(), "")

		d:setBrightness(75)
		eq("brightness", sent(), "cmnd/dim1/Dimmer=75")
		msg("stat/dim1/RESULT", '{"POWER":"ON","Dimmer":75}')
		eq("the answer is not sent back", sent(), "")
		d:switchOff()
		eq("off by POWER", sent(), "cmnd/dim1/POWER=OFF")
	`)
}

func TestTasmotaSensor(t *testing.T) {
	runStateChange(t, tasmotaHarness+`
		MQTT._outbox = {}
		local p = tasmota.sensor("plug1", "ENERGY.Power", { unit = "W" })
		local v = tasmota.sensor("plug1", "ENERGY.Voltage", { unit = "V" })
		eq("asked for state and sensors once", sent(), "cmnd/plug1/STATE= cmnd/plug1/Status=10")
		msg("stat/plug1/STATUS10", '{"StatusSNS":{"Time":"x","ENERGY":{"Power":12,"Voltage":231}}}')
		eq("Status 10", p:getValue(), 12)
		eq("voltage", v:getValue(), 231)
		msg("tele/plug1/SENSOR", '{"Time":"x","ENERGY":{"Power":40.5,"Voltage":229}}')
		eq("SENSOR", p:getValue(), 40.5)
		msg("tele/plug1/SENSOR", '{"Time":"x","ENERGY":{"Voltage":230}}')
		eq("a missing field keeps the value", p:getValue(), 40.5)
		eq("nothing published", sent(), "")
		msg("tele/plug1/LWT", "Online")
		eq("sensors asked after LWT", sent(), "cmnd/plug1/STATE= cmnd/plug1/Status=10")
	`)
}

func TestTasmotaDevicesFromDiscovery(t *testing.T) {
	runStateChange(t, tasmotaHarness+`
		eq("none yet", #tasmota.devices(), 0)
		MQTT:_handleMessage("tasmota/discovery/C8C9A375088A/config",
			'{"ip":"192.168.0.77","dn":"Sonoff","fn":["Lampka",null,null,null],"hn":"tasmota-75088A-2186",' ..
			'"mac":"C8C9A375088A","md":"Sonoff Basic","t":"tasmota_75088A","rl":[1,0,0,0,0,0,0,0],"sw":"13.3.0"}', 0, true)
		MQTT:_handleMessage("tasmota/discovery/AA/config", 'not json', 0, true)
		local list = tasmota.devices()
		eq("one device", #list, 1)
		local d = list[1]
		eq("topic", d.topic, "tasmota_75088A")
		eq("name", d.name, "Lampka")
		eq("relays", d.relays, 1)
		eq("ip", d.ip, "192.168.0.77")
	`)
}

func TestTasmotaNeedsTopic(t *testing.T) {
	runStateChange(t, tasmotaHarness+`
		local ok, err = pcall(tasmota.switch, nil)
		assert(not ok and tostring(err):find("Topic"), tostring(err))
	`)
}

// an automation reacting to the device's own change by commanding the same object
// (e.g. "switch it back off") must reach the device.
func TestTasmotaCommandFromOnChangeOfDeviceState(t *testing.T) {
	runStateChange(t, tasmotaHarness+`
		local sw = tasmota.switch("tasmota_75088A")
		sw:onChange(function(v) if v == 1 then sw:switchOff() end end)
		MQTT._outbox = {}
		msg("stat/tasmota_75088A/POWER", "ON")
		eq("local state", sw:getValue(), 0)
		eq("the command from the automation is sent", sent(), "cmnd/tasmota_75088A/POWER=OFF")
	`)
}

// after the device switched itself off, switchOn (Lua, dashboard, HA "ON") should
// restore the level the device had, not jump to 100.
func TestTasmotaDimmerSwitchOnRestoresDeviceLevel(t *testing.T) {
	runStateChange(t, tasmotaHarness+`
		local d = tasmota.dimmer("dim1")
		msg("stat/dim1/RESULT", '{"POWER":"ON","Dimmer":40}')
		msg("stat/dim1/POWER", "OFF")
		MQTT._outbox = {}
		d:switchOn()
		local out = sent()
		assert(out == "cmnd/dim1/Dimmer=40" or out == "cmnd/dim1/POWER=ON", "switchOn sent: " .. out)
	`)
}

// a topic prefix on the default client would move Tasmota's topics; the library says so.
func TestTasmotaTopicPrefix(t *testing.T) {
	runStateChange(t, tasmotaHarness+`
		local warned = { text = "" }
		local warn = Logger.warn
		Logger.warn = function(self, fmt, ...) warned.text = warned.text .. string.format(fmt, ...) end
		MQTT:setTopicPrefix("grenton")
		tasmota.switch("tasmota_75088A")
		Logger.warn = warn
		assert(warned.text:find("topic prefix", 1, true), "no warning: " .. warned.text)
	`)
}

// a sensor is read-only; a write changes the local value with nothing sent.
func TestTasmotaSensorWriteIsRefused(t *testing.T) {
	runStateChange(t, tasmotaHarness+`
		local p = tasmota.sensor("plug1", "ENERGY.Power", { unit = "W" })
		msg("tele/plug1/SENSOR", '{"ENERGY":{"Power":12}}')
		pcall(function() p:setValue(500) end)
		eq("value stays the device's", p:getValue(), 12)
	`)
}

// an error in a module's handler during a device update must not break later updates
// or commands (quiet flag, StateContext stack, gopher-lua pcall upvalues).
func TestTasmotaErrorInHandlerRecovers(t *testing.T) {
	runStateChange(t, tasmotaHarness+`
		local sw = tasmota.switch("tasmota_75088A")
		local f = { boom = true }
		sw:onChange(function() if f.boom then f.boom = false; error("boom") end end)
		local depth = StateContext.depth()
		msg("stat/tasmota_75088A/POWER", "ON")
		eq("stack", StateContext.depth(), depth)
		eq("state applied", sw:getValue(), 1)
		MQTT._outbox = {}
		msg("stat/tasmota_75088A/POWER", "OFF")
		eq("device state after error", sw:getValue(), 0)
		eq("nothing sent back", sent(), "")
		sw:switchOn()
		eq("command after error", sent(), "cmnd/tasmota_75088A/POWER=ON")
	`)
}

// the docs example exposes the switch as "light": HA ON/OFF reach the device once.
func TestTasmotaSwitchExposedAsLightFromHA(t *testing.T) {
	runStateChange(t, tasmotaHarness+`
		local sw = tasmota.switch("tasmota_75088A")
		expose(sw, "light", { path = "tasmota.biurko", name = "Lampka" })
		MQTT._outbox = {}
		HACommands.handleExposedMessage("vclu/exposed/vclu/light/tasmota_biurko/set", "ON")
		eq("HA ON", sent(), "cmnd/tasmota_75088A/POWER=ON")
		eq("state", sw:getValue(), 1)
		HACommands.handleExposedMessage("vclu/exposed/vclu/light/tasmota_biurko/set", "OFF")
		eq("HA OFF", sent(), "cmnd/tasmota_75088A/POWER=OFF")
		local ok = HACommands.handleExposedMessage("vclu/exposed/vclu/light/tasmota_biurko/brightness/set", "50")
		eq("no brightness on a relay", ok, false)
		eq("nothing for brightness", sent(), "")
	`)
}

// MCP vclu_device_set (MCP.set_value -> ExposedHandle:setValue) on a switch and a dimmer.
func TestTasmotaMCPSetValue(t *testing.T) {
	runStateChange(t, tasmotaHarness+`
		local sw = tasmota.switch("t1")
		local d = tasmota.dimmer("d1")
		expose(sw, "switch", { path = "t:sw" })
		expose(d, "dimmer", { path = "t:dim" })
		MQTT._outbox = {}
		MCP.set_value("t:sw", 1)
		eq("switch", sent(), "cmnd/t1/POWER=ON")
		MCP.set_value("t:dim", 0.3)
		eq("dimmer", sent(), "cmnd/d1/Dimmer=30")
	`)
}

// HA brightness on a Tasmota dimmer goes through setBrightness and the following ON is dropped.
func TestTasmotaDimmerHABrightness(t *testing.T) {
	runStateChange(t, tasmotaHarness+`
		local d = tasmota.dimmer("dim1")
		expose(d, "dimmer", { path = "t:dim" })
		msg("stat/dim1/RESULT", '{"POWER":"OFF","Dimmer":40}')
		MQTT._outbox = {}
		HACommands.handleExposedMessage("vclu/exposed/t/light/dim/brightness/set", "30")
		HACommands.handleExposedMessage("vclu/exposed/t/light/dim/set", "ON")
		eq("brightness then ON", sent(), "cmnd/dim1/Dimmer=30")
		msg("stat/dim1/RESULT", '{"POWER":"ON","Dimmer":30}')
		eq("answer not echoed", sent(), "")
		HACommands.handleExposedMessage("vclu/exposed/t/light/dim/set", "OFF")
		eq("HA OFF", sent(), "cmnd/dim1/POWER=OFF")
		HACommands.handleExposedMessage("vclu/exposed/t/light/dim/set", "ON")
		eq("HA ON restores", sent(), "cmnd/dim1/Dimmer=30")
		d:setValue(1.0)
		eq("full", sent(), "cmnd/dim1/Dimmer=100")
		msg("stat/dim1/RESULT", '{"POWER":"ON","Dimmer":0}')
		eq("Dimmer 0 is off", d:getValue(), 0)
		eq("no echo", sent(), "")
	`)
}

// the device switched on by its button before vCLU learned its Dimmer (STATE answer lost or
// late): the object should be on, not stay at 0.
func TestTasmotaDimmerPowerOnBeforeAnyDimmer(t *testing.T) {
	runStateChange(t, tasmotaHarness+`
		local d = tasmota.dimmer("dim1")
		msg("stat/dim1/POWER", "ON")
		assert(d:getValue() > 0, "device is ON, object value " .. tostring(d:getValue()))
	`)
}

// Dimmer reported alone (no POWER key, e.g. a Fade/Dimmer answer with SetOption20) after the
// device is known to be on.
func TestTasmotaDimmerWithoutPower(t *testing.T) {
	runStateChange(t, tasmotaHarness+`
		local d = tasmota.dimmer("dim1")
		msg("stat/dim1/RESULT", '{"POWER":"ON","Dimmer":40}')
		msg("stat/dim1/RESULT", '{"Dimmer":60}')
		eq("level alone", d:getValue(), 0.6)
		eq("no echo", sent():gsub("cmnd/dim1/STATE=%s*", ""), "")
	`)
}

// the state is asked for again when the MQTT client (re)connects.
func TestTasmotaAskOnConnect(t *testing.T) {
	runStateChange(t, tasmotaHarness+`
		tasmota.switch("a1")
		tasmota.sensor("p1", "ENERGY.Power")
		MQTT._outbox = {}
		MQTT:_emit("connect")
		local out = sent()
		assert(out:find("cmnd/a1/STATE=", 1, true) and out:find("cmnd/p1/STATE=", 1, true)
			and out:find("cmnd/p1/Status=10", 1, true), "on connect: " .. out)
	`)
}

// a read-only sensor written through vclu_device_set (exposed without readonly = true):
// the write changes nothing, so it must not be reported as a success.
func TestTasmotaSensorWriteReportedAsSuccess(t *testing.T) {
	runStateChange(t, tasmotaHarness+`
		local p = tasmota.sensor("plug1", "ENERGY.Power", { unit = "W" })
		expose(p, "number", { path = "t:moc" })
		msg("tele/plug1/SENSOR", '{"ENERGY":{"Power":12}}')
		local res = JSON.decode(MCP.set_value("t:moc", 500))
		eq("value", p:getValue(), 12)
		eq("MCP reports the refused write as ok", res.ok, false)
	`)
}

// obj:expose() (and exposeRegistry) take the type from _type: AnalogOUT is a writable number.
func TestTasmotaSensorSelfExposeIsWritable(t *testing.T) {
	runStateChange(t, tasmotaHarness+`
		local p = tasmota.sensor("plug1", "ENERGY.Power", { unit = "W" })
		registerObject("tasmota.moc", p)
		local h = p:expose()
		local caps = ExposedObjects:getHandleCapabilities(h)
		assert(h.options.readonly or not caps.write,
			"sensor exposed as '" .. h.type .. "', writable, readonly=" .. tostring(h.options.readonly))
	`)
}

// Tasmota discovery rl: 1 relay, 2 light, 3 shutter; a dimmer has no relay.
func TestTasmotaDiscoveryLightIsNotARelay(t *testing.T) {
	runStateChange(t, tasmotaHarness+`
		tasmota.devices()
		MQTT:_handleMessage("tasmota/discovery/AABBCC/config",
			'{"ip":"192.168.0.78","dn":"Dimmer","fn":["Lampa",null],"mac":"AABBCC","t":"tasmota_dim","rl":[2,0,0,0,0,0,0,0]}', 0, true)
		eq("relays of a light", tasmota.devices()[1].relays, 0)
	`)
}

// a device value equal to the one a nested callback sets must not leave the echo guard armed
func TestTasmotaNestedSameValue(t *testing.T) {
	runStateChange(t, tasmotaHarness+`
		local sw = tasmota.switch("t1")
		local f = { n = 0 }
		sw:onChange(function(v)
			f.n = f.n + 1
			if f.n == 1 and v == 1 then sw:switchOff(); sw:switchOn() end
		end)
		MQTT._outbox = {}
		msg("stat/t1/POWER", "ON")
		eq("both commands sent", sent(), "cmnd/t1/POWER=OFF cmnd/t1/POWER=ON")
		msg("stat/t1/POWER", "OFF")
		msg("stat/t1/POWER", "ON")
		eq("quick reports, no echo", sent(), "")
		sw:switchOff()
		eq("command after", sent(), "cmnd/t1/POWER=OFF")
	`)
}
