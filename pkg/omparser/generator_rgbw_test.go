package omparser

import (
	"strings"
	"testing"
)

// An LED RGBW 2.0 module (type 0x18 DIN, 0x1d FM) holds an LEDRGB light and four LED_CHANNEL
// objects (class 49); the older LED RGB module (0x04) has the light alone, without white.
const rgbwOM = `CLU212345678 = OBJECT:new(0, 0xC0A80003)
-- NAME_CLU DOM20=CLU212345678
mm_270000011 = OBJECT:new(2, 0x1017df8b, 0x18, "1017df8b") -- 270000011 02 03
mm_120000005 = OBJECT:new(2, 0x0727a9c5, 0x04, "0727a9c5") -- 120000005 01 0a
 LED1001 = OBJECT:new(9, mm_270000011, 0)
-- NAME_IO SALON_RGBW=LED1001
 LED1002 = OBJECT:new(49, mm_270000011, 0)
-- NAME_IO SALON_R=LED1002
 LED1005 = OBJECT:new(49, mm_270000011, 3)
-- NAME_IO SALON_W=LED1005
 LED2001 = OBJECT:new(9, mm_120000005, 0)
-- NAME_IO KUCHNIA_RGB=LED2001
`

// The proxy gets one object per channel, and the light of an RGBW module is marked with
// _hasWhite (the runtime subscribes its white and announces rgbw to Home Assistant).
func TestGenerateProxyLedChannels(t *testing.T) {
	parsed, err := Parse(rgbwOM)
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}
	config := DefaultConfig()
	config.Namespace = "dom"
	out := GenerateProxy(parsed, config)
	for _, want := range []string{
		`dom.SALON_RGBW = RemoteRGBLight:new(_clu, "LED1001", "SALON_RGBW")`,
		"dom.SALON_RGBW._hasWhite = true\n",
		`dom.SALON_R = RemoteLedChannel:new(_clu, "LED1002", "SALON_R")`,
		`dom.SALON_W = RemoteLedChannel:new(_clu, "LED1005", "SALON_W")`,
		`registerObject("CLU212345678.LED1005", dom.SALON_W)`,
		`dom.KUCHNIA_RGB = RemoteRGBLight:new(_clu, "LED2001", "KUCHNIA_RGB")`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing line: %s", want)
		}
	}
	if strings.Contains(out, "dom.KUCHNIA_RGB._hasWhite") {
		t.Error("an LED RGB module (0x04) has no white")
	}
}
