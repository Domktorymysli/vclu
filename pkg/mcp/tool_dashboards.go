package mcp

import (
	"context"
	"encoding/json"
	"time"

	mcplib "github.com/mark3labs/mcp-go/mcp"
)

// handleDashboards lists all registered dashboards with their widgets.
func (s *MCPServer) handleDashboards(ctx context.Context, _ mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
	start := time.Now()
	if err := s.requireDevMode(ctx); err != nil {
		return err, nil
	}

	if s.executor == nil {
		return toolError("Lua executor not configured"), nil
	}

	raw, err := s.executor.Execute("return Dashboard.API.list()")
	if err != nil {
		s.recordActivity("vclu_dashboards", "", false, start, err.Error(), ctx)
		return toolError(err.Error()), nil
	}

	// Parse the JSON from Lua and re-encode to ensure clean output
	var dashboards any
	if err := json.Unmarshal([]byte(raw), &dashboards); err != nil {
		// Return raw string if not valid JSON
		s.recordActivity("vclu_dashboards", "", true, start, "", ctx)
		return toolJSON(map[string]any{
			"env":        "runtime",
			"dashboards": raw,
		})
	}

	s.recordActivity("vclu_dashboards", "", true, start, "", ctx)
	return toolJSON(map[string]any{
		"env":        "runtime",
		"dashboards": dashboards,
	})
}
