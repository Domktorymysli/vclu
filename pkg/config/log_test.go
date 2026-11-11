package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLogLevel(t *testing.T) {
	dir := t.TempDir()
	for _, c := range []struct{ js, want string }{
		{`{}`, ""},
		{`{"log":{}}`, ""},
		{`{"log":{"level":"debug"}}`, "debug"},
		{`{"log":{"level":"warn"}}`, "warn"},
	} {
		if err := os.WriteFile(filepath.Join(dir, ".vclu.json"), []byte(c.js), 0o600); err != nil {
			t.Fatal(err)
		}
		mc, err := ReadMasterConfig(dir)
		if err != nil {
			t.Fatal(err)
		}
		if got := mc.LogLevel(); got != c.want {
			t.Errorf("%s: %q, want %q", c.js, got, c.want)
		}
	}
	var none *VCLUConfig
	if got := none.LogLevel(); got != "" {
		t.Errorf("nil config: %q, want \"\"", got)
	}
}
