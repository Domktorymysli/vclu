package server

import (
	"fmt"
	"github.com/Domktorymysli/vclu/pkg/cipher"
	"log"
	"net"
	"strings"
)

// handleSetCluIP handles the req_set_clu_ip command from Object Manager
// Format: req_set_clu_ip:SERIAL_HEX:IP:GATEWAY
func (s *Server) handleSetCluIP(cmdStr string, usedKey *cipher.CipherKey, remoteAddr *net.UDPAddr) {
	parts := strings.Split(cmdStr, ":")
	if len(parts) < 4 {
		log.Printf("[SET_CLU_IP] invalid format, expected 4 parts, got %d", len(parts))
		return
	}

	serialHex := parts[1]

	// Verify serial matches (full 16 chars or last 8 chars)
	ourSerialHex := fmt.Sprintf("%016x", s.deviceConfig.SerialNumber)
	ourSerialSuffix := ourSerialHex[len(ourSerialHex)-8:]

	if serialHex != ourSerialHex && serialHex != ourSerialSuffix {
		log.Printf("[SET_CLU_IP] serial mismatch: got %s, want %s", serialHex, ourSerialHex)
		return
	}

	// Create response: resp_set_clu_ip:SERIAL_HEX:IP_ADDRESS
	response := fmt.Sprintf("resp_set_clu_ip:%s:%s\r\n", serialHex, s.localIP.String())

	encryptedResponse, err := usedKey.Encrypt([]byte(response))
	if err != nil {
		log.Printf("[SET_CLU_IP] failed to encrypt response: %v", err)
		return
	}

	if _, err = s.responseConn.WriteToUDP(encryptedResponse, remoteAddr); err != nil {
		log.Printf("[SET_CLU_IP] failed to send response: %v", err)
	}
}
