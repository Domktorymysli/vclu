package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	"github.com/Domktorymysli/vclu/pkg/history"
	mcplib "github.com/mark3labs/mcp-go/mcp"
)

// handleDeviceHistory returns state changes, newest first. With the history
// store it is a read-only tool for any authenticated bot: entries of objects
// the bot may not read are left out, as if they did not exist. The Lua
// fallback (no store wired, e.g. in tests) still needs DevMode.
func (s *MCPServer) handleDeviceHistory(ctx context.Context, req mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
	start := time.Now()
	args := req.GetArguments()
	path, _ := args["path"].(string)
	limit := 50
	if l, ok := args["limit"].(float64); ok {
		limit = int(l)
	}

	if s.historyStore != nil {
		if path != "" && !s.botCanRead(ctx, path) {
			// same answer as for an object that does not exist
			s.recordActivity("vclu_device_history", path, true, start, "", ctx)
			return toolJSON(map[string]any{"env": "runtime", "history": []any{}})
		}
		q := history.SearchQuery{
			Path:   path,
			Limit:  limit,
			Origin: stringArg(args, "origin"),
			Actor:  stringArg(args, "actor"),
			Text:   stringArg(args, "query"),
			Cursor: stringArg(args, "cursor"),
			Allow:  func(p string) bool { return s.botCanRead(ctx, p) },
			Ctx:    ctx,
		}
		var err error
		if q.Since, err = timeArg(args, "since"); err != nil {
			return toolError(err.Error()), nil
		}
		if q.Until, err = timeArg(args, "until"); err != nil {
			return toolError(err.Error()), nil
		}
		res, err := s.historyStore.Search(q)
		if err != nil {
			s.recordActivity("vclu_device_history", path, false, start, err.Error(), ctx)
			return toolError(err.Error()), nil
		}
		out := make([]map[string]any, len(res.Entries))
		for i, e := range res.Entries {
			out[i] = map[string]any{
				"id": e.ID, "path": e.Path, "value": e.Value, "old": e.Old, "atMs": e.AtMs,
				"origin": e.Origin, "actor": e.Actor, "correlationId": e.CorrelationID,
				// aliases of the pre-store format
				"new": e.Value, "time": e.AtMs / 1000,
			}
		}
		s.recordActivity("vclu_device_history", path, true, start, "", ctx)
		result := map[string]any{"env": "runtime", "history": out}
		if res.Next != "" {
			result["next"] = res.Next
		}
		return toolJSON(result)
	}

	if err := s.requireDevMode(ctx); err != nil {
		return err, nil
	}
	if s.executor == nil {
		return toolError("Lua executor not configured"), nil
	}
	if limit <= 0 {
		limit = 50
	}
	if limit > 500 {
		limit = 500
	}

	// Build Lua call
	var script string
	if path != "" {
		script = fmt.Sprintf("return ExposedObjects.getHistory(%q, %d)", path, limit)
	} else {
		script = fmt.Sprintf("return ExposedObjects.getHistory(nil, %d)", limit)
	}

	raw, err := s.executor.Execute(script)
	if err != nil {
		s.recordActivity("vclu_device_history", path, false, start, err.Error(), ctx)
		return toolError(err.Error()), nil
	}

	// Parse JSON from Lua
	var history any
	if err := json.Unmarshal([]byte(raw), &history); err != nil {
		s.recordActivity("vclu_device_history", path, true, start, "", ctx)
		return toolJSON(map[string]any{
			"env":     "runtime",
			"history": raw,
		})
	}

	s.recordActivity("vclu_device_history", path, true, start, "", ctx)
	return toolJSON(map[string]any{
		"env":     "runtime",
		"history": history,
	})
}

func stringArg(args map[string]any, name string) string {
	v, _ := args[name].(string)
	return v
}

// timeArg reads an RFC 3339 time or Unix milliseconds; 0 when absent.
func timeArg(args map[string]any, name string) (int64, error) {
	switch v := args[name].(type) {
	case nil:
		return 0, nil
	case float64:
		return int64(v), nil
	case string:
		if v == "" {
			return 0, nil
		}
		if ms, err := strconv.ParseInt(v, 10, 64); err == nil {
			return ms, nil
		}
		t, err := time.Parse(time.RFC3339, v)
		if err != nil {
			return 0, fmt.Errorf("invalid %s: RFC 3339 or Unix ms expected", name)
		}
		return t.UnixMilli(), nil
	}
	return 0, fmt.Errorf("invalid %s", name)
}
