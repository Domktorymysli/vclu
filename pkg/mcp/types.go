package mcp

import (
	"context"
	"fmt"
	"time"

	"github.com/Domktorymysli/vclu/pkg/accesscontrol"
	"github.com/Domktorymysli/vclu/pkg/config"
	"github.com/Domktorymysli/vclu/pkg/logstream"
	"github.com/Domktorymysli/vclu/pkg/lua"
)

// LuaExecutor provides Lua script execution on the live runtime.
type LuaExecutor interface {
	Execute(script string) (string, error)
	ExecuteWithTimeout(script string, timeout time.Duration) (string, error)
}

// DeviceProvider is the safe interface for accessing devices.
// MCP Layer 1 has NO access to raw Lua execution.
type DeviceProvider interface {
	ListDevices(ctx context.Context, filterJSON string) ([]Device, error)
	GetDevice(ctx context.Context, path string) (*DeviceDetail, error)
	SetValue(ctx context.Context, path string, value any) (*SetResult, error)
	ExecuteMethod(ctx context.Context, path, method string, args []any) (*ExecResult, error)
	ListScenes(ctx context.Context) ([]Scene, error)
	ExecuteScene(ctx context.Context, path string) error
}

// AccessChecker provides access control checks for the MCP integration.
type AccessChecker interface {
	CanRead(integration accesscontrol.Integration, path string) bool
	CanControl(integration accesscontrol.Integration, path string) bool
	ResolveAccess(profileNames []string, path string) accesscontrol.AccessLevel
}

// ConfigProvider reads MCP configuration (always from disk, no cache).
type ConfigProvider interface {
	GetMCPBots() []config.MCPBot
	IsDevModeEnabled() bool
	GetInstalledPlugins() []config.InstalledPlugin
}

// MetricsRecorder records MCP-specific metrics.
type MetricsRecorder interface {
	RecordMCPRequest(tool string, hasError bool, latency time.Duration)
	IncMCPSessions(delta int64)
	AddMCPActivity(entry ActivityEntry)
}

// LogProvider provides access to runtime logs.
type LogProvider interface {
	GetHistory() []logstream.LogEntry
}

// Device represents an exposed device summary.
type Device struct {
	Path        string   `json:"path"`
	Type        string   `json:"type"`
	Name        string   `json:"name"`
	State       any      `json:"state"`
	Readonly    bool     `json:"readonly"`
	Methods     []string `json:"methods"`
	LastChanged *int64   `json:"lastChanged,omitempty"`
	Area        string   `json:"area,omitempty"`
	Group       string   `json:"group,omitempty"`
	Tags        []string `json:"tags,omitempty"`
	Description string   `json:"description,omitempty"`
}

// DeviceDetail represents a device with full details.
type DeviceDetail struct {
	Path        string         `json:"path"`
	Type        string         `json:"type"`
	Name        string         `json:"name"`
	State       any            `json:"state"`
	Readonly    bool           `json:"readonly"`
	Methods     []string       `json:"methods"`
	Events      []string       `json:"events"`
	Caps        map[string]any `json:"caps"`
	LastChanged *int64         `json:"lastChanged,omitempty"`
	Area        string         `json:"area,omitempty"`
	Group       string         `json:"group,omitempty"`
	Tags        []string       `json:"tags,omitempty"`
	Description string         `json:"description,omitempty"`
}

// SetResult represents the result of setting a device value.
type SetResult struct {
	OK       bool `json:"ok"`
	NewState any  `json:"new_state"`
}

// ExecResult represents the result of executing a method.
type ExecResult struct {
	OK     bool `json:"ok"`
	Result any  `json:"result"`
}

// Scene represents an exposed scene.
type Scene struct {
	Path string `json:"path"`
	Name string `json:"name"`
}

// ActivityEntry records a single MCP tool call for the activity log.
type ActivityEntry struct {
	Time     time.Time `json:"time"`
	Tool     string    `json:"tool"`
	Path     string    `json:"path,omitempty"`
	Success  bool      `json:"success"`
	Duration float64   `json:"duration_ms"`
	Error    string    `json:"error,omitempty"`
	RemoteIP string    `json:"remote_ip,omitempty"`
	BotName  string    `json:"bot_name,omitempty"`
}

// --- Phase 3: Sandbox types ---

// ValidationReport is the result of draft Lua syntax + blocklist validation.
type ValidationReport struct {
	Valid    bool     `json:"valid"`
	Errors   []string `json:"errors"`
	Warnings []string `json:"warnings"`
}

// SandboxResult is the outcome of running a draft in the sandbox.
type SandboxResult struct {
	Env          string        `json:"env"` // always "sandbox"
	Success      bool          `json:"success"`
	StateChanges []StateChange `json:"state_changes"`
	BlockedCalls []BlockedCall `json:"blocked_calls"`
	CluCalls     []CluCall     `json:"clu_calls"`
	// CluCallsDropped counts calls past the clu_calls limit; expect_clu_calls fails then
	CluCallsDropped int      `json:"clu_calls_dropped,omitempty"`
	Logs            []string `json:"logs"`
	Errors          []string `json:"errors"`
	DurationMs      int64    `json:"duration_ms"`
}

// StateChange records a device state change in the sandbox.
type StateChange struct {
	Path     string `json:"path"`
	OldValue any    `json:"old_value"`
	NewValue any    `json:"new_value"`
}

// CluCall is a command a draft sent to a CLU, recorded instead of sent.
// Script is the exact transport script, e.g. "DOU4992:execute(1, 60000)".
type CluCall struct {
	Target string `json:"target"`
	Script string `json:"script"`
}

// BlockedCall records a side-effect call that was blocked in sandbox.
type BlockedCall struct {
	Type string         `json:"type"` // "mqtt.publish", "gpio.write", "http.request", "homekit.notify"
	Args map[string]any `json:"args"`
}

// InjectEvent is an event to inject into the sandbox EventBus.
type InjectEvent struct {
	Name string         `json:"name"` // event name: "state_changed", "mqtt.message", etc.
	Data map[string]any `json:"data"` // event payload
}

// SandboxRunner executes drafts in an isolated Lua sandbox.
type SandboxRunner interface {
	RunDraft(draftPaths []string, injectEvents []InjectEvent) (*SandboxResult, error)
}

// LuaReloader reloads the Lua runtime (used after promote).
type LuaReloader interface {
	Reload() error
}

// ModuleLoadReporter exposes the outcome of the last user-module load.
// Optionally implemented by the reloader: a Reload() that returns nil still
// leaves the runtime broken if modules/init.lua failed to load, so reload and
// promote type-assert this to report per-module failures to the caller.
type ModuleLoadReporter interface {
	LastModuleLoad() []lua.ModuleLoadResult
}

// moduleLoadFailures returns the failed entries from the reloader's last module
// load, or nil when the reloader does not report module status.
func moduleLoadFailures(reloader LuaReloader) []lua.ModuleLoadResult {
	reporter, ok := reloader.(ModuleLoadReporter)
	if !ok {
		return nil
	}
	var failed []lua.ModuleLoadResult
	for _, r := range reporter.LastModuleLoad() {
		if !r.OK {
			failed = append(failed, r)
		}
	}
	return failed
}

// moduleLoadSummary renders failures as short "name: error" strings for messages.
func moduleLoadSummary(failed []lua.ModuleLoadResult) []string {
	out := make([]string, 0, len(failed))
	for _, f := range failed {
		out = append(out, fmt.Sprintf("modules/%s: %s", f.Name, f.Error))
	}
	return out
}

// AuditEntry records a Layer 2 operation for the audit trail.
type AuditEntry struct {
	Time    time.Time `json:"time"`
	Tool    string    `json:"tool"`
	Path    string    `json:"path,omitempty"`
	Paths   []string  `json:"paths,omitempty"` // for multi-file operations
	Env     string    `json:"env"`             // "workspace", "sandbox", "runtime"
	Success bool      `json:"success"`
	Details string    `json:"details,omitempty"`
	BotName string    `json:"bot_name,omitempty"`
	Actor   string    `json:"actor,omitempty"`   // "human" | bot name
	Session string    `json:"session,omitempty"` // MCP session ID (groups related ops)
}

// PromoteResult is the detailed result of a promote operation.
type PromoteResult struct {
	OK       bool     `json:"ok"`
	Paths    []string `json:"paths"`
	Backups  []string `json:"backups,omitempty"`
	Warnings []string `json:"warnings,omitempty"`
	Incident string   `json:"incident,omitempty"` // resource with logs and diff of a failed promote
	// ModuleErrors lists user modules that failed to load during the post-promote
	// reload and are attributable to THIS promote. Non-empty means the promoted
	// code is on disk but NOT running, and drives OK=false.
	ModuleErrors []string `json:"module_errors,omitempty"`
	// PreexistingErrors lists modules that were already failing with the same
	// error before this promote and were not part of it. Reported so nothing is
	// hidden, but they do not make this promote a failure.
	PreexistingErrors []string `json:"preexisting_module_errors,omitempty"`
}
