package lua

import (
	"encoding/json"
	"os"
	"path/filepath"
	goruntime "runtime"
	"strings"
	"testing"

	"github.com/Domktorymysli/vclu/pkg/notify"
)

// Notifications.on handlers get what Go delivers, as the system; a failing handler does not
// stop the next, and unsubscribing stops one.
func TestNotificationsOn(t *testing.T) {
	engine, store, _ := newHistoryEngine(t)
	luaOK(t, engine, `
		local sw = VirtualSwitch:new("siren")
		registerObject("virt.siren", sw)
		got = {}
		Notifications.on(function(n) error("boom") end)
		stop = Notifications.on(function(n)
			got[#got + 1] = n.key .. ":" .. n.level .. ":" .. tostring(n.resolved) .. ":" .. tostring(n.params.ip) .. ":" .. n.count
			if n.source == "login" then sw:setValue(1) end
		end)`)
	deliver := func(d notify.Delivery) {
		t.Helper()
		js, _ := json.Marshal(d)
		if _, err := engine.Call("Notifications._deliver", string(js)); err != nil {
			t.Fatal(err)
		}
	}
	deliver(notify.Delivery{Entry: notify.Entry{ID: 1, Kind: notify.KindEvent, Source: notify.SourceLogin, Level: notify.LevelWarning,
		Key: "login:10.0.0.5", Title: "Failed login to the panel", Count: 1, Params: map[string]string{"ip": "10.0.0.5"}}})
	deliver(notify.Delivery{Entry: notify.Entry{ID: 2, Kind: notify.KindState, Source: notify.SourceUpdate, Level: notify.LevelInfo,
		Key: "update:1.13.0", Count: 1, Params: map[string]string{}}, Resolved: true})
	out, _ := engine.Execute(`return table.concat(got, ";")`)
	if out != "login:10.0.0.5:warning:false:10.0.0.5:1;update:1.13.0:info:true:nil:1" {
		t.Errorf("handlers got %q", out)
	}
	if e, _ := store.Last("virt.siren"); e.Origin != "system" || e.CorrelationID != "notify:1" {
		t.Errorf("history: %+v", e)
	}
	luaOK(t, engine, `stop(); got = {}`)
	deliver(notify.Delivery{Entry: notify.Entry{ID: 3, Key: "x"}})
	if out, _ := engine.Execute(`return #got`); out != "0" {
		t.Errorf("after unsubscribe: %s", out)
	}
	luaOK(t, engine, `assert(not pcall(Notifications.on, 5))`)
}

// Every module load report reaches the handler, the failed modules with their errors.
func TestModuleLoadHandler(t *testing.T) {
	engine := NewLuaEngine()
	defer engine.Close()
	var got [][]ModuleLoadResult
	engine.SetModuleLoadHandler(func(r []ModuleLoadResult) { got = append(got, r) })
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "modules"), 0o755); err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(filepath.Join(dir, "modules", "bad.lua"), []byte("error('boom')"), 0o644)
	_ = os.WriteFile(filepath.Join(dir, "modules", "good.lua"), []byte("x = 1"), 0o644)
	engine.LoadUserModules(dir)
	_ = os.Remove(filepath.Join(dir, "modules", "bad.lua"))
	engine.LoadUserModules(dir)
	if len(got) != 2 || len(got[0]) != 2 || got[0][0].Name != "bad.lua" || got[0][0].OK || !strings.Contains(got[0][0].Error, "boom") ||
		len(got[1]) != 1 || !got[1][0].OK {
		t.Fatalf("reports %+v", got)
	}
}

// A module still broken after a reload stays the same entry, read stays read, and the
// handlers hear nothing new.
func TestModuleLoadHandler_ReloadKeepsState(t *testing.T) {
	engine := NewLuaEngine()
	defer engine.Close()
	n := notify.New(notify.Options{})
	var delivered []notify.Delivery
	n.Subscribe(func(d notify.Delivery) { delivered = append(delivered, d) })
	dir := t.TempDir()
	engine.SetModuleLoadHandler(ModuleNotices(n, filepath.Join(dir, "modules")))
	if err := os.MkdirAll(filepath.Join(dir, "modules"), 0o755); err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(filepath.Join(dir, "modules", "bad.lua"), []byte("error('boom')"), 0o644)
	engine.LoadUserModules(dir)
	entries, _, _ := n.List()
	if len(entries) != 1 || !strings.HasPrefix(entries[0].Detail, "modules/bad.lua:") || strings.Contains(entries[0].Detail, dir) {
		t.Fatalf("entries %+v", entries)
	}
	id := entries[0].ID
	n.MarkRead([]int64{id}, false)

	if err := engine.Reinitialize("VCLU"); err != nil {
		t.Fatal(err)
	}
	engine.LoadUserModules(dir)
	entries, unread, _ := n.List()
	if len(entries) != 1 || entries[0].ID != id || !entries[0].Read || unread != 0 {
		t.Errorf("after reload %+v (unread %d)", entries, unread)
	}
	if len(delivered) != 1 || delivered[0].Resolved {
		t.Errorf("deliveries %+v", delivered)
	}
}

// The source list in Notifications.on's doc is the one Go delivers to Lua.
func TestNotificationsDocSources(t *testing.T) {
	_, self, _, _ := goruntime.Caller(0) // other tests change the working directory
	src, err := os.ReadFile(filepath.Join(filepath.Dir(self), "runtime/core/Notifications.lua"))
	if err != nil {
		t.Fatal(err)
	}
	want := `source ("` + strings.Join(notify.LuaSources(), `"|"`) + `")`
	if !strings.Contains(string(src), want) {
		t.Errorf("Notifications.lua does not document %s", want)
	}
}
