package mcp

import (
	"encoding/json"
	"strings"
	"testing"
)

// The runtime schema documents Notifications.on with the sources that reach Lua, and not log.
func TestRuntimeSchema_Notifications(t *testing.T) {
	srv := NewMCPServer(&mockDeviceProvider{}, newMockAccessChecker(), &mockConfigProvider{devMode: true}, &mockMetrics{})
	srv.SetEnabled(true)
	res, err := srv.handleRuntimeSchema(devBotCtx(false), makeCallRequest(nil))
	if err != nil {
		t.Fatal(err)
	}
	text := extractText(t, res)
	if res.IsError {
		t.Fatalf("error: %s", text)
	}
	var schema struct {
		Notifications map[string]string `json:"notifications"`
	}
	if err := json.Unmarshal([]byte(text), &schema); err != nil {
		t.Fatalf("%v: %s", err, text)
	}
	on := schema.Notifications["on"]
	if !strings.Contains(on, "source 'update'|'plugin'|'module'|'login'|'lock',") || strings.Contains(on, "'log'") {
		t.Errorf("on: %s", on)
	}
	if !strings.Contains(schema.Notifications["forward"], "Cloud.notify") || schema.Notifications["sandbox"] == "" {
		t.Errorf("notifications %+v", schema.Notifications)
	}
}
