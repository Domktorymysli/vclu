package server

import (
	"github.com/Domktorymysli/vclu/pkg/cipher"
	"github.com/Domktorymysli/vclu/pkg/commands"
	"log"
	"net"
)

// handleSetIpRequest handles a SetIp request
func (s *Server) handleSetIpRequest(decryptedData []byte, usedKey *cipher.CipherKey, remoteAddr *net.UDPAddr) {
	req, err := commands.ParseSetIpRequest(decryptedData)
	if err != nil {
		log.Printf("[SetIp] failed to parse request: %v", err)
		return
	}
	_ = req // virtual CLU doesn't actually change IP, just acknowledge

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
