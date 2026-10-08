package plugins

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/Domktorymysli/vclu/pkg/config"
)

// LuaExecutor interface for executing Lua scripts
type LuaExecutor interface {
	Execute(script string) (string, error)
}

// executeAsPlugin runs plugin start-up code as plugin:<id>: pollers and
// timers it creates change state as that plugin.
func (l *Loader) executeAsPlugin(pluginID, script string) (string, error) {
	if ce, ok := l.luaExecutor.(interface {
		ExecuteAs(origin, actor, correlationID, script string) (string, error)
	}); ok {
		return ce.ExecuteAs("system", "plugin:"+pluginID, "", script)
	}
	return l.luaExecutor.Execute(script)
}

// PluginManifest represents plugin.json structure
type PluginManifest struct {
	ID           string              `json:"id"`
	Name         string              `json:"name"`
	Version      string              `json:"version"`
	Description  string              `json:"description"`
	Author       string              `json:"author"`
	Homepage     string              `json:"homepage,omitempty"`
	License      string              `json:"license,omitempty"`
	MinVCLU      string              `json:"minVcluVersion,omitempty"`
	Tags         []string            `json:"tags,omitempty"`
	Dependencies []string            `json:"dependencies,omitempty"`
	ConfigSchema []ConfigFieldSchema `json:"configSchema,omitempty"`
	Docs         []PluginDoc         `json:"docs,omitempty"`
	Files        []string            `json:"files,omitempty"` // Additional files to download (default: ["init.lua"])
}

// PluginDoc represents a documentation file in plugin.json
type PluginDoc struct {
	Title string `json:"title"`
	File  string `json:"file"`
}

// ConfigFieldSchema represents a configuration field schema
type ConfigFieldSchema struct {
	Key         string `json:"key"`
	Type        string `json:"type"`
	Label       string `json:"label"`
	Description string `json:"description,omitempty"`
	Required    bool   `json:"required,omitempty"`
	Default     string `json:"default,omitempty"`
}

// RepositoryIndex represents plugins.json from a repository
type RepositoryIndex struct {
	Version     int             `json:"version"`
	Name        string          `json:"name"`
	Namespace   string          `json:"namespace,omitempty"`
	Description string          `json:"description"`
	Author      string          `json:"author,omitempty"`
	URL         string          `json:"url"`
	Plugins     []PluginListing `json:"plugins"`
}

// PluginListing represents a plugin entry in the repository index
type PluginListing struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Version     string   `json:"version"`
	Description string   `json:"description"`
	Author      string   `json:"author"`
	Path        string   `json:"path"`
	Categories  []string `json:"tags,omitempty"` // JSON: "tags" (category tags, not git tags)
	GitTag      string   `json:"tag,omitempty"`  // JSON: "tag" (git tag for immutable download)
}

// LockEntry represents a single plugin entry in the lock file
type LockEntry struct {
	Version  string `json:"version"`
	GitTag   string `json:"gitTag,omitempty"`
	Path     string `json:"path"`
	LockedAt string `json:"lockedAt"`
}

// LockFile maps plugin fullId to its lock entry
type LockFile map[string]LockEntry

// parseFullId parses a fullId like "@vclu/weather" into namespace and short id
// Returns (namespace, shortId). If not a fullId, returns ("", fullId)
func parseFullId(fullId string) (namespace, shortId string) {
	if strings.HasPrefix(fullId, "@") {
		parts := strings.SplitN(strings.TrimPrefix(fullId, "@"), "/", 2)
		if len(parts) == 2 {
			return parts[0], parts[1]
		}
	}
	return "", fullId
}

// safePluginDir converts fullId to safe directory name
// "@vclu/weather" -> "vclu--weather"
func safePluginDir(fullId string) string {
	namespace, shortId := parseFullId(fullId)
	if namespace != "" {
		return namespace + "--" + shortId
	}
	return shortId
}

// LoadedPlugin represents a plugin that's been loaded into memory
type LoadedPlugin struct {
	Manifest  PluginManifest
	Config    map[string]interface{}
	Enabled   bool
	LocalPath string // Path to downloaded plugin files
}

// Loader manages plugin loading and lifecycle
type Loader struct {
	serverDir   string
	pluginsDir  string
	httpClient  *http.Client
	luaExecutor LuaExecutor
	loaded      map[string]*LoadedPlugin
}

// NewLoader creates a new plugin loader
func NewLoader(serverDir string) *Loader {
	pluginsDir := filepath.Join(serverDir, "plugins")

	// Ensure plugins directory exists
	if err := os.MkdirAll(pluginsDir, 0755); err != nil {
		log.Printf("Warning: Failed to create plugins directory: %v", err)
	}

	return &Loader{
		serverDir:  serverDir,
		pluginsDir: pluginsDir,
		httpClient: &http.Client{Timeout: 30 * time.Second},
		loaded:     make(map[string]*LoadedPlugin),
	}
}

// SetLuaExecutor sets the Lua executor for running plugin code
func (l *Loader) SetLuaExecutor(executor LuaExecutor) {
	l.luaExecutor = executor
}

// FetchRepositoryIndex fetches plugins.json from a repository URL
func (l *Loader) FetchRepositoryIndex(repo config.PluginRepository) (*RepositoryIndex, error) {
	resolver := DefaultResolver(repo.URL)
	pluginsURL := resolver.RawFileURL(repo.URL, "", "plugins.json")

	resp, err := l.httpClient.Get(pluginsURL)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch repository: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	var index RepositoryIndex
	if err := json.Unmarshal(body, &index); err != nil {
		return nil, fmt.Errorf("invalid JSON: %w", err)
	}

	return &index, nil
}

// DownloadPlugin downloads a plugin from a repository
// pluginID can be fullId (@vclu/weather) or short id (weather)
func (l *Loader) DownloadPlugin(repo config.PluginRepository, pluginID string) error {
	// Parse fullId to get short id for repository lookup
	_, shortId := parseFullId(pluginID)

	// Fetch repository index
	index, err := l.FetchRepositoryIndex(repo)
	if err != nil {
		return fmt.Errorf("failed to fetch repository index: %w", err)
	}

	// Find plugin in index by short id
	var listing *PluginListing
	for i := range index.Plugins {
		if index.Plugins[i].ID == shortId {
			listing = &index.Plugins[i]
			break
		}
	}

	if listing == nil {
		return fmt.Errorf("plugin %s not found in repository", shortId)
	}

	safeDir := safePluginDir(pluginID)

	// Determine version subdir: tagged plugins get versioned dir, untagged get "latest"
	version := listing.Version
	if listing.GitTag == "" {
		version = "latest"
	}

	targetDir := filepath.Join(l.pluginsDir, safeDir, version)
	completeMarker := filepath.Join(targetDir, ".complete")

	// Cache hit: immutable tag already downloaded
	if listing.GitTag != "" {
		if _, err := os.Stat(completeMarker); err == nil {
			log.Printf("[PLUGINS] cached %s/%s, skipping download", safeDir, version)
			return nil
		}
	}

	// Download to temp dir for atomicity
	tmpDir := filepath.Join(l.pluginsDir, safeDir, ".tmp-"+version)
	os.RemoveAll(tmpDir) // clean up any previous failed attempt
	if err := os.MkdirAll(tmpDir, 0755); err != nil {
		return fmt.Errorf("failed to create temp directory: %w", err)
	}

	resolver := DefaultResolver(repo.URL)

	// Try with git tag first, fallback to main on failure
	baseURL := resolver.RawFileURL(repo.URL, listing.GitTag, listing.Path)
	dlErr := l.downloadAllFiles(baseURL, tmpDir)
	if dlErr != nil && listing.GitTag != "" {
		log.Printf("[PLUGINS] WARNING: tag %s fetch failed (%v), retrying from main", listing.GitTag, dlErr)
		os.RemoveAll(tmpDir)
		if err := os.MkdirAll(tmpDir, 0755); err != nil {
			return fmt.Errorf("failed to create temp directory: %w", err)
		}
		baseURL = resolver.RawFileURL(repo.URL, "", listing.Path)
		dlErr = l.downloadAllFiles(baseURL, tmpDir)
	}
	if dlErr != nil {
		os.RemoveAll(tmpDir)
		return fmt.Errorf("failed to download plugin: %w", dlErr)
	}

	// Atomic rename: tmp → target
	os.RemoveAll(targetDir) // remove incomplete previous attempt
	if err := os.Rename(tmpDir, targetDir); err != nil {
		os.RemoveAll(tmpDir)
		return fmt.Errorf("failed to finalize download: %w", err)
	}

	// Mark as complete (only for tagged versions — untagged always re-download)
	if listing.GitTag != "" {
		os.WriteFile(completeMarker, []byte(listing.GitTag), 0644)
	}

	// Cleanup old versions (after successful download)
	l.cleanupOldVersions(filepath.Join(l.pluginsDir, safeDir), version)

	// Update lock file (after cleanup — lock never points to removed dir)
	l.writeLockEntry(pluginID, LockEntry{
		Version:  listing.Version,
		GitTag:   listing.GitTag,
		Path:     safeDir + "/" + version,
		LockedAt: time.Now().Format(time.RFC3339),
	})

	log.Printf("[PLUGINS] downloaded %s to %s", pluginID, targetDir)
	return nil
}

// downloadAllFiles downloads plugin.json and all files listed in the manifest
func (l *Loader) downloadAllFiles(baseURL, localDir string) error {
	// 1. Download plugin.json (always required)
	manifestURL := baseURL + "/plugin.json"
	manifestPath := filepath.Join(localDir, "plugin.json")
	if err := l.downloadFile(manifestURL, manifestPath); err != nil {
		return fmt.Errorf("failed to download plugin.json: %w", err)
	}

	// 2. Parse manifest to get file list
	manifestData, err := os.ReadFile(manifestPath)
	if err != nil {
		return fmt.Errorf("failed to read plugin.json: %w", err)
	}

	var manifest PluginManifest
	if err := json.Unmarshal(manifestData, &manifest); err != nil {
		return fmt.Errorf("failed to parse plugin.json: %w", err)
	}

	// 3. Get files to download (default: ["init.lua"])
	files := manifest.Files
	if len(files) == 0 {
		files = []string{"init.lua"}
	}

	// 4. Download all files from the list
	for _, filename := range files {
		fileURL := baseURL + "/" + filename
		localPath := filepath.Join(localDir, filename)

		if err := l.downloadFile(fileURL, localPath); err != nil {
			return fmt.Errorf("failed to download %s: %w", filename, err)
		}
	}

	return nil
}

// cleanupOldVersions removes version directories other than currentVersion
func (l *Loader) cleanupOldVersions(pluginBaseDir, currentVersion string) {
	entries, err := os.ReadDir(pluginBaseDir)
	if err != nil {
		return
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		name := entry.Name()
		if name == currentVersion || strings.HasPrefix(name, ".tmp-") {
			continue
		}
		path := filepath.Join(pluginBaseDir, name)
		if err := os.RemoveAll(path); err != nil {
			log.Printf("[PLUGINS] WARNING: failed to clean up old version %s: %v", path, err)
		} else {
			log.Printf("[PLUGINS] cleaned up old version: %s", path)
		}
	}
}

// downloadFile downloads a file from URL to local path
func (l *Loader) downloadFile(url, localPath string) error {
	resp, err := l.httpClient.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	out, err := os.Create(localPath)
	if err != nil {
		return err
	}
	defer out.Close()

	_, err = io.Copy(out, resp.Body)
	return err
}

// resolvePluginDir returns the local directory for a plugin's files.
// Checks lock file first, then scans for versioned dirs, then falls back to flat dir.
func (l *Loader) resolvePluginDir(pluginID string) string {
	// 1. Check lock file
	lock := l.ReadLockFile()
	if entry, ok := lock[pluginID]; ok {
		dir := filepath.Join(l.pluginsDir, entry.Path)
		if _, err := os.Stat(filepath.Join(dir, "plugin.json")); err == nil {
			return dir
		}
	}

	// 2. Scan for versioned subdirs with plugin.json
	safeDir := safePluginDir(pluginID)
	baseDir := filepath.Join(l.pluginsDir, safeDir)
	entries, err := os.ReadDir(baseDir)
	if err == nil {
		sort.Slice(entries, func(i, j int) bool {
			return entries[i].Name() > entries[j].Name()
		})
		for _, entry := range entries {
			if !entry.IsDir() || strings.HasPrefix(entry.Name(), ".tmp-") {
				continue
			}
			dir := filepath.Join(baseDir, entry.Name())
			if _, err := os.Stat(filepath.Join(dir, "plugin.json")); err == nil {
				return dir
			}
		}
	}

	// 3. Fallback: old flat directory (migration compat)
	return baseDir
}

// Lock file operations

func (l *Loader) lockFilePath() string {
	return filepath.Join(l.pluginsDir, "plugins.lock.json")
}

// ReadLockFile reads the lock file from disk. Returns empty map on error.
func (l *Loader) ReadLockFile() LockFile {
	data, err := os.ReadFile(l.lockFilePath())
	if err != nil {
		return make(LockFile)
	}
	var lock LockFile
	if err := json.Unmarshal(data, &lock); err != nil {
		return make(LockFile)
	}
	return lock
}

// WriteLockFile atomically writes the full lock file to disk.
func (l *Loader) WriteLockFile(lock LockFile) {
	data, err := json.MarshalIndent(lock, "", "  ")
	if err != nil {
		log.Printf("[PLUGINS] WARNING: failed to marshal lock file: %v", err)
		return
	}

	tmpPath := l.lockFilePath() + ".tmp"
	if err := os.WriteFile(tmpPath, data, 0644); err != nil {
		log.Printf("[PLUGINS] WARNING: failed to write lock file: %v", err)
		return
	}
	if err := os.Rename(tmpPath, l.lockFilePath()); err != nil {
		log.Printf("[PLUGINS] WARNING: failed to finalize lock file: %v", err)
		os.Remove(tmpPath)
	}
}

// writeLockEntry updates a single entry in the lock file
func (l *Loader) writeLockEntry(pluginID string, entry LockEntry) {
	lock := l.ReadLockFile()
	lock[pluginID] = entry
	l.WriteLockFile(lock)
}

// LoadPlugin loads a plugin from local directory in a sandbox
// pluginID is the fullId (e.g., "@vclu/weather")
func (l *Loader) LoadPlugin(pluginID string, pluginConfig map[string]interface{}) error {
	if l.luaExecutor == nil {
		return fmt.Errorf("Lua executor not set")
	}

	localDir := l.resolvePluginDir(pluginID)

	// Read plugin manifest
	manifestPath := filepath.Join(localDir, "plugin.json")
	manifestData, err := os.ReadFile(manifestPath)
	if err != nil {
		return fmt.Errorf("failed to read plugin manifest: %w", err)
	}

	var manifest PluginManifest
	if err := json.Unmarshal(manifestData, &manifest); err != nil {
		return fmt.Errorf("invalid plugin manifest: %w", err)
	}

	// Read init.lua
	initPath := filepath.Join(localDir, "init.lua")
	initCode, err := os.ReadFile(initPath)
	if err != nil {
		return fmt.Errorf("failed to read init.lua: %w", err)
	}

	// 1. Create sandbox for the plugin
	createSandboxScript := fmt.Sprintf(`PluginSandbox:create("%s")`, pluginID)
	if _, err := l.luaExecutor.Execute(createSandboxScript); err != nil {
		return fmt.Errorf("failed to create sandbox: %w", err)
	}

	// 2. Set plugin config in sandbox
	configJSON, _ := json.Marshal(pluginConfig)
	setConfigScript := fmt.Sprintf(`PluginSandbox:setConfig("%s", JSON:decode([[%s]]), "%s")`,
		pluginID, string(configJSON), localDir)
	if _, err := l.luaExecutor.Execute(setConfigScript); err != nil {
		return fmt.Errorf("failed to set plugin config: %w", err)
	}

	// 3. Execute init.lua in sandbox
	// Escape the code for Lua string literal
	escapedCode := strings.ReplaceAll(string(initCode), "\\", "\\\\")
	escapedCode = strings.ReplaceAll(escapedCode, "\"", "\\\"")
	escapedCode = strings.ReplaceAll(escapedCode, "\n", "\\n")
	escapedCode = strings.ReplaceAll(escapedCode, "\r", "")

	executeScript := fmt.Sprintf(`
local code = "%s"
local result, err = PluginSandbox:execute("%s", code)
if err then
    error(err)
end
`, escapedCode, pluginID)

	if _, err := l.executeAsPlugin(pluginID, executeScript); err != nil {
		// Cleanup sandbox on failure
		cleanupScript := fmt.Sprintf(`PluginSandbox:cleanup("%s")`, pluginID)
		l.luaExecutor.Execute(cleanupScript)
		return fmt.Errorf("failed to execute init.lua: %w", err)
	}

	// 4. Initialize plugin with config (triggers onInit callback)
	initConfigScript := fmt.Sprintf(`Plugin.initFromConfig("%s", JSON:decode([[%s]]))`,
		pluginID, string(configJSON))
	if _, err := l.executeAsPlugin(pluginID, initConfigScript); err != nil {
		// Cleanup sandbox on failure
		cleanupScript := fmt.Sprintf(`PluginSandbox:cleanup("%s")`, pluginID)
		l.luaExecutor.Execute(cleanupScript)
		return fmt.Errorf("failed to init plugin with config: %w", err)
	}

	// Store loaded plugin by fullId
	l.loaded[pluginID] = &LoadedPlugin{
		Manifest:  manifest,
		Config:    pluginConfig,
		Enabled:   true,
		LocalPath: localDir,
	}

	log.Printf("[PLUGINS] loaded %s v%s (%s)", manifest.Name, manifest.Version, pluginID)
	return nil
}

// pluginDeclaresConfig reports whether a plugin asks the user for configuration.
// Manifests carry it either as the "config" object or the older "configSchema"
// array; a plugin with neither is configured from code, not from a form.
func pluginDeclaresConfig(localDir string) bool {
	data, err := os.ReadFile(filepath.Join(localDir, "plugin.json"))
	if err != nil {
		// Unreadable manifest: keep the old, cautious behaviour.
		return true
	}
	var probe struct {
		Config       map[string]json.RawMessage `json:"config"`
		ConfigSchema []json.RawMessage          `json:"configSchema"`
	}
	if err := json.Unmarshal(data, &probe); err != nil {
		return true
	}
	return len(probe.Config) > 0 || len(probe.ConfigSchema) > 0
}

// LoadAllEnabled loads all enabled plugins from config
// This also clears the loaded map to support reloading after Lua reinitialize
func (l *Loader) LoadAllEnabled() error {
	// Clear loaded map (supports reloading after Lua reinitialize)
	l.loaded = make(map[string]*LoadedPlugin)

	masterConfig, err := config.ReadMasterConfig(l.serverDir)
	if err != nil || masterConfig == nil || masterConfig.Plugins == nil {
		return nil // No plugins configured
	}

	var errors []string
	loadedCount := 0

	for _, installed := range masterConfig.Plugins.Installed {
		if !installed.Enabled {
			continue
		}

		// Check if plugin files are available. This has to happen before the
		// configuration check below, which needs the manifest.
		localDir := l.resolvePluginDir(installed.ID)
		manifestPath := filepath.Join(localDir, "plugin.json")
		if _, err := os.Stat(manifestPath); os.IsNotExist(err) {
			// Try to download from repository
			repo := l.findRepository(masterConfig.Plugins.Repositories, installed.Repository)
			if repo == nil {
				errors = append(errors, fmt.Sprintf("%s: repository %s not found", installed.ID, installed.Repository))
				continue
			}

			if err := l.DownloadPlugin(*repo, installed.ID); err != nil {
				errors = append(errors, fmt.Sprintf("%s: %v", installed.ID, err))
				continue
			}
			localDir = l.resolvePluginDir(installed.ID)
		}

		// Skip plugins that ask for configuration and have none. A plugin that
		// declares none is a factory driven from user modules, so there is
		// nothing to wait for and it must still load.
		if len(installed.Config) == 0 && pluginDeclaresConfig(localDir) {
			log.Printf("[PLUGINS] skipping %s: not configured", installed.ID)
			continue
		}

		// Load the plugin (passing fullId)
		if err := l.LoadPlugin(installed.ID, installed.Config); err != nil {
			errors = append(errors, fmt.Sprintf("%s: %v", installed.ID, err))
			continue
		}

		loadedCount++
	}

	if loadedCount > 0 {
		log.Printf("[PLUGINS] loaded %d plugins", loadedCount)
	}

	if len(errors) > 0 {
		return fmt.Errorf("some plugins failed to load: %s", strings.Join(errors, "; "))
	}

	return nil
}

// findRepository finds a repository by name
func (l *Loader) findRepository(repos []config.PluginRepository, name string) *config.PluginRepository {
	for i := range repos {
		if repos[i].Name == name {
			return &repos[i]
		}
	}
	return nil
}

// UnloadPlugin unloads a plugin and cleans up its sandbox
func (l *Loader) UnloadPlugin(pluginID string) error {
	plugin, exists := l.loaded[pluginID]
	if !exists {
		return fmt.Errorf("plugin %s not loaded", pluginID)
	}

	if l.luaExecutor != nil {
		// 1. Call plugin's custom cleanup callback if defined
		pluginCleanupScript := fmt.Sprintf(`
if Plugin and Plugin.instances and Plugin.instances["%s"] then
    local p = Plugin.instances["%s"]
    if p._cleanupCallback then
        pcall(p._cleanupCallback)
    end
    Plugin.instances["%s"] = nil
end
`, pluginID, pluginID, pluginID)
		l.luaExecutor.Execute(pluginCleanupScript)

		// 2. Cleanup sandbox (removes all tracked resources automatically)
		sandboxCleanupScript := fmt.Sprintf(`PluginSandbox:cleanup("%s")`, pluginID)
		l.luaExecutor.Execute(sandboxCleanupScript)
	}

	delete(l.loaded, pluginID)
	log.Printf("[PLUGINS] unloaded %s", plugin.Manifest.Name)
	return nil
}

// GetLoadedPlugins returns all loaded plugins
func (l *Loader) GetLoadedPlugins() map[string]*LoadedPlugin {
	return l.loaded
}

// IsLoaded checks if a plugin is loaded
func (l *Loader) IsLoaded(pluginID string) bool {
	_, exists := l.loaded[pluginID]
	return exists
}

// GetPluginDir returns the plugins directory path
func (l *Loader) GetPluginDir() string {
	return l.pluginsDir
}
