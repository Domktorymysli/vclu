package web

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/Domktorymysli/vclu/pkg/config"
	"github.com/Domktorymysli/vclu/pkg/plugins"
	"github.com/gin-gonic/gin"
)

// PluginRegistryResponse represents the plugins.json from a repository
type PluginRegistryResponse struct {
	Version     int              `json:"version"`
	Name        string           `json:"name"`
	Namespace   string           `json:"namespace"`
	Description string           `json:"description"`
	URL         string           `json:"url"`
	Plugins     []PluginMetadata `json:"plugins"`
}

// PluginMetadata represents a plugin in the registry
type PluginMetadata struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Version     string   `json:"version"`
	Description string   `json:"description"`
	Author      string   `json:"author"`
	Path        string   `json:"path"`
	Tags        []string `json:"tags"`
	GitTag      string   `json:"tag,omitempty"`
}

// handlePlugins shows the plugins management page
func (s *Server) handlePlugins(c *gin.Context) {
	masterConfig, _ := config.ReadMasterConfig(s.serverDir)

	data := gin.H{
		"title":           "Plugins",
		"currentPage":     "plugins",
		"configured":      s.isConfigured,
		"supportsModules": s.supportsModules,
	}

	// Get plugins config or use defaults
	var pluginsConfig *config.VCLUPluginsConfig
	if masterConfig != nil && masterConfig.Plugins != nil {
		pluginsConfig = masterConfig.Plugins
	} else {
		pluginsConfig = config.DefaultPluginsConfig()
	}

	data["repositories"] = pluginsConfig.Repositories
	data["installed"] = pluginsConfig.Installed

	// Add translations
	trans := s.getTranslations(c)
	data["lang"] = trans["lang"]
	data["t"] = trans["t"]
	data["langs"] = trans["langs"]

	s.renderHTML(c, http.StatusOK, "plugins.html", data)
}

// handlePluginsAPIRepositories returns list of repositories
func (s *Server) handlePluginsAPIRepositories(c *gin.Context) {
	masterConfig, _ := config.ReadMasterConfig(s.serverDir)

	var repos []config.PluginRepository
	if masterConfig != nil && masterConfig.Plugins != nil {
		repos = masterConfig.Plugins.Repositories
	} else {
		repos = config.DefaultPluginsConfig().Repositories
	}

	c.JSON(http.StatusOK, gin.H{"repositories": repos})
}

// handlePluginsAPIAddRepository adds a new repository
func (s *Server) handlePluginsAPIAddRepository(c *gin.Context) {
	var req struct {
		Name string `json:"name"`
		URL  string `json:"url"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request"})
		return
	}

	if req.Name == "" || req.URL == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Name and URL are required"})
		return
	}

	masterConfig, _ := config.ReadMasterConfig(s.serverDir)
	if masterConfig == nil {
		masterConfig = &config.VCLUConfig{}
	}
	if masterConfig.Plugins == nil {
		masterConfig.Plugins = config.DefaultPluginsConfig()
	}

	// Check if repo with same name or URL exists
	for _, repo := range masterConfig.Plugins.Repositories {
		if repo.Name == req.Name {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Repository with this name already exists"})
			return
		}
		if repo.URL == req.URL {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Repository with this URL already exists"})
			return
		}
	}

	// Add new repository
	masterConfig.Plugins.Repositories = append(masterConfig.Plugins.Repositories, config.PluginRepository{
		Name:    req.Name,
		URL:     req.URL,
		Enabled: true,
		Default: false,
	})

	if err := config.WriteMasterConfig(s.serverDir, masterConfig); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to save config"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"success": true})
}

// handlePluginsAPIRemoveRepository removes a repository
func (s *Server) handlePluginsAPIRemoveRepository(c *gin.Context) {
	name := c.Param("name")

	masterConfig, _ := config.ReadMasterConfig(s.serverDir)
	if masterConfig == nil || masterConfig.Plugins == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Repository not found"})
		return
	}

	// Find and remove repository
	var newRepos []config.PluginRepository
	found := false
	for _, repo := range masterConfig.Plugins.Repositories {
		if repo.Name == name {
			found = true
			if repo.Default {
				c.JSON(http.StatusBadRequest, gin.H{"error": "Cannot remove default repository"})
				return
			}
			continue
		}
		newRepos = append(newRepos, repo)
	}

	if !found {
		c.JSON(http.StatusNotFound, gin.H{"error": "Repository not found"})
		return
	}

	masterConfig.Plugins.Repositories = newRepos

	if err := config.WriteMasterConfig(s.serverDir, masterConfig); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to save config"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"success": true})
}

// handlePluginsAPIAvailable fetches available plugins from all enabled repositories
func (s *Server) handlePluginsAPIAvailable(c *gin.Context) {
	masterConfig, _ := config.ReadMasterConfig(s.serverDir)

	var repos []config.PluginRepository
	if masterConfig != nil && masterConfig.Plugins != nil {
		repos = masterConfig.Plugins.Repositories
	} else {
		repos = config.DefaultPluginsConfig().Repositories
	}

	var allPlugins []gin.H
	var errors []string

	for _, repo := range repos {
		if !repo.Enabled {
			continue
		}

		registry, err := fetchRegistryFromRepo(repo)
		if err != nil {
			errors = append(errors, fmt.Sprintf("%s: %v", repo.Name, err))
			continue
		}

		namespace := registry.Namespace
		if namespace == "" {
			namespace = repo.Name // fallback to repo name
		}

		for _, p := range registry.Plugins {
			// Build fullId: @namespace/id
			fullId := fmt.Sprintf("@%s/%s", namespace, p.ID)

			// Check if installed
			installed := false
			installedVersion := ""
			if masterConfig != nil && masterConfig.Plugins != nil {
				for _, inst := range masterConfig.Plugins.Installed {
					if inst.ID == fullId {
						installed = true
						installedVersion = inst.Version
						break
					}
				}
			}

			allPlugins = append(allPlugins, gin.H{
				"id":               p.ID,
				"fullId":           fullId,
				"namespace":        namespace,
				"name":             p.Name,
				"version":          p.Version,
				"description":      p.Description,
				"author":           p.Author,
				"tags":             p.Tags,
				"gitTag":           p.GitTag,
				"path":             p.Path,
				"repository":       repo.Name,
				"repoUrl":          buildPluginRepoUrl(repo.URL, p.Path, p.GitTag),
				"installed":        installed,
				"installedVersion": installedVersion,
				"updateAvailable":  installed && installedVersion != p.Version,
			})
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"plugins": allPlugins,
		"errors":  errors,
	})
}

// handlePluginsAPIInstalled returns list of installed plugins
func (s *Server) handlePluginsAPIInstalled(c *gin.Context) {
	masterConfig, _ := config.ReadMasterConfig(s.serverDir)

	type installedPlugin struct {
		config.InstalledPlugin
		NeedsConfig bool `json:"needsConfig"`
	}
	installed := []installedPlugin{}
	if masterConfig != nil && masterConfig.Plugins != nil {
		loader := plugins.NewLoader(s.serverDir)
		for _, p := range masterConfig.Plugins.Installed {
			installed = append(installed, installedPlugin{p, loader.NeedsConfig(p)})
		}
	}

	c.JSON(http.StatusOK, gin.H{"installed": installed})
}

// handlePluginsAPIInstall installs a plugin
func (s *Server) handlePluginsAPIInstall(c *gin.Context) {
	var req struct {
		ID         string `json:"id"`         // short id (e.g., "weather")
		FullID     string `json:"fullId"`     // full id (e.g., "@vclu/weather")
		Repository string `json:"repository"` // repository name
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request"})
		return
	}

	masterConfig, _ := config.ReadMasterConfig(s.serverDir)
	if masterConfig == nil {
		masterConfig = &config.VCLUConfig{}
	}
	if masterConfig.Plugins == nil {
		masterConfig.Plugins = config.DefaultPluginsConfig()
	}

	// Find repository
	var targetRepo *config.PluginRepository
	for i := range masterConfig.Plugins.Repositories {
		if masterConfig.Plugins.Repositories[i].Name == req.Repository {
			targetRepo = &masterConfig.Plugins.Repositories[i]
			break
		}
	}

	if targetRepo == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Repository not found"})
		return
	}

	// Fetch plugin info from repository
	registry, err := fetchRegistryFromRepo(*targetRepo)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("Failed to fetch from repository: %v", err)})
		return
	}

	// Get namespace
	namespace := registry.Namespace
	if namespace == "" {
		namespace = req.Repository
	}

	var pluginMeta *PluginMetadata
	for i := range registry.Plugins {
		if registry.Plugins[i].ID == req.ID {
			pluginMeta = &registry.Plugins[i]
			break
		}
	}

	if pluginMeta == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Plugin not found in repository"})
		return
	}

	// Build fullId
	fullId := fmt.Sprintf("@%s/%s", namespace, req.ID)

	// The files first: the config then never names a version that is not on disk (#112)
	if err := plugins.NewLoader(s.serverDir).DownloadPlugin(*targetRepo, fullId); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("Failed to download plugin: %v", err)})
		return
	}

	// Check if already installed
	for i, inst := range masterConfig.Plugins.Installed {
		if inst.ID == fullId {
			// Update version
			masterConfig.Plugins.Installed[i].Version = pluginMeta.Version
			masterConfig.Plugins.Installed[i].InstalledAt = time.Now().Format(time.RFC3339)

			if err := config.WriteMasterConfig(s.serverDir, masterConfig); err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to save config"})
				return
			}

			c.JSON(http.StatusOK, gin.H{"success": true, "action": "updated", "fullId": fullId, "version": pluginMeta.Version})
			return
		}
	}

	// Add to installed
	masterConfig.Plugins.Installed = append(masterConfig.Plugins.Installed, config.InstalledPlugin{
		ID:          fullId,
		Version:     pluginMeta.Version,
		Enabled:     true,
		Repository:  req.Repository,
		InstalledAt: time.Now().Format(time.RFC3339),
		Config:      make(map[string]interface{}),
	})

	if err := config.WriteMasterConfig(s.serverDir, masterConfig); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to save config"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"success": true, "action": "installed", "fullId": fullId, "version": pluginMeta.Version})
}

// handlePluginsAPIUninstall uninstalls a plugin
func (s *Server) handlePluginsAPIUninstall(c *gin.Context) {
	pluginID := c.Param("id")

	masterConfig, _ := config.ReadMasterConfig(s.serverDir)
	if masterConfig == nil || masterConfig.Plugins == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Plugin not found"})
		return
	}

	var newInstalled []config.InstalledPlugin
	found := false
	for _, inst := range masterConfig.Plugins.Installed {
		if inst.ID == pluginID {
			found = true
			continue
		}
		newInstalled = append(newInstalled, inst)
	}

	if !found {
		c.JSON(http.StatusNotFound, gin.H{"error": "Plugin not found"})
		return
	}

	masterConfig.Plugins.Installed = newInstalled

	if err := config.WriteMasterConfig(s.serverDir, masterConfig); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to save config"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"success": true})
}

// handlePluginsAPIToggle enables/disables a plugin
func (s *Server) handlePluginsAPIToggle(c *gin.Context) {
	pluginID := c.Param("id")

	var req struct {
		Enabled bool `json:"enabled"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request"})
		return
	}

	masterConfig, _ := config.ReadMasterConfig(s.serverDir)
	if masterConfig == nil || masterConfig.Plugins == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Plugin not found"})
		return
	}

	// Find plugin and get its config
	var pluginConfig map[string]interface{}
	found := false
	for i := range masterConfig.Plugins.Installed {
		if masterConfig.Plugins.Installed[i].ID == pluginID {
			masterConfig.Plugins.Installed[i].Enabled = req.Enabled
			pluginConfig = masterConfig.Plugins.Installed[i].Config
			found = true
			break
		}
	}

	if !found {
		c.JSON(http.StatusNotFound, gin.H{"error": "Plugin not found"})
		return
	}

	if err := config.WriteMasterConfig(s.serverDir, masterConfig); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to save config"})
		return
	}

	// Load or unload the plugin immediately if controller is available
	if s.pluginController != nil {
		if req.Enabled {
			// Load the plugin
			if err := s.pluginController.LoadPlugin(pluginID, pluginConfig); err != nil {
				c.JSON(http.StatusOK, gin.H{
					"success": true,
					"warning": fmt.Sprintf("Plugin enabled but failed to load: %v", err),
				})
				return
			}
		} else {
			// Unload the plugin
			if s.pluginController.IsLoaded(pluginID) {
				if err := s.pluginController.UnloadPlugin(pluginID); err != nil {
					c.JSON(http.StatusOK, gin.H{
						"success": true,
						"warning": fmt.Sprintf("Plugin disabled but failed to unload: %v", err),
					})
					return
				}
			}
		}
	}

	c.JSON(http.StatusOK, gin.H{"success": true})
}

// handlePluginsAPIConfig updates plugin configuration
func (s *Server) handlePluginsAPIConfig(c *gin.Context) {
	pluginID := c.Param("id")

	var req struct {
		Config map[string]interface{} `json:"config"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request"})
		return
	}

	masterConfig, _ := config.ReadMasterConfig(s.serverDir)
	if masterConfig == nil || masterConfig.Plugins == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Plugin not found"})
		return
	}

	found := false
	for i := range masterConfig.Plugins.Installed {
		if masterConfig.Plugins.Installed[i].ID == pluginID {
			masterConfig.Plugins.Installed[i].Config = req.Config
			found = true
			break
		}
	}

	if !found {
		c.JSON(http.StatusNotFound, gin.H{"error": "Plugin not found"})
		return
	}

	if err := config.WriteMasterConfig(s.serverDir, masterConfig); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to save config"})
		return
	}

	// If plugin is enabled and has config now, load it (or reload with new config)
	if s.pluginController != nil && len(req.Config) > 0 {
		// Find if plugin is enabled
		var isEnabled bool
		for _, inst := range masterConfig.Plugins.Installed {
			if inst.ID == pluginID {
				isEnabled = inst.Enabled
				break
			}
		}

		if isEnabled {
			// Unload if already loaded (to apply new config)
			if s.pluginController.IsLoaded(pluginID) {
				s.pluginController.UnloadPlugin(pluginID)
			}

			// Load with new config
			if err := s.pluginController.LoadPlugin(pluginID, req.Config); err != nil {
				c.JSON(http.StatusOK, gin.H{
					"success": true,
					"warning": fmt.Sprintf("Config saved but plugin failed to load: %v", err),
				})
				return
			}
		}
	}

	c.JSON(http.StatusOK, gin.H{"success": true})
}

// handlePluginsAPIGetSchema returns plugin.json config schema for a plugin
func (s *Server) handlePluginsAPIGetSchema(c *gin.Context) {
	pluginID := c.Param("id")
	repoName := c.Query("repository")

	// Handle fullId format (@namespace/id)
	shortID := pluginID
	if strings.HasPrefix(pluginID, "@") {
		parts := strings.SplitN(strings.TrimPrefix(pluginID, "@"), "/", 2)
		if len(parts) == 2 {
			shortID = parts[1]
		}
	}

	masterConfig, _ := config.ReadMasterConfig(s.serverDir)
	if masterConfig == nil || masterConfig.Plugins == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "No plugins configured"})
		return
	}

	// Find repository
	var targetRepo *config.PluginRepository
	for i := range masterConfig.Plugins.Repositories {
		if masterConfig.Plugins.Repositories[i].Name == repoName {
			targetRepo = &masterConfig.Plugins.Repositories[i]
			break
		}
	}

	if targetRepo == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Repository not found"})
		return
	}

	// Find plugin path and gitTag in registry
	registryPlugins, err := fetchPluginsFromRepo(*targetRepo)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	var pluginPath, gitTag string
	for _, p := range registryPlugins {
		if p.ID == shortID {
			pluginPath = p.Path
			gitTag = p.GitTag
			break
		}
	}

	if pluginPath == "" {
		c.JSON(http.StatusNotFound, gin.H{"error": "Plugin not found"})
		return
	}

	// Fetch plugin.json
	schema, err := fetchPluginSchema(*targetRepo, pluginPath, gitTag)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, schema)
}

// fetchPluginSchema fetches plugin.json from repository
func fetchPluginSchema(repo config.PluginRepository, pluginPath, gitTag string) (map[string]interface{}, error) {
	resolver := plugins.DefaultResolver(repo.URL)
	pluginURL := resolver.RawFileURL(repo.URL, gitTag, pluginPath+"/plugin.json")

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Get(pluginURL)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	var schema map[string]interface{}
	if err := json.Unmarshal(body, &schema); err != nil {
		return nil, fmt.Errorf("invalid JSON: %w", err)
	}

	return schema, nil
}

// handlePluginsAPIGetDoc returns documentation content for a plugin
func (s *Server) handlePluginsAPIGetDoc(c *gin.Context) {
	pluginID := c.Param("id")
	docFile := c.Query("file")
	repoName := c.Query("repository")

	if docFile == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "file parameter is required"})
		return
	}

	// Handle fullId format (@namespace/id)
	shortID := pluginID
	if strings.HasPrefix(pluginID, "@") {
		parts := strings.SplitN(strings.TrimPrefix(pluginID, "@"), "/", 2)
		if len(parts) == 2 {
			shortID = parts[1]
		}
	}

	masterConfig, _ := config.ReadMasterConfig(s.serverDir)
	if masterConfig == nil || masterConfig.Plugins == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "No plugins configured"})
		return
	}

	// Find repository
	var targetRepo *config.PluginRepository
	for i := range masterConfig.Plugins.Repositories {
		if masterConfig.Plugins.Repositories[i].Name == repoName {
			targetRepo = &masterConfig.Plugins.Repositories[i]
			break
		}
	}

	if targetRepo == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Repository not found"})
		return
	}

	// Find plugin path and gitTag in registry
	registryPlugins, err := fetchPluginsFromRepo(*targetRepo)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	var pluginPath, gitTag string
	for _, p := range registryPlugins {
		if p.ID == shortID {
			pluginPath = p.Path
			gitTag = p.GitTag
			break
		}
	}

	if pluginPath == "" {
		c.JSON(http.StatusNotFound, gin.H{"error": "Plugin not found"})
		return
	}

	// Fetch doc file
	content, err := fetchPluginDoc(*targetRepo, pluginPath, docFile, gitTag)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"content": content,
		"file":    docFile,
	})
}

// fetchPluginDoc fetches a documentation file from repository
func fetchPluginDoc(repo config.PluginRepository, pluginPath, docFile, gitTag string) (string, error) {
	resolver := plugins.DefaultResolver(repo.URL)
	docURL := resolver.RawFileURL(repo.URL, gitTag, pluginPath+"/"+docFile)

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Get(docURL)
	if err != nil {
		return "", fmt.Errorf("failed to fetch: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read response: %w", err)
	}

	return string(body), nil
}

// fetchPluginsFromRepo fetches plugins.json from a repository
func fetchPluginsFromRepo(repo config.PluginRepository) ([]PluginMetadata, error) {
	registry, err := fetchRegistryFromRepo(repo)
	if err != nil {
		return nil, err
	}
	return registry.Plugins, nil
}

// fetchRegistryFromRepo fetches full plugins.json registry from a repository
func fetchRegistryFromRepo(repo config.PluginRepository) (*PluginRegistryResponse, error) {
	resolver := plugins.DefaultResolver(repo.URL)
	pluginsURL := resolver.RawFileURL(repo.URL, "", "plugins.json")

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Get(pluginsURL)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	var registry PluginRegistryResponse
	if err := json.Unmarshal(body, &registry); err != nil {
		return nil, fmt.Errorf("invalid JSON: %w", err)
	}

	return &registry, nil
}

// buildPluginRepoUrl builds a URL to the plugin's directory in the repository
func buildPluginRepoUrl(repoUrl, pluginPath, gitTag string) string {
	if pluginPath == "" {
		return repoUrl
	}
	resolver := plugins.DefaultResolver(repoUrl)
	return resolver.BrowseURL(repoUrl, gitTag, pluginPath)
}
