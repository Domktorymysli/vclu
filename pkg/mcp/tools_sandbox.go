package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/Domktorymysli/vclu/pkg/workspace"
	mcplib "github.com/mark3labs/mcp-go/mcp"
)

// registerSandboxTools registers Layer 2 sandbox/draft pipeline tools.
// Tools are always registered — devMode gate is checked per request.
func (s *MCPServer) registerSandboxTools() {
	s.addTool("vclu_draft_write", "Write or update a draft file in the workspace (modules/*.lua only)", s.handleDraftWrite,
		map[string]any{
			"path": map[string]any{
				"type":        "string",
				"description": "Relative file path (e.g., modules/init.lua, modules/scenes/movie.lua, modules/custom.css)",
			},
			"content": map[string]any{
				"type":        "string",
				"description": "Lua source code content",
			},
		}, []string{"path", "content"})

	s.addTool("vclu_draft_edit", "Edit a draft by replacing an exact string (modules/*.lua only); without a draft it starts from the production file. "+
		"old_string must occur once, or pass replace_all. Answers with the size, not the file", s.handleDraftEdit,
		map[string]any{
			"path": map[string]any{
				"type":        "string",
				"description": "Relative file path (e.g., modules/init.lua)",
			},
			"old_string": map[string]any{
				"type":        "string",
				"description": "Exact text to replace, with enough context to be unique",
			},
			"new_string": map[string]any{
				"type":        "string",
				"description": "Text to put in its place",
			},
			"replace_all": map[string]any{
				"type":        "boolean",
				"description": "Replace every occurrence (default: false)",
			},
		}, []string{"path", "old_string", "new_string"})

	s.addTool("vclu_draft_read", "Read a draft file from the workspace", s.handleDraftRead,
		map[string]any{
			"path": map[string]any{
				"type":        "string",
				"description": "Relative file path (e.g., modules/init.lua)",
			},
		}, []string{"path"})

	s.addTool("vclu_draft_list", "List all draft files in the workspace", s.handleDraftList, nil, nil)

	s.addTool("vclu_draft_delete", "Delete a draft file from the workspace", s.handleDraftDelete,
		map[string]any{
			"path": map[string]any{
				"type":        "string",
				"description": "Relative file path to delete",
			},
		}, []string{"path"})

	s.addTool("vclu_draft_validate", "Validate a draft file (Lua syntax + blocklist check)", s.handleDraftValidate,
		map[string]any{
			"path": map[string]any{
				"type":        "string",
				"description": "Draft file path to validate",
			},
		}, []string{"path"})

	s.addTool("vclu_draft_test", "Run drafts in the sandbox (isolated Lua engine, no real side effects). "+
		"Time is virtual and stands still; timers fire only on an advance step. CLU commands are recorded in clu_calls, not sent. "+
		"CLU objects come from imports/proxy_*.lua; integration objects (v1: Satel zones) from the live runtime or fixtures. "+
		"Failed expect_* steps are listed in errors and make success false.", s.handleDraftTest,
		map[string]any{
			"path": map[string]any{
				"description": "Draft file path (string) or array of paths (for multi-file test)",
			},
			"inject_events": map[string]any{
				"type":        "array",
				"description": "Events to inject into the sandbox EventBus after loading the draft",
				"items": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"name": map[string]any{
							"type":        "string",
							"description": "Event name (e.g., state_changed, mqtt.message)",
						},
						"data": map[string]any{
							"type":        "object",
							"description": "Event payload",
						},
					},
				},
			},
			"at": map[string]any{
				"type":        "string",
				"description": "Virtual start time, RFC 3339 (e.g. 2026-10-04T23:00:00+02:00). Default: now",
			},
			"timezone": map[string]any{
				"type":        "string",
				"description": "Zone for datetime (e.g. Europe/Warsaw). Default: the zone set in the live runtime by modules/init.lua",
			},
			"steps": map[string]any{
				"type": "array",
				"description": "Scenario steps run after the drafts load, each with exactly one operation: " +
					`{"set": path or name, "value": v} (same path as a CLU report: _:sync), {"emit": event, "data": {...}}, ` +
					`{"advance": seconds}, {"click": panel or button id, "input": "btn1"} (a Smart Panel input as the CLU sends it: ` +
					"btn1..btn4, btn1:long, swipe:up; the frame drawn shows in clu_calls after an advance), " +
					`{"mqtt": topic, "payload": "ON"} (a message as the broker delivers it, to the subscriptions of every MQTT client; ` +
					"what the module publishes shows in blocked_calls), " +
					`{"reload": true} (new Lua state; clock and KV kept, timers dropped, ` +
					"integration objects back to the starting snapshot), " +
					`{"expect_state": {path: value}}, {"expect_clu_calls": [{"script": "DOU4992:execute(1, 60000)", "target": "192.168.0.5"}]} ` +
					"(exact list of CLU calls since the previous expect_clu_calls; target optional), " +
					`{"expect_history": {"path": p, "changes": [{"old": 0, "value": 1}]}} (every recorded change of p in the scenario, oldest first; ` +
					"history survives reload; a first reading is not a change). " +
					"The CLU itself is not simulated: switchOn(60000) does not switch off after a minute, set the report with a step",
				"items": map[string]any{"type": "object"},
			},
			"fixtures": map[string]any{
				"type": "array",
				"description": "Integration objects instead of the live snapshot, for reproducible tests: " +
					`[{"path": "satel.zone.2", "kind": "satel_zone", "state": 0, "config": {"number": 2}}]`,
				"items": map[string]any{"type": "object"},
			},
			"verbose": map[string]any{
				"type":        "boolean",
				"description": "Also return the runtime's start-up lines in logs (default: only what the drafts print, warnings and errors)",
			},
		}, []string{"path"})

	s.addTool("vclu_draft_promote", "Promote drafts to production (backup + replace + reload). Omit path or pass \"all\" to promote all drafts atomically. Files of one change (a module, its dashboard, the require in modules/init.lua) go in one call: one reload, and one of the promotes allowed per hour.", s.handleDraftPromote,
		map[string]any{
			"path": map[string]any{
				"description": "Draft file path (string), array of paths, or \"all\" to promote everything. Omit to promote all drafts.",
			},
		}, nil)

	s.addTool("vclu_diff", "Show unified diff between draft and production file", s.handleDiff,
		map[string]any{
			"path": map[string]any{
				"type":        "string",
				"description": "Draft file path to diff against production",
			},
		}, []string{"path"})

	s.addTool("vclu_audit_log", "Get recent Layer 2 audit log entries", s.handleAuditLog,
		map[string]any{
			"limit": map[string]any{
				"type":        "number",
				"description": "Number of entries to return (default: 50, max: 200)",
			},
		}, nil)

	// Agent context tools
	s.registerContextTools()
}

// --- parsePaths extracts paths from a string or array argument ---

func parsePaths(arg any) ([]string, error) {
	switch v := arg.(type) {
	case string:
		if v == "" {
			return nil, fmt.Errorf("path is required")
		}
		return []string{v}, nil
	case []any:
		if len(v) == 0 {
			return nil, fmt.Errorf("path array is empty")
		}
		paths := make([]string, 0, len(v))
		for _, item := range v {
			s, ok := item.(string)
			if !ok || s == "" {
				return nil, fmt.Errorf("invalid path in array: %v", item)
			}
			paths = append(paths, s)
		}
		return paths, nil
	default:
		return nil, fmt.Errorf("path must be a string or array of strings")
	}
}

// --- Draft Tool Handlers ---

func (s *MCPServer) handleDraftWrite(ctx context.Context, req mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
	start := time.Now()
	if err := s.requireDevMode(ctx); err != nil {
		return err, nil
	}
	if s.workspace == nil {
		return toolError("workspace not configured"), nil
	}

	args := req.GetArguments()
	path, _ := args["path"].(string)
	content, _ := args["content"].(string)

	if path == "" || content == "" {
		return toolError("path and content are required"), nil
	}

	if err := s.workspace.CreateDraft(path, content); err != nil {
		s.recordActivity("vclu_draft_write", path, false, start, err.Error(), ctx)
		return toolError(err.Error()), nil
	}

	s.addAudit(ctx, "draft_write", path, nil, "workspace", true, fmt.Sprintf("%d bytes", len(content)))
	s.recordActivity("vclu_draft_write", path, true, start, "", ctx)
	return toolJSON(map[string]any{
		"env":  "workspace",
		"ok":   true,
		"path": path,
		"size": len(content),
	})
}

func (s *MCPServer) handleDraftEdit(ctx context.Context, req mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
	start := time.Now()
	if err := s.requireDevMode(ctx); err != nil {
		return err, nil
	}
	if s.workspace == nil {
		return toolError("workspace not configured"), nil
	}

	args := req.GetArguments()
	path, _ := args["path"].(string)
	oldStr, _ := args["old_string"].(string)
	newStr, _ := args["new_string"].(string)
	replaceAll, _ := args["replace_all"].(bool)
	fail := func(msg string) (*mcplib.CallToolResult, error) {
		s.recordActivity("vclu_draft_edit", path, false, start, msg, ctx)
		return toolError(msg), nil
	}
	if err := workspace.ValidatePath(path); err != nil {
		return fail(err.Error())
	}
	if oldStr == "" {
		return fail("old_string is required")
	}
	if oldStr == newStr {
		return fail("old_string and new_string are the same")
	}

	content, err := s.workspace.GetDraft(path)
	if err != nil {
		if _, statErr := os.Stat(s.workspace.GetDraftFullPath(path)); !os.IsNotExist(statErr) {
			return fail(err.Error())
		}
		// GetProductionContent gives "" for a missing file
		if content, err = s.workspace.GetProductionContent(path); err != nil {
			return fail(err.Error())
		}
		if content == "" {
			return fail(fmt.Sprintf("no draft or production file %s: create it with vclu_draft_write", path))
		}
	}

	n := strings.Count(content, oldStr)
	switch {
	case n == 0:
		return fail(fmt.Sprintf("old_string not found in %s", path))
	case n > 1 && !replaceAll:
		return fail(fmt.Sprintf("old_string occurs %d times in %s: add context to make it unique, or pass replace_all", n, path))
	}
	if !replaceAll {
		n = 1
	}
	content = strings.Replace(content, oldStr, newStr, n)

	if err := s.workspace.CreateDraft(path, content); err != nil {
		return fail(err.Error())
	}

	s.addAudit(ctx, "draft_edit", path, nil, "workspace", true, fmt.Sprintf("%d replacements, %d bytes", n, len(content)))
	s.recordActivity("vclu_draft_edit", path, true, start, "", ctx)
	return toolJSON(map[string]any{
		"env":          "workspace",
		"ok":           true,
		"path":         path,
		"size":         len(content),
		"replacements": n,
	})
}

func (s *MCPServer) handleDraftRead(ctx context.Context, req mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
	start := time.Now()
	if err := s.requireDevMode(ctx); err != nil {
		return err, nil
	}
	if s.workspace == nil {
		return toolError("workspace not configured"), nil
	}

	path, _ := req.GetArguments()["path"].(string)
	if path == "" {
		return toolError("path is required"), nil
	}

	content, err := s.workspace.GetDraft(path)
	if err != nil {
		s.recordActivity("vclu_draft_read", path, false, start, err.Error(), ctx)
		return toolError(err.Error()), nil
	}

	s.recordActivity("vclu_draft_read", path, true, start, "", ctx)
	return toolJSON(map[string]any{
		"env":     "workspace",
		"path":    path,
		"content": content,
	})
}

func (s *MCPServer) handleDraftList(ctx context.Context, _ mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
	start := time.Now()
	if err := s.requireDevMode(ctx); err != nil {
		return err, nil
	}
	if s.workspace == nil {
		return toolError("workspace not configured"), nil
	}

	drafts, err := s.workspace.ListDrafts()
	if err != nil {
		s.recordActivity("vclu_draft_list", "", false, start, err.Error(), ctx)
		return toolError(err.Error()), nil
	}

	s.recordActivity("vclu_draft_list", "", true, start, "", ctx)
	return toolJSON(map[string]any{
		"env":    "workspace",
		"drafts": drafts,
		"count":  len(drafts),
	})
}

func (s *MCPServer) handleDraftDelete(ctx context.Context, req mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
	start := time.Now()
	if err := s.requireDevMode(ctx); err != nil {
		return err, nil
	}
	if s.workspace == nil {
		return toolError("workspace not configured"), nil
	}

	path, _ := req.GetArguments()["path"].(string)
	if path == "" {
		return toolError("path is required"), nil
	}

	if err := s.workspace.DeleteDraft(path); err != nil {
		s.recordActivity("vclu_draft_delete", path, false, start, err.Error(), ctx)
		return toolError(err.Error()), nil
	}

	s.addAudit(ctx, "draft_delete", path, nil, "workspace", true, "")
	s.recordActivity("vclu_draft_delete", path, true, start, "", ctx)
	return toolJSON(map[string]any{
		"env":  "workspace",
		"ok":   true,
		"path": path,
	})
}

func (s *MCPServer) handleDraftValidate(ctx context.Context, req mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
	start := time.Now()
	if err := s.requireDevMode(ctx); err != nil {
		return err, nil
	}
	if s.workspace == nil {
		return toolError("workspace not configured"), nil
	}

	path, _ := req.GetArguments()["path"].(string)
	if path == "" {
		return toolError("path is required"), nil
	}

	content, err := s.workspace.GetDraft(path)
	if err != nil {
		s.recordActivity("vclu_draft_validate", path, false, start, err.Error(), ctx)
		return toolError(err.Error()), nil
	}

	// Skip Lua validation for non-Lua files (e.g., .css)
	var report *ValidationReport
	if strings.HasSuffix(path, ".lua") {
		report = ValidateDraft(content)
	} else {
		report = &ValidationReport{Valid: true}
	}

	s.addAudit(ctx, "draft_validate", path, nil, "workspace", report.Valid,
		fmt.Sprintf("%d errors, %d warnings", len(report.Errors), len(report.Warnings)))
	s.recordActivity("vclu_draft_validate", path, report.Valid, start, "", ctx)
	return toolJSON(map[string]any{
		"env":    "workspace",
		"path":   path,
		"report": report,
	})
}

// jsonArg returns an argument as JSON; clients with a stale tool schema send
// arrays as a JSON-encoded string, which is taken as is.
func jsonArg(v any) []byte {
	if s, ok := v.(string); ok {
		return []byte(s)
	}
	data, _ := json.Marshal(v)
	return data
}

func (s *MCPServer) handleDraftTest(ctx context.Context, req mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
	start := time.Now()
	if err := s.requireDevMode(ctx); err != nil {
		return err, nil
	}
	if s.sandbox == nil {
		return toolError("sandbox not configured"), nil
	}

	args := req.GetArguments()

	// Parse path(s) — string or array
	paths, err := parsePaths(args["path"])
	if err != nil {
		return toolError(err.Error()), nil
	}

	// Parse inject_events
	var events []InjectEvent
	if rawEvents, ok := args["inject_events"]; ok {
		eventsJSON, err := json.Marshal(rawEvents)
		if err != nil {
			return toolError(fmt.Sprintf("invalid inject_events: %v", err)), nil
		}
		if err := json.Unmarshal(eventsJSON, &events); err != nil {
			return toolError(fmt.Sprintf("invalid inject_events format: %v", err)), nil
		}
	}

	sc := SandboxScenario{Events: events}
	if at, ok := args["at"].(string); ok && at != "" {
		t, err := time.Parse(time.RFC3339, at)
		if err != nil {
			return toolError(fmt.Sprintf("invalid at (RFC 3339 expected): %v", err)), nil
		}
		sc.At = t
	}
	if tz, ok := args["timezone"].(string); ok {
		sc.Timezone = tz
	}
	sc.Verbose, _ = args["verbose"].(bool)
	if raw, ok := args["steps"]; ok && raw != nil {
		steps, err := ParseSteps(jsonArg(raw))
		if err != nil {
			return toolError(fmt.Sprintf("invalid steps: %v", err)), nil
		}
		sc.Steps = steps
	}
	if raw, ok := args["fixtures"]; ok && raw != nil {
		dec := json.NewDecoder(bytes.NewReader(jsonArg(raw)))
		dec.DisallowUnknownFields()
		sc.Fixtures = []ObjectDescriptor{}
		if err := dec.Decode(&sc.Fixtures); err != nil {
			return toolError(fmt.Sprintf("invalid fixtures: %v", err)), nil
		}
	}

	// Rate limit: max 30 sandbox executions per minute
	if !s.sandboxLimiter.allow() {
		pathStr := strings.Join(paths, ", ")
		s.recordActivity("vclu_draft_test", pathStr, false, start, "rate limited", ctx)
		return toolError("rate limit exceeded: max 30 sandbox executions per minute"), nil
	}

	result, err := s.sandbox.RunScenario(paths, sc)
	if err != nil {
		pathStr := strings.Join(paths, ", ")
		s.recordActivity("vclu_draft_test", pathStr, false, start, err.Error(), ctx)
		return toolError(err.Error()), nil
	}

	details := fmt.Sprintf("%d state changes, %d blocked calls, %d CLU calls, %d errors",
		len(result.StateChanges), len(result.BlockedCalls), len(result.CluCalls), len(result.Errors))
	s.addAudit(ctx, "draft_test", "", paths, "sandbox", result.Success, details)

	pathStr := strings.Join(paths, ", ")
	s.recordActivity("vclu_draft_test", pathStr, result.Success, start, "", ctx)
	return toolJSON(result)
}

func (s *MCPServer) handleDraftPromote(ctx context.Context, req mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
	start := time.Now()
	if err := s.requireDevMode(ctx); err != nil {
		return err, nil
	}
	if s.workspace == nil {
		return toolError("workspace not configured"), nil
	}

	// Parse path(s) — string, array, "all", or omitted (= all drafts)
	var paths []string
	rawPath := req.GetArguments()["path"]
	pathStr, isStr := rawPath.(string)

	if rawPath == nil || (isStr && (pathStr == "all" || pathStr == "")) {
		// Promote all drafts
		drafts, err := s.workspace.ListDrafts()
		if err != nil {
			return toolError(fmt.Sprintf("failed to list drafts: %v", err)), nil
		}
		if len(drafts) == 0 {
			return toolError("no drafts to promote"), nil
		}
		for _, d := range drafts {
			paths = append(paths, d.Path)
		}
	} else {
		var err error
		paths, err = parsePaths(rawPath)
		if err != nil {
			return toolError(err.Error()), nil
		}
	}

	// Rate limit: every promote reloads the runtime; this stops a looping agent, not normal work
	if !s.promoteLimiter.allow() {
		s.addAudit(ctx, "promote", "", paths, "runtime", false, "rate limited")
		return toolError(fmt.Sprintf("rate limit exceeded: max %d promotes per hour; promote several drafts in one call (a path array or \"all\")", promotesPerHour)), nil
	}

	// Validate all drafts exist and pass validation
	for _, p := range paths {
		content, err := s.workspace.GetDraft(p)
		if err != nil {
			return toolError(fmt.Sprintf("draft %s: %v", p, err)), nil
		}
		// Skip Lua validation for non-Lua files
		if !strings.HasSuffix(p, ".lua") {
			continue
		}
		report := ValidateDraft(content)
		if !report.Valid {
			s.addAudit(ctx, "promote", p, paths, "workspace", false, "validation failed")
			return toolJSON(map[string]any{
				"env":    "workspace",
				"status": "rejected",
				"path":   p,
				"reason": "draft failed validation",
				"errors": report.Errors,
			})
		}
	}

	// Atomic promote: backup all → replace all → reload
	var backupNames []string
	var promoted []workspace.Promoted
	for _, p := range paths {
		backupName, err := s.workspace.Promote(p)
		if err != nil {
			// Undo already-promoted files
			msg := fmt.Sprintf("promote failed at %s: %v (rolled back %d files)", p, err, len(promoted))
			if undoErrs := s.workspace.Undo(promoted); len(undoErrs) > 0 {
				msg += "; ROLLBACK FAILED: " + strings.Join(undoErrs, "; ")
			}
			s.addAudit(ctx, "promote", p, paths, "runtime", false, msg)
			errPathStr := strings.Join(paths, ", ")
			s.recordActivity("vclu_draft_promote", errPathStr, false, start, err.Error(), ctx)
			return toolError(msg), nil
		}
		promoted = append(promoted, workspace.Promoted{Path: p, Backup: backupName})
		if backupName != "" {
			backupNames = append(backupNames, backupName)
		}
	}

	// Auto-reload the Lua runtime after promote so changes take effect immediately.
	// Snapshot the pre-existing module failures first: a module that was already
	// broken before this promote must not be reported as caused by it.
	//
	// Keyed by name AND error text. Keying by name alone would treat "still
	// broken, but now for a different reason" as pre-existing — which is exactly
	// the common case of fixing a broken module with a fix that is itself broken.
	var preFailures map[string]bool
	var reloadErr error
	if s.reloader != nil {
		preFailures = make(map[string]bool)
		for _, f := range moduleLoadFailures(s.reloader) {
			preFailures[f.Name+"\x00"+f.Error] = true
		}
		log.Printf("[MCP] Reloading Lua after promote of %d files", len(paths))
		// The error matters: Reload() can bail out before it ever reaches
		// LoadUserModules, which would leave the module report empty and make the
		// promote look clean while the runtime is broken.
		reloadErr = s.reloader.Reload()
		if reloadErr != nil {
			log.Printf("[MCP] reload after promote FAILED: %v", reloadErr)
		}
	}

	// Check for subdirectory modules that may not be loaded without require() in init.lua
	warnings := s.subdirRequireWarnings(paths)

	// The files are on disk, but if the reload could not load them the promoted
	// code is not actually running — say so rather than reporting plain success.
	//
	// OK flips only on problems this promote is responsible for: a failed reload,
	// or a module failure that is not byte-for-byte the one already present before.
	// A module broken long before this call is reported separately, so it stays
	// visible without turning every later promote into a failure.
	//
	// Two guards keep this from hiding real breakage:
	//   - a failure of a module that was itself just promoted is NEVER demoted;
	//   - the same module failing with a DIFFERENT error counts as new.
	promotedModules := make(map[string]bool, len(paths))
	for _, p := range paths {
		promotedModules[strings.TrimPrefix(p, "modules/")] = true
	}

	var moduleErrors, preexistingErrors []string
	promoteOK := true
	if reloadErr != nil {
		promoteOK = false
		moduleErrors = append(moduleErrors, fmt.Sprintf("reload failed: %v", reloadErr))
	}
	for _, f := range moduleLoadFailures(s.reloader) {
		entry := fmt.Sprintf("modules/%s: %s", f.Name, f.Error)
		if !promotedModules[f.Name] && preFailures[f.Name+"\x00"+f.Error] {
			preexistingErrors = append(preexistingErrors, entry)
			continue
		}
		moduleErrors = append(moduleErrors, entry)
		promoteOK = false
	}

	auditMsg := fmt.Sprintf("%d files promoted + reloaded", len(paths))
	if !promoteOK {
		auditMsg = fmt.Sprintf("%d files promoted but %d module(s) failed to load", len(paths), len(moduleErrors))
	}
	s.addAudit(ctx, "promote", "", paths, "runtime", promoteOK, auditMsg)
	// No automatic rollback: the agent fixes the module; the incident keeps
	// the logs and audit trail of the failed load for it
	if !promoteOK {
		s.collectIncident("promote", strings.Join(moduleErrors, "; "), promoted)
	}
	promotePathStr := strings.Join(paths, ", ")
	s.recordActivity("vclu_draft_promote", promotePathStr, promoteOK, start, strings.Join(moduleErrors, "; "), ctx)
	incident := ""
	if !promoteOK && s.incidents != nil {
		incident = "vclu://incidents/latest"
	}
	return toolJSON(PromoteResult{
		Incident:          incident,
		OK:                promoteOK,
		Paths:             paths,
		Backups:           backupNames,
		PreexistingErrors: preexistingErrors,
		Warnings:          warnings,
		ModuleErrors:      moduleErrors,
	})
}

// requireCallRe matches require "mod", require("mod") and require('mod'), and the same with
// optional, which loads a module the same way (#132).
// The leading (^|[^%w_.:]) stands in for a word boundary Go's RE2 spells as \b but
// which must also exclude '.' and ':' — without it, safe_require("x") and
// helpers.require("x") would count as a plain require of x.
var requireCallRe = regexp.MustCompile(`(^|[^\w_.:])(?:require|optional)\s*\(?\s*["']([^"']+)["']`)

// stripLuaBlockComments removes Lua long comments: --[[ ... ]] and the levelled
// form --[==[ ... ]==]. This cannot be a regexp: matching the closing bracket to
// the opening one needs a backreference, which Go's RE2 does not support.
func stripLuaBlockComments(src string) string {
	var b strings.Builder
	i := 0
	for i < len(src) {
		idx := strings.Index(src[i:], "--[")
		if idx < 0 {
			b.WriteString(src[i:])
			break
		}
		start := i + idx

		// Count the '=' padding between the brackets to find the matching close.
		j := start + len("--[")
		eq := 0
		for j < len(src) && src[j] == '=' {
			eq++
			j++
		}
		if j >= len(src) || src[j] != '[' {
			// Not a long comment — keep it; a plain "--" line comment is handled
			// by the caller's line-based stripping.
			b.WriteString(src[i : start+len("--[")])
			i = start + len("--[")
			continue
		}

		b.WriteString(src[i:start])
		closing := "]" + strings.Repeat("=", eq) + "]"
		rest := src[j+1:]
		end := strings.Index(rest, closing)
		if end < 0 {
			// Unterminated comment swallows the remainder of the file.
			break
		}
		i = j + 1 + end + len(closing)
	}
	return b.String()
}

// hasRequire reports whether luaSrc contains a require() or optional() of the given module path.
//
// Lua's package.path substitution turns dots into path separators, so
// require("a.b") and require("a/b") load the same file — both must count as a
// match. A plain substring search gets this wrong in both directions.
//
// False positives here are the dangerous direction: they suppress the "module
// will not be loaded" warning and restore the silent failure this check exists
// to catch. So block comments and line comments are stripped before matching,
// and 'require' or 'optional' must stand as its own word.
//
// Known limits, all either harmless or erring towards a spurious warning:
//   - a require inside a long string ([[ ... ]]) or a quoted string is counted;
//   - a require built by concatenation (require("a/" .. name)) is not resolved;
//   - the block-comment stripper is not string-aware, so a line comment
//     containing "--[[" swallows text up to the next "]]" (e.g. an unrelated
//     t[a[b]]), which can drop a real require from view and warn needlessly.
//
// A directory module's init.lua is loaded by the directory name, so
// "pogoda/init" is satisfied by require("pogoda") too.
func hasRequire(luaSrc, modPath string) bool {
	want := strings.ReplaceAll(modPath, ".", "/")
	dir := strings.TrimSuffix(want, "/init")

	// Block comments first — they span lines, so line-based stripping cannot see them.
	src := stripLuaBlockComments(luaSrc)

	for _, line := range strings.Split(src, "\n") {
		if i := strings.Index(line, "--"); i >= 0 {
			line = line[:i]
		}
		for _, m := range requireCallRe.FindAllStringSubmatch(line, -1) {
			if got := strings.ReplaceAll(m[2], ".", "/"); got == want || got == dir {
				return true
			}
		}
	}
	return false
}

// subdirRequireWarnings warns about promoted files in a subdirectory of modules/ that no
// module requires, so they would not load. modules/init.lua requires a domain
// (require("energia")), and the domain's own files require its other files
// (require("energia/power") in modules/energia/init.lua): a require in any module counts.
// The file provider is optional wiring; without it the check is skipped.
func (s *MCPServer) subdirRequireWarnings(paths []string) []string {
	var warnings []string
	if s.files == nil {
		return nil
	}
	initContent, initErr := s.files.ReadFile("modules/init.lua")
	var others []string
	for _, pattern := range []string{"modules/*.lua", "modules/*/*.lua", "modules/*/*/*.lua"} {
		files, _ := s.files.ListFiles(pattern)
		for _, f := range files {
			if f.Path != "modules/init.lua" {
				others = append(others, f.Path)
			}
		}
	}
	for _, p := range paths {
		if !strings.HasPrefix(p, "modules/") || strings.Count(p, "/") < 2 {
			continue
		}
		// "modules/scenes/movie.lua" -> "scenes/movie"
		reqPath := strings.TrimSuffix(strings.TrimPrefix(p, "modules/"), ".lua")
		if initErr == nil && hasRequire(initContent, reqPath) {
			continue
		}
		requiredElsewhere := false
		for _, o := range others {
			if o == p {
				continue
			}
			if src, err := s.files.ReadFile(o); err == nil && hasRequire(src, reqPath) {
				requiredElsewhere = true
				break
			}
		}
		if requiredElsewhere {
			continue
		}
		if initErr != nil {
			warnings = append(warnings, fmt.Sprintf("%s is in a subdirectory — modules/init.lua not found. Add require() or optional() to load it.", p))
			continue
		}
		warnings = append(warnings, fmt.Sprintf("%s is in a subdirectory but no module loads it (modules/init.lua has no require('%s') or optional('%s')). It will NOT be loaded automatically.", p, reqPath, reqPath))
	}
	return warnings
}

func (s *MCPServer) handleDiff(ctx context.Context, req mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
	start := time.Now()
	if err := s.requireDevMode(ctx); err != nil {
		return err, nil
	}
	if s.workspace == nil {
		return toolError("workspace not configured"), nil
	}

	path, _ := req.GetArguments()["path"].(string)
	if path == "" {
		return toolError("path is required"), nil
	}

	draftContent, err := s.workspace.GetDraft(path)
	if err != nil {
		s.recordActivity("vclu_diff", path, false, start, err.Error(), ctx)
		return toolError(err.Error()), nil
	}

	prodContent, err := s.workspace.GetProductionContent(path)
	if err != nil {
		s.recordActivity("vclu_diff", path, false, start, err.Error(), ctx)
		return toolError(err.Error()), nil
	}

	diff := unifiedDiff(
		fmt.Sprintf("a/%s (production)", path),
		fmt.Sprintf("b/%s (draft)", path),
		prodContent, draftContent,
	)

	if diff == "" {
		diff = "(no differences)"
	}

	s.recordActivity("vclu_diff", path, true, start, "", ctx)
	return toolJSON(map[string]any{
		"env":  "workspace",
		"path": path,
		"diff": diff,
	})
}

func (s *MCPServer) handleAuditLog(ctx context.Context, req mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
	if err := s.requireDevMode(ctx); err != nil {
		return err, nil
	}

	limit := 50
	if l, ok := req.GetArguments()["limit"].(float64); ok {
		limit = int(l)
	}
	if limit <= 0 {
		limit = 50
	}
	if limit > auditMaxEntries {
		limit = auditMaxEntries
	}

	entries := s.audit.GetRecent(limit)
	return toolJSON(map[string]any{
		"env":     "runtime",
		"entries": entries,
		"count":   len(entries),
	})
}

// --- Incident helper ---

// collectIncident saves an incident snapshot of a promote: recent logs, the
// audit trail and what the promote changed (backup -> production).
func (s *MCPServer) collectIncident(trigger, errorMsg string, promoted []workspace.Promoted) {
	if s.incidents == nil {
		return
	}
	paths := make([]string, len(promoted))
	for i, p := range promoted {
		paths[i] = p.Path
	}

	snap := IncidentSnapshot{
		Timestamp: time.Now(),
		Trigger:   trigger,
		ErrorMsg:  errorMsg,
		Paths:     paths,
	}

	// Collect recent logs (last 30)
	if s.logs != nil {
		history := s.logs.GetHistory()
		start := 0
		if len(history) > 30 {
			start = len(history) - 30
		}
		for _, e := range history[start:] {
			snap.RecentLogs = append(snap.RecentLogs, fmt.Sprintf("%s [%s] %s",
				e.Time.Format("15:04:05"), e.Level, e.Message))
		}
	}

	// Collect audit trail (last 20)
	snap.AuditTrail = s.audit.GetRecent(20)

	// The drafts are gone after promote: diff the backup (or nothing, for a
	// new file) against what is in production now
	if s.workspace != nil {
		var diffs []string
		for _, p := range promoted {
			before := ""
			if p.Backup != "" {
				if c, _, err := s.workspace.GetBackup(p.Backup); err == nil {
					before = c
				}
			}
			after, err := s.workspace.GetProductionContent(p.Path)
			if err != nil {
				continue
			}
			if diff := unifiedDiff(
				fmt.Sprintf("a/%s (before)", p.Path),
				fmt.Sprintf("b/%s (promoted)", p.Path),
				before, after,
			); diff != "" {
				diffs = append(diffs, diff)
			}
		}
		snap.CodeDiff = strings.Join(diffs, "\n\n")
	}

	if _, err := s.incidents.Save(snap); err != nil {
		log.Printf("[MCP] Failed to save incident: %v", err)
	}
}

// --- Audit helper ---

func (s *MCPServer) addAudit(ctx context.Context, tool, path string, paths []string, env string, success bool, details string) {
	botName := ""
	actor := ""
	session := ""
	if bot := botFromContext(ctx); bot != nil {
		botName = bot.Name
		actor = bot.Name
		session = sessionFromContext(ctx)
	}
	s.audit.Add(AuditEntry{
		Time:    time.Now(),
		Tool:    tool,
		Path:    path,
		Paths:   paths,
		Env:     env,
		Success: success,
		Details: details,
		BotName: botName,
		Actor:   actor,
		Session: session,
	})
}

// --- Rate limiter ---

type rateLimiter struct {
	mu      sync.Mutex
	window  time.Duration
	max     int
	history []time.Time
}

// promotesPerHour: each promote reloads the Lua runtime, so a looping agent is stopped at
// one reload a minute on average; a person working with an agent does not reach it.
const promotesPerHour = 60

func newRateLimiter(window time.Duration, max int) *rateLimiter {
	return &rateLimiter{window: window, max: max}
}

func (rl *rateLimiter) allow() bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	now := time.Now()
	cutoff := now.Add(-rl.window)

	// Remove expired entries
	valid := rl.history[:0]
	for _, t := range rl.history {
		if t.After(cutoff) {
			valid = append(valid, t)
		}
	}
	rl.history = valid

	if len(rl.history) >= rl.max {
		return false
	}

	rl.history = append(rl.history, now)
	return true
}

// InitWorkspace initializes the workspace, sandbox engine, incidents, agent context,
// and Lua reloader for the MCP Layer 2 draft pipeline. Call once after NewMCPServer.
func (s *MCPServer) InitWorkspace(cluDir, runtimePath string, reloader LuaReloader) {
	ws, err := workspace.New(cluDir)
	if err != nil {
		log.Printf("[MCP] Warning: failed to create workspace: %v", err)
		return
	}
	s.workspace = ws
	s.sandbox = NewSandboxEngine(runtimePath, filepath.Join(cluDir, "a"), cluDir, ws)
	s.sandbox.SetObjectSource(func() (string, error) {
		if s.executor == nil {
			return "", fmt.Errorf("Lua executor not configured")
		}
		return s.executor.ExecuteWithTimeout("SandboxObjects.export()", time.Second)
	})
	s.reloader = reloader
	s.incidents = NewIncidentStore(cluDir)
	s.agentCtx = NewAgentContext(cluDir)

	// Re-create audit log with file persistence (workspace/ dir)
	auditDir := filepath.Join(cluDir, "workspace")
	s.audit = NewAuditLog(auditDir)
}
