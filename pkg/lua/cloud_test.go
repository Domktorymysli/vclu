package lua

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"
)

// sentNotifications collects what Cloud.notify hands to the cloud client.
type sentNotifications struct {
	mu  sync.Mutex
	all []map[string]any
}

func (s *sentNotifications) notifier(raw string) error {
	var m map[string]any
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		return err
	}
	s.mu.Lock()
	s.all = append(s.all, m)
	s.mu.Unlock()
	return nil
}

func (s *sentNotifications) last(t *testing.T) map[string]any {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.all) == 0 {
		t.Fatal("nothing sent")
	}
	return s.all[len(s.all)-1]
}

func TestCloudNotifyEncodes(t *testing.T) {
	engine := NewLuaEngine()
	defer engine.Close()
	if out, _ := engine.Execute(`return select(2, Cloud.notify("x"))`); out != "vclu cloud is not available" {
		t.Errorf("without the cloud: %q", out)
	}
	var sent sentNotifications
	engine.SetCloud(CloudBridge{Notify: sent.notifier})

	luaOK(t, engine, `assert(Cloud.notify("Pralka", "Skończyła"))`)
	if m := sent.last(t); m["type"] != "notify" || m["title"] != "Pralka" || m["body"] != "Skończyła" || m["id"] == "" || m["actions"] != nil {
		t.Errorf("short form = %v", m)
	}
	luaOK(t, engine, `
		local id = Cloud.notify{ title = "Alarm!", priority = "high", to = { 7, "cloud:12" }, tag = "alarm", open = "alarm",
			actions = { { label = "Rozbrój", run = function() end }, { id = "gate", label = "Brama", run = function() end } } }
		assert(id and id:match("^n%d+%-%d+$"), tostring(id))`)
	m := sent.last(t)
	b, _ := json.Marshal(m)
	for _, want := range []string{`"to":"users"`, `"users":["cloud:7","cloud:12"]`, `"open":"/d/alarm"`, `"priority":"high"`,
		`"actions":[{"id":"1","label":"Rozbrój"},{"id":"gate","label":"Brama"}]`} {
		if !strings.Contains(string(b), want) {
			t.Errorf("%s missing in %s", want, b)
		}
	}

	for script, want := range map[string]string{
		`Cloud.notify{}`: "title is required",
		`Cloud.notify{ title = "x", color = "red" }`:                 `unknown option "color"`,
		`Cloud.notify{ title = "x", priority = "urgent" }`:           "priority",
		`Cloud.notify{ title = "x", to = "everyone" }`:               "to is",
		`Cloud.notify{ title = "x", to = { "anna" } }`:               "cloud:<id>",
		`Cloud.notify{ title = "x", actions = { { label = "a" } } }`: "run = function",
		`Cloud.notify{ title = "x", actions = { {label="1",run=print}, {label="2",run=print}, {label="3",run=print}, {label="4",run=print} } }`: "at most 3",
	} {
		_, err := engine.Execute(script)
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: %v, want %q", script, err, want)
		}
	}
}

// A pressed button runs as the person who pressed it, within its roles.
func TestCloudNotifyButtons(t *testing.T) {
	engine, store, _ := newHistoryEngine(t)
	var sent sentNotifications
	engine.SetCloud(CloudBridge{Notify: sent.notifier})
	luaOK(t, engine, `
		local sw = VirtualSwitch:new("alarm")
		registerObject("virt.alarm", sw)
		_:sync("virt.alarm", 1)
		Cloud.notify{ id = "a1", title = "Alarm!", actions = {
			{ id = "disarm", label = "Rozbrój", roles = { "admin" },
			  run = function(who) sw:setValue(0); return "Rozbrojono (" .. who.name .. ")" end },
			{ id = "fail", label = "Nie", run = function() return false, "Brama zablokowana" end },
			{ id = "boom", label = "Błąd", run = function() error("bug") end },
		} }`)

	press := func(action, role string) map[string]any {
		t.Helper()
		out, err := engine.CallAs("remote", "cloud:7", "req-1", "Cloud._action", "a1", action, "cloud:7", "Tytus", role)
		if err != nil {
			t.Fatal(err)
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(out), &m); err != nil {
			t.Fatal(err)
		}
		return m
	}
	if m := press("disarm", "member"); m["ok"] != false || m["reason"] != "forbidden" {
		t.Errorf("member = %v", m)
	}
	if m := press("disarm", "owner"); m["ok"] != true || m["message"] != "Rozbrojono (Tytus)" {
		t.Errorf("owner = %v", m)
	}
	if last, _ := store.Last("virt.alarm"); fmt.Sprint(last.Value) != "0" || last.Origin != "remote" || last.Actor != "cloud:7" || last.CorrelationID != "req-1" {
		t.Errorf("history = %+v", last)
	}
	if m := press("fail", "member"); m["ok"] != false || m["message"] != "Brama zablokowana" {
		t.Errorf("refusal = %v", m)
	}
	if m := press("boom", "member"); m["ok"] != false || m["reason"] != "error" {
		t.Errorf("error = %v", m)
	}
	if m := press("nothing", "owner"); m["reason"] != "not_found" {
		t.Errorf("unknown button = %v", m)
	}
	out, _ := engine.CallAs("remote", "cloud:7", "", "Cloud._action", "other", "disarm", "cloud:7", "Tytus", "owner")
	if !strings.Contains(out, "not_found") {
		t.Errorf("unknown notification = %s", out)
	}
}

func TestCloudChannelForCalendar(t *testing.T) {
	engine := NewLuaEngine()
	defer engine.Close()
	var sent sentNotifications
	engine.SetCloud(CloudBridge{Notify: sent.notifier})
	luaOK(t, engine, `assert(Cloud.channel{ to = "admins" }:send({ calendar = "dom", id = "bio", title = "Bio", kind = "reminder" }))`)
	if m := sent.last(t); m["title"] != "Bio" || m["to"] != "admins" || m["tag"] != "calendar:dom:bio" || !strings.HasPrefix(m["body"].(string), "Przypomnienie") {
		t.Errorf("calendar = %v", m)
	}
}

func TestCloudPresence(t *testing.T) {
	engine, store, _ := newHistoryEngine(t)
	var last string
	engine.SetCloud(CloudBridge{Presence: func() string { return last }})
	deliver := func(json string) {
		t.Helper()
		last = json
		if _, err := engine.Call("Cloud._presence", json); err != nil {
			t.Fatal(err)
		}
	}
	luaOK(t, engine, `
		local sw = VirtualSwitch:new("lights")
		registerObject("virt.lights", sw)
		sw:setValue(1)
		events = {}
		Cloud.onPresence(function(e)
			events[#events + 1] = e.name .. (e.home and " came" or " left") .. (e.anyone and "" or ", nobody home")
			if not e.anyone then sw:setValue(0) end
		end)
		assert(Cloud.anyoneHome() == false)`)

	// The first list after start only sets the state.
	deliver(`[{"user":"cloud:7","name":"Tytus","state":"home","since":100},{"user":"cloud:12","name":"Anna","state":"away","since":90}]`)
	luaOK(t, engine, `
		assert(#events == 0, "events on the first list")
		assert(Cloud.isHome("Tytus") == true and Cloud.isHome("cloud:12") == false and Cloud.isHome("Ola") == nil)
		assert(Cloud.anyoneHome())
		local p = Cloud.presence()
		assert(#p == 2 and p[1].name == "Anna" and p[2].since == 100)`)

	deliver(`[{"user":"cloud:7","name":"Tytus","state":"home","since":100},{"user":"cloud:12","name":"Anna","state":"home","since":200}]`)
	deliver(`[{"user":"cloud:7","name":"Tytus","state":"away","since":300},{"user":"cloud:12","name":"Anna","state":"away","since":300}]`)
	// Someone who starts sharing is not an arrival.
	deliver(`[{"user":"cloud:7","name":"Tytus","state":"away","since":300},{"user":"cloud:12","name":"Anna","state":"away","since":300},{"user":"cloud:20","name":"Ola","state":"home","since":400}]`)
	out, _ := engine.Execute(`return table.concat(events, "; ")`)
	if out != "Anna came; Anna left, nobody home; Tytus left, nobody home" && out != "Anna came; Tytus left, nobody home; Anna left, nobody home" {
		t.Errorf("events: %s", out)
	}
	if e, _ := store.Last("virt.lights"); e.Origin != "remote" || !strings.HasPrefix(e.Actor, "cloud:") {
		t.Errorf("history: %+v", e)
	}

	// After a Lua reload the state comes back from the cloud client, so the next change counts.
	if err := engine.Reinitialize("CLU_TEST"); err != nil {
		t.Fatal(err)
	}
	luaOK(t, engine, `
		assert(Cloud.isHome("Ola") == true, "state lost on reload")
		events = {}
		Cloud.onPresence(function(e) events[#events + 1] = e.name end)`)
	deliver(`[{"user":"cloud:20","name":"Ola","state":"away","since":500}]`)
	if out, _ := engine.Execute(`return table.concat(events, ",")`); out != "Ola" {
		t.Errorf("after reload: %q", out)
	}
}

func TestCloudSwitch(t *testing.T) {
	engine := NewLuaEngine()
	defer engine.Close()
	luaOK(t, engine, `assert(Cloud.enabled() == false)
		local ok, err = Cloud.disable(); assert(ok == nil and err == "vclu cloud is not available")`)

	on := true
	engine.SetCloud(CloudBridge{
		SetEnabled: func(v bool) error { on = v; return nil },
		Enabled:    func() bool { return on },
	})
	luaOK(t, engine, `assert(Cloud.disable() == true); assert(Cloud.enabled() == false)
		assert(Cloud.enable() == true); assert(Cloud.enabled() == true)`)

	engine.SetCloud(CloudBridge{SetEnabled: func(bool) error { return fmt.Errorf("cloud: not paired") }})
	luaOK(t, engine, `local ok, err = Cloud.enable(); assert(ok == nil and err == "cloud: not paired", tostring(err))`)
}
