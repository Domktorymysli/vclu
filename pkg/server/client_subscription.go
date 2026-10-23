package server

import (
	"fmt"
	"log"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"

	lua "github.com/yuin/gopher-lua"
)

const (
	// ClientReportPort is the port for receiving clientReport push notifications
	ClientReportPort = 4344

	// ReplyBufferSize is the largest UDP payload. A clientReport of a large subscription
	// (text values) can pass BufferSize, and a cut datagram does not decrypt.
	ReplyBufferSize = 65535
)

// RemoteSubscription represents a subscription to a remote CLU
type RemoteSubscription struct {
	CLUIP        string
	CLUSerial    string // e.g., "CLU212345678"
	SessionID    int
	Objects      []SubscribedObject
	RegisteredAt time.Time
	ExpiresAt    time.Time
}

// SubscribedObject represents a single object in a subscription
type SubscribedObject struct {
	ObjectID     string // "DOU3328"
	FeatureIndex int    // 0 = Value
}

// SubscriptionManager manages remote CLU subscriptions
type SubscriptionManager struct {
	subscriptions map[string]*RemoteSubscription // key: CLUIP:SessionID
	mu            sync.RWMutex
	nextSessionID int
}

// NewSubscriptionManager creates a new subscription manager
func NewSubscriptionManager() *SubscriptionManager {
	return &SubscriptionManager{
		subscriptions: make(map[string]*RemoteSubscription),
		nextSessionID: 1,
	}
}

// GenerateSessionID generates a unique session ID
func (sm *SubscriptionManager) GenerateSessionID() int {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	id := sm.nextSessionID
	sm.nextSessionID++
	return id
}

// AddSubscription adds a subscription
func (sm *SubscriptionManager) AddSubscription(sub *RemoteSubscription) {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	key := fmt.Sprintf("%s:%d", sub.CLUIP, sub.SessionID)
	sm.subscriptions[key] = sub
}

// GetSubscription gets a subscription by CLUIP and SessionID
func (sm *SubscriptionManager) GetSubscription(cluIP string, sessionID int) *RemoteSubscription {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	key := fmt.Sprintf("%s:%d", cluIP, sessionID)
	return sm.subscriptions[key]
}

// RemoveSubscription removes a subscription
func (sm *SubscriptionManager) RemoveSubscription(cluIP string, sessionID int) {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	key := fmt.Sprintf("%s:%d", cluIP, sessionID)
	delete(sm.subscriptions, key)
}

// GetExpiringSubscriptions returns subscriptions that expire within the given duration
func (sm *SubscriptionManager) GetExpiringSubscriptions(within time.Duration) []*RemoteSubscription {
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	deadline := time.Now().Add(within)
	var expiring []*RemoteSubscription

	for _, sub := range sm.subscriptions {
		if sub.ExpiresAt.Before(deadline) {
			expiring = append(expiring, sub)
		}
	}

	return expiring
}

// GetAllSubscriptions returns all active subscriptions
func (sm *SubscriptionManager) GetAllSubscriptions() []*RemoteSubscription {
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	subs := make([]*RemoteSubscription, 0, len(sm.subscriptions))
	for _, sub := range sm.subscriptions {
		subs = append(subs, sub)
	}
	return subs
}

// startClientReportListener starts the UDP listener on port 4344 for clientReport push notifications
func (s *Server) startClientReportListener() error {
	addr := &net.UDPAddr{
		IP:   net.IPv4zero,
		Port: ClientReportPort,
	}

	conn, err := net.ListenUDP("udp4", addr)
	if err != nil {
		return fmt.Errorf("failed to listen on client report port %d: %w", ClientReportPort, err)
	}

	log.Printf("[CLIENT-REPORT] Listening for clientReport on port %d", ClientReportPort)

	go s.handleClientReports(conn)

	return nil
}

// handleClientReports handles incoming clientReport messages on port 4344
func (s *Server) handleClientReports(conn *net.UDPConn) {
	defer conn.Close()

	buffer := make([]byte, ReplyBufferSize)

	for {
		select {
		case <-s.ctx.Done():
			log.Printf("[CLIENT-REPORT] Listener stopped")
			return
		default:
		}

		// Set read deadline to allow checking ctx.Done()
		conn.SetReadDeadline(time.Now().Add(1 * time.Second))

		n, remoteAddr, err := conn.ReadFromUDP(buffer)
		if err != nil {
			if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
				continue // Timeout, check ctx.Done() and continue
			}
			log.Printf("[CLIENT-REPORT] Read error: %v", err)
			continue
		}

		// Decrypt the message
		s.mu.RLock()
		projectKey := s.projectCipherKey
		s.mu.RUnlock()

		if projectKey == nil {
			continue
		}

		decrypted, err := projectKey.Decrypt(buffer[:n])
		if err != nil {
			log.Printf("[CLIENT-REPORT] Decrypt error from %s: %v", remoteAddr, err)
			continue
		}

		message := string(decrypted)

		// Parse and handle the clientReport
		s.handleClientReportMessage(remoteAddr.IP.String(), message)
	}
}

// handleClientReportMessage parses and processes a clientReport message
// Format: resp:CLU_IP:SESSION:clientReport:SID:{values}
func (s *Server) handleClientReportMessage(senderIP string, message string) {
	cluIP, sessionID, valuesStr, ok := parseClientReportMessage(message)
	if !ok {
		return
	}

	log.Printf("[CLIENT-REPORT] from %s session=%d values=%s", cluIP, sessionID, valuesStr)

	// Call Lua to update registry
	s.updateRegistryFromClientReport(cluIP, sessionID, valuesStr)
}

// parseClientReportMessage splits a clientReport message, pushed to ClientReportPort or sent as
// the answer to clientRegister.
// Expected format: resp:192.168.0.3:00000000:clientReport:123:{1,0,0.5}
func parseClientReportMessage(message string) (cluIP string, sessionID int, valuesStr string, ok bool) {
	if !strings.HasPrefix(message, "resp:") {
		return "", 0, "", false
	}

	parts := strings.SplitN(message, ":", 5)
	if len(parts) < 5 {
		return "", 0, "", false
	}

	// parts[0] = "resp"
	cluIP = parts[1]
	// parts[2] = session (ignored for push notifications, always "00000000")
	payload := parts[3] + ":" + parts[4] // "clientReport:SID:{values}"

	if !strings.HasPrefix(payload, "clientReport:") {
		return "", 0, "", false
	}

	// clientReport:123:{1,0,0.5}
	reportParts := strings.SplitN(payload, ":", 3)
	if len(reportParts) < 3 {
		return "", 0, "", false
	}

	if _, err := fmt.Sscanf(reportParts[1], "%d", &sessionID); err != nil {
		return "", 0, "", false
	}
	return cluIP, sessionID, strings.TrimSpace(reportParts[2]), true
}

// updateRegistryFromClientReport updates the Lua registry with values from clientReport.
// Parses values in Go and passes a pre-built Lua table to avoid Lua-side string
// parsing allocations (parseValues creates O(N×chars) temp tables/strings per call).
func (s *Server) updateRegistryFromClientReport(cluIP string, sessionID int, valuesStr string) {
	s.callRegistryUpdate("RemoteSubscription.updateRegistry", cluIP, sessionID, valuesStr)
}

// callRegistryUpdate calls the Lua function fn(cluIP, sessionID, values) with parsed values.
func (s *Server) callRegistryUpdate(fn string, cluIP string, sessionID int, valuesStr string) {
	if s.luaEngine == nil {
		log.Printf("[CLIENT-REPORT] Lua engine not available")
		return
	}

	// Parse "{1,0,0.5}" → Lua table in Go — zero Lua allocations
	values := s.luaEngine.NewLuaTable()
	parseClientReportValues(valuesStr, values)

	if _, err := s.luaEngine.CallLuaFunctionLValues(
		fn,
		lua.LString(cluIP),
		lua.LNumber(float64(sessionID)),
		values,
	); err != nil {
		log.Printf("[CLIENT-REPORT] Failed to update registry: %v", err)
	}
}

// parseClientReportValues parses "{1,0,0.5}" into a Lua table with numeric indices.
// Handles nested braces (e.g., "{1,{2,3},4}") by skipping commas inside braces.
// Values are stored as LNumber when possible, otherwise as LString.
func parseClientReportValues(valuesStr string, tbl *lua.LTable) {
	// Strip outer braces
	s := strings.TrimSpace(valuesStr)
	if len(s) >= 2 && s[0] == '{' && s[len(s)-1] == '}' {
		s = s[1 : len(s)-1]
	}
	if s == "" {
		return
	}

	depth := 0
	start := 0
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '{':
			depth++
		case '}':
			depth--
		case ',':
			if depth == 0 {
				addParsedValue(tbl, strings.TrimSpace(s[start:i]))
				start = i + 1
			}
		}
	}
	// Last value
	addParsedValue(tbl, strings.TrimSpace(s[start:]))
}

func addParsedValue(tbl *lua.LTable, val string) {
	if val == "" {
		return
	}
	if f, err := strconv.ParseFloat(val, 64); err == nil {
		tbl.Append(lua.LNumber(f))
	} else {
		tbl.Append(lua.LString(val))
	}
}

// triggerAutoSubscribe runs auto-subscribe to get initial states from remote CLUs
// This is called once at startup and after reset to sync initial values
func (s *Server) triggerAutoSubscribe() {
	if s.luaEngine == nil {
		log.Printf("[CLIENT-REPORT] Cannot auto-subscribe: Lua engine not available")
		return
	}

	log.Printf("[CLIENT-REPORT] Triggering auto-subscribe to remote CLUs")

	script := `
		if RemoteSubscription and RemoteSubscription.autoSubscribe then
			RemoteSubscription.autoSubscribe()
			return "OK"
		else
			return "RemoteSubscription not available"
		end
	`

	result, err := s.luaEngine.Execute(script)
	if err != nil {
		log.Printf("[CLIENT-REPORT] Auto-subscribe failed: %v", err)
	} else {
		log.Printf("[CLIENT-REPORT] Auto-subscribe result: %v", result)
	}
}
