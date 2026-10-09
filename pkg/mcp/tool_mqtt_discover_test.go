package mcp

import (
	"strings"
	"testing"

	"github.com/Domktorymysli/vclu/pkg/lua"
)

// Helpers on the real MQTT client: deliver() goes through _handleMessage like
// a broker message, sent() lists what publish queued since the last call.
const snippetHarness = `
function deliver(topic, payload, client)
    (client or MQTT):_handleMessage(topic, payload, 0, false)
end
local seen = {}
function sent(client)
    client = client or MQTT
    local out, box = {}, client:getOutbox()
    for i = (seen[client] or 0) + 1, #box do
        local m = box[i]
        if not m.topic:match("^homeassistant/") and not m.topic:match("^vclu/") then
            table.insert(out, m.topic .. "=" .. m.payload)
        end
    end
    seen[client] = #box
    return table.concat(out, ";")
end
function value(path)
    local h = ExposedObjects:get(path)
    assert(h, "not exposed: " .. path)
    return h:getValue()
end
`

var defaultBroker = BrokerResult{ID: "MQTT"}

// runSnippet runs a generated snippet in the real runtime, then check.
func runSnippet(t *testing.T, setup, snippet, check string) {
	t.Helper()
	if snippet == "" {
		t.Fatal("no snippet generated")
	}
	engine := lua.NewLuaEngine()
	defer engine.Close()
	res, err := engine.Execute(snippetHarness + setup + "\nlocal ok, err = pcall(function()\n" + snippet + "\nsent()\n" + check +
		"\nend)\nreturn ok and \"ok\" or tostring(err)")
	if err != nil {
		t.Fatalf("%v\n%s", err, snippet)
	}
	if res != "ok" {
		t.Fatalf("%s\n--- snippet:\n%s", res, snippet)
	}
}

func TestMQTTDiscoverSnippet_Z2MLight(t *testing.T) {
	d := DiscoveredDevice{Source: "zigbee2mqtt", ID: "0x0011", Name: `Lampa "salon"`, Type: "light",
		Topics: map[string]string{"state": "zigbee2mqtt/lampa"}}
	runSnippet(t, "", generateSnippet(d, defaultBroker), `
		deliver("zigbee2mqtt/lampa", '{"state":"ON","brightness":120}')
		assert(value("z2m:0x0011") == 1, "state ON")
		assert(sent() == "", "a report from the device is not sent back: " .. sent())
		deliver("zigbee2mqtt/lampa", 'not json')
		assert(value("z2m:0x0011") == 1, "bad payload ignored")
		ExposedObjects:get("z2m:0x0011"):setValue(0)
		local s = sent()
		assert(s == 'zigbee2mqtt/lampa/set={"state":"OFF"}', "OFF command: " .. s)
		ExposedObjects:get("z2m:0x0011"):setValue(1)
		s = sent()
		assert(s == 'zigbee2mqtt/lampa/set={"state":"ON"}', "ON command: " .. s)
	`)
}

func TestMQTTDiscoverSnippet_Z2MSensorFeatures(t *testing.T) {
	d := DiscoveredDevice{Source: "zigbee2mqtt", ID: "0x0022", Name: "Okno", Type: "sensor",
		Features: []string{"contact", "temperature", "illuminance", "illuminance_lux", "battery"},
		Topics:   map[string]string{"state": "zigbee2mqtt/okno"}}
	snippet := generateSnippet(d, defaultBroker)
	if strings.Contains(snippet, `"z2m:0x0022:illuminance"`) {
		t.Errorf("raw illuminance next to lux should be skipped:\n%s", snippet)
	}
	runSnippet(t, "", snippet, `
		deliver("zigbee2mqtt/okno", '{"contact":false,"temperature":21.5,"illuminance_lux":120,"battery":90}')
		assert(value("z2m:0x0022:contact") == 1, "open window is active")
		assert(value("z2m:0x0022:temperature") == 21.5, "temperature")
		assert(value("z2m:0x0022:illuminance_lux") == 120, "lux")
		deliver("zigbee2mqtt/okno", '{"contact":true}')
		assert(value("z2m:0x0022:contact") == 0, "closed")
		assert(value("z2m:0x0022:temperature") == 21.5, "missing field keeps the value")
		assert(ExposedObjects:get("z2m:0x0022:battery") == nil, "unmapped feature skipped")
	`)
}

func TestMQTTDiscoverSnippet_Z2MMultiEndpointKeys(t *testing.T) {
	d := DiscoveredDevice{Source: "zigbee2mqtt", ID: "0x0033", Name: "Czujnik", Type: "sensor",
		Features: []string{"temperature_l1"}, Topics: map[string]string{"state": "zigbee2mqtt/c"}}
	if s := generateSnippet(d, defaultBroker); s != "" {
		t.Errorf("unknown key temperature_l1 should give no snippet:\n%s", s)
	}
	sw := DiscoveredDevice{Source: "zigbee2mqtt", ID: "0x0044", Name: "Podwojny", Type: "switch", stateKey: "state_l1"}
	if s := generateSnippet(sw, defaultBroker); s != "" {
		t.Errorf("multi-endpoint switch should give no snippet:\n%s", s)
	}
}

func TestMQTTDiscoverSnippet_Z2MNoReliableMapping(t *testing.T) {
	for _, d := range []DiscoveredDevice{
		{Source: "zigbee2mqtt", ID: "0x1", Name: "Roleta", Type: "cover"},
		{Source: "zigbee2mqtt", ID: "0x2", Name: "Przycisk", Type: "sensor", Features: []string{"action", "battery"}},
	} {
		if s := generateSnippet(d, defaultBroker); s != "" {
			t.Errorf("%s: want no snippet, got\n%s", d.Name, s)
		}
	}
}

func TestMQTTDiscoverSnippet_Tasmota(t *testing.T) {
	d := DiscoveredDevice{Source: "tasmota", ID: "tasmota_desk", Name: "Biurko", Model: "S26", IP: "10.0.0.5",
		Topics: map[string]string{"state": "stat/tasmota_desk/POWER", "command": "cmnd/tasmota_desk/POWER"}}
	runSnippet(t, "", generateSnippet(d, defaultBroker), `
		deliver("stat/tasmota_desk/POWER", "ON")
		assert(value("tasmota:tasmota_desk") == 1, "ON")
		assert(sent() == "", "wall switch report not echoed: " .. sent())
		ExposedObjects:get("tasmota:tasmota_desk"):setValue(0)
		local s = sent()
		assert(s == "cmnd/tasmota_desk/POWER=OFF", "command: " .. s)
	`)
}

func TestMQTTDiscoverSnippet_ESPHome(t *testing.T) {
	d := DiscoveredDevice{Source: "esphome", ID: "kotlownia", Name: "kotlownia", IP: "10.0.0.7",
		Topics: map[string]string{"state": "esphome/kotlownia/sensor/+/state"}}
	runSnippet(t, "", generateSnippet(d, defaultBroker), `
		deliver("esphome/kotlownia/sensor/temperature/state", "48.25")
		assert(value("esphome:kotlownia:temperature") == 48.25, "temperature")
		deliver("esphome/kotlownia/sensor/temperature/state", "nan")
		assert(value("esphome:kotlownia:temperature") == 48.25, "non-number ignored")
	`)
}

// A device found through another Lua client is wired to that client.
func TestMQTTDiscoverSnippet_OtherClient(t *testing.T) {
	d := DiscoveredDevice{Source: "tasmota", ID: "t9", Name: "Garaz"}
	runSnippet(t, `EXT = MQTT:new("ext")`, generateSnippet(d, BrokerResult{ID: "ext"}), `
		deliver("stat/t9/POWER", "ON", EXT)
		assert(value("tasmota:t9") == 1, "report on the other client")
		ExposedObjects:get("tasmota:t9"):setValue(0)
		local s = sent(EXT)
		assert(s == "cmnd/t9/POWER=OFF", "command on the other client: " .. s)
		assert(sent() == "", "nothing on the default client")
	`)
	if s := generateSnippet(d, BrokerResult{ID: "embedded", URL: "tcp://localhost:1883"}); !strings.Contains(s, "embedded broker") {
		t.Errorf("embedded broker note missing:\n%s", s)
	}
}

// Names and IDs come from devices on the broker: they must stay data.
func TestMQTTDiscoverSnippet_QuotingRoundTrip(t *testing.T) {
	var b strings.Builder
	for i := 0; i < 256; i++ {
		b.WriteByte(byte(i))
		b.WriteString("1")
	}
	all := b.String()
	engine := lua.NewLuaEngine()
	defer engine.Close()
	for _, s := range []string{all, `x\`, "a\x001", "]]--", "\"\\\"\n\r"} {
		res, err := engine.Execute("return (" + luaQuote(s) + ") == " + luaQuote(s) + " and #" + luaQuote(s))
		if err != nil {
			t.Fatalf("%q: %v", s, err)
		}
		if res != strings.TrimSpace(res) || res == "false" {
			t.Fatalf("%q: %s", s, res)
		}
	}
	// the decoded literal equals the input byte for byte
	res, err := engine.Execute(`local s = ` + luaQuote(all) + `
		local ok = #s == 512
		for i = 0, 255 do ok = ok and s:byte(2*i+1) == i and s:sub(2*i+2, 2*i+2) == "1" end
		return ok and "ok" or "mismatch"`)
	if err != nil || res != "ok" {
		t.Fatalf("round trip: %v %s", err, res)
	}
	for _, d := range []DiscoveredDevice{
		{Source: "tasmota", ID: "t1", Name: "x\nos.exit(1) --\" ]]\\", IP: "1.2.3.4\n os.exit(2)"},
		{Source: "zigbee2mqtt", ID: "z\"1", Name: "y\r\nos.exit(1)", Type: "switch"},
		{Source: "esphome", ID: "e1", Name: "n\\", IP: "\nos.exit(3)", Topics: map[string]string{"state": "esphome/n\n/sensor/+/state"}},
	} {
		s := generateSnippet(d, BrokerResult{ID: "c\"\nos.exit(4)"})
		for _, line := range strings.Split(s, "\n") {
			if strings.HasPrefix(strings.TrimSpace(line), "os.exit") {
				t.Fatalf("%s: injected line:\n%s", d.Source, s)
			}
		}
		engine2 := lua.NewLuaEngine()
		_, err := engine2.Execute("local ok, err = loadstring(" + luaQuote(s) + ")\nassert(ok, err)")
		engine2.Close()
		if err != nil {
			t.Fatalf("%s: snippet does not compile: %v\n%s", d.Source, err, s)
		}
	}
}

// The guard is reset even when a handler fails, so later commands still go out.
func TestMQTTDiscoverSnippet_GuardSurvivesHandlerError(t *testing.T) {
	d := DiscoveredDevice{Source: "tasmota", ID: "t2", Name: "Lampka"}
	runSnippet(t, "", generateSnippet(d, defaultBroker), `
		local h = ExposedObjects:get("tasmota:t2")
		h.object:onSwitchOn(function() error("user handler") end)
		deliver("stat/t2/POWER", "ON")
		sent()
		h:setValue(0)
		local s = sent()
		assert(s == "cmnd/t2/POWER=OFF", "command after a failing handler: " .. s)
	`)
}

func TestParseZigbee2MQTTDevices_PayloadKeys(t *testing.T) {
	payload := `[
	 {"ieee_address":"0x1","friendly_name":"jeden","type":"Router","definition":{"model":"A","exposes":[
	   {"type":"switch","features":[{"name":"state","property":"state","type":"binary"}]}]}},
	 {"ieee_address":"0x2","friendly_name":"dwa","type":"Router","definition":{"model":"B","exposes":[
	   {"type":"switch","endpoint":"l1","features":[{"name":"state","property":"state_l1","type":"binary"}]},
	   {"type":"switch","endpoint":"l2","features":[{"name":"state","property":"state_l2","type":"binary"}]}]}},
	 {"ieee_address":"0x3","friendly_name":"temp","type":"EndDevice","definition":{"model":"C","exposes":[
	   {"type":"numeric","name":"temperature","property":"temperature_l1"},
	   {"type":"numeric","name":"humidity","property":"humidity"}]}}
	]`
	devs := map[string]DiscoveredDevice{}
	for _, d := range parseZigbee2MQTTDevices(payload) {
		devs[d.Name] = d
	}
	if d := devs["jeden"]; d.stateKey != "state" || generateSnippet(d, defaultBroker) == "" {
		t.Errorf("single endpoint: stateKey %q", d.stateKey)
	}
	if d := devs["dwa"]; d.stateKey != "state_l1" || generateSnippet(d, defaultBroker) != "" {
		t.Errorf("multi endpoint: stateKey %q, want no snippet", d.stateKey)
	}
	d := devs["temp"]
	if strings.Join(d.Features, ",") != "temperature_l1,humidity" {
		t.Errorf("features from property: %v", d.Features)
	}
	s := generateSnippet(d, defaultBroker)
	if strings.Contains(s, ":temperature") || !strings.Contains(s, `data["humidity"]`) {
		t.Errorf("only humidity is a known key:\n%s", s)
	}
}
