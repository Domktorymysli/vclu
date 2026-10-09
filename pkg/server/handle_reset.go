package server

import (
	"log"
	"net"
	"path/filepath"
	"strings"

	"github.com/Domktorymysli/vclu/pkg/cipher"
	"github.com/Domktorymysli/vclu/pkg/commands"
	"github.com/Domktorymysli/vclu/pkg/config"
	"github.com/Domktorymysli/vclu/pkg/omparser"
)

// reloadLuaEngine reinitializes the Lua engine, reloads CLU data, generates
// local proxy for object registration, loads remote proxies, and triggers subscriptions.
func (s *Server) reloadLuaEngine() {
	if s.luaEngine == nil {
		log.Printf("[RESET] no Lua engine to restart")
		return
	}

	// Pause HomeKit writes during the reload, as a manual reload does
	s.callOnLuaPreReset()

	if err := s.luaEngine.Reinitialize("VCLU"); err != nil {
		log.Printf("[RESET] failed to reinitialize Lua engine: %v", err)
		return
	}

	// Set firmware version string for CLU:get(17)
	s.luaEngine.SetGlobal("_FIRMWARE_VERSION", s.deviceConfig.FirmwareVersionStr)

	// Pre-load hook: access sync + plugins (before user.lua so Plugin.getPlugin works)
	s.callPreLuaLoad()

	// Reload CLU data from a/ directory
	cluDataPath := filepath.Join(s.serverDir, "a")
	if err := s.luaEngine.LoadCLUData(cluDataPath); err != nil {
		log.Printf("[RESET] failed to reload CLU data: %v", err)
		return
	}

	// Generate local proxy file (registers local DIN/DOUT in _registry)
	importsDir := config.GetOMFilesDir(s.serverDir)
	omparser.GenerateLocalProxyFile(cluDataPath, importsDir)

	// Load proxy files (remote + local)
	if err := s.luaEngine.LoadProxyFiles(importsDir); err != nil {
		log.Printf("[RESET] failed to load proxy files: %v", err)
	}

	// Load user modules AFTER proxy files so remote objects are available.
	// Without this, manual expose() calls in modules/*.lua are skipped and
	// the post-load exposeRegistry() exposes the ENTIRE registry to HA/HomeKit.
	s.luaEngine.LoadUserModules(s.serverDir)

	// Post-load hook: auto-expose registry (after plugins + user.lua)
	s.callOnLuaReset()

	// Auto-subscribe to remote CLUs for initial state sync (non-blocking)
	go s.triggerAutoSubscribe()

	log.Printf("[RESET] Lua engine reloaded")
}

// handleResetRequest handles a Reset request (unicast version)
func (s *Server) handleResetRequest(decryptedData []byte, usedKey *cipher.CipherKey, remoteAddr *net.UDPAddr) {
	req, err := commands.ParseResetRequest(decryptedData)
	if err != nil {
		log.Printf("[RESET] failed to parse request: %v", err)
		return
	}
	_ = req // parsed for validation

	// Stop TFTP server (files have been uploaded)
	if s.tftpServer != nil && s.tftpServer.IsRunning() {
		s.tftpServer.Stop()
	}

	s.reloadLuaEngine()

	// Create Reset response with our IP
	response := &commands.ResetResponse{
		IPAddress: s.localIP,
	}
	responseData := response.AsBytes()

	encryptedResponse, err := usedKey.Encrypt(responseData)
	if err != nil {
		log.Printf("[RESET] failed to encrypt response: %v", err)
		return
	}

	if _, err = s.responseConn.WriteToUDP(encryptedResponse, remoteAddr); err != nil {
		log.Printf("[RESET] failed to send response: %v", err)
	}
}

// handleResetBroadcast handles req_reset broadcast command
// Format: req_reset:<HOST_IP_ADDRESS>\r\n
// HOST_IP_ADDRESS is the sender's IP (Object Manager), not the target CLU
// This command is sent after TFTP upload to restart CLU with new Lua files
func (s *Server) handleResetBroadcast(cmdStr string, requestKey *cipher.CipherKey, remoteAddr *net.UDPAddr) {
	// Stop TFTP server (files have been uploaded)
	if s.tftpServer != nil && s.tftpServer.IsRunning() {
		s.tftpServer.Stop()
	}

	s.reloadLuaEngine()

	// Send acknowledgement response: resp_reset:<CLU_IP_ADDRESS>
	response := []byte("resp_reset:" + s.localIP.String() + "\r\n")
	encryptedResponse, err := requestKey.Encrypt(response)
	if err != nil {
		log.Printf("[RESET] failed to encrypt response: %v", err)
		return
	}

	if _, err = s.responseConn.WriteToUDP(encryptedResponse, remoteAddr); err != nil {
		log.Printf("[RESET] failed to send response: %v", err)
	}

	_ = strings.Split(cmdStr, ":") // keep for potential future use
}
