package plugins

import (
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/Domktorymysli/vclu/pkg/config"
)

// A plugin repository is not trusted: ids, versions and file names from its index and manifests
// must not write or remove anything outside the plugins directory.

// outsidePlugins lists every path under root except the plugins directory, with file contents.
func outsidePlugins(t *testing.T, root, pluginsDir string) map[string]string {
	t.Helper()
	got := map[string]string{}
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if p == pluginsDir || strings.HasPrefix(p, pluginsDir+string(filepath.Separator)) {
			if d.IsDir() && p == pluginsDir {
				return fs.SkipDir
			}
			return nil
		}
		rel, _ := filepath.Rel(root, p)
		if d.IsDir() {
			got[rel+"/"] = ""
			return nil
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		got[rel] = string(b)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func diffPaths(before, after map[string]string) []string {
	var d []string
	for p, v := range before {
		if w, ok := after[p]; !ok {
			d = append(d, "removed "+p)
		} else if w != v {
			d = append(d, "changed "+p)
		}
	}
	for p := range after {
		if _, ok := before[p]; !ok {
			d = append(d, "created "+p)
		}
	}
	sort.Strings(d)
	return d
}

func TestDownloadPluginStaysInsidePluginsDir(t *testing.T) {
	const okManifest = `{"id":"weather","name":"Weather","version":"1.1.0","files":["init.lua"]}`
	index := func(id, version string) string {
		return `{"version":1,"name":"evil","description":"d","url":"x","plugins":[{"id":"` + id +
			`","name":"W","version":"` + version + `","path":"plugins/weather","tag":"weather-v1"}]}`
	}
	cases := []struct {
		name     string
		fullID   string
		index    string
		manifest string
	}{
		{"id climbs out", "@vclu/../../../victim", index("../../../victim", "1.1.0"), okManifest},
		{"namespace climbs out", "@../../../victim/weather", index("weather", "1.1.0"), okManifest},
		{"absolute id", "@vclu//tmp/victim", index("/tmp/victim", "1.1.0"), okManifest},
		{"version climbs out", "@vclu/weather", index("weather", "../../../victim"), okManifest},
		{"version removes a directory", "@vclu/weather", index("weather", "../../../../victim"), okManifest},
		{"version with a separator", "@vclu/weather", index("weather", "1.1.0/../../../../victim"), okManifest},
		{"file name climbs out", "@vclu/weather", index("weather", "1.1.0"),
			`{"id":"weather","name":"Weather","version":"1.1.0","files":["init.lua","../../../../../victim/keep.txt"]}`},
		{"absolute file name", "@vclu/weather", index("weather", "1.1.0"),
			`{"id":"weather","name":"Weather","version":"1.1.0","files":["/victim/keep.txt"]}`},
		{"backslash file name", "@vclu/weather", index("weather", "1.1.0"),
			`{"id":"weather","name":"Weather","version":"1.1.0","files":["..\\..\\..\\..\\..\\victim\\keep.txt"]}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// root/victim/keep.txt sits next to the data directory, root/server/plugins is the target
			root := t.TempDir()
			victim := filepath.Join(root, "victim")
			if err := os.MkdirAll(filepath.Join(victim, "1.0.0"), 0755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(victim, "keep.txt"), []byte("keep"), 0644); err != nil {
				t.Fatal(err)
			}
			loader := NewLoader(filepath.Join(root, "server", "data"))
			before := outsidePlugins(t, root, loader.pluginsDir)

			// a real server answers ../ in the path too: serve the cleaned path
			files := map[string]string{
				"/plugins.json":                tc.index,
				"/plugins/weather/plugin.json": tc.manifest,
				"/plugins/weather/init.lua":    "return {}\n",
				"/victim/keep.txt":             "overwritten",
			}
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				content, ok := files[path.Clean(strings.ReplaceAll(r.URL.Path, "\\", "/"))]
				if !ok {
					w.WriteHeader(http.StatusNotFound)
					return
				}
				w.Write([]byte(content))
			}))
			defer ts.Close()

			err := loader.DownloadPlugin(configRepo("evil", ts.URL), tc.fullID)

			if d := diffPaths(before, outsidePlugins(t, root, loader.pluginsDir)); len(d) > 0 {
				t.Errorf("outside the plugins directory: %v", d)
			}
			if err == nil {
				t.Errorf("DownloadPlugin accepted %s", tc.name)
			}
			if lock := loader.ReadLockFile(); len(lock) > 0 {
				t.Errorf("lock written for a refused plugin: %v", lock)
			}
		})
	}
}

// An existing version directory outside the plugins directory is not a candidate for clean-up,
// even when the plugin's directory name would lead there.
func TestCleanupOldVersionsStaysInsidePluginsDir(t *testing.T) {
	root := t.TempDir()
	loader := NewLoader(filepath.Join(root, "server", "data"))
	victimOld := filepath.Join(root, "victim", "1.0.0")
	if err := os.MkdirAll(victimOld, 0755); err != nil {
		t.Fatal(err)
	}
	before := outsidePlugins(t, root, loader.pluginsDir)

	loader.cleanupOldVersions(filepath.Join(loader.pluginsDir, "vclu--../../../../../victim"), "2.0.0", "2.0.0")

	if d := diffPaths(before, outsidePlugins(t, root, loader.pluginsDir)); len(d) > 0 {
		t.Errorf("outside the plugins directory: %v", d)
	}
}

// A lock entry is data on disk too: one that points outside the plugins directory is not loaded.
func TestResolvePluginDirIgnoresLockOutsidePluginsDir(t *testing.T) {
	root := t.TempDir()
	loader := NewLoader(filepath.Join(root, "server", "data"))
	outside := filepath.Join(root, "victim")
	if err := os.MkdirAll(outside, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(outside, "plugin.json"), []byte(`{"id":"weather"}`), 0644); err != nil {
		t.Fatal(err)
	}
	loader.WriteLockFile(LockFile{"@vclu/weather": {Version: "1.0.0", Path: "../../../victim"}})

	if dir := loader.resolvePluginDir("@vclu/weather"); dir == outside {
		t.Errorf("resolvePluginDir followed the lock outside the plugins directory: %s", dir)
	}
}

// A disabled repository is not used to download a missing plugin.
func TestFindRepositorySkipsDisabled(t *testing.T) {
	loader := NewLoader(t.TempDir())
	off := configRepo("evil", "http://127.0.0.1:1")
	off.Enabled = false
	if repo := loader.findRepository([]config.PluginRepository{off}, "evil"); repo != nil {
		t.Errorf("findRepository returned a disabled repository")
	}
	if repo := loader.findRepository([]config.PluginRepository{configRepo("ok", "x")}, "ok"); repo == nil {
		t.Errorf("findRepository missed an enabled repository")
	}
}
