package logstream

import (
	"bytes"
	"log"
	"strings"
	"testing"
)

// lines logs each line through a LogStream at level and returns stdout and the buffered messages.
func lines(t *testing.T, level string, in ...string) (stdout string, buffered []string) {
	t.Helper()
	var out bytes.Buffer
	ls := NewLogStream(&out, 100)
	if level != "-" {
		if err := ls.SetLevel(level); err != nil {
			t.Fatal(err)
		}
	}
	l := log.New(ls, "", log.Ldate|log.Ltime)
	for _, s := range in {
		l.Println(s)
	}
	for _, e := range ls.GetHistory() {
		buffered = append(buffered, e.Message)
	}
	return out.String(), buffered
}

var sample = []string{
	"[DEBUG] MQTT[MQTT]:publish(x)",
	"[INFO] module loaded",
	"[WARN] sensor stale",
	"[ERROR] module broke",
	"[MQTT] connected",
	"plain print from a module",
}

func TestLevelDefaultHidesDebugFromStdout(t *testing.T) {
	stdout, buffered := lines(t, "-", sample...)
	if strings.Contains(stdout, "[DEBUG]") {
		t.Errorf("debug on stdout at the default level:\n%s", stdout)
	}
	for _, s := range sample[1:] {
		if !strings.Contains(stdout, s) {
			t.Errorf("%q missing from stdout:\n%s", s, stdout)
		}
	}
	if len(buffered) != len(sample) {
		t.Errorf("ring buffer has %d lines, want all %d: %q", len(buffered), len(sample), buffered)
	}
}

func TestLevelFiltersStdout(t *testing.T) {
	for _, c := range []struct {
		level string
		want  []string // sample lines on stdout, by their start
	}{
		{"debug", sample},
		{"info", sample[1:]},
		{"", sample[1:]},
		{"WARN", sample[2:4]},
		{"warning", sample[2:4]},
		{"error", sample[3:4]},
	} {
		stdout, buffered := lines(t, c.level, sample...)
		if got := strings.Count(stdout, "\n"); got != len(c.want) {
			t.Errorf("level %q: %d lines on stdout, want %d:\n%s", c.level, got, len(c.want), stdout)
		}
		for _, s := range c.want {
			if !strings.Contains(stdout, s) {
				t.Errorf("level %q: %q missing from stdout", c.level, s)
			}
		}
		if len(buffered) != len(sample) {
			t.Errorf("level %q: ring buffer has %d lines, want %d", c.level, len(buffered), len(sample))
		}
	}
}

func TestLevelUnknownIsInfo(t *testing.T) {
	var out bytes.Buffer
	ls := NewLogStream(&out, 10)
	_ = ls.SetLevel("debug")
	if err := ls.SetLevel("verbose"); err == nil {
		t.Error("SetLevel(verbose): no error")
	}
	l := log.New(ls, "", log.Ldate|log.Ltime)
	l.Println("[DEBUG] hidden")
	l.Println("[INFO] shown")
	if strings.Contains(out.String(), "hidden") || !strings.Contains(out.String(), "shown") {
		t.Errorf("unknown level does not behave as info:\n%s", out.String())
	}
}

// An untagged line keeps what the text says: Go's "failed" lines pass a warn or error level.
func TestLevelUntaggedErrorPassesErrorLevel(t *testing.T) {
	stdout, _ := lines(t, "error", "[HomeKit] failed to start: boom", "Warning: no translations", "[MQTT] connected")
	if !strings.Contains(stdout, "failed to start") {
		t.Errorf("untagged error line missing at level error:\n%s", stdout)
	}
	if strings.Contains(stdout, "connected") || strings.Contains(stdout, "translations") {
		t.Errorf("info or warning line at level error:\n%s", stdout)
	}
}

// The level tag decides: a debug line that mentions an error stays off stdout.
func TestLevelTagWins(t *testing.T) {
	stdout, _ := lines(t, "info", "[DEBUG] [CLIENT-REPORT] from 10.0.0.2 values={1}", "[DEBUG] retry after error")
	if stdout != "" {
		t.Errorf("tagged debug on stdout:\n%s", stdout)
	}
}

// log.Lshortfile (--verbose) puts file:line before the tag.
func TestLevelWithShortfile(t *testing.T) {
	var out bytes.Buffer
	ls := NewLogStream(&out, 10)
	l := log.New(ls, "", log.Ldate|log.Ltime|log.Lshortfile)
	l.Println("[DEBUG] hidden")
	l.Println("[INFO] shown")
	if strings.Contains(out.String(), "hidden") || !strings.Contains(out.String(), "shown") {
		t.Errorf("Lshortfile line filtered wrong:\n%s", out.String())
	}
}

func TestParseLevel(t *testing.T) {
	for in, want := range map[string]int32{"debug": LevelDebug, "": LevelInfo, " Info ": LevelInfo, "warn": LevelWarn, "warning": LevelWarn, "ERROR": LevelError} {
		got, err := ParseLevel(in)
		if err != nil || got != want {
			t.Errorf("ParseLevel(%q) = %d, %v; want %d", in, got, err, want)
		}
	}
	if got, err := ParseLevel("trace"); err == nil || got != LevelInfo {
		t.Errorf("ParseLevel(trace) = %d, %v; want info and an error", got, err)
	}
}
