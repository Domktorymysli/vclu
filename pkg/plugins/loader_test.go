package plugins

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Domktorymysli/vclu/pkg/config"
)

// newTestLoader creates a Loader with a temp dir
func newTestLoader(t *testing.T) (*Loader, string) {
	t.Helper()
	tmpDir := t.TempDir()
	loader := NewLoader(tmpDir)
	return loader, tmpDir
}

// newTestServer creates a test HTTP server that serves files from a map
func newTestServer(files map[string]string) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		content, ok := files[r.URL.Path]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(content))
	}))
}

// redirectTransport redirects all HTTP requests to a test server,
// preserving the URL path. Used with GitHubResolver tests where URLs
// contain "github.com" / "raw.githubusercontent.com" but need to hit localhost.
type redirectTransport struct {
	targetURL string
}

func (t *redirectTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	target, _ := url.Parse(t.targetURL)
	req.URL.Scheme = target.Scheme
	req.URL.Host = target.Host
	return http.DefaultTransport.RoundTrip(req)
}

func TestDownloadPlugin_CacheHit(t *testing.T) {
	loader, tmpDir := newTestLoader(t)

	// Pre-create versioned dir with .complete marker
	sd := safePluginDir("@vclu/weather")
	versionDir := filepath.Join(tmpDir, "plugins", sd, "1.0.1")
	os.MkdirAll(versionDir, 0755)
	os.WriteFile(filepath.Join(versionDir, ".complete"), []byte("weather-v1.0.1"), 0644)
	os.WriteFile(filepath.Join(versionDir, "plugin.json"), []byte(`{"id":"weather"}`), 0644)

	// Server that tracks if plugin files were requested
	called := false
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/plugins.json" {
			w.Write([]byte(`{
				"version": 1, "name": "test", "description": "test", "url": "x",
				"plugins": [{"id":"weather","name":"Weather","version":"1.0.1","path":"plugins/weather","tag":"weather-v1.0.1"}]
			}`))
			return
		}
		called = true
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	loader.httpClient = ts.Client()

	err := loader.DownloadPlugin(configRepo("test", ts.URL), "@vclu/weather")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if called {
		t.Error("server was called for plugin files — cache should have been used")
	}
}

func TestDownloadPlugin_AtomicWrite(t *testing.T) {
	loader, tmpDir := newTestLoader(t)

	manifest := `{"id":"weather","name":"Weather","version":"1.0.0","files":["init.lua"]}`
	initLua := `print("hello")`

	// GenericResolver ignores ref, so paths are just /{path}
	ts := newTestServer(map[string]string{
		"/plugins.json":                `{"version":1,"name":"test","description":"d","url":"x","plugins":[{"id":"weather","name":"Weather","version":"1.0.0","path":"plugins/weather","tag":"weather-v1.0.0"}]}`,
		"/plugins/weather/plugin.json": manifest,
		"/plugins/weather/init.lua":    initLua,
	})
	defer ts.Close()

	loader.httpClient = ts.Client()

	err := loader.DownloadPlugin(configRepo("test", ts.URL), "@vclu/weather")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Check .complete marker exists
	sd := safePluginDir("@vclu/weather")
	completeFile := filepath.Join(tmpDir, "plugins", sd, "1.0.0", ".complete")
	if _, err := os.Stat(completeFile); os.IsNotExist(err) {
		t.Error(".complete marker not created")
	}

	// Check no .tmp dirs remain
	entries, _ := os.ReadDir(filepath.Join(tmpDir, "plugins", sd))
	for _, e := range entries {
		if e.IsDir() && strings.HasPrefix(e.Name(), ".tmp") {
			t.Errorf("temp dir %s should have been renamed", e.Name())
		}
	}

	// Check lock file was written
	lock := loader.ReadLockFile()
	entry, ok := lock["@vclu/weather"]
	if !ok {
		t.Fatal("lock file missing entry for @vclu/weather")
	}
	if entry.Version != "1.0.0" || entry.GitTag != "weather-v1.0.0" {
		t.Errorf("lock entry mismatch: %+v", entry)
	}
}

func TestDownloadPlugin_404Fallback(t *testing.T) {
	loader, _ := newTestLoader(t)

	manifest := `{"id":"weather","name":"Weather","version":"1.0.0","files":["init.lua"]}`
	initLua := `print("hello")`

	// GitHubResolver produces /{ref}/{path} URLs — tag path returns 404, main succeeds
	ts := newTestServer(map[string]string{
		// /main/plugins.json — for FetchRepositoryIndex (always uses "main")
		"/user/repo/main/plugins.json": `{"version":1,"name":"test","description":"d","url":"x","plugins":[{"id":"weather","name":"Weather","version":"1.0.0","path":"plugins/weather","tag":"weather-v1.0.0"}]}`,
		// Tag path NOT served (404) — simulates deleted/missing tag
		// Main branch path IS served (fallback)
		"/user/repo/main/plugins/weather/plugin.json": manifest,
		"/user/repo/main/plugins/weather/init.lua":    initLua,
	})
	defer ts.Close()

	// Use GitHub URL so GitHubResolver is used (tag-aware paths)
	// redirectTransport sends all requests to the test server
	loader.httpClient = &http.Client{
		Transport: &redirectTransport{targetURL: ts.URL},
	}

	err := loader.DownloadPlugin(
		configRepo("test", "https://github.com/user/repo"),
		"@vclu/weather",
	)
	if err != nil {
		t.Fatalf("expected fallback to main, got error: %v", err)
	}
}

func TestDownloadPlugin_CleanupOldVersions(t *testing.T) {
	loader, tmpDir := newTestLoader(t)

	// Pre-create an old version
	sd := safePluginDir("@vclu/weather")
	oldDir := filepath.Join(tmpDir, "plugins", sd, "0.9.0")
	os.MkdirAll(oldDir, 0755)
	os.WriteFile(filepath.Join(oldDir, "plugin.json"), []byte("old"), 0644)

	manifest := `{"id":"weather","name":"Weather","version":"1.0.0","files":["init.lua"]}`
	initLua := `print("hello")`

	ts := newTestServer(map[string]string{
		"/plugins.json":                `{"version":1,"name":"test","description":"d","url":"x","plugins":[{"id":"weather","name":"Weather","version":"1.0.0","path":"plugins/weather","tag":"weather-v1.0.0"}]}`,
		"/plugins/weather/plugin.json": manifest,
		"/plugins/weather/init.lua":    initLua,
	})
	defer ts.Close()

	loader.httpClient = ts.Client()

	err := loader.DownloadPlugin(configRepo("test", ts.URL), "@vclu/weather")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Old version should be removed
	if _, err := os.Stat(oldDir); !os.IsNotExist(err) {
		t.Error("old version directory should have been removed")
	}

	// New version should exist
	newDir := filepath.Join(tmpDir, "plugins", sd, "1.0.0")
	if _, err := os.Stat(newDir); os.IsNotExist(err) {
		t.Error("new version directory should exist")
	}
}

func TestDownloadPlugin_NoTag_LatestDir(t *testing.T) {
	loader, tmpDir := newTestLoader(t)

	manifest := `{"id":"custom","name":"Custom","version":"1.0.0","files":["init.lua"]}`
	initLua := `print("hello")`

	// No "tag" field in listing → GenericResolver, "latest" subdir
	ts := newTestServer(map[string]string{
		"/plugins.json":               `{"version":1,"name":"test","description":"d","url":"x","plugins":[{"id":"custom","name":"Custom","version":"1.0.0","path":"plugins/custom"}]}`,
		"/plugins/custom/plugin.json": manifest,
		"/plugins/custom/init.lua":    initLua,
	})
	defer ts.Close()

	loader.httpClient = ts.Client()

	err := loader.DownloadPlugin(configRepo("test", ts.URL), "custom")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should use "latest" subdir, no .complete marker
	latestDir := filepath.Join(tmpDir, "plugins", "custom", "latest")
	if _, err := os.Stat(filepath.Join(latestDir, "plugin.json")); os.IsNotExist(err) {
		t.Error("expected plugin.json in latest/ subdir")
	}
	if _, err := os.Stat(filepath.Join(latestDir, ".complete")); !os.IsNotExist(err) {
		t.Error("untagged plugins should NOT have .complete marker")
	}
}

func TestLockFile_ReadWrite(t *testing.T) {
	loader, _ := newTestLoader(t)

	// Empty lock file on first read
	lock := loader.ReadLockFile()
	if len(lock) != 0 {
		t.Errorf("expected empty lock, got %d entries", len(lock))
	}

	// Write an entry
	entry := LockEntry{
		Version:  "1.0.1",
		GitTag:   "weather-v1.0.1",
		Path:     "vclu--weather/1.0.1",
		LockedAt: "2026-03-26T21:00:00Z",
	}
	lock["@vclu/weather"] = entry
	loader.WriteLockFile(lock)

	// Read it back
	lock2 := loader.ReadLockFile()
	if len(lock2) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(lock2))
	}
	got := lock2["@vclu/weather"]
	if got.Version != "1.0.1" || got.GitTag != "weather-v1.0.1" || got.Path != "vclu--weather/1.0.1" {
		t.Errorf("lock entry mismatch: %+v", got)
	}
}

func TestLockFile_AtomicWrite(t *testing.T) {
	loader, tmpDir := newTestLoader(t)

	lock := LockFile{
		"@vclu/weather": {Version: "1.0.0", Path: "vclu--weather/1.0.0", LockedAt: "2026-01-01T00:00:00Z"},
	}
	loader.WriteLockFile(lock)

	// No .tmp file should remain
	tmpFile := filepath.Join(tmpDir, "plugins", "plugins.lock.json.tmp")
	if _, err := os.Stat(tmpFile); !os.IsNotExist(err) {
		t.Error("temp lock file should have been removed after rename")
	}

	// Lock file should exist and be valid JSON
	lockPath := filepath.Join(tmpDir, "plugins", "plugins.lock.json")
	data, err := os.ReadFile(lockPath)
	if err != nil {
		t.Fatalf("failed to read lock file: %v", err)
	}
	var parsed LockFile
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("lock file is not valid JSON: %v", err)
	}
}

func TestResolvePluginDir_FromLockFile(t *testing.T) {
	loader, tmpDir := newTestLoader(t)

	// Create versioned dir
	sd := safePluginDir("@vclu/weather")
	versionDir := filepath.Join(tmpDir, "plugins", sd, "1.0.1")
	os.MkdirAll(versionDir, 0755)
	os.WriteFile(filepath.Join(versionDir, "plugin.json"), []byte(`{}`), 0644)

	// Write lock file pointing to it
	loader.WriteLockFile(LockFile{
		"@vclu/weather": {Version: "1.0.1", Path: "vclu--weather/1.0.1"},
	})

	dir := loader.resolvePluginDir("@vclu/weather")
	if dir != versionDir {
		t.Errorf("expected %s, got %s", versionDir, dir)
	}
}

func TestResolvePluginDir_ScansFallback(t *testing.T) {
	loader, tmpDir := newTestLoader(t)

	// Create versioned dir without lock file
	sd := safePluginDir("@vclu/weather")
	versionDir := filepath.Join(tmpDir, "plugins", sd, "2.0.0")
	os.MkdirAll(versionDir, 0755)
	os.WriteFile(filepath.Join(versionDir, "plugin.json"), []byte(`{}`), 0644)

	dir := loader.resolvePluginDir("@vclu/weather")
	if dir != versionDir {
		t.Errorf("expected %s, got %s", versionDir, dir)
	}
}

func TestResolvePluginDir_OldFlatDir(t *testing.T) {
	loader, tmpDir := newTestLoader(t)

	// Create old-style flat dir (migration compat)
	sd := safePluginDir("@vclu/weather")
	flatDir := filepath.Join(tmpDir, "plugins", sd)
	os.MkdirAll(flatDir, 0755)
	os.WriteFile(filepath.Join(flatDir, "plugin.json"), []byte(`{}`), 0644)

	dir := loader.resolvePluginDir("@vclu/weather")
	if dir != flatDir {
		t.Errorf("expected %s, got %s", flatDir, dir)
	}
}

func TestPluginListing_JSONCompat(t *testing.T) {
	// Verify JSON field naming: "tags" for categories, "tag" for git tag
	listing := PluginListing{
		ID:         "weather",
		Name:       "Weather",
		Version:    "1.0.0",
		Categories: []string{"automation", "sensor"},
		GitTag:     "weather-v1.0.0",
	}

	data, err := json.Marshal(listing)
	if err != nil {
		t.Fatal(err)
	}

	var raw map[string]interface{}
	json.Unmarshal(data, &raw)

	if _, ok := raw["tags"]; !ok {
		t.Error("Categories should marshal as 'tags'")
	}
	if _, ok := raw["tag"]; !ok {
		t.Error("GitTag should marshal as 'tag'")
	}
	if _, ok := raw["categories"]; ok {
		t.Error("'categories' key should NOT appear in JSON")
	}
	if _, ok := raw["gitTag"]; ok {
		t.Error("'gitTag' key should NOT appear in JSON")
	}

	// Round-trip
	input := `{"id":"test","name":"Test","version":"1.0.0","tags":["a","b"],"tag":"test-v1.0.0"}`
	var parsed PluginListing
	if err := json.Unmarshal([]byte(input), &parsed); err != nil {
		t.Fatal(err)
	}
	if len(parsed.Categories) != 2 || parsed.Categories[0] != "a" {
		t.Errorf("Categories not parsed from 'tags': %v", parsed.Categories)
	}
	if parsed.GitTag != "test-v1.0.0" {
		t.Errorf("GitTag not parsed from 'tag': %s", parsed.GitTag)
	}
}

// ---------------------------------------------------------------------------
// Integration test: real example plugin from vclu-plugins repo format
// ---------------------------------------------------------------------------

// Realistic plugins.json matching the vclu-plugins repository format
const realPluginsJSON = `{
  "version": 1,
  "name": "vCLU Official Plugins",
  "namespace": "vclu",
  "description": "Oficjalne pluginy dla Virtual CLU",
  "author": "Domktorymysli",
  "url": "https://github.com/Domktorymysli/vclu-plugins",
  "plugins": [
    {
      "id": "example",
      "name": "Example Plugin",
      "version": "2.1.0",
      "description": "Przykładowy plugin - szablon",
      "author": "vCLU Team",
      "path": "plugins/example",
      "tags": ["example", "template"],
      "tag": "example-v2.1.0"
    }
  ]
}`

// Realistic plugin.json matching vclu-plugins/plugins/example/plugin.json
const realPluginManifest = `{
  "id": "example",
  "name": "Example Plugin",
  "version": "2.1.0",
  "author": "vCLU Team",
  "license": "MIT",
  "description": "Przykładowy plugin pokazujący jak pisać własne pluginy dla vCLU",
  "docs": [{ "title": "Getting Started", "file": "README.md" }],
  "config": {
    "greeting": { "type": "string", "default": "Hello", "description": "Tekst powitania" },
    "interval": { "type": "number", "default": 60, "description": "Interwał w sekundach" },
    "enabled":  { "type": "boolean", "default": true, "description": "Czy aktywny" }
  },
  "exports": {
    "sensors":  [{ "id": "value", "type": "number" }, { "id": "ready", "type": "sensor" }],
    "controls": [{ "id": "target", "type": "number" }]
  }
}`

const realInitLua = `local plugin = Plugin:new("example", {
    name = "Example Plugin",
    version = "2.1.0"
})
plugin:onInit(function(config) end)
return plugin
`

// TestExamplePlugin_FullLifecycle tests download → cache → upgrade → cleanup
// using realistic plugin data matching the vclu-plugins repository format.
func TestExamplePlugin_FullLifecycle(t *testing.T) {
	loader, tmpDir := newTestLoader(t)

	// GitHub-style test server with tag-based paths
	ts := newTestServer(map[string]string{
		// GitHubResolver: /{user}/{repo}/{ref}/{path}
		"/Domktorymysli/vclu-plugins/main/plugins.json":                          realPluginsJSON,
		"/Domktorymysli/vclu-plugins/example-v2.1.0/plugins/example/plugin.json": realPluginManifest,
		"/Domktorymysli/vclu-plugins/example-v2.1.0/plugins/example/init.lua":    realInitLua,
	})
	defer ts.Close()

	loader.httpClient = &http.Client{
		Transport: &redirectTransport{targetURL: ts.URL},
	}
	repo := configRepo("vCLU Official", "https://github.com/Domktorymysli/vclu-plugins")

	// --- Step 1: First download from tag ---
	err := loader.DownloadPlugin(repo, "@vclu/example")
	if err != nil {
		t.Fatalf("step 1: download failed: %v", err)
	}

	sd := safePluginDir("@vclu/example")
	versionDir := filepath.Join(tmpDir, "plugins", sd, "2.1.0")

	// Versioned dir with .complete marker
	if _, err := os.Stat(filepath.Join(versionDir, ".complete")); os.IsNotExist(err) {
		t.Fatal("step 1: .complete marker missing")
	}
	// plugin.json downloaded
	if _, err := os.Stat(filepath.Join(versionDir, "plugin.json")); os.IsNotExist(err) {
		t.Fatal("step 1: plugin.json missing")
	}
	// init.lua downloaded
	if _, err := os.Stat(filepath.Join(versionDir, "init.lua")); os.IsNotExist(err) {
		t.Fatal("step 1: init.lua missing")
	}
	// Manifest content is valid
	manifestData, _ := os.ReadFile(filepath.Join(versionDir, "plugin.json"))
	var manifest PluginManifest
	if err := json.Unmarshal(manifestData, &manifest); err != nil {
		t.Fatalf("step 1: invalid plugin.json: %v", err)
	}
	if manifest.Version != "2.1.0" || manifest.Name != "Example Plugin" {
		t.Errorf("step 1: manifest mismatch: %s v%s", manifest.Name, manifest.Version)
	}

	// Lock file written
	lock := loader.ReadLockFile()
	entry, ok := lock["@vclu/example"]
	if !ok {
		t.Fatal("step 1: lock file missing @vclu/example")
	}
	if entry.Version != "2.1.0" || entry.GitTag != "example-v2.1.0" || entry.Path != sd+"/2.1.0" {
		t.Errorf("step 1: lock entry: %+v", entry)
	}

	// --- Step 2: Second download → cache hit ---
	httpCalls := 0
	ts2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/Domktorymysli/vclu-plugins/main/plugins.json" {
			w.Write([]byte(realPluginsJSON))
			return
		}
		httpCalls++
		w.WriteHeader(http.StatusOK)
	}))
	defer ts2.Close()

	loader.httpClient = &http.Client{
		Transport: &redirectTransport{targetURL: ts2.URL},
	}

	err = loader.DownloadPlugin(repo, "@vclu/example")
	if err != nil {
		t.Fatalf("step 2: cache hit failed: %v", err)
	}
	if httpCalls > 0 {
		t.Errorf("step 2: expected cache hit, but %d file requests were made", httpCalls)
	}

	// --- Step 3: Version bump → new download + old cleanup ---
	upgradedPluginsJSON := strings.Replace(realPluginsJSON, `"version": "2.1.0"`, `"version": "3.0.0"`, 1)
	upgradedPluginsJSON = strings.Replace(upgradedPluginsJSON, `"tag": "example-v2.1.0"`, `"tag": "example-v3.0.0"`, 1)
	upgradedManifest := strings.Replace(realPluginManifest, `"version": "2.1.0"`, `"version": "3.0.0"`, 1)

	ts3 := newTestServer(map[string]string{
		"/Domktorymysli/vclu-plugins/main/plugins.json":                          upgradedPluginsJSON,
		"/Domktorymysli/vclu-plugins/example-v3.0.0/plugins/example/plugin.json": upgradedManifest,
		"/Domktorymysli/vclu-plugins/example-v3.0.0/plugins/example/init.lua":    realInitLua,
	})
	defer ts3.Close()

	loader.httpClient = &http.Client{
		Transport: &redirectTransport{targetURL: ts3.URL},
	}

	err = loader.DownloadPlugin(repo, "@vclu/example")
	if err != nil {
		t.Fatalf("step 3: upgrade failed: %v", err)
	}

	// New version exists
	newDir := filepath.Join(tmpDir, "plugins", sd, "3.0.0")
	if _, err := os.Stat(filepath.Join(newDir, ".complete")); os.IsNotExist(err) {
		t.Fatal("step 3: new version .complete missing")
	}

	// Old version cleaned up
	if _, err := os.Stat(versionDir); !os.IsNotExist(err) {
		t.Error("step 3: old version 2.1.0 should have been removed")
	}

	// Lock file updated
	lock = loader.ReadLockFile()
	entry = lock["@vclu/example"]
	if entry.Version != "3.0.0" || entry.GitTag != "example-v3.0.0" {
		t.Errorf("step 3: lock not updated: %+v", entry)
	}

	// --- Step 4: resolvePluginDir finds the versioned dir ---
	resolved := loader.resolvePluginDir("@vclu/example")
	if resolved != newDir {
		t.Errorf("step 4: resolvePluginDir = %s, want %s", resolved, newDir)
	}
}

// TestExamplePlugin_GitHubFallback tests that when a tag is deleted from GitHub,
// the download falls back to main branch and still succeeds.
func TestExamplePlugin_GitHubFallback(t *testing.T) {
	loader, tmpDir := newTestLoader(t)

	// Tag path returns 404, main path succeeds
	ts := newTestServer(map[string]string{
		"/Domktorymysli/vclu-plugins/main/plugins.json": realPluginsJSON,
		// example-v2.1.0 paths NOT served → 404
		// main paths served (fallback)
		"/Domktorymysli/vclu-plugins/main/plugins/example/plugin.json": realPluginManifest,
		"/Domktorymysli/vclu-plugins/main/plugins/example/init.lua":    realInitLua,
	})
	defer ts.Close()

	loader.httpClient = &http.Client{
		Transport: &redirectTransport{targetURL: ts.URL},
	}
	repo := configRepo("vCLU Official", "https://github.com/Domktorymysli/vclu-plugins")

	err := loader.DownloadPlugin(repo, "@vclu/example")
	if err != nil {
		t.Fatalf("fallback to main should succeed: %v", err)
	}

	// Plugin downloaded despite tag 404
	sd := safePluginDir("@vclu/example")
	versionDir := filepath.Join(tmpDir, "plugins", sd, "2.1.0")
	if _, err := os.Stat(filepath.Join(versionDir, "init.lua")); os.IsNotExist(err) {
		t.Error("plugin files should exist after fallback")
	}
}

// TestExamplePlugin_RegistryParsing tests that the real plugins.json format
// is correctly parsed, including namespace, tags, and gitTag fields.
func TestExamplePlugin_RegistryParsing(t *testing.T) {
	loader, _ := newTestLoader(t)

	ts := newTestServer(map[string]string{
		"/plugins.json": realPluginsJSON,
	})
	defer ts.Close()

	loader.httpClient = ts.Client()

	// GenericResolver — use non-GitHub URL for direct path mapping
	index, err := loader.FetchRepositoryIndex(configRepo("test", ts.URL))
	if err != nil {
		t.Fatalf("failed to fetch index: %v", err)
	}

	if index.Namespace != "vclu" {
		t.Errorf("namespace = %q, want 'vclu'", index.Namespace)
	}
	if index.Name != "vCLU Official Plugins" {
		t.Errorf("name = %q", index.Name)
	}
	if len(index.Plugins) != 1 {
		t.Fatalf("expected 1 plugin, got %d", len(index.Plugins))
	}

	p := index.Plugins[0]
	if p.ID != "example" {
		t.Errorf("id = %q", p.ID)
	}
	if p.GitTag != "example-v2.1.0" {
		t.Errorf("gitTag = %q, want 'example-v2.1.0'", p.GitTag)
	}
	if len(p.Categories) != 2 || p.Categories[0] != "example" {
		t.Errorf("categories = %v", p.Categories)
	}
	if p.Path != "plugins/example" {
		t.Errorf("path = %q", p.Path)
	}
}

// helper to create a config.PluginRepository
func configRepo(name, url string) config.PluginRepository {
	return config.PluginRepository{
		Name:    name,
		URL:     url,
		Enabled: true,
	}
}

// A factory plugin declares no configuration and must still load; the loader
// used to skip every plugin whose config was empty, which made the pattern
// impossible.
func TestPluginDeclaresConfig(t *testing.T) {
	cases := []struct {
		name     string
		manifest string
		want     bool
	}{
		{"config object with fields", `{"id":"a","config":{"host":{"type":"string"}}}`, true},
		{"empty config object", `{"id":"a","config":{}}`, false},
		{"no config key at all", `{"id":"a","name":"Factory"}`, false},
		{"legacy configSchema", `{"id":"a","configSchema":[{"key":"host"}]}`, true},
		{"empty configSchema", `{"id":"a","configSchema":[]}`, false},
		// Anything unreadable keeps the old, cautious behaviour.
		{"broken json", `{not json`, true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, "plugin.json"), []byte(tc.manifest), 0644); err != nil {
				t.Fatal(err)
			}
			if got := pluginDeclaresConfig(dir); got != tc.want {
				t.Errorf("pluginDeclaresConfig() = %v, want %v", got, tc.want)
			}
		})
	}

	t.Run("missing manifest", func(t *testing.T) {
		if !pluginDeclaresConfig(t.TempDir()) {
			t.Error("a missing manifest should not make a plugin load unconfigured")
		}
	})
}
