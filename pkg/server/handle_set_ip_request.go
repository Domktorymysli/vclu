package server

import (
	"github.com/Domktorymysli/vclu/pkg/cipher"
	"github.com/Domktorymysli/vclu/pkg/commands"
	"log"
	"net"
	"strings"
)

// handleSetIpRequest handles a SetIp request
func (s *Server) handleSetIpRequest(decryptedData []byte, usedKey *cipher.CipherKey, remoteAddr *net.UDPAddr) {
	// Before parsing: the address may be "null", which is not an IP
	if parts := strings.Split(strings.TrimSpace(string(decryptedData)), ":"); len(parts) == 4 && !s.keepsIP(parts[2]) {
		return
	}
	req, err := commands.ParseSetIpRequest(decryptedData)
	if err != nil {
		log.Printf("[SetIp] failed to parse request: %v", err)
		return
	}
	_ = req // vCLU keeps its address; keepsIP let through only a request for it

	response := &commands.SetIpResponse{
		SerialNumber: s.deviceConfig.SerialNumber,
		IPAddress:    s.localIP,
	}

	encryptedResponse, err := usedKey.Encrypt(response.AsBytes())
	if err != nil {
		log.Printf("[SetIp] failed to encrypt response: %v", err)
		return
	}

	if _, err = s.responseConn.WriteToUDP(encryptedResponse, remoteAddr); err != nil {
		log.Printf("[SetIp] failed to send response: %v", err)
	}
}
