package server

import (
	"github.com/Domktorymysli/vclu/pkg/cipher"
	"github.com/Domktorymysli/vclu/pkg/commands"
	"log"
	"net"
)

// handleStartTFTPdRequest handles the StartTFTPd command
func (s *Server) handleStartTFTPdRequest(decryptedData []byte, usedKey *cipher.CipherKey, remoteAddr *net.UDPAddr) {
	if _, err := commands.ParseStartTFTPdRequest(decryptedData); err != nil {
		log.Printf("[StartTFTPd] failed to parse request: %v", err)
		return
	}

	if !s.tftpServer.IsRunning() {
		if err := s.tftpServer.Start(); err != nil {
			log.Printf("[StartTFTPd] failed to start TFTP server: %v", err)
			return
		}
	}

	response := &commands.StartTFTPdResponse{}
	encryptedResponse, err := s.projectCipherKey.Encrypt(response.AsBytes())
	if err != nil {
		log.Printf("[StartTFTPd] failed to encrypt response: %v", err)
		return
	}

	if _, err = s.responseConn.WriteToUDP(encryptedResponse, remoteAddr); err != nil {
		log.Printf("[StartTFTPd] failed to send response: %v", err)
	}
}
