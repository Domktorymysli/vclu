package server

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"log"
	"net"
	"time"

	"github.com/Domktorymysli/vclu/pkg/cipher"
	"github.com/Domktorymysli/vclu/pkg/commands"
	"github.com/Domktorymysli/vclu/pkg/config"
)

// handleDiscoveryRequestFull handles the full discovery protocol
func (s *Server) handleDiscoveryRequestFull(encryptedData []byte, remoteAddr *net.UDPAddr) {
	// Parse discovery request
	req, err := commands.ParseDiscoveryRequest(encryptedData)
	if err != nil {
		log.Printf("[DISCOVERY] failed to parse request: %v", err)
		return
	}

	// Try to decrypt the encrypted random bytes with project cipher key
	decryptedRandom, err := s.projectCipherKey.Decrypt(req.Encrypted)
	if err != nil {
		// If decryption fails, use random bytes (this is what Java does)
		decryptedRandom = make([]byte, commands.RandomBytes)
		if _, err := rand.Read(decryptedRandom); err != nil {
			log.Printf("[DISCOVERY] crypto/rand failed: %v", err)
			return
		}
	}

	// Hash the decrypted random bytes using Grenton's custom hash
	randomHash := commands.Hash(decryptedRandom)

	// Generate temporary cipher key for initial communication
	temporaryIV := make([]byte, 16)
	if _, err := rand.Read(temporaryIV); err != nil {
		log.Printf("[DISCOVERY] failed to generate temporary IV: %v", err)
		return
	}

	// Initial cipher key = GenerateKey(DefaultIV, PIN)
	temporaryCipherKey := cipher.GetInitialCipherKey(temporaryIV, s.keys.PIN)

	// Store temporary key for further communication
	s.mu.Lock()
	s.temporaryCipherKey = temporaryCipherKey
	s.temporaryIV = temporaryIV
	s.unicastCipherKeys = []*cipher.CipherKey{temporaryCipherKey, s.projectCipherKey}
	s.broadcastCipherKeys = []*cipher.CipherKey{cipher.DefaultBroadcastKey, temporaryCipherKey, s.projectCipherKey}
	s.mu.Unlock()

	// Encrypt the hash with temporary cipher key
	encryptedHashFull, err := temporaryCipherKey.Encrypt(randomHash)
	if err != nil {
		log.Printf("[DISCOVERY] failed to encrypt hash: %v", err)
		return
	}

	// Use only first 32 bytes (encryption adds padding, we need exactly 32 bytes for protocol)
	encryptedHash := encryptedHashFull[:32]

	// Create discovery response
	// MAC address without colons for OM compatibility (OM uses split(":") to parse)
	macWithoutColons := fmt.Sprintf("%02x%02x%02x%02x%02x%02x",
		s.macAddr[0], s.macAddr[1], s.macAddr[2], s.macAddr[3], s.macAddr[4], s.macAddr[5])

	response := &commands.DiscoveryResponse{
		Encrypted:    encryptedHash, // 32 bytes
		TemporaryIV:  temporaryIV,   // 16 bytes
		SerialNumber: s.deviceConfig.SerialNumber,
		MACAddress:   macWithoutColons,
	}

	responseData := response.AsBytes()

	// Encrypt entire response with DEFAULT_BROADCAST key using the request IV
	responseCipherKey := cipher.DefaultBroadcastKey.WithIV(req.IV)

	encryptedResponse, err := responseCipherKey.Encrypt(responseData)
	if err != nil {
		log.Printf("[DISCOVERY] failed to encrypt response: %v", err)
		return
	}

	// Send response
	_, err = s.responseConn.WriteToUDP(encryptedResponse, remoteAddr)
	if err != nil {
		log.Printf("[DISCOVERY] failed to send response: %v", err)
		return
	}

	log.Printf("[DISCOVERY] responded to %s, temporary IV %x… (new at every discovery)", remoteAddr.IP, temporaryIV[:4])
}

// handleSetKeyRequest handles the SetKey command
func (s *Server) handleSetKeyRequest(encryptedData []byte, usedKey *cipher.CipherKey, remoteAddr *net.UDPAddr) {
	// Parse SetKey request
	req, err := commands.ParseSetKeyRequest(encryptedData)
	if err != nil {
		log.Printf("[SetKey] failed to parse request: %v", err)
		return
	}

	// Verify encrypted random bytes (should decrypt correctly with current key)
	_, err = usedKey.Decrypt(req.Encrypted)
	if err != nil {
		log.Printf("[SetKey] verification failed: %v", err)
		// Continue anyway - Java doesn't strictly enforce this
	}

	// Update project cipher key
	newProjectKey := cipher.NewCipherKey(req.Key, req.IV)

	s.mu.Lock()
	s.projectCipherKey = newProjectKey
	s.temporaryCipherKey = nil
	s.temporaryIV = nil
	s.unicastCipherKeys = []*cipher.CipherKey{newProjectKey}
	s.broadcastCipherKeys = []*cipher.CipherKey{cipher.DefaultBroadcastKey, newProjectKey}

	// Update keys in config
	s.keys.Key = req.Key
	s.keys.IV = req.IV
	s.mu.Unlock()

	// Persist new keys to file
	if err := s.persistKeys(); err != nil {
		log.Printf("[SetKey] failed to persist keys: %v", err)
	}

	// Mark CLU as paired and save project key to local config
	projectKeyB64 := base64.StdEncoding.EncodeToString(req.Key)
	projectIVB64 := base64.StdEncoding.EncodeToString(req.IV)
	if err := config.SetPaired(s.serverDir, "Grenton Project", time.Now().Format("20060102-150405"), projectKeyB64, projectIVB64); err != nil {
		log.Printf("[SetKey] failed to save pairing info: %v", err)
	}

	log.Printf("[SetKey] paired from %s", remoteAddr.IP)

	// Create SetKey response
	response := &commands.SetKeyResponse{}
	responseData := response.AsBytes()

	// Encrypt response with NEW project key
	encryptedResponse, err := newProjectKey.Encrypt(responseData)
	if err != nil {
		log.Printf("[SetKey] failed to encrypt response: %v", err)
		return
	}

	// Send response
	_, err = s.responseConn.WriteToUDP(encryptedResponse, remoteAddr)
	if err != nil {
		log.Printf("[SetKey] failed to send response: %v", err)
	}
}
