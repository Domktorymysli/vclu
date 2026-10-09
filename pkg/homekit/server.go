package homekit

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"sort"
	"strings"
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
	Satel    bool        `json:"satel,omitempty"` // alarm objects: read-only in HomeKit
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
	aids          *aidStore
	signature     string          // exposed set the running bridge was built from
	built         []ExposedObject // objects the running bridge was built from
	initGen       uint64          // bumped by SetInitializing(true)
	stopped       bool            // Stop called: no automatic (re)start

	ctx    context.Context
	cancel context.CancelFunc
	mu     sync.RWMutex
	wg     sync.WaitGroup
	syncMu sync.Mutex // serializes Start/Stop/Sync
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
		aids:          loadAIDStore(dataDir),
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

// initializingMax ends a pause that nobody ends (e.g. a Lua reload that failed
// before its post-load hook): Home app writes must not be ignored for good.
var initializingMax = 60 * time.Second

// SetInitializing pauses (true) or resumes (false) Home app writes, e.g.
// around a Lua reload. A pause ends at the latest after initializingMax.
func (s *Server) SetInitializing(init bool) {
	s.mu.Lock()
	s.initializing = init
	if !init {
		s.mu.Unlock()
		return
	}
	s.initGen++
	gen := s.initGen
	s.mu.Unlock()
	time.AfterFunc(initializingMax, func() { s.endInitializing(gen) })
}

// InitGen identifies the current pause; take it when the work the pause
// covers is done, before anything that can wait (a newer pause may start).
func (s *Server) InitGen() uint64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.initGen
}

// ResumeGen ends pause gen after d, unless a newer pause began meanwhile.
func (s *Server) ResumeGen(gen uint64, d time.Duration) {
	time.AfterFunc(d, func() { s.endInitializing(gen) })
}

// ResumeAfter ends the current pause after d (see ResumeGen).
func (s *Server) ResumeAfter(d time.Duration) {
	s.ResumeGen(s.InitGen(), d)
}

func (s *Server) endInitializing(gen uint64) {
	s.mu.Lock()
	if s.initGen == gen {
		s.initializing = false
	}
	s.mu.Unlock()
}

// Start reads the exposed objects and starts the bridge. If they cannot be
// read (Lua busy), it does not publish an empty bridge: it retries instead.
func (s *Server) Start() error {
	s.syncMu.Lock()
	defer s.syncMu.Unlock()
	s.stopped = false
	objs, err := s.fetchExposed()
	if err != nil {
		go s.retryStart()
		return fmt.Errorf("exposed objects not read, retrying: %w", err)
	}
	return s.start(objs)
}

// retryStart retries a failed Start with backoff until it succeeds or Stop.
func (s *Server) retryStart() {
	for delay := 5 * time.Second; ; delay = min(2*delay, time.Minute) {
		time.Sleep(delay)
		s.syncMu.Lock()
		s.mu.RLock()
		done := s.stopped || s.hapServer != nil
		s.mu.RUnlock()
		if done {
			s.syncMu.Unlock()
			return
		}
		objs, err := s.fetchExposed()
		if err == nil {
			err = s.start(objs)
		}
		s.syncMu.Unlock()
		if err == nil {
			return
		}
		log.Printf("[HomeKit] bridge start failed, retrying: %v", err)
	}
}

// Sync brings a running bridge in line with the exposed objects after a Lua
// reload or an access change. It adds objects and applies access levels, but
// keeps objects that are missing now: a module that failed to load or a read
// in the middle of a reload must not remove accessories (the Home app would
// drop their rooms and automations). An object leaves the bridge only when
// hidden for HomeKit or on an explicit Reload. A stopped bridge stays stopped.
func (s *Server) Sync() error {
	s.syncMu.Lock()
	defer s.syncMu.Unlock()
	s.mu.RLock()
	running := s.hapServer != nil
	built := s.built
	s.mu.RUnlock()
	if !running {
		return nil
	}
	objs, err := s.fetchExposed()
	if err != nil {
		return err
	}
	if kept := missingPaths(objs, built); len(kept) > 0 {
		log.Printf("[HomeKit] kept %d accessories no longer exposed (restart the bridge to remove them): %s",
			len(kept), strings.Join(kept, ", "))
	}
	objs = mergeExposed(objs, built)
	s.mu.RLock()
	same := s.signatureOf(objs) == s.signature
	ctx := s.ctx
	s.mu.RUnlock()
	if same {
		s.refreshAllStates(ctx)
		return nil
	}
	s.stop()
	return s.start(objs)
}

// missingPaths lists paths of previous that current lacks.
func missingPaths(current, previous []ExposedObject) []string {
	seen := make(map[string]bool, len(current))
	for _, o := range current {
		seen[o.Path] = true
	}
	var out []string
	for _, o := range previous {
		if !seen[o.Path] {
			out = append(out, o.Path)
		}
	}
	return out
}

// mergeExposed returns current plus the objects of previous that current
// lacks (hidden ones drop out later, in buildAccessories).
func mergeExposed(current, previous []ExposedObject) []ExposedObject {
	seen := make(map[string]bool, len(current))
	for _, o := range current {
		seen[o.Path] = true
	}
	out := current
	for _, o := range previous {
		if !seen[o.Path] {
			out = append(out, o)
		}
	}
	return out
}

func (s *Server) start(objs []ExposedObject) error {
	// Must be in place before hap spins up its mDNS responder, otherwise the
	// first dnssd log lines bypass the filter.
	installDnssdLogFilter()

	s.mu.Lock()
	if s.hapServer != nil {
		s.mu.Unlock()
		return fmt.Errorf("HomeKit server already running")
	}
	s.mu.Unlock()

	accessories := s.buildAccessories(objs)
	if err := s.aidStore().save(); err != nil {
		log.Printf("[HomeKit] Warning: accessory ids not saved: %v", err)
	}

	// Fixed order by accessory id: stable configuration for the Home app
	all := make([]*accessory.A, 0, len(accessories))
	for _, w := range accessories {
		all = append(all, w.Accessory)
	}
	sort.Slice(all, func(i, j int) bool { return all[i].Id < all[j].Id })

	bridge := accessory.NewBridge(accessory.Info{
		Name:         s.config.BridgeName,
		SerialNumber: s.config.SerialNumber,
		Manufacturer: "vCLU",
		Model:        "HomeKit Bridge",
		Firmware:     "1.0.0",
	})

	store := hap.NewFsStore(s.dataDir + "/homekit-db")
	hapServer, err := hap.NewServer(store, bridge.A, all...)
	if err != nil {
		return fmt.Errorf("failed to create HAP server: %w", err)
	}
	hapServer.Pin = s.config.Pin
	hapServer.Addr = fmt.Sprintf(":%d", s.config.Port)

	ctx, cancel := context.WithCancel(context.Background())
	s.mu.Lock()
	s.ctx, s.cancel = ctx, cancel
	s.bridge = bridge
	s.hapServer = hapServer
	s.accessories = accessories
	s.signature = s.signatureOf(objs)
	s.built = objs
	s.mu.Unlock()

	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		log.Printf("[HomeKit] Starting server on port %d with %d accessories", s.config.Port, len(accessories))
		if err := hapServer.ListenAndServe(ctx); err != nil {
			if ctx.Err() == nil {
				log.Printf("[HomeKit] Server error: %v", err)
			}
		}
	}()

	// Ignore Home app writes while the controllers resync
	s.SetInitializing(true)
	s.ResumeAfter(5 * time.Second)

	s.startPeriodicRefresh(ctx)
	s.startDnssdWatchdog(ctx)
	return nil
}

// Stop stops the HomeKit server
func (s *Server) Stop() error {
	s.syncMu.Lock()
	defer s.syncMu.Unlock()
	s.stopped = true
	s.stop()
	return nil
}

// stop cancels the server and waits for its goroutines without holding s.mu:
// they may be waiting on the Lua loop, which may be waiting on s.mu.
func (s *Server) stop() {
	s.mu.Lock()
	cancel := s.cancel
	s.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	s.wg.Wait()

	s.mu.Lock()
	s.hapServer = nil
	s.bridge = nil
	s.cancel = nil
	s.signature = ""
	s.built = nil
	s.accessories = make(map[string]*AccessoryWrapper)
	s.mu.Unlock()
	log.Printf("[HomeKit] Server stopped")
}

// reloadUnlessStopped is Reload for background recovery (dnssd watchdog): it
// must not bring the bridge back after Stop.
func (s *Server) reloadUnlessStopped() error {
	s.syncMu.Lock()
	defer s.syncMu.Unlock()
	if s.stopped {
		return nil
	}
	return s.reloadLocked()
}

// Reload restarts the bridge with the current exposed objects; objects no
// longer exposed leave it. A failed read keeps the running bridge.
func (s *Server) Reload() error {
	s.syncMu.Lock()
	defer s.syncMu.Unlock()
	s.stopped = false
	return s.reloadLocked()
}

// reloadLocked is Reload with syncMu held.
func (s *Server) reloadLocked() error {
	objs, err := s.fetchExposed()
	if err != nil {
		return fmt.Errorf("bridge kept, exposed objects not read: %w", err)
	}
	s.stop()
	return s.start(objs)
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

// fetchExposed reads the objects exposed to HomeKit from Lua.
func (s *Server) fetchExposed() ([]ExposedObject, error) {
	result, err := s.luaExecutor.Execute("return ExposedObjects:toJSON({homekit = true})")
	if err != nil {
		return nil, fmt.Errorf("failed to get exposed objects: %w", err)
	}
	if result == "" || result == "nil" || result == "[]" {
		return nil, nil
	}
	var objects []ExposedObject
	if err := json.Unmarshal([]byte(result), &objects); err != nil {
		return nil, fmt.Errorf("failed to parse exposed objects JSON: %w", err)
	}
	return objects, nil
}

// effectiveAccess applies the HomeKit access profile and the Satel rule.
func (s *Server) effectiveAccess(obj ExposedObject) (hidden, readonly bool) {
	if s.accessManager != nil {
		hidden = s.accessManager.IsHidden(accesscontrol.IntegrationHomeKit, obj.Path)
		readonly = s.accessManager.IsReadonly(accesscontrol.IntegrationHomeKit, obj.Path)
	}
	return hidden, readonly || obj.Readonly || obj.Satel
}

// signatureOf describes what the bridge would contain; equal signatures need
// no rebuild.
func (s *Server) signatureOf(objs []ExposedObject) string {
	lines := make([]string, 0, len(objs))
	for _, o := range objs {
		hidden, ro := s.effectiveAccess(o)
		lines = append(lines, fmt.Sprintf("%s\x00%s\x00%s\x00%t\x00%t", o.Path, o.Type, o.displayName(), hidden, ro))
	}
	sort.Strings(lines)
	return strings.Join(lines, "\n")
}

// buildAccessories turns exposed objects into accessories with stable ids.
func (s *Server) buildAccessories(objs []ExposedObject) map[string]*AccessoryWrapper {
	// Over the limit the newest objects are left out, not ones that already
	// have an id (and maybe rooms and automations in the Home app)
	ordered := append([]ExposedObject(nil), objs...)
	store := s.aidStore()
	sort.SliceStable(ordered, func(i, j int) bool {
		a, aok := store.known(ordered[i].Path)
		b, bok := store.known(ordered[j].Path)
		if aok != bok {
			return aok
		}
		return a < b
	})
	out := make(map[string]*AccessoryWrapper, len(objs))
	for _, obj := range ordered {
		// Hidden wins over readonly: a hidden object never becomes an accessory
		hidden, readonly := s.effectiveAccess(obj)
		if hidden {
			continue
		}
		if len(out) >= maxBridgedAccessories {
			log.Printf("[HomeKit] more than %d accessories, %s and the rest are left out (HomeKit limit)", maxBridgedAccessories, obj.Path)
			break
		}
		wrapper := s.createExposedAccessory(obj, readonly)
		if wrapper == nil {
			continue
		}
		wrapper.Accessory.Id = s.aidStore().id(obj.Path)
		out[obj.Path] = wrapper
	}
	return out
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
	p := luaString(path)
	script := fmt.Sprintf(`
		local h = ExposedObjects:get(%s)
		if h then return tostring(h:getValue() or 0) end
		local r = _.get(%s)
		if r then return tostring(r:getValue() or 0) end
		return "nil"
	`, p, p)

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
func (s *Server) refreshAllStates(ctx context.Context) {
	s.mu.RLock()
	accessories := make(map[string]*AccessoryWrapper, len(s.accessories))
	for k, v := range s.accessories {
		accessories[k] = v
	}
	metrics := s.metrics
	s.mu.RUnlock()

	refreshed := 0
	for path, wrapper := range accessories {
		if ctx != nil && ctx.Err() != nil {
			return // stopping: do not keep the Lua loop busy
		}
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
func (s *Server) startPeriodicRefresh(ctx context.Context) {
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
			case <-ctx.Done():
				return
			case <-ticker.C:
				// Don't refresh during initialization
				if s.IsInitializing() {
					continue
				}
				s.refreshAllStates(ctx)
			}
		}
	}()
}

// aidStore returns the id store; a Server built without NewServer (tests)
// keeps ids in memory only.
func (s *Server) aidStore() *aidStore {
	if s.aids == nil {
		s.aids = loadAIDStore("")
	}
	return s.aids
}

// maxBridgedAccessories is the HAP limit of 150 accessories per bridge,
// the bridge itself included.
var maxBridgedAccessories = 149

// luaString returns s as a Lua string literal for generated scripts.
func luaString(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`, "\r", `\r`, "\x00", `\000`)
	return `"` + r.Replace(s) + `"`
}
