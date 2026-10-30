package lua

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"
)

// cmdAnswer is what Cloud._command returns (proto.CmdResult without type and id).
type cmdAnswer struct {
	OK      bool   `json:"ok"`
	Reason  string `json:"reason"`
	Message string `json:"message"`
}

// commander runs Cloud._command the way cmd/vclu does: as the person, with the request id.
type commander struct {
	t      *testing.T
	engine *LuaEngine
}

func (c commander) run(path, action, roles string, confirmed bool, pin string) cmdAnswer {
	c.t.Helper()
	return c.runBy(path, action, roles, confirmed, pin, float64(time.Now().Add(time.Minute).UnixMilli()))
}

func (c commander) runBy(path, action, roles string, confirmed bool, pin string, deadlineMs float64) cmdAnswer {
	c.t.Helper()
	out, err := c.engine.CallAs("remote", "cloud:7", "cmd-1", "Cloud._command", path, action, roles, confirmed, pin, deadlineMs)
	if err != nil {
		c.t.Fatal(err)
	}
	var a cmdAnswer
	if err := json.Unmarshal([]byte(out), &a); err != nil {
		c.t.Fatalf("answer %q: %v", out, err)
	}
	return a
}

// calls returns the methods the fake devices recorded since the last call, and forgets them.
func takeCalls(t *testing.T, engine *LuaEngine) string {
	t.Helper()
	return mustExec(t, engine, `(function() local s = table.concat(_calls, " "); _calls = {}; return s end)()`)
}

const commandDevices = `
	_calls = {}
	local function rec(name, ...)
		local ret = { ... }
		return function(self) _calls[#_calls + 1] = name; return unpack(ret) end
	end
	local function base()
		return { _lastState = 0,
			get = function(s) return s._lastState end,
			set = function(s, i, v) _calls[#_calls + 1] = "set"; s._lastState = v end,
			onChange = function() return function() end end }
	end
	local cover = base()
	cover.setPosition, cover.open, cover.close, cover.stop = rec("setPosition"), rec("open"), rec("close"), rec("stop")
	expose(cover, "cover", { id = "roleta" }):remote()

	local bare = base() -- a cover without its own methods: never written through set
	expose(bare, "cover", { id = "bare" }):remote()

	local plug = base() -- a switch without switchOn: setValue, as Home Assistant does
	expose(plug, "switch", { id = "plug" }):remote()

	local broken = base()
	broken.switchOn = rec("switchOn", false, "Brak zasilania")
	broken.switchOff = function() error("bus timeout") end
	expose(broken, "light", { id = "broken" }):remote()

	local scene = base()
	scene.execute = rec("execute")
	expose(scene, "scene", { id = "wieczor" }):remote()

	expose(base(), "temperature", { id = "temp" }):remote()
	expose(base(), "switch", { id = "local" })
	expose(base(), "switch", { id = "nopin" }):remote({ pin = true })
	expose(base(), "switch", { id = "ro" }):remote({ readonly = true })
	expose(base(), "switch", { id = "gro" }):readonly():remote()
	expose(base(), "switch", { id = "gate" }):remote({ confirm = true }):roles("domownicy")
	expose(base(), "switch", { id = "safe" }):pin("0420"):remote({ pin = true })
	expose(base(), "switch", { id = "safe2" }):pin(1234):remote({ pin = true })
`

// Contract: a remote command calls the device's own method (a cover's open(), a switch's
// switchOn()), never a setValue the device does not mean, and only actions that fit its type.
func TestCloudCommandCallsOwnMethods(t *testing.T) {
	engine, store, _ := newHistoryEngine(t)
	c := commander{t, engine}
	mustExec(t, engine, commandDevices)
	luaOK(t, engine, `
		local sw = VirtualSwitch:new("lampa")
		registerObject("virt.lampa", sw)
		_:sync("virt.lampa", 0)
		expose(sw, "light"):remote()`)

	if a := c.run("virt.lampa", "on", "[]", false, ""); !a.OK {
		t.Fatalf("on = %+v", a)
	}
	if last, _ := store.Last("virt.lampa"); fmt.Sprint(last.Value) != "1" || last.Origin != "remote" || last.Actor != "cloud:7" || last.CorrelationID != "cmd-1" {
		t.Errorf("history = %+v", last)
	}
	if v := mustExec(t, engine, `_:get("virt.lampa"):getValue()`); v != "1" {
		t.Errorf("lamp value %s", v)
	}

	for _, action := range []string{"open", "close", "stop"} {
		if a := c.run("vclu:roleta", action, "[]", false, ""); !a.OK {
			t.Errorf("%s = %+v", action, a)
		}
	}
	if got := takeCalls(t, engine); got != "open close stop" {
		t.Errorf("cover calls %q, want its own methods only", got)
	}
	if a := c.run("vclu:bare", "open", "[]", false, ""); a.Reason != "invalid" {
		t.Errorf("cover without open() = %+v", a)
	}
	if a := c.run("vclu:plug", "on", "[]", false, ""); !a.OK {
		t.Errorf("plug on = %+v", a)
	}
	if got := takeCalls(t, engine); got != "set" {
		t.Errorf("calls %q: a cover without open() was written, or the plug was not", got)
	}

	if a := c.run("vclu:broken", "on", "[]", false, ""); a.OK || a.Reason != "error" || a.Message != "Brak zasilania" {
		t.Errorf("refused by the device = %+v", a)
	}
	// a script error stays in vCLU's log: the person gets a plain message, not a Lua trace
	if a := c.run("vclu:broken", "off", "[]", false, ""); a.OK || a.Reason != "error" || a.Message != "Błąd w skrypcie vCLU." {
		t.Errorf("device error = %+v", a)
	}
	// a scene run twice in a row: the second falls in the cooldown, and it still ran
	for i := 0; i < 2; i++ {
		if a := c.run("vclu:wieczor", "execute", "[]", false, ""); !a.OK {
			t.Errorf("scene run %d = %+v", i, a)
		}
	}
	takeCalls(t, engine)

	for _, tc := range []struct{ path, action string }{
		{"vclu:plug", "open"}, {"vclu:roleta", "on"}, {"vclu:wieczor", "off"}, {"vclu:temp", "on"},
		{"vclu:plug", "explode"}, {"vclu:plug", "setValue"},
	} {
		if a := c.run(tc.path, tc.action, "[]", false, ""); a.Reason != "invalid" {
			t.Errorf("%s on %s = %+v", tc.action, tc.path, a)
		}
	}
	if got := takeCalls(t, engine); got != "" {
		t.Errorf("an invalid command called %q", got)
	}
}

// Contract: the checks of :remote(), :roles() and :pin() hold on every command.
func TestCloudCommandChecks(t *testing.T) {
	engine := NewLuaEngine()
	defer engine.Close()
	c := commander{t, engine}
	mustExec(t, engine, commandDevices)

	for _, path := range []string{"vclu:local", "vclu:nothing", "vclu:nopin"} {
		if a := c.run(path, "on", "[]", false, ""); a.Reason != "not_found" {
			t.Errorf("%s = %+v", path, a)
		}
	}
	mustExec(t, engine, `ExposedObjects:get("vclu:plug"):remote(false)`)
	if a := c.run("vclu:plug", "on", "[]", false, ""); a.Reason != "not_found" {
		t.Errorf("after :remote(false) = %+v", a)
	}
	for _, path := range []string{"vclu:ro", "vclu:gro"} {
		if a := c.run(path, "on", "[]", false, ""); a.Reason != "forbidden" {
			t.Errorf("readonly %s = %+v", path, a)
		}
	}

	// roles, then confirmation
	if a := c.run("vclu:gate", "on", `["gosia"]`, true, ""); a.Reason != "forbidden" {
		t.Errorf("without the role = %+v", a)
	}
	if a := c.run("vclu:gate", "on", `not json`, true, ""); a.Reason != "forbidden" {
		t.Errorf("bad roles = %+v", a)
	}
	if a := c.run("vclu:gate", "on", `["gosia","domownicy"]`, false, ""); a.Reason != "confirm" {
		t.Errorf("unconfirmed = %+v", a)
	}
	if a := c.run("vclu:gate", "on", `["domownicy"]`, true, ""); !a.OK {
		t.Errorf("confirmed with the role = %+v", a)
	}

	// PIN: required, wrong, five wrong in a row lock the device, a right one resets the count
	if a := c.run("vclu:safe", "on", "[]", false, ""); a.Reason != "pin_required" {
		t.Errorf("no PIN = %+v", a)
	}
	if a := c.run("vclu:safe", "on", "[]", false, "0420"); !a.OK {
		t.Errorf("right PIN = %+v", a)
	}
	if a := c.run("vclu:safe2", "on", "[]", false, "1234"); !a.OK {
		t.Errorf("a numeric :pin() = %+v", a)
	}
	for i := 0; i < 4; i++ {
		c.run("vclu:safe", "on", "[]", false, "1111")
	}
	if a := c.run("vclu:safe", "on", "[]", false, "0420"); !a.OK {
		t.Errorf("right PIN after 4 wrong = %+v", a)
	}
	for i := 0; i < 5; i++ {
		if a := c.run("vclu:safe", "on", "[]", false, "1111"); a.Reason != "pin_wrong" {
			t.Errorf("wrong PIN %d = %+v", i+1, a)
		}
	}
	if a := c.run("vclu:safe", "on", "[]", false, "0420"); a.Reason != "rate_limited" {
		t.Errorf("after 5 wrong PINs = %+v", a)
	}
	if a := c.run("vclu:safe2", "on", "[]", false, "1234"); !a.OK {
		t.Errorf("another device is not locked = %+v", a)
	}

	// a command the Lua queue held past its deadline is not run
	takeCalls(t, engine)
	if a := c.runBy("vclu:roleta", "open", "[]", false, "", float64(time.Now().Add(-time.Second).UnixMilli())); a.Reason != "expired" {
		t.Errorf("late = %+v", a)
	}
	if got := takeCalls(t, engine); got != "" {
		t.Errorf("a late command called %q", got)
	}
}
