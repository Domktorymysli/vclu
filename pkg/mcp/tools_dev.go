package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"

	mcplib "github.com/mark3labs/mcp-go/mcp"
)

// registerDevTools registers Layer 2 (Development Pipeline) tools.
// Tools are always registered — devMode gate is checked per request.
func (s *MCPServer) registerDevTools() {
	s.addTool("vclu_read_file", "Read a file from the vCLU data directory (Lua code, documentation, modules)", s.handleReadFile,
		map[string]any{
			"path": map[string]any{
				"type":        "string",
				"description": "Relative file path (e.g., a/user.lua, modules/init.lua, docs/ai/house.md)",
			},
		}, []string{"path"})

	s.addTool("vclu_list_files", "List files matching a glob pattern", s.handleListFiles,
		map[string]any{
			"pattern": map[string]any{
				"type":        "string",
				"description": "Glob pattern (e.g., modules/*.lua, docs/ai/*.md, a/*.lua)",
			},
		}, []string{"pattern"})

	s.addTool("vclu_grep", "Search for a regex pattern in files", s.handleGrep,
		map[string]any{
			"pattern": map[string]any{
				"type":        "string",
				"description": "Regex pattern to search for",
			},
			"glob": map[string]any{
				"type":        "string",
				"description": "File glob, matched against the file name or the path (default: *.lua; ** spans directories, e.g. modules/**/*.lua)",
			},
			"max_results": map[string]any{
				"type":        "number",
				"description": "Maximum number of results (default: 50, max: 200)",
			},
		}, []string{"pattern"})

	s.addTool("vclu_logs", "Get recent runtime logs", s.handleLogs,
		map[string]any{
			"limit": map[string]any{
				"type":        "number",
				"description": "Number of log entries to return (default: 100, max: 1000)",
			},
			"filter": map[string]any{
				"type":        "string",
				"description": "Regex filter for log messages",
			},
			"level": map[string]any{
				"type":        "string",
				"description": "Minimum level: debug, info, warning, error. Use 'error' to find failures without the debug flood.",
			},
		}, nil)

	s.addTool("vclu_config", "Get system configuration (without secrets)", s.handleConfig, nil, nil)

	s.addTool("vclu_reload", "Reload the Lua runtime (apply code changes without restart)", s.handleReload, nil, nil)

	s.addTool("vclu_runtime_schema", "Get object types, dashboard widget API, plugin methods, and installed plugins with their sensors/controls", s.handleRuntimeSchema, nil, nil)

	s.addTool("vclu_exec", "Execute a Lua snippet on the live runtime (requires ExecMode)", s.handleExec,
		map[string]any{
			"script": map[string]any{
				"type":        "string",
				"description": "Lua code to execute (max 8KB). Use 'return expr' to get a result.",
			},
		}, []string{"script"})

	s.addTool("vclu_dashboards", "List all registered dashboards with their widgets", s.handleDashboards, nil, nil)

	s.addTool("vclu_plugin_config", "Get plugin configuration (secrets redacted)", s.handlePluginConfig,
		map[string]any{
			"id": map[string]any{
				"type":        "string",
				"description": "Filter by plugin ID (optional)",
			},
		}, nil)

	s.addTool("vclu_device_history", "State changes of devices, newest first, with who made them (origin, actor). Older entries come from the daily files on disk (retention set in Tools -> History).", s.handleDeviceHistory,
		map[string]any{
			"path": map[string]any{
				"type":        "string",
				"description": "Filter by device path (optional)",
			},
			"limit": map[string]any{
				"type":        "number",
				"description": "Entries per page (default 50, max 1000)",
			},
			"since":  map[string]any{"type": "string", "description": "From this time, RFC 3339 (optional)"},
			"until":  map[string]any{"type": "string", "description": "Before this time, RFC 3339 (optional)"},
			"origin": map[string]any{"type": "string", "description": "grenton, mqtt, homekit, web, api, mcp, lua, physical, system (optional)"},
			"actor":  map[string]any{"type": "string", "description": "e.g. automation:wiatrolap, user:tytus, device:CLU220000592 (optional)"},
			"query":  map[string]any{"type": "string", "description": "Text in path, actor, values or correlation id (optional)"},
			"cursor": map[string]any{"type": "string", "description": "The next value of the previous page"},
		}, nil)

	s.addTool("vclu_registry", "Get full device registry with metadata (area, group, tags, description)", s.handleRegistry,
		map[string]any{
			"filter": map[string]any{
				"type":        "object",
				"description": "Filter object (all fields optional, combined with AND)",
				"properties": map[string]any{
					"type": map[string]any{
						"type":        "string",
						"description": "Device type (switch, dimmer, cover, sensor, scene, etc.)",
					},
					"area": map[string]any{
						"type":        "string",
						"description": "Physical area/room name",
					},
					"group": map[string]any{
						"type":        "string",
						"description": "Logical group name",
					},
					"tag": map[string]any{
						"type":        "string",
						"description": "Tag to match",
					},
					"text": map[string]any{
						"type":        "string",
						"description": "Free-text search in name, description, path",
					},
				},
			},
		}, nil)

	s.addTool("vclu_metrics", "List registered metrics and query time-series data for dashboard charts", s.handleMetrics,
		map[string]any{
			"path": map[string]any{
				"type":        "string",
				"description": "Metric path to query data for (e.g., temp:salon, salda:outsideAir). Omit to list all metrics.",
			},
			"range": map[string]any{
				"type":        "string",
				"description": "Time range for data query (e.g., 30m, 2h, 24h, 7d). Default: 2h. Only used when path is specified.",
			},
		}, nil)

	s.addTool("vclu_file_delete", "Delete a production file (modules/*.lua only). Use for cleanup — cannot delete system files.", s.handleFileDelete,
		map[string]any{
			"path": map[string]any{
				"type":        "string",
				"description": "Relative file path to delete (e.g., modules/old_dashboard.lua)",
			},
		}, []string{"path"})

	s.registerMQTTDiscoverTool()
}

// requireDevMode checks if dev mode is enabled (global + per-bot). Returns an error result if disabled.
func (s *MCPServer) requireDevMode(ctx context.Context) *mcplib.CallToolResult {
	if !s.isDevModeEnabled() {
		return toolError("Dev Mode is disabled. Enable it in the MCP dashboard.")
	}
	if bot := botFromContext(ctx); bot != nil && !bot.DevMode {
		return toolError("Bot \"" + bot.Name + "\" does not have sandbox access. Enable it in MCP dashboard → Bots.")
	}
	return nil
}

// --- Layer 2 Tool Handlers ---

func (s *MCPServer) handleReadFile(ctx context.Context, req mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
	start := time.Now()
	if err := s.requireDevMode(ctx); err != nil {
		return err, nil
	}

	path, _ := req.GetArguments()["path"].(string)
	if path == "" {
		return toolError("path is required"), nil
	}

	if s.files == nil {
		return toolError("file provider not configured"), nil
	}

	content, err := s.files.ReadFile(path)
	if err != nil {
		s.recordActivity("vclu_read_file", path, false, start, err.Error(), ctx)
		return toolError(err.Error()), nil
	}

	s.recordActivity("vclu_read_file", path, true, start, "", ctx)
	return toolJSON(map[string]any{
		"env":     "runtime",
		"path":    path,
		"content": content,
	})
}

func (s *MCPServer) handleListFiles(ctx context.Context, req mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
	start := time.Now()
	if err := s.requireDevMode(ctx); err != nil {
		return err, nil
	}

	pattern, _ := req.GetArguments()["pattern"].(string)
	if pattern == "" {
		return toolError("pattern is required"), nil
	}

	if s.files == nil {
		return toolError("file provider not configured"), nil
	}

	files, err := s.files.ListFiles(pattern)
	if err != nil {
		s.recordActivity("vclu_list_files", pattern, false, start, err.Error(), ctx)
		return toolError(err.Error()), nil
	}

	s.recordActivity("vclu_list_files", pattern, true, start, "", ctx)
	return toolJSON(map[string]any{
		"env":   "runtime",
		"files": files,
		"count": len(files),
	})
}

func (s *MCPServer) handleFileDelete(ctx context.Context, req mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
	start := time.Now()
	if err := s.requireDevMode(ctx); err != nil {
		return err, nil
	}

	path, _ := req.GetArguments()["path"].(string)
	if path == "" {
		return toolError("path is required"), nil
	}

	if s.files == nil {
		return toolError("file provider not configured"), nil
	}

	if err := s.files.DeleteFile(path); err != nil {
		s.recordActivity("vclu_file_delete", path, false, start, err.Error(), ctx)
		return toolError(err.Error()), nil
	}

	s.addAudit(ctx, "file_delete", path, nil, "runtime", true, "")
	s.recordActivity("vclu_file_delete", path, true, start, "", ctx)
	return toolJSON(map[string]any{
		"env":  "runtime",
		"ok":   true,
		"path": path,
	})
}

func (s *MCPServer) handleGrep(ctx context.Context, req mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
	start := time.Now()
	if err := s.requireDevMode(ctx); err != nil {
		return err, nil
	}

	args := req.GetArguments()
	pattern, _ := args["pattern"].(string)
	if pattern == "" {
		return toolError("pattern is required"), nil
	}

	glob, _ := args["glob"].(string)
	maxResults := 50
	if mr, ok := args["max_results"].(float64); ok {
		maxResults = int(mr)
	}

	if s.files == nil {
		return toolError("file provider not configured"), nil
	}

	results, err := s.files.Grep(pattern, glob, maxResults)
	if err != nil {
		s.recordActivity("vclu_grep", pattern, false, start, err.Error(), ctx)
		return toolError(err.Error()), nil
	}

	s.recordActivity("vclu_grep", pattern, true, start, "", ctx)
	return toolJSON(map[string]any{
		"env":     "runtime",
		"matches": results,
		"count":   len(results),
	})
}

// logLevelRank orders levels for minimum-level filtering.
var logLevelRank = map[string]int{
	"debug": 0, "info": 1, "warning": 2, "warn": 2, "error": 3,
}

// effectiveLogLevel resolves the real level of a log entry.
//
// Two independent signals carry level information and neither is authoritative
// on its own: LogStream.parseLine infers a level from the text (defaulting to
// "info"), while Lua's Logger writes "[DEBUG] ..." / "[WARN] ..." and that tag
// is captured as the *source*. A Lua debug line therefore arrives as
// level=info source=DEBUG, and would survive a min-level filter.
//
// The two are combined by taking the HIGHER severity, never by letting the
// source override. A line like "[DEBUG] ... failed to load ..." is tagged
// source=DEBUG but parsed as level=error; preferring the source would hide it
// from vclu_logs(level:"error") — exactly the failure this filter exists to
// surface. Over-reporting severity is the safe direction here.
func effectiveLogLevel(level, source string) string {
	// What the source tag claims, if it names a level at all.
	var fromSource string
	switch strings.ToUpper(source) {
	case "DEBUG":
		fromSource = "debug"
	case "WARN", "WARNING":
		fromSource = "warning"
	case "ERROR":
		fromSource = "error"
	}

	parsed := strings.ToLower(level)
	if parsed == "warn" {
		parsed = "warning"
	}
	if _, known := logLevelRank[parsed]; !known {
		parsed = ""
	}

	// parseLine returns "info" both for a line it judged informational and for one
	// it could not classify — "info" is its default, not a positive finding. So it
	// carries no severity evidence, and the source tag decides.
	if parsed == "" || parsed == "info" {
		if fromSource != "" {
			return fromSource
		}
		return "info"
	}

	// parseLine positively detected error/warning. Never let a source tag lower
	// that: "[DEBUG] ... failed to load ..." must stay findable at level:"error".
	if fromSource != "" && logLevelRank[fromSource] > logLevelRank[parsed] {
		return fromSource
	}
	return parsed
}

func (s *MCPServer) handleLogs(ctx context.Context, req mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
	start := time.Now()
	if err := s.requireDevMode(ctx); err != nil {
		return err, nil
	}

	if s.logs == nil {
		return toolError("log provider not configured"), nil
	}

	args := req.GetArguments()
	limit := 100
	if l, ok := args["limit"].(float64); ok {
		limit = int(l)
	}
	if limit <= 0 {
		limit = 100
	}
	if limit > 1000 {
		limit = 1000
	}

	filterStr, _ := args["filter"].(string)
	var filterRe *regexp.Regexp
	if filterStr != "" {
		var err error
		filterRe, err = regexp.Compile(filterStr)
		if err != nil {
			return toolError(fmt.Sprintf("invalid filter regex: %v", err)), nil
		}
	}

	minRank := 0
	if lvl, ok := args["level"].(string); ok && lvl != "" {
		r, known := logLevelRank[strings.ToLower(lvl)]
		if !known {
			return toolError(fmt.Sprintf("invalid level %q — use debug, info, warning or error", lvl)), nil
		}
		minRank = r
	}

	history := s.logs.GetHistory()

	// Apply filter
	type logEntry struct {
		Time    string `json:"time"`
		Level   string `json:"level"`
		Source  string `json:"source,omitempty"`
		Message string `json:"message"`
	}

	var entries []logEntry
	// Walk from newest to oldest (history is oldest-first, reverse it)
	for i := len(history) - 1; i >= 0; i-- {
		if len(entries) >= limit {
			break
		}
		h := history[i]
		level := effectiveLogLevel(h.Level, h.Source)
		if logLevelRank[level] < minRank {
			continue
		}
		if filterRe != nil && !filterRe.MatchString(h.Message) && !filterRe.MatchString(h.Source) {
			continue
		}
		entries = append(entries, logEntry{
			Time:    h.Time.Format(time.RFC3339),
			Level:   level,
			Source:  h.Source,
			Message: h.Message,
		})
	}

	s.recordActivity("vclu_logs", "", true, start, "", ctx)
	return toolJSON(map[string]any{
		"env":   "runtime",
		"logs":  entries,
		"count": len(entries),
	})
}

func (s *MCPServer) handleConfig(ctx context.Context, _ mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
	start := time.Now()
	if err := s.requireDevMode(ctx); err != nil {
		return err, nil
	}

	// Read config from disk — expose safe subset only
	cfg := s.config

	// Build safe config snapshot (no secrets)
	bots := cfg.GetMCPBots()
	var botNames []string
	for _, b := range bots {
		botNames = append(botNames, b.Name)
	}

	result := map[string]any{
		"env":            "runtime",
		"devModeEnabled": s.isDevModeEnabled(),
		"mcpBots":        botNames,
	}

	s.recordActivity("vclu_config", "", true, start, "", ctx)
	return toolJSON(result)
}

func (s *MCPServer) handleReload(ctx context.Context, _ mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
	start := time.Now()
	if err := s.requireDevMode(ctx); err != nil {
		return err, nil
	}

	if s.reloader == nil {
		return toolError("reloader not configured"), nil
	}

	if err := s.reloader.Reload(); err != nil {
		s.recordActivity("vclu_reload", "", false, start, err.Error(), ctx)
		return toolError(fmt.Sprintf("reload failed: %v", err)), nil
	}

	// The runtime can reload cleanly while a user module still fails to load —
	// reporting a bare success there hides a broken runtime from the caller.
	if failed := moduleLoadFailures(s.reloader); len(failed) > 0 {
		errs := moduleLoadSummary(failed)
		msg := fmt.Sprintf("Lua runtime reloaded, but %d user module(s) FAILED to load", len(failed))
		s.addAudit(ctx, "reload", "", nil, "runtime", false, msg)
		s.recordActivity("vclu_reload", "", false, start, strings.Join(errs, "; "), ctx)
		return toolJSON(map[string]any{
			"ok":            false,
			"message":       msg,
			"module_errors": errs,
		})
	}

	s.addAudit(ctx, "reload", "", nil, "runtime", true, "Lua reload via MCP")
	s.recordActivity("vclu_reload", "", true, start, "", ctx)
	return toolJSON(map[string]any{"ok": true, "message": "Lua runtime reloaded"})
}

func (s *MCPServer) handleRuntimeSchema(ctx context.Context, _ mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
	start := time.Now()
	if err := s.requireDevMode(ctx); err != nil {
		return err, nil
	}

	schema := map[string]any{
		"env": "runtime",
		"objects": map[string]any{
			"RemoteSwitch (DOUT)": map[string]any{
				"methods": []string{"get(feature)", "set(feature,value)", "switchOn()", "switchOff()", "toggle()", "getValue()"},
				"note":    "Remote CLU binary output",
			},
			"RemoteDimmer (DIMMER)": map[string]any{
				"methods": []string{"get(feature)", "set(feature,value)", "switchOn()", "switchOff()", "toggle()", "setValue(value)", "getValue()"},
				"note":    "Remote CLU dimmer",
			},
			"RemoteRoller (ROLLER_SHUTTER)": map[string]any{
				"methods": []string{"get(feature)", "set(feature,value)", "open()", "close()", "stop()", "setPosition(pos)", "getValue()"},
				"note":    "Remote CLU roller shutter / cover",
			},
			"VirtualSwitch (DOUT)": map[string]any{
				"constructor": "VirtualSwitch:new(name, {initial=0, persist=true|'key'})",
				"methods":     []string{"switchOn()", "switchOff()", "toggle()", "getValue()", "setValue(v)", "onChange(cb)"},
				"note":        "Software-only switch (no hardware)",
			},
			"VirtualDimmer (DIMMER)": map[string]any{
				"constructor": "VirtualDimmer:new(name, {initial=0, persist=true|'key'})",
				"methods":     []string{"switchOn()", "switchOff()", "toggle()", "getBrightness()", "setBrightness(pct)", "getValue()", "setValue(v)", "onChange(cb)"},
				"note":        "Software-only dimmer, brightness 0.0-1.0",
			},
			"VirtualCover (ROLLER)": map[string]any{
				"constructor": "VirtualCover:new(name, {initial=0, persist=true|'key'})",
				"methods":     []string{"open()", "close()", "stop()", "up()", "down()", "getPosition()", "setPosition(pos)", "onChange(cb)"},
				"note":        "Software-only cover, position 0 = closed .. 100 = open",
			},
			"VirtualSensor (DIN)": map[string]any{
				"constructor": "VirtualSensor:new(name, {initial=0, persist=true|'key'})",
				"methods":     []string{"getValue()", "isActive()", "onChange(cb)"},
				"note":        "Software-only binary sensor (read-only, set via updateState())",
			},
			"VirtualNumber (AnalogOUT)": map[string]any{
				"methods":     []string{"getValue()", "setValue(v)", "getMeta()", "onChange(cb)"},
				"constructor": "VirtualNumber:new(name, {min=n, max=n, step=n, unit='str', initial=n, persist=true|'key'})",
				"note":        "Numeric value with optional range/step validation. setValue() auto-clamps.",
			},
			"VirtualText (TEXT)": map[string]any{
				"methods":     []string{"getValue()", "setValue(v)", "onChange(cb)"},
				"constructor": "VirtualText:new(name, {initial='str', maxLength=n, persist=true|'key'})",
				"note":        "Text value. Display with :status() dashboard widget.",
			},
			"PluginSensor": map[string]any{
				"methods": []string{"get()", "onChange(cb)"},
				"note":    "Created via plugin:sensor(). NOT getValue()!",
			},
			"PluginControl": map[string]any{
				"methods": []string{"get()", "set(value)", "onChange(cb)"},
				"note":    "Created via plugin:control(). NOT getValue/setValue!",
			},
			"RemotePanel (PANEL)": map[string]any{
				"methods": []string{"beep(freq, ms, volume)", "displayOn()", "getButton(index)"},
				"note":    "Grenton Smart Panel screen from the OM import; draw it with require('lib/panel'), not with execute()",
			},
		},
		"smartPanel": map[string]any{
			"library": "local Panel = require('lib/panel'); Panel.new(_:get('CLU….PAN…')) — the panel object from the OM import",
			"builder": "Panel.new(panel):buttons{tl='btn1',tr='btn2',bl='btn3',br='btn4'}:back('br'):home(page):idle(page):sleepAfter(s):onGesture('up', fn):onProximity(fn)",
			"pages": map[string]any{
				"menu":       "Panel.menu(title):toggle(label, obj):number(label, getFn, setFn, {step,min,max,format,watch}):choice(label, getFn, setFn, {options}):value(label, obj|fn, format):submenu(label, page):action(label, fn|page, {confirm=text})",
				"value":      "Panel.value(title, obj|fn, format)",
				"toggle":     "Panel.toggle(obj, title)",
				"thermostat": "Panel.thermostat{name, current=obj|fn, get=fn, set=fn, step, min, max, format}",
				"idle":       "Panel.idle{ {label, obj|fn, format}, ... } — clock and up to 3 values; any button opens home",
				"cleaning":   "Panel.cleaning(seconds) — inputs ignored, countdown",
				"own":        "Panel.register(name, factory): a page is {draw=function(screen, panel), keys={up={label,fn},down=…,ok=…}|fn, input=function(role, panel), watch={objs}, every=s}",
			},
			"screen":        "screen:text(x, y, s, font 1-3), textRight, textCenter, line, rule(y), point, icon(x, y, 'name' of a .bmp on the panel) — 128x64, fonts 10/14/28 px, y is the top, 15 characters",
			"helpers":       "panel:notify(text, {seconds, beep}), panel:open(page), panel:goBack(), panel:goHome(), panel:redraw(), panel:debounce(key, s, fn)",
			"objectManager": "every button event (OnClick 'btn1'..'btn4', OnLongPress 'btn1:long') calls click(panel or button id, input) on vCLU; an empty click() from OM in user.lua hides the runtime one, so the module sets click = RemotePanel.click",
			"sandbox":       "vclu_draft_test steps {\"click\": \"PAN…\", \"input\": \"btn3\"} then {\"advance\": 1}: the frame drawn is in clu_calls",
		},
		"dashboard": map[string]any{
			"builder":  "Dashboard.new(id):name():icon():columns():auth():pin():poll():theme()",
			"sections": "dash:header() first, then dash:section(name, icon, {columns=n})",
			"widgets": map[string]any{
				"toggle": "section:toggle(id, label, icon, getValueFn, onToggleFn, opts)",
				"slider": "section:slider(id, label, min, max, getValueFn, onSetFn, opts)",
				"status": "section:status(id, label, icon, getValueFn, unit, opts) — opts.color may be function() -> color|nil, re-read on every poll",
				"button": "section:button(id, label, icon, onTapFn, opts)",
				"link":   "section:link(id, label, icon, url, opts)",
				"cover":  "section:cover(id, label, icon, fns, opts) — fns={getPosition,getState,open,close,stop,set?}",
				"dimmer": "section:dimmer(id, label, icon, fns, opts) — fns={get?,set,switchOn?,switchOff?,isOn?}",
				"color":  "section:color(id, label, icon, fns, opts) — fns={getColor,setColor,get?,set?,switchOn?,switchOff?,isOn?}, opts.presets=[{label,r,g,b}]",
			},
			"opts":       "{ span=n, size='lg', color='#hex', disabled=function() }",
			"auth_types": "public | pin | session",
		},
		"pluginAPI": map[string]any{
			"getPlugin": "Plugin.getPlugin('@vclu/name')",
			"sensors":   "plugin:get('id') → obj with :get()",
			"controls":  "plugin:get('id') → obj with :get() and :set(value)",
			"expose":    "expose(obj, type, { name, path, area, description })",
		},
		"filesystem": map[string]any{
			"autoLoaded":     "modules/*.lua flat files only",
			"subdirectories": "Must add require('subdir/file') to modules/init.lua",
			"plugins":        "plugins/{safe-name}/init.lua — read-only via vclu_read_file",
		},
	}

	// Dynamic: build plugins array from config + plugin.json
	installed := s.config.GetInstalledPlugins()
	if len(installed) > 0 {
		var pluginsInfo []map[string]any
		for _, p := range installed {
			info := map[string]any{
				"id":      p.ID,
				"version": p.Version,
				"enabled": p.Enabled,
			}

			// Try to read exports from plugin.json
			if s.files != nil {
				safeName := safePluginDirName(p.ID)
				content, err := s.files.ReadFile("plugins/" + safeName + "/plugin.json")
				if err == nil {
					var manifest map[string]any
					if json.Unmarshal([]byte(content), &manifest) == nil {
						if exports, ok := manifest["exports"].(map[string]any); ok {
							if sensors, ok := exports["sensors"]; ok {
								info["sensors"] = sensors
							}
							if controls, ok := exports["controls"]; ok {
								info["controls"] = controls
							}
						}
					}
				}
			}

			pluginsInfo = append(pluginsInfo, info)
		}
		schema["plugins"] = pluginsInfo
	}

	s.recordActivity("vclu_runtime_schema", "", true, start, "", ctx)
	return toolJSON(schema)
}

// safePluginDirName converts a plugin ID to its safe directory name.
// "@vclu/weather" -> "vclu--weather", "weather" -> "weather"
func safePluginDirName(fullID string) string {
	fullID = strings.TrimPrefix(fullID, "@")
	parts := strings.SplitN(fullID, "/", 2)
	if len(parts) == 2 {
		return parts[0] + "--" + parts[1]
	}
	return fullID
}

func (s *MCPServer) handleRegistry(ctx context.Context, req mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
	start := time.Now()
	if err := s.requireDevMode(ctx); err != nil {
		return err, nil
	}

	// Pass full filter object as JSON to Lua
	args := req.GetArguments()
	filterJSON := ""
	if filter, ok := args["filter"].(map[string]any); ok {
		if b, err := json.Marshal(filter); err == nil {
			filterJSON = string(b)
		}
	}

	devices, err := s.devices.ListDevices(ctx, filterJSON)
	if err != nil {
		s.recordActivity("vclu_registry", "", false, start, err.Error(), ctx)
		return toolError(err.Error()), nil
	}

	// Filter by ACL
	var visible []Device
	for _, d := range devices {
		if s.botCanRead(ctx, d.Path) {
			if !s.botCanControl(ctx, d.Path) {
				d.Readonly = true
			}
			visible = append(visible, d)
		}
	}

	s.recordActivity("vclu_registry", "", true, start, "", ctx)
	return toolJSON(map[string]any{
		"env":     "runtime",
		"devices": visible,
		"count":   len(visible),
	})
}
