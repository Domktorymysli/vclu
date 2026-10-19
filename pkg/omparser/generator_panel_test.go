package omparser

import (
	"strings"
	"testing"
)

// A Smart Panel from an om.lua (lines as Object Manager writes them): the screen (class 29)
// becomes a RemotePanel, its buttons (class 30) RemotePanelButtons attached by index, and
// neither is registered for Home Assistant.
const panelOM = `CLU212345678 = OBJECT:new(0, 0xC0A80003)
-- NAME_CLU DOM20=CLU212345678
-- MODULES
mm_250000067 = OBJECT:new(2, 0x0ee6b5c3, 0x1f, "0ee6b5c3") -- 250000067 02 01
mm_330000027 = OBJECT:new(2, 0x13ab669b, 0x1e, "13ab669b") -- 330000027 02 01
-- IO_MODULES
 PAN5621 = OBJECT:new(30, mm_250000067, 0)
-- NAME_IO x250000067_PANEL_BUTTON1=PAN5621
 PAN0312 = OBJECT:new(30, mm_250000067, 1)
-- NAME_IO P1_SALON_B2=PAN0312
 PAN9297 = OBJECT:new(29, mm_250000067, 0)
-- NAME_IO P1_PANEL_SALON=PAN9297
 PAN3925 = OBJECT:new(20, mm_250000067, 0)
-- NAME_IO P1_PANEL_SALON_TEMP=PAN3925
 PAN4340 = OBJECT:new(19, mm_250000067, 0)
 DOU5048 = OBJECT:new(4, mm_330000027, 0)
`

func TestGenerateProxySmartPanel(t *testing.T) {
	parsed, err := Parse(panelOM)
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}
	config := DefaultConfig()
	config.Namespace = "dom"
	out := GenerateProxy(parsed, config)

	for _, want := range []string{
		`dom.PAN5621 = RemotePanelButton:new(_clu, "PAN5621", "x250000067_PANEL_BUTTON1")`,
		`dom.P1_SALON_B2 = RemotePanelButton:new(_clu, "PAN0312", "P1_SALON_B2")`,
		`dom.P1_PANEL_SALON = RemotePanel:new(_clu, "PAN9297", "P1_PANEL_SALON")`,
		`dom.P1_PANEL_SALON:addButton(0, dom.PAN5621)`,
		`dom.P1_PANEL_SALON:addButton(1, dom.P1_SALON_B2)`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing line: %s", want)
		}
	}
	// Buttons are attached after both exist
	if strings.Index(out, "addButton(0") < strings.Index(out, "RemotePanel:new") {
		t.Error("addButton before the panel is created")
	}
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, "registerObject(") &&
			(strings.Contains(line, "PAN9297") || strings.Contains(line, "PAN5621")) {
			t.Errorf("panel object registered: %s", line)
		}
	}
	if strings.Contains(out, "PAN4340") {
		t.Error("PANEL_PAGE generated, vCLU does not use it")
	}
}

// A Smart Panel WiFi is its own CLU (declared with GATE:new) with the same panel module.
func TestGenerateProxySmartPanelWifi(t *testing.T) {
	parsed, err := Parse(`CLU470000100 = GATE:new(5000, 0xC0A80007, 0xFFFFFF00, 0xC0A80001, "", "")
-- NAME_CLU WIFI=CLU470000100
mm_250002119 = OBJECT:new(2, 0x0ee6bac7, 0x03, "0ee6bac7") -- 250002119 03 04
PAN7110 = OBJECT:new(30, mm_250002119, 0)
-- NAME_IO x250002119_PANEL_BUTTON1=PAN7110
PAN3353 = OBJECT:new(29, mm_250002119, 0)
-- NAME_IO x250002119_PANEL1=PAN3353
PAN1364 = OBJECT:new(20, mm_250002119, 0)
`)
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}
	out := GenerateProxy(parsed, DefaultConfig())
	for _, want := range []string{
		`CLU470000100 = CLU_REMOTE:new(470000100, 0xC0A80007, system:getEventBus())`,
		`wifi.PAN3353 = RemotePanel:new(_clu, "PAN3353", "x250002119_PANEL1")`,
		`wifi.PAN3353:addButton(0, wifi.PAN7110)`,
		`wifi.PAN1364 = RemoteValueSensor:new(_clu, "PAN1364"`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing line: %s", want)
		}
	}
}
