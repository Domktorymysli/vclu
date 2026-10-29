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

	if !s.keepsIP(parts[2]) {
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

// keepsIP reports whether Object Manager asks for the address vCLU already has. Object Manager
// picks a CLU's address from the project's IP range, skipping addresses of other CLUs in the
// project, and asks the CLU to move there; it sends "null" when the range has no free address.
// vCLU cannot move. Confirming anyway makes Object Manager put the CLU at the new address, so
// SetKey and the project upload go nowhere; not answering keeps the CLU where it is.
func (s *Server) keepsIP(requested string) bool {
	requested = strings.TrimSpace(requested)
	if requested == s.localIP.String() {
		log.Printf("[SET_CLU_IP] Object Manager asks for %s, the address vCLU has: confirmed", requested)
		return true
	}
	log.Printf("[SET_CLU_IP] Object Manager asks to move this CLU to %s, vCLU stays at %s and does not answer: "+
		"remove other CLUs with %s from the project (e.g. this vCLU before a reset) and make the project's IP range include it",
		requested, s.localIP, s.localIP)
	return false
}
