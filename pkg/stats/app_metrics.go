package stats

import (
	"encoding/json"
	"runtime"
	"strconv"
	"sync"
	"sync/atomic"
	"time"
)

// Configuration for 24h retention at 1 minute intervals
const (
	MetricsSampleInterval = 1 * time.Minute
	MetricsRetention      = 1440 // 24 hours at 1 min intervals
)

// AppMetrics holds all application metrics for vCLU
type AppMetrics struct {
	mu        sync.RWMutex
	startTime time.Time

	// System metrics (time series)
	MemoryUsed *TimeSeries
	Goroutines *TimeSeries
	GCPause    *TimeSeries

	// Lua metrics
	LuaExecutions  atomic.Int64
	LuaErrors      atomic.Int64
	LuaExecTimeNs  atomic.Int64 // cumulative for averaging
	LuaExecCount   atomic.Int64 // count for averaging
	LuaExecTime    *TimeSeries  // avg exec time per sample
	TimersActive   *TimeSeries
	LuaBusyPercent *TimeSeries // runtime busy %
	LuaLoopLag     *TimeSeries // event loop lag ms
	TimersFired    atomic.Int64

	// Protocol metrics (CLU communication)
	CmdReceived atomic.Int64
	CmdExecuted atomic.Int64
	CmdErrors   atomic.Int64
	CmdRate     *TimeSeries // commands per minute

	// Plugin metrics
	PluginsActive      *TimeSeries
	PluginHTTPRequests atomic.Int64
	PluginHTTPErrors   atomic.Int64

	// MQTT metrics
	MQTTConnected  atomic.Bool
	MQTTPublished  atomic.Int64
	MQTTReceived   atomic.Int64
	MQTTReconnects atomic.Int64

	// HTTP Server metrics
	HTTPRequests    atomic.Int64
	HTTPRequestsAPI atomic.Int64
	HTTPRequestsWeb atomic.Int64
	HTTPErrors4xx   atomic.Int64
	HTTPErrors5xx   atomic.Int64
	HTTPRate        *TimeSeries // requests per minute

	// HomeKit metrics
	HomeKitAccessories *TimeSeries
	HomeKitPaired      *TimeSeries
	HomeKitRequests    atomic.Int64
	HomeKitGet         atomic.Int64
	HomeKitSet         atomic.Int64
	HomeKitEvents      atomic.Int64

	// MCP metrics
	MCPRequests       atomic.Int64
	MCPErrors         atomic.Int64
	MCPActiveSessions atomic.Int64
	MCPLatencyBuckets [6]atomic.Int64 // <10ms, <50ms, <100ms, <500ms, <1s, >1s
	mcpActivity       []MCPActivityEntry
	mcpActivityMu     sync.Mutex

	// Internal tracking for rate calculations
	lastCmdReceived  int64
	lastHTTPRequests int64

	// Sampling control
	stopChan chan struct{}
	running  bool

	// External data providers (set by server)
	getTimersActive  func() int
	getPluginsActive func() int
	getPluginStats   func() (*PluginStatsResult, error)
	getHKAccessories func() int
	getHKPaired      func() int
}

// MCPActivityEntry records a single MCP tool call for the activity log.
type MCPActivityEntry struct {
	Time     time.Time `json:"time"`
	Tool     string    `json:"tool"`
	Path     string    `json:"path,omitempty"`
	Success  bool      `json:"success"`
	Duration float64   `json:"duration_ms"`
	Error    string    `json:"error,omitempty"`
	RemoteIP string    `json:"remote_ip,omitempty"`
}

const mcpActivityMaxEntries = 100

// PluginStatsResult represents plugin statistics from Lua
type PluginStatsResult struct {
	Plugins     map[string]PluginDetailSnapshot
	Totals      PluginTotals
	LuaMemoryKB float64
}

// PluginTotals represents totals across all plugins
type PluginTotals struct {
	Count         int
	Timers        int
	Sensors       int
	Controls      int
	Subscriptions int
	Exposed       int
}

// NewAppMetrics creates a new AppMetrics instance
func NewAppMetrics() *AppMetrics {
	return &AppMetrics{
		startTime: time.Now(),

		// System
		MemoryUsed: NewTimeSeries("memory_used", "MB", MetricsRetention, MetricsSampleInterval),
		Goroutines: NewTimeSeries("goroutines", "count", MetricsRetention, MetricsSampleInterval),
		GCPause:    NewTimeSeries("gc_pause", "ms", MetricsRetention, MetricsSampleInterval),

		// Lua
		LuaExecTime:    NewTimeSeries("lua_exec_time", "ms", MetricsRetention, MetricsSampleInterval),
		TimersActive:   NewTimeSeries("timers_active", "count", MetricsRetention, MetricsSampleInterval),
		LuaBusyPercent: NewTimeSeries("lua_busy_percent", "%", MetricsRetention, MetricsSampleInterval),
		LuaLoopLag:     NewTimeSeries("lua_loop_lag", "ms", MetricsRetention, MetricsSampleInterval),

		// Protocol
		CmdRate: NewTimeSeries("cmd_rate", "req/min", MetricsRetention, MetricsSampleInterval),

		// Plugins
		PluginsActive: NewTimeSeries("plugins_active", "count", MetricsRetention, MetricsSampleInterval),

		// HTTP
		HTTPRate: NewTimeSeries("http_rate", "req/min", MetricsRetention, MetricsSampleInterval),

		// HomeKit
		HomeKitAccessories: NewTimeSeries("homekit_accessories", "count", MetricsRetention, MetricsSampleInterval),
		HomeKitPaired:      NewTimeSeries("homekit_paired", "count", MetricsRetention, MetricsSampleInterval),

		stopChan: make(chan struct{}),
	}
}

// SetTimersActiveFunc sets the function to get active timers count
func (m *AppMetrics) SetTimersActiveFunc(fn func() int) {
	m.mu.Lock()
	m.getTimersActive = fn
	m.mu.Unlock()
}

// SetPluginsActiveFunc sets the function to get active plugins count
func (m *AppMetrics) SetPluginsActiveFunc(fn func() int) {
	m.mu.Lock()
	m.getPluginsActive = fn
	m.mu.Unlock()
}

// SetPluginStatsFunc sets the function to get detailed plugin statistics
func (m *AppMetrics) SetPluginStatsFunc(fn func() (*PluginStatsResult, error)) {
	m.mu.Lock()
	m.getPluginStats = fn
	m.mu.Unlock()
}

// SetHomeKitFuncs sets the functions to get HomeKit stats
func (m *AppMetrics) SetHomeKitFuncs(accessories, paired func() int) {
	m.mu.Lock()
	m.getHKAccessories = accessories
	m.getHKPaired = paired
	m.mu.Unlock()
}

// Start begins collecting metrics
func (m *AppMetrics) Start() {
	m.mu.Lock()
	if m.running {
		m.mu.Unlock()
		return
	}
	m.running = true
	m.stopChan = make(chan struct{})
	m.mu.Unlock()

	// Initial sample
	m.collectSample()

	go func() {
		ticker := time.NewTicker(MetricsSampleInterval)
		defer ticker.Stop()

		for {
			select {
			case <-m.stopChan:
				return
			case <-ticker.C:
				m.collectSample()
			}
		}
	}()
}

// Stop stops collecting metrics
func (m *AppMetrics) Stop() {
	m.mu.Lock()
	if !m.running {
		m.mu.Unlock()
		return
	}
	m.running = false
	m.mu.Unlock()

	close(m.stopChan)
}

// collectSample gathers current metrics
func (m *AppMetrics) collectSample() {
	// System metrics
	var memStats runtime.MemStats
	runtime.ReadMemStats(&memStats)

	m.MemoryUsed.Record(float64(memStats.Alloc) / 1024 / 1024)
	m.Goroutines.Record(float64(runtime.NumGoroutine()))

	if memStats.NumGC > 0 {
		lastPauseIdx := (memStats.NumGC + 255) % 256
		m.GCPause.Record(float64(memStats.PauseNs[lastPauseIdx]) / 1e6)
	}

	// Lua exec time average
	count := m.LuaExecCount.Swap(0)
	totalNs := m.LuaExecTimeNs.Swap(0)
	if count > 0 {
		avgMs := float64(totalNs) / float64(count) / 1e6
		m.LuaExecTime.Record(avgMs)
	}

	// Timers active
	m.mu.RLock()
	getTimers := m.getTimersActive
	getPlugins := m.getPluginsActive
	getHKAcc := m.getHKAccessories
	getHKPaired := m.getHKPaired
	m.mu.RUnlock()

	if getTimers != nil {
		m.TimersActive.Record(float64(getTimers()))
	}

	// Plugins active
	if getPlugins != nil {
		m.PluginsActive.Record(float64(getPlugins()))
	}

	// HomeKit
	if getHKAcc != nil {
		m.HomeKitAccessories.Record(float64(getHKAcc()))
	}
	if getHKPaired != nil {
		m.HomeKitPaired.Record(float64(getHKPaired()))
	}

	// Rate calculations (per minute)
	currentCmd := m.CmdReceived.Load()
	cmdDelta := currentCmd - m.lastCmdReceived
	m.lastCmdReceived = currentCmd
	m.CmdRate.Record(float64(cmdDelta))

	currentHTTP := m.HTTPRequests.Load()
	httpDelta := currentHTTP - m.lastHTTPRequests
	m.lastHTTPRequests = currentHTTP
	m.HTTPRate.Record(float64(httpDelta))
}

// Uptime returns how long the app has been running
func (m *AppMetrics) Uptime() time.Duration {
	return time.Since(m.startTime)
}

// =============================================================================
// Instrumentation methods (call these from application code)
// =============================================================================

// RecordLuaExecution records a Lua script execution
func (m *AppMetrics) RecordLuaExecution(durationNs int64, hasError bool) {
	m.LuaExecutions.Add(1)
	m.LuaExecTimeNs.Add(durationNs)
	m.LuaExecCount.Add(1)
	if hasError {
		m.LuaErrors.Add(1)
	}
}

// RecordLuaBusy records Lua runtime busy percent and loop lag.
func (m *AppMetrics) RecordLuaBusy(busyPercent, loopLagMs float64) {
	m.LuaBusyPercent.Record(busyPercent)
	m.LuaLoopLag.Record(loopLagMs)
}

// RecordTimerFired records a timer firing
func (m *AppMetrics) RecordTimerFired() {
	m.TimersFired.Add(1)
}

// RecordCommand records a CLU command
func (m *AppMetrics) RecordCommand(executed bool, hasError bool) {
	m.CmdReceived.Add(1)
	if executed {
		m.CmdExecuted.Add(1)
	}
	if hasError {
		m.CmdErrors.Add(1)
	}
}

// RecordPluginHTTP records a plugin HTTP request
func (m *AppMetrics) RecordPluginHTTP(hasError bool) {
	m.PluginHTTPRequests.Add(1)
	if hasError {
		m.PluginHTTPErrors.Add(1)
	}
}

// RecordMQTTPublish records an MQTT publish
func (m *AppMetrics) RecordMQTTPublish() {
	m.MQTTPublished.Add(1)
}

// RecordMQTTReceive records an MQTT message received
func (m *AppMetrics) RecordMQTTReceive() {
	m.MQTTReceived.Add(1)
}

// RecordMQTTReconnect records an MQTT reconnection
func (m *AppMetrics) RecordMQTTReconnect() {
	m.MQTTReconnects.Add(1)
}

// SetMQTTConnected sets MQTT connection state
func (m *AppMetrics) SetMQTTConnected(connected bool) {
	m.MQTTConnected.Store(connected)
}

// RecordHTTPRequest records an HTTP request
func (m *AppMetrics) RecordHTTPRequest(isAPI bool, statusCode int) {
	m.HTTPRequests.Add(1)
	if isAPI {
		m.HTTPRequestsAPI.Add(1)
	} else {
		m.HTTPRequestsWeb.Add(1)
	}
	if statusCode >= 400 && statusCode < 500 {
		m.HTTPErrors4xx.Add(1)
	} else if statusCode >= 500 {
		m.HTTPErrors5xx.Add(1)
	}
}

// RecordHomeKitRequest records a HomeKit request
func (m *AppMetrics) RecordHomeKitRequest(isGet bool) {
	m.HomeKitRequests.Add(1)
	if isGet {
		m.HomeKitGet.Add(1)
	} else {
		m.HomeKitSet.Add(1)
	}
}

// RecordHomeKitEvent records a HomeKit event sent
func (m *AppMetrics) RecordHomeKitEvent() {
	m.HomeKitEvents.Add(1)
}

// RecordMCPRequest records an MCP tool call with latency tracking.
func (m *AppMetrics) RecordMCPRequest(tool string, hasError bool, latency time.Duration) {
	m.MCPRequests.Add(1)
	if hasError {
		m.MCPErrors.Add(1)
	}
	// Bucket latency
	ms := latency.Milliseconds()
	switch {
	case ms < 10:
		m.MCPLatencyBuckets[0].Add(1)
	case ms < 50:
		m.MCPLatencyBuckets[1].Add(1)
	case ms < 100:
		m.MCPLatencyBuckets[2].Add(1)
	case ms < 500:
		m.MCPLatencyBuckets[3].Add(1)
	case ms < 1000:
		m.MCPLatencyBuckets[4].Add(1)
	default:
		m.MCPLatencyBuckets[5].Add(1)
	}
}

// IncMCPSessions adjusts the active MCP sessions counter.
func (m *AppMetrics) IncMCPSessions(delta int64) {
	m.MCPActiveSessions.Add(delta)
}

// AddMCPActivity adds an entry to the MCP activity ring buffer.
func (m *AppMetrics) AddMCPActivity(entry MCPActivityEntry) {
	m.mcpActivityMu.Lock()
	defer m.mcpActivityMu.Unlock()
	m.mcpActivity = append(m.mcpActivity, entry)
	if len(m.mcpActivity) > mcpActivityMaxEntries {
		m.mcpActivity = m.mcpActivity[len(m.mcpActivity)-mcpActivityMaxEntries:]
	}
}

// GetMCPActivity returns the recent MCP activity entries.
func (m *AppMetrics) GetMCPActivity(limit int) []MCPActivityEntry {
	m.mcpActivityMu.Lock()
	defer m.mcpActivityMu.Unlock()
	if limit <= 0 || limit > len(m.mcpActivity) {
		limit = len(m.mcpActivity)
	}
	// Return most recent entries (last N)
	start := len(m.mcpActivity) - limit
	result := make([]MCPActivityEntry, limit)
	copy(result, m.mcpActivity[start:])
	return result
}

// =============================================================================
// Snapshot for API
// =============================================================================

// Snapshot returns current metrics snapshot for API
func (m *AppMetrics) Snapshot() AppMetricsSnapshot {
	var memStats runtime.MemStats
	runtime.ReadMemStats(&memStats)

	// Build plugins snapshot
	pluginsSnap := PluginsSnapshot{
		Active:       int(m.PluginsActive.Stats().Latest),
		HTTPRequests: m.PluginHTTPRequests.Load(),
		HTTPErrors:   m.PluginHTTPErrors.Load(),
	}

	// Get detailed plugin stats if available
	m.mu.RLock()
	getPluginStats := m.getPluginStats
	m.mu.RUnlock()

	if getPluginStats != nil {
		if stats, err := getPluginStats(); err == nil && stats != nil {
			pluginsSnap.Active = stats.Totals.Count
			pluginsSnap.Timers = stats.Totals.Timers
			pluginsSnap.Sensors = stats.Totals.Sensors
			pluginsSnap.Controls = stats.Totals.Controls
			pluginsSnap.Subscriptions = stats.Totals.Subscriptions
			pluginsSnap.Exposed = stats.Totals.Exposed
			pluginsSnap.LuaMemoryKB = stats.LuaMemoryKB

			// Convert plugin details
			if len(stats.Plugins) > 0 {
				pluginsSnap.Details = make([]PluginDetailSnapshot, 0, len(stats.Plugins))
				for _, p := range stats.Plugins {
					pluginsSnap.Details = append(pluginsSnap.Details, p)
				}
			}
		}
	}

	return AppMetricsSnapshot{
		Timestamp: time.Now(),
		Uptime:    m.Uptime(),

		System: SystemSnapshot{
			MemoryMB:   float64(memStats.Alloc) / 1024 / 1024,
			Goroutines: runtime.NumGoroutine(),
			GCRuns:     int(memStats.NumGC),
		},

		Lua: LuaSnapshot{
			Executions:   m.LuaExecutions.Load(),
			Errors:       m.LuaErrors.Load(),
			TimersActive: int(m.TimersActive.Stats().Latest),
			TimersFired:  m.TimersFired.Load(),
		},

		Protocol: ProtocolSnapshot{
			CommandsReceived: m.CmdReceived.Load(),
			CommandsExecuted: m.CmdExecuted.Load(),
			Errors:           m.CmdErrors.Load(),
		},

		Plugins: pluginsSnap,

		MQTT: MQTTSnapshot{
			Connected:  m.MQTTConnected.Load(),
			Published:  m.MQTTPublished.Load(),
			Received:   m.MQTTReceived.Load(),
			Reconnects: m.MQTTReconnects.Load(),
		},

		HTTP: HTTPSnapshot{
			Requests:    m.HTTPRequests.Load(),
			APIRequests: m.HTTPRequestsAPI.Load(),
			WebRequests: m.HTTPRequestsWeb.Load(),
			Errors4xx:   m.HTTPErrors4xx.Load(),
			Errors5xx:   m.HTTPErrors5xx.Load(),
		},

		HomeKit: HomeKitSnapshot{
			Accessories: int(m.HomeKitAccessories.Stats().Latest),
			Paired:      int(m.HomeKitPaired.Stats().Latest),
			Requests:    m.HomeKitRequests.Load(),
			Get:         m.HomeKitGet.Load(),
			Set:         m.HomeKitSet.Load(),
			Events:      m.HomeKitEvents.Load(),
		},

		MCP: MCPSnapshot{
			Requests:       m.MCPRequests.Load(),
			Errors:         m.MCPErrors.Load(),
			ActiveSessions: m.MCPActiveSessions.Load(),
			LatencyBuckets: [6]int64{
				m.MCPLatencyBuckets[0].Load(),
				m.MCPLatencyBuckets[1].Load(),
				m.MCPLatencyBuckets[2].Load(),
				m.MCPLatencyBuckets[3].Load(),
				m.MCPLatencyBuckets[4].Load(),
				m.MCPLatencyBuckets[5].Load(),
			},
		},
	}
}

// GetTimeSeries returns all time series data for charts
func (m *AppMetrics) GetTimeSeries() map[string][]DataPoint {
	return map[string][]DataPoint{
		"memory_used":         m.MemoryUsed.GetAll(),
		"goroutines":          m.Goroutines.GetAll(),
		"gc_pause":            m.GCPause.GetAll(),
		"lua_exec_time":       m.LuaExecTime.GetAll(),
		"timers_active":       m.TimersActive.GetAll(),
		"lua_busy_percent":    m.LuaBusyPercent.GetAll(),
		"lua_loop_lag":        m.LuaLoopLag.GetAll(),
		"cmd_rate":            m.CmdRate.GetAll(),
		"plugins_active":      m.PluginsActive.GetAll(),
		"http_rate":           m.HTTPRate.GetAll(),
		"homekit_accessories": m.HomeKitAccessories.GetAll(),
		"homekit_paired":      m.HomeKitPaired.GetAll(),
	}
}

// GetTimeSeriesSince returns time series data since given time
func (m *AppMetrics) GetTimeSeriesSince(since time.Time) map[string][]DataPoint {
	return map[string][]DataPoint{
		"memory_used":         m.MemoryUsed.GetSince(since),
		"goroutines":          m.Goroutines.GetSince(since),
		"gc_pause":            m.GCPause.GetSince(since),
		"lua_exec_time":       m.LuaExecTime.GetSince(since),
		"timers_active":       m.TimersActive.GetSince(since),
		"lua_busy_percent":    m.LuaBusyPercent.GetSince(since),
		"lua_loop_lag":        m.LuaLoopLag.GetSince(since),
		"cmd_rate":            m.CmdRate.GetSince(since),
		"plugins_active":      m.PluginsActive.GetSince(since),
		"http_rate":           m.HTTPRate.GetAll(),
		"homekit_accessories": m.HomeKitAccessories.GetSince(since),
		"homekit_paired":      m.HomeKitPaired.GetSince(since),
	}
}

// =============================================================================
// Snapshot types
// =============================================================================

type AppMetricsSnapshot struct {
	Timestamp time.Time     `json:"timestamp"`
	Uptime    time.Duration `json:"uptime"`

	System   SystemSnapshot   `json:"system"`
	Lua      LuaSnapshot      `json:"lua"`
	Protocol ProtocolSnapshot `json:"protocol"`
	Plugins  PluginsSnapshot  `json:"plugins"`
	MQTT     MQTTSnapshot     `json:"mqtt"`
	HTTP     HTTPSnapshot     `json:"http"`
	HomeKit  HomeKitSnapshot  `json:"homekit"`
	MCP      MCPSnapshot      `json:"mcp"`
}

func (s AppMetricsSnapshot) MarshalJSON() ([]byte, error) {
	type Alias AppMetricsSnapshot
	return json.Marshal(&struct {
		Alias
		UptimeStr string `json:"uptime_str"`
	}{
		Alias:     Alias(s),
		UptimeStr: formatUptime(s.Uptime),
	})
}

type SystemSnapshot struct {
	MemoryMB   float64 `json:"memory_mb"`
	Goroutines int     `json:"goroutines"`
	GCRuns     int     `json:"gc_runs"`
}

type LuaSnapshot struct {
	Executions   int64   `json:"executions"`
	Errors       int64   `json:"errors"`
	TimersActive int     `json:"timers_active"`
	TimersFired  int64   `json:"timers_fired"`
	BusyPercent  float64 `json:"busy_percent,omitempty"`
	LoopLagMs    float64 `json:"loop_lag_ms,omitempty"`
}

type ProtocolSnapshot struct {
	Running          bool  `json:"running"`
	CommandsReceived int64 `json:"commands_received"`
	CommandsExecuted int64 `json:"commands_executed"`
	Errors           int64 `json:"errors"`
}

type PluginsSnapshot struct {
	Active        int                    `json:"active"`
	HTTPRequests  int64                  `json:"http_requests"`
	HTTPErrors    int64                  `json:"http_errors"`
	Timers        int                    `json:"timers"`
	Sensors       int                    `json:"sensors"`
	Controls      int                    `json:"controls"`
	Subscriptions int                    `json:"subscriptions"`
	Exposed       int                    `json:"exposed"`
	LuaMemoryKB   float64                `json:"lua_memory_kb"`
	Details       []PluginDetailSnapshot `json:"details,omitempty"`
}

type PluginDetailSnapshot struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	Version       string `json:"version"`
	Timers        int    `json:"timers"`
	Sensors       int    `json:"sensors"`
	Controls      int    `json:"controls"`
	Subscriptions int    `json:"subscriptions"`
	Exposed       int    `json:"exposed"`
}

type MQTTSnapshot struct {
	Connected  bool  `json:"connected"`
	Published  int64 `json:"published"`
	Received   int64 `json:"received"`
	Reconnects int64 `json:"reconnects"`
}

type HTTPSnapshot struct {
	Requests    int64 `json:"requests"`
	APIRequests int64 `json:"api_requests"`
	WebRequests int64 `json:"web_requests"`
	Errors4xx   int64 `json:"errors_4xx"`
	Errors5xx   int64 `json:"errors_5xx"`
}

type HomeKitSnapshot struct {
	Running     bool  `json:"running"`
	Accessories int   `json:"accessories"`
	Paired      int   `json:"paired"`
	Requests    int64 `json:"requests"`
	Get         int64 `json:"get"`
	Set         int64 `json:"set"`
	Events      int64 `json:"events"`
}

type MCPSnapshot struct {
	Requests       int64    `json:"requests"`
	Errors         int64    `json:"errors"`
	ActiveSessions int64    `json:"active_sessions"`
	LatencyBuckets [6]int64 `json:"latency_buckets"` // <10ms, <50ms, <100ms, <500ms, <1s, >1s
}

func formatUptime(d time.Duration) string {
	days := int(d.Hours()) / 24
	hours := int(d.Hours()) % 24
	minutes := int(d.Minutes()) % 60

	if days > 0 {
		return sprintf("%dd %dh %dm", days, hours, minutes)
	}
	if hours > 0 {
		return sprintf("%dh %dm", hours, minutes)
	}
	return sprintf("%dm %ds", minutes, int(d.Seconds())%60)
}

func sprintf(format string, args ...interface{}) string {
	// Simple sprintf without fmt import
	result := format
	for _, arg := range args {
		switch v := arg.(type) {
		case int:
			result = replaceFirst(result, "%d", strconv.Itoa(v))
		case int64:
			result = replaceFirst(result, "%d", strconv.Itoa(int(v)))
		}
	}
	return result
}

func replaceFirst(s, old, new string) string {
	for i := 0; i <= len(s)-len(old); i++ {
		if s[i:i+len(old)] == old {
			return s[:i] + new + s[i+len(old):]
		}
	}
	return s
}

// =============================================================================
// Prometheus Format Export
// =============================================================================

// PrometheusFormat returns metrics in Prometheus text format
func (m *AppMetrics) PrometheusFormat() string {
	var memStats runtime.MemStats
	runtime.ReadMemStats(&memStats)

	var b []byte

	// Helper to add metric
	addGauge := func(name, help string, value float64, labels ...string) {
		b = append(b, "# HELP "...)
		b = append(b, name...)
		b = append(b, ' ')
		b = append(b, help...)
		b = append(b, '\n')
		b = append(b, "# TYPE "...)
		b = append(b, name...)
		b = append(b, " gauge\n"...)
		b = append(b, name...)
		if len(labels) > 0 {
			b = append(b, '{')
			for i := 0; i < len(labels); i += 2 {
				if i > 0 {
					b = append(b, ',')
				}
				b = append(b, labels[i]...)
				b = append(b, '=', '"')
				b = append(b, labels[i+1]...)
				b = append(b, '"')
			}
			b = append(b, '}')
		}
		b = append(b, ' ')
		b = strconv.AppendFloat(b, value, 'f', 2, 64)
		b = append(b, '\n', '\n')
	}

	addCounter := func(name, help string, value int64, labels ...string) {
		b = append(b, "# HELP "...)
		b = append(b, name...)
		b = append(b, ' ')
		b = append(b, help...)
		b = append(b, '\n')
		b = append(b, "# TYPE "...)
		b = append(b, name...)
		b = append(b, " counter\n"...)
		b = append(b, name...)
		if len(labels) > 0 {
			b = append(b, '{')
			for i := 0; i < len(labels); i += 2 {
				if i > 0 {
					b = append(b, ',')
				}
				b = append(b, labels[i]...)
				b = append(b, '=', '"')
				b = append(b, labels[i+1]...)
				b = append(b, '"')
			}
			b = append(b, '}')
		}
		b = append(b, ' ')
		b = strconv.AppendInt(b, value, 10)
		b = append(b, '\n', '\n')
	}

	// System metrics
	addGauge("vclu_uptime_seconds", "Time since vCLU started in seconds", m.Uptime().Seconds())
	addGauge("vclu_memory_bytes", "Current memory usage in bytes", float64(memStats.Alloc))
	addGauge("vclu_memory_mb", "Current memory usage in megabytes", float64(memStats.Alloc)/1024/1024)
	addGauge("vclu_goroutines", "Current number of goroutines", float64(runtime.NumGoroutine()))
	addCounter("vclu_gc_runs_total", "Total number of GC cycles", int64(memStats.NumGC))

	// Lua metrics
	addCounter("vclu_lua_executions_total", "Total Lua script executions", m.LuaExecutions.Load())
	addCounter("vclu_lua_errors_total", "Total Lua execution errors", m.LuaErrors.Load())
	addGauge("vclu_lua_timers_active", "Currently active Lua timers", m.TimersActive.Stats().Latest)
	addCounter("vclu_lua_timers_fired_total", "Total timers fired", m.TimersFired.Load())

	// Protocol metrics
	addCounter("vclu_commands_received_total", "Total CLU commands received", m.CmdReceived.Load())
	addCounter("vclu_commands_executed_total", "Total CLU commands executed", m.CmdExecuted.Load())
	addCounter("vclu_commands_errors_total", "Total CLU command errors", m.CmdErrors.Load())
	addGauge("vclu_commands_rate", "Commands per minute", m.CmdRate.Stats().Latest)

	// Plugin metrics
	addGauge("vclu_plugins_active", "Number of active plugins", m.PluginsActive.Stats().Latest)
	addCounter("vclu_plugin_http_requests_total", "Total plugin HTTP requests", m.PluginHTTPRequests.Load())
	addCounter("vclu_plugin_http_errors_total", "Total plugin HTTP errors", m.PluginHTTPErrors.Load())

	// MQTT metrics
	mqttConnected := 0.0
	if m.MQTTConnected.Load() {
		mqttConnected = 1.0
	}
	addGauge("vclu_mqtt_connected", "MQTT connection status (1=connected, 0=disconnected)", mqttConnected)
	addCounter("vclu_mqtt_published_total", "Total MQTT messages published", m.MQTTPublished.Load())
	addCounter("vclu_mqtt_received_total", "Total MQTT messages received", m.MQTTReceived.Load())
	addCounter("vclu_mqtt_reconnects_total", "Total MQTT reconnections", m.MQTTReconnects.Load())

	// HTTP metrics
	addCounter("vclu_http_requests_total", "Total HTTP requests", m.HTTPRequests.Load())
	addCounter("vclu_http_requests_api_total", "Total API HTTP requests", m.HTTPRequestsAPI.Load())
	addCounter("vclu_http_requests_web_total", "Total Web HTTP requests", m.HTTPRequestsWeb.Load())
	addCounter("vclu_http_errors_4xx_total", "Total HTTP 4xx errors", m.HTTPErrors4xx.Load())
	addCounter("vclu_http_errors_5xx_total", "Total HTTP 5xx errors", m.HTTPErrors5xx.Load())
	addGauge("vclu_http_requests_rate", "HTTP requests per minute", m.HTTPRate.Stats().Latest)

	// HomeKit metrics
	addGauge("vclu_homekit_accessories", "Number of HomeKit accessories", m.HomeKitAccessories.Stats().Latest)
	addGauge("vclu_homekit_paired", "HomeKit pairing status (1=paired, 0=not paired)", m.HomeKitPaired.Stats().Latest)
	addCounter("vclu_homekit_requests_total", "Total HomeKit requests", m.HomeKitRequests.Load())
	addCounter("vclu_homekit_get_total", "Total HomeKit GET requests", m.HomeKitGet.Load())
	addCounter("vclu_homekit_set_total", "Total HomeKit SET requests", m.HomeKitSet.Load())
	addCounter("vclu_homekit_events_total", "Total HomeKit events sent", m.HomeKitEvents.Load())

	// MCP metrics
	addCounter("vclu_mcp_requests_total", "Total MCP tool calls", m.MCPRequests.Load())
	addCounter("vclu_mcp_errors_total", "Total MCP errors", m.MCPErrors.Load())
	addGauge("vclu_mcp_active_sessions", "Currently in-flight MCP requests", float64(m.MCPActiveSessions.Load()))

	return string(b)
}
