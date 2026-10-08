package homekit

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/brutella/hap"
	"github.com/brutella/hap/accessory"

	"github.com/Domktorymysli/vclu/pkg/accesscontrol"
	"github.com/Domktorymysli/vclu/pkg/config"
	"github.com/Domktorymysli/vclu/pkg/stats"
)

// LuaExecutor interface for executing Lua code
type LuaExecutor interface {
	Execute(code string) (string, error)
}

// executeCommand runs a command from the Home app; the changes it makes are
// attributed to HomeKit (the Home app does not tell which Apple user it was).
func (s *Server) executeCommand(script string) (string, error) {
	if ce, ok := s.luaExecutor.(interface {
		ExecuteAs(origin, actor, correlationID, script string) (string, error)
	}); ok {
		return ce.ExecuteAs("homekit", "service:homekit", "", script)
	}
	return s.luaExecutor.Execute(script)
}

// ExposedObject represents an object from ExposedObjects API
type ExposedObject struct {
	Path     string      `json:"path"`
	Type     string      `json:"type"`
	Name     string      `json:"name"`
	Area     string      `json:"area"`
	Value    any         `json:"value"`
	Readonly bool        `json:"readonly"`
	Caps     ExposedCaps `json:"caps"`
}

// displayName prefixes the accessory name with its room, so that during Apple Home
// pairing each accessory is distinguishable (otherwise many objects share a bare
// name like "Światło"). HomeKit has no per-accessory room hint (unlike HA's
// suggested_area), so the room in the name is the only setup aid we can give.
func (o ExposedObject) displayName() string {
	if o.Area != "" && o.Name != "" {
		return o.Area + " " + o.Name
	}
	if o.Name != "" {
		return o.Name
	}
	return o.Path
}

// ExposedCaps represents capabilities of an exposed object
type ExposedCaps struct {
	Binary     bool `json:"binary,omitempty"`
	Brightness bool `json:"brightness,omitempty"`
	Position   bool `json:"position,omitempty"`
	Execute    bool `json:"execute,omitempty"`
	Stateless  bool `json:"stateless,omitempty"`
}

// ServerStatus represents the current status of the HomeKit server
type ServerStatus struct {
	Running          bool   `json:"running"`
	AccessoryCount   int    `json:"accessoryCount"`
	Pin              string `json:"pin"`
	BridgeName       string `json:"bridgeName"`
	Port             int    `json:"port"`
	SetupURI         string `json:"setupUri,omitempty"`
	PairingAvailable bool   `json:"pairingAvailable"`
}

// Server manages the HomeKit integration
type Server struct {
	config        *config.VCLUHomeKitConfig
	hapServer     *hap.Server
	bridge        *accessory.Bridge
	accessories   map[string]*AccessoryWrapper // path → accessory wrapper
	luaExecutor   LuaExecutor
	accessManager *accesscontrol.Manager
	dataDir       string
	metrics       *stats.AppMetrics
	initializing  bool // true during startup to prevent sync callbacks

	ctx    context.Context
	cancel context.CancelFunc
	mu     sync.RWMutex
	wg     sync.WaitGroup
}

// AccessoryWrapper wraps an accessory with its metadata
type AccessoryWrapper struct {
	Accessory      *accessory.A
	Path           string
	Type           string
	Readonly       bool
	UpdateFunc     func(value float64) // Function to update accessory state
	LastKnownState float64             // Last known state for comparison
}

// NewServer creates a new HomeKit server
func NewServer(cfg *config.VCLUHomeKitConfig, luaExec LuaExecutor, accessMgr *accesscontrol.Manager, dataDir string) *Server {
	return &Server{
		config:        cfg,
		luaExecutor:   luaExec,
		accessManager: accessMgr,
		dataDir:       dataDir,
		accessories:   make(map[string]*AccessoryWrapper),
	}
}

// SetMetrics sets the metrics collector
func (s *Server) SetMetrics(m *stats.AppMetrics) {
	s.mu.Lock()
	s.metrics = m
	s.mu.Unlock()
}

// IsInitializing returns true if server is still initializing (ignore sync callbacks)
func (s *Server) IsInitializing() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.initializing
}

// SetInitializing sets the initializing flag to temporarily block callbacks
// Called during Lua reload to prevent HAP callbacks from firing while state syncs
func (s *Server) SetInitializing(init bool) {
	s.mu.Lock()
	s.initializing = init
	s.mu.Unlock()
	// (no log — caller context is sufficient)
}

// Start starts the HomeKit server
func (s *Server) Start() error {
	// Must be in place before hap spins up its mDNS responder, otherwise the
	// first dnssd log lines bypass the filter.
	installDnssdLogFilter()

	s.mu.Lock()
	if s.hapServer != nil {
		s.mu.Unlock()
		return fmt.Errorf("HomeKit server already running")
	}

	// Create context for server
	s.ctx, s.cancel = context.WithCancel(context.Background())

	// Set initializing flag to prevent callbacks during startup
	s.initializing = true

	// Create bridge accessory
	bridgeInfo := accessory.Info{
		Name:         s.config.BridgeName,
		SerialNumber: s.config.SerialNumber,
		Manufacturer: "vCLU",
		Model:        "HomeKit Bridge",
		Firmware:     "1.0.0",
	}
	s.bridge = accessory.NewBridge(bridgeInfo)
	s.mu.Unlock()

	// Load registry objects and create accessories
	// NOTE: We release the lock before calling loadAccessories() because it calls
	// lua.Execute() which can trigger Lua code that calls NotifyStateChange()
	// which needs s.mu.RLock(). Holding s.mu.Lock() here would cause a deadlock.
	if err := s.loadAccessories(); err != nil {
		log.Printf("[HomeKit] Warning: failed to load accessories: %v", err)
	}

	s.mu.Lock()

	// Collect all accessories for the server
	var allAccessories []*accessory.A
	for _, wrapper := range s.accessories {
		allAccessories = append(allAccessories, wrapper.Accessory)
	}

	// Create HAP server with file storage
	store := hap.NewFsStore(s.dataDir + "/homekit-db")
	hapServer, err := hap.NewServer(store, s.bridge.A, allAccessories...)
	if err != nil {
		s.mu.Unlock()
		return fmt.Errorf("failed to create HAP server: %w", err)
	}

	s.hapServer = hapServer
	s.hapServer.Pin = s.config.Pin
	s.hapServer.Addr = fmt.Sprintf(":%d", s.config.Port)

	// Start server in background
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		log.Printf("[HomeKit] Starting server on port %d with %d accessories", s.config.Port, len(s.accessories))
		if err := s.hapServer.ListenAndServe(s.ctx); err != nil {
			if s.ctx.Err() == nil {
				log.Printf("[HomeKit] Server error: %v", err)
			}
		}
	}()

	// Clear initializing flag after a delay to allow HAP protocol to sync
	go func() {
		time.Sleep(5 * time.Second)
		s.mu.Lock()
		s.initializing = false
		s.mu.Unlock()
		// initialization complete — callbacks now enabled
	}()

	// Start periodic state refresh
	s.startPeriodicRefresh()

	// Watch for the dnssd netlink loop (see dnssd_watchdog.go)
	s.startDnssdWatchdog()

	s.mu.Unlock()
	return nil
}

// Stop stops the HomeKit server
func (s *Server) Stop() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.cancel != nil {
		s.cancel()
	}

	// Wait for goroutines to finish
	s.wg.Wait()

	s.hapServer = nil
	s.bridge = nil
	s.accessories = make(map[string]*AccessoryWrapper)

	log.Printf("[HomeKit] Server stopped")
	return nil
}

// Reload reloads accessories from registry
func (s *Server) Reload() error {
	// Stop and restart the server to reload accessories
	if err := s.Stop(); err != nil {
		return err
	}
	return s.Start()
}

// GetStatus returns the current server status
func (s *Server) GetStatus() ServerStatus {
	s.mu.RLock()
	defer s.mu.RUnlock()

	status := ServerStatus{
		Running:        s.hapServer != nil,
		AccessoryCount: len(s.accessories),
		Pin:            s.config.Pin,
		BridgeName:     s.config.BridgeName,
		Port:           s.config.Port,
	}

	if s.hapServer != nil {
		status.PairingAvailable = !s.hapServer.IsPaired()
	}

	return status
}

// loadAccessories loads objects from ExposedObjects, creates HomeKit accessories
func (s *Server) loadAccessories() error {
	if err := s.loadExposedAccessories(); err != nil {
		log.Printf("[HomeKit] failed to load exposed accessories: %v", err)
	}

	return nil
}

// loadRegistryAccessories loads objects from the legacy registry
// loadExposedAccessories loads objects from the ExposedObjects API
func (s *Server) loadExposedAccessories() error {
	// Get exposed objects via Lua (filter for homekit=true)
	result, err := s.luaExecutor.Execute("return ExposedObjects:toJSON({homekit = true})")
	if err != nil {
		return fmt.Errorf("failed to get exposed objects: %w", err)
	}

	if result == "" || result == "nil" || result == "[]" {
		return nil
	}

	var objects []ExposedObject
	if err := json.Unmarshal([]byte(result), &objects); err != nil {
		return fmt.Errorf("failed to parse exposed objects JSON: %w", err)
	}

	log.Printf("[HomeKit] loaded %d exposed objects", len(objects))

	for _, obj := range objects {
		// Hidden wins over everything: a Hidden object must not become an accessory,
		// even if it was exposed with readonly=true (hidden is the stricter level).
		if s.accessManager.IsHidden(accesscontrol.IntegrationHomeKit, obj.Path) {
			continue
		}

		readonly := obj.Readonly || s.accessManager.IsReadonly(accesscontrol.IntegrationHomeKit, obj.Path)

		// Create accessory
		wrapper := s.createExposedAccessory(obj, readonly)
		if wrapper != nil {
			s.accessories[obj.Path] = wrapper
		}
	}

	return nil
}

// RestartBridge restarts the HomeKit bridge (called when objects are exposed/unexposed)
func (s *Server) RestartBridge() error {
	return s.Reload()
}

// NotifyStateChange is called when a state changes in Lua (via __go_homekit_notify)
func (s *Server) NotifyStateChange(path string, value float64, objType string) {
	s.mu.RLock()
	wrapper, ok := s.accessories[path]
	metrics := s.metrics
	s.mu.RUnlock()

	if !ok {
		return // Accessory not found (might be hidden or not loaded)
	}

	if wrapper.UpdateFunc != nil {
		wrapper.UpdateFunc(value)
		// Record HomeKit event (state change sent to HomeKit)
		if metrics != nil {
			metrics.RecordHomeKitEvent()
		}
	}
}

// GetAccessories returns list of accessories with their access levels
func (s *Server) GetAccessories() []map[string]interface{} {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var result []map[string]interface{}
	for path, wrapper := range s.accessories {
		access := s.accessManager.GetAccess(accesscontrol.IntegrationHomeKit, path)
		result = append(result, map[string]interface{}{
			"path":     path,
			"type":     wrapper.Type,
			"access":   string(access),
			"readonly": wrapper.Readonly,
		})
	}
	return result
}

// GetCurrentState fetches current state from Lua for a given path
// Returns the state value and true if successful, or 0 and false if failed
func (s *Server) GetCurrentState(path string) (float64, bool) {
	// Try ExposedObjects first
	script := fmt.Sprintf(`
		local h = ExposedObjects:get("%s")
		if h then return tostring(h:getValue() or 0) end
		local r = _.get("%s")
		if r then return tostring(r:getValue() or 0) end
		return "nil"
	`, path, path)

	result, err := s.luaExecutor.Execute(script)
	if err != nil {
		log.Printf("[HomeKit] GetCurrentState error for %s: %v", path, err)
		return 0, false
	}

	if result == "nil" || result == "" {
		return 0, false
	}

	var value float64
	if _, err := fmt.Sscanf(result, "%f", &value); err != nil {
		log.Printf("[HomeKit] GetCurrentState parse error for %s: %v (result=%s)", path, err, result)
		return 0, false
	}

	return value, true
}

// refreshAllStates refreshes all accessory states from Lua
func (s *Server) refreshAllStates() {
	s.mu.RLock()
	accessories := make(map[string]*AccessoryWrapper, len(s.accessories))
	for k, v := range s.accessories {
		accessories[k] = v
	}
	metrics := s.metrics
	s.mu.RUnlock()

	refreshed := 0
	for path, wrapper := range accessories {
		value, ok := s.GetCurrentState(path)
		if !ok {
			continue
		}

		// Update HomeKit state
		if wrapper.UpdateFunc != nil {
			wrapper.UpdateFunc(value)
			refreshed++
		}
	}

	if metrics != nil {
		metrics.RecordHomeKitEvent()
	}
}

// startPeriodicRefresh starts a goroutine that periodically refreshes all accessory states
func (s *Server) startPeriodicRefresh() {
	interval := s.config.RefreshInterval
	if interval <= 0 {
		return
	}

	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		ticker := time.NewTicker(time.Duration(interval) * time.Second)
		defer ticker.Stop()

		for {
			select {
			case <-s.ctx.Done():
				return
			case <-ticker.C:
				// Don't refresh during initialization
				if s.IsInitializing() {
					continue
				}
				s.refreshAllStates()
			}
		}
	}()
}
