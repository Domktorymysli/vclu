package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestUpdateCheckEnabled(t *testing.T) {
	dir := t.TempDir()
	if !UpdateCheckEnabled(dir) {
		t.Error("no .vclu.json: on")
	}
	write := func(js string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, ".vclu.json"), []byte(js), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	for _, c := range []struct {
		js   string
		want bool
	}{
		{`{}`, true},
		{`{"updateCheck":{}}`, true},
		{`{"updateCheck":{"enabled":true}}`, true},
		{`{"updateCheck":{"enabled":false}}`, false},
		{`{not json`, false}, // unknown: send nothing
	} {
		write(c.js)
		if got := UpdateCheckEnabled(dir); got != c.want {
			t.Errorf("%s: %v, want %v", c.js, got, c.want)
		}
	}

	write(`{"updateCheck":{"enabled":true}}`)
	for _, v := range []string{"off", "OFF", " off "} {
		t.Setenv(UpdateCheckEnv, v)
		if UpdateCheckEnabled(dir) {
			t.Errorf("%s=%q: still on", UpdateCheckEnv, v)
		}
	}
	t.Setenv(UpdateCheckEnv, "")
	if !UpdateCheckEnabled(dir) {
		t.Error("empty variable: the file decides")
	}
}
