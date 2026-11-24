package web

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/Domktorymysli/vclu/pkg/config"
	"github.com/Domktorymysli/vclu/pkg/plugins"
)

// PluginWizard: the plugin controller's side of the configuration wizard.
type PluginWizard interface {
	Form(pluginID string) (*plugins.Form, json.RawMessage, error)
	Discover(ctx context.Context, pluginID string) ([]map[string]any, []string, error)
	ModuleCode(pluginID string) (string, error)
}

func (s *Server) wizard(c *gin.Context) (PluginWizard, string, bool) {
	w, ok := s.pluginController.(PluginWizard)
	if !ok {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "plugins are not available"})
		return nil, "", false
	}
	id := c.Param("id")
	if !plugins.ValidPluginID(id) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid plugin id"})
		return nil, "", false
	}
	return w, id, true
}

func installedEntry(serverDir, id string) (*config.InstalledPlugin, error) {
	mc, err := config.ReadMasterConfig(serverDir)
	if err != nil || mc == nil || mc.Plugins == nil {
		return nil, errPluginNotFound
	}
	for i := range mc.Plugins.Installed {
		if mc.Plugins.Installed[i].ID == id {
			return &mc.Plugins.Installed[i], nil
		}
	}
	return nil, errPluginNotFound
}

// handlePluginsAPIForm returns the plugin's wizard and its saved values; secrets only as set.
func (s *Server) handlePluginsAPIForm(c *gin.Context) {
	w, id, ok := s.wizard(c)
	if !ok {
		return
	}
	entry, err := installedEntry(s.serverDir, id)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "plugin not installed"})
		return
	}
	form, raw, err := w.Form(id)
	if err != nil {
		// the panel falls back to the simple form and says why
		c.JSON(http.StatusOK, gin.H{"form": nil, "formError": err.Error()})
		return
	}
	if form == nil {
		c.JSON(http.StatusOK, gin.H{"form": nil})
		return
	}
	settings := map[string]any{}
	for k, v := range entry.Config {
		if k != plugins.DevicesKey || form.Devices == nil {
			settings[k] = v
		}
	}
	secrets, _ := plugins.PluginSecrets(s.serverDir, id)
	set := map[string]bool{}
	for name, f := range form.Settings {
		if f.Type == "secret" {
			delete(settings, name)
			set[name] = secrets[name] != ""
		}
	}
	resp := gin.H{
		"form":     raw,
		"settings": settings,
		"secrets":  set,
		"running":  s.pluginController.IsLoaded(id),
		"enabled":  entry.Enabled,
		"module":   "modules/plugins/" + plugins.ModuleName(id) + ".lua",
	}
	if form.Devices != nil {
		devices := entry.Config[plugins.DevicesKey]
		if devices == nil {
			devices = []any{}
		}
		resp["devices"] = devices
		resp["rowKey"] = form.Devices.RowKey()
	}
	c.JSON(http.StatusOK, resp)
}

// handlePluginsAPISaveForm checks and saves a step of the wizard, then restarts the plugin
// with it. Without "devices" the saved rows stay.
func (s *Server) handlePluginsAPISaveForm(c *gin.Context) {
	w, id, ok := s.wizard(c)
	if !ok {
		return
	}
	var req struct {
		Settings map[string]any   `json:"settings"`
		Devices  []map[string]any `json:"devices"`
	}
	dec := json.NewDecoder(c.Request.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
		return
	}
	form, _, err := w.Form(id)
	if err != nil || form == nil {
		c.JSON(http.StatusUnprocessableEntity, gin.H{"error": "the plugin has no form"})
		return
	}
	entry, err := installedEntry(s.serverDir, id)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "plugin not installed"})
		return
	}
	keepDevices := req.Devices == nil
	if req.Settings == nil {
		// devices only: the saved settings stay
		req.Settings = map[string]any{}
		for k, v := range entry.Config {
			if _, declared := form.Settings[k]; declared {
				req.Settings[k] = v
			}
		}
	}
	values, err := form.Check(req.Settings, req.Devices)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if keepDevices && form.Devices != nil {
		if saved, ok := entry.Config[plugins.DevicesKey]; ok {
			values.Config[plugins.DevicesKey] = saved
		} else {
			delete(values.Config, plugins.DevicesKey)
		}
	}
	// keys the form does not declare (set by an agent or an older version) stay
	for k, v := range entry.Config {
		if _, declared := form.Settings[k]; !declared && k != plugins.DevicesKey {
			values.Config[k] = v
		}
	}
	if err := plugins.SetPluginSecrets(s.serverDir, id, values.Secrets); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to save secrets"})
		return
	}
	var enabled bool
	var added []string
	err = config.UpdateMasterConfig(s.serverDir, func(mc *config.VCLUConfig) error {
		if mc.Plugins == nil {
			return errPluginNotFound
		}
		for i := range mc.Plugins.Installed {
			p := &mc.Plugins.Installed[i]
			if p.ID != id {
				continue
			}
			p.Config = values.Config
			enabled = p.Enabled
			// a narrowed lan grant gets the hosts of the form
			if p.Grants != nil && p.Grants.LAN != nil && p.Grants.LAN.Hosts != nil {
				for _, h := range values.Hosts {
					if !slices.Contains(p.Grants.LAN.Hosts, h) {
						p.Grants.LAN.Hosts = append(p.Grants.LAN.Hosts, h)
						added = append(added, h)
					}
				}
			}
			return nil
		}
		return errPluginNotFound
	})
	if ce, ok := asConfigError(err); ok {
		c.JSON(ce.status, gin.H{"error": ce.msg})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to save config"})
		return
	}
	resp := gin.H{"success": true, "hostsAdded": added}
	if enabled {
		if s.pluginController.IsLoaded(id) {
			_ = s.pluginController.UnloadPlugin(id)
		}
		if err := s.pluginController.LoadPlugin(id, values.Config); err != nil {
			resp["warning"] = fmt.Sprintf("saved, but the plugin failed to start: %v", err)
		}
	}
	c.JSON(http.StatusOK, resp)
}

// handlePluginsAPIDiscover runs the plugin's discover with its saved settings.
func (s *Server) handlePluginsAPIDiscover(c *gin.Context) {
	w, id, ok := s.wizard(c)
	if !ok {
		return
	}
	rows, dropped, err := w.Discover(c.Request.Context(), id)
	if err != nil {
		c.JSON(http.StatusUnprocessableEntity, gin.H{"error": err.Error()})
		return
	}
	if rows == nil {
		rows = []map[string]any{}
	}
	c.JSON(http.StatusOK, gin.H{"rows": rows, "dropped": dropped})
}

func codeHash(code string) string {
	sum := sha256.Sum256([]byte(code))
	return hex.EncodeToString(sum[:])
}

// handlePluginsAPIModuleCode returns the module the plugin proposes next to the one on disk.
func (s *Server) handlePluginsAPIModuleCode(c *gin.Context) {
	w, id, ok := s.wizard(c)
	if !ok {
		return
	}
	proposed, err := w.ModuleCode(id)
	if err != nil {
		c.JSON(http.StatusUnprocessableEntity, gin.H{"error": err.Error()})
		return
	}
	current, err := plugins.ReadModuleCode(s.serverDir, id)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to read the module"})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"path":     "modules/plugins/" + plugins.ModuleName(id) + ".lua",
		"current":  current,
		"proposed": plugins.ModuleText(id, proposed),
		"hash":     codeHash(plugins.ModuleText(id, proposed)),
	})
}

// handlePluginsAPIWriteModuleCode writes the proposed module the user saw (by its hash),
// then reloads Lua.
func (s *Server) handlePluginsAPIWriteModuleCode(c *gin.Context) {
	w, id, ok := s.wizard(c)
	if !ok {
		return
	}
	var req struct {
		Hash string `json:"hash"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || req.Hash == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
		return
	}
	proposed, err := w.ModuleCode(id)
	if err != nil {
		c.JSON(http.StatusUnprocessableEntity, gin.H{"error": err.Error()})
		return
	}
	if codeHash(plugins.ModuleText(id, proposed)) != req.Hash {
		c.JSON(http.StatusConflict, gin.H{"error": "the proposed code changed: look at the diff again"})
		return
	}
	if strings.TrimSpace(proposed) == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "the plugin proposes no code"})
		return
	}
	if err := plugins.WriteModuleCode(s.serverDir, id, proposed); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to write the module: " + err.Error()})
		return
	}
	resp := gin.H{"success": true, "path": "modules/plugins/" + plugins.ModuleName(id) + ".lua"}
	if s.luaReloader != nil {
		if err := s.luaReloader.Reload(); err != nil {
			resp["warning"] = "written, but reload failed: " + err.Error()
		}
	}
	c.JSON(http.StatusOK, resp)
}
