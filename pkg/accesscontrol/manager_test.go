package accesscontrol

import (
	"os"
	"path/filepath"
	"testing"
)

func TestNewManager(t *testing.T) {
	m := NewManager("/tmp/test")

	if m.configPath != "/tmp/test/access_control.yaml" {
		t.Errorf("expected config path /tmp/test/access_control.yaml, got %s", m.configPath)
	}

	if m.config.Profiles == nil {
		t.Error("expected Profiles map to be initialized")
	}
}

func TestGetAccess_DefaultsToFull(t *testing.T) {
	m := NewManager("/tmp/test")
	// Simulate a loaded state with full-access profile assigned
	m.config.Profiles[BuiltinProfileFullAccess] = &Profile{Default: AccessFull}
	m.config.Assignments["mqtt"] = []string{BuiltinProfileFullAccess}

	level := m.GetAccess(IntegrationMQTT, "CLU.DOU1")

	if level != AccessFull {
		t.Errorf("expected AccessFull, got %s", level)
	}
}

func TestSetAccess_SetsLevel(t *testing.T) {
	m := newTestManager()

	m.SetAccess(IntegrationMQTT, "CLU.DOU1", AccessReadonly)

	if m.GetAccess(IntegrationMQTT, "CLU.DOU1") != AccessReadonly {
		t.Error("expected AccessReadonly after SetAccess")
	}

	// HomeKit should still be full (default)
	if m.GetAccess(IntegrationHomeKit, "CLU.DOU1") != AccessFull {
		t.Error("expected AccessFull for HomeKit (not set)")
	}
}

func TestSetAccess_RemovesDefaultLevel(t *testing.T) {
	m := newTestManager()

	// Set to hidden first
	m.SetAccess(IntegrationMQTT, "CLU.DOU1", AccessHidden)

	// Verify it was set
	if m.GetAccess(IntegrationMQTT, "CLU.DOU1") != AccessHidden {
		t.Fatal("expected hidden after set")
	}

	// Set back to full - should remove override since profile default is full
	m.SetAccess(IntegrationMQTT, "CLU.DOU1", AccessFull)

	if m.GetAccess(IntegrationMQTT, "CLU.DOU1") != AccessFull {
		t.Error("expected AccessFull after resetting")
	}
}

func TestSetAccess_KeepsOtherIntegrations(t *testing.T) {
	m := newTestManager()

	m.SetAccess(IntegrationMQTT, "CLU.DOU1", AccessHidden)
	m.SetAccess(IntegrationHomeKit, "CLU.DOU1", AccessReadonly)

	// Set MQTT back to full
	m.SetAccess(IntegrationMQTT, "CLU.DOU1", AccessFull)

	// MQTT should be full now
	if m.GetAccess(IntegrationMQTT, "CLU.DOU1") != AccessFull {
		t.Error("expected MQTT to be full after reset")
	}

	// HomeKit should still be readonly
	if m.GetAccess(IntegrationHomeKit, "CLU.DOU1") != AccessReadonly {
		t.Error("expected HomeKit to remain readonly")
	}
}

func TestIsHidden(t *testing.T) {
	m := newTestManager()

	m.SetAccess(IntegrationMQTT, "CLU.DOU1", AccessHidden)

	if !m.IsHidden(IntegrationMQTT, "CLU.DOU1") {
		t.Error("expected IsHidden to return true")
	}

	if m.IsHidden(IntegrationHomeKit, "CLU.DOU1") {
		t.Error("expected IsHidden to return false for HomeKit")
	}
}

func TestIsReadonly(t *testing.T) {
	m := newTestManager()

	m.SetAccess(IntegrationMQTT, "CLU.DOU1", AccessReadonly)

	if !m.IsReadonly(IntegrationMQTT, "CLU.DOU1") {
		t.Error("expected IsReadonly to return true")
	}

	if m.IsReadonly(IntegrationHomeKit, "CLU.DOU1") {
		t.Error("expected IsReadonly to return false for HomeKit")
	}
}

func TestCanControl(t *testing.T) {
	m := newTestManager()

	// Default - can control
	if !m.CanControl(IntegrationMQTT, "CLU.DOU1") {
		t.Error("expected CanControl to return true by default")
	}

	// Set to readonly - cannot control
	m.SetAccess(IntegrationMQTT, "CLU.DOU1", AccessReadonly)
	if m.CanControl(IntegrationMQTT, "CLU.DOU1") {
		t.Error("expected CanControl to return false for readonly")
	}

	// Set to hidden - cannot control
	m.SetAccess(IntegrationMQTT, "CLU.DOU1", AccessHidden)
	if m.CanControl(IntegrationMQTT, "CLU.DOU1") {
		t.Error("expected CanControl to return false for hidden")
	}
}

func TestGetAllForIntegration(t *testing.T) {
	m := newTestManager()

	m.SetAccess(IntegrationMQTT, "CLU.DOU1", AccessHidden)
	m.SetAccess(IntegrationMQTT, "CLU.DOU2", AccessReadonly)
	m.SetAccess(IntegrationHomeKit, "CLU.DOU1", AccessReadonly)

	result := m.GetAllForIntegration(IntegrationMQTT)

	if len(result) != 2 {
		t.Errorf("expected 2 entries for MQTT, got %d", len(result))
	}

	if result["CLU.DOU1"] != "hidden" {
		t.Errorf("expected CLU.DOU1 to be hidden, got %s", result["CLU.DOU1"])
	}

	if result["CLU.DOU2"] != "readonly" {
		t.Errorf("expected CLU.DOU2 to be readonly, got %s", result["CLU.DOU2"])
	}
}

func TestLoadSave(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "accesscontrol_test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	m := NewManager(tmpDir)
	// Load creates defaults
	if err := m.Load(); err != nil {
		t.Fatalf("failed initial load: %v", err)
	}

	// Set some values
	m.SetAccess(IntegrationMQTT, "CLU.DOU1", AccessHidden)
	m.SetAccess(IntegrationHomeKit, "CLU.DOU1", AccessReadonly)
	m.SetAccess(IntegrationMQTT, "CLU.DOU2", AccessReadonly)

	// Save
	if err := m.Save(); err != nil {
		t.Fatalf("failed to save: %v", err)
	}

	// Check file exists
	configPath := filepath.Join(tmpDir, "access_control.yaml")
	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		t.Fatal("config file was not created")
	}

	// Create new manager and load
	m2 := NewManager(tmpDir)
	if err := m2.Load(); err != nil {
		t.Fatalf("failed to load: %v", err)
	}

	// Verify values
	if m2.GetAccess(IntegrationMQTT, "CLU.DOU1") != AccessHidden {
		t.Error("expected CLU.DOU1 MQTT to be hidden after load")
	}

	if m2.GetAccess(IntegrationHomeKit, "CLU.DOU1") != AccessReadonly {
		t.Error("expected CLU.DOU1 HomeKit to be readonly after load")
	}

	if m2.GetAccess(IntegrationMQTT, "CLU.DOU2") != AccessReadonly {
		t.Error("expected CLU.DOU2 MQTT to be readonly after load")
	}
}

func TestLoad_MissingFile(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "accesscontrol_test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	m := NewManager(tmpDir)

	if err := m.Load(); err != nil {
		t.Errorf("expected no error on missing file, got: %v", err)
	}

	// Should have built-in profile
	if _, ok := m.config.Profiles[BuiltinProfileFullAccess]; !ok {
		t.Error("expected full-access built-in profile")
	}
}

func TestAllIntegrations(t *testing.T) {
	integrations := AllIntegrations()

	if len(integrations) != 3 {
		t.Errorf("expected 3 integrations, got %d", len(integrations))
	}

	hasMQTT := false
	hasHomeKit := false
	hasMCP := false
	for _, i := range integrations {
		switch i {
		case IntegrationMQTT:
			hasMQTT = true
		case IntegrationHomeKit:
			hasHomeKit = true
		case IntegrationMCP:
			hasMCP = true
		}
	}

	if !hasMQTT {
		t.Error("expected MQTT integration")
	}
	if !hasHomeKit {
		t.Error("expected HomeKit integration")
	}
	if !hasMCP {
		t.Error("expected MCP integration")
	}
}

func TestConcurrentAccess(t *testing.T) {
	m := newTestManager()

	done := make(chan bool)

	go func() {
		for i := 0; i < 100; i++ {
			m.SetAccess(IntegrationMQTT, "CLU.DOU1", AccessHidden)
			m.SetAccess(IntegrationMQTT, "CLU.DOU1", AccessFull)
		}
		done <- true
	}()

	go func() {
		for i := 0; i < 100; i++ {
			_ = m.GetAccess(IntegrationMQTT, "CLU.DOU1")
			_ = m.IsHidden(IntegrationMQTT, "CLU.DOU1")
			_ = m.GetAllForIntegration(IntegrationMQTT)
		}
		done <- true
	}()

	<-done
	<-done
}

// --- Profile CRUD Tests ---

func TestProfileCRUD(t *testing.T) {
	m := newTestManager()

	// Create
	err := m.SaveProfile("sensors", Profile{
		Default: AccessHidden,
		Objects: map[string]AccessLevel{
			"CLU.TEMP": AccessReadonly,
			"CLU.HUM":  AccessReadonly,
		},
	})
	if err != nil {
		t.Fatalf("SaveProfile failed: %v", err)
	}

	// Read
	p := m.GetProfile("sensors")
	if p == nil {
		t.Fatal("expected profile to exist")
	}
	if p.Default != AccessHidden {
		t.Errorf("expected default=hidden, got %s", p.Default)
	}
	if len(p.Objects) != 2 {
		t.Errorf("expected 2 objects, got %d", len(p.Objects))
	}

	// List
	all := m.ListProfiles()
	if _, ok := all["sensors"]; !ok {
		t.Error("expected sensors in ListProfiles")
	}
	if _, ok := all[BuiltinProfileFullAccess]; !ok {
		t.Error("expected full-access in ListProfiles")
	}

	// Delete (should fail — need to unassign first if assigned)
	// Not assigned, so delete should succeed
	err = m.DeleteProfile("sensors")
	if err != nil {
		t.Fatalf("DeleteProfile failed: %v", err)
	}

	if m.GetProfile("sensors") != nil {
		t.Error("expected profile to be deleted")
	}
}

func TestCannotModifyBuiltinProfile(t *testing.T) {
	m := newTestManager()

	err := m.SaveProfile(BuiltinProfileFullAccess, Profile{Default: AccessHidden})
	if err == nil {
		t.Error("expected error modifying built-in profile")
	}

	err = m.DeleteProfile(BuiltinProfileFullAccess)
	if err == nil {
		t.Error("expected error deleting built-in profile")
	}
}

func TestDeleteAssignedProfile_AutoUnassigns(t *testing.T) {
	m := newTestManager()

	_ = m.SaveProfile("lights", Profile{Default: AccessHidden})
	_ = m.SetAssignment(IntegrationMQTT, []string{"lights"})

	err := m.DeleteProfile("lights")
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}

	// Profile should be gone
	if m.GetProfile("lights") != nil {
		t.Error("expected profile to be deleted")
	}

	// MQTT should have fallen back to full-access
	names := m.GetAssignment(IntegrationMQTT)
	if len(names) != 1 || names[0] != BuiltinProfileFullAccess {
		t.Errorf("expected fallback to full-access, got %v", names)
	}
}

func TestDeleteProfile_KeepsOtherAssignments(t *testing.T) {
	m := newTestManager()

	_ = m.SaveProfile("sensors", Profile{Default: AccessHidden})
	_ = m.SaveProfile("lights", Profile{Default: AccessHidden})
	_ = m.SetAssignment(IntegrationMQTT, []string{"sensors", "lights"})

	err := m.DeleteProfile("sensors")
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}

	// MQTT should still have "lights"
	names := m.GetAssignment(IntegrationMQTT)
	if len(names) != 1 || names[0] != "lights" {
		t.Errorf("expected [lights], got %v", names)
	}
}

// --- ResolveAccess Tests ---

func TestResolveAccess_SingleProfile(t *testing.T) {
	m := newTestManager()

	_ = m.SaveProfile("sensors", Profile{
		Default: AccessHidden,
		Objects: map[string]AccessLevel{
			"CLU.TEMP": AccessReadonly,
		},
	})

	level := m.ResolveAccess([]string{"sensors"}, "CLU.TEMP")
	if level != AccessReadonly {
		t.Errorf("expected readonly, got %s", level)
	}

	level = m.ResolveAccess([]string{"sensors"}, "CLU.DOU1")
	if level != AccessHidden {
		t.Errorf("expected hidden (profile default), got %s", level)
	}
}

func TestResolveAccess_MultiProfile_MostPermissiveWins(t *testing.T) {
	m := newTestManager()

	_ = m.SaveProfile("sensors", Profile{
		Default: AccessHidden,
		Objects: map[string]AccessLevel{
			"CLU.TEMP": AccessReadonly,
			"CLU.HUM":  AccessReadonly,
		},
	})

	_ = m.SaveProfile("lights", Profile{
		Default: AccessHidden,
		Objects: map[string]AccessLevel{
			"CLU.LED1": AccessFull,
			"CLU.TEMP": AccessFull, // more permissive than sensors
		},
	})

	// CLU.TEMP: sensors says readonly, lights says full → full wins
	level := m.ResolveAccess([]string{"sensors", "lights"}, "CLU.TEMP")
	if level != AccessFull {
		t.Errorf("expected full (most permissive), got %s", level)
	}

	// CLU.HUM: only sensors has it → readonly
	level = m.ResolveAccess([]string{"sensors", "lights"}, "CLU.HUM")
	if level != AccessReadonly {
		t.Errorf("expected readonly, got %s", level)
	}

	// CLU.LED1: only lights has it → full
	level = m.ResolveAccess([]string{"sensors", "lights"}, "CLU.LED1")
	if level != AccessFull {
		t.Errorf("expected full, got %s", level)
	}

	// CLU.UNKNOWN: both default to hidden → hidden
	level = m.ResolveAccess([]string{"sensors", "lights"}, "CLU.UNKNOWN")
	if level != AccessHidden {
		t.Errorf("expected hidden, got %s", level)
	}
}

func TestResolveAccess_EmptyProfiles(t *testing.T) {
	m := newTestManager()

	level := m.ResolveAccess([]string{}, "CLU.DOU1")
	if level != AccessHidden {
		t.Errorf("expected hidden for empty profiles, got %s", level)
	}
}

func TestResolveAccess_NonexistentProfile(t *testing.T) {
	m := newTestManager()

	level := m.ResolveAccess([]string{"does-not-exist"}, "CLU.DOU1")
	if level != AccessHidden {
		t.Errorf("expected hidden for nonexistent profile, got %s", level)
	}
}

// --- Assignment Tests ---

func TestAssignments(t *testing.T) {
	m := newTestManager()

	_ = m.SaveProfile("sensors", Profile{Default: AccessHidden})
	_ = m.SaveProfile("lights", Profile{Default: AccessHidden})

	err := m.SetAssignment(IntegrationMQTT, []string{"sensors", "lights"})
	if err != nil {
		t.Fatalf("SetAssignment failed: %v", err)
	}

	names := m.GetAssignment(IntegrationMQTT)
	if len(names) != 2 {
		t.Errorf("expected 2 profiles, got %d", len(names))
	}

	all := m.GetAllAssignments()
	if _, ok := all["mqtt"]; !ok {
		t.Error("expected mqtt in assignments")
	}
}

func TestSetAssignment_InvalidProfile(t *testing.T) {
	m := newTestManager()

	err := m.SetAssignment(IntegrationMQTT, []string{"nonexistent"})
	if err == nil {
		t.Error("expected error for nonexistent profile")
	}
}

// --- Migration Tests ---

func TestMigrateLegacyFormat(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "accesscontrol_test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Write legacy format
	legacyYAML := `objects:
  CLU.DOU1:
    mqtt: hidden
    homekit: readonly
  CLU.DOU2:
    mqtt: readonly
`
	configPath := filepath.Join(tmpDir, "access_control.yaml")
	if err := os.WriteFile(configPath, []byte(legacyYAML), 0644); err != nil {
		t.Fatalf("failed to write legacy config: %v", err)
	}

	m := NewManager(tmpDir)
	if err := m.Load(); err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	// Should have migrated profiles
	if len(m.config.Profiles) == 0 {
		t.Fatal("expected profiles after migration")
	}

	// Legacy objects should be gone
	if len(m.config.Objects) > 0 {
		t.Error("expected legacy objects to be removed after migration")
	}

	// Access should still work the same way
	if m.GetAccess(IntegrationMQTT, "CLU.DOU1") != AccessHidden {
		t.Errorf("expected CLU.DOU1 MQTT to be hidden, got %s", m.GetAccess(IntegrationMQTT, "CLU.DOU1"))
	}
	if m.GetAccess(IntegrationHomeKit, "CLU.DOU1") != AccessReadonly {
		t.Errorf("expected CLU.DOU1 HomeKit to be readonly, got %s", m.GetAccess(IntegrationHomeKit, "CLU.DOU1"))
	}
	if m.GetAccess(IntegrationMQTT, "CLU.DOU2") != AccessReadonly {
		t.Errorf("expected CLU.DOU2 MQTT to be readonly, got %s", m.GetAccess(IntegrationMQTT, "CLU.DOU2"))
	}

	// Verify migration persists
	if err := m.Save(); err != nil {
		t.Fatalf("Save after migration failed: %v", err)
	}
	m2 := NewManager(tmpDir)
	if err := m2.Load(); err != nil {
		t.Fatalf("Load after migration save failed: %v", err)
	}
	if m2.GetAccess(IntegrationMQTT, "CLU.DOU1") != AccessHidden {
		t.Error("migration did not persist correctly")
	}
}

func TestGetAccessViaAssignment(t *testing.T) {
	m := newTestManager()

	_ = m.SaveProfile("limited", Profile{
		Default: AccessHidden,
		Objects: map[string]AccessLevel{
			"CLU.LED1": AccessFull,
			"CLU.TEMP": AccessReadonly,
		},
	})
	_ = m.SetAssignment(IntegrationMCP, []string{"limited"})

	if m.GetAccess(IntegrationMCP, "CLU.LED1") != AccessFull {
		t.Error("expected full for CLU.LED1 via MCP")
	}
	if m.GetAccess(IntegrationMCP, "CLU.TEMP") != AccessReadonly {
		t.Error("expected readonly for CLU.TEMP via MCP")
	}
	if m.GetAccess(IntegrationMCP, "CLU.OTHER") != AccessHidden {
		t.Error("expected hidden for unlisted object via MCP")
	}

	// CanRead / CanControl should work
	if !m.CanRead(IntegrationMCP, "CLU.LED1") {
		t.Error("expected CanRead=true for full")
	}
	if !m.CanControl(IntegrationMCP, "CLU.LED1") {
		t.Error("expected CanControl=true for full")
	}
	if !m.CanRead(IntegrationMCP, "CLU.TEMP") {
		t.Error("expected CanRead=true for readonly")
	}
	if m.CanControl(IntegrationMCP, "CLU.TEMP") {
		t.Error("expected CanControl=false for readonly")
	}
	if m.CanRead(IntegrationMCP, "CLU.OTHER") {
		t.Error("expected CanRead=false for hidden")
	}
}

// --- Reset Tests ---

func TestReset(t *testing.T) {
	m := newTestManager()

	_ = m.SaveProfile("sensors", Profile{Default: AccessHidden})
	_ = m.SaveProfile("lights", Profile{Default: AccessHidden})
	_ = m.SetAssignment(IntegrationMQTT, []string{"sensors", "lights"})

	m.Reset()

	// Only full-access should remain
	all := m.ListProfiles()
	if len(all) != 1 {
		t.Errorf("expected 1 profile after reset, got %d", len(all))
	}
	if _, ok := all[BuiltinProfileFullAccess]; !ok {
		t.Error("expected full-access profile after reset")
	}

	// All integrations should have full-access
	for _, intg := range AllIntegrations() {
		names := m.GetAssignment(intg)
		if len(names) != 1 || names[0] != BuiltinProfileFullAccess {
			t.Errorf("expected [full-access] for %s, got %v", intg, names)
		}
	}
}

// --- Helper ---

// newTestManager creates a manager with built-in profiles and per-integration assignments
// suitable for testing the legacy SetAccess API.
func newTestManager() *Manager {
	m := NewManager("/tmp/test")
	m.config.Profiles[BuiltinProfileFullAccess] = &Profile{Default: AccessFull}

	// Create per-integration custom profiles so SetAccess works
	for _, intg := range AllIntegrations() {
		name := "custom-" + string(intg)
		m.config.Profiles[name] = &Profile{
			Default: AccessFull,
			Objects: map[string]AccessLevel{},
		}
		m.config.Assignments[string(intg)] = []string{name}
	}

	return m
}

// A config we cannot read must deny, not silently unrestrict. Before this, a
// corrupted YAML logged one warning and every readonly/hidden marking vanished.
func TestLoad_MalformedConfigDeniesAccess(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "access_control.yaml")
	if err := os.WriteFile(path, []byte("profiles: [this is: not valid: yaml\n"), 0644); err != nil {
		t.Fatal(err)
	}

	m := NewManager(dir)
	if err := m.Load(); err == nil {
		t.Fatal("Load() accepted malformed YAML")
	}

	for _, intg := range AllIntegrations() {
		if got := m.GetAccess(intg, "CLU.BOILER"); got != AccessHidden {
			t.Errorf("%s: access = %q after a failed load, want %q", intg, got, AccessHidden)
		}
		if m.CanControl(intg, "CLU.BOILER") {
			t.Errorf("%s: CanControl allowed after a failed load", intg)
		}
	}
}

// A missing file is a fresh install, not a failure — it must stay permissive.
func TestLoad_MissingFileStaysPermissive(t *testing.T) {
	m := NewManager(t.TempDir())
	if err := m.Load(); err != nil {
		t.Fatalf("Load() on missing file returned %v", err)
	}
	if got := m.GetAccess(IntegrationMQTT, "CLU.LAMP"); got != AccessFull {
		t.Errorf("fresh install access = %q, want %q", got, AccessFull)
	}
}

// A successful load after a failed one must clear the deny-all state.
func TestLoad_RecoversAfterFixingConfig(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "access_control.yaml")

	if err := os.WriteFile(path, []byte("profiles: [broken\n"), 0644); err != nil {
		t.Fatal(err)
	}
	m := NewManager(dir)
	_ = m.Load()
	if m.CanControl(IntegrationMQTT, "CLU.LAMP") {
		t.Fatal("setup: expected deny after the failed load")
	}

	if err := os.WriteFile(path, []byte("profiles:\n  full-access:\n    default: full\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := m.Load(); err != nil {
		t.Fatalf("reload of a valid config failed: %v", err)
	}
	if !m.CanControl(IntegrationMQTT, "CLU.LAMP") {
		t.Error("still denying after the config was fixed")
	}
}
