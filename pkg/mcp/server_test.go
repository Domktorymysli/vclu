package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Domktorymysli/vclu/pkg/accesscontrol"
	"github.com/Domktorymysli/vclu/pkg/config"
)

// --- Mock implementations for server tests ---

type mockDeviceProvider struct {
	devices []Device
	scenes  []Scene
}

func (m *mockDeviceProvider) ListDevices(_ context.Context, _ string) ([]Device, error) {
	return m.devices, nil
}
func (m *mockDeviceProvider) GetDevice(_ context.Context, path string) (*DeviceDetail, error) {
	for _, d := range m.devices {
		if d.Path == path {
			return &DeviceDetail{
				Path: d.Path, Type: d.Type, Name: d.Name,
				State: d.State, Readonly: d.Readonly, Methods: d.Methods,
			}, nil
		}
	}
	return nil, nil
}
func (m *mockDeviceProvider) SetValue(_ context.Context, path string, _ any) (*SetResult, error) {
	// Simulate Lua MCP.set_value readonly check
	for _, d := range m.devices {
		if d.Path == path && d.Readonly {
			return nil, fmt.Errorf("device is readonly: %s", path)
		}
	}
	return &SetResult{OK: true, NewState: 1}, nil
}
func (m *mockDeviceProvider) ExecuteMethod(_ context.Context, path, _ string, _ []any) (*ExecResult, error) {
	// Simulate Lua MCP.execute_method readonly check
	for _, d := range m.devices {
		if d.Path == path && d.Readonly {
			return nil, fmt.Errorf("device is readonly: %s", path)
		}
	}
	return &ExecResult{OK: true}, nil
}
func (m *mockDeviceProvider) ListScenes(_ context.Context) ([]Scene, error) {
	return m.scenes, nil
}
func (m *mockDeviceProvider) ExecuteScene(_ context.Context, _ string) error {
	return nil
}

type mockAccessChecker struct {
	readPaths    map[string]bool
	controlPaths map[string]bool
}

func newMockAccessChecker() *mockAccessChecker {
	return &mockAccessChecker{
		readPaths:    make(map[string]bool),
		controlPaths: make(map[string]bool),
	}
}

func (m *mockAccessChecker) CanRead(_ accesscontrol.Integration, path string) bool {
	v, ok := m.readPaths[path]
	if !ok {
		return true // default: visible
	}
	return v
}

func (m *mockAccessChecker) CanControl(_ accesscontrol.Integration, path string) bool {
	v, ok := m.controlPaths[path]
	if !ok {
		return true // default: controllable
	}
	return v
}

func (m *mockAccessChecker) ResolveAccess(profileNames []string, path string) accesscontrol.AccessLevel {
	// For tests: delegate to CanRead/CanControl
	if !m.CanRead(accesscontrol.IntegrationMCP, path) {
		return accesscontrol.AccessHidden
	}
	if !m.CanControl(accesscontrol.IntegrationMCP, path) {
		return accesscontrol.AccessReadonly
	}
	return accesscontrol.AccessFull
}

type mockMetrics struct {
	requests int
	sessions int64
	activity []ActivityEntry
}

func (m *mockMetrics) RecordMCPRequest(_ string, _ bool, _ time.Duration) {
	m.requests++
}
func (m *mockMetrics) IncMCPSessions(delta int64) {
	m.sessions += delta
}
func (m *mockMetrics) AddMCPActivity(entry ActivityEntry) {
	m.activity = append(m.activity, entry)
}

// --- Tests ---

func newTestServer() (*MCPServer, *mockConfigProvider) {
	devices := &mockDeviceProvider{
		devices: []Device{
			{Path: "CLU.SW1", Type: "switch", Name: "Switch 1", State: 0, Methods: []string{"toggle"}},
			{Path: "CLU.TEMP", Type: "sensor", Name: "Temp", State: 22.5, Readonly: true},
		},
		scenes: []Scene{
			{Path: "CLU.MovieMode", Name: "Movie Mode"},
		},
	}

	key := "vclu_mcp_testkey123"
	cfg := &mockConfigProvider{bots: []config.MCPBot{{
		ID: "bot-test", Name: "Test", APIKeyHash: config.HashAPIKey(key), Profiles: []string{"full-access"},
	}}}
	access := newMockAccessChecker()
	metrics := &mockMetrics{}

	srv := NewMCPServer(devices, access, cfg, metrics)
	srv.SetEnabled(true)

	return srv, cfg
}

func TestHandler_DisabledReturns503(t *testing.T) {
	srv, _ := newTestServer()
	srv.SetEnabled(false)

	req := httptest.NewRequest("POST", "/mcp", nil)
	req.Header.Set("Authorization", "Bearer vclu_mcp_testkey123")
	w := httptest.NewRecorder()

	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("expected 503, got %d", w.Code)
	}

	var body map[string]string
	json.NewDecoder(w.Body).Decode(&body)
	if body["error"] != "MCP server is disabled" {
		t.Errorf("unexpected error: %s", body["error"])
	}
}

func TestHandler_NoTokenReturns401(t *testing.T) {
	srv, _ := newTestServer()

	req := httptest.NewRequest("POST", "/mcp", nil)
	w := httptest.NewRecorder()

	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", w.Code)
	}
}

func TestHandler_WrongTokenReturns401(t *testing.T) {
	srv, _ := newTestServer()

	req := httptest.NewRequest("POST", "/mcp", nil)
	req.Header.Set("Authorization", "Bearer wrong-key")
	w := httptest.NewRecorder()

	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", w.Code)
	}
}

func TestHandler_ValidTokenPassesThrough(t *testing.T) {
	srv, _ := newTestServer()

	// A valid MCP request would be a proper JSON-RPC call, but we just check
	// that the handler doesn't return 401/503 — it will delegate to mcp-go.
	req := httptest.NewRequest("POST", "/mcp", nil)
	req.Header.Set("Authorization", "Bearer vclu_mcp_testkey123")
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	srv.Handler().ServeHTTP(w, req)

	// Should NOT be 401 or 503
	if w.Code == http.StatusUnauthorized || w.Code == http.StatusServiceUnavailable {
		body, _ := io.ReadAll(w.Body)
		t.Errorf("expected pass-through, got %d: %s", w.Code, string(body))
	}
}

func TestSetEnabled(t *testing.T) {
	srv, _ := newTestServer()

	srv.SetEnabled(false)
	if srv.IsEnabled() {
		t.Fatal("expected disabled")
	}

	srv.SetEnabled(true)
	if !srv.IsEnabled() {
		t.Fatal("expected enabled")
	}
}
