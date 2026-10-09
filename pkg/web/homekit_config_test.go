package web

import (
	"testing"

	"github.com/Domktorymysli/vclu/pkg/config"
)

// Saving keeps the refresh interval and never stores an empty bridge name
// (hap refuses it and the bridge would not start).
func TestHomeKitConfigDefaults(t *testing.T) {
	got := homeKitConfig(&config.VCLUHomeKitConfig{RefreshInterval: 120}, true, "73519284", 5123, "  ", "SN")
	if got.BridgeName != "vCLU Bridge" || got.RefreshInterval != 120 || got.Pin != "73519284" {
		t.Errorf("%+v", got)
	}
	if homeKitConfig(nil, true, "73519284", 5123, "Dom", "").RefreshInterval != 300 {
		t.Error("default refresh interval lost")
	}
}
