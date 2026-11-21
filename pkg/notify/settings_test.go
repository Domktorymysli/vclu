package notify

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

type logs struct {
	mu    sync.Mutex
	lines []string
}

func (l *logs) logf(format string, args ...any) {
	l.mu.Lock()
	l.lines = append(l.lines, fmt.Sprintf(format, args...))
	l.mu.Unlock()
}

func (l *logs) has(s string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, line := range l.lines {
		if strings.Contains(line, s) {
			return true
		}
	}
	return false
}

// A .vclu.json that does not parse leaves the defaults and changes are refused, not written over it.
func TestSettingsBadConfig(t *testing.T) {
	dir := t.TempDir()
	bad := []byte("{not json")
	if err := os.WriteFile(filepath.Join(dir, ".vclu.json"), bad, 0o600); err != nil {
		t.Fatal(err)
	}
	l := &logs{}
	s := New(Options{ConfigDir: dir, Logf: l.logf})
	if !l.has("settings not read") || !s.Enabled(SourceUpdate) || s.Enabled(SourceLog) {
		t.Fatalf("logs %v, update %v, log %v", l.lines, s.Enabled(SourceUpdate), s.Enabled(SourceLog))
	}
	s.Add(update("1.13.0"))
	if err := s.SetSources(map[string]bool{SourceUpdate: false}); err == nil {
		t.Error("SetSources saved into a broken file")
	}
	if list, _, _ := s.List(); len(list) != 1 || !s.Enabled(SourceUpdate) {
		t.Errorf("a failed save changed the store: %+v", list)
	}
	if err := s.Mute("update:1.13.0", 0); err == nil {
		t.Error("Mute saved into a broken file")
	}
	if b, _ := os.ReadFile(filepath.Join(dir, ".vclu.json")); string(b) != string(bad) {
		t.Errorf(".vclu.json changed: %s", b)
	}
}

func TestMuteNoKey(t *testing.T) {
	s := New(Options{Logf: t.Logf})
	if err := s.Mute("", time.Hour); err == nil {
		t.Error("an empty key muted")
	}
	if m := s.Settings().Muted; len(m) != 0 {
		t.Errorf("muted %+v", m)
	}
}

// Settings leaves out mutes expired since the last write, sorts by key, and titles a mute
// without one by its key.
func TestSettingsMutedList(t *testing.T) {
	dir := t.TempDir()
	cfg := `{"notifications": {"muted": {
		"b": {"until": "forever"},
		"a": {"until": "2026-11-21T13:00:00Z", "title": "A"},
		"c": {"until": "2026-11-21T11:00:00Z", "title": "C"},
		"d": {"until": "not a time"}}}}`
	if err := os.WriteFile(filepath.Join(dir, ".vclu.json"), []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	c := &clock{t: time.Date(2026, 11, 21, 12, 0, 0, 0, time.UTC)}
	s := New(Options{ConfigDir: dir, Now: c.now, Logf: t.Logf})
	if got := fmt.Sprint(s.Settings().Muted); got != "[{a 2026-11-21T13:00:00Z A  map[]} {b forever b  map[]}]" {
		t.Errorf("muted %s", got)
	}
	c.add(2 * time.Hour)
	if got := fmt.Sprint(s.Settings().Muted); got != "[{b forever b  map[]}]" {
		t.Errorf("after an hour %s", got)
	}
}
