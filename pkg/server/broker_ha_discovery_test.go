package server

import (
	"net"
	"strings"
	"testing"
	"time"

	"github.com/Domktorymysli/vclu/pkg/broker"
	"github.com/Domktorymysli/vclu/pkg/lua"
	glua "github.com/yuin/gopher-lua"
)

// The HA integration is set up once, on the first connect, never while the connect is pending.
func TestBrokerConnectSetsUpHAOnceConnected(t *testing.T) {
	L := glua.NewState()
	defer L.Close()
	if err := L.DoString(`
		got = { setups = 0 }
		MQTT = { handlers = {} }
		function MQTT:setHost() return self end
		function MQTT:setPort() return self end
		function MQTT:setClientId() return self end
		function MQTT:setCredentials() return self end
		function MQTT:onConnect(fn) table.insert(self.handlers, fn); return self end
		function MQTT:connect() return true end
		function emitConnect() for _, fn in ipairs(MQTT.handlers) do fn() end end
		HAIntegration = { setup = function() got.setups = got.setups + 1 end }
	`); err != nil {
		t.Fatal(err)
	}
	if err := L.DoString(brokerConnectScript(1883, "", "")); err != nil {
		t.Fatalf("script: %v", err)
	}
	setups := func() string { return L.GetGlobal("got").(*glua.LTable).RawGetString("setups").String() }
	if n := setups(); n != "0" {
		t.Errorf("setup ran %s times before the connect", n)
	}
	for i := 0; i < 2; i++ {
		if err := L.DoString(`emitConnect()`); err != nil {
			t.Fatal(err)
		}
	}
	if n := setups(); n != "1" {
		t.Errorf("setup ran %s times after two connects, want 1", n)
	}
}

// Embedded broker, no mqttClient: a subscriber gets the discovery configs and vclu/status.
func TestBrokerConnectPublishesHADiscovery(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	_ = l.Close()
	b := broker.New(&broker.Config{Enabled: true, Port: port})
	if err := b.Start(); err != nil {
		t.Fatalf("broker start: %v", err)
	}
	t.Cleanup(func() { _ = b.Stop() })

	engine := lua.NewLuaEngine()
	defer engine.Close()
	if _, err := engine.Execute(`
		local sw = RemoteSwitch:new({ executeNoReply = function() end }, "DOU1", "LAMPA")
		registerObject("CLU1.DOU1", sw)
		expose(sw, "switch", { path = "salon.lampa" })
	`); err != nil {
		t.Fatal(err)
	}
	if _, err := engine.Execute(brokerConnectScript(port, "", "")); err != nil {
		t.Fatalf("connect: %v", err)
	}

	deadline := time.Now().Add(10 * time.Second)
	for {
		_, _ = engine.Execute(`MQTT.pollEvents(); MQTT.pollMessages()`)
		status, config := "", ""
		for _, m := range b.GetRetainedByFilter("#") {
			switch {
			case m.Topic == "vclu/status":
				status = string(m.Payload)
			case strings.HasPrefix(m.Topic, "homeassistant/") && strings.HasSuffix(m.Topic, "/config") && len(m.Payload) > 0:
				config = string(m.Payload)
			}
		}
		if status == "online" && strings.Contains(config, "salon_lampa") {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("no discovery on the broker: vclu/status %q, config %q", status, config)
		}
		time.Sleep(20 * time.Millisecond)
	}
}
