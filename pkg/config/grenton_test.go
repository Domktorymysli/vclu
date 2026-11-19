package config

import (
	"os"
	"path/filepath"
	"testing"
)

// grenton.gateAlarm.allowDisarm is off unless .vclu.json says true (#214).
func TestGateAlarmAllowDisarm(t *testing.T) {
	var nilConfig *VCLUConfig
	if nilConfig.GateAlarmAllowDisarm() {
		t.Error("a nil config allows disarming")
	}
	dir := t.TempDir()
	for js, want := range map[string]bool{
		`{}`:                           false,
		`{"grenton":{}}`:               false,
		`{"grenton":{"gateAlarm":{}}}`: false,
		`{"grenton":{"gateAlarm":{"allowDisarm":false}}}`: false,
		`{"grenton":{"gateAlarm":{"allowDisarm":true}}}`:  true,
	} {
		if err := os.WriteFile(filepath.Join(dir, ".vclu.json"), []byte(js), 0o600); err != nil {
			t.Fatal(err)
		}
		mc, err := ReadMasterConfig(dir)
		if err != nil {
			t.Fatal(err)
		}
		if got := mc.GateAlarmAllowDisarm(); got != want {
			t.Errorf("%s: %v, want %v", js, got, want)
		}
	}
}
