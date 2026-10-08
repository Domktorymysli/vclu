package stats

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestNewAppMetrics(t *testing.T) {
	m := NewAppMetrics()
	if m == nil {
		t.Fatal("NewAppMetrics returned nil")
	}
	if m.MemoryUsed == nil || m.CmdRate == nil || m.HTTPRate == nil {
		t.Error("Time series not initialized")
	}
	if m.Uptime() > time.Second {
		t.Error("Fresh metrics should have near-zero uptime")
	}
}

func TestRecordLuaExecution(t *testing.T) {
	m := NewAppMetrics()

	m.RecordLuaExecution(5_000_000, false) // 5ms, no error
	m.RecordLuaExecution(3_000_000, true)  // 3ms, error

	if m.LuaExecutions.Load() != 2 {
		t.Errorf("Expected 2 executions, got %d", m.LuaExecutions.Load())
	}
	if m.LuaErrors.Load() != 1 {
		t.Errorf("Expected 1 error, got %d", m.LuaErrors.Load())
	}
	if m.LuaExecTimeNs.Load() != 8_000_000 {
		t.Errorf("Expected 8ms cumulative, got %d ns", m.LuaExecTimeNs.Load())
	}
	if m.LuaExecCount.Load() != 2 {
		t.Errorf("Expected exec count 2, got %d", m.LuaExecCount.Load())
	}
}

func TestRecordCommand(t *testing.T) {
	m := NewAppMetrics()

	m.RecordCommand(true, false)  // executed, no error
	m.RecordCommand(true, true)   // executed, error
	m.RecordCommand(false, false) // received but not executed

	if m.CmdReceived.Load() != 3 {
		t.Errorf("Expected 3 received, got %d", m.CmdReceived.Load())
	}
	if m.CmdExecuted.Load() != 2 {
		t.Errorf("Expected 2 executed, got %d", m.CmdExecuted.Load())
	}
	if m.CmdErrors.Load() != 1 {
		t.Errorf("Expected 1 error, got %d", m.CmdErrors.Load())
	}
}

func TestRecordHTTPRequest(t *testing.T) {
	m := NewAppMetrics()

	m.RecordHTTPRequest(true, 200)  // API 200
	m.RecordHTTPRequest(false, 200) // Web 200
	m.RecordHTTPRequest(true, 404)  // API 404
	m.RecordHTTPRequest(true, 500)  // API 500
	m.RecordHTTPRequest(false, 503) // Web 503

	if m.HTTPRequests.Load() != 5 {
		t.Errorf("Expected 5 total, got %d", m.HTTPRequests.Load())
	}
	if m.HTTPRequestsAPI.Load() != 3 {
		t.Errorf("Expected 3 API, got %d", m.HTTPRequestsAPI.Load())
	}
	if m.HTTPRequestsWeb.Load() != 2 {
		t.Errorf("Expected 2 Web, got %d", m.HTTPRequestsWeb.Load())
	}
	if m.HTTPErrors4xx.Load() != 1 {
		t.Errorf("Expected 1 4xx error, got %d", m.HTTPErrors4xx.Load())
	}
	if m.HTTPErrors5xx.Load() != 2 {
		t.Errorf("Expected 2 5xx errors, got %d", m.HTTPErrors5xx.Load())
	}
}

func TestRecordMQTT(t *testing.T) {
	m := NewAppMetrics()

	m.SetMQTTConnected(true)
	m.RecordMQTTPublish()
	m.RecordMQTTPublish()
	m.RecordMQTTReceive()
	m.RecordMQTTReconnect()

	if !m.MQTTConnected.Load() {
		t.Error("Expected MQTT connected")
	}
	if m.MQTTPublished.Load() != 2 {
		t.Errorf("Expected 2 published, got %d", m.MQTTPublished.Load())
	}
	if m.MQTTReceived.Load() != 1 {
		t.Errorf("Expected 1 received, got %d", m.MQTTReceived.Load())
	}
	if m.MQTTReconnects.Load() != 1 {
		t.Errorf("Expected 1 reconnect, got %d", m.MQTTReconnects.Load())
	}

	m.SetMQTTConnected(false)
	if m.MQTTConnected.Load() {
		t.Error("Expected MQTT disconnected")
	}
}

func TestRecordHomeKit(t *testing.T) {
	m := NewAppMetrics()

	m.RecordHomeKitRequest(true)  // GET
	m.RecordHomeKitRequest(true)  // GET
	m.RecordHomeKitRequest(false) // SET
	m.RecordHomeKitEvent()
	m.RecordHomeKitEvent()

	if m.HomeKitRequests.Load() != 3 {
		t.Errorf("Expected 3 requests, got %d", m.HomeKitRequests.Load())
	}
	if m.HomeKitGet.Load() != 2 {
		t.Errorf("Expected 2 GET, got %d", m.HomeKitGet.Load())
	}
	if m.HomeKitSet.Load() != 1 {
		t.Errorf("Expected 1 SET, got %d", m.HomeKitSet.Load())
	}
	if m.HomeKitEvents.Load() != 2 {
		t.Errorf("Expected 2 events, got %d", m.HomeKitEvents.Load())
	}
}

func TestRecordMCPRequest_LatencyBuckets(t *testing.T) {
	m := NewAppMetrics()

	m.RecordMCPRequest("vclu_devices", false, 5*time.Millisecond)      // <10ms  → bucket 0
	m.RecordMCPRequest("vclu_device_set", false, 30*time.Millisecond)  // <50ms  → bucket 1
	m.RecordMCPRequest("vclu_exec", false, 80*time.Millisecond)        // <100ms → bucket 2
	m.RecordMCPRequest("vclu_logs", false, 200*time.Millisecond)       // <500ms → bucket 3
	m.RecordMCPRequest("vclu_draft_test", false, 800*time.Millisecond) // <1s  → bucket 4
	m.RecordMCPRequest("vclu_draft_test", true, 2*time.Second)         // >1s   → bucket 5

	if m.MCPRequests.Load() != 6 {
		t.Errorf("Expected 6 MCP requests, got %d", m.MCPRequests.Load())
	}
	if m.MCPErrors.Load() != 1 {
		t.Errorf("Expected 1 MCP error, got %d", m.MCPErrors.Load())
	}

	expected := [6]int64{1, 1, 1, 1, 1, 1}
	for i := 0; i < 6; i++ {
		if m.MCPLatencyBuckets[i].Load() != expected[i] {
			t.Errorf("Bucket %d: expected %d, got %d", i, expected[i], m.MCPLatencyBuckets[i].Load())
		}
	}
}

func TestMCPSessions(t *testing.T) {
	m := NewAppMetrics()

	m.IncMCPSessions(1)
	m.IncMCPSessions(1)
	if m.MCPActiveSessions.Load() != 2 {
		t.Errorf("Expected 2 sessions, got %d", m.MCPActiveSessions.Load())
	}

	m.IncMCPSessions(-1)
	if m.MCPActiveSessions.Load() != 1 {
		t.Errorf("Expected 1 session, got %d", m.MCPActiveSessions.Load())
	}
}

func TestMCPActivity(t *testing.T) {
	m := NewAppMetrics()

	// Add entries
	for i := 0; i < 150; i++ {
		m.AddMCPActivity(MCPActivityEntry{
			Time:    time.Now(),
			Tool:    "vclu_devices",
			Success: true,
		})
	}

	// Should be capped at 100
	all := m.GetMCPActivity(0)
	if len(all) != 100 {
		t.Errorf("Expected 100 entries (capped), got %d", len(all))
	}

	// Get last 10
	last10 := m.GetMCPActivity(10)
	if len(last10) != 10 {
		t.Errorf("Expected 10 entries, got %d", len(last10))
	}
}

func TestSnapshot(t *testing.T) {
	m := NewAppMetrics()

	// Record some data
	m.RecordLuaExecution(1_000_000, false)
	m.RecordCommand(true, false)
	m.RecordHTTPRequest(true, 200)
	m.SetMQTTConnected(true)
	m.RecordMQTTPublish()
	m.RecordHomeKitEvent()
	m.RecordMCPRequest("test", false, 5*time.Millisecond)

	snap := m.Snapshot()

	if snap.Lua.Executions != 1 {
		t.Errorf("Snapshot Lua executions: expected 1, got %d", snap.Lua.Executions)
	}
	if snap.Protocol.CommandsReceived != 1 {
		t.Errorf("Snapshot commands received: expected 1, got %d", snap.Protocol.CommandsReceived)
	}
	if snap.HTTP.Requests != 1 {
		t.Errorf("Snapshot HTTP requests: expected 1, got %d", snap.HTTP.Requests)
	}
	if !snap.MQTT.Connected {
		t.Error("Snapshot MQTT should be connected")
	}
	if snap.MQTT.Published != 1 {
		t.Errorf("Snapshot MQTT published: expected 1, got %d", snap.MQTT.Published)
	}
	if snap.HomeKit.Events != 1 {
		t.Errorf("Snapshot HomeKit events: expected 1, got %d", snap.HomeKit.Events)
	}
	if snap.MCP.Requests != 1 {
		t.Errorf("Snapshot MCP requests: expected 1, got %d", snap.MCP.Requests)
	}
	if snap.System.MemoryMB <= 0 {
		t.Error("Snapshot should have positive memory")
	}
	if snap.System.Goroutines <= 0 {
		t.Error("Snapshot should have positive goroutines")
	}
	if snap.Uptime <= 0 {
		t.Error("Snapshot should have positive uptime")
	}
}

func TestSnapshotJSON(t *testing.T) {
	m := NewAppMetrics()
	m.RecordHTTPRequest(true, 200)

	snap := m.Snapshot()
	data, err := json.Marshal(snap)
	if err != nil {
		t.Fatalf("Failed to marshal snapshot: %v", err)
	}

	// Should contain uptime_str field (custom MarshalJSON)
	if !strings.Contains(string(data), "uptime_str") {
		t.Error("JSON should contain uptime_str field")
	}

	// Should be valid JSON
	var parsed map[string]interface{}
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("Failed to unmarshal snapshot JSON: %v", err)
	}

	// Verify key sections exist
	for _, key := range []string{"system", "lua", "protocol", "mqtt", "http", "homekit", "mcp", "plugins"} {
		if _, ok := parsed[key]; !ok {
			t.Errorf("Missing key %q in snapshot JSON", key)
		}
	}
}

func TestSnapshotWithPluginStats(t *testing.T) {
	m := NewAppMetrics()

	m.SetPluginStatsFunc(func() (*PluginStatsResult, error) {
		return &PluginStatsResult{
			Totals: PluginTotals{
				Count:   2,
				Timers:  3,
				Sensors: 5,
			},
			LuaMemoryKB: 128.5,
			Plugins: map[string]PluginDetailSnapshot{
				"weather": {ID: "weather", Name: "Weather", Version: "1.0"},
				"salda":   {ID: "salda", Name: "Salda", Version: "0.9"},
			},
		}, nil
	})

	snap := m.Snapshot()

	if snap.Plugins.Active != 2 {
		t.Errorf("Expected 2 active plugins, got %d", snap.Plugins.Active)
	}
	if snap.Plugins.Timers != 3 {
		t.Errorf("Expected 3 plugin timers, got %d", snap.Plugins.Timers)
	}
	if snap.Plugins.LuaMemoryKB != 128.5 {
		t.Errorf("Expected 128.5 KB Lua memory, got %f", snap.Plugins.LuaMemoryKB)
	}
	if len(snap.Plugins.Details) != 2 {
		t.Errorf("Expected 2 plugin details, got %d", len(snap.Plugins.Details))
	}
}

func TestGetTimeSeries(t *testing.T) {
	m := NewAppMetrics()
	m.MemoryUsed.Record(10.0)
	m.CmdRate.Record(5.0)

	ts := m.GetTimeSeries()

	expectedKeys := []string{"memory_used", "goroutines", "gc_pause", "lua_exec_time",
		"timers_active", "lua_busy_percent", "lua_loop_lag",
		"cmd_rate", "plugins_active", "http_rate",
		"homekit_accessories", "homekit_paired"}

	for _, key := range expectedKeys {
		if _, ok := ts[key]; !ok {
			t.Errorf("Missing time series key: %s", key)
		}
	}

	if len(ts["memory_used"]) != 1 || ts["memory_used"][0].Value != 10.0 {
		t.Errorf("memory_used: expected [10.0], got %v", ts["memory_used"])
	}
}

func TestPrometheusFormat(t *testing.T) {
	m := NewAppMetrics()

	m.RecordLuaExecution(1_000_000, false)
	m.RecordCommand(true, false)
	m.RecordHTTPRequest(true, 200)
	m.SetMQTTConnected(true)
	m.RecordMCPRequest("test", false, 5*time.Millisecond)

	output := m.PrometheusFormat()

	// Check key metrics are present
	expectedMetrics := []string{
		"vclu_uptime_seconds",
		"vclu_memory_bytes",
		"vclu_goroutines",
		"vclu_lua_executions_total",
		"vclu_commands_received_total",
		"vclu_http_requests_total",
		"vclu_mqtt_connected",
		"vclu_mcp_requests_total",
		"vclu_homekit_requests_total",
	}

	for _, metric := range expectedMetrics {
		if !strings.Contains(output, metric) {
			t.Errorf("Prometheus output missing metric: %s", metric)
		}
	}

	// Check TYPE declarations
	if !strings.Contains(output, "# TYPE vclu_uptime_seconds gauge") {
		t.Error("Missing TYPE declaration for vclu_uptime_seconds")
	}
	if !strings.Contains(output, "# TYPE vclu_lua_executions_total counter") {
		t.Error("Missing TYPE declaration for vclu_lua_executions_total")
	}

	// MQTT connected should be 1
	if !strings.Contains(output, "vclu_mqtt_connected 1") {
		t.Error("vclu_mqtt_connected should be 1")
	}
}

func TestFormatUptime(t *testing.T) {
	tests := []struct {
		d        time.Duration
		expected string
	}{
		{30 * time.Second, "0m 30s"},
		{5*time.Minute + 30*time.Second, "5m 30s"},
		{2*time.Hour + 15*time.Minute, "2h 15m"},
		{25*time.Hour + 30*time.Minute, "1d 1h 30m"},
		{48*time.Hour + 5*time.Minute, "2d 0h 5m"},
	}

	for _, tc := range tests {
		result := formatUptime(tc.d)
		if result != tc.expected {
			t.Errorf("formatUptime(%v): expected %q, got %q", tc.d, tc.expected, result)
		}
	}
}

func TestStartStop(t *testing.T) {
	m := NewAppMetrics()

	m.Start()
	// Double start should be safe
	m.Start()

	time.Sleep(10 * time.Millisecond)

	m.Stop()
	// Double stop should be safe
	m.Stop()
}

func TestCollectSampleWithProviders(t *testing.T) {
	m := NewAppMetrics()

	m.SetTimersActiveFunc(func() int { return 5 })
	m.SetPluginsActiveFunc(func() int { return 3 })
	m.SetHomeKitFuncs(
		func() int { return 10 }, // accessories
		func() int { return 1 },  // paired
	)

	// Record some commands/HTTP for rate calculation
	m.RecordCommand(true, false)
	m.RecordCommand(true, false)
	m.RecordHTTPRequest(true, 200)

	m.collectSample()

	if m.TimersActive.Stats().Latest != 5 {
		t.Errorf("Expected 5 active timers, got %f", m.TimersActive.Stats().Latest)
	}
	if m.PluginsActive.Stats().Latest != 3 {
		t.Errorf("Expected 3 active plugins, got %f", m.PluginsActive.Stats().Latest)
	}
	if m.HomeKitAccessories.Stats().Latest != 10 {
		t.Errorf("Expected 10 HK accessories, got %f", m.HomeKitAccessories.Stats().Latest)
	}
	if m.CmdRate.Stats().Latest != 2 {
		t.Errorf("Expected cmd rate 2, got %f", m.CmdRate.Stats().Latest)
	}
	if m.HTTPRate.Stats().Latest != 1 {
		t.Errorf("Expected http rate 1, got %f", m.HTTPRate.Stats().Latest)
	}
}

func TestRecordPluginHTTP(t *testing.T) {
	m := NewAppMetrics()

	m.RecordPluginHTTP(false)
	m.RecordPluginHTTP(false)
	m.RecordPluginHTTP(true)

	if m.PluginHTTPRequests.Load() != 3 {
		t.Errorf("Expected 3 plugin HTTP requests, got %d", m.PluginHTTPRequests.Load())
	}
	if m.PluginHTTPErrors.Load() != 1 {
		t.Errorf("Expected 1 plugin HTTP error, got %d", m.PluginHTTPErrors.Load())
	}
}

func TestRecordTimerFired(t *testing.T) {
	m := NewAppMetrics()

	m.RecordTimerFired()
	m.RecordTimerFired()
	m.RecordTimerFired()

	if m.TimersFired.Load() != 3 {
		t.Errorf("Expected 3 timers fired, got %d", m.TimersFired.Load())
	}
}
