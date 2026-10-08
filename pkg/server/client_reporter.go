package server

import (
	"encoding/json"
	"fmt"
	"log"
	"net"
	"strconv"
	"strings"
	"time"
)

const (
	// DefaultClientReportInterval is the default interval for sending client reports (ms)
	DefaultClientReportInterval = 1000
)

// ClientReport represents a pending client report to be sent
type ClientReport struct {
	IP        string `json:"ip"`
	Port      int    `json:"port"`
	SessionID int    `json:"sessionID"`
	Values    string `json:"values"`
}

// startClientReporter starts the goroutine that periodically sends client reports
func (s *Server) startClientReporter() {
	go s.clientReporterLoop()
}

// clientReporterLoop is the main loop for sending client reports
func (s *Server) clientReporterLoop() {
	currentInterval := DefaultClientReportInterval
	ticker := time.NewTicker(time.Duration(currentInterval) * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-s.ctx.Done():
			log.Printf("[REPORTER] Stopped")
			return
		case <-s.clientReportSignal:
			// State changed — send reports immediately
			s.sendPendingClientReports()
		case <-ticker.C:
			// Periodic: check interval, cleanup expired, send reports
			newInterval := s.getClientReportInterval()
			if newInterval != currentInterval {
				currentInterval = newInterval
				ticker.Reset(time.Duration(currentInterval) * time.Millisecond)
			}

			s.cleanupExpiredClients()
			s.sendPendingClientReports()
		}
	}
}

// getClientReportInterval reads the ClientReportInterval from Lua CLU
func (s *Server) getClientReportInterval() int {
	if s.luaEngine == nil {
		return DefaultClientReportInterval
	}

	// Get CLU and read feature 1 (ClientReportInterval)
	result, err := s.luaEngine.ExecuteCached("if SYSTEM then return SYSTEM:getShared():getClu():get(1) else return 1000 end")
	if err != nil {
		return DefaultClientReportInterval
	}

	// Parse result
	interval, err := strconv.Atoi(strings.TrimSpace(result))
	if err != nil || interval < 1000 || interval > 10000 {
		return DefaultClientReportInterval
	}

	return interval
}

// sendPendingClientReports gets pending reports from Lua and sends them
func (s *Server) sendPendingClientReports() {
	if s.luaEngine == nil {
		return
	}

	// Get pending reports from Lua (returns JSON string)
	jsonStr, err := s.luaEngine.ExecuteCached("if SYSTEM then return SYSTEM:getShared():getPendingClientReports() else return '[]' end")
	if err != nil {
		log.Printf("[REPORTER] Error getting pending reports: %v", err)
		return
	}

	// Parse JSON
	var reports []ClientReport
	if err := json.Unmarshal([]byte(jsonStr), &reports); err != nil {
		log.Printf("[REPORTER] Error parsing reports JSON: %v (json: %s)", err, jsonStr)
		return
	}

	if len(reports) == 0 {
		return
	}

	for _, report := range reports {
		if err := s.sendClientReport(report); err != nil {
			log.Printf("[REPORTER] Error sending to %s:%d: %v", report.IP, report.Port, err)
		}
	}
}

// sendClientReport sends a single client report via UDP
func (s *Server) sendClientReport(report ClientReport) error {
	// Format: resp:CLU_IP:00000000:clientReport:sessionID:{values}
	// Session ID 00000000 indicates push notification (not response to request)
	responseStr := fmt.Sprintf("resp:%s:00000000:clientReport:%d:%s",
		s.localIP.String(), report.SessionID, report.Values)

	// Encrypt with project key
	s.mu.RLock()
	projectKey := s.projectCipherKey
	s.mu.RUnlock()

	if projectKey == nil {
		return fmt.Errorf("project key not configured")
	}

	encryptedData, err := projectKey.Encrypt([]byte(responseStr))
	if err != nil {
		return fmt.Errorf("encrypt failed: %w", err)
	}

	// Target address
	targetAddr := &net.UDPAddr{
		IP:   net.ParseIP(report.IP),
		Port: report.Port,
	}
	if targetAddr.IP == nil {
		return fmt.Errorf("invalid IP: %s", report.IP)
	}

	// Send UDP
	_, err = s.responseConn.WriteToUDP(encryptedData, targetAddr)
	return err
}

// cleanupExpiredClients removes expired client subscriptions based on TTL
func (s *Server) cleanupExpiredClients() {
	if s.luaEngine == nil {
		return
	}

	result, err := s.luaEngine.ExecuteCached("if SYSTEM then return SYSTEM:getShared():cleanupExpiredClients() else return 0 end")
	if err != nil {
		log.Printf("[REPORTER] Error during cleanup: %v", err)
		return
	}

	// Parse result (number of cleaned up subscriptions)
	cleaned, err := strconv.Atoi(strings.TrimSpace(result))
	if err == nil && cleaned > 0 {
		log.Printf("[REPORTER] Cleaned up %d expired client subscriptions", cleaned)
	}
}
