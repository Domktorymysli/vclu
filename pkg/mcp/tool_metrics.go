package mcp

import (
	"context"
	"time"

	mcplib "github.com/mark3labs/mcp-go/mcp"
)

// handleMetrics lists registered metrics or queries time-series data.
// Without "path" → returns list of all registered metrics.
// With "path" → returns time-series samples for that metric.
func (s *MCPServer) handleMetrics(ctx context.Context, req mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
	start := time.Now()
	if err := s.requireDevMode(ctx); err != nil {
		return err, nil
	}
	if s.metricsCollector == nil {
		return toolError("metrics collector not configured"), nil
	}

	args := req.GetArguments()
	path, _ := args["path"].(string)

	if path == "" {
		// List all registered metrics
		list := s.metricsCollector.List()
		s.recordActivity("vclu_metrics", "", true, start, "", ctx)
		return toolJSON(map[string]any{
			"count":   len(list),
			"metrics": list,
		})
	}

	// Query data for a specific metric
	rangeStr := "2h"
	if r, ok := args["range"].(string); ok && r != "" {
		rangeStr = r
	}
	// at most the retention back, at most metrics.MaxPoints samples (averaged)
	samples := s.metricsCollector.QueryRange(path, parseMetricRange(rangeStr))

	// Return as [[timestamp, value], ...] for chart compatibility
	data := make([][2]float64, len(samples))
	for i, sample := range samples {
		data[i] = [2]float64{float64(sample.Timestamp), sample.Value}
	}

	s.recordActivity("vclu_metrics", path, true, start, "", ctx)
	return toolJSON(map[string]any{
		"path":    path,
		"range":   rangeStr,
		"count":   len(data),
		"samples": data,
	})
}

// parseMetricRange converts range string to duration (same logic as web handler).
func parseMetricRange(s string) time.Duration {
	// Handle "Nd" for days
	if len(s) > 1 && s[len(s)-1] == 'd' {
		var days int
		for _, c := range s[:len(s)-1] {
			if c >= '0' && c <= '9' {
				days = days*10 + int(c-'0')
			} else {
				days = 0
				break
			}
		}
		if days > 0 {
			return time.Duration(days) * 24 * time.Hour
		}
	}
	if d, err := time.ParseDuration(s); err == nil {
		return d
	}
	return 2 * time.Hour
}
