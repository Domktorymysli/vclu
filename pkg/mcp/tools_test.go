package mcp

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/Domktorymysli/vclu/pkg/accesscontrol"
	"github.com/Domktorymysli/vclu/pkg/config"
	mcplib "github.com/mark3labs/mcp-go/mcp"
)

// botCtx returns a context with a test bot that has full-access profile.
func botCtx() context.Context {
	bot := &config.MCPBot{
		ID: "test-bot", Name: "Test Bot", Profiles: []string{"full-access"},
	}
	return context.WithValue(context.Background(), botKey, bot)
}

// testHarness provides a ready-to-use MCPServer with mock dependencies.
type testHarness struct {
	server  *MCPServer
	devices *mockDeviceProvider
	access  *mockAccessChecker
	metrics *mockMetrics
}

func newTestHarness() *testHarness {
	devices := &mockDeviceProvider{
		devices: []Device{
			{Path: "CLU.SW1", Type: "switch", Name: "Switch 1", State: 0, Methods: []string{"toggle"}},
			{Path: "CLU.HIDDEN", Type: "switch", Name: "Hidden", State: 0},
		},
		scenes: []Scene{
			{Path: "CLU.MovieMode", Name: "Movie Mode"},
		},
	}

	access := newMockAccessChecker()
	access.readPaths["CLU.HIDDEN"] = false    // hidden
	access.controlPaths["CLU.HIDDEN"] = false // no control

	cfg := &mockConfigProvider{}
	metrics := &mockMetrics{}

	srv := NewMCPServer(devices, access, cfg, metrics)
	srv.SetEnabled(true)

	return &testHarness{
		server:  srv,
		devices: devices,
		access:  access,
		metrics: metrics,
	}
}

func makeCallRequest(args map[string]any) mcplib.CallToolRequest {
	var arguments any
	if args != nil {
		arguments = args
	}
	return mcplib.CallToolRequest{
		Params: mcplib.CallToolParams{
			Arguments: arguments,
		},
	}
}

func TestHandleDevices_FiltersHidden(t *testing.T) {
	h := newTestHarness()
	ctx := botCtx()

	result, err := h.server.handleDevices(ctx, makeCallRequest(nil))
	if err != nil {
		t.Fatal(err)
	}

	// Parse result text
	text := result.Content[0].(mcplib.TextContent).Text
	var resp struct {
		Devices []Device `json:"devices"`
	}
	if err := json.Unmarshal([]byte(text), &resp); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}

	// Should only see CLU.SW1, not CLU.HIDDEN
	if len(resp.Devices) != 1 {
		t.Fatalf("expected 1 visible device, got %d", len(resp.Devices))
	}
	if resp.Devices[0].Path != "CLU.SW1" {
		t.Errorf("expected CLU.SW1, got %s", resp.Devices[0].Path)
	}
}

func TestHandleDeviceGet_AccessDenied(t *testing.T) {
	h := newTestHarness()
	ctx := botCtx()

	result, err := h.server.handleDeviceGet(ctx, makeCallRequest(map[string]any{"path": "CLU.HIDDEN"}))
	if err != nil {
		t.Fatal(err)
	}

	if !result.IsError {
		t.Fatal("expected error result for hidden device")
	}
}

func TestHandleDeviceSet_AccessDenied(t *testing.T) {
	h := newTestHarness()
	ctx := botCtx()

	result, err := h.server.handleDeviceSet(ctx, makeCallRequest(map[string]any{
		"path":  "CLU.HIDDEN",
		"value": 1,
	}))
	if err != nil {
		t.Fatal(err)
	}

	if !result.IsError {
		t.Fatal("expected error for hidden device control")
	}
}

func TestHandleDeviceExecute_AccessDenied(t *testing.T) {
	h := newTestHarness()
	ctx := botCtx()

	result, err := h.server.handleDeviceExecute(ctx, makeCallRequest(map[string]any{
		"path":   "CLU.HIDDEN",
		"method": "toggle",
	}))
	if err != nil {
		t.Fatal(err)
	}

	if !result.IsError {
		t.Fatal("expected error for hidden device execute")
	}
}

func TestHandleScenes_FiltersHidden(t *testing.T) {
	h := newTestHarness()
	ctx := botCtx()

	result, err := h.server.handleScenes(ctx, makeCallRequest(nil))
	if err != nil {
		t.Fatal(err)
	}

	text := result.Content[0].(mcplib.TextContent).Text
	var resp struct {
		Scenes []Scene `json:"scenes"`
	}
	if err := json.Unmarshal([]byte(text), &resp); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}

	if len(resp.Scenes) != 1 {
		t.Fatalf("expected 1 scene, got %d", len(resp.Scenes))
	}
}

func TestHandleSceneExecute_AccessDenied(t *testing.T) {
	h := newTestHarness()
	h.access.controlPaths["CLU.MovieMode"] = false
	ctx := botCtx()

	result, err := h.server.handleSceneExecute(ctx, makeCallRequest(map[string]any{
		"path": "CLU.MovieMode",
	}))
	if err != nil {
		t.Fatal(err)
	}

	if !result.IsError {
		t.Fatal("expected error for denied scene execute")
	}
}

func TestRecordActivity(t *testing.T) {
	h := newTestHarness()
	ctx := context.WithValue(context.Background(), remoteIPKey, "192.168.1.100")

	h.server.recordActivity("vclu_devices", "", true, time.Now(), "", ctx)

	if h.metrics.requests != 1 {
		t.Errorf("expected 1 request recorded, got %d", h.metrics.requests)
	}
	if len(h.metrics.activity) != 1 {
		t.Fatalf("expected 1 activity entry, got %d", len(h.metrics.activity))
	}
	if h.metrics.activity[0].RemoteIP != "192.168.1.100" {
		t.Errorf("expected IP 192.168.1.100, got %s", h.metrics.activity[0].RemoteIP)
	}
}

func TestToolJSON(t *testing.T) {
	result, err := toolJSON(map[string]string{"hello": "world"})
	if err != nil {
		t.Fatal(err)
	}
	text := result.Content[0].(mcplib.TextContent).Text
	if text != `{"hello":"world"}` {
		t.Errorf("unexpected JSON: %s", text)
	}
	if result.IsError {
		t.Fatal("should not be error")
	}
}

func TestToolError(t *testing.T) {
	result := toolError("something broke")
	text := result.Content[0].(mcplib.TextContent).Text
	if text != "something broke" {
		t.Errorf("unexpected error text: %s", text)
	}
	if !result.IsError {
		t.Fatal("should be error")
	}
}

// Test that readonly flag is applied from ACL, not just from Lua
func TestHandleDevices_ReadonlyFromACL(t *testing.T) {
	h := newTestHarness()
	// CLU.SW1 is visible (CanRead=true by default) but readonly in ACL
	h.access.controlPaths["CLU.SW1"] = false
	ctx := botCtx()

	result, err := h.server.handleDevices(ctx, makeCallRequest(nil))
	if err != nil {
		t.Fatal(err)
	}

	text := result.Content[0].(mcplib.TextContent).Text
	var resp struct {
		Devices []Device `json:"devices"`
	}
	if err := json.Unmarshal([]byte(text), &resp); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}

	if len(resp.Devices) != 1 {
		t.Fatalf("expected 1 visible device, got %d", len(resp.Devices))
	}
	if !resp.Devices[0].Readonly {
		t.Error("expected CLU.SW1 to be readonly when ACL denies control")
	}
}

func TestHandleDevices_NotReadonlyWhenFullAccess(t *testing.T) {
	h := newTestHarness()
	// Default: CanControl returns true — device should NOT be readonly
	ctx := botCtx()

	result, err := h.server.handleDevices(ctx, makeCallRequest(nil))
	if err != nil {
		t.Fatal(err)
	}

	text := result.Content[0].(mcplib.TextContent).Text
	var resp struct {
		Devices []Device `json:"devices"`
	}
	if err := json.Unmarshal([]byte(text), &resp); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}

	if len(resp.Devices) != 1 {
		t.Fatalf("expected 1 visible device, got %d", len(resp.Devices))
	}
	if resp.Devices[0].Readonly {
		t.Error("expected CLU.SW1 to NOT be readonly when ACL allows control")
	}
}

func TestHandleDeviceGet_ReadonlyFromACL(t *testing.T) {
	h := newTestHarness()
	// CLU.SW1 is visible but readonly
	h.access.controlPaths["CLU.SW1"] = false
	ctx := botCtx()

	result, err := h.server.handleDeviceGet(ctx, makeCallRequest(map[string]any{"path": "CLU.SW1"}))
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatal("expected success for readable device")
	}

	text := result.Content[0].(mcplib.TextContent).Text
	var dev DeviceDetail
	if err := json.Unmarshal([]byte(text), &dev); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}

	if !dev.Readonly {
		t.Error("expected device to be readonly when ACL denies control")
	}
}

func TestHandleDeviceGet_NotReadonlyWhenFullAccess(t *testing.T) {
	h := newTestHarness()
	ctx := botCtx()

	result, err := h.server.handleDeviceGet(ctx, makeCallRequest(map[string]any{"path": "CLU.SW1"}))
	if err != nil {
		t.Fatal(err)
	}

	text := result.Content[0].(mcplib.TextContent).Text
	var dev DeviceDetail
	if err := json.Unmarshal([]byte(text), &dev); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}

	if dev.Readonly {
		t.Error("expected device to NOT be readonly when ACL allows control")
	}
}

func TestHandleDeviceSet_DeniedForReadonly(t *testing.T) {
	h := newTestHarness()
	h.access.controlPaths["CLU.SW1"] = false
	ctx := botCtx()

	result, err := h.server.handleDeviceSet(ctx, makeCallRequest(map[string]any{
		"path":  "CLU.SW1",
		"value": 1,
	}))
	if err != nil {
		t.Fatal(err)
	}

	if !result.IsError {
		t.Fatal("expected error when setting readonly device")
	}
	text := result.Content[0].(mcplib.TextContent).Text
	if text == "" {
		t.Fatal("expected error message")
	}
}

func TestHandleDeviceExecute_DeniedForReadonly(t *testing.T) {
	h := newTestHarness()
	h.access.controlPaths["CLU.SW1"] = false
	ctx := botCtx()

	result, err := h.server.handleDeviceExecute(ctx, makeCallRequest(map[string]any{
		"path":   "CLU.SW1",
		"method": "toggle",
	}))
	if err != nil {
		t.Fatal(err)
	}

	if !result.IsError {
		t.Fatal("expected error when executing on readonly device")
	}
}

// --- Tests: Lua ExposedObjects readonly overrides ACL ---
// Scenario: Device exposed as readonly in Lua, but ACL gives full access.
// The readonly flag from Lua must be preserved and writes must be blocked.

func newTestHarnessWithLuaReadonly() *testHarness {
	devices := &mockDeviceProvider{
		devices: []Device{
			{Path: "CLU.SW1", Type: "switch", Name: "Switch 1", State: 0, Methods: []string{"toggle"}},
			{Path: "CLU.SENSOR", Type: "sensor", Name: "Temperature", State: 22.5, Readonly: true},
			{Path: "CLU.HIDDEN", Type: "switch", Name: "Hidden", State: 0},
		},
		scenes: []Scene{},
	}

	access := newMockAccessChecker()
	access.readPaths["CLU.HIDDEN"] = false
	access.controlPaths["CLU.HIDDEN"] = false
	// CLU.SENSOR: ACL gives full access (default) — but Lua says readonly

	cfg := &mockConfigProvider{}
	metrics := &mockMetrics{}

	srv := NewMCPServer(devices, access, cfg, metrics)
	srv.SetEnabled(true)

	return &testHarness{
		server:  srv,
		devices: devices,
		access:  access,
		metrics: metrics,
	}
}

func TestHandleDevices_LuaReadonlyPreservedWithFullACL(t *testing.T) {
	h := newTestHarnessWithLuaReadonly()
	ctx := botCtx()

	result, err := h.server.handleDevices(ctx, makeCallRequest(nil))
	if err != nil {
		t.Fatal(err)
	}

	text := result.Content[0].(mcplib.TextContent).Text
	var resp struct {
		Devices []Device `json:"devices"`
	}
	if err := json.Unmarshal([]byte(text), &resp); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}

	// Should see CLU.SW1 (writable) and CLU.SENSOR (readonly from Lua)
	if len(resp.Devices) != 2 {
		t.Fatalf("expected 2 visible devices, got %d", len(resp.Devices))
	}

	for _, d := range resp.Devices {
		switch d.Path {
		case "CLU.SW1":
			if d.Readonly {
				t.Error("CLU.SW1 should NOT be readonly")
			}
		case "CLU.SENSOR":
			if !d.Readonly {
				t.Error("CLU.SENSOR should be readonly (set by Lua ExposedObjects) even though ACL gives full access")
			}
		default:
			t.Errorf("unexpected device: %s", d.Path)
		}
	}
}

func TestHandleDeviceGet_LuaReadonlyPreservedWithFullACL(t *testing.T) {
	h := newTestHarnessWithLuaReadonly()
	ctx := botCtx()

	result, err := h.server.handleDeviceGet(ctx, makeCallRequest(map[string]any{"path": "CLU.SENSOR"}))
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatal("expected success for visible device")
	}

	text := result.Content[0].(mcplib.TextContent).Text
	var dev DeviceDetail
	if err := json.Unmarshal([]byte(text), &dev); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}

	if !dev.Readonly {
		t.Error("CLU.SENSOR should be readonly from Lua even though ACL gives full access")
	}
}

func TestHandleDeviceSet_BlockedByLuaReadonly(t *testing.T) {
	h := newTestHarnessWithLuaReadonly()
	// ACL allows full control (default), but Lua ExposedObjects says readonly
	ctx := botCtx()

	result, err := h.server.handleDeviceSet(ctx, makeCallRequest(map[string]any{
		"path":  "CLU.SENSOR",
		"value": 99,
	}))
	if err != nil {
		t.Fatal(err)
	}

	if !result.IsError {
		t.Fatal("expected error when setting a device that is readonly in Lua ExposedObjects")
	}
}

func TestHandleDeviceExecute_BlockedByLuaReadonly(t *testing.T) {
	h := newTestHarnessWithLuaReadonly()
	ctx := botCtx()

	result, err := h.server.handleDeviceExecute(ctx, makeCallRequest(map[string]any{
		"path":   "CLU.SENSOR",
		"method": "toggle",
	}))
	if err != nil {
		t.Fatal(err)
	}

	if !result.IsError {
		t.Fatal("expected error when executing method on a device that is readonly in Lua ExposedObjects")
	}
}

// --- Tests: Both ACL readonly AND Lua readonly (double readonly) ---
// Both layers say readonly — verify no "double negative" bug.

func TestHandleDevices_BothACLAndLuaReadonly(t *testing.T) {
	h := newTestHarnessWithLuaReadonly()
	// CLU.SENSOR is already Lua-readonly; now also make ACL readonly
	h.access.controlPaths["CLU.SENSOR"] = false
	ctx := botCtx()

	result, err := h.server.handleDevices(ctx, makeCallRequest(nil))
	if err != nil {
		t.Fatal(err)
	}

	text := result.Content[0].(mcplib.TextContent).Text
	var resp struct {
		Devices []Device `json:"devices"`
	}
	if err := json.Unmarshal([]byte(text), &resp); err != nil {
		t.Fatalf("failed to parse: %v", err)
	}

	for _, d := range resp.Devices {
		if d.Path == "CLU.SENSOR" {
			if !d.Readonly {
				t.Error("CLU.SENSOR must be readonly when both ACL and Lua say readonly")
			}
			return
		}
	}
	t.Fatal("CLU.SENSOR should be visible (not hidden)")
}

func TestHandleDeviceSet_BlockedByBothACLAndLuaReadonly(t *testing.T) {
	h := newTestHarnessWithLuaReadonly()
	h.access.controlPaths["CLU.SENSOR"] = false
	ctx := botCtx()

	result, err := h.server.handleDeviceSet(ctx, makeCallRequest(map[string]any{
		"path":  "CLU.SENSOR",
		"value": 99,
	}))
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError {
		t.Fatal("expected error when both ACL and Lua say readonly")
	}
}

// --- Tests: Hidden device with Lua readonly ---

func TestHandleDevices_HiddenDeviceNotVisibleEvenIfLuaReadonly(t *testing.T) {
	h := newTestHarnessWithLuaReadonly()
	// Add a device that is Lua-readonly AND ACL-hidden
	h.devices.devices = append(h.devices.devices, Device{
		Path: "CLU.GHOST", Type: "sensor", Name: "Ghost", State: 0, Readonly: true,
	})
	h.access.readPaths["CLU.GHOST"] = false
	ctx := botCtx()

	result, err := h.server.handleDevices(ctx, makeCallRequest(nil))
	if err != nil {
		t.Fatal(err)
	}

	text := result.Content[0].(mcplib.TextContent).Text
	var resp struct {
		Devices []Device `json:"devices"`
	}
	json.Unmarshal([]byte(text), &resp)

	for _, d := range resp.Devices {
		if d.Path == "CLU.GHOST" {
			t.Fatal("hidden device should NOT be visible regardless of Lua readonly")
		}
	}
}

// --- Tests: Edge cases ---

func TestHandleDeviceSet_NonexistentDevice(t *testing.T) {
	h := newTestHarness()
	ctx := botCtx()

	result, err := h.server.handleDeviceSet(ctx, makeCallRequest(map[string]any{
		"path":  "CLU.NONEXISTENT",
		"value": 1,
	}))
	if err != nil {
		t.Fatal(err)
	}

	// ACL defaults to allow, but provider should handle missing device
	// (mock returns success for unknown paths — real Lua returns error)
	// This test documents current behavior; the important thing is no panic.
	_ = result
}

func TestHandleDeviceGet_NonexistentDevice(t *testing.T) {
	h := newTestHarness()
	ctx := botCtx()

	result, err := h.server.handleDeviceGet(ctx, makeCallRequest(map[string]any{"path": "CLU.NONEXISTENT"}))
	if err != nil {
		t.Fatal(err)
	}

	// mockDeviceProvider.GetDevice returns nil,nil for unknown paths
	// The handler should handle this gracefully (not panic)
	_ = result
}

func TestHandleDeviceSet_EmptyPath(t *testing.T) {
	h := newTestHarness()
	ctx := botCtx()

	result, err := h.server.handleDeviceSet(ctx, makeCallRequest(map[string]any{
		"path":  "",
		"value": 1,
	}))
	if err != nil {
		t.Fatal(err)
	}
	// Should not panic; behavior depends on ACL default for empty path
	_ = result
}

func TestHandleDeviceExecute_EmptyMethod(t *testing.T) {
	h := newTestHarness()
	ctx := botCtx()

	result, err := h.server.handleDeviceExecute(ctx, makeCallRequest(map[string]any{
		"path":   "CLU.SW1",
		"method": "",
	}))
	if err != nil {
		t.Fatal(err)
	}
	// Should not panic
	_ = result
}

// Test that CanRead with IntegrationMCP is used correctly
func TestAccessChecker_IntegrationMCP(t *testing.T) {
	ac := newMockAccessChecker()
	ac.readPaths["CLU.TEST"] = false

	if ac.CanRead(accesscontrol.IntegrationMCP, "CLU.TEST") {
		t.Fatal("should be hidden")
	}
	if !ac.CanRead(accesscontrol.IntegrationMCP, "CLU.OTHER") {
		t.Fatal("should be visible by default")
	}
}

// Test zero-trust: bot without profiles gets no access
func TestZeroTrust_NoBotDeniesAll(t *testing.T) {
	h := newTestHarness()
	ctx := context.Background() // no bot in context

	result, err := h.server.handleDevices(ctx, makeCallRequest(nil))
	if err != nil {
		t.Fatal(err)
	}

	text := result.Content[0].(mcplib.TextContent).Text
	var resp struct {
		Devices []Device `json:"devices"`
	}
	if err := json.Unmarshal([]byte(text), &resp); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}

	if len(resp.Devices) != 0 {
		t.Fatalf("expected 0 devices without bot context, got %d", len(resp.Devices))
	}
}

func TestZeroTrust_EmptyProfilesDeniesAll(t *testing.T) {
	h := newTestHarness()
	bot := &config.MCPBot{ID: "empty", Name: "Empty", Profiles: []string{}}
	ctx := context.WithValue(context.Background(), botKey, bot)

	result, err := h.server.handleDevices(ctx, makeCallRequest(nil))
	if err != nil {
		t.Fatal(err)
	}

	text := result.Content[0].(mcplib.TextContent).Text
	var resp struct {
		Devices []Device `json:"devices"`
	}
	json.Unmarshal([]byte(text), &resp)

	if len(resp.Devices) != 0 {
		t.Fatalf("expected 0 devices with empty profiles, got %d", len(resp.Devices))
	}
}

// Test path validation rejects traversal attempts
func TestPathValidation_RejectsTraversal(t *testing.T) {
	h := newTestHarness()
	ctx := botCtx()

	cases := []string{"../etc/passwd", "CLU.SW1; rm -rf /", "../../.vclu.json", ""}
	for _, path := range cases {
		result, err := h.server.handleDeviceGet(ctx, makeCallRequest(map[string]any{"path": path}))
		if err != nil {
			t.Fatal(err)
		}
		if !result.IsError {
			t.Errorf("expected error for path %q", path)
		}
	}
}
