package mcp

import (
	"os"
	"path/filepath"
	"testing"
)

// A proxy as generated from an OM upload: one remote CLU with one switch.
const testProxy = `
CLU220000592 = CLU_REMOTE:new(220000592, 0xC0A80005, system:getEventBus())
local _clu = CLU220000592
dom1 = {}
dom1.DOU4992 = RemoteSwitch:new(_clu, "DOU4992", "x200003152_DOUT1_WIATROLAP")
registerObject("CLU220000592.DOU4992", dom1.DOU4992)
`

func writeProxy(t *testing.T, serverDir string) {
	t.Helper()
	dir := filepath.Join(serverDir, "imports")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "proxy_220000592.lua"), []byte(testProxy), 0644); err != nil {
		t.Fatal(err)
	}
}

func TestSandbox_RecordsCluCalls(t *testing.T) {
	se, ws, serverDir := newTestSandbox(t)
	writeProxy(t, serverDir)
	writeDraft(t, ws, "modules/probe.lua", `
		local light = _:get("CLU220000592.DOU4992")
		if not light then error("proxy objects not loaded") end
		light:switchOn(60000)
		light:setValue(0)
		if light:getValue() ~= 0 then error("optimistic state lost") end
	`)
	res, err := se.RunDraft([]string{"modules/probe.lua"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Success {
		t.Fatalf("draft failed: %v %v", res.Errors, res.Logs)
	}
	want := []CluCall{
		{Target: "192.168.0.5", Script: "DOU4992:execute(1, 60000)"},
		{Target: "192.168.0.5", Script: "DOU4992:set(0, 0)"},
	}
	if len(res.CluCalls) != len(want) {
		t.Fatalf("clu_calls = %+v, want %+v", res.CluCalls, want)
	}
	for i := range want {
		if res.CluCalls[i] != want[i] {
			t.Errorf("clu_calls[%d] = %+v, want %+v", i, res.CluCalls[i], want[i])
		}
	}
	for _, bc := range res.BlockedCalls {
		if bc.Type == "remote.send_command" {
			t.Errorf("CLU command still reported as blocked: %+v", bc)
		}
	}
}
