package plugins

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A repository that is one plugin: plugin.json and init.lua at its root, no plugins.json.
func TestSinglePluginRepository(t *testing.T) {
	for _, c := range []struct {
		name, manifestID, wantNamespace string
	}{
		{"namespace from the owner", "blebox", "jan-k"},
		{"namespace from the manifest", "@jan/blebox", "jan"},
	} {
		t.Run(c.name, func(t *testing.T) {
			loader, tmpDir := newTestLoader(t)
			ts := newTestServer(map[string]string{
				"/Jan-K/vclu-blebox/main/plugin.json": `{"id":"` + c.manifestID + `","name":"BleBox","version":"0.2.0","tags":["relay"],"files":["init.lua","README.md"]}`,
				"/Jan-K/vclu-blebox/main/init.lua":    `return Plugin:new("blebox", {})`,
				"/Jan-K/vclu-blebox/main/README.md":   `# BleBox`,
			})
			defer ts.Close()
			loader.httpClient = &http.Client{Transport: &redirectTransport{targetURL: ts.URL}}
			repo := configRepo("blebox", "https://github.com/Jan-K/vclu-blebox")

			index, err := loader.FetchRepositoryIndex(repo)
			if err != nil {
				t.Fatalf("index: %v", err)
			}
			if index.Namespace != c.wantNamespace || len(index.Plugins) != 1 || index.Plugins[0].ID != "blebox" ||
				index.Plugins[0].Version != "0.2.0" || index.Plugins[0].Path != "" || index.Plugins[0].Categories[0] != "relay" {
				t.Fatalf("index: %+v", index)
			}

			id := "@" + c.wantNamespace + "/blebox"
			if err := loader.DownloadPlugin(repo, id); err != nil {
				t.Fatalf("download: %v", err)
			}
			dir := filepath.Join(tmpDir, "plugins", safePluginDir(id), "latest")
			for _, f := range []string{"plugin.json", "init.lua", "README.md"} {
				if _, err := os.Stat(filepath.Join(dir, f)); err != nil {
					t.Errorf("%s: %v", f, err)
				}
			}
		})
	}
}

func TestSinglePluginRepositoryRefused(t *testing.T) {
	for name, files := range map[string]map[string]string{
		"neither file":     {},
		"invalid id":       {"/jan/foo/main/plugin.json": `{"id":"Blebox--x","version":"1.0.0"}`},
		"broken manifest":  {"/jan/foo/main/plugin.json": `{"id":`},
		"plugins.json 500": {"/jan/foo/main/plugin.json": `{"id":"foo","version":"1.0.0"}`, "500": ""},
	} {
		t.Run(name, func(t *testing.T) {
			loader, _ := newTestLoader(t)
			var ts = newTestServer(files)
			if _, ok := files["500"]; ok {
				ts.Close()
				ts = newStatusServer(http.StatusInternalServerError)
			}
			defer ts.Close()
			loader.httpClient = &http.Client{Transport: &redirectTransport{targetURL: ts.URL}}
			if _, err := loader.FetchRepositoryIndex(configRepo("x", "https://github.com/jan/foo")); err == nil {
				t.Error("index accepted")
			}
		})
	}
}

func newStatusServer(code int) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(code) }))
}

func TestRepoFilePath(t *testing.T) {
	if got := RepoFilePath("", "plugin.json"); got != "plugin.json" {
		t.Errorf("root: %s", got)
	}
	if got := RepoFilePath("plugins/x/", "README.md"); got != "plugins/x/README.md" {
		t.Errorf("dir: %s", got)
	}
	if !strings.HasSuffix(DefaultResolver("https://github.com/a/b").RawFileURL("https://github.com/a/b", "", RepoFilePath("", "plugin.json")), "/main/plugin.json") {
		t.Error("raw URL of a root file")
	}
}
