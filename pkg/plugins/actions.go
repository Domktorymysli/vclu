package plugins

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/Domktorymysli/vclu/pkg/luastr"
)

// Actions a plugin registers with plugin:action (PluginSandbox.lua): agents and voice call
// them over HTTP and MCP. The scripts run on the Lua engine like any other Go→Lua call.

// ActionParam is one parameter of an action: type "string", "number" or "boolean".
type ActionParam struct {
	Type        string `json:"type"`
	Description any    `json:"description,omitempty"` // a text or {"pl": ..., "en": ...}
	Required    bool   `json:"required"`
}

// Action is a plugin's named action.
type Action struct {
	Plugin      string                 `json:"plugin"`
	Name        string                 `json:"name"`
	Description any                    `json:"description"`
	Params      map[string]ActionParam `json:"params"`
}

// ActionError is an action's refusal: Code "not_found", "invalid" (params) or "failed".
type ActionError struct {
	Msg  string
	Code string
}

func (e *ActionError) Error() string { return e.Msg }

// ListActionsScript lists the actions of running plugins, of one when pluginID is set.
func ListActionsScript(pluginID string) string {
	return fmt.Sprintf(`return PluginSandbox:listActions(%s)`, luastr.Quote(pluginID))
}

// ParseActions reads what ListActionsScript returned.
func ParseActions(out string) ([]Action, error) {
	actions := []Action{}
	if err := json.Unmarshal([]byte(out), &actions); err != nil {
		return nil, fmt.Errorf("actions: %w", err)
	}
	for i := range actions {
		if actions[i].Params == nil {
			actions[i].Params = map[string]ActionParam{}
		}
	}
	return actions, nil
}

// CallActionScript runs a plugin's action with params; the plugin id and name are quoted,
// the params go as JSON (nulls left out: a param is given or not).
func CallActionScript(pluginID, name string, params map[string]any) (string, error) {
	clean := map[string]any{}
	for k, v := range params {
		if v != nil {
			clean[k] = v
		}
	}
	raw, err := json.Marshal(clean)
	if err != nil {
		return "", &ActionError{Msg: "params: " + err.Error(), Code: "invalid"}
	}
	return fmt.Sprintf(`return PluginSandbox:callAction(%s, %s, %s)`,
		luastr.Quote(pluginID), luastr.Quote(name), luastr.Quote(string(raw))), nil
}

// ParseActionResult reads what CallActionScript returned: the result, or an *ActionError.
func ParseActionResult(out string) (any, error) {
	var r struct {
		Result any    `json:"result"`
		Error  string `json:"error"`
		Code   string `json:"code"`
	}
	if err := json.Unmarshal([]byte(out), &r); err != nil {
		return nil, fmt.Errorf("action: %w", err)
	}
	if r.Error != "" || r.Code != "" {
		return nil, &ActionError{Msg: r.Error, Code: r.Code}
	}
	return r.Result, nil
}

// ValidActionName: what plugin:action accepts as a name.
func ValidActionName(name string) bool {
	if name == "" || len(name) > 64 || name[0] < 'a' || name[0] > 'z' {
		return false
	}
	return strings.Trim(name, "abcdefghijklmnopqrstuvwxyz0123456789_") == ""
}

// ActionErrorCode is the code of an *ActionError, "" for any other error.
func ActionErrorCode(err error) string {
	var ae *ActionError
	if errors.As(err, &ae) {
		return ae.Code
	}
	return ""
}
