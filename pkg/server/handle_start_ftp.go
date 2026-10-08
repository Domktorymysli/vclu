package server

import (
	"log"
	"net"
	"time"

	"github.com/Domktorymysli/vclu/pkg/cipher"
)

// handleStartFTP handles the req_start_ftp command from Object Manager
// This command enables TFTP server for file transfers
// Response: resp:OK
func (s *Server) handleStartFTP(usedKey *cipher.CipherKey, remoteAddr *net.UDPAddr) {
	// Start TFTP server if not already running
	if s.tftpServer != nil && !s.tftpServer.IsRunning() {
		if err := s.tftpServer.Start(); err != nil {
			log.Printf("[START_FTP] failed to start TFTP server: %v", err)
			return
		}
	}

	// Create response: resp:OK
	encryptedResponse, err := usedKey.Encrypt([]byte("resp:OK"))
	if err != nil {
		log.Printf("[START_FTP] failed to encrypt response: %v", err)
		return
	}

	if _, err = s.responseConn.WriteToUDP(encryptedResponse, remoteAddr); err != nil {
		log.Printf("[START_FTP] failed to send response: %v", err)
	}

	// Small delay to give OM time to process the response before TFTP starts
	time.Sleep(100 * time.Millisecond)
}
