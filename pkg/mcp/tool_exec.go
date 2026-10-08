package mcp

import (
	"context"
	"fmt"
	"time"

	mcplib "github.com/mark3labs/mcp-go/mcp"
)

const (
	execTimeout     = 10 * time.Second
	execMaxScriptKB = 8 * 1024 // 8KB
)

// handleExec executes a Lua snippet on the live runtime.
// Requires DevMode + per-bot ExecMode flag.
func (s *MCPServer) handleExec(ctx context.Context, req mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
	start := time.Now()

	// Dev mode gate
	if err := s.requireDevMode(ctx); err != nil {
		return err, nil
	}

	// Per-bot ExecMode gate
	bot := botFromContext(ctx)
	if bot == nil || !bot.ExecMode {
		botName := "unknown"
		if bot != nil {
			botName = bot.Name
		}
		return toolError(fmt.Sprintf("Bot %q does not have ExecMode enabled. Enable it in MCP dashboard → Bots.", botName)), nil
	}

	if s.executor == nil {
		return toolError("Lua executor not configured"), nil
	}

	script, _ := req.GetArguments()["script"].(string)
	if script == "" {
		return toolError("script is required"), nil
	}
	if len(script) > execMaxScriptKB {
		return toolError(fmt.Sprintf("script too large: %d bytes (max %d)", len(script), execMaxScriptKB)), nil
	}

	// Rate limit: 20 executions per minute
	if !s.execLimiter.allow() {
		s.recordActivity("vclu_exec", "", false, start, "rate limited", ctx)
		return toolError("rate limit exceeded: max 20 executions per minute"), nil
	}

	// Execute with timeout, as the bot: the raw script may change state
	var result string
	var err error
	if ce, ok := s.executor.(interface {
		ExecuteAsWithTimeout(origin, actor, correlationID, script string, timeout time.Duration) (string, error)
	}); ok {
		result, err = ce.ExecuteAsWithTimeout("mcp", botActor(ctx), "", script, execTimeout)
	} else {
		result, err = s.executor.ExecuteWithTimeout(script, execTimeout)
	}
	if err != nil {
		// Audit with truncated script
		detail := truncateScript(script, 100)
		s.addAudit(ctx, "exec", "", nil, "runtime", false, detail)
		s.recordActivity("vclu_exec", "", false, start, err.Error(), ctx)
		return toolError(fmt.Sprintf("execution error: %v", err)), nil
	}

	detail := truncateScript(script, 100)
	s.addAudit(ctx, "exec", "", nil, "runtime", true, detail)
	s.recordActivity("vclu_exec", "", true, start, "", ctx)

	return toolJSON(map[string]any{
		"env":    "runtime",
		"scope":  "global",
		"ok":     true,
		"result": result,
	})
}

// truncateScript returns at most n characters of s, with "..." appended if truncated.
func truncateScript(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
