package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	mcplib "github.com/mark3labs/mcp-go/mcp"
)

// registerResources registers MCP resources (context for AI models).
// Uses the same DeviceProvider as tools — single data source.
func (s *MCPServer) registerResources() {
	// vclu://devices — snapshot of all visible devices
	s.server.AddResource(
		mcplib.Resource{
			URI:         "vclu://devices",
			Name:        "Device List",
			Description: "All exposed smart home devices with current state",
			MIMEType:    "application/json",
		},
		s.handleDevicesResource,
	)

	// vclu://device/{path} — uses resource template
	s.server.AddResourceTemplate(
		mcplib.NewResourceTemplate(
			"vclu://device/{path}",
			"Device Detail",
			mcplib.WithTemplateDescription("Detailed information about a specific device"),
			mcplib.WithTemplateMIMEType("application/json"),
		),
		s.handleDeviceResource,
	)

	// vclu://docs/house — house description from docs/ai/house.md
	s.server.AddResource(
		mcplib.Resource{
			URI:         "vclu://docs/house",
			Name:        "House Description",
			Description: "Description of the house: rooms, devices, logic, rules",
			MIMEType:    "text/markdown",
		},
		s.handleDocsHouseResource,
	)

	// vclu://docs/examples — code cookbook from docs/ai/examples.md
	s.server.AddResource(
		mcplib.Resource{
			URI:         "vclu://docs/examples",
			Name:        "Code Examples",
			Description: "Cookbook with code patterns: scenes, automations, schedules, HTTP endpoints",
			MIMEType:    "text/markdown",
		},
		s.handleDocsExamplesResource,
	)

	// vclu://context/agent-notes — persistent agent memory
	s.server.AddResource(
		mcplib.Resource{
			URI:         "vclu://context/agent-notes",
			Name:        "Agent Notes",
			Description: "Persistent notes from previous agent sessions (observations, TODOs, constraints)",
			MIMEType:    "text/markdown",
		},
		s.handleAgentNotesResource,
	)

	// vclu://incidents/latest — most recent incident
	s.server.AddResource(
		mcplib.Resource{
			URI:         "vclu://incidents/latest",
			Name:        "Latest Incident",
			Description: "Most recent incident snapshot (rollback, watchdog trigger, or guardrail block)",
			MIMEType:    "text/markdown",
		},
		s.handleIncidentLatestResource,
	)

	// vclu://incidents/list — all incidents
	s.server.AddResource(
		mcplib.Resource{
			URI:         "vclu://incidents/list",
			Name:        "Incident List",
			Description: "List of all incidents from the last 30 days",
			MIMEType:    "application/json",
		},
		s.handleIncidentListResource,
	)

	// vclu://incidents/{id} — specific incident
	s.server.AddResourceTemplate(
		mcplib.NewResourceTemplate(
			"vclu://incidents/{id}",
			"Incident Detail",
			mcplib.WithTemplateDescription("Read a specific incident by ID"),
			mcplib.WithTemplateMIMEType("text/markdown"),
		),
		s.handleIncidentDetailResource,
	)

	// vclu://files/{path} — read any allowed file
	s.server.AddResourceTemplate(
		mcplib.NewResourceTemplate(
			"vclu://files/{path}",
			"File Contents",
			mcplib.WithTemplateDescription("Read a file from the data directory (Lua code, documentation)"),
			mcplib.WithTemplateMIMEType("text/plain"),
		),
		s.handleFileResource,
	)
}

func (s *MCPServer) handleDevicesResource(ctx context.Context, req mcplib.ReadResourceRequest) ([]mcplib.ResourceContents, error) {
	devices, err := s.devices.ListDevices(ctx, "")
	if err != nil {
		return nil, fmt.Errorf("failed to list devices: %w", err)
	}

	// Filter by ACL (bot-aware)
	var visible []Device
	for _, d := range devices {
		if s.botCanRead(ctx, d.Path) {
			visible = append(visible, d)
		}
	}

	b, err := json.MarshalIndent(map[string]any{"devices": visible}, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("failed to marshal devices: %w", err)
	}

	return []mcplib.ResourceContents{
		mcplib.TextResourceContents{
			URI:      req.Params.URI,
			MIMEType: "application/json",
			Text:     string(b),
		},
	}, nil
}

func (s *MCPServer) handleDocsHouseResource(_ context.Context, req mcplib.ReadResourceRequest) ([]mcplib.ResourceContents, error) {
	if s.files == nil {
		return nil, fmt.Errorf("file provider not configured")
	}
	content, err := s.files.ReadFile("docs/ai/house.md")
	if err != nil {
		return nil, fmt.Errorf("failed to read house docs: %w", err)
	}
	return []mcplib.ResourceContents{
		mcplib.TextResourceContents{
			URI:      req.Params.URI,
			MIMEType: "text/markdown",
			Text:     content,
		},
	}, nil
}

func (s *MCPServer) handleDocsExamplesResource(_ context.Context, req mcplib.ReadResourceRequest) ([]mcplib.ResourceContents, error) {
	if s.files == nil {
		return nil, fmt.Errorf("file provider not configured")
	}
	content, err := s.files.ReadFile("docs/ai/examples.md")
	if err != nil {
		return nil, fmt.Errorf("failed to read examples: %w", err)
	}
	return []mcplib.ResourceContents{
		mcplib.TextResourceContents{
			URI:      req.Params.URI,
			MIMEType: "text/markdown",
			Text:     content,
		},
	}, nil
}

func (s *MCPServer) handleFileResource(_ context.Context, req mcplib.ReadResourceRequest) ([]mcplib.ResourceContents, error) {
	if s.files == nil {
		return nil, fmt.Errorf("file provider not configured")
	}

	uri := req.Params.URI
	const prefix = "vclu://files/"
	if len(uri) <= len(prefix) {
		return nil, fmt.Errorf("invalid file URI: %s", uri)
	}
	path := uri[len(prefix):]

	content, err := s.files.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read file: %w", err)
	}

	mimeType := "text/plain"
	if strings.HasSuffix(path, ".md") {
		mimeType = "text/markdown"
	} else if strings.HasSuffix(path, ".lua") {
		mimeType = "text/x-lua"
	} else if strings.HasSuffix(path, ".json") {
		mimeType = "application/json"
	}

	return []mcplib.ResourceContents{
		mcplib.TextResourceContents{
			URI:      req.Params.URI,
			MIMEType: mimeType,
			Text:     content,
		},
	}, nil
}

func (s *MCPServer) handleAgentNotesResource(_ context.Context, req mcplib.ReadResourceRequest) ([]mcplib.ResourceContents, error) {
	if s.agentCtx == nil {
		return nil, fmt.Errorf("agent context not configured")
	}
	content, err := s.agentCtx.Read()
	if err != nil {
		return nil, err
	}
	if content == "" {
		content = "No agent notes yet. Use vclu_context_save to add notes."
	}
	return []mcplib.ResourceContents{
		mcplib.TextResourceContents{
			URI:      req.Params.URI,
			MIMEType: "text/markdown",
			Text:     content,
		},
	}, nil
}

func (s *MCPServer) handleIncidentLatestResource(_ context.Context, req mcplib.ReadResourceRequest) ([]mcplib.ResourceContents, error) {
	if s.incidents == nil {
		return nil, fmt.Errorf("incident store not configured")
	}
	content, err := s.incidents.GetLatest()
	if err != nil {
		return nil, err
	}
	if content == "" {
		content = "No incidents recorded."
	}
	return []mcplib.ResourceContents{
		mcplib.TextResourceContents{
			URI:      req.Params.URI,
			MIMEType: "text/markdown",
			Text:     content,
		},
	}, nil
}

func (s *MCPServer) handleIncidentListResource(_ context.Context, req mcplib.ReadResourceRequest) ([]mcplib.ResourceContents, error) {
	if s.incidents == nil {
		return nil, fmt.Errorf("incident store not configured")
	}
	list := s.incidents.List()
	b, err := json.MarshalIndent(map[string]any{"incidents": list, "count": len(list)}, "", "  ")
	if err != nil {
		return nil, err
	}
	return []mcplib.ResourceContents{
		mcplib.TextResourceContents{
			URI:      req.Params.URI,
			MIMEType: "application/json",
			Text:     string(b),
		},
	}, nil
}

func (s *MCPServer) handleIncidentDetailResource(_ context.Context, req mcplib.ReadResourceRequest) ([]mcplib.ResourceContents, error) {
	if s.incidents == nil {
		return nil, fmt.Errorf("incident store not configured")
	}
	uri := req.Params.URI
	const prefix = "vclu://incidents/"
	if len(uri) <= len(prefix) {
		return nil, fmt.Errorf("invalid incident URI: %s", uri)
	}
	id := uri[len(prefix):]
	content, err := s.incidents.Get(id)
	if err != nil {
		return nil, err
	}
	return []mcplib.ResourceContents{
		mcplib.TextResourceContents{
			URI:      req.Params.URI,
			MIMEType: "text/markdown",
			Text:     content,
		},
	}, nil
}

func (s *MCPServer) handleDeviceResource(ctx context.Context, req mcplib.ReadResourceRequest) ([]mcplib.ResourceContents, error) {
	// Extract path from URI: vclu://device/CLU220000592.DOU5048
	uri := req.Params.URI
	const prefix = "vclu://device/"
	if len(uri) <= len(prefix) {
		return nil, fmt.Errorf("invalid device URI: %s", uri)
	}
	path := uri[len(prefix):]

	if !s.botCanRead(ctx, path) {
		return nil, fmt.Errorf("device not found or hidden: %s", path)
	}

	device, err := s.devices.GetDevice(ctx, path)
	if err != nil {
		return nil, fmt.Errorf("failed to get device: %w", err)
	}

	b, err := json.MarshalIndent(device, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("failed to marshal device: %w", err)
	}

	return []mcplib.ResourceContents{
		mcplib.TextResourceContents{
			URI:      req.Params.URI,
			MIMEType: "application/json",
			Text:     string(b),
		},
	}, nil
}
