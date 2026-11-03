package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLuaCallbackTimeout(t *testing.T) {
	dir := t.TempDir()
	write := func(js string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, ".vclu.json"), []byte(js), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	read := func() time.Duration {
		t.Helper()
		mc, err := ReadMasterConfig(dir)
		if err != nil {
			t.Fatal(err)
		}
		return mc.LuaCallbackTimeout()
	}
	if got := read(); got != 0 {
		t.Errorf("no .vclu.json: %v, want 0 (default)", got)
	}
	for _, c := range []struct {
		js   string
		want time.Duration
	}{
		{`{}`, 0},
		{`{"lua":{}}`, 0},
		{`{"lua":{"callbackTimeoutSeconds":5}}`, 5 * time.Second},
		{`{"lua":{"callbackTimeoutSeconds":-1}}`, -1},
	} {
		write(c.js)
		if got := read(); got != c.want {
			t.Errorf("%s: %v, want %v", c.js, got, c.want)
		}
	}

	// another handler's write keeps the setting
	write(`{"lua":{"callbackTimeoutSeconds":7}}`)
	if err := UpdateMasterConfig(dir, func(c *VCLUConfig) error { c.ProjectName = "x"; return nil }); err != nil {
		t.Fatal(err)
	}
	if got := read(); got != 7*time.Second {
		t.Errorf("after UpdateMasterConfig: %v, want 7s", got)
	}
}
