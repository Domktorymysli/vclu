package lua

import (
	"testing"
	"time"
)

func TestMQTTManagerCreate(t *testing.T) {
	mm := NewMQTTManager()
	if mm == nil {
		t.Fatal("Expected non-nil MQTTManager")
	}
	if mm.clients == nil {
		t.Error("Expected clients map to be initialized")
	}
	if mm.incoming == nil {
		t.Error("Expected incoming channel to be initialized")
	}
	if mm.events == nil {
		t.Error("Expected events channel to be initialized")
	}
}

func TestMQTTStatsEmpty(t *testing.T) {
	mm := NewMQTTManager()
	stats := mm.Stats()

	if stats.TotalClients != 0 {
		t.Errorf("Expected 0 total clients, got %d", stats.TotalClients)
	}
	if stats.ConnectedClients != 0 {
		t.Errorf("Expected 0 connected clients, got %d", stats.ConnectedClients)
	}
	if len(stats.Clients) != 0 {
		t.Errorf("Expected 0 clients, got %d", len(stats.Clients))
	}
}

func TestMQTTManagerClose(t *testing.T) {
	mm := NewMQTTManager()
	// Close should not panic even with no clients
	mm.Close()
}

func TestMQTTIsConnectedNonExistent(t *testing.T) {
	mm := NewMQTTManager()
	if mm.IsConnected("non-existent") {
		t.Error("Expected IsConnected to return false for non-existent client")
	}
}

func TestMQTTDisconnectNonExistent(t *testing.T) {
	mm := NewMQTTManager()
	// Should not panic
	mm.Disconnect("non-existent")
}

func TestMQTTPublishNonExistent(t *testing.T) {
	mm := NewMQTTManager()
	err := mm.Publish("non-existent", "topic", "payload", 0, false)
	if err == nil {
		t.Error("Expected error when publishing to non-existent client")
	}
}

func TestMQTTSubscribeNonExistent(t *testing.T) {
	mm := NewMQTTManager()
	err := mm.Subscribe("non-existent", "topic", 0)
	if err == nil {
		t.Error("Expected error when subscribing with non-existent client")
	}
}

func TestMQTTUnsubscribeNonExistent(t *testing.T) {
	mm := NewMQTTManager()
	err := mm.Unsubscribe("non-existent", "topic")
	if err == nil {
		t.Error("Expected error when unsubscribing from non-existent client")
	}
}

func TestMQTTGetPendingMessageEmpty(t *testing.T) {
	mm := NewMQTTManager()
	msg := mm.GetPendingMessage()
	if msg != nil {
		t.Error("Expected nil message when no messages pending")
	}
}

// TestMQTTConnectInvalidBroker tests connecting to an invalid broker
func TestMQTTConnectInvalidBroker(t *testing.T) {
	mm := NewMQTTManager()

	// Try to connect to a non-existent broker (should fail/timeout)
	err := mm.Connect("test", "tcp://127.0.0.1", 19999, "test-client", "", "", 5, true)

	// We expect either timeout or connection refused
	if err == nil {
		// If somehow connected, clean up
		mm.Disconnect("test")
	}
	// Error is expected since broker doesn't exist
}

// TestMQTTLuaEngineIntegration tests that MQTT stats work through LuaEngine
func TestMQTTLuaEngineIntegration(t *testing.T) {
	engine := NewLuaEngine()
	defer engine.Close()

	stats := engine.GetMQTTStats()
	if stats.TotalClients != 0 {
		t.Errorf("Expected 0 total clients from engine, got %d", stats.TotalClients)
	}
}

// --- Async MQTT tests (Phase 3) ---

func TestMQTTPublishFireAndForget(t *testing.T) {
	mm := NewMQTTManager()
	defer mm.Close()

	// Publish to non-existent client returns error immediately (client lookup, not blocking)
	err := mm.Publish("non-existent", "test/topic", "hello", 0, false)
	if err == nil {
		t.Error("Expected error for non-existent client")
	}
}

func TestMQTTPublishWhenDisconnected(t *testing.T) {
	mm := NewMQTTManager()
	defer mm.Close()

	// Register a wrapper without connecting (simulate disconnected state)
	mm.mu.Lock()
	mm.clients["test"] = &mqttClientWrapper{
		instanceID:    "test",
		subscriptions: make([]string, 0),
		// client is nil — not connected
	}
	mm.mu.Unlock()

	err := mm.Publish("test", "test/topic", "hello", 0, false)
	if err == nil {
		t.Error("Expected error for nil client")
	}
}

func TestMQTTConnectAsync(t *testing.T) {
	mm := NewMQTTManager()
	defer mm.Close()

	// ConnectAsync to invalid broker — should return immediately, event appears on channel
	mm.ConnectAsync("test-async", "tcp://127.0.0.1", 19999, "test-client", "", "", 5, true)

	// Wait for event with timeout
	select {
	case ev := <-mm.events:
		if ev.Type != "connect_result" {
			t.Errorf("Expected connect_result event, got %s", ev.Type)
		}
		if ev.InstanceID != "test-async" {
			t.Errorf("Expected instanceID test-async, got %s", ev.InstanceID)
		}
		// Error expected (no broker running)
		if ev.Error == "" {
			t.Log("Connect succeeded (unexpected — broker might be running)")
		}
	case <-time.After(15 * time.Second):
		t.Fatal("Timed out waiting for connect_result event")
	}
}

func TestMQTTSubscribeAsync(t *testing.T) {
	mm := NewMQTTManager()
	defer mm.Close()

	// SubscribeAsync to non-existent client — event with error
	mm.SubscribeAsync("non-existent", "test/topic", 0)

	select {
	case ev := <-mm.events:
		if ev.Type != "subscribe_result" {
			t.Errorf("Expected subscribe_result event, got %s", ev.Type)
		}
		if ev.InstanceID != "non-existent" {
			t.Errorf("Expected instanceID non-existent, got %s", ev.InstanceID)
		}
		if ev.Topic != "test/topic" {
			t.Errorf("Expected topic test/topic, got %s", ev.Topic)
		}
		if ev.Error == "" {
			t.Error("Expected error for non-existent client")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Timed out waiting for subscribe_result event")
	}
}

func TestMQTTEventChannelBlocking(t *testing.T) {
	mm := NewMQTTManager()
	defer mm.Close()

	// Fill events channel to capacity (256)
	for i := 0; i < 256; i++ {
		mm.events <- &mqttEvent{Type: "test", InstanceID: "fill"}
	}

	// Next send should block (not panic, not drop)
	done := make(chan bool, 1)
	go func() {
		mm.events <- &mqttEvent{Type: "test", InstanceID: "overflow"}
		done <- true
	}()

	// Goroutine should be blocked
	select {
	case <-done:
		t.Fatal("Expected goroutine to block on full channel")
	case <-time.After(100 * time.Millisecond):
		// Expected — goroutine is blocked
	}

	// Drain one event to unblock
	<-mm.events

	// Now the blocked goroutine should complete
	select {
	case <-done:
		// Success
	case <-time.After(1 * time.Second):
		t.Fatal("Goroutine did not unblock after draining channel")
	}
}

func TestMQTTPollEventBridge(t *testing.T) {
	mm := NewMQTTManager()
	defer mm.Close()

	// GetPendingEvent on empty channel returns nil
	ev := mm.GetPendingEvent()
	if ev != nil {
		t.Error("Expected nil event on empty channel")
	}

	// Push an event and retrieve it
	mm.events <- &mqttEvent{
		Type:       "connect_result",
		InstanceID: "test-instance",
		Topic:      "",
		Error:      "",
	}

	ev = mm.GetPendingEvent()
	if ev == nil {
		t.Fatal("Expected non-nil event")
	}
	if ev.Type != "connect_result" {
		t.Errorf("Expected connect_result, got %s", ev.Type)
	}
	if ev.InstanceID != "test-instance" {
		t.Errorf("Expected test-instance, got %s", ev.InstanceID)
	}

	// Channel should be empty again
	ev = mm.GetPendingEvent()
	if ev != nil {
		t.Error("Expected nil event after draining")
	}
}

func TestMQTTConnectAsyncReturnsImmediately(t *testing.T) {
	mm := NewMQTTManager()
	defer mm.Close()

	// ConnectAsync should return in <100ms even though Connect blocks for 10s
	start := time.Now()
	mm.ConnectAsync("instant-test", "tcp://192.0.2.1", 19999, "test-client", "", "", 5, true)
	elapsed := time.Since(start)

	if elapsed > 100*time.Millisecond {
		t.Errorf("ConnectAsync took %v, expected <100ms (non-blocking)", elapsed)
	}

	// Clean up: drain event (may take up to 10s for connect timeout)
	select {
	case <-mm.events:
	case <-time.After(15 * time.Second):
		t.Log("Warning: connect event not received within 15s")
	}
}
