package web

import (
	"net/http"
	"time"

	"github.com/Domktorymysli/vclu/pkg/config"
	"github.com/Domktorymysli/vclu/pkg/lua"
	"github.com/Domktorymysli/vclu/pkg/notify"
	"github.com/gin-gonic/gin"
)

// pluginUsageReader: the engine's plugin time budget (pkg/lua plugin_budget.go).
type pluginUsageReader interface {
	PluginUsage() map[string]lua.PluginUsage
	ResumePlugin(pluginID string)
}

// pluginSuspender: the loader keeps a plugin over its time limit off until Resume.
type pluginSuspender interface {
	IsSuspended(pluginID string) bool
	Resume(pluginID string)
}

// pluginUsageView: how much of the run loop a plugin takes, for its card.
type pluginUsageView struct {
	Suspended bool  `json:"suspended,omitempty"`
	CPUms     int64 `json:"cpuMs"`
	// CPUmsPerMin: on average since it was loaded
	CPUmsPerMin int64 `json:"cpuMsPerMin"`
	Overruns    int   `json:"overruns,omitempty"`
}

func (s *Server) pluginUsage() map[string]pluginUsageView {
	out := map[string]pluginUsageView{}
	r, ok := s.luaExecutor.(pluginUsageReader)
	if !ok {
		return out
	}
	for id, u := range r.PluginUsage() {
		v := pluginUsageView{Suspended: u.Suspended, CPUms: u.CPU.Milliseconds(), Overruns: u.Overruns}
		if mins := time.Since(u.Since).Minutes(); mins >= 1 {
			v.CPUmsPerMin = int64(float64(u.CPU.Milliseconds()) / mins)
		} else {
			v.CPUmsPerMin = u.CPU.Milliseconds()
		}
		out[id] = v
	}
	if sp, ok := s.pluginController.(pluginSuspender); ok {
		for id, v := range out {
			v.Suspended = v.Suspended || sp.IsSuspended(id)
			out[id] = v
		}
	}
	return out
}

// NotifyPluginSuspended: the bell says a plugin went over its time limit too often.
func (s *Server) NotifyPluginSuspended(pluginID string) {
	if s.notifier == nil || !s.notifier.Enabled(notify.SourcePlugin) {
		return
	}
	s.notifier.Add(notify.Entry{
		Kind: notify.KindEvent, Source: notify.SourcePlugin, Level: notify.LevelWarning,
		Key: "plugin-suspended:" + pluginID, Msg: notify.MsgPluginSuspend, Link: "/plugins",
		Params: map[string]string{"plugin": pluginID},
	})
}

// handlePluginsAPIResume turns on a plugin suspended for going over its time limit.
func (s *Server) handlePluginsAPIResume(c *gin.Context) {
	defer dashCache.Invalidate() // a plugin started brings its dashboards
	pluginID := c.Param("id")
	sp, ok := s.pluginController.(pluginSuspender)
	if !ok {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "Plugins not ready"})
		return
	}
	var installed *config.InstalledPlugin
	if mc, err := config.ReadMasterConfig(s.serverDir); err == nil && mc != nil && mc.Plugins != nil {
		for i := range mc.Plugins.Installed {
			if mc.Plugins.Installed[i].ID == pluginID {
				installed = &mc.Plugins.Installed[i]
			}
		}
	}
	if installed == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Plugin not installed"})
		return
	}
	sp.Resume(pluginID)
	if r, ok := s.luaExecutor.(pluginUsageReader); ok {
		r.ResumePlugin(pluginID)
	}
	if installed.Enabled && !s.pluginController.IsLoaded(pluginID) {
		if err := s.pluginController.LoadPlugin(pluginID, installed.Config); err != nil {
			c.JSON(http.StatusOK, gin.H{"success": true, "warning": "Plugin failed to load: " + err.Error()})
			return
		}
	}
	c.JSON(http.StatusOK, gin.H{"success": true})
}
