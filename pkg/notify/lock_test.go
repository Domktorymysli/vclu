package notify

import (
	"testing"
	"time"
)

// A device locked twice is one entry with a count; it goes to Lua and names the device.
func TestDeviceLocked(t *testing.T) {
	s, _, k := newTestStore(t, t.TempDir())
	before := time.Now()
	DeviceLocked(s, "vclu:brama", 30*time.Second)
	DeviceLocked(s, "vclu:brama", time.Minute)
	list, _, _ := s.List()
	if len(list) != 1 {
		t.Fatalf("%+v", list)
	}
	e := list[0]
	until, err := time.Parse(time.RFC3339, e.Params["until"])
	if e.Key != "lock:device:vclu:brama" || e.Source != SourceLock || e.Msg != MsgLockDevice || e.Count != 2 ||
		e.Params["device"] != "vclu:brama" || err != nil || until.Before(before.Add(59*time.Second).Truncate(time.Second)) {
		t.Fatalf("%+v", e)
	}
	if e.Title != "PIN of vclu:brama locked" {
		t.Errorf("title %q", e.Title)
	}
	if info, _ := Lookup(e.Source); !info.ToLua || len(k.list()) == 0 {
		t.Error("a device lock is not delivered")
	}
}
