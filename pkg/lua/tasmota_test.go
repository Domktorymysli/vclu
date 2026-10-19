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
