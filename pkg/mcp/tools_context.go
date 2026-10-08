package mcp

import (
	"context"
	"time"

	mcplib "github.com/mark3labs/mcp-go/mcp"
)

// registerContextTools registers the agent context save tool.
// Called from registerSandboxTools to keep all Layer 2 tools together.
func (s *MCPServer) registerContextTools() {
	s.addTool("vclu_context_save", "Save notes to persistent agent context (memory between sessions). Append mode — add observations, TODOs, discovered constraints.", s.handleContextSave,
		map[string]any{
			"notes": map[string]any{
				"type":        "string",
				"description": "Markdown notes to append (observations, TODOs, warnings for next session)",
			},
		}, []string{"notes"})

	s.addTool("vclu_context_read", "Read persistent agent context (notes from previous sessions)", s.handleContextRead, nil, nil)
}

func (s *MCPServer) handleContextSave(ctx context.Context, req mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
	start := time.Now()
	if err := s.requireDevMode(ctx); err != nil {
		return err, nil
	}
	if s.agentCtx == nil {
		return toolError("agent context not configured"), nil
	}

	notes, _ := req.GetArguments()["notes"].(string)
	if notes == "" {
		return toolError("notes are required"), nil
	}

	if err := s.agentCtx.Append(notes); err != nil {
		s.recordActivity("vclu_context_save", "", false, start, err.Error(), ctx)
		return toolError(err.Error()), nil
	}

	s.addAudit(ctx, "context_save", "", nil, "workspace", true, "")
	s.recordActivity("vclu_context_save", "", true, start, "", ctx)
	return toolJSON(map[string]any{
		"ok":      true,
		"message": "Notes saved to agent context",
	})
}

func (s *MCPServer) handleContextRead(ctx context.Context, _ mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
	start := time.Now()
	if err := s.requireDevMode(ctx); err != nil {
		return err, nil
	}
	if s.agentCtx == nil {
		return toolError("agent context not configured"), nil
	}

	content, err := s.agentCtx.Read()
	if err != nil {
		s.recordActivity("vclu_context_read", "", false, start, err.Error(), ctx)
		return toolError(err.Error()), nil
	}

	if content == "" {
		content = "(no notes yet)"
	}

	s.recordActivity("vclu_context_read", "", true, start, "", ctx)
	return toolJSON(map[string]any{
		"content": content,
	})
}
