package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Domktorymysli/vclu/pkg/accesscontrol"
	mcplib "github.com/mark3labs/mcp-go/mcp"
)

// validPath matches safe device paths: alphanumeric, dots, colons, underscores, hyphens.
var validPath = regexp.MustCompile(`^[a-zA-Z0-9._:\-]{1,128}$`)

// registerTools registers all 6 MCP tools on the server.
func (s *MCPServer) registerTools() {
	s.addTool("vclu_devices", "List all exposed smart home devices with their current state", s.handleDevices,
		map[string]any{
			"type": map[string]any{
				"type":        "string",
				"description": "Filter by device type (switch, light, dimmer, cover, sensor, scene, etc.)",
			},
		}, nil)

	s.addTool("vclu_device_get", "Get the current state and details of a specific device", s.handleDeviceGet,
		map[string]any{
			"path": map[string]any{
				"type":        "string",
				"description": "Device path (e.g., CLU220000592.DOU5048)",
			},
		}, []string{"path"})

	s.addTool("vclu_device_set", "Set the value of a device (turn on/off, set brightness, etc.)", s.handleDeviceSet,
		map[string]any{
			"path": map[string]any{
				"type":        "string",
				"description": "Device path",
			},
			"value": map[string]any{
				"description": "Value to set (1/0 for switches, 0..1 for dimmers, 0-100 for cover position, etc.)",
			},
		}, []string{"path", "value"})

	s.addTool("vclu_device_execute", "Execute a method on a device (toggle, switchOn, switchOff, etc.)", s.handleDeviceExecute,
		map[string]any{
			"path": map[string]any{
				"type":        "string",
				"description": "Device path",
			},
			"method": map[string]any{
				"type":        "string",
				"description": "Method name to execute",
			},
			"args": map[string]any{
				"type":        "array",
				"description": "Optional arguments for the method",
				"items":       map[string]any{},
			},
		}, []string{"path", "method"})

	s.addTool("vclu_scenes", "List all available scenes", s.handleScenes, nil, nil)

	s.addTool("vclu_scene_execute", "Execute a scene (e.g., Movie Mode, Good Night)", s.handleSceneExecute,
		map[string]any{
			"path": map[string]any{
				"type":        "string",
				"description": "Scene path",
			},
		}, []string{"path"})
}

// addTool is a helper to register a tool with schema.
func (s *MCPServer) addTool(name, description string, handler func(ctx context.Context, req mcplib.CallToolRequest) (*mcplib.CallToolResult, error), props map[string]any, required []string) {
	tool := mcplib.Tool{
		Name:        name,
		Description: description,
		InputSchema: mcplib.ToolInputSchema{
			Type:       "object",
			Properties: props,
		},
	}
	if required != nil {
		tool.InputSchema.Required = required
	}
	s.server.AddTool(tool, func(ctx context.Context, req mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
		if err := checkArgs(name, props, required, req.GetArguments()); err != nil {
			return toolError(err.Error()), nil
		}
		return handler(ctx, req)
	})
}

// argAliases are names agents send for an argument that is called differently here.
var argAliases = map[string][]string{
	"search": {"filter", "pattern", "query"},
	"query":  {"filter", "pattern"},
	"grep":   {"filter", "pattern"},
	"regex":  {"filter", "pattern"},
	"lines":  {"limit"},
	"count":  {"limit"},
	"max":    {"limit", "max_results"},
	"n":      {"limit"},
	"code":   {"script", "content"},
	"lua":    {"script", "content"},
	"source": {"script", "content"},
	"file":   {"path"},
	"name":   {"path", "id"},
	"glob":   {"pattern"},
	"text":   {"content"},
	"body":   {"content"},
}

// checkArgs refuses an argument the tool's schema does not name, and a missing required one.
func checkArgs(tool string, props map[string]any, required []string, args map[string]any) error {
	valid := make([]string, 0, len(props))
	for k := range props {
		valid = append(valid, k)
	}
	sort.Strings(valid)

	var unknown []string
	for k := range args {
		if _, ok := props[k]; !ok {
			unknown = append(unknown, k)
		}
	}
	if len(unknown) > 0 {
		sort.Strings(unknown)
		if len(valid) == 0 {
			return fmt.Errorf("unknown argument %q: %s takes no arguments", unknown[0], tool)
		}
		msg := fmt.Sprintf("unknown argument %q for %s", unknown[0], tool)
		if s := suggestArg(unknown[0], props, valid); s != "" {
			msg += fmt.Sprintf("; did you mean %q?", s)
		} else {
			msg += "."
		}
		return fmt.Errorf("%s Valid: %s", msg, strings.Join(valid, ", "))
	}
	if err := numberArgs(tool, props, args); err != nil {
		return err
	}
	for _, r := range required {
		if _, ok := args[r]; !ok {
			return fmt.Errorf("missing required argument %q for %s. Valid: %s", r, tool, strings.Join(valid, ", "))
		}
	}
	return nil
}

// numberArgs makes a number argument float64 in place (handlers read it so): a numeric string
// such as "10" is taken, anything else that is not a number is refused, not ignored.
func numberArgs(tool string, props map[string]any, args map[string]any) error {
	for k, v := range args {
		if p, _ := props[k].(map[string]any); p["type"] != "number" && p["type"] != "integer" {
			continue
		}
		switch n := v.(type) {
		case nil, float64:
		case int:
			args[k] = float64(n)
		case int64:
			args[k] = float64(n)
		case string:
			f, err := strconv.ParseFloat(strings.TrimSpace(n), 64)
			if err != nil || math.IsNaN(f) || math.IsInf(f, 0) {
				return fmt.Errorf("argument %q for %s must be a number, got %q", k, tool, n)
			}
			args[k] = f
		default:
			return fmt.Errorf("argument %q for %s must be a number, got %v", k, tool, v)
		}
	}
	return nil
}

// suggestArg names the valid argument closest to name: a known alias, else at most 2 edits away.
func suggestArg(name string, props map[string]any, valid []string) string {
	lower := strings.ToLower(name)
	if _, ok := props[lower]; ok {
		return lower
	}
	for _, a := range argAliases[lower] {
		if _, ok := props[a]; ok {
			return a
		}
	}
	best, bestDist := "", 3
	for _, v := range valid {
		if d := editDistance(lower, v); d < bestDist && d < len(v) {
			best, bestDist = v, d
		}
	}
	return best
}

func editDistance(a, b string) int {
	prev := make([]int, len(b)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(a); i++ {
		cur := make([]int, len(b)+1)
		cur[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
		}
		prev = cur
	}
	return prev[len(b)]
}

// --- Bot-aware access helpers ---

// botCanRead checks if the bot (from context) can read the object.
// Zero-trust: no bot or no profiles → deny access.
func (s *MCPServer) botCanRead(ctx context.Context, path string) bool {
	bot := botFromContext(ctx)
	if bot == nil || len(bot.Profiles) == 0 {
		return false
	}
	level := s.access.ResolveAccess(bot.Profiles, path)
	return level != accesscontrol.AccessHidden
}

// botCanControl checks if the bot (from context) can control the object.
// Zero-trust: no bot or no profiles → deny access.
func (s *MCPServer) botCanControl(ctx context.Context, path string) bool {
	bot := botFromContext(ctx)
	if bot == nil || len(bot.Profiles) == 0 {
		return false
	}
	level := s.access.ResolveAccess(bot.Profiles, path)
	return level == accesscontrol.AccessFull
}

// --- Tool Handlers ---

func (s *MCPServer) handleDevices(ctx context.Context, req mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
	start := time.Now()
	filterType, _ := req.GetArguments()["type"].(string)

	devices, err := s.devices.ListDevices(ctx, filterType) // Layer 1: simple type filter (backwards compat)
	if err != nil {
		s.recordActivity("vclu_devices", "", false, start, err.Error(), ctx)
		return toolError(err.Error()), nil
	}

	// Filter by ACL and apply readonly flag
	var visible []Device
	for _, d := range devices {
		if s.botCanRead(ctx, d.Path) {
			if !s.botCanControl(ctx, d.Path) {
				d.Readonly = true
			}
			visible = append(visible, d)
		}
	}

	s.recordActivity("vclu_devices", "", true, start, "", ctx)
	return toolJSON(map[string]any{"devices": visible})
}

func (s *MCPServer) handleDeviceGet(ctx context.Context, req mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
	start := time.Now()
	path, _ := req.GetArguments()["path"].(string)

	if !validPath.MatchString(path) {
		return toolError("invalid device path"), nil
	}

	if !s.botCanRead(ctx, path) {
		s.recordActivity("vclu_device_get", path, false, start, "access denied", ctx)
		return toolError("device not found or hidden: " + path), nil
	}

	device, err := s.devices.GetDevice(ctx, path)
	if err != nil {
		s.recordActivity("vclu_device_get", path, false, start, err.Error(), ctx)
		return toolError(err.Error()), nil
	}

	// Apply readonly flag from ACL
	if !s.botCanControl(ctx, path) {
		device.Readonly = true
	}

	s.recordActivity("vclu_device_get", path, true, start, "", ctx)
	return toolJSON(device)
}

func (s *MCPServer) handleDeviceSet(ctx context.Context, req mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
	start := time.Now()
	args := req.GetArguments()
	path, _ := args["path"].(string)
	value := args["value"]

	if !validPath.MatchString(path) {
		return toolError("invalid device path"), nil
	}

	if !s.botCanControl(ctx, path) {
		s.recordActivity("vclu_device_set", path, false, start, "access denied", ctx)
		return toolError("cannot control device: " + path + " (readonly or hidden)"), nil
	}

	result, err := s.devices.SetValue(ctx, path, value)
	if err != nil {
		s.recordActivity("vclu_device_set", path, false, start, err.Error(), ctx)
		return toolError(err.Error()), nil
	}

	s.recordActivity("vclu_device_set", path, true, start, "", ctx)
	return toolJSON(result)
}

func (s *MCPServer) handleDeviceExecute(ctx context.Context, req mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
	start := time.Now()
	args := req.GetArguments()
	path, _ := args["path"].(string)
	method, _ := args["method"].(string)

	if !validPath.MatchString(path) {
		return toolError("invalid device path"), nil
	}

	if !s.botCanControl(ctx, path) {
		s.recordActivity("vclu_device_execute", path, false, start, "access denied", ctx)
		return toolError("cannot control device: " + path + " (readonly or hidden)"), nil
	}

	// Parse args array
	var execArgs []any
	if rawArgs, ok := args["args"]; ok {
		if arr, ok := rawArgs.([]any); ok {
			execArgs = arr
		}
	}

	result, err := s.devices.ExecuteMethod(ctx, path, method, execArgs)
	if err != nil {
		s.recordActivity("vclu_device_execute", path, false, start, err.Error(), ctx)
		return toolError(err.Error()), nil
	}

	s.recordActivity("vclu_device_execute", path, true, start, "", ctx)
	return toolJSON(result)
}

func (s *MCPServer) handleScenes(ctx context.Context, _ mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
	start := time.Now()

	scenes, err := s.devices.ListScenes(ctx)
	if err != nil {
		s.recordActivity("vclu_scenes", "", false, start, err.Error(), ctx)
		return toolError(err.Error()), nil
	}

	// Filter by ACL
	var visible []Scene
	for _, sc := range scenes {
		if s.botCanRead(ctx, sc.Path) {
			visible = append(visible, sc)
		}
	}

	s.recordActivity("vclu_scenes", "", true, start, "", ctx)
	return toolJSON(map[string]any{"scenes": visible})
}

func (s *MCPServer) handleSceneExecute(ctx context.Context, req mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
	start := time.Now()
	path, _ := req.GetArguments()["path"].(string)

	if !validPath.MatchString(path) {
		return toolError("invalid scene path"), nil
	}

	if !s.botCanControl(ctx, path) {
		s.recordActivity("vclu_scene_execute", path, false, start, "access denied", ctx)
		return toolError("cannot execute scene: " + path), nil
	}

	if err := s.devices.ExecuteScene(ctx, path); err != nil {
		s.recordActivity("vclu_scene_execute", path, false, start, err.Error(), ctx)
		return toolError(err.Error()), nil
	}

	s.recordActivity("vclu_scene_execute", path, true, start, "", ctx)
	return toolJSON(map[string]any{"ok": true})
}

// --- Helpers ---

func (s *MCPServer) recordActivity(tool, path string, success bool, start time.Time, errMsg string, ctx context.Context) {
	latency := time.Since(start)
	s.metrics.RecordMCPRequest(tool, !success, latency)

	remoteIP := ""
	if r, ok := ctx.Value(remoteIPKey).(string); ok {
		remoteIP = r
	}

	botName := ""
	if bot := botFromContext(ctx); bot != nil {
		botName = bot.Name
	}

	s.metrics.AddMCPActivity(ActivityEntry{
		Time:     start,
		Tool:     tool,
		Path:     path,
		Success:  success,
		Duration: float64(latency.Milliseconds()),
		Error:    errMsg,
		RemoteIP: remoteIP,
		BotName:  botName,
	})
}

func toolJSON(v any) (*mcplib.CallToolResult, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return toolError(fmt.Sprintf("JSON marshal error: %v", err)), nil
	}
	return &mcplib.CallToolResult{
		Content: []mcplib.Content{
			mcplib.TextContent{Type: "text", Text: string(b)},
		},
	}, nil
}

func toolError(msg string) *mcplib.CallToolResult {
	return &mcplib.CallToolResult{
		Content: []mcplib.Content{
			mcplib.TextContent{Type: "text", Text: msg},
		},
		IsError: true,
	}
}

// Context keys
type contextKey string

const (
	remoteIPKey contextKey = "remote_ip"
	botKey      contextKey = "mcp_bot"
	sessionKey  contextKey = "mcp_session"
)
