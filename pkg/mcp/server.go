package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"sync/atomic"
	"time"

	"github.com/Domktorymysli/vclu/pkg/broker"
	"github.com/Domktorymysli/vclu/pkg/config"
	"github.com/Domktorymysli/vclu/pkg/history"
	"github.com/Domktorymysli/vclu/pkg/lua"
	"github.com/Domktorymysli/vclu/pkg/metrics"
	"github.com/Domktorymysli/vclu/pkg/workspace"
	mcpserver "github.com/mark3labs/mcp-go/server"
)

const mcpInstructionsBase = `vCLU Smart Home Controller.

Control layer: Use vclu_devices to list available devices, then vclu_device_set or vclu_device_execute to control them. Use vclu_scenes to list scenes and vclu_scene_execute to run them.

Dev Mode tools (vclu_read_file, vclu_list_files, vclu_grep, vclu_logs, vclu_config, vclu_registry): explore code, search files, read logs, and inspect the full device registry. These require Dev Mode to be enabled in the dashboard. Start with vclu_registry() to discover devices, then vclu_read_file("ai/house.md") for house documentation. vclu_read_file takes offset/limit (lines) to read part of a file. vclu_logs leaves out DEBUG lines: pass level "debug" to see a module's Logger:debug.

Sandbox pipeline (vclu_draft_write, vclu_draft_edit, vclu_draft_read, vclu_draft_list, vclu_draft_delete, vclu_draft_validate, vclu_draft_test, vclu_draft_promote): write code to modules/*.lua drafts (vclu_draft_edit changes an existing draft or production module by replacing a string, without sending the whole file), validate syntax, test in an isolated sandbox (no real side effects), then promote to production with an automatic backup (promote reports modules that failed to load; there is no automatic rollback). Drafts go to modules/ directory only — never modify user.lua or om.lua (OM-generated). Test with vclu_draft_test to see state_changes, blocked_calls and clu_calls before promoting; for automations pass at/steps (set, advance, expect_clu_calls...) to check timing logic on a virtual clock.

Agent memory: Use vclu_context_save to persist notes between sessions. Read vclu://context/agent-notes at start. Code patterns: vclu_read_file("ai/examples.md").

Runtime schema: Use vclu_runtime_schema to get object types, dashboard widget API, plugin methods, and installed plugins with their sensors/controls. Call this when writing modules or dashboards. Libraries shipped with vCLU are required, never copied into modules/: require("lib/panel") draws a Grenton Smart Panel (see smartPanel in the schema and ai/examples.md).

Reload: Use vclu_reload to reload the Lua runtime after manual file changes (draft_promote auto-reloads).

Live execution: Use vclu_exec to run Lua snippets on the live runtime (requires ExecMode on bot). Use vclu_dashboards to list registered dashboards; vclu_dashboards(id) gives one dashboard's widgets. Use vclu_calendar for the house calendar (what is today or tomorrow, schedules and their next change) instead of vclu_exec. Use vclu_plugin_config to read plugin configuration (secrets redacted). Use vclu_device_history to see recent state changes. Use vclu_metrics to list registered metrics or query time-series data for charts (temperature, humidity, fan speed, etc.).

MQTT discovery: Use vclu_mqtt_discover to scan MQTT brokers for smart home devices (Zigbee2MQTT, Tasmota, ESPHome, Shelly, HA Discovery). Returns discovered devices with suggested Lua integration snippets. Use the sandbox pipeline (draft_write → draft_test → draft_promote) to deploy the generated code.

The "description" field on devices contains rules and constraints — always read it.

IMPORTANT — common pitfalls when writing Lua modules:

1. Load order: modules/init.lua first (with the subdirectory modules it requires), then the other flat modules/*.lua files alphabetically; a file init.lua requires runs once. If your code depends on objects created by another module (e.g., _registry["tasmota:xxx"]), wrap it in EventBus:getShared():setTimeout(fn, 5000) to delay execution. Never use "if not obj then return end" as a guard — it silently fails.

2. Dashboard API: use dash:section("Title", "icon") for interactive widgets (toggle, slider, button). dash:header() is ONLY for compact status indicators in the top bar. Finish with dash:build(). For dashboards that depend on plugins or late-loading objects, use setTimeout.

3. Where code goes: in modules, not loose files. Read modules/init.lua first and follow the layout already there. A domain (a plugin, a device, a room) is a directory modules/<domain>/init.lua that holds its readings, metrics, exports and automations and returns a table. Its dashboard goes in modules/dashboards/<domain>.lua and reads from require("<domain>"), not from the plugin directly, so the dashboard and every export show the same values. Both need a require() in modules/init.lua: require("<domain>") loads modules/<domain>/init.lua, require("dashboards/<domain>") the dashboard. Subdirectory files without a matching require() do NOT load. A flat modules/*.lua is auto-loaded, but keep it for a short standalone script; never put modules/<name>.lua next to modules/<name>/, because require("<name>") then loads the flat file.

4. Never modify a/user.lua or a/om.lua — these are OM-generated and will be overwritten.

5. Before writing a dashboard, READ an existing one with vclu_read_file to learn the patterns. Call vclu_dashboards to see what's already registered, and vclu_dashboards with an id for that dashboard's widgets.

6. Read vclu://docs/examples resource for complete code cookbook with all widget types, Virtual* objects, MQTT patterns, and dashboard examples.`

// MQTTCredentialProvider provides access to MQTT client credentials for discovery.
type MQTTCredentialProvider interface {
	GetClientCredentials(instanceID string) *lua.MQTTClientCredentials
	Stats() lua.MQTTStats
}

// MCPServer wraps the mcp-go server with vCLU-specific logic.
type MCPServer struct {
	server           *mcpserver.MCPServer
	handler          *mcpserver.StreamableHTTPServer
	devices          DeviceProvider
	access           AccessChecker
	config           ConfigProvider
	metrics          MetricsRecorder
	files            *FileProvider
	logs             LogProvider
	workspace        *workspace.Workspace
	sandbox          *SandboxEngine
	reloader         LuaReloader
	executor         LuaExecutor
	execLimiter      *rateLimiter
	audit            *AuditLog
	incidents        *IncidentStore
	agentCtx         *AgentContext
	metricsCollector *metrics.Collector
	historyStore     *history.Store
	broker           *broker.Broker
	mqttCreds        MQTTCredentialProvider
	mqttCache        mqttDiscoveryCache
	sandboxLimiter   *rateLimiter
	promoteLimiter   *rateLimiter
	enabled          atomic.Bool
}

// NewMCPServer creates a new MCP server.
// The server always exists — enabled flag gates requests at runtime.
func NewMCPServer(devices DeviceProvider, access AccessChecker, cfg ConfigProvider, metrics MetricsRecorder) *MCPServer {
	s := &MCPServer{
		devices:        devices,
		access:         access,
		config:         cfg,
		metrics:        metrics,
		audit:          NewAuditLog(""),
		mqttCache:      mqttDiscoveryCache{ttl: 10 * time.Second},
		sandboxLimiter: newRateLimiter(time.Minute, 30),
		promoteLimiter: newRateLimiter(time.Hour, promotesPerHour),
		execLimiter:    newRateLimiter(time.Minute, 20),
	}

	// Create mcp-go server
	s.server = mcpserver.NewMCPServer(
		"vCLU Smart Home",
		"1.0.0",
		mcpserver.WithToolCapabilities(false),
		mcpserver.WithResourceCapabilities(true, false),
		mcpserver.WithRecovery(),
		mcpserver.WithInstructions(mcpInstructionsBase),
	)

	// Register tools, resources, and prompts
	s.registerTools()
	s.registerDevTools()
	s.registerSandboxTools()
	s.registerResources()
	s.registerPrompts()

	// Create HTTP handler with auth context injection
	s.handler = mcpserver.NewStreamableHTTPServer(s.server,
		mcpserver.WithStateLess(true),
		mcpserver.WithHTTPContextFunc(func(ctx context.Context, r *http.Request) context.Context {
			// Inject remote IP for activity logging
			ip := r.RemoteAddr
			if fwd := r.Header.Get("X-Forwarded-For"); fwd != "" {
				ip = fwd
			}
			ctx = context.WithValue(ctx, remoteIPKey, ip)

			// Inject bot from auth (already validated in Handler pipeline)
			if bot, ok := ctx.Value(botKey).(*config.MCPBot); ok {
				return context.WithValue(ctx, botKey, bot)
			}
			return ctx
		}),
	)

	return s
}

// SetEnabled sets the runtime enabled state.
func (s *MCPServer) SetEnabled(v bool) {
	s.enabled.Store(v)
	if v {
		log.Printf("[MCP] Server enabled")
	} else {
		log.Printf("[MCP] Server disabled")
	}
}

// IsEnabled returns whether MCP is currently enabled.
func (s *MCPServer) IsEnabled() bool {
	return s.enabled.Load()
}

// isDevModeEnabled reads config from disk on every call — runtime kill-switch.
func (s *MCPServer) isDevModeEnabled() bool {
	return s.config.IsDevModeEnabled()
}

// SetFileProvider sets the file provider for Layer 2 read tools.
func (s *MCPServer) SetFileProvider(fp *FileProvider) {
	s.files = fp
}

// SetLogProvider sets the log provider for Layer 2 log tools.
func (s *MCPServer) SetLogProvider(lp LogProvider) {
	s.logs = lp
}

// SetLuaExecutor sets the Lua executor for vclu_exec and other runtime tools.
func (s *MCPServer) SetLuaExecutor(e LuaExecutor) {
	s.executor = e
}

// SetHistoryStore sets the store read by vclu_device_history.
func (s *MCPServer) SetHistoryStore(store *history.Store) {
	s.historyStore = store
}

// SetMetricsCollector sets the metrics collector for vclu_metrics tool.
func (s *MCPServer) SetMetricsCollector(mc *metrics.Collector) {
	s.metricsCollector = mc
}

// SetBroker sets the embedded MQTT broker for discovery.
func (s *MCPServer) SetBroker(b *broker.Broker) {
	s.broker = b
}

// SetMQTTCredentialProvider sets the MQTT credential provider for external broker discovery.
func (s *MCPServer) SetMQTTCredentialProvider(p MQTTCredentialProvider) {
	s.mqttCreds = p
}

// GetAuditLog returns the audit log (for dashboard API).
func (s *MCPServer) GetAuditLog() *AuditLog {
	return s.audit
}

// GetWorkspace returns the workspace (for dashboard API).
func (s *MCPServer) GetWorkspace() *workspace.Workspace {
	return s.workspace
}

// RunSandboxTest runs draft(s) in the sandbox and returns the result.
// This is the public API for the web editor (no MCP context needed).
func (s *MCPServer) RunSandboxTest(paths []string, events []InjectEvent) (*SandboxResult, error) {
	if s.sandbox == nil {
		return nil, fmt.Errorf("sandbox not configured")
	}
	return s.sandbox.RunDraft(paths, events)
}

// ValidateDraftFile validates a draft file from the workspace.
func (s *MCPServer) ValidateDraftFile(path string) (*ValidationReport, error) {
	if s.workspace == nil {
		return nil, fmt.Errorf("workspace not configured")
	}
	content, err := s.workspace.GetDraft(path)
	if err != nil {
		return nil, err
	}
	// Skip Lua validation for non-Lua files
	if !strings.HasSuffix(path, ".lua") {
		return &ValidationReport{Valid: true}, nil
	}
	return ValidateDraft(content), nil
}

// GetIncidents returns the incident store (for dashboard API).
func (s *MCPServer) GetIncidents() *IncidentStore {
	return s.incidents
}

// GetAgentContext returns the agent context (for dashboard API).
func (s *MCPServer) GetAgentContext() *AgentContext {
	return s.agentCtx
}

// Handler returns the HTTP handler for the /mcp endpoint.
// Pipeline: enabledCheck → auth (with bot injection) → mcp-go handler
func (s *MCPServer) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 1. Enabled check
		if !s.enabled.Load() {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusServiceUnavailable)
			json.NewEncoder(w).Encode(map[string]string{
				"error": "MCP server is disabled",
			})
			return
		}

		// 2. Auth check (Bearer token) — returns matching bot or nil
		bot := authenticateRequest(r, s.config)
		if bot == nil {
			authHeader := r.Header.Get("Authorization")
			log.Printf("[MCP] Auth failed: method=%s, authHeader=%q, bots=%d",
				r.Method, authHeader, len(s.config.GetMCPBots()))
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("WWW-Authenticate", `Bearer realm="vclu-mcp"`)
			w.WriteHeader(http.StatusUnauthorized)
			json.NewEncoder(w).Encode(map[string]string{
				"error": "unauthorized: invalid or missing Bearer token",
			})
			return
		}

		// 3. Inject bot + session ID into request context
		ctx := context.WithValue(r.Context(), botKey, bot)
		sessionID := fmt.Sprintf("%s-%d", bot.Name, time.Now().UnixMilli())
		ctx = context.WithValue(ctx, sessionKey, sessionID)
		r = r.WithContext(ctx)

		// 4. Track in-flight requests
		s.metrics.IncMCPSessions(1)
		defer s.metrics.IncMCPSessions(-1)

		// 5. Delegate to mcp-go
		s.handler.ServeHTTP(w, r)
	})
}

// botActor is the actor of changes made by the request's bot.
func botActor(ctx context.Context) string {
	if bot := botFromContext(ctx); bot != nil && bot.ID != "" {
		return "agent:" + bot.ID
	}
	return "agent:unknown"
}

// botFromContext extracts the authenticated bot from context.
func botFromContext(ctx context.Context) *config.MCPBot {
	if bot, ok := ctx.Value(botKey).(*config.MCPBot); ok {
		return bot
	}
	return nil
}

// sessionFromContext extracts the MCP session ID from context.
func sessionFromContext(ctx context.Context) string {
	if s, ok := ctx.Value(sessionKey).(string); ok {
		return s
	}
	return ""
}

// AddAudit records an audit entry (public wrapper for web handlers).
func (s *MCPServer) AddAudit(entry AuditEntry) {
	s.audit.Add(entry)
}
