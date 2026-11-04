package server

import (
	"net"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/Domktorymysli/vclu/pkg/broker"
	"github.com/Domktorymysli/vclu/pkg/config"
	"github.com/Domktorymysli/vclu/pkg/lua"
)

// startBroker starts the embedded broker on a free port.
func startBroker(t *testing.T) (*broker.Broker, int) {
	t.Helper()
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
	return b, port
}

// startServerWithModule runs the start sequence of main (RunLoop from Start, then LoadInitialData) with one module
// that subscribes in its init, and an MQTT client configured for the broker on port.
func startServerWithModule(t *testing.T, port int, module string) *lua.LuaEngine {
	t.Helper()
	dir := t.TempDir()
	for _, sub := range []string{"a", "modules"} {
		if err := os.MkdirAll(filepath.Join(dir, sub), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "modules", "sub.lua"), []byte(module), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := config.SaveMQTTClientConfig(dir, &config.VCLUMQTTClientConfig{
		Enabled: true, Host: "127.0.0.1", Port: port, ClientID: "vclu_start_test", HAIntegration: true,
	}); err != nil {
		t.Fatal(err)
	}

	engine := lua.NewLuaEngine()
	s := &Server{
		serverDir:    dir,
		deviceConfig: &config.CLUDeviceConfig{FirmwareVersionStr: "test"},
		luaEngine:    engine,
	}
	stop := make(chan struct{})
	go engine.RunLoop(stop)
	t.Cleanup(func() {
		close(stop)
		engine.Close()
	})
	s.LoadInitialData()
	return engine
}

// waitFor publishes on topic until the Lua expression check is "true".
func waitFor(t *testing.T, b *broker.Broker, engine *lua.LuaEngine, topic, check string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		_ = b.Publish(topic, []byte("1"), false)
		time.Sleep(50 * time.Millisecond)
		if got, _ := engine.Execute(check); got == "true" {
			return
		}
		if time.Now().After(deadline) {
			got, _ := engine.Execute(`tostring(GOT.n)`)
			t.Fatalf("no message on %s after start (callbacks: %s)", topic, got)
		}
	}
}

// A module that subscribes in its init, before the client exists, gets messages after a fresh start (#177).
func TestModuleSubscribeOnStartReceivesMessages(t *testing.T) {
	b, port := startBroker(t)
	engine := startServerWithModule(t, port, `
		GOT = { n = 0 }
		MQTT:subscribe("test/start", function() GOT.n = GOT.n + 1 end)
	`)
	// Go has the topic once the start returns ("MQTT reconnected (0 subscriptions restored)" before #177)
	if subs := engine.GetMQTTManager().Stats().Clients[0].Subscriptions; !slices.Contains(subs, "test/start") {
		t.Errorf("subscriptions after start = %v, want test/start", subs)
	}
	waitFor(t, b, engine, "test/start", `tostring(GOT.n > 0)`)
}

// After a broker restart the topic is subscribed again, and each message reaches the callback once.
func TestModuleSubscribeSurvivesBrokerRestartOnce(t *testing.T) {
	b, port := startBroker(t)
	engine := startServerWithModule(t, port, `
		GOT = { n = 0 }
		MQTT:subscribe("test/restart", function() GOT.n = GOT.n + 1 end)
	`)
	waitFor(t, b, engine, "test/restart", `tostring(GOT.n > 0)`)

	if err := b.Stop(); err != nil {
		t.Fatal(err)
	}
	b2 := broker.New(&broker.Config{Enabled: true, Port: port})
	if err := b2.Start(); err != nil {
		t.Fatalf("broker restart: %v", err)
	}
	t.Cleanup(func() { _ = b2.Stop() })
	_, _ = engine.Execute(`GOT.n = 0`)
	waitFor(t, b2, engine, "test/restart", `tostring(GOT.n > 0)`)

	// One publish, one callback: no handler or broker subscription doubled by the reconnect
	time.Sleep(300 * time.Millisecond)
	_, _ = engine.Execute(`GOT.n = 0`)
	_ = b2.Publish("test/restart", []byte("1"), false)
	time.Sleep(300 * time.Millisecond)
	if got, _ := engine.Execute(`tostring(GOT.n)`); got != "1" {
		t.Errorf("one message gave %s callbacks, want 1", got)
	}
}
