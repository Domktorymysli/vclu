package mcp

import (
	"context"
	"encoding/json"
	"regexp"
	"strings"
	"time"

	mcplib "github.com/mark3labs/mcp-go/mcp"
)

// sensitiveKeyPattern matches config keys that should be redacted.
var sensitiveKeyPattern = regexp.MustCompile(`(?i)(password|secret|token|apikey|api_key|clientsecret|client_secret|accesstoken|access_token|privatekey|private_key)`)

// handlePluginConfig returns plugin configuration with secrets redacted.
func (s *MCPServer) handlePluginConfig(ctx context.Context, req mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
	start := time.Now()
	if err := s.requireDevMode(ctx); err != nil {
		return err, nil
	}

	filterID, _ := req.GetArguments()["id"].(string)

	installed := s.config.GetInstalledPlugins()
	if len(installed) == 0 {
		s.recordActivity("vclu_plugin_config", "", true, start, "", ctx)
		return toolJSON(map[string]any{
			"env":     "runtime",
			"plugins": []any{},
			"count":   0,
		})
	}

	var results []map[string]any
	for _, p := range installed {
		if filterID != "" && p.ID != filterID {
			continue
		}

		info := map[string]any{
			"id":      p.ID,
			"version": p.Version,
			"enabled": p.Enabled,
		}

		if p.InstalledAt != "" {
			info["installedAt"] = p.InstalledAt
		}

		// Redact sensitive config values
		if p.Config != nil {
			redacted := redactConfig(p.Config)
			info["config"] = redacted
		}

		// Try to read plugin.json manifest for extra metadata
		if s.files != nil {
			safeName := safePluginDirName(p.ID)
			content, err := s.files.ReadFile("plugins/" + safeName + "/plugin.json")
			if err == nil {
				var manifest map[string]any
				if json.Unmarshal([]byte(content), &manifest) == nil {
					if desc, ok := manifest["description"].(string); ok {
						info["description"] = desc
					}
					if exports, ok := manifest["exports"].(map[string]any); ok {
						info["exports"] = exports
					}
				}
			}
		}

		results = append(results, info)
	}

	if results == nil {
		results = []map[string]any{}
	}

	s.recordActivity("vclu_plugin_config", filterID, true, start, "", ctx)
	return toolJSON(map[string]any{
		"env":     "runtime",
		"plugins": results,
		"count":   len(results),
	})
}

// redactConfig returns a copy of the config map with sensitive values replaced by "***".
func redactConfig(cfg map[string]interface{}) map[string]interface{} {
	result := make(map[string]interface{}, len(cfg))
	for k, v := range cfg {
		if sensitiveKeyPattern.MatchString(k) {
			result[k] = "***"
		} else if nested, ok := v.(map[string]interface{}); ok {
			result[k] = redactConfig(nested)
		} else {
			// Also check if the key contains sensitive substrings via simpler heuristic
			lower := strings.ToLower(k)
			if strings.Contains(lower, "pass") || strings.Contains(lower, "secret") {
				result[k] = "***"
			} else {
				result[k] = v
			}
		}
	}
	return result
}
