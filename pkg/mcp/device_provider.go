package mcp

import (
	"context"
	"encoding/json"
	"fmt"
)

// LuaCaller is the minimal interface for safe Lua function invocation.
// Only Call() is exposed — no raw Execute().
type LuaCaller interface {
	Call(funcName string, args ...any) (string, error)
}

// contextCaller runs a call as a given initiator (pkg/lua.LuaEngine.CallAs).
type contextCaller interface {
	CallAs(origin, actor, correlationID, funcName string, args ...any) (string, error)
}

// callAsBot runs state-changing calls as the MCP bot of the request.
func (p *DeviceProviderImpl) callAsBot(ctx context.Context, funcName string, args ...any) (string, error) {
	if cc, ok := p.lua.(contextCaller); ok {
		return cc.CallAs("mcp", botActor(ctx), "", funcName, args...)
	}
	return p.lua.Call(funcName, args...)
}

// DeviceProviderImpl implements DeviceProvider by calling MCP.* Lua functions.
type DeviceProviderImpl struct {
	lua LuaCaller
}

// NewDeviceProvider creates a new DeviceProviderImpl.
func NewDeviceProvider(lua LuaCaller) *DeviceProviderImpl {
	return &DeviceProviderImpl{lua: lua}
}

func (p *DeviceProviderImpl) ListDevices(_ context.Context, filterJSON string) ([]Device, error) {
	result, err := p.lua.Call("MCP.list_devices", filterJSON)
	if err != nil {
		return nil, fmt.Errorf("MCP.list_devices failed: %w", err)
	}

	var devices []Device
	if err := json.Unmarshal([]byte(result), &devices); err != nil {
		return nil, fmt.Errorf("failed to parse devices JSON: %w", err)
	}
	return devices, nil
}

func (p *DeviceProviderImpl) GetDevice(_ context.Context, path string) (*DeviceDetail, error) {
	result, err := p.lua.Call("MCP.get_device", path)
	if err != nil {
		return nil, fmt.Errorf("MCP.get_device failed: %w", err)
	}

	var resp struct {
		Error string `json:"error"`
		DeviceDetail
	}
	if err := json.Unmarshal([]byte(result), &resp); err != nil {
		return nil, fmt.Errorf("failed to parse device JSON: %w", err)
	}
	if resp.Error != "" {
		return nil, fmt.Errorf("%s", resp.Error)
	}
	return &resp.DeviceDetail, nil
}

func (p *DeviceProviderImpl) SetValue(ctx context.Context, path string, value any) (*SetResult, error) {
	result, err := p.callAsBot(ctx, "MCP.set_value", path, value)
	if err != nil {
		return nil, fmt.Errorf("MCP.set_value failed: %w", err)
	}

	var resp struct {
		OK       bool   `json:"ok"`
		NewState any    `json:"new_state"`
		Error    string `json:"error"`
	}
	if err := json.Unmarshal([]byte(result), &resp); err != nil {
		return nil, fmt.Errorf("failed to parse set result: %w", err)
	}
	if !resp.OK {
		return nil, fmt.Errorf("%s", resp.Error)
	}
	return &SetResult{OK: true, NewState: resp.NewState}, nil
}

func (p *DeviceProviderImpl) ExecuteMethod(ctx context.Context, path, method string, args []any) (*ExecResult, error) {
	// Serialize args to JSON for safe passing to Lua
	argsJSON := ""
	if len(args) > 0 {
		b, err := json.Marshal(args)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal args: %w", err)
		}
		argsJSON = string(b)
	}

	result, err := p.callAsBot(ctx, "MCP.execute_method", path, method, argsJSON)
	if err != nil {
		return nil, fmt.Errorf("MCP.execute_method failed: %w", err)
	}

	var resp struct {
		OK     bool   `json:"ok"`
		Result any    `json:"result"`
		Error  string `json:"error"`
	}
	if err := json.Unmarshal([]byte(result), &resp); err != nil {
		return nil, fmt.Errorf("failed to parse exec result: %w", err)
	}
	if !resp.OK {
		return nil, fmt.Errorf("%s", resp.Error)
	}
	return &ExecResult{OK: true, Result: resp.Result}, nil
}

func (p *DeviceProviderImpl) ListScenes(_ context.Context) ([]Scene, error) {
	result, err := p.lua.Call("MCP.list_scenes")
	if err != nil {
		return nil, fmt.Errorf("MCP.list_scenes failed: %w", err)
	}

	var scenes []Scene
	if err := json.Unmarshal([]byte(result), &scenes); err != nil {
		return nil, fmt.Errorf("failed to parse scenes JSON: %w", err)
	}
	return scenes, nil
}

func (p *DeviceProviderImpl) ExecuteScene(ctx context.Context, path string) error {
	result, err := p.callAsBot(ctx, "MCP.execute_scene", path)
	if err != nil {
		return fmt.Errorf("MCP.execute_scene failed: %w", err)
	}

	var resp struct {
		OK    bool   `json:"ok"`
		Error string `json:"error"`
	}
	if err := json.Unmarshal([]byte(result), &resp); err != nil {
		return fmt.Errorf("failed to parse scene result: %w", err)
	}
	if !resp.OK {
		return fmt.Errorf("%s", resp.Error)
	}
	return nil
}
