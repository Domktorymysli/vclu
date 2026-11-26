package plugins

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/Domktorymysli/vclu/pkg/config"
	lua "github.com/yuin/gopher-lua"
)

// repoOf serves an untagged plugin at plugins/<id> from the given files.
func repoOf(t *testing.T, id, version string, files map[string][]byte) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/plugins.json" {
			_, _ = fmt.Fprintf(w, `{"namespace":"jan","plugins":[{"id":%q,"version":%q,"path":"plugins/%s"}]}`, id, version, id)
			return
		}
		body, ok := files[strings.TrimPrefix(r.URL.Path, "/plugins/"+id+"/")]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_, _ = w.Write(body)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// nothingLeft: no version directory, no temporary one and no lock entry for the plugin.
func nothingLeft(t *testing.T, l *Loader, id string) {
	t.Helper()
	entries, _ := os.ReadDir(filepath.Join(l.pluginsDir, safePluginDir(id)))
	for _, e := range entries {
		t.Errorf("%s left behind", e.Name())
	}
	if _, ok := l.ReadLockFile()[id]; ok {
		t.Error("lock entry written")
	}
}

// A repository file over the limit refuses the download and leaves nothing behind.
func TestDownloadPluginRefusesOversizedFile(t *testing.T) {
	loader, _ := newTestLoader(t)
	srv := repoOf(t, "big", "1.0.0", map[string][]byte{
		"plugin.json": []byte(`{"id":"big","version":"1.0.0","files":["init.lua","data.lua"]}`),
		"init.lua":    []byte("return {}\n"),
		"data.lua":    make([]byte, maxPluginFile+1),
	})
	err := loader.DownloadPlugin(configRepo("test", srv.URL), "@jan/big")
	if err == nil || !strings.Contains(err.Error(), "larger than") {
		t.Fatalf("download: %v, want a size refusal", err)
	}
	nothingLeft(t, loader, "@jan/big")
}

// A manifest listing more files than the limit refuses the download.
func TestDownloadPluginRefusesTooManyFiles(t *testing.T) {
	loader, _ := newTestLoader(t)
	files := map[string][]byte{"init.lua": []byte("return {}\n")}
	var names []string
	for i := 0; i <= maxPluginFiles; i++ {
		name := fmt.Sprintf("f%d.lua", i)
		files[name] = []byte("-- part\n")
		names = append(names, fmt.Sprintf("%q", name))
	}
	files["plugin.json"] = []byte(`{"id":"many","version":"1.0.0","files":[` + strings.Join(names, ",") + `]}`)
	srv := repoOf(t, "many", "1.0.0", files)
	err := loader.DownloadPlugin(configRepo("test", srv.URL), "@jan/many")
	if err == nil || !strings.Contains(err.Error(), "at most") {
		t.Fatalf("download: %v, want a file count refusal", err)
	}
	nothingLeft(t, loader, "@jan/many")
}

// The manifest's plugin API is one this vclu runs, on both sides of the range.
func TestCheckDownloadedManifestAPIRange(t *testing.T) {
	dir := t.TempDir()
	for api, ok := range map[string]bool{"-1": false, "0": true, "1": true, "2": true, "3": false} {
		path := filepath.Join(dir, "plugin.json")
		if err := os.WriteFile(path, []byte(`{"id":"x","version":"1.0.0","permissions":{},"api":`+api+`}`), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := checkDownloadedManifest(path); (err == nil) != ok {
			t.Errorf("api %s: %v, want ok=%v", api, err, ok)
		}
	}
}

// A configuration that lists plugins without this one says nothing about what the user allowed
// it: the plugin does not run. Without any configuration (a first start, tests) loading goes on.
func TestLoadPluginNotInConfigDoesNotRun(t *testing.T) {
	manifest := `{"id":"gone","version":"1.0.0","api":2,"permissions":{"lan":{"why":"devices"},"mqtt":{"why":"topics"}}}`
	l := permissionsLoader(t, nil, []config.InstalledPlugin{{ID: "@jan/other", Enabled: true}})
	l.SetLuaExecutor(&noLua{})
	writePluginFiles(t, l, "@jan/gone", "1.0.0", manifest)
	if perms, _, _, err := l.effectivePermissions("@jan/gone", PluginManifest{API: 2, Permissions: &Permissions{LAN: true, MQTT: true}}); err == nil || perms.LAN || perms.MQTT {
		t.Errorf("not installed: perms %+v, err %v; want an error and nothing allowed", perms, err)
	}
	if err := l.LoadPlugin("@jan/gone", nil); err == nil || !strings.Contains(err.Error(), "not installed") {
		t.Errorf("load of a plugin not in the configuration: %v", err)
	}
	if l.IsLoaded("@jan/gone") {
		t.Error("loaded although not installed")
	}

	bare, _ := newTestLoader(t)
	bare.SetLuaExecutor(&noLua{})
	writePluginFiles(t, bare, "@jan/gone", "1.0.0", manifest)
	if err := bare.LoadPlugin("@jan/gone", nil); err != nil {
		t.Errorf("load without any configuration: %v", err)
	}
}

// An entry without grants whose files are gone gets nothing of what the fetched files ask for:
// the plugin runs with no permission until the user allows them, as after an update from the
// panel without the old files.
func TestSyncFilesFetchedWithoutGrantsWaits(t *testing.T) {
	loader, serverDir := newTestLoader(t)
	srv := repoOf(t, "weather", "1.1.0", map[string][]byte{
		"plugin.json": []byte(`{"id":"weather","version":"1.1.0","api":2,"permissions":{"lan":{"why":"devices"},"mqtt":{"why":"topics"},"internet":{"hosts":["*"],"why":"any"}}}`),
		"init.lua":    []byte("return {}\n"),
	})
	err := writeTestConfig(serverDir, &config.VCLUConfig{Plugins: &config.VCLUPluginsConfig{
		Repositories: []config.PluginRepository{configRepo("test", srv.URL)},
		Installed:    []config.InstalledPlugin{{ID: "@jan/weather", Version: "1.1.0", Enabled: true, Repository: "test"}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	L := lua.NewState()
	t.Cleanup(L.Close)
	if err := L.DoString(`
		got = {}
		JSON = {}
		function JSON:decode(s) return s end
		PluginSandbox = {}
		function PluginSandbox:create(id, opts) got.lan = opts.lan; got.mqtt = opts.mqtt; got.limits = opts.limits end
		function PluginSandbox:setConfig() end
		function PluginSandbox:execute() return true end
		function PluginSandbox:cleanup() end
		Plugin = {}
		function Plugin.initFromConfig() end
	`); err != nil {
		t.Fatal(err)
	}
	loader.SetLuaExecutor(&luaStateExecutor{L})

	if err := loader.LoadAllEnabled(); err != nil {
		t.Fatalf("LoadAllEnabled: %v", err)
	}
	if !loader.IsLoaded("@jan/weather") {
		t.Fatal("not loaded")
	}
	inst, ok, err := loader.installedEntry("@jan/weather")
	if err != nil || !ok {
		t.Fatal(err, ok)
	}
	if inst.Grants == nil || inst.Grants.LAN != nil || inst.Grants.MQTT != nil || inst.Grants.Internet != nil || inst.Grants.Actions == nil || len(*inst.Grants.Actions) != 0 {
		t.Errorf("grants %+v, want recorded and empty", inst.Grants)
	}
	if pending := loader.PendingPermissions(inst); !reflect.DeepEqual(pending, []string{"internet", "lan", "mqtt"}) {
		t.Errorf("pending %v", pending)
	}
	got := L.GetGlobal("got").(*lua.LTable)
	if got.RawGetString("lan") != lua.LFalse || got.RawGetString("mqtt") != lua.LFalse {
		t.Errorf("sandbox got lan=%v mqtt=%v, want neither", got.RawGetString("lan"), got.RawGetString("mqtt"))
	}
	if limits := got.RawGetString("limits").String(); !strings.Contains(limits, `"internetHosts":[]`) {
		t.Errorf("sandbox limits %s, want no internet host", limits)
	}
}

// PinPorts records the old files' ports for a grant from before ports were recorded, once.
func TestPinPortsKeepsTheOldFilesPorts(t *testing.T) {
	l := permissionsLoader(t, nil, []config.InstalledPlugin{{ID: "@jan/ws", Grants: &config.PluginGrants{
		LAN: &config.LANGrant{Hosts: []string{"192.168.1.0/24"}}, TCP: &config.HostsGrant{}, UDP: &config.HostsGrant{},
	}}})
	old := Permissions{LAN: true, LANPorts: []int{80}, TCP: true, TCPPorts: []int{23}}
	l.PinPorts("@jan/ws", old)
	inst, _, _ := l.installedEntry("@jan/ws")
	g := inst.Grants
	if g.LAN.Ports == nil || !slices.Equal(*g.LAN.Ports, []int{80}) || !slices.Equal(g.LAN.Hosts, []string{"192.168.1.0/24"}) {
		t.Errorf("lan %+v, want websocket ports [80] and the hosts kept", g.LAN)
	}
	if g.TCP.Ports == nil || !slices.Equal(*g.TCP.Ports, []int{23}) {
		t.Errorf("tcp %+v, want ports [23]", g.TCP)
	}
	if g.UDP.Broadcast != nil {
		t.Errorf("udp %+v: ports recorded for a permission the files do not ask for", g.UDP)
	}
	// recorded ports stay as they are
	l.PinPorts("@jan/ws", Permissions{LAN: true, LANPorts: []int{80, 22}})
	inst, _, _ = l.installedEntry("@jan/ws")
	if !slices.Equal(*inst.Grants.LAN.Ports, []int{80}) {
		t.Errorf("lan ports %v after a second pin, want [80]", *inst.Grants.LAN.Ports)
	}
	// nothing for a plugin that is not installed
	l.PinPorts("@jan/none", old)
}
