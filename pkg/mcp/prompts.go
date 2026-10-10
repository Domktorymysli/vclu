package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	mcplib "github.com/mark3labs/mcp-go/mcp"
)

// registerPrompts registers MCP prompts (structured conversation templates).
func (s *MCPServer) registerPrompts() {
	// create_scene — boilerplate for a new scene
	s.server.AddPrompt(
		mcplib.NewPrompt("create_scene",
			mcplib.WithPromptDescription("Generate boilerplate for a new Lua scene with expose()"),
			mcplib.WithArgument("name", mcplib.ArgumentDescription("Scene name (e.g., Movie Mode)"), mcplib.RequiredArgument()),
			mcplib.WithArgument("devices", mcplib.ArgumentDescription("Comma-separated device paths to include")),
			mcplib.WithArgument("area", mcplib.ArgumentDescription("Room/area for the scene")),
		),
		s.handleCreateScenePrompt,
	)

	// debug_device — diagnostic template for a device
	s.server.AddPrompt(
		mcplib.NewPrompt("debug_device",
			mcplib.WithPromptDescription("Generate a diagnostic report for a device: state, logs, related devices, suggestions"),
			mcplib.WithArgument("path", mcplib.ArgumentDescription("Device path (e.g., custom:living_main)"), mcplib.RequiredArgument()),
		),
		s.handleDebugDevicePrompt,
	)

	// optimize_automation — analyze existing code
	s.server.AddPrompt(
		mcplib.NewPrompt("optimize_automation",
			mcplib.WithPromptDescription("Analyze Lua automation code for issues: redundant calls, missing guards, timing problems"),
			mcplib.WithArgument("file", mcplib.ArgumentDescription("File path to analyze (e.g., modules/init.lua)"), mcplib.RequiredArgument()),
		),
		s.handleOptimizePrompt,
	)

	// explain_code — explain what code does
	s.server.AddPrompt(
		mcplib.NewPrompt("explain_code",
			mcplib.WithPromptDescription("Explain what a Lua file or function does, its dependencies, and side effects"),
			mcplib.WithArgument("file", mcplib.ArgumentDescription("File path to explain"), mcplib.RequiredArgument()),
			mcplib.WithArgument("function", mcplib.ArgumentDescription("Specific function name (optional)")),
		),
		s.handleExplainPrompt,
	)
}

func (s *MCPServer) handleCreateScenePrompt(ctx context.Context, req mcplib.GetPromptRequest) (*mcplib.GetPromptResult, error) {
	name := req.Params.Arguments["name"]
	devicesStr := req.Params.Arguments["devices"]
	area := req.Params.Arguments["area"]

	if area == "" {
		area = "Salon"
	}

	// Parse device paths
	var devicePaths []string
	if devicesStr != "" {
		for _, d := range strings.Split(devicesStr, ",") {
			d = strings.TrimSpace(d)
			if d != "" {
				devicePaths = append(devicePaths, d)
			}
		}
	}

	// Look up device details from registry
	type deviceInfo struct {
		path    string
		varName string
		devType string
	}
	var devices []deviceInfo
	for _, path := range devicePaths {
		dev, err := s.devices.GetDevice(ctx, path)
		varName := strings.ReplaceAll(path, ":", "_")
		varName = strings.ReplaceAll(varName, ".", "_")
		if err != nil {
			devices = append(devices, deviceInfo{path: path, varName: varName, devType: "unknown"})
		} else {
			devices = append(devices, deviceInfo{path: path, varName: varName, devType: dev.Type})
		}
	}

	// Build function name from scene name
	funcName := strings.ReplaceAll(strings.ToLower(name), " ", "_")
	funcName = strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_' {
			return r
		}
		return -1
	}, funcName)

	scenePath := "scene:" + funcName

	// Generate boilerplate
	var code strings.Builder
	code.WriteString(fmt.Sprintf("local function %s()\n", funcName))
	for _, d := range devices {
		code.WriteString(fmt.Sprintf("    local %s = _registry:get(%q)\n", d.varName, d.path))
	}
	code.WriteString("\n")
	for _, d := range devices {
		if d.devType == "dimmer" {
			fmt.Fprintf(&code, "    %s:set(0, VALUE) -- 0/1, dimmer 0..1\n", d.varName)
		} else if d.devType == "cover" {
			fmt.Fprintf(&code, "    %s:set(0, VALUE) -- 0=closed, 100=open\n", d.varName)
		} else {
			fmt.Fprintf(&code, "    %s:set(0, VALUE) -- 0/1 or specific value\n", d.varName)
		}
	}
	code.WriteString("end\n\n")

	code.WriteString(fmt.Sprintf("local %sScene = { execute = function(self) %s() end }\n", funcName, funcName))
	code.WriteString(fmt.Sprintf("expose(%sScene, \"scene\", {\n", funcName))
	code.WriteString(fmt.Sprintf("    name = %q,\n", name))
	code.WriteString(fmt.Sprintf("    path = %q,\n", scenePath))
	code.WriteString(fmt.Sprintf("    area = %q,\n", area))
	code.WriteString("    group = \"Sceny\",\n")
	code.WriteString("}):description(\"TODO: opis sceny\")\n")

	// Build the full prompt response
	var msg strings.Builder
	msg.WriteString(fmt.Sprintf("## Scene: %s\n\n", name))
	msg.WriteString("### Boilerplate\n```lua\n")
	msg.WriteString(code.String())
	msg.WriteString("```\n\n")
	msg.WriteString("### Checklist\n")
	msg.WriteString("- [ ] Replace VALUE with actual values (0..1 for dimmers, 0/1 for switches)\n")
	msg.WriteString("- [ ] Add description explaining what the scene does\n")
	msg.WriteString("- [ ] Check if devices are not readonly\n")
	msg.WriteString("- [ ] Consider adding a reverse scene (e.g., scene:normal)\n")
	msg.WriteString("- [ ] Test in sandbox: vclu_draft_test\n")

	return &mcplib.GetPromptResult{
		Description: fmt.Sprintf("Create scene: %s", name),
		Messages: []mcplib.PromptMessage{
			mcplib.NewPromptMessage(mcplib.RoleAssistant, mcplib.NewTextContent(msg.String())),
		},
	}, nil
}

func (s *MCPServer) handleDebugDevicePrompt(ctx context.Context, req mcplib.GetPromptRequest) (*mcplib.GetPromptResult, error) {
	path := req.Params.Arguments["path"]
	if path == "" {
		return nil, fmt.Errorf("path is required")
	}

	var msg strings.Builder
	msg.WriteString(fmt.Sprintf("## Debug: %s\n\n", path))

	// Get device state
	dev, err := s.devices.GetDevice(ctx, path)
	if err != nil {
		msg.WriteString(fmt.Sprintf("**Device not found:** %v\n\n", err))
		msg.WriteString("### Suggestions\n")
		msg.WriteString("- Check if the device path is correct (use vclu_devices to list all)\n")
		msg.WriteString("- Check if the device is exposed (it might be hidden in access control)\n")
	} else {
		msg.WriteString("### State\n")
		msg.WriteString(fmt.Sprintf("- Type: %s\n", dev.Type))
		stateJSON, _ := json.Marshal(dev.State)
		msg.WriteString(fmt.Sprintf("- Value: %s\n", string(stateJSON)))
		msg.WriteString(fmt.Sprintf("- Readonly: %v\n", dev.Readonly))
		if dev.Area != "" {
			msg.WriteString(fmt.Sprintf("- Area: %s\n", dev.Area))
		}
		if dev.Group != "" {
			msg.WriteString(fmt.Sprintf("- Group: %s\n", dev.Group))
		}
		if len(dev.Tags) > 0 {
			msg.WriteString(fmt.Sprintf("- Tags: %s\n", strings.Join(dev.Tags, ", ")))
		}
		if dev.Description != "" {
			msg.WriteString(fmt.Sprintf("- Description: %s\n", dev.Description))
		}
		msg.WriteString(fmt.Sprintf("- Methods: %s\n", strings.Join(dev.Methods, ", ")))
		if len(dev.Events) > 0 {
			msg.WriteString(fmt.Sprintf("- Events: %s\n", strings.Join(dev.Events, ", ")))
		}
		msg.WriteString("\n")

		// Check for dependsOn tags
		var dependencies []string
		for _, tag := range dev.Tags {
			if strings.HasPrefix(tag, "dependsOn:") {
				dependencies = append(dependencies, strings.TrimPrefix(tag, "dependsOn:"))
			}
		}
		if len(dependencies) > 0 {
			msg.WriteString("### Dependencies\n")
			for _, dep := range dependencies {
				depDev, err := s.devices.GetDevice(ctx, dep)
				if err != nil {
					msg.WriteString(fmt.Sprintf("- %s (not found)\n", dep))
				} else {
					depState, _ := json.Marshal(depDev.State)
					msg.WriteString(fmt.Sprintf("- %s (%s) = %s\n", dep, depDev.Type, string(depState)))
				}
			}
			msg.WriteString("\n")
		}

		msg.WriteString("### Suggestions\n")
		if dev.Readonly {
			msg.WriteString("- Device is readonly — can only observe, not control\n")
		}
		if len(dependencies) > 0 {
			msg.WriteString("- Check dependency devices before operating\n")
		}
		for _, tag := range dev.Tags {
			if tag == "critical" {
				msg.WriteString("- Tagged as CRITICAL — changes may affect other systems\n")
			}
			if tag == "noAutoOff" {
				msg.WriteString("- Tagged noAutoOff — do not include in auto-off automations\n")
			}
		}
		msg.WriteString("- Use vclu_logs(level=\"error\") to check for recent errors\n")
		msg.WriteString("- Read user.lua and modules/*.lua for related automation code\n")
	}

	return &mcplib.GetPromptResult{
		Description: fmt.Sprintf("Debug device: %s", path),
		Messages: []mcplib.PromptMessage{
			mcplib.NewPromptMessage(mcplib.RoleAssistant, mcplib.NewTextContent(msg.String())),
		},
	}, nil
}

func (s *MCPServer) handleOptimizePrompt(_ context.Context, req mcplib.GetPromptRequest) (*mcplib.GetPromptResult, error) {
	file := req.Params.Arguments["file"]
	if file == "" {
		return nil, fmt.Errorf("file is required")
	}

	var msg strings.Builder
	msg.WriteString(fmt.Sprintf("## Optimize: %s\n\n", file))

	// Try to read the file
	if s.files != nil {
		content, err := s.files.ReadFile(file)
		if err != nil {
			msg.WriteString(fmt.Sprintf("Cannot read file: %v\n\n", err))
			msg.WriteString("Use vclu_read_file to read the file manually.\n")
		} else {
			msg.WriteString("### Current Code\n```lua\n")
			msg.WriteString(content)
			msg.WriteString("\n```\n\n")
		}
	}

	msg.WriteString("### Analysis Checklist\n")
	msg.WriteString("- [ ] Redundant device lookups (`_registry:get()` called multiple times for same device)\n")
	msg.WriteString("- [ ] Missing nil guards (what if `_registry:get()` returns nil?)\n")
	msg.WriteString("- [ ] Timer cleanup (setTimeout without clear on reset → memory leak)\n")
	msg.WriteString("- [ ] Event listener cleanup (multiple on() registrations on reload)\n")
	msg.WriteString("- [ ] Race conditions (two automations controlling same device)\n")
	msg.WriteString("- [ ] Error handling in pcall for risky operations\n")
	msg.WriteString("- [ ] Magic numbers (use named constants)\n")
	msg.WriteString("- [ ] Missing description/tags on exposed objects\n")

	return &mcplib.GetPromptResult{
		Description: fmt.Sprintf("Optimization analysis: %s", file),
		Messages: []mcplib.PromptMessage{
			mcplib.NewPromptMessage(mcplib.RoleAssistant, mcplib.NewTextContent(msg.String())),
		},
	}, nil
}

func (s *MCPServer) handleExplainPrompt(_ context.Context, req mcplib.GetPromptRequest) (*mcplib.GetPromptResult, error) {
	file := req.Params.Arguments["file"]
	funcName := req.Params.Arguments["function"]

	if file == "" {
		return nil, fmt.Errorf("file is required")
	}

	var msg strings.Builder
	if funcName != "" {
		msg.WriteString(fmt.Sprintf("## Explain: %s — function `%s`\n\n", file, funcName))
	} else {
		msg.WriteString(fmt.Sprintf("## Explain: %s\n\n", file))
	}

	// Try to read the file
	if s.files != nil {
		content, err := s.files.ReadFile(file)
		if err != nil {
			msg.WriteString(fmt.Sprintf("Cannot read file: %v\n\n", err))
			msg.WriteString("Use vclu_read_file to read the file manually.\n")
		} else {
			msg.WriteString("### Source Code\n```lua\n")
			msg.WriteString(content)
			msg.WriteString("\n```\n\n")
		}
	}

	msg.WriteString("### Explain the following\n")
	msg.WriteString("1. **Purpose**: What does this code do? What problem does it solve?\n")
	msg.WriteString("2. **Dependencies**: What devices, modules, or APIs does it use?\n")
	msg.WriteString("3. **Side effects**: What does it change? (device states, timers, HTTP endpoints)\n")
	msg.WriteString("4. **Event flow**: What events trigger this code? What events does it emit?\n")
	msg.WriteString("5. **Risks**: What could go wrong? Edge cases?\n")

	return &mcplib.GetPromptResult{
		Description: fmt.Sprintf("Code explanation: %s", file),
		Messages: []mcplib.PromptMessage{
			mcplib.NewPromptMessage(mcplib.RoleAssistant, mcplib.NewTextContent(msg.String())),
		},
	}, nil
}
