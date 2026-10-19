package mcp

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A Smart Panel as the OM import generates it: the panel and its buttons on their own CLU.
const testPanelProxy = `
CLU470000100 = CLU_REMOTE:new(470000100, 0xC0A8000A, system:getEventBus())
local _clu = CLU470000100
smartpanel = {}
smartpanel.PAN9852 = RemotePanelButton:new(_clu, "PAN9852", "x250002119_PANEL_BUTTON_01")
smartpanel.PAN5618 = RemotePanelButton:new(_clu, "PAN5618", "x250002119_PANEL_BUTTON_02")
smartpanel.PAN4994 = RemotePanelButton:new(_clu, "PAN4994", "x250002119_PANEL_BUTTON_03")
smartpanel.PAN8281 = RemotePanelButton:new(_clu, "PAN8281", "x250002119_PANEL_BUTTON_04")
smartpanel.PAN1085 = RemotePanel:new(_clu, "PAN1085", "x250002119_PANEL_01")
smartpanel.PAN1085:addButton(0, smartpanel.PAN9852)
smartpanel.PAN1085:addButton(1, smartpanel.PAN5618)
smartpanel.PAN1085:addButton(2, smartpanel.PAN4994)
smartpanel.PAN1085:addButton(3, smartpanel.PAN8281)
registerObject("CLU470000100.PAN1085", smartpanel.PAN1085)
`

const testPanelModule = `
local Panel = require("lib/panel")
LAMPA = VirtualSwitch:new("lampa")
Panel.new(_:get("CLU470000100.PAN1085"))
	:home(Panel.menu("Biuro"):toggle("Lampa", LAMPA):toggle("Druga", VirtualSwitch:new("druga")))
`

func newPanelSandbox(t *testing.T) (*SandboxEngine, []string) {
	t.Helper()
	se, ws, serverDir := newTestSandbox(t)
	dir := filepath.Join(serverDir, "imports")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "proxy_470000100.lua"), []byte(testPanelProxy), 0644); err != nil {
		t.Fatal(err)
	}
	writeDraft(t, ws, "modules/panel.lua", testPanelModule)
	return se, []string{"modules/panel.lua"}
}

func panelFrames(res *SandboxResult) string {
	var out []string
	for _, c := range res.CluCalls {
		out = append(out, c.Script)
	}
	return strings.Join(out, "\n")
}

// An agent drives a panel module in the sandbox: the home frame after start, a button
// press by the button's id as Object Manager sends it, and the frame that follows.
func TestScenario_PanelClickDrawsFrames(t *testing.T) {
	se, paths := newPanelSandbox(t)
	res := runScenario(t, se, paths, "2026-10-18T10:00:00+02:00", `[
		{"advance": 1},
		{"click": "PAN9852", "input": "btn3"},
		{"advance": 1},
		{"click": "PAN1085", "input": "btn4"},
		{"advance": 1}
	]`)
	frames := panelFrames(res)
	// home, cursor down to Druga, OK switches it on and the row follows
	for _, want := range []string{`PAN1085:execute(3, 0, 14, ">", 1)`, `PAN1085:execute(3, 0, 27, ">", 1)`, `PAN1085:execute(3, 8, 14, "Lampa", 1)`, `"ON"`, `PAN1085:execute(7)`, `PAN9852:execute(1)`} {
		if !strings.Contains(frames, want) {
			t.Errorf("frames lack %s:\n%s", want, frames)
		}
	}
	for _, c := range res.CluCalls {
		if c.Target != "192.168.0.10" {
			t.Errorf("call to %s, want the panel's CLU", c.Target)
		}
	}
	requireSuccess(t, res)
}

// A click no panel takes fails the step with a hint, instead of passing quietly.
func TestScenario_PanelClickUnknownFails(t *testing.T) {
	se, paths := newPanelSandbox(t)
	res := runScenario(t, se, paths, "2026-10-18T10:00:00+02:00", `[{"click": "PAN0000", "input": "btn1"}]`)
	if res.Success || len(res.Errors) != 1 || !strings.Contains(res.Errors[0], `no panel "PAN0000"`) {
		t.Errorf("errors = %v", res.Errors)
	}
}

func TestParseSteps_Click(t *testing.T) {
	bad := map[string]string{
		`[{"click": "PAN1"}]`:                          "click needs a panel and an input",
		`[{"click": "", "input": "btn1"}]`:             "click needs a panel and an input",
		`[{"advance": 1, "input": "btn1"}]`:            "input is only allowed with click",
		`[{"click": "P", "input": "b", "advance": 1}]`: "exactly one",
	}
	for js, want := range bad {
		if _, err := ParseSteps([]byte(js)); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: err = %v, want %q", js, err, want)
		}
	}
}
