package mcp

import (
	"context"
	"fmt"
	"testing"
)

// mockLuaCaller records calls and returns configured responses.
type mockLuaCaller struct {
	calls   []luaCall
	results map[string]luaResult
}

type luaCall struct {
	funcName string
	args     []any
}

type luaResult struct {
	value string
	err   error
}

func newMockLuaCaller() *mockLuaCaller {
	return &mockLuaCaller{results: make(map[string]luaResult)}
}

func (m *mockLuaCaller) Call(funcName string, args ...any) (string, error) {
	m.calls = append(m.calls, luaCall{funcName: funcName, args: args})
	if r, ok := m.results[funcName]; ok {
		return r.value, r.err
	}
	return "", fmt.Errorf("no mock result for %s", funcName)
}

func (m *mockLuaCaller) setResult(funcName, value string, err error) {
	m.results[funcName] = luaResult{value: value, err: err}
}

func TestListDevices(t *testing.T) {
	lua := newMockLuaCaller()
	lua.setResult("MCP.list_devices", `[{"path":"CLU.SW1","type":"switch","name":"Switch 1","state":1,"readonly":false,"methods":["toggle"],"lastChanged":1740400000}]`, nil)

	p := NewDeviceProvider(lua)
	devices, err := p.ListDevices(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	if len(devices) != 1 {
		t.Fatalf("expected 1 device, got %d", len(devices))
	}
	if devices[0].Path != "CLU.SW1" {
		t.Errorf("expected path CLU.SW1, got %s", devices[0].Path)
	}
	if devices[0].Type != "switch" {
		t.Errorf("expected type switch, got %s", devices[0].Type)
	}
	if devices[0].LastChanged == nil || *devices[0].LastChanged != 1740400000 {
		t.Errorf("expected lastChanged=1740400000, got %v", devices[0].LastChanged)
	}
}

func TestListDevices_LuaError(t *testing.T) {
	lua := newMockLuaCaller()
	lua.setResult("MCP.list_devices", "", fmt.Errorf("Lua engine is closed"))

	p := NewDeviceProvider(lua)
	_, err := p.ListDevices(context.Background(), "")
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestListDevices_EmptyArray(t *testing.T) {
	lua := newMockLuaCaller()
	lua.setResult("MCP.list_devices", `[]`, nil)

	p := NewDeviceProvider(lua)
	devices, err := p.ListDevices(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	if len(devices) != 0 {
		t.Fatalf("expected 0 devices, got %d", len(devices))
	}
}

func TestGetDevice(t *testing.T) {
	lua := newMockLuaCaller()
	lua.setResult("MCP.get_device", `{"path":"CLU.SW1","type":"switch","name":"Switch 1","state":0,"readonly":false,"methods":["toggle"],"events":["onChange"],"caps":{"binary":true},"lastChanged":1740400000}`, nil)

	p := NewDeviceProvider(lua)
	device, err := p.GetDevice(context.Background(), "CLU.SW1")
	if err != nil {
		t.Fatal(err)
	}
	if device.Path != "CLU.SW1" {
		t.Errorf("expected path CLU.SW1, got %s", device.Path)
	}
	if len(device.Methods) != 1 || device.Methods[0] != "toggle" {
		t.Errorf("unexpected methods: %v", device.Methods)
	}
	if device.LastChanged == nil || *device.LastChanged != 1740400000 {
		t.Errorf("expected lastChanged=1740400000, got %v", device.LastChanged)
	}
}

func TestGetDevice_Error(t *testing.T) {
	lua := newMockLuaCaller()
	lua.setResult("MCP.get_device", `{"error":"device not found: CLU.NOPE"}`, nil)

	p := NewDeviceProvider(lua)
	_, err := p.GetDevice(context.Background(), "CLU.NOPE")
	if err == nil {
		t.Fatal("expected error for missing device")
	}
}

func TestSetValue(t *testing.T) {
	lua := newMockLuaCaller()
	lua.setResult("MCP.set_value", `{"ok":true,"new_state":1}`, nil)

	p := NewDeviceProvider(lua)
	result, err := p.SetValue(context.Background(), "CLU.SW1", 1)
	if err != nil {
		t.Fatal(err)
	}
	if !result.OK {
		t.Fatal("expected OK=true")
	}
}

func TestSetValue_Readonly(t *testing.T) {
	lua := newMockLuaCaller()
	lua.setResult("MCP.set_value", `{"ok":false,"error":"device is readonly: CLU.TEMP"}`, nil)

	p := NewDeviceProvider(lua)
	_, err := p.SetValue(context.Background(), "CLU.TEMP", 1)
	if err == nil {
		t.Fatal("expected error for readonly device")
	}
}

func TestExecuteMethod(t *testing.T) {
	lua := newMockLuaCaller()
	lua.setResult("MCP.execute_method", `{"ok":true,"result":null}`, nil)

	p := NewDeviceProvider(lua)
	result, err := p.ExecuteMethod(context.Background(), "CLU.SW1", "toggle", nil)
	if err != nil {
		t.Fatal(err)
	}
	if !result.OK {
		t.Fatal("expected OK=true")
	}

	// Verify args were passed correctly
	call := lua.calls[0]
	if call.funcName != "MCP.execute_method" {
		t.Errorf("unexpected func: %s", call.funcName)
	}
	// path, method, argsJSON
	if call.args[0] != "CLU.SW1" || call.args[1] != "toggle" {
		t.Errorf("unexpected args: %v", call.args)
	}
}

func TestExecuteMethod_WithArgs(t *testing.T) {
	lua := newMockLuaCaller()
	lua.setResult("MCP.execute_method", `{"ok":true,"result":42}`, nil)

	p := NewDeviceProvider(lua)
	result, err := p.ExecuteMethod(context.Background(), "CLU.DIM1", "setBrightness", []any{75})
	if err != nil {
		t.Fatal(err)
	}
	if !result.OK {
		t.Fatal("expected OK=true")
	}

	// argsJSON should be "[75]"
	call := lua.calls[0]
	argsJSON := call.args[2].(string)
	if argsJSON != "[75]" {
		t.Errorf("expected argsJSON=[75], got %s", argsJSON)
	}
}

func TestListScenes(t *testing.T) {
	lua := newMockLuaCaller()
	lua.setResult("MCP.list_scenes", `[{"path":"CLU.MovieMode","name":"Movie Mode"}]`, nil)

	p := NewDeviceProvider(lua)
	scenes, err := p.ListScenes(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(scenes) != 1 {
		t.Fatalf("expected 1 scene, got %d", len(scenes))
	}
	if scenes[0].Path != "CLU.MovieMode" {
		t.Errorf("expected path CLU.MovieMode, got %s", scenes[0].Path)
	}
}

func TestExecuteScene(t *testing.T) {
	lua := newMockLuaCaller()
	lua.setResult("MCP.execute_scene", `{"ok":true}`, nil)

	p := NewDeviceProvider(lua)
	err := p.ExecuteScene(context.Background(), "CLU.MovieMode")
	if err != nil {
		t.Fatal(err)
	}
}

func TestExecuteScene_NotFound(t *testing.T) {
	lua := newMockLuaCaller()
	lua.setResult("MCP.execute_scene", `{"ok":false,"error":"scene not found: CLU.NOPE"}`, nil)

	p := NewDeviceProvider(lua)
	err := p.ExecuteScene(context.Background(), "CLU.NOPE")
	if err == nil {
		t.Fatal("expected error for missing scene")
	}
}
