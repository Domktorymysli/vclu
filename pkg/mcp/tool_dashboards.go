package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	mcplib "github.com/mark3labs/mcp-go/mcp"
)

// handleDashboards lists the registered dashboards; with an id it gives that one with its widgets.
// The filtering is here: Dashboard.API.list() also serves the web.
func (s *MCPServer) handleDashboards(ctx context.Context, req mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
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

	list, _ := dashboards.([]any)
	if id, _ := req.GetArguments()["id"].(string); id != "" {
		var known []string
		for _, d := range list {
			if m, ok := d.(map[string]any); ok {
				if m["id"] == id {
					s.recordActivity("vclu_dashboards", id, true, start, "", ctx)
					return toolJSON(map[string]any{"env": "runtime", "dashboard": m})
				}
				if k, ok := m["id"].(string); ok {
					known = append(known, k)
				}
			}
		}
		s.recordActivity("vclu_dashboards", id, false, start, "unknown dashboard", ctx)
		return toolError(fmt.Sprintf("dashboard %q not found; known: %s", id, strings.Join(known, ", "))), nil
	}

	summary := make([]map[string]any, 0, len(list))
	for _, d := range list {
		m, ok := d.(map[string]any)
		if !ok {
			continue
		}
		summary = append(summary, map[string]any{
			"id": m["id"], "name": m["name"], "auth": m["auth"], "widgetCount": m["widgetCount"],
		})
	}
	s.recordActivity("vclu_dashboards", "", true, start, "", ctx)
	return toolJSON(map[string]any{
		"env":        "runtime",
		"dashboards": summary,
	})
}
