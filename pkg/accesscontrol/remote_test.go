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

// A no-access in the file that grants anything does not open remote: it is renamed, remote keeps the built-in.
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

// A user's own no-access from before #39 is kept under a new name, not overwritten.
func TestRemote_UserNoAccessIsRenamed(t *testing.T) {
	dir := writeConfig(t, `profiles:
  no-access:
    default: full
    objects:
      CLU.BOILER: hidden
assignments:
  mqtt: [no-access]
  remote: [no-access]
`)
	m := loadDir(t, dir)

	if got := m.RenamedProfiles(); !reflect.DeepEqual(got, map[string]string{"no-access": "no-access-user"}) {
		t.Errorf("RenamedProfiles = %v", got)
	}
	if got := m.GetAssignment(IntegrationMQTT); !reflect.DeepEqual(got, []string{"no-access-user"}) {
		t.Errorf("mqtt assignment = %v, want [no-access-user]", got)
	}
	p := m.GetProfile("no-access-user")
	if p == nil || p.Default != AccessFull || p.Objects["CLU.BOILER"] != AccessHidden {
		t.Errorf("renamed profile = %+v, want the user's content", p)
	}
	if m.GetAccess(IntegrationMQTT, "CLU.LAMP") != AccessFull || m.GetAccess(IntegrationMQTT, "CLU.BOILER") != AccessHidden {
		t.Error("mqtt access changed by the rename")
	}
	// remote keeps the built-in name: it must not open through the user's profile
	if got := m.GetAssignment(IntegrationRemote); !reflect.DeepEqual(got, []string{BuiltinProfileNoAccess}) {
		t.Errorf("remote assignment = %v, want [no-access]", got)
	}
	if m.CanRead(IntegrationRemote, "CLU.LAMP") {
		t.Error("remote opened through the user's no-access")
	}

	// saved once: the next load has nothing to rename and the same rules
	m2 := loadDir(t, dir)
	if got := m2.RenamedProfiles(); len(got) != 0 {
		t.Errorf("second load renamed again: %v", got)
	}
	if m2.GetAccess(IntegrationMQTT, "CLU.BOILER") != AccessHidden || m2.GetAccess(IntegrationMQTT, "CLU.LAMP") != AccessFull {
		t.Error("rename not saved")
	}
}

func TestRemote_UserNoAccessRenameAvoidsTakenNames(t *testing.T) {
	dir := writeConfig(t, `profiles:
  no-access:
    default: readonly
  no-access-user:
    default: hidden
    objects:
      CLU.LAMP: full
assignments:
  homekit: [no-access, no-access-user]
`)
	m := loadDir(t, dir)
	if got := m.RenamedProfiles(); !reflect.DeepEqual(got, map[string]string{"no-access": "no-access-user-2"}) {
		t.Errorf("RenamedProfiles = %v", got)
	}
	if got := m.GetAssignment(IntegrationHomeKit); !reflect.DeepEqual(got, []string{"no-access-user-2", "no-access-user"}) {
		t.Errorf("homekit assignment = %v", got)
	}
	if p := m.GetProfile("no-access-user"); p == nil || p.Objects["CLU.LAMP"] != AccessFull {
		t.Errorf("existing no-access-user changed: %+v", p)
	}
	if m.GetAccess(IntegrationHomeKit, "CLU.OTHER") != AccessReadonly {
		t.Error("homekit access changed by the rename")
	}
}

func TestRemote_BuiltinShapedNoAccessNotRenamed(t *testing.T) {
	dir := writeConfig(t, `profiles:
  no-access:
    default: hidden
assignments:
  mqtt: [no-access]
`)
	before, err := os.ReadFile(filepath.Join(dir, "access_control.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	m := loadDir(t, dir)
	if got := m.RenamedProfiles(); len(got) != 0 {
		t.Errorf("RenamedProfiles = %v, want none", got)
	}
	if got := m.GetAssignment(IntegrationMQTT); !reflect.DeepEqual(got, []string{BuiltinProfileNoAccess}) {
		t.Errorf("mqtt assignment = %v", got)
	}
	after, _ := os.ReadFile(filepath.Join(dir, "access_control.yaml"))
	if string(before) != string(after) {
		t.Error("file written on a load with nothing to rename")
	}
}
