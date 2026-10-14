package lua

import (
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	mqtt "github.com/eclipse/paho.mqtt.golang"
	lua "github.com/yuin/gopher-lua"

	"github.com/Domktorymysli/vclu/pkg/stats"
)

// MQTTManager manages MQTT client instances for Lua
type MQTTManager struct {
	mu              sync.RWMutex
	clients         map[string]*mqttClientWrapper
	incoming        chan *mqttMessage // channel for incoming messages
	events          chan *mqttEvent   // channel for async lifecycle events (connect/subscribe results)
	maxBuffer       int
	droppedMessages int64 // counter for dropped messages when buffer full
	debug           bool  // enable verbose logging
	metrics         *stats.AppMetrics
}

// mqttClientWrapper wraps an MQTT client with its configuration
type mqttClientWrapper struct {
	client        mqtt.Client
	instanceID    string
	url           string
	port          int
	clientID      string
	username      string
	password      string
	connected     bool
	connectedAt   time.Time
	lastError     string
	messageCount  int64
	subscriptions []string
}

// MQTTClientCredentials holds connection credentials for an MQTT client instance.
type MQTTClientCredentials struct {
	URL      string
	Port     int
	Username string
	Password string
}

// mqttMessage represents an incoming MQTT message
type mqttMessage struct {
	InstanceID string
	Topic      string
	Payload    string
	QoS        byte
	Retained   bool
}

// mqttEvent represents an async lifecycle event (connect result, subscribe result, connection lost, etc.)
type mqttEvent struct {
	Type       string // "connect_result", "subscribe_result", "connection_lost"
	InstanceID string
	Topic      string // for subscribe_result
	Error      string // "" on success
}

// MQTTClientStatus represents the status of an MQTT client
type MQTTClientStatus struct {
	InstanceID    string    `json:"instanceId"`
	URL           string    `json:"url"`
	Port          int       `json:"port"`
	ClientID      string    `json:"clientId"`
	Connected     bool      `json:"connected"`
	ConnectedAt   time.Time `json:"connectedAt,omitempty"`
	LastError     string    `json:"lastError,omitempty"`
	MessageCount  int64     `json:"messageCount"`
	Subscriptions []string  `json:"subscriptions"`
}

// MQTTStats represents overall MQTT statistics
type MQTTStats struct {
	TotalClients     int                `json:"totalClients"`
	ConnectedClients int                `json:"connectedClients"`
	DroppedMessages  int64              `json:"droppedMessages"`
	BufferSize       int                `json:"bufferSize"`
	BufferUsed       int                `json:"bufferUsed"`
	Clients          []MQTTClientStatus `json:"clients"`
}

// NewMQTTManager creates a new MQTT manager
func NewMQTTManager() *MQTTManager {
	return &MQTTManager{
		clients:   make(map[string]*mqttClientWrapper),
		incoming:  make(chan *mqttMessage, 1000),
		events:    make(chan *mqttEvent, 256),
		maxBuffer: 1000,
	}
}

// SetMetrics sets the AppMetrics instance for MQTT stats tracking.
func (mm *MQTTManager) SetMetrics(m *stats.AppMetrics) {
	mm.metrics = m
}

// GetMetrics returns the AppMetrics instance (used to preserve across reinitialize).
func (mm *MQTTManager) GetMetrics() *stats.AppMetrics {
	return mm.metrics
}

// Connect connects to an MQTT broker
func (mm *MQTTManager) Connect(instanceID, url string, port int, clientID, username, password string, keepAlive int, cleanSession bool) error {
	mm.mu.Lock()
	// Check if already connected
	if existing, ok := mm.clients[instanceID]; ok && existing.client != nil && existing.client.IsConnected() {
		mm.mu.Unlock()
		return nil
	}
	mm.mu.Unlock()

	// Build broker URL - ensure protocol prefix
	if url == "" {
		url = "tcp://localhost"
	} else if !strings.HasPrefix(url, "tcp://") && !strings.HasPrefix(url, "ssl://") && !strings.HasPrefix(url, "ws://") && !strings.HasPrefix(url, "wss://") {
		url = "tcp://" + url
	}
	brokerURL := fmt.Sprintf("%s:%d", url, port)

	log.Printf("[MQTT] %s connecting to %s", instanceID, brokerURL)

	opts := mqtt.NewClientOptions().
		AddBroker(brokerURL).
		SetClientID(clientID).
		SetKeepAlive(time.Duration(keepAlive) * time.Second).
		SetCleanSession(cleanSession).
		SetAutoReconnect(true).
		SetConnectRetry(true).
		SetConnectRetryInterval(5 * time.Second)

	if username != "" {
		opts.SetUsername(username)
		if password != "" {
			opts.SetPassword(password)
		}
	}

	// Create wrapper
	wrapper := &mqttClientWrapper{
		instanceID:    instanceID,
		url:           url,
		port:          port,
		clientID:      clientID,
		username:      username,
		password:      password,
		subscriptions: make([]string, 0),
	}

	// Set connection handlers
	opts.SetOnConnectHandler(func(c mqtt.Client) {
		mm.mu.Lock()
		if w, ok := mm.clients[instanceID]; ok {
			w.connected = true
			w.connectedAt = time.Now()
			w.lastError = ""
		}
		mm.mu.Unlock()
		if mm.metrics != nil {
			mm.metrics.SetMQTTConnected(true)
		}
		// Notify Lua about (re)connection — enables subscription recovery
		select {
		case mm.events <- &mqttEvent{Type: "connect_result", InstanceID: instanceID, Error: ""}:
		default:
		}
	})

	opts.SetConnectionLostHandler(func(c mqtt.Client, err error) {
		log.Printf("[MQTT] %s connection lost: %v", instanceID, err)
		mm.mu.Lock()
		if w, ok := mm.clients[instanceID]; ok {
			w.connected = false
			w.lastError = err.Error()
		}
		mm.mu.Unlock()
		if mm.metrics != nil {
			mm.metrics.SetMQTTConnected(false)
		}
		// Notify Lua about connection loss
		select {
		case mm.events <- &mqttEvent{Type: "connection_lost", InstanceID: instanceID, Error: err.Error()}:
		default:
		}
	})

	opts.SetReconnectingHandler(func(c mqtt.Client, opts *mqtt.ClientOptions) {
		if mm.metrics != nil {
			mm.metrics.RecordMQTTReconnect()
		}
	})

	// Default message handler
	opts.SetDefaultPublishHandler(func(c mqtt.Client, m mqtt.Message) {
		if mm.debug {
			log.Printf("[MQTT] %s received: %s = %s", instanceID, m.Topic(), string(m.Payload()))
		}
		if mm.metrics != nil {
			mm.metrics.RecordMQTTReceive()
		}
		msg := &mqttMessage{
			InstanceID: instanceID,
			Topic:      m.Topic(),
			Payload:    string(m.Payload()),
			QoS:        m.Qos(),
			Retained:   m.Retained(),
		}
		select {
		case mm.incoming <- msg:
			// Queued successfully
		default:
			mm.mu.Lock()
			mm.droppedMessages++
			mm.mu.Unlock()
			log.Printf("[MQTT] %s incoming message buffer full, dropping message (total dropped: %d)", instanceID, mm.droppedMessages)
		}
	})

	// Create and connect client
	client := mqtt.NewClient(opts)
	wrapper.client = client

	// Register wrapper BEFORE Connect() so OnConnectHandler can find it,
	// and AutoReconnect keeps working even if initial connect fails.
	mm.mu.Lock()
	mm.clients[instanceID] = wrapper
	mm.mu.Unlock()

	token := client.Connect()
	if !token.WaitTimeout(10 * time.Second) {
		mm.mu.Lock()
		wrapper.lastError = "connection timeout (auto-reconnect active)"
		mm.mu.Unlock()
		log.Printf("[MQTT] %s initial connect timeout, auto-reconnect will retry", instanceID)
		return fmt.Errorf("connection timeout (auto-reconnect active)")
	}
	if err := token.Error(); err != nil {
		mm.mu.Lock()
		wrapper.lastError = err.Error()
		mm.mu.Unlock()
		log.Printf("[MQTT] %s initial connect failed: %v, auto-reconnect will retry", instanceID, err)
		return fmt.Errorf("connection failed: %w (auto-reconnect active)", err)
	}

	// The OnConnect handler writes these too, under the lock
	mm.mu.Lock()
	wrapper.connected = true
	wrapper.connectedAt = time.Now()
	mm.mu.Unlock()

	log.Printf("[MQTT] %s connected", instanceID)
	return nil
}

// ConnectAsync starts a connection to an MQTT broker in a background goroutine.
// On success, the OnConnectHandler sends a "connect_result" event (covers initial + auto-reconnect).
// On error, this method sends the "connect_result" event with the error.
func (mm *MQTTManager) ConnectAsync(instanceID, url string, port int, clientID, username, password string, keepAlive int, cleanSession bool) {
	go func() {
		err := mm.Connect(instanceID, url, port, clientID, username, password, keepAlive, cleanSession)
		if err != nil {
			// Only send on error — success event is sent by OnConnectHandler inside Connect()
			mm.events <- &mqttEvent{Type: "connect_result", InstanceID: instanceID, Error: err.Error()}
		}
	}()
}

// Disconnect disconnects from an MQTT broker
func (mm *MQTTManager) Disconnect(instanceID string) {
	mm.mu.Lock()
	defer mm.mu.Unlock()

	if wrapper, ok := mm.clients[instanceID]; ok && wrapper.client != nil {
		wrapper.client.Disconnect(1000) // 1 second timeout
		wrapper.connected = false
		delete(mm.clients, instanceID)
		if mm.metrics != nil {
			mm.metrics.SetMQTTConnected(false)
		}
	}
}

// Destroy removes the client at once, so a new client with the same id gets its own connection,
// then unsubscribes its topics on the broker and disconnects in the background.
func (mm *MQTTManager) Destroy(instanceID string) {
	mm.mu.Lock()
	wrapper, ok := mm.clients[instanceID]
	if !ok || wrapper.client == nil {
		mm.mu.Unlock()
		return
	}
	delete(mm.clients, instanceID)
	subs := append([]string(nil), wrapper.subscriptions...)
	mm.mu.Unlock()
	if mm.metrics != nil {
		mm.metrics.SetMQTTConnected(false)
	}

	go func() {
		if len(subs) > 0 && wrapper.client.IsConnected() {
			wrapper.client.Unsubscribe(subs...).WaitTimeout(2 * time.Second)
		}
		wrapper.client.Disconnect(1000)
	}()
}

// Publish publishes a message to a topic
func (mm *MQTTManager) Publish(instanceID, topic, payload string, qos int, retain bool) error {
	// Validate QoS
	if qos < 0 || qos > 2 {
		return fmt.Errorf("invalid QoS level %d (must be 0, 1, or 2)", qos)
	}

	mm.mu.RLock()
	wrapper, ok := mm.clients[instanceID]
	mm.mu.RUnlock()

	if !ok || wrapper.client == nil {
		return fmt.Errorf("client %s not found", instanceID)
	}

	if !wrapper.client.IsConnected() {
		return fmt.Errorf("client %s not connected", instanceID)
	}

	token := wrapper.client.Publish(topic, byte(qos), retain, payload)

	// Fire-and-forget: count optimistically, log errors in background goroutine
	mm.mu.Lock()
	wrapper.messageCount++
	mm.mu.Unlock()

	if mm.metrics != nil {
		mm.metrics.RecordMQTTPublish()
	}

	go func() {
		if !token.WaitTimeout(5 * time.Second) {
			log.Printf("[MQTT] %s publish to %s timed out", instanceID, topic)
		} else if err := token.Error(); err != nil {
			log.Printf("[MQTT] %s publish to %s failed: %v", instanceID, topic, err)
		}
	}()

	return nil
}

// Subscribe subscribes to a topic
func (mm *MQTTManager) Subscribe(instanceID, topic string, qos int) error {
	// Validate QoS
	if qos < 0 || qos > 2 {
		return fmt.Errorf("invalid QoS level %d (must be 0, 1, or 2)", qos)
	}

	mm.mu.RLock()
	wrapper, ok := mm.clients[instanceID]
	mm.mu.RUnlock()

	if !ok || wrapper.client == nil {
		return fmt.Errorf("client %s not found", instanceID)
	}

	if !wrapper.client.IsConnected() {
		return fmt.Errorf("client %s not connected", instanceID)
	}

	// Subscribe without holding lock (blocking operation)
	token := wrapper.client.Subscribe(topic, byte(qos), nil)
	if !token.WaitTimeout(5 * time.Second) {
		return fmt.Errorf("subscribe timeout")
	}
	if err := token.Error(); err != nil {
		return fmt.Errorf("subscribe failed: %w", err)
	}

	// Add to subscriptions list (avoid duplicates)
	mm.mu.Lock()
	found := false
	for _, s := range wrapper.subscriptions {
		if s == topic {
			found = true
			break
		}
	}
	if !found {
		wrapper.subscriptions = append(wrapper.subscriptions, topic)
	}
	mm.mu.Unlock()

	return nil
}

// SubscribeAsync starts a subscription in a background goroutine.
// The result is delivered via the events channel as a "subscribe_result" event.
func (mm *MQTTManager) SubscribeAsync(instanceID, topic string, qos int) {
	go func() {
		err := mm.Subscribe(instanceID, topic, qos)
		errStr := ""
		if err != nil {
			errStr = err.Error()
		}
		// Blocking send — goroutine can wait, doesn't block RunLoop.
		mm.events <- &mqttEvent{Type: "subscribe_result", InstanceID: instanceID, Topic: topic, Error: errStr}
	}()
}

// Unsubscribe unsubscribes from a topic
func (mm *MQTTManager) Unsubscribe(instanceID, topic string) error {
	mm.mu.RLock()
	wrapper, ok := mm.clients[instanceID]
	mm.mu.RUnlock()

	if !ok || wrapper.client == nil {
		return fmt.Errorf("client %s not found", instanceID)
	}

	token := wrapper.client.Unsubscribe(topic)
	if !token.WaitTimeout(5 * time.Second) {
		return fmt.Errorf("unsubscribe timeout")
	}
	if err := token.Error(); err != nil {
		return fmt.Errorf("unsubscribe failed: %w", err)
	}

	// Remove from subscriptions list
	mm.mu.Lock()
	newSubs := make([]string, 0)
	for _, s := range wrapper.subscriptions {
		if s != topic {
			newSubs = append(newSubs, s)
		}
	}
	wrapper.subscriptions = newSubs
	mm.mu.Unlock()

	return nil
}

// GetPendingMessage returns next pending incoming message (non-blocking)
func (mm *MQTTManager) GetPendingMessage() *mqttMessage {
	select {
	case msg := <-mm.incoming:
		return msg
	default:
		return nil
	}
}

// GetPendingEvent returns the next pending lifecycle event (non-blocking)
func (mm *MQTTManager) GetPendingEvent() *mqttEvent {
	select {
	case ev := <-mm.events:
		return ev
	default:
		return nil
	}
}

// IsConnected checks if a client is connected
func (mm *MQTTManager) IsConnected(instanceID string) bool {
	mm.mu.RLock()
	defer mm.mu.RUnlock()

	if wrapper, ok := mm.clients[instanceID]; ok && wrapper.client != nil {
		return wrapper.client.IsConnected()
	}
	return false
}

// Stats returns MQTT statistics
func (mm *MQTTManager) Stats() MQTTStats {
	mm.mu.RLock()
	defer mm.mu.RUnlock()

	stats := MQTTStats{
		TotalClients:    len(mm.clients),
		DroppedMessages: mm.droppedMessages,
		BufferSize:      mm.maxBuffer,
		BufferUsed:      len(mm.incoming),
		Clients:         make([]MQTTClientStatus, 0, len(mm.clients)),
	}

	for _, wrapper := range mm.clients {
		connected := wrapper.client != nil && wrapper.client.IsConnected()
		if connected {
			stats.ConnectedClients++
		}

		status := MQTTClientStatus{
			InstanceID:    wrapper.instanceID,
			URL:           wrapper.url,
			Port:          wrapper.port,
			ClientID:      wrapper.clientID,
			Connected:     connected,
			ConnectedAt:   wrapper.connectedAt,
			LastError:     wrapper.lastError,
			MessageCount:  wrapper.messageCount,
			Subscriptions: wrapper.subscriptions,
		}
		stats.Clients = append(stats.Clients, status)
	}

	return stats
}

// GetClientCredentials returns connection credentials for a specific MQTT client instance.
func (mm *MQTTManager) GetClientCredentials(instanceID string) *MQTTClientCredentials {
	mm.mu.RLock()
	defer mm.mu.RUnlock()
	w, ok := mm.clients[instanceID]
	if !ok {
		return nil
	}
	return &MQTTClientCredentials{URL: w.url, Port: w.port, Username: w.username, Password: w.password}
}

// Reconnect disconnects an existing client and reconnects with new parameters,
// preserving and re-subscribing all tracked subscriptions.
func (mm *MQTTManager) Reconnect(instanceID, host string, port int, clientID, username, password string) error {
	mm.mu.Lock()
	// Collect existing subscriptions before disconnect
	var savedSubs []string
	if existing, ok := mm.clients[instanceID]; ok {
		savedSubs = make([]string, len(existing.subscriptions))
		copy(savedSubs, existing.subscriptions)
		if existing.client != nil {
			existing.client.Disconnect(1000)
		}
		delete(mm.clients, instanceID)
	}
	mm.mu.Unlock()

	// Build broker URL
	url := host
	if url == "" {
		url = "tcp://localhost"
	} else if !strings.HasPrefix(url, "tcp://") && !strings.HasPrefix(url, "ssl://") && !strings.HasPrefix(url, "ws://") && !strings.HasPrefix(url, "wss://") {
		url = "tcp://" + url
	}
	brokerURL := fmt.Sprintf("%s:%d", url, port)

	if clientID == "" {
		clientID = "vclu_internal"
	}

	log.Printf("[MQTT] %s reconnecting to %s", instanceID, brokerURL)

	opts := mqtt.NewClientOptions().
		AddBroker(brokerURL).
		SetClientID(clientID).
		SetKeepAlive(60*time.Second).
		SetCleanSession(true).
		SetAutoReconnect(true).
		SetConnectRetry(true).
		SetConnectRetryInterval(5*time.Second).
		SetWill("vclu/status", "offline", 0, true)

	if username != "" {
		opts.SetUsername(username)
		if password != "" {
			opts.SetPassword(password)
		}
	}

	wrapper := &mqttClientWrapper{
		instanceID:    instanceID,
		url:           url,
		port:          port,
		clientID:      clientID,
		username:      username,
		password:      password,
		subscriptions: savedSubs,
	}

	// Re-subscribe tracked topics on every connect/reconnect
	opts.SetOnConnectHandler(func(c mqtt.Client) {
		mm.mu.Lock()
		if w, ok := mm.clients[instanceID]; ok {
			w.connected = true
			w.connectedAt = time.Now()
			w.lastError = ""
			// Re-subscribe saved topics
			for _, topic := range w.subscriptions {
				c.Subscribe(topic, 0, nil)
			}
		}
		mm.mu.Unlock()
		if mm.metrics != nil {
			mm.metrics.SetMQTTConnected(true)
		}
		// Notify Lua about (re)connection — enables subscription recovery
		select {
		case mm.events <- &mqttEvent{Type: "connect_result", InstanceID: instanceID, Error: ""}:
		default:
		}
		// Publish online status
		c.Publish("vclu/status", 0, true, "online")
	})

	opts.SetConnectionLostHandler(func(c mqtt.Client, err error) {
		log.Printf("[MQTT] %s connection lost: %v", instanceID, err)
		mm.mu.Lock()
		if w, ok := mm.clients[instanceID]; ok {
			w.connected = false
			w.lastError = err.Error()
		}
		mm.mu.Unlock()
		if mm.metrics != nil {
			mm.metrics.SetMQTTConnected(false)
		}
		// Notify Lua about connection loss
		select {
		case mm.events <- &mqttEvent{Type: "connection_lost", InstanceID: instanceID, Error: err.Error()}:
		default:
		}
	})

	opts.SetReconnectingHandler(func(c mqtt.Client, opts *mqtt.ClientOptions) {
		log.Printf("[MQTT] %s auto-reconnecting...", instanceID)
		if mm.metrics != nil {
			mm.metrics.RecordMQTTReconnect()
		}
	})

	opts.SetDefaultPublishHandler(func(c mqtt.Client, m mqtt.Message) {
		if mm.debug {
			log.Printf("[MQTT] %s received: %s = %s", instanceID, m.Topic(), string(m.Payload()))
		}
		if mm.metrics != nil {
			mm.metrics.RecordMQTTReceive()
		}
		msg := &mqttMessage{
			InstanceID: instanceID,
			Topic:      m.Topic(),
			Payload:    string(m.Payload()),
			QoS:        m.Qos(),
			Retained:   m.Retained(),
		}
		select {
		case mm.incoming <- msg:
		default:
			mm.mu.Lock()
			mm.droppedMessages++
			mm.mu.Unlock()
		}
	})

	client := mqtt.NewClient(opts)
	wrapper.client = client

	// Register wrapper BEFORE Connect() so OnConnectHandler can find it,
	// and AutoReconnect keeps working even if initial connect fails.
	mm.mu.Lock()
	mm.clients[instanceID] = wrapper
	mm.mu.Unlock()

	token := client.Connect()
	if !token.WaitTimeout(10 * time.Second) {
		wrapper.lastError = "connection timeout (auto-reconnect active)"
		log.Printf("[MQTT] %s initial connect timeout, auto-reconnect will retry", instanceID)
		return fmt.Errorf("connection timeout (auto-reconnect active)")
	}
	if err := token.Error(); err != nil {
		wrapper.lastError = err.Error()
		log.Printf("[MQTT] %s initial connect failed: %v, auto-reconnect will retry", instanceID, err)
		return fmt.Errorf("connection failed: %w (auto-reconnect active)", err)
	}

	wrapper.connected = true
	wrapper.connectedAt = time.Now()

	log.Printf("[MQTT] %s reconnected (%d subscriptions restored)", instanceID, len(savedSubs))
	return nil
}

// TestConnection tests connectivity to an MQTT broker using a temporary client.
// Pure Go — no interaction with Lua runtime.
func (mm *MQTTManager) TestConnection(host string, port int, username, password string) error {
	url := host
	if url == "" {
		url = "tcp://localhost"
	} else if !strings.HasPrefix(url, "tcp://") && !strings.HasPrefix(url, "ssl://") && !strings.HasPrefix(url, "ws://") && !strings.HasPrefix(url, "wss://") {
		url = "tcp://" + url
	}
	brokerURL := fmt.Sprintf("%s:%d", url, port)

	opts := mqtt.NewClientOptions().
		AddBroker(brokerURL).
		SetClientID("vclu_test_" + fmt.Sprintf("%d", time.Now().UnixNano()%10000)).
		SetKeepAlive(5 * time.Second).
		SetCleanSession(true).
		SetAutoReconnect(false)

	if username != "" {
		opts.SetUsername(username)
		if password != "" {
			opts.SetPassword(password)
		}
	}

	client := mqtt.NewClient(opts)
	token := client.Connect()
	if !token.WaitTimeout(5 * time.Second) {
		return fmt.Errorf("connection timeout to %s", brokerURL)
	}
	if err := token.Error(); err != nil {
		return fmt.Errorf("connection failed: %w", err)
	}

	client.Disconnect(500)
	return nil
}

// Close closes all MQTT connections
func (mm *MQTTManager) Close() {
	mm.mu.Lock()
	defer mm.mu.Unlock()

	for _, wrapper := range mm.clients {
		if wrapper.client != nil && wrapper.client.IsConnected() {
			wrapper.client.Disconnect(500)
		}
	}
	mm.clients = make(map[string]*mqttClientWrapper)
}

// registerMQTTFunctions registers Go MQTT functions in Lua
func registerMQTTFunctions(L *lua.LState, mm *MQTTManager) {
	// __go_mqtt_connect(instanceId, url, port, clientId, username, password, keepAlive, cleanSession)
	// Returns: success (bool), error (string or nil)
	L.SetGlobal("__go_mqtt_connect", L.NewFunction(func(L *lua.LState) int {
		instanceID := L.CheckString(1)
		url := L.CheckString(2)
		port := L.CheckInt(3)
		clientID := L.CheckString(4)
		username := L.OptString(5, "")
		password := L.OptString(6, "")
		keepAlive := L.OptInt(7, 60)
		cleanSession := L.OptBool(8, true)

		// Async: start connection in background, result delivered via __go_mqtt_poll_event
		mm.ConnectAsync(instanceID, url, port, clientID, username, password, keepAlive, cleanSession)

		L.Push(lua.LTrue)
		L.Push(lua.LNil)
		return 2
	}))

	// __go_mqtt_disconnect(instanceId, destroy) — fire-and-forget; destroy also unsubscribes
	L.SetGlobal("__go_mqtt_disconnect", L.NewFunction(func(L *lua.LState) int {
		instanceID := L.CheckString(1)
		if L.OptBool(2, false) {
			mm.Destroy(instanceID)
		} else {
			go mm.Disconnect(instanceID)
		}
		L.Push(lua.LTrue)
		L.Push(lua.LNil)
		return 2
	}))

	// __go_mqtt_publish(instanceId, topic, payload, qos, retain)
	// Returns: success (bool), error (string or nil)
	L.SetGlobal("__go_mqtt_publish", L.NewFunction(func(L *lua.LState) int {
		instanceID := L.CheckString(1)
		topic := L.CheckString(2)
		payload := L.CheckString(3)
		qos := L.OptInt(4, 0)
		retain := L.OptBool(5, false)

		err := mm.Publish(instanceID, topic, payload, qos, retain)
		if err != nil {
			L.Push(lua.LFalse)
			L.Push(lua.LString(err.Error()))
			return 2
		}

		L.Push(lua.LTrue)
		L.Push(lua.LNil)
		return 2
	}))

	// __go_mqtt_subscribe(instanceId, topic, qos)
	// Returns: success (bool), error (string or nil)
	L.SetGlobal("__go_mqtt_subscribe", L.NewFunction(func(L *lua.LState) int {
		instanceID := L.CheckString(1)
		topic := L.CheckString(2)
		qos := L.OptInt(3, 0)

		// Async: start subscription in background, result delivered via __go_mqtt_poll_event
		mm.SubscribeAsync(instanceID, topic, qos)

		L.Push(lua.LTrue)
		L.Push(lua.LNil)
		return 2
	}))

	// __go_mqtt_unsubscribe(instanceId, topic) — fire-and-forget
	L.SetGlobal("__go_mqtt_unsubscribe", L.NewFunction(func(L *lua.LState) int {
		instanceID := L.CheckString(1)
		topic := L.CheckString(2)

		go func() {
			if err := mm.Unsubscribe(instanceID, topic); err != nil {
				log.Printf("[MQTT] %s unsubscribe %s failed: %v", instanceID, topic, err)
			}
		}()

		L.Push(lua.LTrue)
		L.Push(lua.LNil)
		return 2
	}))

	// __go_mqtt_is_connected(instanceId)
	// Returns: connected (bool)
	L.SetGlobal("__go_mqtt_is_connected", L.NewFunction(func(L *lua.LState) int {
		instanceID := L.CheckString(1)
		connected := mm.IsConnected(instanceID)
		L.Push(lua.LBool(connected))
		return 1
	}))

	// __go_mqtt_poll() - returns next pending message or nil
	// Returns: instanceId, topic, payload, qos, retained (or nil if no message)
	L.SetGlobal("__go_mqtt_poll", L.NewFunction(func(L *lua.LState) int {
		msg := mm.GetPendingMessage()
		if msg == nil {
			return 0
		}

		L.Push(lua.LString(msg.InstanceID))
		L.Push(lua.LString(msg.Topic))
		L.Push(lua.LString(msg.Payload))
		L.Push(lua.LNumber(msg.QoS))
		L.Push(lua.LBool(msg.Retained))
		return 5
	}))

	// __go_mqtt_poll_event() - returns next pending lifecycle event or nil
	// Returns: eventType, instanceId, topic, error (or nothing if no event)
	L.SetGlobal("__go_mqtt_poll_event", L.NewFunction(func(L *lua.LState) int {
		select {
		case ev := <-mm.events:
			L.Push(lua.LString(ev.Type))
			L.Push(lua.LString(ev.InstanceID))
			L.Push(lua.LString(ev.Topic))
			L.Push(lua.LString(ev.Error))
			return 4
		default:
			return 0
		}
	}))

	// __go_mqtt_stats() - returns JSON string with MQTT statistics
	L.SetGlobal("__go_mqtt_stats", L.NewFunction(func(L *lua.LState) int {
		stats := mm.Stats()
		jsonBytes, err := json.Marshal(stats)
		if err != nil {
			L.Push(lua.LString("{}"))
			return 1
		}
		L.Push(lua.LString(string(jsonBytes)))
		return 1
	}))
}
