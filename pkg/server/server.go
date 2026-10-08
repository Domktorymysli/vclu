package server

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"log"
	"net"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/Domktorymysli/vclu/pkg/broker"
	"github.com/Domktorymysli/vclu/pkg/cipher"
	"github.com/Domktorymysli/vclu/pkg/commands"
	"github.com/Domktorymysli/vclu/pkg/config"
	"github.com/Domktorymysli/vclu/pkg/gpio"
	"github.com/Domktorymysli/vclu/pkg/lua"
	"github.com/Domktorymysli/vclu/pkg/omparser"
	"github.com/Domktorymysli/vclu/pkg/stats"
	"github.com/Domktorymysli/vclu/pkg/tftp"
)

const (
	BufferSize    = 2048
	CommandPort   = 1234
	TimeoutMillis = 1000
)

// Server represents the Virtual CLU server
type Server struct {
	serverDir    string
	aDriveDir    string // a/ subdirectory for CLU data (main.lua, om.lua, CONFIG.JSON, etc.)
	deviceConfig *config.CLUDeviceConfig
	keys         *config.CLUKeys
	localIP      net.IP
	macAddr      net.HardwareAddr

	projectCipherKey    *cipher.CipherKey
	broadcastCipherKeys []*cipher.CipherKey
	unicastCipherKeys   []*cipher.CipherKey
	temporaryCipherKey  *cipher.CipherKey
	temporaryIV         []byte

	broadcastConn *net.UDPConn
	commandConn   *net.UDPConn
	responseConn  *net.UDPConn

	grentonEnabled bool // whether Grenton UDP protocol is active

	luaEngine           *lua.LuaEngine
	tftpServer          *tftp.Server
	mqttBroker          *broker.Broker
	subscriptionManager *SubscriptionManager
	onLuaPreReset       func() // callback called BEFORE Lua engine is reset
	onPreLuaLoad        func() // callback called BEFORE LoadCLUData (access sync + plugins)
	onLuaReset          func() // callback called after Lua engine is reset
	metrics             *stats.AppMetrics

	clientReportSignal chan struct{} // event-driven client push signal

	mu     sync.RWMutex
	ctx    context.Context
	cancel context.CancelFunc
}

// SetOnLuaPreReset sets a callback that is called BEFORE Lua engine is reset
// Use this to block HomeKit callbacks before reload happens
func (s *Server) SetOnLuaPreReset(callback func()) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.onLuaPreReset = callback
}

// SetOnPreLuaLoad sets a callback that runs BEFORE LoadCLUData (e.g., access sync + plugin loading).
// This ensures plugins are available when user.lua executes.
func (s *Server) SetOnPreLuaLoad(callback func()) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.onPreLuaLoad = callback
}

// callPreLuaLoad calls the onPreLuaLoad callback if set
func (s *Server) callPreLuaLoad() {
	s.mu.RLock()
	callback := s.onPreLuaLoad
	s.mu.RUnlock()
	if callback != nil {
		callback()
	}
}

// SetOnLuaReset sets a callback that is called after Lua engine is reset/reinitialized
// This allows web server to sync MQTT access levels after reset
func (s *Server) SetOnLuaReset(callback func()) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.onLuaReset = callback
}

// callOnLuaPreReset calls the onLuaPreReset callback if set
func (s *Server) callOnLuaPreReset() {
	s.mu.RLock()
	callback := s.onLuaPreReset
	s.mu.RUnlock()
	if callback != nil {
		callback()
	}
}

// callOnLuaReset calls the onLuaReset callback if set
func (s *Server) callOnLuaReset() {
	s.mu.RLock()
	callback := s.onLuaReset
	s.mu.RUnlock()
	if callback != nil {
		callback()
	}
}

// LastModuleLoad reports the outcome of the most recent user-module load.
// Implements mcp.ModuleLoadReporter so reload/promote can surface a module that
// failed to load instead of reporting an unqualified success.
func (s *Server) LastModuleLoad() []lua.ModuleLoadResult {
	if s.luaEngine == nil {
		return nil
	}
	return s.luaEngine.LastModuleLoad()
}

// Reload implements the web.LuaReloader interface
// It reinitializes the Lua engine and reloads all CLU data
func (s *Server) Reload() error {
	log.Println("🔄 [RELOAD] Manual Lua reload requested")

	if s.luaEngine == nil {
		return fmt.Errorf("no Lua engine available")
	}

	// Call pre-reset callback (e.g., put HomeKit in initialization mode)
	s.callOnLuaPreReset()

	// Reinitialize Lua engine with fresh state
	if err := s.luaEngine.Reinitialize("VCLU"); err != nil {
		return fmt.Errorf("failed to reinitialize Lua engine: %w", err)
	}

	// Set firmware version string for CLU:get(17)
	s.luaEngine.SetGlobal("_FIRMWARE_VERSION", s.deviceConfig.FirmwareVersionStr)

	// Pre-load hook: access sync + plugins (before user.lua so Plugin.getPlugin works)
	s.callPreLuaLoad()

	// Reload CLU data from a/ directory
	cluDataPath := filepath.Join(s.serverDir, "a")
	if err := s.luaEngine.LoadCLUData(cluDataPath); err != nil {
		return fmt.Errorf("failed to reload CLU data: %w", err)
	}

	// Generate local proxy file (registers local DIN/DOUT in _registry)
	importsDir := config.GetOMFilesDir(s.serverDir)
	omparser.GenerateLocalProxyFile(cluDataPath, importsDir)

	// Load proxy files (remote + local)
	if err := s.luaEngine.LoadProxyFiles(importsDir); err != nil {
		log.Printf("[RELOAD] failed to load proxy files: %v", err)
	}

	// Load user modules AFTER proxy files so remote objects are available
	s.luaEngine.LoadUserModules(s.serverDir)

	// Post-load hook: auto-expose registry (after plugins + user.lua)
	s.callOnLuaReset()

	// Re-apply MQTT config to Lua and HA integration (Go connection survives reload)
	if clientConfig := config.GetMQTTClientConfig(s.serverDir); clientConfig != nil && clientConfig.Enabled {
		s.luaEngine.Execute(fmt.Sprintf(`MQTT:setHost(%q):setPort(%d):setClientId(%q):setCredentials(%q, %q)`,
			clientConfig.Host, clientConfig.Port, clientConfig.ClientID, clientConfig.Username, clientConfig.Password))
		if clientConfig.HAIntegration {
			s.luaEngine.Execute("HAIntegration.setup(MQTT)")
		}
	}

	// Auto-subscribe to remote CLUs for initial state sync (non-blocking)
	go s.triggerAutoSubscribe()

	log.Println("✅ [RELOAD] Lua reload complete")
	return nil
}

// initCipherKeys sets up Grenton cipher keys for the discovery protocol.
// In standalone mode, keys are not needed and initialization is skipped.
func initCipherKeys(deviceConfig *config.CLUDeviceConfig, keys *config.CLUKeys) {
	if deviceConfig.IsStandaloneMode() {
		log.Printf("Standalone mode — Grenton cipher keys skipped")
		return
	}

	if keys.BroadcastEnabled && len(keys.BroadcastKey) > 0 && len(keys.BroadcastIV) > 0 {
		cipher.SetDefaultBroadcastKey(keys.BroadcastKey, keys.BroadcastIV)
	} else {
		log.Printf("WARNING: Broadcast keys not configured — discovery will not work")
	}

	if len(keys.DefaultIV) > 0 {
		cipher.SetDefaultIV(keys.DefaultIV)
	} else {
		log.Printf("WARNING: DefaultIV not set — discovery handshake may fail")
	}
}

// NewServer creates a new Virtual CLU server
func NewServer(serverDir string, localIP net.IP, macAddr net.HardwareAddr) (*Server, error) {
	ctx, cancel := context.WithCancel(context.Background())

	// Load config from .vclu.json (source of truth) and propagate to all config files
	// This handles migration from legacy config.json/keys.json if needed
	deviceConfig, keys, err := config.LoadAndPropagateConfig(serverDir, localIP, macAddr)
	if err != nil {
		cancel()
		return nil, fmt.Errorf("failed to load config: %w", err)
	}
	log.Printf("Configuration loaded from .vclu.json and propagated to all config files")

	// Determine if Grenton protocol should be active
	masterConfig, _ := config.ReadMasterConfig(serverDir)
	grentonEnabled := false
	if masterConfig != nil {
		grentonEnabled = masterConfig.IsGrentonEnabled()
	}

	initCipherKeys(deviceConfig, keys)

	// a/ directory for CLU data (CONFIG.JSON already written by LoadAndPropagateConfig)
	aDriveDir := filepath.Join(serverDir, "a")

	projectCipherKey := cipher.NewCipherKey(keys.Key, keys.IV)

	// Create TFTP server
	tftpServer := tftp.NewServer(serverDir, localIP.String(), tftp.DefaultPort, tftp.GetAndReplace)

	// Create and start MQTT broker BEFORE Lua loads (so OnInit can connect)
	var mqttBroker *broker.Broker
	if brokerConfig := config.GetMQTTBrokerConfig(serverDir); brokerConfig != nil {
		mqttBroker = broker.New(&broker.Config{
			Enabled:  brokerConfig.Enabled,
			Port:     brokerConfig.Port,
			WSPort:   brokerConfig.WSPort,
			Username: brokerConfig.Username,
			Password: brokerConfig.Password,
		})
		// Start broker immediately so Lua OnInit can connect to it
		if err := mqttBroker.Start(); err != nil {
			log.Printf("WARNING: Failed to start MQTT broker: %v", err)
		}
	} else {
		// Default: disabled
		mqttBroker = broker.New(broker.DefaultConfig())
	}

	// Create GPIO controller (RPi hardware or mock fallback)
	gpioConfig := gpio.Config{BCMValidation: true}
	gpioCtrl := gpio.NewController(gpioConfig)

	// Create Lua engine
	luaEngine := lua.NewLuaEngine()

	// Inject GPIO manager into Lua engine
	luaEngine.SetGPIOManager(lua.NewGPIOManager(gpioCtrl))

	// Satel Integra alarm integration (optional, off by default)
	setupSatel(serverDir, luaEngine)

	// Set firmware version string for CLU:get(17)
	luaEngine.SetGlobal("_FIRMWARE_VERSION", deviceConfig.FirmwareVersionStr)

	// Set vCLU IP for RemoteSubscription (used in clientRegister calls)
	luaEngine.SetGlobal("__vclu_ip", localIP.String())

	// Initialize metrics collector early so MQTT connect can record stats
	metrics := stats.NewAppMetrics()
	luaEngine.GetMQTTManager().SetMetrics(metrics)

	// Set up metrics collector callbacks
	metrics.SetTimersActiveFunc(func() int {
		return luaEngine.GetTimerStats().ActiveTimers
	})
	metrics.SetPluginsActiveFunc(func() int {
		return luaEngine.GetPluginCount()
	})
	metrics.SetPluginStatsFunc(func() (*stats.PluginStatsResult, error) {
		luaStats, err := luaEngine.GetPluginStats()
		if err != nil {
			return nil, err
		}
		// Convert lua.AllPluginStats to stats.PluginStatsResult
		result := &stats.PluginStatsResult{
			Plugins:     make(map[string]stats.PluginDetailSnapshot),
			LuaMemoryKB: luaStats.LuaMemoryKB,
			Totals: stats.PluginTotals{
				Count:         luaStats.Totals.Count,
				Timers:        luaStats.Totals.Timers,
				Sensors:       luaStats.Totals.Sensors,
				Controls:      luaStats.Totals.Controls,
				Subscriptions: luaStats.Totals.Subscriptions,
				Exposed:       luaStats.Totals.Exposed,
			},
		}
		for id, p := range luaStats.Plugins {
			result.Plugins[id] = stats.PluginDetailSnapshot{
				ID:            p.ID,
				Name:          p.Name,
				Version:       p.Version,
				Timers:        p.Timers,
				Sensors:       p.Sensors,
				Controls:      p.Controls,
				Subscriptions: p.Subscriptions,
				Exposed:       p.Exposed,
			}
		}
		return result, nil
	})

	s := &Server{
		serverDir:           serverDir,
		aDriveDir:           aDriveDir,
		deviceConfig:        deviceConfig,
		keys:                keys,
		localIP:             localIP,
		macAddr:             macAddr,
		grentonEnabled:      grentonEnabled,
		projectCipherKey:    projectCipherKey,
		broadcastCipherKeys: []*cipher.CipherKey{cipher.DefaultBroadcastKey, projectCipherKey},
		unicastCipherKeys:   []*cipher.CipherKey{projectCipherKey},
		luaEngine:           luaEngine,
		tftpServer:          tftpServer,
		mqttBroker:          mqttBroker,
		subscriptionManager: NewSubscriptionManager(),
		metrics:             metrics,
		clientReportSignal:  make(chan struct{}, 1),
		ctx:                 ctx,
		cancel:              cancel,
	}

	// Enable CLU_REMOTE to send commands to real CLUs
	luaEngine.SetCommandSender(s.SendCommand)
	luaEngine.SetCommandNoReplySender(s.SendCommandNoReply)

	return s, nil
}

// LoadInitialData loads CLU data, plugins, proxy files, user modules, MQTT, and exposes registry.
// Must be called from main after plugin loader and callbacks are wired up.
func (s *Server) LoadInitialData() {
	// Pre-load hook: access sync + plugins (before user.lua so Plugin.getPlugin works)
	s.callPreLuaLoad()

	// Load CLU data from a/ directory (user.lua → om.lua → SYSTEM.Init)
	cluDataPath := filepath.Join(s.serverDir, "a")
	if err := s.luaEngine.LoadCLUData(cluDataPath); err != nil {
		log.Printf("WARNING: Failed to load CLU data from %s: %v", cluDataPath, err)
		log.Printf("CLU will run without user scripts")
	}

	// Generate local proxy file (registers local DIN/DOUT in _registry)
	importsDir := config.GetOMFilesDir(s.serverDir)
	omparser.GenerateLocalProxyFile(cluDataPath, importsDir)

	// Load proxy files (remote + local)
	if err := s.luaEngine.LoadProxyFiles(importsDir); err != nil {
		log.Printf("WARNING: Failed to load proxy files: %v", err)
	}

	// Load user modules AFTER proxy files so remote objects are available
	s.luaEngine.LoadUserModules(s.serverDir)

	// Auto-expose all registry objects to ExposedObjects (MQTT/HomeKit)
	if _, err := s.luaEngine.Execute("exposeRegistry()"); err != nil {
		log.Printf("WARNING: Failed to auto-expose registry: %v", err)
	}

	// Auto-connect MQTT client from config
	s.connectMQTT()

	// Auto-subscribe to remote CLU states (non-blocking)
	go s.triggerAutoSubscribe()
}

// connectMQTT auto-connects the MQTT client from config (client config takes priority over broker fallback)
func (s *Server) connectMQTT() {
	clientConfig := config.GetMQTTClientConfig(s.serverDir)
	if clientConfig != nil && clientConfig.Enabled {
		mm := s.luaEngine.GetMQTTManager()
		host := clientConfig.Host
		if host == "" {
			host = "localhost"
		}
		port := clientConfig.Port
		if port == 0 {
			port = 1883
		}
		clientID := clientConfig.ClientID
		if clientID == "" {
			clientID = "vclu_internal"
		}
		connectScript := fmt.Sprintf(`MQTT:setHost(%q):setPort(%d):setClientId(%q):setCredentials(%q, %q)`,
			host, port, clientID, clientConfig.Username, clientConfig.Password)
		s.luaEngine.Execute(connectScript)

		if err := mm.Reconnect(config.DefaultMQTTInstance, host, port, clientID, clientConfig.Username, clientConfig.Password); err != nil {
			log.Printf("WARNING: MQTT client initial connect failed (auto-reconnect active): %v", err)
		}

		if clientConfig.HAIntegration {
			if _, err := s.luaEngine.Execute("HAIntegration.setup(MQTT)"); err != nil {
				log.Printf("WARNING: HA integration setup failed: %v", err)
			}
		}
		if clientConfig.DefaultAccess != "" && clientConfig.DefaultAccess != "full" {
			s.luaEngine.Execute(fmt.Sprintf(`if AccessControl then AccessControl.DEFAULT_MQTT = %q end`, clientConfig.DefaultAccess))
		}
	} else if brokerConfig := config.GetMQTTBrokerConfig(s.serverDir); brokerConfig != nil && brokerConfig.Enabled {
		connectScript := fmt.Sprintf(`
			MQTT:setHost("localhost"):setPort(%d):setClientId("vclu_internal"):setCredentials("%s", "%s")
			local ok = MQTT:connect()
			if ok then HAIntegration.setup(MQTT) end
			return ok
		`, brokerConfig.Port, brokerConfig.Username, brokerConfig.Password)
		if _, err := s.luaEngine.Execute(connectScript); err != nil {
			log.Printf("WARNING: MQTT auto-connect failed: %v", err)
		}
	}
}

// Start starts the Virtual CLU server
func (s *Server) Start() error {
	if s.grentonEnabled {
		// Setup broadcast and command listener (single socket for everything)
		// Listen on 0.0.0.0 to receive broadcasts, system will choose correct source IP for replies
		listenAddr := &net.UDPAddr{
			IP:   net.IPv4zero,
			Port: CommandPort,
		}
		var err error
		s.commandConn, err = net.ListenUDP("udp4", listenAddr)
		if err != nil {
			return fmt.Errorf("failed to listen on command port: %w", err)
		}

		// Use the same connection for everything (broadcast, unicast, responses)
		s.broadcastConn = s.commandConn
		s.responseConn = s.commandConn

		// Start command handlers
		go s.handleBroadcastCommands()
		go s.handleUnicastCommands()

		// Start client reporter for push notifications (sending reports TO clients)
		s.startClientReporter()

		// Start client report listener (receiving reports FROM real CLUs)
		if err := s.startClientReportListener(); err != nil {
			log.Printf("WARNING: Failed to start client report listener: %v", err)
		}
	} else {
		log.Printf("Grenton protocol disabled (standalone or manually toggled off)")
	}

	log.Printf("Virtual CLU started")
	log.Printf("  Serial: %016x", s.deviceConfig.SerialNumber)
	log.Printf("  MAC: %s", s.macAddr.String())
	log.Printf("  IP: %s", s.localIP.String())
	if s.grentonEnabled {
		log.Printf("  PIN: %s", string(s.keys.PIN))
	}

	// MQTT broker is already started in NewServer() before Lua loads

	// Start Lua timer processing loop (for MQTT polling, etc.)
	go s.luaEngine.RunLoop(s.ctx.Done())

	// Start metrics collection
	s.metrics.Start()
	log.Printf("📊 Metrics collector started (24h retention, 1 min samples)")

	return nil
}

// Stop stops the server
func (s *Server) Stop() error {
	s.cancel()

	// Close command connection
	if s.commandConn != nil {
		_ = s.commandConn.Close()
	}
	// Close response connection (separate from command)
	if s.responseConn != nil && s.responseConn != s.commandConn {
		_ = s.responseConn.Close()
	}
	if s.luaEngine != nil {
		s.luaEngine.Close()
	}
	if s.tftpServer != nil {
		s.tftpServer.Stop()
	}
	if s.mqttBroker != nil {
		_ = s.mqttBroker.Stop()
	}
	if s.metrics != nil {
		s.metrics.Stop()
	}

	return nil
}

// GetMetrics returns the metrics collector
func (s *Server) GetMetrics() *stats.AppMetrics {
	return s.metrics
}

// GetMQTTBroker returns the MQTT broker instance
func (s *Server) GetMQTTBroker() *broker.Broker {
	return s.mqttBroker
}

// GetConfig returns the device configuration and keys
func (s *Server) GetConfig() (*config.CLUDeviceConfig, *config.CLUKeys) {
	return s.deviceConfig, s.keys
}

// GetLuaEngine returns the Lua engine
func (s *Server) GetLuaEngine() *lua.LuaEngine {
	return s.luaEngine
}

// GetServerDir returns the server directory
func (s *Server) GetServerDir() string {
	return s.serverDir
}

// IsGrentonEnabled returns whether Grenton UDP protocol is active
func (s *Server) IsGrentonEnabled() bool {
	return s.grentonEnabled
}

// NotifyClientPush signals the client reporter to send reports immediately
func (s *Server) NotifyClientPush() {
	select {
	case s.clientReportSignal <- struct{}{}:
	default: // already signaled, skip
	}
}

// handleBroadcastCommands handles broadcast commands (discovery, etc.)
func (s *Server) handleBroadcastCommands() {
	buffer := make([]byte, BufferSize)

	for {
		select {
		case <-s.ctx.Done():
			return
		default:
		}

		err := s.broadcastConn.SetReadDeadline(time.Now().Add(TimeoutMillis * time.Millisecond))
		if err != nil {
			log.Printf("WARNING: Failed to set read deadline: %v", err)
		}
		n, remoteAddr, err := s.broadcastConn.ReadFromUDP(buffer)
		if err != nil {
			if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
				continue
			}
			log.Printf("Error reading broadcast: %v", err)
			continue
		}

		data := buffer[:n]
		s.processBroadcastCommand(data, remoteAddr)
	}
}

// handleUnicastCommands handles unicast commands (Lua scripts, etc.)
func (s *Server) handleUnicastCommands() {
	buffer := make([]byte, BufferSize)

	for {
		select {
		case <-s.ctx.Done():
			return
		default:
		}

		s.commandConn.SetReadDeadline(time.Now().Add(TimeoutMillis * time.Millisecond))
		n, remoteAddr, err := s.commandConn.ReadFromUDP(buffer)
		if err != nil {
			if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
				continue
			}
			log.Printf("Error reading command: %v", err)
			continue
		}

		data := buffer[:n]
		s.processUnicastCommand(data, remoteAddr)
	}
}

// processBroadcastCommand processes a broadcast command
func (s *Server) processBroadcastCommand(encryptedData []byte, remoteAddr *net.UDPAddr) {
	// Try to decrypt with broadcast cipher keys
	var decryptedData []byte
	var usedKey *cipher.CipherKey

	s.mu.RLock()
	keys := make([]*cipher.CipherKey, len(s.broadcastCipherKeys))
	copy(keys, s.broadcastCipherKeys)
	s.mu.RUnlock()

	for _, key := range keys {
		if key == nil {
			continue
		}
		data, err := key.Decrypt(encryptedData)
		if err == nil {
			decryptedData = data
			usedKey = key
			break
		}
	}

	if decryptedData == nil {
		// This is normal - broadcast traffic from other devices we can't decrypt
		// Only log at debug level or skip entirely
		return
	}

	// Check if it's a full discovery request (with encrypted random bytes)
	if commands.IsDiscoveryRequest(decryptedData) {
		log.Printf("[BROADCAST] discovery from %s", remoteAddr.IP)
		s.handleDiscoveryRequestFull(decryptedData, remoteAddr)
		return
	}

	// Check for simple discovery (old format - just "dscvr")
	if bytes.HasPrefix(decryptedData, []byte("dscvr")) {
		log.Printf("[BROADCAST] simple discovery from %s", remoteAddr.IP)
		s.handleSimpleDiscoveryRequest(usedKey, remoteAddr)
		return
	}

	// Check if it's a SetKey request
	if commands.IsSetKeyRequest(decryptedData) {
		log.Printf("[BROADCAST] SetKey from %s", remoteAddr.IP)
		s.handleSetKeyRequest(decryptedData, usedKey, remoteAddr)
		return
	}

	// Check if it's a Lua script request
	req, err := commands.ParseLuaScriptRequest(decryptedData)
	if err == nil {
		log.Printf("[BROADCAST] lua script from %s", remoteAddr.IP)
		s.handleLuaScriptRequest(req, usedKey, remoteAddr)
		return
	}

	// Check for req_set_clu_ip command (sent after PIN entry)
	cmdStr := strings.TrimSpace(string(decryptedData))
	if strings.HasPrefix(cmdStr, "req_set_clu_ip:") {
		log.Printf("[BROADCAST] req_set_clu_ip from %s", remoteAddr.IP)
		s.handleSetCluIP(cmdStr, usedKey, remoteAddr)
		return
	}

	// Check for req_start_ftp command
	if strings.HasPrefix(cmdStr, "req_start_ftp") {
		log.Printf("[BROADCAST] req_start_ftp from %s", remoteAddr.IP)
		s.handleStartFTP(usedKey, remoteAddr)
		return
	}

	// Check for req_reset command
	if strings.HasPrefix(cmdStr, "req_reset") {
		log.Printf("[BROADCAST] req_reset from %s", remoteAddr.IP)
		s.handleResetBroadcast(cmdStr, usedKey, remoteAddr)
		return
	}

	log.Printf("[BROADCAST] unknown command from %s: %s", remoteAddr.IP, string(decryptedData[:min(50, len(decryptedData))]))
}

// processUnicastCommand processes a unicast command
func (s *Server) processUnicastCommand(encryptedData []byte, remoteAddr *net.UDPAddr) {
	// Try to decrypt with unicast cipher keys
	var decryptedData []byte
	var usedKey *cipher.CipherKey

	s.mu.RLock()
	keys := append([]*cipher.CipherKey{}, s.unicastCipherKeys...)
	if s.temporaryCipherKey != nil {
		keys = append([]*cipher.CipherKey{s.temporaryCipherKey}, keys...)
	}
	s.mu.RUnlock()

	for _, key := range keys {
		if key == nil {
			continue
		}
		data, err := key.Decrypt(encryptedData)
		if err == nil {
			decryptedData = data
			usedKey = key
			break
		}
	}

	if decryptedData == nil {
		// Try with broadcast keys as fallback (some messages come as unicast but use broadcast keys)
		s.processBroadcastCommand(encryptedData, remoteAddr)
		return
	}

	// Check if it's a SetKey request
	if commands.IsSetKeyRequest(decryptedData) {
		log.Printf("[UNICAST] SetKey from %s", remoteAddr.IP)
		s.handleSetKeyRequest(decryptedData, usedKey, remoteAddr)
		return
	}

	// Check if it's a SetIp request
	if commands.IsSetIpRequest(decryptedData) {
		log.Printf("[UNICAST] SetIp from %s", remoteAddr.IP)
		s.handleSetIpRequest(decryptedData, usedKey, remoteAddr)
		return
	}

	// Check if it's a Reset request
	if commands.IsResetRequest(decryptedData) {
		log.Printf("[UNICAST] Reset from %s", remoteAddr.IP)
		s.handleResetRequest(decryptedData, usedKey, remoteAddr)
		return
	}

	// Check if it's a StartTFTPd request (only with project key)
	if usedKey == s.projectCipherKey && commands.IsStartTFTPdRequest(decryptedData) {
		log.Printf("[UNICAST] StartTFTPd from %s", remoteAddr.IP)
		s.handleStartTFTPdRequest(decryptedData, usedKey, remoteAddr)
		return
	}

	// Check if it's a Lua script request
	req, err := commands.ParseLuaScriptRequest(decryptedData)
	if err == nil {
		log.Printf("[UNICAST] lua script from %s", remoteAddr.IP)
		s.handleLuaScriptRequest(req, usedKey, remoteAddr)
		return
	}

	log.Printf("[UNICAST] unknown command from %s", remoteAddr.IP)
}

// handleSimpleDiscoveryRequest handles a simple "dscvr" discovery request (legacy format)
func (s *Server) handleSimpleDiscoveryRequest(requestKey *cipher.CipherKey, remoteAddr *net.UDPAddr) {

	// Create discovery response (old format)
	response := &commands.DiscoverCLUsResponse{
		IPAddress:       s.localIP,
		SerialNumber:    s.deviceConfig.SerialNumber,
		MACAddress:      s.macAddr.String(),
		HardwareType:    s.deviceConfig.HardwareType,
		HardwareVersion: s.deviceConfig.HardwareVersion,
		FirmwareType:    s.deviceConfig.FirmwareType,
		FirmwareVersion: s.deviceConfig.FirmwareVersion,
	}

	responseData := response.AsBytes()

	// Encrypt response with broadcast key
	encryptedResponse, err := cipher.DefaultBroadcastKey.Encrypt(responseData)
	if err != nil {
		log.Printf("Failed to encrypt response: %v", err)
		return
	}

	// Send response
	_, err = s.responseConn.WriteToUDP(encryptedResponse, remoteAddr)
	if err != nil {
		log.Printf("Failed to send discovery response: %v", err)
		return
	}

	log.Printf("Sent simple discovery response to %s", remoteAddr.String())
}

// persistKeys saves the current keys to disk (.vclu.json + legacy keys.json)
func (s *Server) persistKeys() error {
	// Write to legacy keys.json
	if err := config.WriteKeys(s.serverDir, s.keys); err != nil {
		log.Printf("Warning: Failed to write keys.json: %v", err)
	}

	// Update .vclu.json (source of truth for LoadAndPropagateConfig at startup)
	masterConfig, err := config.ReadMasterConfig(s.serverDir)
	if err != nil || masterConfig == nil {
		masterConfig = &config.VCLUConfig{}
	}
	if masterConfig.Keys == nil {
		masterConfig.Keys = &config.VCLUKeysConfig{}
	}
	masterConfig.Keys.Key = base64.StdEncoding.EncodeToString(s.keys.Key)
	masterConfig.Keys.IV = base64.StdEncoding.EncodeToString(s.keys.IV)
	masterConfig.Keys.DefaultIV = base64.StdEncoding.EncodeToString(s.keys.DefaultIV)
	masterConfig.Keys.PIN = string(s.keys.PIN)
	masterConfig.Keys.BroadcastEnabled = s.keys.BroadcastEnabled
	if len(s.keys.BroadcastKey) > 0 {
		masterConfig.Keys.BroadcastKey = base64.StdEncoding.EncodeToString(s.keys.BroadcastKey)
	}
	if len(s.keys.BroadcastIV) > 0 {
		masterConfig.Keys.BroadcastIV = base64.StdEncoding.EncodeToString(s.keys.BroadcastIV)
	}
	return config.WriteMasterConfig(s.serverDir, masterConfig)
}
