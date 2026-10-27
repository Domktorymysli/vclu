package accesscontrol

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// Remote access (#39) reaches the house from outside: closed until the owner opens it.
// The other integrations keep full-access as their default.

func writeConfig(t *testing.T, yaml string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "access_control.yaml"), []byte(yaml), 0644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func loadDir(t *testing.T, dir string) *Manager {
	t.Helper()
	m := NewManager(dir)
	if err := m.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}
	return m
}

func TestRemote_FreshInstallIsHidden(t *testing.T) {
	m := loadDir(t, t.TempDir())

	if got := m.GetAssignment(IntegrationRemote); !reflect.DeepEqual(got, []string{BuiltinProfileNoAccess}) {
		t.Errorf("remote assignment = %v, want [%s]", got, BuiltinProfileNoAccess)
	}
	if got := m.GetAccess(IntegrationRemote, "CLU.LAMP"); got != AccessHidden {
		t.Errorf("remote access on a fresh install = %q, want hidden", got)
	}
	if m.CanRead(IntegrationRemote, "CLU.LAMP") || m.CanControl(IntegrationRemote, "CLU.LAMP") {
		t.Error("remote can read or control on a fresh install")
	}
	for _, intg := range []Integration{IntegrationMQTT, IntegrationHomeKit, IntegrationMCP} {
		if got := m.GetAssignment(intg); !reflect.DeepEqual(got, []string{BuiltinProfileFullAccess}) {
			t.Errorf("%s assignment = %v, want [full-access]", intg, got)
		}
		if got := m.GetAccess(intg, "CLU.LAMP"); got != AccessFull {
			t.Errorf("%s access on a fresh install = %q, want full", intg, got)
		}
	}
}

func TestRemote_ExistingConfigWithoutRemoteGetsHidden(t *testing.T) {
	dir := writeConfig(t, `profiles:
  full-access:
    default: full
  custom-mqtt:
    default: full
    objects:
      CLU.BOILER: hidden
  sensors:
    default: readonly
assignments:
  mqtt: [custom-mqtt]
  homekit: [full-access]
  mcp: [sensors]
`)
	m := loadDir(t, dir)

	if got := m.GetAssignment(IntegrationRemote); !reflect.DeepEqual(got, []string{BuiltinProfileNoAccess}) {
		t.Errorf("remote assignment = %v, want [%s]", got, BuiltinProfileNoAccess)
	}
	if m.CanRead(IntegrationRemote, "CLU.LAMP") {
		t.Error("remote sees an object after loading a config without a remote assignment")
	}

	want := map[Integration][]string{
		IntegrationMQTT:    {"custom-mqtt"},
		IntegrationHomeKit: {"full-access"},
		IntegrationMCP:     {"sensors"},
	}
	for intg, names := range want {
		if got := m.GetAssignment(intg); !reflect.DeepEqual(got, names) {
			t.Errorf("%s assignment changed: %v, want %v", intg, got, names)
		}
	}
	if m.GetAccess(IntegrationMQTT, "CLU.BOILER") != AccessHidden || m.GetAccess(IntegrationMQTT, "CLU.LAMP") != AccessFull {
		t.Error("mqtt rules changed")
	}
	if m.GetAccess(IntegrationMCP, "CLU.LAMP") != AccessReadonly {
		t.Error("mcp rules changed")
	}
}

// An integration missing from an older file still gets full-access, as before.
func TestRemote_OtherMissingIntegrationsStillFull(t *testing.T) {
	dir := writeConfig(t, "assignments:\n  mqtt: [full-access]\n")
	m := loadDir(t, dir)
	for _, intg := range []Integration{IntegrationHomeKit, IntegrationMCP} {
		if got := m.GetAssignment(intg); !reflect.DeepEqual(got, []string{BuiltinProfileFullAccess}) {
			t.Errorf("%s assignment = %v, want [full-access]", intg, got)
		}
	}
}

func TestRemote_ExistingRemoteAssignmentKept(t *testing.T) {
	dir := writeConfig(t, `profiles:
  outside:
    default: hidden
    objects:
      CLU.LAMP: full
      CLU.TEMP: readonly
assignments:
  remote: [outside]
`)
	m := loadDir(t, dir)
	if got := m.GetAssignment(IntegrationRemote); !reflect.DeepEqual(got, []string{"outside"}) {
		t.Fatalf("remote assignment = %v, want [outside]", got)
	}
	for path, want := range map[string]AccessLevel{"CLU.LAMP": AccessFull, "CLU.TEMP": AccessReadonly, "CLU.BOILER": AccessHidden} {
		if got := m.GetAccess(IntegrationRemote, path); got != want {
			t.Errorf("remote %s = %q, want %q", path, got, want)
		}
	}
}

func TestRemote_LegacyMigrationGivesHidden(t *testing.T) {
	dir := writeConfig(t, "objects:\n  CLU.DOU1:\n    mqtt: hidden\n")
	m := loadDir(t, dir)
	if got := m.GetAssignment(IntegrationRemote); !reflect.DeepEqual(got, []string{BuiltinProfileNoAccess}) {
		t.Errorf("remote assignment after migration = %v, want [%s]", got, BuiltinProfileNoAccess)
	}
	if got := m.GetAssignment(IntegrationHomeKit); !reflect.DeepEqual(got, []string{BuiltinProfileFullAccess}) {
		t.Errorf("homekit assignment after migration = %v", got)
	}
}

func TestRemote_SaveAndReloadStaysHidden(t *testing.T) {
	dir := t.TempDir()
	m := loadDir(t, dir)
	if err := m.Save(); err != nil {
		t.Fatal(err)
	}
	m2 := loadDir(t, dir)
	if m2.CanRead(IntegrationRemote, "CLU.LAMP") {
		t.Error("remote sees objects after save and reload")
	}
	if !m2.CanControl(IntegrationMQTT, "CLU.LAMP") {
		t.Error("mqtt lost full access after save and reload")
	}
}

func TestRemote_ResetGivesHidden(t *testing.T) {
	m := loadDir(t, t.TempDir())
	_ = m.SaveProfile("open", Profile{Default: AccessFull})
	_ = m.SetAssignment(IntegrationRemote, []string{"open"})

	m.Reset()

	if got := m.GetAssignment(IntegrationRemote); !reflect.DeepEqual(got, []string{BuiltinProfileNoAccess}) {
		t.Errorf("remote after reset = %v, want [%s]", got, BuiltinProfileNoAccess)
	}
	if got := m.GetAssignment(IntegrationMQTT); !reflect.DeepEqual(got, []string{BuiltinProfileFullAccess}) {
		t.Errorf("mqtt after reset = %v", got)
	}
	if m.GetProfile(BuiltinProfileNoAccess) == nil {
		t.Error("no-access profile missing after reset")
	}
}

func TestRemote_DeletingAssignedProfileFallsBackToHidden(t *testing.T) {
	m := loadDir(t, t.TempDir())
	_ = m.SaveProfile("open", Profile{Default: AccessFull})
	_ = m.SetAssignment(IntegrationRemote, []string{"open"})
	_ = m.SetAssignment(IntegrationMQTT, []string{"open"})

	if err := m.DeleteProfile("open"); err != nil {
		t.Fatal(err)
	}
	if got := m.GetAssignment(IntegrationRemote); !reflect.DeepEqual(got, []string{BuiltinProfileNoAccess}) {
		t.Errorf("remote after delete = %v, want [%s]", got, BuiltinProfileNoAccess)
	}
	if got := m.GetAssignment(IntegrationMQTT); !reflect.DeepEqual(got, []string{BuiltinProfileFullAccess}) {
		t.Errorf("mqtt after delete = %v, want [full-access]", got)
	}
}

func TestRemote_EmptyAssignmentIsHidden(t *testing.T) {
	m := loadDir(t, t.TempDir())
	if err := m.SetAssignment(IntegrationRemote, nil); err != nil {
		t.Fatal(err)
	}
	if got := m.GetAccess(IntegrationRemote, "CLU.LAMP"); got != AccessHidden {
		t.Errorf("remote with no profiles = %q, want hidden", got)
	}
}

func TestRemote_NoAccessIsBuiltin(t *testing.T) {
	m := loadDir(t, t.TempDir())
	if err := m.SaveProfile(BuiltinProfileNoAccess, Profile{Default: AccessFull}); err == nil {
		t.Error("no-access profile was modified")
	}
	if err := m.DeleteProfile(BuiltinProfileNoAccess); err == nil {
		t.Error("no-access profile was deleted")
	}
	if p := m.GetProfile(BuiltinProfileNoAccess); p == nil || p.Default != AccessHidden || len(p.Objects) > 0 {
		t.Errorf("no-access profile = %+v, want default hidden", p)
	}
}

// A no-access in the file that grants anything is put back: remote must not open by an edit.
func TestRemote_NoAccessFromFileIsEnforced(t *testing.T) {
	dir := writeConfig(t, `profiles:
  no-access:
    default: full
    objects:
      CLU.LAMP: full
`)
	m := loadDir(t, dir)
	if got := m.GetAccess(IntegrationRemote, "CLU.LAMP"); got != AccessHidden {
		t.Errorf("remote via an edited no-access = %q, want hidden", got)
	}
}

// The per-object setting of the Access page opens one object, not everything.
func TestRemote_SetAccessOpensOnlyThatObject(t *testing.T) {
	m := loadDir(t, t.TempDir())

	m.SetAccess(IntegrationRemote, "CLU.LAMP", AccessFull)
	m.SetAccess(IntegrationRemote, "CLU.TEMP", AccessReadonly)

	for path, want := range map[string]AccessLevel{"CLU.LAMP": AccessFull, "CLU.TEMP": AccessReadonly, "CLU.BOILER": AccessHidden} {
		if got := m.GetAccess(IntegrationRemote, path); got != want {
			t.Errorf("remote %s = %q, want %q", path, got, want)
		}
	}

	m.SetAccess(IntegrationRemote, "CLU.LAMP", AccessHidden)
	if got := m.GetAccess(IntegrationRemote, "CLU.LAMP"); got != AccessHidden {
		t.Errorf("remote CLU.LAMP after hiding = %q", got)
	}
	if !m.CanControl(IntegrationMQTT, "CLU.LAMP") {
		t.Error("remote setting changed mqtt")
	}
}

func TestRemote_IsIntegration(t *testing.T) {
	for _, s := range []string{"mqtt", "homekit", "mcp", "remote"} {
		if !IsIntegration(s) {
			t.Errorf("IsIntegration(%q) = false", s)
		}
	}
	for _, s := range []string{"", "http", "Remote"} {
		if IsIntegration(s) {
			t.Errorf("IsIntegration(%q) = true", s)
		}
	}
}
