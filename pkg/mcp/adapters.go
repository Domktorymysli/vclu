package mcp

import (
	"time"

	"github.com/Domktorymysli/vclu/pkg/config"
	"github.com/Domktorymysli/vclu/pkg/stats"
)

// --- ConfigProvider adapter ---

// diskConfigProvider reads MCP config from disk (always from disk, no cache).
type diskConfigProvider struct {
	serverDir string
}

// NewConfigProvider creates a ConfigProvider that reads from disk on every call.
func NewConfigProvider(serverDir string) ConfigProvider {
	return &diskConfigProvider{serverDir: serverDir}
}

func (p *diskConfigProvider) GetMCPBots() []config.MCPBot {
	masterConfig, _ := config.ReadMasterConfig(p.serverDir)
	if masterConfig == nil || masterConfig.MCP == nil {
		return nil
	}
	return masterConfig.MCP.Bots
}

func (p *diskConfigProvider) IsDevModeEnabled() bool {
	masterConfig, _ := config.ReadMasterConfig(p.serverDir)
	if masterConfig == nil || masterConfig.MCP == nil {
		return false
	}
	return masterConfig.MCP.DevModeEnabled
}

func (p *diskConfigProvider) GetInstalledPlugins() []config.InstalledPlugin {
	masterConfig, _ := config.ReadMasterConfig(p.serverDir)
	if masterConfig == nil || masterConfig.Plugins == nil {
		return nil
	}
	return masterConfig.Plugins.Installed
}

// --- MetricsRecorder adapter ---

// metricsAdapter adapts stats.AppMetrics to MetricsRecorder.
type metricsAdapter struct {
	m *stats.AppMetrics
}

// NewMetricsRecorder creates a MetricsRecorder backed by AppMetrics.
func NewMetricsRecorder(m *stats.AppMetrics) MetricsRecorder {
	return &metricsAdapter{m: m}
}

func (a *metricsAdapter) RecordMCPRequest(tool string, hasError bool, latency time.Duration) {
	if a.m != nil {
		a.m.RecordMCPRequest(tool, hasError, latency)
	}
}

func (a *metricsAdapter) IncMCPSessions(delta int64) {
	if a.m != nil {
		a.m.IncMCPSessions(delta)
	}
}

func (a *metricsAdapter) AddMCPActivity(entry ActivityEntry) {
	if a.m != nil {
		a.m.AddMCPActivity(stats.MCPActivityEntry{
			Time:     entry.Time,
			Tool:     entry.Tool,
			Path:     entry.Path,
			Success:  entry.Success,
			Duration: entry.Duration,
			Error:    entry.Error,
			RemoteIP: entry.RemoteIP,
		})
	}
}
