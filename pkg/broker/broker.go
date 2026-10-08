package broker

import (
	"bytes"
	"fmt"
	"log"
	"sync"
	"time"

	mqtt "github.com/mochi-mqtt/server/v2"
	"github.com/mochi-mqtt/server/v2/hooks/auth"
	"github.com/mochi-mqtt/server/v2/listeners"
	"github.com/mochi-mqtt/server/v2/packets"
)

// Config represents MQTT broker configuration
type Config struct {
	Enabled  bool
	Port     int
	WSPort   int    // WebSocket port (0 = disabled)
	Username string // Optional authentication
	Password string // Optional authentication
}

// DefaultConfig returns default broker configuration
func DefaultConfig() *Config {
	return &Config{
		Enabled: false,
		Port:    1883,
		WSPort:  0,
	}
}

// LoggingHook logs MQTT messages for debugging
type LoggingHook struct {
	mqtt.HookBase
}

func (h *LoggingHook) ID() string {
	return "logging-hook"
}

func (h *LoggingHook) Provides(b byte) bool {
	return bytes.Contains([]byte{
		mqtt.OnConnect,
		mqtt.OnDisconnect,
		mqtt.OnPublished,
		mqtt.OnSubscribed,
	}, []byte{b})
}

func (h *LoggingHook) OnConnect(cl *mqtt.Client, pk packets.Packet) error {
	log.Printf("[MQTT] client connected: %s", cl.ID)
	return nil
}

func (h *LoggingHook) OnDisconnect(cl *mqtt.Client, err error, expire bool) {
	if err != nil {
		log.Printf("[MQTT] client disconnected: %s (error: %v)", cl.ID, err)
	} else {
		log.Printf("[MQTT] client disconnected: %s", cl.ID)
	}
}

func (h *LoggingHook) OnPublished(cl *mqtt.Client, pk packets.Packet) {
	payload := string(pk.Payload)
	if len(payload) > 200 {
		payload = payload[:200] + "..."
	}
	log.Printf("[MQTT] %s -> %s: %s", cl.ID, pk.TopicName, payload)
}

func (h *LoggingHook) OnSubscribed(cl *mqtt.Client, pk packets.Packet, reasonCodes []byte) {
	for _, sub := range pk.Filters {
		log.Printf("[MQTT] %s subscribed: %s", cl.ID, sub.Filter)
	}
}

// Broker represents an embedded MQTT broker
type Broker struct {
	server    *mqtt.Server
	config    *Config
	running   bool
	mu        sync.RWMutex
	lastError error // last server error
}

// Stats represents broker statistics
type Stats struct {
	Running          bool   `json:"running"`
	Port             int    `json:"port"`
	WSPort           int    `json:"wsPort,omitempty"`
	ClientsConnected int64  `json:"clientsConnected"`
	ClientsTotal     int64  `json:"clientsTotal"`
	MessagesReceived int64  `json:"messagesReceived"`
	MessagesSent     int64  `json:"messagesSent"`
	BytesReceived    int64  `json:"bytesReceived"`
	BytesSent        int64  `json:"bytesSent"`
	Subscriptions    int64  `json:"subscriptions"`
	RetainedMessages int64  `json:"retainedMessages"`
	Inflight         int64  `json:"inflight"`
	InflightDropped  int64  `json:"inflightDropped"`
	MemoryAlloc      int64  `json:"memoryAlloc"`
	AuthEnabled      bool   `json:"authEnabled"`
	LastError        string `json:"lastError,omitempty"`
}

// New creates a new embedded MQTT broker
func New(cfg *Config) *Broker {
	if cfg == nil {
		cfg = DefaultConfig()
	}
	return &Broker{
		config: cfg,
	}
}

// Start starts the MQTT broker
func (b *Broker) Start() error {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.running {
		return nil
	}

	if !b.config.Enabled {
		log.Printf("[MQTT Broker] Disabled in configuration")
		return nil
	}

	// Create server
	b.server = mqtt.New(&mqtt.Options{
		InlineClient: true,
	})

	// Add logging hook for debugging
	if err := b.server.AddHook(new(LoggingHook), nil); err != nil {
		return fmt.Errorf("failed to add logging hook: %w", err)
	}

	// Add authentication if configured
	if b.config.Username != "" && b.config.Password != "" {
		err := b.server.AddHook(new(auth.Hook), &auth.Options{
			Ledger: &auth.Ledger{
				Auth: auth.AuthRules{
					{
						Username: auth.RString(b.config.Username),
						Password: auth.RString(b.config.Password),
						Allow:    true,
					},
				},
				ACL: auth.ACLRules{
					{
						Username: auth.RString(b.config.Username),
						Filters:  auth.Filters{"#": auth.ReadWrite},
					},
				},
			},
		})
		if err != nil {
			return fmt.Errorf("failed to add auth hook: %w", err)
		}
		log.Printf("[MQTT Broker] Authentication enabled for user: %s", b.config.Username)
	} else {
		// Allow anonymous access
		err := b.server.AddHook(new(auth.Hook), &auth.Options{
			Ledger: &auth.Ledger{
				Auth: auth.AuthRules{
					{Allow: true}, // Allow all
				},
				ACL: auth.ACLRules{
					{Filters: auth.Filters{"#": auth.ReadWrite}}, // Full access
				},
			},
		})
		if err != nil {
			return fmt.Errorf("failed to add anonymous auth hook: %w", err)
		}
		log.Printf("[MQTT Broker] Anonymous access enabled")
	}

	// Add TCP listener
	tcpAddr := fmt.Sprintf(":%d", b.config.Port)
	tcp := listeners.NewTCP(listeners.Config{
		ID:      "tcp",
		Address: tcpAddr,
	})
	if err := b.server.AddListener(tcp); err != nil {
		return fmt.Errorf("failed to add TCP listener: %w", err)
	}

	// Add WebSocket listener if configured
	if b.config.WSPort > 0 {
		wsAddr := fmt.Sprintf(":%d", b.config.WSPort)
		ws := listeners.NewWebsocket(listeners.Config{
			ID:      "ws",
			Address: wsAddr,
		})
		if err := b.server.AddListener(ws); err != nil {
			return fmt.Errorf("failed to add WebSocket listener: %w", err)
		}
		log.Printf("[MQTT Broker] WebSocket listener on port %d", b.config.WSPort)
	}

	// Start server in goroutine but track errors
	errChan := make(chan error, 1)
	go func() {
		if err := b.server.Serve(); err != nil {
			log.Printf("[MQTT Broker] Server error: %v", err)
			b.mu.Lock()
			b.lastError = err
			b.running = false
			b.mu.Unlock()
			select {
			case errChan <- err:
			default:
			}
		}
	}()

	// Give server a moment to start and check for immediate failures
	select {
	case err := <-errChan:
		return fmt.Errorf("broker failed to start: %w", err)
	case <-time.After(100 * time.Millisecond):
		// Server started successfully
	}

	b.running = true
	b.lastError = nil
	log.Printf("[MQTT Broker] Started on port %d", b.config.Port)

	return nil
}

// Stop stops the MQTT broker
func (b *Broker) Stop() error {
	b.mu.Lock()
	defer b.mu.Unlock()

	if !b.running || b.server == nil {
		return nil
	}

	// Publish offline status before stopping (LWT equivalent)
	b.publishOfflineStatus()

	if err := b.server.Close(); err != nil {
		return fmt.Errorf("failed to stop broker: %w", err)
	}

	b.running = false
	log.Printf("[MQTT Broker] Stopped")

	return nil
}

// publishOfflineStatus publishes offline status to availability topic
func (b *Broker) publishOfflineStatus() {
	if b.server == nil {
		return
	}
	// Publish to vclu/status topic with offline payload (retained)
	_ = b.server.Publish("vclu/status", []byte("offline"), true, 0)
}

// IsRunning returns whether the broker is running
func (b *Broker) IsRunning() bool {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.running
}

// Stats returns broker statistics
func (b *Broker) Stats() Stats {
	b.mu.RLock()
	defer b.mu.RUnlock()

	stats := Stats{
		Running:     b.running,
		Port:        b.config.Port,
		WSPort:      b.config.WSPort,
		AuthEnabled: b.config.Username != "" && b.config.Password != "",
	}

	if b.lastError != nil {
		stats.LastError = b.lastError.Error()
	}

	if b.server != nil && b.running {
		sysInfo := b.server.Info
		stats.ClientsConnected = sysInfo.ClientsConnected
		stats.ClientsTotal = sysInfo.ClientsTotal
		stats.MessagesReceived = sysInfo.MessagesReceived
		stats.MessagesSent = sysInfo.MessagesSent
		stats.BytesReceived = sysInfo.BytesReceived
		stats.BytesSent = sysInfo.BytesSent
		stats.Subscriptions = sysInfo.Subscriptions
		stats.RetainedMessages = sysInfo.Retained
		stats.Inflight = sysInfo.Inflight
		stats.InflightDropped = sysInfo.InflightDropped
		stats.MemoryAlloc = sysInfo.MemoryAlloc
	}

	return stats
}

// Reconfigure updates broker configuration and restarts if needed
func (b *Broker) Reconfigure(cfg *Config) error {
	b.mu.Lock()
	wasRunning := b.running
	b.mu.Unlock()

	if wasRunning {
		if err := b.Stop(); err != nil {
			return fmt.Errorf("failed to stop broker for reconfiguration: %w", err)
		}
	}

	b.mu.Lock()
	b.config = cfg
	b.mu.Unlock()

	if cfg.Enabled {
		if err := b.Start(); err != nil {
			return fmt.Errorf("failed to restart broker: %w", err)
		}
	}

	return nil
}

// GetConfig returns current broker configuration
func (b *Broker) GetConfig() *Config {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.config
}

// RetainedMessage represents a retained MQTT message
type RetainedMessage struct {
	Topic   string `json:"topic"`
	Payload []byte `json:"payload"`
}

// GetRetainedByFilter returns retained messages matching an MQTT topic filter.
// Supports standard MQTT wildcards: + (single level), # (multi level).
func (b *Broker) GetRetainedByFilter(filter string) []RetainedMessage {
	b.mu.RLock()
	defer b.mu.RUnlock()

	if b.server == nil || !b.running {
		return nil
	}

	packets := b.server.Topics.Messages(filter)
	result := make([]RetainedMessage, 0, len(packets))
	for _, pkt := range packets {
		result = append(result, RetainedMessage{
			Topic:   pkt.TopicName,
			Payload: pkt.Payload,
		})
	}
	return result
}

// ClientInfo represents information about a connected MQTT client
type ClientInfo struct {
	ID            string   `json:"id"`
	RemoteAddr    string   `json:"remoteAddr"`
	Username      string   `json:"username,omitempty"`
	CleanSession  bool     `json:"cleanSession"`
	Subscriptions []string `json:"subscriptions"`
	Connected     bool     `json:"connected"`
}

// Publish publishes a message to a topic using the broker's inline client
func (b *Broker) Publish(topic string, payload []byte, retain bool) error {
	b.mu.RLock()
	defer b.mu.RUnlock()

	if b.server == nil || !b.running {
		return fmt.Errorf("broker not running")
	}

	return b.server.Publish(topic, payload, retain, 0)
}

// GetClients returns list of all connected clients
func (b *Broker) GetClients() []ClientInfo {
	b.mu.RLock()
	defer b.mu.RUnlock()

	var clients []ClientInfo
	if b.server == nil || !b.running {
		return clients
	}

	// Get all clients from the broker
	allClients := b.server.Clients.GetAll()
	for _, cl := range allClients {
		// Skip internal mochi-mqtt client
		if cl.ID == "inline" {
			continue
		}

		info := ClientInfo{
			ID:           cl.ID,
			CleanSession: cl.Properties.Clean,
			Connected:    !cl.Closed(),
		}

		// Get remote address
		if cl.Net.Remote != "" {
			info.RemoteAddr = cl.Net.Remote
		}

		// Get username if authenticated
		if cl.Properties.Username != nil {
			info.Username = string(cl.Properties.Username)
		}

		// Get subscriptions
		subs := cl.State.Subscriptions.GetAll()
		for filter := range subs {
			info.Subscriptions = append(info.Subscriptions, filter)
		}

		clients = append(clients, info)
	}

	return clients
}
