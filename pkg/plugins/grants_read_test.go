package plugins

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Domktorymysli/vclu/pkg/config"
)

// An unreadable .vclu.json never stands for "installed before grants": the plugin does not
// load, nothing is downloaded over it, and plugins are not loaded at all.
func TestUnreadableConfigLoadsNothing(t *testing.T) {
	narrow := &config.PluginGrants{LAN: &config.LANGrant{Hosts: []string{"192.168.1.5"}}}
	l := permissionsLoader(t, nil, []config.InstalledPlugin{{ID: "@jan/shelly", Enabled: true, API: 2, Grants: narrow}})
	l.SetLuaExecutor(&noLua{})
	writePluginFiles(t, l, "@jan/shelly", "1.0.0", `{"id":"shelly","version":"1.0.0","api":2,"permissions":{"lan":true}}`)

	m := PluginManifest{API: 2, Permissions: &Permissions{LAN: true}}
	if _, limits, _, err := l.effectivePermissions("@jan/shelly", m); err != nil || len(limits.LANHosts) != 1 {
		t.Fatalf("readable config: limits %+v, err %v", limits, err)
	}

	if err := os.WriteFile(filepath.Join(l.serverDir, ".vclu.json"), []byte(`{"plugins": {`), 0o600); err != nil {
		t.Fatal(err)
	}
	if perms, _, _, err := l.effectivePermissions("@jan/shelly", m); err == nil || perms.LAN {
		t.Errorf("unreadable config: perms %+v, err %v; want an error and nothing allowed", perms, err)
	}
	if err := l.LoadPlugin("@jan/shelly", nil); err == nil || !strings.Contains(err.Error(), ".vclu.json") {
		t.Errorf("load with an unreadable config: %v", err)
	}
	if _, err := l.APIFloor("@jan/shelly"); err == nil {
		t.Error("API floor with an unreadable config: no error")
	}
	if err := l.LoadAllEnabled(); err == nil {
		t.Error("LoadAllEnabled with an unreadable config: no error")
	}
}

// A readable entry without grants (installed by hand, or before grants) keeps what installing would give.
func TestEntryWithoutGrantsAsBefore(t *testing.T) {
	l := permissionsLoader(t, nil, []config.InstalledPlugin{{ID: "@jan/shelly", Enabled: true}})
	m := PluginManifest{API: 2, Permissions: &Permissions{LAN: true}}
	perms, _, _, err := l.effectivePermissions("@jan/shelly", m)
	if err != nil || !perms.LAN {
		t.Errorf("entry without grants: perms %+v, err %v; want lan as on install", perms, err)
	}
}
