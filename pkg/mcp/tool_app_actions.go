package mcp

import (
	"context"
	"time"

	"github.com/Domktorymysli/vclu/pkg/plugins"
	mcplib "github.com/mark3labs/mcp-go/mcp"
)

// appActionTimeout bounds one action of a plugin (it runs on the Lua engine).
const appActionTimeout = 10 * time.Second

// registerAppActionTools: the actions of installed apps (plugin:action), control layer like
// vclu_device_set. A bot sees a plugin's actions when its profiles read the plugin's id and
// calls them when they control it.
func (s *MCPServer) registerAppActionTools() {
	s.addTool("vclu_app_actions", "List the actions of the user's installed apps (vclu plugins: a shopping list, "+
		"a to-do list, a media player...). Each has plugin, name, description (text or {pl, en}) and params "+
		"({name: {type, description, required}}). When the user asks for something an app does "+
		"(\"add milk to the shopping list\"), find its action here and call it with vclu_app_action.", s.handleAppActions,
		map[string]any{
			"plugin": map[string]any{"type": "string", "description": "Only this app's actions: the plugin id, e.g. @vclu/zakupy (optional)"},
		}, nil)

	s.addTool("vclu_app_action", "Run an action of one of the user's installed apps, as listed by vclu_app_actions: "+
		"e.g. plugin \"@vclu/zakupy\", action \"add\", params {\"item\": \"milk\"}. Params must match the action's "+
		"types (string, number, boolean); unknown ones are refused. Returns the app's result.", s.handleAppAction,
		map[string]any{
			"plugin": map[string]any{"type": "string", "description": "Plugin id from vclu_app_actions, e.g. @vclu/zakupy"},
			"action": map[string]any{"type": "string", "description": "Action name from vclu_app_actions"},
			"params": map[string]any{"type": "object", "description": "The action's params by name (optional when it takes none)"},
		}, []string{"plugin", "action"})
}

func (s *MCPServer) handleAppActions(ctx context.Context, req mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
	start := time.Now()
	filter, _ := req.GetArguments()["plugin"].(string)
	if filter != "" && !plugins.ValidPluginID(filter) {
		return toolError("invalid plugin id: " + filter), nil
	}
	if s.executor == nil {
		return toolError("Lua executor not configured"), nil
	}
	out, err := s.executor.Execute(plugins.ListActionsScript(filter))
	if err == nil {
		var all []plugins.Action
		if all, err = plugins.ParseActions(out); err == nil {
			visible := []plugins.Action{}
			for _, a := range all {
				if s.botCanRead(ctx, a.Plugin) {
					visible = append(visible, a)
				}
			}
			s.recordActivity("vclu_app_actions", filter, true, start, "", ctx)
			return toolJSON(map[string]any{"actions": visible})
		}
	}
	s.recordActivity("vclu_app_actions", filter, false, start, err.Error(), ctx)
	return toolError(err.Error()), nil
}

func (s *MCPServer) handleAppAction(ctx context.Context, req mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
	start := time.Now()
	args := req.GetArguments()
	plugin, _ := args["plugin"].(string)
	action, _ := args["action"].(string)
	if !plugins.ValidPluginID(plugin) || !plugins.ValidActionName(action) {
		return toolError("unknown action " + action + " of " + plugin + "; see vclu_app_actions"), nil
	}
	params := map[string]any{}
	if raw, ok := args["params"]; ok && raw != nil {
		if params, ok = raw.(map[string]any); !ok {
			return toolError("params is an object: {\"name\": value}"), nil
		}
	}
	if !s.botCanControl(ctx, plugin) {
		s.recordActivity("vclu_app_action", plugin, false, start, "access denied", ctx)
		return toolError("cannot run actions of " + plugin + " (readonly or hidden)"), nil
	}
	if s.executor == nil {
		return toolError("Lua executor not configured"), nil
	}
	script, err := plugins.CallActionScript(plugin, action, params)
	if err != nil {
		return toolError(err.Error()), nil
	}
	// as the bot: the action may change state
	var out string
	if ce, ok := s.executor.(interface {
		ExecuteAsWithTimeout(origin, actor, correlationID, script string, timeout time.Duration) (string, error)
	}); ok {
		out, err = ce.ExecuteAsWithTimeout("mcp", botActor(ctx), "", script, appActionTimeout)
	} else {
		out, err = s.executor.ExecuteWithTimeout(script, appActionTimeout)
	}
	var result any
	if err == nil {
		result, err = plugins.ParseActionResult(out)
	}
	if err != nil {
		s.recordActivity("vclu_app_action", plugin+":"+action, false, start, err.Error(), ctx)
		return toolError(err.Error()), nil
	}
	s.recordActivity("vclu_app_action", plugin+":"+action, true, start, "", ctx)
	return toolJSON(map[string]any{"result": result})
}
