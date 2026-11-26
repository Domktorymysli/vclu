package web

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"github.com/Domktorymysli/vclu/pkg/config"
	"github.com/Domktorymysli/vclu/pkg/plugins"
	"github.com/gin-gonic/gin"
)

var errPluginNotFound = &configError{http.StatusNotFound, "Plugin not found"}

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
	data["installed"] = s.installedPluginsView(pluginsConfig.Installed)

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

	err := config.UpdateMasterConfig(s.serverDir, func(masterConfig *config.VCLUConfig) error {
		if masterConfig.Plugins == nil {
			masterConfig.Plugins = config.DefaultPluginsConfig()
		}

		// Check if repo with same name or URL exists
		for _, repo := range masterConfig.Plugins.Repositories {
			if repo.Name == req.Name {
				return &configError{http.StatusBadRequest, "Repository with this name already exists"}
			}
			if repo.URL == req.URL {
				return &configError{http.StatusBadRequest, "Repository with this URL already exists"}
			}
		}

		// Add new repository
		masterConfig.Plugins.Repositories = append(masterConfig.Plugins.Repositories, config.PluginRepository{
			Name:    req.Name,
			URL:     req.URL,
			Enabled: true,
			Default: false,
		})
		return nil
	})
	if ce, ok := asConfigError(err); ok {
		c.JSON(ce.status, gin.H{"error": ce.msg})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to save config"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"success": true})
}

// handlePluginsAPIRemoveRepository removes a repository
func (s *Server) handlePluginsAPIRemoveRepository(c *gin.Context) {
	name := c.Param("name")

	errRepoNotFound := &configError{http.StatusNotFound, "Repository not found"}
	err := config.UpdateMasterConfig(s.serverDir, func(masterConfig *config.VCLUConfig) error {
		if masterConfig.Plugins == nil {
			return errRepoNotFound
		}

		// Find and remove repository
		var newRepos []config.PluginRepository
		found := false
		for _, repo := range masterConfig.Plugins.Repositories {
			if repo.Name == name {
				found = true
				if repo.Default {
					return &configError{http.StatusBadRequest, "Cannot remove default repository"}
				}
				continue
			}
			newRepos = append(newRepos, repo)
		}

		if !found {
			return errRepoNotFound
		}

		masterConfig.Plugins.Repositories = newRepos
		return nil
	})
	if ce, ok := asConfigError(err); ok {
		c.JSON(ce.status, gin.H{"error": ce.msg})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to save config"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"success": true})
}

// handlePluginsAPIAvailable fetches available plugins from all enabled repositories
func (s *Server) handlePluginsAPIAvailable(c *gin.Context) {
	masterConfig, _ := config.ReadMasterConfig(s.serverDir)
	found, errors := availablePlugins(c.Request.Context(), masterConfig)

	var allPlugins []gin.H
	for _, a := range found {
		p := a.Meta
		allPlugins = append(allPlugins, gin.H{
			"id":               p.ID,
			"fullId":           a.FullID,
			"namespace":        a.Namespace,
			"name":             p.Name,
			"version":          p.Version,
			"description":      p.Description,
			"author":           p.Author,
			"tags":             p.Tags,
			"gitTag":           p.GitTag,
			"path":             p.Path,
			"repository":       a.Repo.Name,
			"repoUrl":          buildPluginRepoUrl(a.Repo.URL, p.Path, p.GitTag),
			"installed":        a.Installed,
			"installedVersion": a.InstalledVersion,
			"updateAvailable":  a.UpdateAvailable,
			"sourceMoved":      a.SourceMoved,
		})
	}

	c.JSON(http.StatusOK, gin.H{
		"plugins": allPlugins,
		"errors":  errors,
	})
}

// availablePlugin is a plugin of an enabled repository and what is installed of it.
type availablePlugin struct {
	Meta             PluginMetadata
	Repo             config.PluginRepository
	FullID           string // @namespace/id
	Namespace        string
	Installed        bool
	InstalledVersion string
	UpdateAvailable  bool // the registry's version is newer than the installed one
	SourceMoved      bool // the repository the plugin came from has another address now
}

// availablePlugins reads the registries of mc's enabled repositories (the defaults without
// a plugins section); errors are per repository.
func availablePlugins(ctx context.Context, mc *config.VCLUConfig) ([]availablePlugin, []string) {
	var repos []config.PluginRepository
	if mc != nil && mc.Plugins != nil {
		repos = mc.Plugins.Repositories
	} else {
		repos = config.DefaultPluginsConfig().Repositories
	}

	installed := map[string]config.InstalledPlugin{} // full id -> entry
	if mc != nil && mc.Plugins != nil {
		for _, inst := range mc.Plugins.Installed {
			installed[inst.ID] = inst
		}
	}
	var out []availablePlugin
	var errors []string
	for _, repo := range repos {
		if !repo.Enabled {
			continue
		}
		if ctx.Err() != nil {
			errors = append(errors, fmt.Sprintf("%s: %v", repo.Name, ctx.Err()))
			continue
		}
		registry, err := fetchRegistryFromRepoCtx(ctx, repo)
		if err != nil {
			errors = append(errors, fmt.Sprintf("%s: %v", repo.Name, err))
			continue
		}
		namespace := registry.Namespace
		if namespace == "" {
			namespace = plugins.NamespaceFromName(repo.Name)
		}
		for _, p := range registry.Plugins {
			a := availablePlugin{Meta: p, Repo: repo, FullID: fmt.Sprintf("@%s/%s", namespace, p.ID), Namespace: namespace}
			inst, ok := installed[a.FullID]
			a.Installed, a.InstalledVersion = ok, inst.Version
			// another repository listing the same id is not an update of this plugin
			a.UpdateAvailable = ok && plugins.FromRepository(inst, repo) && newerVersion(p.Version, inst.Version)
			a.SourceMoved = ok && plugins.SourceMoved(inst, repo)
			out = append(out, a)
		}
	}
	return out, errors
}

// newerVersion is whether v is newer than installed; versions that do not compare differ.
func newerVersion(v, installed string) bool {
	if cmp, ok := plugins.CompareVersions(v, installed); ok {
		return cmp > 0
	}
	return v != installed
}

// handlePluginsAPIInstalled returns list of installed plugins
func (s *Server) handlePluginsAPIInstalled(c *gin.Context) {
	masterConfig, _ := config.ReadMasterConfig(s.serverDir)

	var installed []config.InstalledPlugin
	if masterConfig != nil && masterConfig.Plugins != nil {
		installed = masterConfig.Plugins.Installed
	}
	c.JSON(http.StatusOK, gin.H{"installed": s.installedPluginsView(installed)})
}

// installedPluginView is an installed plugin as the plugins page and API show it.
type installedPluginView struct {
	config.InstalledPlugin
	NeedsConfig bool `json:"needsConfig"`
	// Permissions the plugin asks for, and of them those the user has not allowed
	// (POST /plugins/:id/permissions)
	RequestedPermissions []string `json:"requestedPermissions,omitempty"`
	PendingPermissions   []string `json:"pendingPermissions,omitempty"`
	// Internet hosts it asks for (["*"]: any) and of them those not allowed yet
	Internet        []string `json:"internet,omitempty"`
	PendingInternet []string `json:"pendingInternet,omitempty"`
	// with udp: the ports it may broadcast to
	BroadcastPorts []int `json:"broadcastPorts,omitempty"`
	// with tcp: the ports it connects to
	TCPPorts []int `json:"tcpPorts,omitempty"`
	// of those, the ports an update added and the user has not allowed
	PendingBroadcastPorts []int `json:"pendingBroadcastPorts,omitempty"`
	PendingTCPPorts       []int `json:"pendingTcpPorts,omitempty"`
	// with lan: the local ports its WebSockets reach, and those not allowed yet
	WebSocketPorts        []int `json:"websocketPorts,omitempty"`
	PendingWebSocketPorts []int `json:"pendingWebsocketPorts,omitempty"`
	// its time on the run loop, and whether it is off for going over its limit
	Usage *pluginUsageView `json:"usage,omitempty"`
}

func (s *Server) installedPluginsView(installed []config.InstalledPlugin) []installedPluginView {
	out := []installedPluginView{}
	loader := plugins.NewLoader(s.serverDir)
	usage := s.pluginUsage()
	for _, p := range installed {
		v := loader.PermissionsView(p)
		view := installedPluginView{p, loader.NeedsConfig(p), v.Requested.Names(), v.Pending.Names(), v.Internet, v.Pending.Internet,
			v.BroadcastPorts, v.TCPPorts, v.Pending.BroadcastPorts, v.Pending.TCPPorts, v.WebSocketPorts, v.Pending.WebSocketPorts, nil}
		if u, ok := usage[p.ID]; ok {
			view.Usage = &u
		}
		out = append(out, view)
	}
	return out
}

// handlePluginsAPIPreview: what a plugin of a repository asks for, read from its plugin.json
// before installing, and every permission there is, for the confirmation in the panel.
func (s *Server) handlePluginsAPIPreview(c *gin.Context) {
	id, repository := c.Query("id"), c.Query("repository")
	masterConfig, _ := config.ReadMasterConfig(s.serverDir)
	repos := config.DefaultPluginsConfig().Repositories
	if masterConfig != nil && masterConfig.Plugins != nil {
		repos = masterConfig.Plugins.Repositories
	}
	var repo *config.PluginRepository
	for i := range repos {
		if repos[i].Name == repository && repos[i].Enabled {
			repo = &repos[i]
			break
		}
	}
	if repo == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Repository not found"})
		return
	}
	loader := plugins.NewLoader(s.serverDir)
	preview, err := loader.PreviewPermissions(*repo, id)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": fmt.Sprintf("Failed to read the plugin: %v", err)})
		return
	}
	// an update to a lower plugin API is refused: the panel says so before installing
	var downgradeFrom int
	if fullID := c.Query("fullId"); fullID != "" && plugins.ValidPluginID(fullID) {
		floor, err := loader.APIFloor(fullID)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		if floor > preview.API {
			downgradeFrom = floor
		}
	}
	// API 1 reaches any internet host outside the grants: the dialog shows it as "*"
	requested, internet := preview.Requested.Names(), []string(preview.Requested.Internet)
	if preview.API < 2 {
		requested, internet = append([]string{"internet"}, requested...), []string{"*"}
	}
	c.JSON(http.StatusOK, gin.H{
		"version":        preview.Version,
		"api":            preview.API,
		"downgradeFrom":  downgradeFrom,
		"requested":      nonNil(requested),
		"internet":       nonNil(internet),
		"broadcastPorts": append([]int{}, preview.BroadcastPorts...),
		"tcpPorts":       append([]int{}, preview.TCPPorts...),
		"websocketPorts": append([]int{}, preview.WebSocketPorts...),
		"all":            plugins.AllPermissions.Names(),
	})
}

// handlePluginsAPIInstall installs a plugin
func (s *Server) handlePluginsAPIInstall(c *gin.Context) {
	var req struct {
		ID         string `json:"id"`         // short id (e.g., "weather")
		FullID     string `json:"fullId"`     // full id (e.g., "@vclu/weather")
		Repository string `json:"repository"` // repository name
		// The permissions the user confirmed (GET /plugins/preview); a new plugin gets only
		// these, the rest waits as on an update. Without the field, what it asks for.
		Accept *[]string `json:"accept"`
		// What the confirmation showed for them; the files may say otherwise
		Seen *struct {
			API            int      `json:"api"`
			Internet       []string `json:"internet"`
			BroadcastPorts []int    `json:"broadcastPorts"`
			TCPPorts       []int    `json:"tcpPorts"`
			WebSocketPorts []int    `json:"websocketPorts"`
		} `json:"seen"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request"})
		return
	}
	// one install at a time, from the checks to the saved entry: another one cannot slip in
	// between with a stale configuration
	defer plugins.LockInstalls()()

	// an unreadable configuration hides where an installed plugin came from and what it may do
	masterConfig, err := config.ReadMasterConfig(s.serverDir)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if masterConfig == nil {
		masterConfig = &config.VCLUConfig{}
	}
	if masterConfig.Plugins == nil {
		masterConfig.Plugins = config.DefaultPluginsConfig()
	}

	// Find repository
	var targetRepo *config.PluginRepository
	for i := range masterConfig.Plugins.Repositories {
		if masterConfig.Plugins.Repositories[i].Name == req.Repository && masterConfig.Plugins.Repositories[i].Enabled {
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
		namespace = plugins.NamespaceFromName(req.Repository)
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

	if !plugins.ValidPluginID(fullId) {
		c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("invalid plugin id %q", fullId)})
		return
	}

	// An installed plugin is updated only from the repository it came from; another one listing
	// the same id is another publisher's plugin
	// two plugins whose wizards would write the same module are not installed together
	var ids []string
	for _, inst := range masterConfig.Plugins.Installed {
		ids = append(ids, inst.ID)
	}
	if other := plugins.ModuleNameTaken(fullId, ids); other != "" {
		c.JSON(http.StatusConflict, gin.H{"error": fmt.Sprintf("%s cannot be installed next to %s: their wizards would write the same module, modules/plugins/%s.lua", fullId, other, plugins.ModuleName(fullId)), "code": "module_name"})
		return
	}

	sourceCheck := plugins.NewLoader(s.serverDir)
	for _, inst := range masterConfig.Plugins.Installed {
		if inst.ID != fullId {
			continue
		}
		if ok, moved := sourceCheck.FromSource(inst, *targetRepo); moved {
			c.JSON(http.StatusConflict, gin.H{"error": fmt.Sprintf("%s was installed from %s at another address; the repository of that name has moved, so it is not updated from there. Uninstall it first to install it from the new address (its settings will have to be entered again)", fullId, inst.Repository), "code": "source_moved"})
			return
		} else if !ok {
			from := inst.Repository
			if from == "" {
				from = "the official repository"
			}
			c.JSON(http.StatusConflict, gin.H{"error": fmt.Sprintf("%s is installed from %s; to install it from %s, uninstall it first (its settings will have to be entered again)", fullId, from, targetRepo.Name)})
			return
		}
	}

	// What the files on disk ask for before an update: an installation from before grants
	// keeps that, and an update asking for more waits for the user
	loader := plugins.NewLoader(s.serverDir)
	previous, previousErr := loader.RequestedPermissions(fullId)
	// Recorded before the download, so a reload meanwhile cannot take the new files' request;
	// without the old files nothing is known and everything waits for the user
	err = config.UpdateMasterConfig(s.serverDir, func(masterConfig *config.VCLUConfig) error {
		if masterConfig.Plugins == nil {
			return nil
		}
		for i := range masterConfig.Plugins.Installed {
			if inst := &masterConfig.Plugins.Installed[i]; inst.ID == fullId && inst.Grants == nil {
				if previousErr == nil {
					inst.Grants = previous.Grants()
				} else {
					inst.Grants = &config.PluginGrants{}
				}
			}
		}
		return nil
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to save config"})
		return
	}

	// The files first: the config then never names a version that is not on disk (#112)
	seenAPI := 0
	if req.Seen != nil {
		seenAPI = req.Seen.API
	}
	if err := loader.DownloadPluginSeen(*targetRepo, fullId, seenAPI); err != nil {
		if errors.Is(err, plugins.ErrAPIDowngrade) {
			c.JSON(http.StatusConflict, gin.H{"error": err.Error(), "code": "api_downgrade"})
			return
		}
		if errors.Is(err, plugins.ErrChangedSincePreview) {
			c.JSON(http.StatusConflict, gin.H{"error": err.Error(), "code": "changed_since_preview"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("Failed to download plugin: %v", err)})
		return
	}
	api, _ := loader.DiskAPI(fullId)
	requested, _ := loader.RequestedPermissions(fullId)
	if req.Accept != nil {
		requested = requested.Only(*req.Accept)
	}
	if req.Seen != nil {
		// hosts and ports not shown wait for the user, as after an update
		requested = requested.OnlyHostsSeen(req.Seen.Internet).
			OnlyPortsSeen(req.Seen.TCPPorts, req.Seen.BroadcastPorts, req.Seen.WebSocketPorts)
	}

	// Fetched without the lock; the install goes into the file as it is now
	action := "installed"
	err = config.UpdateMasterConfig(s.serverDir, func(masterConfig *config.VCLUConfig) error {
		if masterConfig.Plugins == nil {
			masterConfig.Plugins = config.DefaultPluginsConfig()
		}

		// Check if already installed
		for i, inst := range masterConfig.Plugins.Installed {
			if inst.ID == fullId {
				// Update version; the repository is the one the files come from (checked above;
				// an entry from before repositories were recorded gets it now)
				masterConfig.Plugins.Installed[i].Version = pluginMeta.Version
				masterConfig.Plugins.Installed[i].Repository = targetRepo.Name
				masterConfig.Plugins.Installed[i].RepositoryURL = plugins.RepoURL(targetRepo.URL)
				masterConfig.Plugins.Installed[i].InstalledAt = time.Now().Format(time.RFC3339)
				if api > masterConfig.Plugins.Installed[i].API {
					masterConfig.Plugins.Installed[i].API = api
				}
				action = "updated"
				return nil
			}
		}

		// Add to installed
		masterConfig.Plugins.Installed = append(masterConfig.Plugins.Installed, config.InstalledPlugin{
			ID:            fullId,
			Version:       pluginMeta.Version,
			Enabled:       true,
			Repository:    req.Repository,
			RepositoryURL: plugins.RepoURL(targetRepo.URL),
			API:           api,
			InstalledAt:   time.Now().Format(time.RFC3339),
			Config:        make(map[string]interface{}),
			// Installing is allowing what the plugin asks for now
			Grants: requested.Grants(),
		})
		return nil
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to save config"})
		return
	}

	s.resolvePluginNotices()
	c.JSON(http.StatusOK, gin.H{"success": true, "action": action, "fullId": fullId, "version": pluginMeta.Version})
}

// handlePluginsAPIUninstall uninstalls a plugin
func (s *Server) handlePluginsAPIUninstall(c *gin.Context) {
	defer dashCache.Invalidate() // a plugin started or stopped brings or takes its dashboards
	pluginID := c.Param("id")

	// the install lock only for the entry: unloading below waits for the Lua loop, which may
	// be fetching files under it
	unlock := plugins.LockInstalls()
	err := config.UpdateMasterConfig(s.serverDir, func(masterConfig *config.VCLUConfig) error {
		if masterConfig.Plugins == nil {
			return errPluginNotFound
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
			return errPluginNotFound
		}

		masterConfig.Plugins.Installed = newInstalled
		return nil
	})
	unlock()
	if ce, ok := asConfigError(err); ok {
		c.JSON(ce.status, gin.H{"error": ce.msg})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to save config"})
		return
	}

	// its dashboards and actions go with it
	if s.pluginController != nil && s.pluginController.IsLoaded(pluginID) {
		if err := s.pluginController.UnloadPlugin(pluginID); err != nil {
			log.Printf("[PLUGINS] %s: unloading: %v", pluginID, err)
		}
	}
	if err := plugins.DeletePluginSecrets(s.serverDir, pluginID); err != nil {
		log.Printf("[PLUGINS] %s: forgetting secrets: %v", pluginID, err)
	}
	s.resolvePluginNotices()
	c.JSON(http.StatusOK, gin.H{"success": true})
}

// handlePluginsAPIToggle enables/disables a plugin
func (s *Server) handlePluginsAPIToggle(c *gin.Context) {
	defer dashCache.Invalidate() // a plugin started or stopped brings or takes its dashboards
	pluginID := c.Param("id")

	var req struct {
		Enabled bool `json:"enabled"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request"})
		return
	}

	// Find plugin and get its config
	var pluginConfig map[string]interface{}
	err := config.UpdateMasterConfig(s.serverDir, func(masterConfig *config.VCLUConfig) error {
		if masterConfig.Plugins == nil {
			return errPluginNotFound
		}
		for i := range masterConfig.Plugins.Installed {
			if masterConfig.Plugins.Installed[i].ID == pluginID {
				masterConfig.Plugins.Installed[i].Enabled = req.Enabled
				pluginConfig = masterConfig.Plugins.Installed[i].Config
				return nil
			}
		}
		return errPluginNotFound
	})
	if ce, ok := asConfigError(err); ok {
		c.JSON(ce.status, gin.H{"error": ce.msg})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to save config"})
		return
	}
	s.resolvePluginNotices() // a disabled plugin no longer waits for permissions

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

// serialPortPatterns: where serial ports show up on Linux; by-id names survive replugging.
var serialPortPatterns = []string{"/dev/serial/by-id/*", "/dev/ttyUSB*", "/dev/ttyACM*", "/dev/ttyAMA*", "/dev/ttyS0"}

// handlePluginsAPISerialPorts lists the serial ports on this machine, to assign one to a plugin.
func (s *Server) handlePluginsAPISerialPorts(c *gin.Context) {
	ports := []string{}
	for _, pattern := range serialPortPatterns {
		matches, _ := filepath.Glob(pattern)
		ports = append(ports, matches...)
	}
	c.JSON(http.StatusOK, gin.H{"ports": ports})
}

// handlePluginsAPIPermissions sets what the user allows a plugin
// and loads the plugin again with it. The plugin still gets only what its plugin.json asks for.
func (s *Server) handlePluginsAPIPermissions(c *gin.Context) {
	defer dashCache.Invalidate() // a plugin started or stopped brings or takes its dashboards
	pluginID := c.Param("id")

	// Unknown keys refused: {"lan": {"host": [...]}} would otherwise allow any host
	var req config.PluginGrants
	dec := json.NewDecoder(c.Request.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request: " + err.Error()})
		return
	}
	if err := plugins.ValidateGrants(req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	var installed config.InstalledPlugin
	err := config.UpdateMasterConfig(s.serverDir, func(masterConfig *config.VCLUConfig) error {
		if masterConfig.Plugins == nil {
			return errPluginNotFound
		}
		for i := range masterConfig.Plugins.Installed {
			if masterConfig.Plugins.Installed[i].ID == pluginID {
				grants := req
				masterConfig.Plugins.Installed[i].Grants = &grants
				installed = masterConfig.Plugins.Installed[i]
				return nil
			}
		}
		return errPluginNotFound
	})
	if ce, ok := asConfigError(err); ok {
		c.JSON(ce.status, gin.H{"error": ce.msg})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to save config"})
		return
	}
	s.resolvePluginNotices() // the bell's "waiting for permissions"

	if s.pluginController != nil && installed.Enabled && s.pluginController.IsLoaded(pluginID) {
		if err := s.pluginController.UnloadPlugin(pluginID); err != nil {
			c.JSON(http.StatusOK, gin.H{
				"success": true,
				"warning": fmt.Sprintf("Permissions saved but plugin failed to unload: %v", err),
			})
			return
		}
		if err := s.pluginController.LoadPlugin(pluginID, installed.Config); err != nil {
			c.JSON(http.StatusOK, gin.H{
				"success": true,
				"warning": fmt.Sprintf("Permissions saved but plugin failed to load: %v", err),
			})
			return
		}
	}

	v := plugins.NewLoader(s.serverDir).PermissionsView(installed)
	c.JSON(http.StatusOK, gin.H{"success": true, "pendingPermissions": v.Pending.Names(), "pendingInternet": v.Pending.Internet,
		"pendingBroadcastPorts": v.Pending.BroadcastPorts, "pendingTcpPorts": v.Pending.TCPPorts, "pendingWebsocketPorts": v.Pending.WebSocketPorts})
}

// handlePluginsAPIConfig updates plugin configuration
func (s *Server) handlePluginsAPIConfig(c *gin.Context) {
	defer dashCache.Invalidate() // a plugin started or stopped brings or takes its dashboards
	pluginID := c.Param("id")

	var req struct {
		Config map[string]interface{} `json:"config"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request"})
		return
	}

	var isEnabled bool
	err := config.UpdateMasterConfig(s.serverDir, func(masterConfig *config.VCLUConfig) error {
		if masterConfig.Plugins == nil {
			return errPluginNotFound
		}
		for i := range masterConfig.Plugins.Installed {
			if masterConfig.Plugins.Installed[i].ID == pluginID {
				masterConfig.Plugins.Installed[i].Config = req.Config
				isEnabled = masterConfig.Plugins.Installed[i].Enabled
				return nil
			}
		}
		return errPluginNotFound
	})
	if ce, ok := asConfigError(err); ok {
		c.JSON(ce.status, gin.H{"error": ce.msg})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to save config"})
		return
	}

	// If plugin is enabled and has config now, load it (or reload with new config)
	if s.pluginController != nil && len(req.Config) > 0 {
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
	pluginURL := resolver.RawFileURL(repo.URL, gitTag, plugins.RepoFilePath(pluginPath, "plugin.json"))

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
	docURL := resolver.RawFileURL(repo.URL, gitTag, plugins.RepoFilePath(pluginPath, docFile))

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

// maxRegistrySize bounds a repository's plugins.json.
const maxRegistrySize = plugins.MaxIndexSize

// fetchRegistryFromRepo fetches full plugins.json registry from a repository; a
// repository with only a root plugin.json is a registry of one plugin
func fetchRegistryFromRepo(repo config.PluginRepository) (*PluginRegistryResponse, error) {
	return fetchRegistryFromRepoCtx(context.Background(), repo)
}

func fetchRegistryFromRepoCtx(ctx context.Context, repo config.PluginRepository) (*PluginRegistryResponse, error) {
	index, err := plugins.FetchIndexCtx(ctx, &http.Client{Timeout: 10 * time.Second}, repo)
	if err != nil {
		return nil, err
	}
	registry := &PluginRegistryResponse{
		Version:     index.Version,
		Name:        index.Name,
		Namespace:   index.Namespace,
		Description: index.Description,
		URL:         index.URL,
	}
	for _, p := range index.Plugins {
		registry.Plugins = append(registry.Plugins, PluginMetadata{
			ID:          p.ID,
			Name:        p.Name,
			Version:     p.Version,
			Description: p.Description,
			Author:      p.Author,
			Path:        p.Path,
			Tags:        p.Categories,
			GitTag:      p.GitTag,
		})
	}
	return registry, nil
}

// buildPluginRepoUrl builds a URL to the plugin's directory in the repository
func buildPluginRepoUrl(repoUrl, pluginPath, gitTag string) string {
	if pluginPath == "" {
		return repoUrl
	}
	resolver := plugins.DefaultResolver(repoUrl)
	return resolver.BrowseURL(repoUrl, gitTag, pluginPath)
}
