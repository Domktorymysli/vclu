package lua

import (
	"encoding/json"
	"strings"
	"sync/atomic"
	"testing"
)

// Contracts of :remote(), :roles() and :pin() on an exposed handle: what the vclu cloud app may
// see and command. The cloud builds its device list from ExposedObjects:remoteList().

// remoteDevices defines mk(type, opts): a plain device exposed with a fresh id, so each script
// runs on its own handle.
const remoteDevices = `
	_n = 0
	function dev()
		return { _lastState = 0,
			get = function(s) return s._lastState end,
			set = function(s, i, v) s._lastState = v end,
			onChange = function() return function() end end }
	end
	function mk(t, opts)
		_n = _n + 1
		opts = opts or {}
		opts.id = opts.id or ("d" .. _n)
		return expose(dev(), t or "switch", opts)
	end
`

// remoteDevice is an entry of remoteList(), as the cloud client decodes it (proto.Object).
type remoteDevice struct {
	Path   string   `json:"path"`
	Type   string   `json:"type"`
	Name   string   `json:"name"`
	Area   string   `json:"area"`
	Floor  string   `json:"floor"`
	Access string   `json:"access"`
	Roles  []string `json:"roles"`
}

func remoteList(t *testing.T, engine *LuaEngine) (string, []remoteDevice) {
	t.Helper()
	out := mustExec(t, engine, `JSON.encode(ExposedObjects:remoteList())`)
	var list []remoteDevice
	if err := json.Unmarshal([]byte(out), &list); err != nil {
		t.Fatalf("remoteList %s: %v", out, err)
	}
	return out, list
}

// Contract: wrong arguments to :remote(), :roles() and :pin() raise at the caller's line, and
// remote, roles or pin as expose()/update() options are refused with a pointer to the method.
func TestContract_Expose_RemoteValidation(t *testing.T) {
	engine := NewLuaEngine()
	defer engine.Close()
	mustExec(t, engine, remoteDevices)

	for script, want := range map[string]string{
		`mk():remote({ color = true })`:                    `expose:remote: unknown option "color" (confirm, pin, readonly)`,
		`mk():remote({ readonly = true, pin = true })`:     "expose:remote: readonly cannot be combined with confirm or pin",
		`mk():remote({ readonly = true, confirm = true })`: "expose:remote: readonly cannot be combined",
		`mk():remote({ confirm = "yes" })`:                 "expose:remote: confirm must be true or false",
		`mk():remote(5)`:                                   "expose:remote: expects nothing, true, false or a table",
		`mk():roles("domownicy")`:                          "expose:roles: call :remote() first",
		`mk():remote():roles()`:                            "expose:roles: expects at least one role",
		`mk():remote():roles("Gosia")`:                     `expose:roles: invalid role "Gosia"`,
		`mk():remote():roles("-x")`:                        `expose:roles: invalid role "-x"`,
		`mk():remote():roles(string.rep("a", 33))`:         "expose:roles: invalid role",
		`mk():remote():roles("ok", 7)`:                     `expose:roles: invalid role "7"`,
		`mk():pin("123")`:                                  "expose:pin: the PIN is 4 to 8 digits",
		`mk():pin("123456789")`:                            "expose:pin: the PIN is 4 to 8 digits",
		`mk():pin("12a4")`:                                 "expose:pin: the PIN is 4 to 8 digits",
		`mk():pin(12.5)`:                                   "expose:pin: the PIN is 4 to 8 digits",
		`mk():pin(-1234)`:                                  "expose:pin: the PIN is 4 to 8 digits",
		`mk():pin()`:                                       "expose:pin: the PIN is 4 to 8 digits",
		`mk("switch", { remote = true })`:                  "expose: use :remote() instead of the option remote",
		`mk("switch", { roles = { "a" } })`:                "expose: use :roles() instead of the option roles",
		`mk("switch", { pin = "1234" })`:                   "expose: use :pin() instead of the option pin",
		`mk():update({ remote = {} })`:                     "expose: use :remote() instead of the option remote",
		`mk():update({ pin = "1234" })`:                    "expose: use :pin() instead of the option pin",
	} {
		_, err := engine.Execute(script)
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: %v, want %q", script, err, want)
			continue
		}
		if strings.HasPrefix(script, "mk():") && !strings.Contains(err.Error(), "<string>:1: "+want) {
			t.Errorf("%s: the error does not blame the caller's line: %v", script, err)
		}
	}
	// a re-expose cannot slip them in either
	if _, err := engine.Execute(`local o = dev(); expose(o, "switch", { id = "again" }); expose(o, "switch", { pin = "1234" })`); err == nil || !strings.Contains(err.Error(), "use :pin()") {
		t.Errorf("re-expose with pin: %v", err)
	}
	// valid forms chain and return the handle
	mustExec(t, engine, `
		local h = mk()
		assert(h:pin(1234) == h and h:remote({ pin = true }) == h and h:roles("a", "b_2", "c-3") == h)
		assert(mk():remote(true):remote({ confirm = true, pin = false }):pin("00000420"))
		assert(mk():remote({ readonly = true, confirm = false }))
	`)
}

// Contract: remoteList() lists the devices exposed with :remote(), sorted by path, with the access
// the cloud enforces and the roles; never the PIN, nor a device that needs a PIN and has none.
func TestContract_Expose_RemoteList(t *testing.T) {
	engine := NewLuaEngine()
	defer engine.Close()
	mustExec(t, engine, remoteDevices)
	mustExec(t, engine, `
		mk("light", { id = "lamp", name = "Lampa", area = "Salon", floor = "Parter" }):remote()
		mk("switch", { id = "gate", name = "Brama" }):remote({ confirm = true }):roles("domownicy", "gosia")
		mk("switch", { id = "safe" }):pin(8642):remote({ pin = true })
		mk("switch", { id = "late" }):remote({ pin = true }):pin("0420")
		mk("switch", { id = "nopin" }):remote({ pin = true })
		mk("cover", { id = "ro" }):remote({ readonly = true })
		mk("switch", { id = "gro", readonly = true }):remote({ confirm = true })
		mk("switch", { id = "plain" })
		mk("switch", { id = "gone" }):remote():unexpose()
		mk("switch", { id = "taken" }):remote():roles("x"):remote(false)
		mk("switch", { id = "again" }):remote():roles("x"):remote(false):remote()
		local hidden = expose(dev(), "switch", { hidden = true })
		assert(hidden:remote():roles("x"):pin("1234") == hidden)
	`)
	out, list := remoteList(t, engine)
	var paths []string
	byPath := map[string]remoteDevice{}
	for _, o := range list {
		paths = append(paths, o.Path)
		byPath[o.Path] = o
	}
	if got := strings.Join(paths, " "); got != "vclu:again vclu:gate vclu:gro vclu:lamp vclu:late vclu:ro vclu:safe" {
		t.Fatalf("remoteList paths %q (%s)", got, out)
	}
	if l := byPath["vclu:lamp"]; l.Type != "light" || l.Name != "Lampa" || l.Area != "Salon" || l.Floor != "Parter" || l.Access != "full" || l.Roles != nil {
		t.Errorf("lamp = %+v", l)
	}
	if g := byPath["vclu:gate"]; g.Access != "confirm" || strings.Join(g.Roles, ",") != "domownicy,gosia" || g.Name != "Brama" {
		t.Errorf("gate = %+v", g)
	}
	for path, want := range map[string]string{"vclu:safe": "pin", "vclu:late": "pin", "vclu:ro": "readonly", "vclu:gro": "readonly", "vclu:again": "full"} {
		if a := byPath[path].Access; a != want {
			t.Errorf("%s access %q, want %q", path, a, want)
		}
	}
	if r := byPath["vclu:again"].Roles; r != nil {
		t.Errorf(":remote(false) kept the roles: %v", r)
	}
	if !strings.Contains(out, `"name":"vclu:safe"`) {
		t.Errorf("a device without a name is named by its path: %s", out)
	}

	// the PIN is in no list anyone reads
	for what, script := range map[string]string{
		"remoteList":      `JSON.encode(ExposedObjects:remoteList())`,
		"toJSON":          `ExposedObjects:toJSON()`,
		"accessList":      `ExposedObjects:accessList()`,
		"MCP.list":        `MCP.list_devices("{}")`,
		"MCP.get_device":  `MCP.get_device("vclu:safe")`,
		"MCP.get_device2": `MCP.get_device("vclu:late")`,
	} {
		got := mustExec(t, engine, script)
		if strings.Contains(got, "8642") || strings.Contains(got, "0420") {
			t.Errorf("%s shows a PIN: %s", what, got)
		}
	}
}

// Contract: every change of :remote(), :roles() or :pin(), and of what is exposed, tells the cloud
// client, which sends the device list again.
func TestContract_Expose_RemoteTellsTheCloud(t *testing.T) {
	engine := NewLuaEngine()
	defer engine.Close()
	var n atomic.Int32
	engine.SetCloud(CloudBridge{ObjectsChanged: func() { n.Add(1) }})
	mustExec(t, engine, remoteDevices)
	mustExec(t, engine, `h = mk()`)
	for _, script := range []string{
		`h:remote()`, `h:roles("a")`, `h:pin("1234")`, `h:remote(false)`, `h:update({ name = "x" })`,
		`h:rename("y")`, `mk()`, `h:unexpose()`,
	} {
		before := n.Load()
		mustExec(t, engine, script)
		if n.Load() == before {
			t.Errorf("%s did not tell the cloud client", script)
		}
	}
	if err := engine.Reinitialize("VCLU"); err != nil {
		t.Fatal(err)
	}
	before := n.Load()
	mustExec(t, engine, remoteDevices+`mk():remote()`)
	if n.Load() == before {
		t.Error("the bridge is gone after a reload")
	}
}

// Contract: :remote() on a device whose path was made from its name warns once: renaming it would
// make it another device in the cloud.
func TestContract_Expose_RemoteWarnsAboutNamePath(t *testing.T) {
	engine := NewLuaEngine()
	defer engine.Close()
	mustExec(t, engine, remoteDevices)
	out := mustExec(t, engine, `
		_warns = {}
		local orig = Logger.warn
		Logger.warn = function(self, msg, ...) _warns[#_warns + 1] = Logger:format(msg, ...) end
		local h = expose(dev(), "switch", { name = "Lampa" })
		h:remote(); h:remote({ confirm = true })
		expose(dev(), "switch", { id = "stable", name = "Druga" }):remote()
		Logger.warn = orig
		return table.concat(_warns, "\n")
	`)
	if strings.Count(out, "comes from the name") != 1 || !strings.Contains(out, "vclu:Lampa") {
		t.Errorf("warnings %q", out)
	}
}

// Contract: :remote(), :roles() and :pin() change nothing in Home Assistant: no object_updated, no
// discovery published again.
func TestHARemoteDoesNotRepublish(t *testing.T) {
	engine := NewLuaEngine()
	defer engine.Close()
	mustExec(t, engine, remoteDevices)
	out := mustExec(t, engine, `
		_pub = {}
		HAStatePublisher.setup({
			publish = function(self, topic, payload) table.insert(_pub, topic); return true end,
			subscribe = function() end, unsubscribe = function() end,
		})
		local h = mk("light", { id = "kanapa", name = "Kanapa" })
		_pub, _events = {}, 0
		for _, e in ipairs({ "object_updated", "object_exposed", "object_unexposed" }) do
			StateBus:getShared():on(e, function() _events = _events + 1 end)
		end
		h:remote({ confirm = true }):roles("domownicy"):pin("1234"):remote({ pin = true }):remote(false)
		local out = #_pub .. " " .. _events .. " " .. table.concat(_pub, ",")
		h:rename("Sofa") -- the control: a rename does republish
		return out .. "|" .. tostring(#_pub > 0)
	`)
	if out != "0 0 |true" {
		t.Errorf("published / events: %q", out)
	}
}
