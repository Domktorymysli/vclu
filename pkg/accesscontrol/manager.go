package accesscontrol

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"

	"gopkg.in/yaml.v3"
)

// Integration represents an integration type (mqtt, homekit, etc.)
type Integration string

const (
	IntegrationMQTT    Integration = "mqtt"
	IntegrationHomeKit Integration = "homekit"
	IntegrationMCP     Integration = "mcp"
	// IntegrationRemote is access from outside the local network; closed by default.
	IntegrationRemote Integration = "remote"
)

// AllIntegrations returns all available integrations
func AllIntegrations() []Integration {
	return []Integration{IntegrationMQTT, IntegrationHomeKit, IntegrationMCP, IntegrationRemote}
}

// IsIntegration reports whether s names a known integration.
func IsIntegration(s string) bool {
	for _, intg := range AllIntegrations() {
		if string(intg) == s {
			return true
		}
	}
	return false
}

// DefaultProfile is the profile an integration gets when nothing else is assigned.
func DefaultProfile(intg Integration) string {
	if intg == IntegrationRemote {
		return BuiltinProfileNoAccess
	}
	return BuiltinProfileFullAccess
}

// defaultLevel is the access of an integration with no profiles at all.
func defaultLevel(intg Integration) AccessLevel {
	if intg == IntegrationRemote {
		return AccessHidden
	}
	return AccessFull // backwards compat
}

func isBuiltinProfile(name string) bool {
	return name == BuiltinProfileFullAccess || name == BuiltinProfileNoAccess
}

// AccessLevel defines the access level for an object
type AccessLevel string

const (
	AccessFull     AccessLevel = "full"
	AccessReadonly AccessLevel = "readonly"
	AccessHidden   AccessLevel = "hidden"
)

// accessLevelOrder returns the numeric order for merging (higher = more permissive).
func accessLevelOrder(l AccessLevel) int {
	switch l {
	case AccessHidden:
		return 0
	case AccessReadonly:
		return 1
	case AccessFull:
		return 2
	default:
		return 2 // unknown defaults to full
	}
}

// Profile is a named set of access rules.
type Profile struct {
	Default AccessLevel            `yaml:"default"`
	Objects map[string]AccessLevel `yaml:"objects,omitempty"`
}

// ObjectAccess represents access settings for a single object across all integrations (legacy format).
type ObjectAccess map[Integration]AccessLevel

// Config represents the access_control.yaml file structure.
type Config struct {
	Profiles    map[string]*Profile     `yaml:"profiles,omitempty"`
	Assignments map[string][]string     `yaml:"assignments,omitempty"` // integration name → profile names
	Objects     map[string]ObjectAccess `yaml:"objects,omitempty"`     // legacy format, migrated on load
}

// BuiltinProfileFullAccess is the name of the built-in full-access profile.
const BuiltinProfileFullAccess = "full-access"

// BuiltinProfileNoAccess hides everything; remote access starts with it.
const BuiltinProfileNoAccess = "no-access"

// Manager manages access control settings for all integrations
type Manager struct {
	configPath string
	config     Config
	mu         sync.RWMutex

	// Failed load (unreadable/malformed YAML) — deny everything until fixed.
	// A missing file is not a failure; Load() handles that as a fresh install.
	loadErr error
}

// NewManager creates a new access control manager
func NewManager(dataDir string) *Manager {
	return &Manager{
		configPath: filepath.Join(dataDir, "access_control.yaml"),
		config:     newEmptyConfig(),
	}
}

func newEmptyConfig() Config {
	return Config{
		Profiles:    map[string]*Profile{},
		Assignments: map[string][]string{},
	}
}

// ensureBuiltinProfiles makes sure built-in profiles always exist.
func (m *Manager) ensureBuiltinProfiles() {
	if _, ok := m.config.Profiles[BuiltinProfileFullAccess]; !ok {
		m.config.Profiles[BuiltinProfileFullAccess] = &Profile{
			Default: AccessFull,
		}
	}
	// Always hidden, also when the file says otherwise: remote must not open by an edit
	if p, ok := m.config.Profiles[BuiltinProfileNoAccess]; ok && (p.Default != AccessHidden || len(p.Objects) > 0) {
		log.Printf("[AccessControl] Built-in profile %q reset to hidden", BuiltinProfileNoAccess)
	}
	m.config.Profiles[BuiltinProfileNoAccess] = &Profile{Default: AccessHidden}
}

// ensureAssignments gives every integration without an assignment its default profile.
func (m *Manager) ensureAssignments() {
	for _, intg := range AllIntegrations() {
		if _, ok := m.config.Assignments[string(intg)]; !ok {
			m.config.Assignments[string(intg)] = []string{DefaultProfile(intg)}
		}
	}
}

// Load reads the config from disk and performs migration if needed.
func (m *Manager) Load() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.loadErr = nil

	data, err := os.ReadFile(m.configPath)
	if err != nil {
		if os.IsNotExist(err) {
			m.config = newEmptyConfig()
			m.ensureBuiltinProfiles()
			m.ensureAssignments()
			return nil
		}
		m.loadErr = fmt.Errorf("failed to read access_control.yaml: %w", err)
		return m.loadErr
	}

	var config Config
	if err := yaml.Unmarshal(data, &config); err != nil {
		m.loadErr = fmt.Errorf("failed to parse access_control.yaml: %w", err)
		return m.loadErr
	}

	if config.Profiles == nil {
		config.Profiles = map[string]*Profile{}
	}
	if config.Assignments == nil {
		config.Assignments = map[string][]string{}
	}

	m.config = config

	// Migrate legacy format: objects → profiles
	if len(m.config.Objects) > 0 && len(m.config.Profiles) == 0 {
		m.migrateLegacy()
	}

	m.ensureBuiltinProfiles()

	// Ensure every integration has at least one assignment
	m.ensureAssignments()

	return nil
}

// migrateLegacy converts the old per-object-per-integration format into profiles.
// Creates one profile per integration with non-default entries, assigns it.
func (m *Manager) migrateLegacy() {
	log.Printf("[AccessControl] Migrating legacy access_control.yaml to profile format")

	// Collect per-integration object maps
	intgObjects := map[Integration]map[string]AccessLevel{}
	for path, obj := range m.config.Objects {
		for intg, level := range obj {
			if level == AccessFull {
				continue // skip default
			}
			if intgObjects[intg] == nil {
				intgObjects[intg] = map[string]AccessLevel{}
			}
			intgObjects[intg][path] = level
		}
	}

	// Create one profile per integration that had non-default entries
	for intg, objects := range intgObjects {
		name := "migrated-" + string(intg)
		m.config.Profiles[name] = &Profile{
			Default: defaultLevel(intg),
			Objects: objects,
		}
		m.config.Assignments[string(intg)] = []string{name}
	}

	// Integrations with no overrides get their default profile
	m.ensureBuiltinProfiles()
	m.ensureAssignments()

	// Drop legacy section
	m.config.Objects = nil

	log.Printf("[AccessControl] Migration complete: created %d profile(s)", len(intgObjects))
}

// Save writes the config to disk
func (m *Manager) Save() error {
	m.mu.RLock()
	defer m.mu.RUnlock()

	data, err := yaml.Marshal(&m.config)
	if err != nil {
		return fmt.Errorf("failed to marshal config: %w", err)
	}

	header := "# Access Control Configuration (profile-based)\n# Levels: full (default), readonly, hidden\n\n"
	fullData := []byte(header + string(data))

	if err := os.WriteFile(m.configPath, fullData, 0644); err != nil {
		return fmt.Errorf("failed to write access_control.yaml: %w", err)
	}

	return nil
}

// Reset clears all profiles and assignments back to defaults (the built-in profiles, each
// integration with its default one).
func (m *Manager) Reset() {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.config = newEmptyConfig()
	m.ensureBuiltinProfiles()
	m.ensureAssignments()
}

// --- Profile CRUD ---

// ListProfiles returns all profiles (name → profile).
func (m *Manager) ListProfiles() map[string]*Profile {
	m.mu.RLock()
	defer m.mu.RUnlock()

	result := make(map[string]*Profile, len(m.config.Profiles))
	for name, p := range m.config.Profiles {
		// Return copies to avoid external mutation
		cp := &Profile{Default: p.Default}
		if len(p.Objects) > 0 {
			cp.Objects = make(map[string]AccessLevel, len(p.Objects))
			for k, v := range p.Objects {
				cp.Objects[k] = v
			}
		}
		result[name] = cp
	}
	return result
}

// GetProfile returns a profile by name (nil if not found).
func (m *Manager) GetProfile(name string) *Profile {
	m.mu.RLock()
	defer m.mu.RUnlock()

	p, ok := m.config.Profiles[name]
	if !ok {
		return nil
	}
	cp := &Profile{Default: p.Default}
	if len(p.Objects) > 0 {
		cp.Objects = make(map[string]AccessLevel, len(p.Objects))
		for k, v := range p.Objects {
			cp.Objects[k] = v
		}
	}
	return cp
}

// SaveProfile creates or updates a profile. The built-in profiles cannot be modified.
func (m *Manager) SaveProfile(name string, p Profile) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if isBuiltinProfile(name) {
		return fmt.Errorf("cannot modify built-in profile %q", name)
	}
	if name == "" {
		return fmt.Errorf("profile name cannot be empty")
	}

	stored := &Profile{Default: p.Default}
	if len(p.Objects) > 0 {
		stored.Objects = make(map[string]AccessLevel, len(p.Objects))
		for k, v := range p.Objects {
			stored.Objects[k] = v
		}
	}
	m.config.Profiles[name] = stored
	return nil
}

// DeleteProfile deletes a profile. Auto-unassigns from all integrations first.
// Returns error only if it's built-in or not found.
func (m *Manager) DeleteProfile(name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if isBuiltinProfile(name) {
		return fmt.Errorf("cannot delete built-in profile %q", name)
	}

	if _, ok := m.config.Profiles[name]; !ok {
		return fmt.Errorf("profile %q not found", name)
	}

	// Auto-unassign from all integrations
	for intg, profiles := range m.config.Assignments {
		var filtered []string
		for _, pn := range profiles {
			if pn != name {
				filtered = append(filtered, pn)
			}
		}
		if len(filtered) == 0 {
			filtered = []string{DefaultProfile(Integration(intg))}
		}
		m.config.Assignments[intg] = filtered
	}

	delete(m.config.Profiles, name)
	return nil
}

// --- Assignments ---

// GetAssignment returns the profile names assigned to an integration.
func (m *Manager) GetAssignment(integration Integration) []string {
	m.mu.RLock()
	defer m.mu.RUnlock()

	names := m.config.Assignments[string(integration)]
	if names == nil {
		return []string{DefaultProfile(integration)}
	}
	result := make([]string, len(names))
	copy(result, names)
	return result
}

// GetAllAssignments returns all integration → profile assignments.
func (m *Manager) GetAllAssignments() map[string][]string {
	m.mu.RLock()
	defer m.mu.RUnlock()

	result := make(map[string][]string, len(m.config.Assignments))
	for k, v := range m.config.Assignments {
		cp := make([]string, len(v))
		copy(cp, v)
		result[k] = cp
	}
	return result
}

// SetAssignment sets the profiles for an integration.
func (m *Manager) SetAssignment(integration Integration, profiles []string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Validate all profile names exist
	for _, name := range profiles {
		if _, ok := m.config.Profiles[name]; !ok {
			return fmt.Errorf("profile %q does not exist", name)
		}
	}

	cp := make([]string, len(profiles))
	copy(cp, profiles)
	m.config.Assignments[string(integration)] = cp
	return nil
}

// --- Access Resolution ---

// ResolveAccess merges multiple profiles and returns the effective access for a path.
// Most permissive wins across all profiles.
func (m *Manager) ResolveAccess(profileNames []string, path string) AccessLevel {
	m.mu.RLock()
	defer m.mu.RUnlock()

	return m.resolveAccessLocked(profileNames, path)
}

// resolveAccessLocked is the internal resolver (caller must hold at least RLock).
func (m *Manager) resolveAccessLocked(profileNames []string, path string) AccessLevel {
	best := AccessHidden
	bestOrder := accessLevelOrder(AccessHidden)

	for _, name := range profileNames {
		p, ok := m.config.Profiles[name]
		if !ok {
			continue
		}

		var level AccessLevel
		if objLevel, ok := p.Objects[path]; ok {
			level = objLevel
		} else {
			level = p.Default
			if level == "" {
				level = AccessFull
			}
		}

		ord := accessLevelOrder(level)
		if ord > bestOrder {
			best = level
			bestOrder = ord
		}

		// Short-circuit: can't be more permissive than full
		if best == AccessFull {
			return AccessFull
		}
	}

	// If no profiles matched at all, default to hidden (zero profiles = no access)
	if len(profileNames) == 0 {
		return AccessHidden
	}

	return best
}

// GetAccess returns the access level for an object and integration.
// Uses profile-based resolution: integration → assigned profiles → merge → level.
func (m *Manager) GetAccess(integration Integration, objectPath string) AccessLevel {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if m.loadErr != nil {
		return AccessHidden // config unreadable — deny rather than silently unrestrict
	}

	profiles := m.config.Assignments[string(integration)]
	if len(profiles) == 0 {
		return defaultLevel(integration) // unrestricted, except remote
	}

	return m.resolveAccessLocked(profiles, objectPath)
}

// SetAccess is a convenience method for the legacy per-object-per-integration API.
// It finds (or creates) a mutable profile for the integration and sets the object level.
// This keeps the existing web UI working during migration.
func (m *Manager) SetAccess(integration Integration, objectPath string, level AccessLevel) {
	m.mu.Lock()
	defer m.mu.Unlock()

	profileNames := m.config.Assignments[string(integration)]

	// Find a mutable (non-builtin) profile, or create one
	var targetName string
	for _, name := range profileNames {
		if !isBuiltinProfile(name) {
			targetName = name
			break
		}
	}

	if targetName == "" {
		// No mutable profile — create one, starting from the integration's default
		targetName = "custom-" + string(integration)
		m.config.Profiles[targetName] = &Profile{
			Default: defaultLevel(integration),
			Objects: map[string]AccessLevel{},
		}
		// Replace or append to assignments
		m.config.Assignments[string(integration)] = []string{targetName}
		profileNames = []string{targetName}
	}

	p := m.config.Profiles[targetName]
	if p == nil {
		return
	}

	if p.Objects == nil {
		p.Objects = map[string]AccessLevel{}
	}

	if level == p.Default || (level == AccessFull && p.Default == "") {
		// Remove the override if it matches the profile default
		delete(p.Objects, objectPath)
	} else {
		p.Objects[objectPath] = level
	}
}

// IsHidden returns true if the object should be hidden from the integration
func (m *Manager) IsHidden(integration Integration, objectPath string) bool {
	return m.GetAccess(integration, objectPath) == AccessHidden
}

// IsReadonly returns true if the object is readonly for the integration
func (m *Manager) IsReadonly(integration Integration, objectPath string) bool {
	return m.GetAccess(integration, objectPath) == AccessReadonly
}

// CanRead returns true if the object is visible (not hidden) for the integration
func (m *Manager) CanRead(integration Integration, objectPath string) bool {
	return m.GetAccess(integration, objectPath) != AccessHidden
}

// CanControl returns true if the object can receive commands from the integration
func (m *Manager) CanControl(integration Integration, objectPath string) bool {
	return m.GetAccess(integration, objectPath) == AccessFull
}

// GetAllForIntegration returns all access settings for a specific integration as string map
func (m *Manager) GetAllForIntegration(integration Integration) map[string]string {
	m.mu.RLock()
	defer m.mu.RUnlock()

	result := make(map[string]string)
	profiles := m.config.Assignments[string(integration)]
	for _, name := range profiles {
		p, ok := m.config.Profiles[name]
		if !ok {
			continue
		}
		for path, level := range p.Objects {
			// Most permissive wins across profiles
			if existing, ok := result[path]; ok {
				if accessLevelOrder(level) <= accessLevelOrder(AccessLevel(existing)) {
					continue
				}
			}
			result[path] = string(level)
		}
	}
	return result
}

// GetProfilesForSync returns profile data suitable for syncing to Lua.
func (m *Manager) GetProfilesForSync() map[string]map[string]interface{} {
	m.mu.RLock()
	defer m.mu.RUnlock()

	result := make(map[string]map[string]interface{}, len(m.config.Profiles))
	for name, p := range m.config.Profiles {
		pm := map[string]interface{}{
			"default": string(p.Default),
		}
		if len(p.Objects) > 0 {
			objects := make(map[string]string, len(p.Objects))
			for k, v := range p.Objects {
				objects[k] = string(v)
			}
			pm["objects"] = objects
		}
		result[name] = pm
	}
	return result
}

// GetAssignmentsForSync returns assignment data suitable for syncing to Lua.
func (m *Manager) GetAssignmentsForSync() map[string][]string {
	return m.GetAllAssignments()
}
