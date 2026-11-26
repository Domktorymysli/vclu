package plugins

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Domktorymysli/vclu/pkg/config"
)

func writePluginFiles(t *testing.T, l *Loader, id, version, manifest string) {
	t.Helper()
	dir := filepath.Join(l.pluginsDir, safePluginDir(id), version)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "plugin.json"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "init.lua"), []byte("return {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	l.writeLockEntry(id, LockEntry{Version: version, Path: safePluginDir(id) + "/" + version})
}

func recordedAPI(t *testing.T, l *Loader, id string) int {
	t.Helper()
	inst, ok := l.installedEntry(id)
	if !ok {
		t.Fatalf("%s not installed", id)
	}
	return inst.API
}

// An entry from before the API was recorded gets it on load; files replaced by hand with a
// version on a lower API do not load.
func TestRecordedAPIGuardsTheFiles(t *testing.T) {
	l := permissionsLoader(t, nil, []config.InstalledPlugin{{ID: "@jan/cloud", Enabled: true}})
	l.SetLuaExecutor(&noLua{})
	writePluginFiles(t, l, "@jan/cloud", "1.0.0", `{"id":"cloud","version":"1.0.0","api":2,"permissions":{}}`)
	if got := l.APIFloor("@jan/cloud"); got != 2 {
		t.Errorf("floor before recording %d, want 2 from the files", got)
	}
	_ = l.LoadPlugin("@jan/cloud", nil)
	if got := recordedAPI(t, l, "@jan/cloud"); got != 2 {
		t.Fatalf("recorded api %d, want 2", got)
	}
	_ = l.UnloadPlugin("@jan/cloud")

	writePluginFiles(t, l, "@jan/cloud", "1.1.0", `{"id":"cloud","version":"1.1.0"}`)
	err := l.LoadPlugin("@jan/cloud", nil)
	if err == nil || !strings.Contains(err.Error(), "lowers the plugin API from 2 to 1") {
		t.Errorf("load of API 1 files over api 2: %v", err)
	}
	if got := recordedAPI(t, l, "@jan/cloud"); got != 2 {
		t.Errorf("recorded api %d after refusing, want 2", got)
	}
}

// A plugin not installed has no floor: a fresh install takes any API.
func TestAPIFloorOnlyForInstalled(t *testing.T) {
	l := permissionsLoader(t, nil, nil)
	writePluginFiles(t, l, "@jan/cloud", "1.0.0", `{"id":"cloud","version":"1.0.0","api":2}`)
	if got := l.APIFloor("@jan/cloud"); got != 0 {
		t.Errorf("floor %d for a plugin not installed, want 0", got)
	}
}
