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
	s.registerCalendarTool()
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
			"RemoteThermostat (THERMOSTAT)": map[string]any{
				"methods": []string{"getValue()", "getTarget()", "getSetpoint()", "setSetpoint(v)", "getState()", "getMode()", "onChange(cb)"},
				"note":    "A CLU's Thermostat (class 18, THE…): value = temperature at its sensor, getTarget() = target in effect (with auto/holiday mode), setSetpoint writes PointValue; mode 0 set point, 1 holiday, 2 auto, 3 heat-up. In the registry (add it to Grenton → Sync for values); exposeRegistry() leaves it out, expose(th, \"climate\", {name=...}) shows it in Home Assistant",
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
			"VirtualThermostat (THERMOSTAT)": map[string]any{
				"constructor": "VirtualThermostat:new(name, {source=obj|path, output=obj|path, hysteresis=0.5, direction='heat'|'cool', setpoint=21, holiday=16, min=5, max=35, staleAfter=900, maxOn=s, persist=true|'key'})",
				"methods":     []string{"getValue()", "getTarget()", "getSetpoint()", "setSetpoint(v)", "getState()", "start()", "stop()", "getMode()", "setMode(0|1)", "getFault()", "onChange(cb)"},
				"note":        "Switches output (switchOn/switchOff) by source temperature at target ± hysteresis; no source change for staleAfter s or on longer than maxOn: output off (getFault 'stale'/'maxOn'). Mode 0 set point, 1 holiday. Starts with the output off. Same interface as RemoteThermostat: expose(th, \"climate\", {...}), lib/panel menu:add",
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
			"library": "local Panel = require('lib/panel'); Panel.new('PAN…') — the panel from the OM import by object id or name (Panel.new asserts when it is missing; check Panel.find(id) first to skip the module); Panel.new also restores click(), which the user.lua from Object Manager hides with an empty one. Panel.lang('pl') sets Polish texts; Panel.strings = {noChanges, cleaning, thermostat, now, set, on, off, open, closed, openShort, closedShort, coverOpen, coverClose, coverStop} are the library's own texts (English), set them in the module's language",
			"builder": "Panel.new(panel):buttons{tl='btn1',tr='btn2',bl='btn3',br='btn4'}:back('tr'):home(page):idle(page):sleepAfter(s):onGesture('up', fn):onProximity(fn)",
			"pages": map[string]any{
				"devices":    "menu:add(label?, obj|path, {kind, format, chart=false, history=false}?) picks the row by what the object is (Panel.kind: its expose() type, else its Grenton type): switch/light switches, dimmer edits brightness, cover opens a cover page (up opens, down closes, OK stops), temperature/humidity show the value and open a chart, a binary sensor opens its history; no label: the expose() name. menu:gate(label, obj|path, {open=1}) open/closed with history. Panel.floor(floor, {order, titles, kinds, skip}) is a menu of the areas exposed with that floor (expose(..., {area=..., floor=...})), each a Panel.area, empty ones left out. expose(..., {panel=false}) keeps an object off panels, panel=true puts it on whatever its kind. Panel.area(area|{areas}, {title, kinds, skip}) is a menu of what expose() put in that area (lights, dimmers, covers, temperature, humidity by default; switches such as pumps, gates and door openers only through kinds), filled each time it opens (devices a plugin exposes later show up; rows added by hand come after the devices); a readonly expose() shows the state only. Prefer these to building rows by hand",
				"menu":       "Panel.menu(title, {submenus}?):toggle(label, obj|'CLU….DOU…') (a path not in the registry adds no row):dimmer(label, obj|path) (0-100 by 10, 0 = OFF):number(label, getFn, setFn, {step,min,max,format,watch}):choice(label, getFn, setFn, {options}):value(label, obj|fn, format, page?) (OK opens page, e.g. a chart):submenu(label?, menu) (an empty menu adds no row; no label: its name):action(label, fn|page, {confirm=text}); a format is a string, fn(value) or a table {[0]='closed', [1]='open'}",
				"value":      "Panel.value(title, obj|fn, format)",
				"chart":      "Panel.chart(title, obj with history | fn returning changes newest first, {hours=12, format}) — hourly bars (time-weighted mean), up/down pick an hour (up = back), its time and value on the right; nil without history, so the menu row gets no OK",
				"history":    "Panel.history(title, obj with history | fn returning changes newest first, {count=10, format}) — the last changes as a list, newest first: time and date left, value right (keep the value short, ~5 characters); e.g. when a gate opened, opened from a menu value row; nil without history",
				"corners":    "Panel.corners{tl={label, obj|path|fn(panel)}, tr=..., bl=..., br=..., clock=true, draw=fn(screen, panel), every=60} — each corner one button (a device does what its row would: a light switches, a cover opens or closes; Panel.press(obj)), clock draws the time in the middle, no back; e.g. home with a room's lights and Menu; a corner with only a label keeps its LED off",
				"toggle":     "Panel.toggle(obj, title)",
				"thermostat": "menu:thermostat(label, obj|path, {readonly=true, step=0.5, min, max, format}) — a Grenton thermostat (RemoteThermostat): the row shows the temperature, OK opens a page with the target; readonly shows them without + and -, otherwise + and - write the set point in manual mode only (holiday/auto: the page shows; min/max bound them). Panel.thermostat{name, current=obj|fn, get=fn, set=fn|nil, step, min, max, format} for any other source; without set it only shows",
				"idle":       "Panel.idle{ {label, obj|fn, format}, ... } — clock and up to 3 values; any button opens home",
				"cleaning":   "Panel.cleaning(seconds) — inputs ignored, countdown",
				"own":        "Panel.register(name, factory): a page is {draw=function(screen, panel), keys={up={label,fn},down=…,ok=…}|fn, input=function(role, panel), watch={objs}, every=s}",
			},
			"screen":        "screen:text(x, y, s, font 1-3), textRight, textCenter, line, rule(y), point, icon(x, y, 'name' of a .bmp on the panel) — 128x64, fonts 10/14/28 px, y is the top, 15 characters",
			"helpers":       "panel:notify(text, {seconds, beep}), panel:open(page), panel:goBack(), panel:goHome(), panel:redraw(), panel:debounce(key, s, fn)",
			"objectManager": "every button event (OnClick 'btn1'..'btn4', OnLongPress 'btn1:long') calls click(panel or button id, input) on vCLU; an empty click() from OM in user.lua hides the runtime one, so the module sets click = RemotePanel.click",
			"sandbox":       "vclu_draft_test steps {\"click\": \"PAN…\", \"input\": \"btn3\"} then {\"advance\": 1}: the frame drawn is in clu_calls",
		},
		"tasmota": map[string]any{
			"library":   "local tasmota = require('lib/tasmota'); the topic is the device's Topic (Tasmota: Configuration → MQTT, e.g. tasmota_75088A); the library binds objects to the default MQTT client, expose() stays in the module",
			"objects":   "tasmota.switch(topic, {relay=2}) → VirtualSwitch (POWER<n>); tasmota.dimmer(topic) → VirtualDimmer (Dimmer 0..100 ↔ 0..1, 0 = POWER OFF); tasmota.sensor(topic, 'ENERGY.Power', {unit='W'}) → read-only VirtualNumber from tele/<topic>/SENSOR and Status 10 (expose it with readonly=true); the device keeps Tasmota's default Full Topic %prefix%/%topic%/",
			"behaviour": "state from stat/<topic>/POWER<n>, RESULT and tele/<topic>/STATE; a state from the device is never sent back as a command; the state is asked for on start, on MQTT connect and on LWT Online; a change from the device is recorded with actor device:<topic>",
			"helpers":   "tasmota.online(topic) (LWT); tasmota.devices() lists {topic, name, device, relays, lights, ip, mac} from Tasmota's own discovery (tasmota/discovery/<mac>/config), nothing is created from it",
			"sandbox":   "vclu_draft_test step {\"mqtt\": \"stat/<topic>/POWER\", \"payload\": \"ON\"} delivers a message; commands show as mqtt.publish in blocked_calls",
		},
		"dashboard": map[string]any{
			"builder":  "Dashboard.new(id):name():icon():columns():auth():pin():poll():theme():width('full'|'normal')",
			"width":    "width('full'): tiles edge to edge with an 8 px gutter (wall tablets), the home-screen app opens full screen; 'normal' (default) keeps wide screens centred at 1100 px; :columns(n) still sets the columns",
			"app":      "every dashboard has a manifest (/d/<id>/manifest.webmanifest: name and theme background only, no auth needed) and meta tags: Safari Share → Add to Home Screen (iPad), Chrome menu → Add to Home screen / Install app (Android) opens it without browser bars",
			"sections": "dash:header() first, then dash:section(name, icon, {columns=n, covers=true}) — covers=true: the header counts the section's covers open, moving and closed",
			"header":   "header:status/toggle/button/link as small chips, and header:chip(id, icon, fn, {color=string|fn, link=URL|'/path'|dashboard id, disabled=fn}) — fn() -> text, nil (chip hidden) or {text, icon, color}; header only",
			"rooms":    "section:area(area|{areas}, {kinds, skip={paths}}) fills the section with a widget per device expose() put in that area (expose(..., {area=..., floor=...})): light/switch/lock a toggle, dimmer a dimmer, RGB light a colour widget, cover a cover, temperature/humidity a status; kinds default light, dimmer, cover, temperature, humidity (switch and lock only through kinds); lights first, then covers, then by name; widget ids 'area:'..path (stable; actions use them); filled on every read, so devices exposed later show up; widgets added by hand come after them; a readonly expose() makes the widget disabled; a thermostat (expose(th, 'climate', {min, max})) only through kinds={'climate', ...} or dashboard=true, with the expose min/max (default 5-35). dash:floor(floor, {order, titles={[area]=title}, kinds, skip}) adds one section per area of that floor, empty ones left out. expose(..., {dashboard=false}) keeps an object off dashboards, dashboard=true adds one of the kinds with a widget (light, switch, dimmer, RGB light, cover, temperature, humidity, lock, climate) even outside kinds; other kinds get none and a log warning (independent of panel=). A dashboard with rooms gives whoever opens it control of everything exposed into those areas later, and MQTT/HomeKit/MCP access profiles do not apply to dashboards: use :auth('session') or 'pin', not the default 'public'. After dash:floor() start a section() before adding widgets. Prefer these to building room widgets by hand",
			"room":     "section:room(id, area|{areas}, {title, temperature=path|fn, humidity=path|fn, kinds, skip, icon, link=dashboard id|'/path'|http(s) URL, disabled, span, size, color}) — one tile per room: its name, 'N włączone' (lights, dimmers above 0, switches when shown) and 'N otwarte' (covers above 0 or moving), its temperature and humidity (default: the area's devices of that kind, the average of several, none left out; a path or a function for others); a tap opens the room's widgets (as section:area: same kinds, skip and ids 'area:'..path, '#n' when the area is also shown elsewhere) over the page, the ‹ button, Escape, a tap outside or the system back button closes it, full screen on phones; link opens that page instead; disabled also disables the room's widgets. dash:floor(floor, {rooms=true, order, titles, kinds, skip}) makes one section named after the floor with a room tile per area (ids 'area:@'..area); filled again only when what is exposed changes",
			"widgets": map[string]any{
				"toggle":     "section:toggle(id, label, icon, getValueFn, onToggleFn, opts)",
				"slider":     "section:slider(id, label, min, max, getValueFn, onSetFn, opts)",
				"status":     "section:status(id, label, icon, getValueFn, unit, opts) — return a number (nil shows --), not a formatted string: opts.decimals (0-6) rounds it on the page, with the viewer's decimal separator (21,4 in Polish); opts.color may be function() -> color|nil, re-read on every poll",
				"button":     "section:button(id, label, icon, onTapFn, opts)",
				"link":       "section:link(id, label, icon, url, opts)",
				"cover":      "section:cover(id, label, icon, fns, opts) — fns={getPosition,getState,open,close,stop,set?}",
				"covers":     "section:covers(id, label, list, {icon, disabled, span, size, color}) — one tile for several covers: list = covers or paths, _:byTag(...), or a function returning one (read each time); up/stop/down/position on each, 50 ms apart (a new movement or slat command drops the rest of the previous one of its kind, stop ends both), slats only on covers reporting getTilt; shows the counts and the average position",
				"dimmer":     "section:dimmer(id, label, icon, fns, opts) — fns={get?,set,switchOn?,switchOff?,isOn?}",
				"thermostat": "section:thermostat(id, label, icon|nil, fns, {min=5, max=35, step=0.5, unit='°C', readonly, disabled, span, size, color}) — fns={getCurrent, getTarget, setTarget?, getState? (1/true on, 0/false off), getMode? (0 manual, 1 holiday, 2 schedule, 3 heat-up)} or a RemoteThermostat (temperature getValue, target getTarget, setSetpoint, state, mode); current and target large, '--' until reported; - and + change the target by step, setTarget is called once 1 s after the last tap and the chosen value shows until the thermostat reports a new target (at most 90 s); no buttons without setTarget, with readonly=true, while readonly (a function) returns true (the tile says locked; it unlocks when it returns false), or while getMode is not 0 (a Grenton thermostat on holiday or schedule follows its own target); the endpoint refuses a set that is not a number within min..max, read-only or outside manual mode",
				"color":      "section:color(id, label, icon, fns, opts) — fns={getColor,setColor,get?,set?,switchOn?,switchOff?,isOn?,getWhite?,setWhite?} or a RemoteRGBLight object, opts.presets=[{label,r,g,b}]",
				"player":     "section:player(id, label, src, {icon, disabled, span, size, color}) — src = a MusicCast device (Plugin.getPlugin('@vclu/musiccast'):create{...}) or {getState=fn -> {playing, title, artist, album, artwork (http(s) URL), volume 0-100, muted, power, online}, play, pause, next, previous, setVolume(p), setMute(b), on, off}; a missing function hides its button; artwork is loaded by the browser from the device (LAN only, a note icon otherwise); two columns wide by default",
				"forecast":   "section:forecast(id, label, src, {days=1-7 (5), hours=0-48 (24, 0 hides the chart), unit='°C', icon, disabled, span, size, color}) — src = the Weather plugin (Plugin.getPlugin('@vclu/weather')) or fn -> {days={{date='YYYY-MM-DD', iconName|icon, min, max, rain (mm), pop 0-1 or a percent above 1}}, hours={{at (unix s), temp, rain}}, updated? (unix s, shown as 'odświeżono HH:MM')} or nil (shows 'brak prognozy'); past days dropped in the house's zone; hours is a count: the current hour and hours-1 after it; a row of days and a temperature line over rain bars; two columns wide by default",
				"calendar":   "section:calendar(id, label, calendar or its id, {view='month'|'day', tags, schedules={ids} or true for all, holidays=true, namedays=false, sun=true}) (what is off is not sent; a viewer hides the rest) — month grid, a tapped day shows the day with a 24 h bar per schedule; full width",
			},
			"opts":       "{ span=n, size='lg', color='#hex', disabled=function() }",
			"auth_types": "public | pin | session",
		},
		"calendar": map[string]any{
			"builder":  "Calendar.new(id):name():holidays('pl'):location(lat, lon):notify(fn | obj with :send(e)):clock({wait=false}):event(id, opts):schedule(id, {default=v (required), values={v, ...} (required when the values are text; a window value not listed fails build()), title, colors={value=color}, {window, value=v}, ...}):build(); remind needs a sign ('-12h' before, '+1h' after); workday/holiday need :holidays()",
			"rules":    "every='day'|'workday'|'weekend'|'holiday'|'mo'..'su'|{'mo','we'}|'month' (day=10|-1, on='first mo'|'last workday')|'year' (date='MM-DD', or month=10 with on='last su'); dates={'YYYY-MM-DD',...} (a published list of days); weeks|months|years=n with from; from, to, times; one-off date='YYYY-MM-DD' or 'a..b' (all day); time='HH:MM' | 'sunset-30m' | 'sunrise+15m' | 'dawn' | 'dusk' (needs location; none: all day), duration='6h' or hours='13:00-15:00' | {'sunset','sunrise'}; sun={azimuth={135,225}, elevation=15 | {low,high}} = the hours the sun stands there (shading by facade); except={dates or ranges}; moves={['date']='date'}",
			"event":    "opts: title, icon, tags, remind='-12h'|{...}, notify, catchup=true (start again after a restart or clock jump during it), jitter='15m'",
			"handlers": "cal:on('start'|'end'|'reminder'|'change'|'ready', [id], fn) → unsubscribe; change gives (value, old), old nil the first time",
			"queries":  "Calendar.get(id), cal:value(schedule), cal:nextChange(schedule) → {value, at}, cal:color(schedule[, value]), cal:segments(schedule, date | from, to), cal:between(from, to, {tags}) (dates or timestamps), cal:day(date) → {date, weekday, workday, holiday, holidayName, namedays, starts, ends}, cal:sun(date) → {sunrise, sunset, dawn, dusk, noon, dayLength} | {polar}, cal:sunPosition([ts]) → {azimuth, elevation}, cal:isActive(event), cal:next(event), cal:today({tags}), cal:upcoming(days, {tags}), cal:isWorkday(date), cal:isHoliday(date), cal:isReady(), cal:info()",
			"runtime":  "cal:add(opts) → id (saved), cal:remove(id), cal:skip(id, date), cal:unskip(id, date), cal:pause('a..b', {tags}), cal:resume(), cal:paused()",
			"clock":    "nothing fires until NTP synchronised the clock (10 min at most, never before 2025); moments skipped by a clock jump do not fire; the sandbox's virtual clock drives it on advance",
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
