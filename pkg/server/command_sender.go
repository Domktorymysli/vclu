package server

import (
	"encoding/json"
	"fmt"
	"log"
	"math/rand"
	"net"
	"strings"
	"time"

	"github.com/Domktorymysli/vclu/pkg/cipher"
	"github.com/Domktorymysli/vclu/pkg/commands"
)

// SendCommand sends a Lua script command to a CLU and waits for response (5s timeout)
func (s *Server) SendCommand(targetIP string, script string) (string, error) {
	return s.SendCommandWithTimeout(targetIP, script, 5*time.Second)
}

// SendCommandWithTimeout sends a Lua script command to a CLU with a custom timeout
func (s *Server) SendCommandWithTimeout(targetIP string, script string, timeout time.Duration) (string, error) {
	// Parse target IP
	ip := net.ParseIP(targetIP)
	if ip == nil {
		return "", fmt.Errorf("invalid target IP: %s", targetIP)
	}

	// Generate random session ID
	sessionID := uint32(rand.Intn(0xFFFFFF))

	// Create request
	req := &commands.LuaScriptRequest{
		IPAddress: s.localIP,
		SessionID: sessionID,
		Script:    script,
	}
	requestData := req.AsBytes()

	// Encrypt with project key
	s.mu.RLock()
	projectKey := s.projectCipherKey
	s.mu.RUnlock()

	if projectKey == nil {
		return "", fmt.Errorf("project key not configured")
	}

	encryptedRequest, err := projectKey.Encrypt(requestData)
	if err != nil {
		return "", fmt.Errorf("failed to encrypt request: %w", err)
	}

	// Create UDP connection for sending/receiving
	conn, err := net.DialUDP("udp4", nil, &net.UDPAddr{
		IP:   ip,
		Port: s.remoteCommandPort(),
	})
	if err != nil {
		return "", fmt.Errorf("failed to connect to target: %w", err)
	}
	defer conn.Close()

	// Set timeout
	conn.SetDeadline(time.Now().Add(timeout))

	// Send request
	if _, err = conn.Write(encryptedRequest); err != nil {
		return "", fmt.Errorf("failed to send request: %w", err)
	}

	// Wait for response
	buffer := make([]byte, BufferSize)
	n, err := conn.Read(buffer)
	if err != nil {
		return "", fmt.Errorf("failed to receive response: %w", err)
	}

	// Decrypt response
	decryptedResponse, err := projectKey.Decrypt(buffer[:n])
	if err != nil {
		return "", fmt.Errorf("failed to decrypt response: %w", err)
	}

	// Parse response: resp:IP:SESSION:RESULT
	responseStr := string(decryptedResponse)
	if !strings.HasPrefix(responseStr, "resp:") {
		return "", fmt.Errorf("invalid response format: %s", responseStr)
	}

	parts := strings.SplitN(responseStr, ":", 4)
	if len(parts) < 4 {
		return "", fmt.Errorf("invalid response format: %s", responseStr)
	}

	return parts[3], nil
}

// SendCommandNoReply sends a Lua script command to a CLU without waiting for a response.
// Used for fire-and-forget operations like clientRegister (re-registration) and clientDestroy where
// the caller does not need the result. This avoids blocking the caller on UDP round-trips.
// A CLU answers a re-registration with the session's current values; that answer is applied
// like a pushed clientReport, so a report lost earlier is corrected. Other answers are ignored.
func (s *Server) SendCommandNoReply(targetIP string, script string) error {
	ip := net.ParseIP(targetIP)
	if ip == nil {
		return fmt.Errorf("invalid target IP: %s", targetIP)
	}

	sessionID := uint32(rand.Intn(0xFFFFFF))

	req := &commands.LuaScriptRequest{
		IPAddress: s.localIP,
		SessionID: sessionID,
		Script:    script,
	}
	requestData := req.AsBytes()

	s.mu.RLock()
	projectKey := s.projectCipherKey
	s.mu.RUnlock()

	if projectKey == nil {
		return fmt.Errorf("project key not configured")
	}

	encryptedRequest, err := projectKey.Encrypt(requestData)
	if err != nil {
		return fmt.Errorf("failed to encrypt request: %w", err)
	}

	conn, err := net.DialUDP("udp4", nil, &net.UDPAddr{
		IP:   ip,
		Port: s.remoteCommandPort(),
	})
	if err != nil {
		return fmt.Errorf("failed to connect to target: %w", err)
	}

	if _, err = conn.Write(encryptedRequest); err != nil {
		_ = conn.Close()
		return fmt.Errorf("failed to send request: %w", err)
	}

	go s.applyReportReply(conn, projectKey, ip.String())
	return nil
}

// noReplyAnswerTimeout bounds how long a fire-and-forget command waits for a clientReport answer.
const noReplyAnswerTimeout = 2 * time.Second

// applyReportReply reads the CLU's answer to a fire-and-forget command and applies it when it
// is a clientReport. It owns conn and closes it.
func (s *Server) applyReportReply(conn *net.UDPConn, projectKey *cipher.CipherKey, cluIP string) {
	defer func() { _ = conn.Close() }()
	_ = conn.SetReadDeadline(time.Now().Add(noReplyAnswerTimeout))

	buffer := make([]byte, BufferSize)
	n, err := conn.Read(buffer)
	if err != nil {
		return
	}
	decrypted, err := projectKey.Decrypt(buffer[:n])
	if err != nil {
		return
	}
	// Same format as a push; the subscription is keyed by the address it was sent to.
	if _, sessionID, valuesStr, ok := parseClientReportMessage(string(decrypted)); ok {
		s.updateRegistryFromClientReport(cluIP, sessionID, valuesStr)
	}
}

func (s *Server) remoteCommandPort() int {
	if s.cluPort != 0 {
		return s.cluPort
	}
	return CommandPort
}

// GetRegisteredCLUs returns list of CLUs registered in Lua
// Returns slice of CLUInfo structs
func (s *Server) GetRegisteredCLUs() ([]CLUInfo, error) {
	if s.luaEngine == nil {
		return nil, fmt.Errorf("Lua engine not initialized")
	}

	result, err := s.luaEngine.ExecuteCached("SYSTEM:getShared():getRegisteredCLUs()")
	if err != nil {
		return nil, fmt.Errorf("failed to get CLUs from Lua: %w", err)
	}

	// Parse JSON result from Lua
	var clus []CLUInfo
	if err := json.Unmarshal([]byte(result), &clus); err != nil {
		log.Printf("[GET-CLUS] failed to parse Lua result: %v", err)
		// Fallback to local CLU only
		clus = []CLUInfo{
			{
				Name:    "Local (this CLU)",
				IP:      s.localIP.String(),
				Type:    "LOCAL",
				IsLocal: true,
			},
		}
	}

	// Mark local CLU
	localIP := s.localIP.String()
	for i := range clus {
		if clus[i].IP == localIP || clus[i].IP == "0.0.0.0" {
			clus[i].IP = localIP
			clus[i].IsLocal = true
		}
	}

	return clus, nil
}

// CLUInfo represents information about a registered CLU
type CLUInfo struct {
	Name    string `json:"name"`
	IP      string `json:"ip"`
	Type    string `json:"type"`
	IsLocal bool   `json:"isLocal"`
}
