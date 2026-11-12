package lua

import (
	"strings"
	"testing"
)

// syncCloud gives the cloud the Access page's levels, as Go's syncProfilesToLua does (mqtt and
// homekit hidden when hideLocal).
func syncCloud(t *testing.T, engine *LuaEngine, objects string, hideLocal bool) {
	t.Helper()
	assign := `{ cloud = { "custom-cloud" } }`
	if hideLocal {
		assign = `{ cloud = { "custom-cloud" }, mqtt = { "hide" }, homekit = { "hide" } }`
	}
	luaOK(t, engine, `LuaAPI.syncProfiles({ ["custom-cloud"] = { default = "hidden", objects = `+objects+` },
		hide = { default = "hidden" } }, `+assign+`)`)
}

func remoteNames(t *testing.T, engine *LuaEngine) string {
	t.Helper()
	return mustExec(t, engine, `(function() local s = {}
		for _, d in ipairs(ExposedObjects:remoteList()) do s[#s + 1] = d.path .. "=" .. d.access end
		return table.concat(s, " ") end)()`)
}

// The Access page opens what Lua's :remote() says nothing about: hidden until then, read-only
// seen but never commanded, full for every member whatever their roles. :remote(false) keeps a
// device home whatever the page says, and :remote() decides over it.
func TestCloudPanelAccess(t *testing.T) {
	engine, _, _ := newHistoryEngine(t)
	c := commander{t, engine}
	luaOK(t, engine, `
		for i, id in ipairs({ "ro", "full", "home", "lua" }) do
			local sw = VirtualSwitch:new(id)
			registerObject("virt." .. id, sw)
			_:sync("virt." .. id, 0)
			local h = expose(sw, "light")
			if id == "home" then h:remote(false) end
			if id == "lua" then h:remote():roles("biuro") end
		end`)

	if a := c.run("virt.full", "on", "[]", false, ""); a.Reason != "not_found" {
		t.Errorf("before the page opens it = %+v", a)
	}
	if got := remoteNames(t, engine); got != "virt.lua=full" {
		t.Errorf("remoteList before = %q", got)
	}

	syncCloud(t, engine, `{ ["virt.ro"] = "readonly", ["virt.full"] = "full", ["virt.home"] = "full", ["virt.lua"] = "hidden" }`, false)
	if got := remoteNames(t, engine); got != "virt.full=full virt.lua=full virt.ro=readonly" {
		t.Errorf("remoteList = %q", got)
	}
	if a := c.run("virt.full", "on", `["goscie"]`, false, ""); !a.OK {
		t.Errorf("full from the page = %+v", a)
	}
	if a := c.run("virt.ro", "on", "[]", false, ""); a.OK || a.Reason == "not_found" {
		t.Errorf("read-only from the page = %+v", a)
	}
	if a := c.run("virt.home", "on", "[]", false, ""); a.Reason != "not_found" {
		t.Errorf(":remote(false) opened by the page = %+v", a)
	}
	if a := c.run("virt.lua", "on", `["biuro"]`, false, ""); !a.OK {
		t.Errorf(":remote() hidden on the page = %+v", a)
	}
	if a := c.run("virt.lua", "on", "[]", false, ""); a.Reason != "forbidden" {
		t.Errorf(":roles() without the role = %+v", a)
	}
	states := mustExec(t, engine, `Cloud._states('["virt.ro","virt.home","virt.full"]', '[]')`)
	if !strings.Contains(states, "virt.ro") || !strings.Contains(states, "virt.full") || strings.Contains(states, "virt.home") {
		t.Errorf("states = %s", states)
	}
}

// A registry object hidden from MQTT and HomeKit has no handle; opening it for the cloud on the
// Access page exposes it at once, not at the next reload.
func TestCloudPanelExposesRegistryObject(t *testing.T) {
	engine, _, _ := newHistoryEngine(t)
	syncCloud(t, engine, `{}`, true)
	luaOK(t, engine, `
		registerObject("CLU1.DOU1", VirtualSwitch:new("dou1"))
		exposeRegistry()`)
	if got := remoteNames(t, engine); got != "" {
		t.Fatalf("remoteList before = %q", got)
	}
	syncCloud(t, engine, `{ ["CLU1.DOU1"] = "full" }`, true)
	if got := remoteNames(t, engine); got != "CLU1.DOU1=full" {
		t.Errorf("remoteList after opening it = %q", got)
	}
	if v := mustExec(t, engine, `tostring(ExposedObjects._objects["CLU1.DOU1"].options.mqtt)`); v != "false" {
		t.Errorf("exposed for the cloud, mqtt = %s", v)
	}
}

// A level the access file does not know (a typo, an empty default) keeps the cloud closed: only
// exactly "full" or "readonly" open a device.
func TestCloudPanelTypoStaysClosed(t *testing.T) {
	engine, _, _ := newHistoryEngine(t)
	c := commander{t, engine}
	luaOK(t, engine, `
		for i, id in ipairs({ "a", "b" }) do
			local sw = VirtualSwitch:new(id)
			registerObject("virt." .. id, sw)
			expose(sw, "light")
		end
		LuaAPI.syncProfiles({ typo = { default = "", objects = { ["virt.a"] = "Readonly" } } }, { cloud = { "typo" } })`)
	if got := remoteNames(t, engine); got != "" {
		t.Errorf("remoteList = %q", got)
	}
	for _, path := range []string{"virt.a", "virt.b"} {
		if a := c.run(path, "on", "[]", false, ""); a.Reason != "not_found" {
			t.Errorf("%s = %+v", path, a)
		}
	}
}

// A registry object exposed only because the cloud is open for it stays out of bots' and API
// clients' reach, goes when the cloud closes, and comes back as an ordinary handle once MQTT
// opens it.
func TestCloudOnlyHandle(t *testing.T) {
	engine, _, _ := newHistoryEngine(t)
	syncCloud(t, engine, `{ ["CLU1.DOU1"] = "full" }`, true)
	luaOK(t, engine, `
		registerObject("CLU1.DOU1", VirtualSwitch:new("dou1"))
		exposeRegistry()`)
	if got := remoteNames(t, engine); got != "CLU1.DOU1=full" {
		t.Fatalf("remoteList = %q", got)
	}
	for what, script := range map[string]string{
		"MCP.list_devices": `MCP.list_devices(nil)`,
		"MCP.get_device":   `MCP.get_device("CLU1.DOU1")`,
		"MCP.set_value":    `MCP.set_value("CLU1.DOU1", 1)`,
		"deviceList":       `JSON.encode(ExposedObjects:deviceList())`,
		"clientCommand":    `Cloud._clientCommand("CLU1.DOU1", "on", "full", false, "", 0)`,
	} {
		out := mustExec(t, engine, script)
		if strings.Contains(out, `"ok":true`) || (strings.Contains(out, "dou1") && !strings.Contains(out, "not found")) {
			t.Errorf("%s reaches a cloud-only device: %s", what, out)
		}
	}

	syncCloud(t, engine, `{}`, true)
	if got := remoteNames(t, engine); got != "" {
		t.Errorf("remoteList after closing = %q", got)
	}
	if v := mustExec(t, engine, `tostring(ExposedObjects._objects["CLU1.DOU1"] ~= nil)`); v != "false" {
		t.Errorf("the cloud-only handle stays after the cloud closed")
	}

	luaOK(t, engine, `LuaAPI.syncProfiles({ ["custom-cloud"] = { default = "hidden" } }, { cloud = { "custom-cloud" } })`)
	if v := mustExec(t, engine, `(function() local h = ExposedObjects._objects["CLU1.DOU1"]
		return h and (tostring(h._cloudOnly) .. " " .. tostring(h.options.mqtt ~= false)) or "none" end)()`); v != "nil true" {
		t.Errorf("opened for MQTT: handle = %s, want an ordinary one", v)
	}
	if !strings.Contains(mustExec(t, engine, `MCP.list_devices(nil)`), "CLU1.DOU1") {
		t.Error("bots do not see a device open for MQTT")
	}
}

// A registry object a module unexposes after load stays unexposed through access changes,
// until a module exposes it again.
func TestCloudAccessChangeKeepsUnexposed(t *testing.T) {
	engine, _, _ := newHistoryEngine(t)
	syncCloud(t, engine, `{}`, false)
	luaOK(t, engine, `
		_dou = VirtualSwitch:new("dou1")
		registerObject("CLU1.DOU1", _dou)
		exposeRegistry()
		ExposedObjects:get("CLU1.DOU1"):unexpose()`)
	syncCloud(t, engine, `{ ["CLU1.DOU1"] = "full" }`, false)
	if v := mustExec(t, engine, `tostring(ExposedObjects._objects["CLU1.DOU1"] ~= nil)`); v != "false" {
		t.Error("an access change brought back an object a module unexposed")
	}
	luaOK(t, engine, `expose(_dou, "light")`)
	if got := remoteNames(t, engine); got != "CLU1.DOU1=full" {
		t.Errorf("exposed again: remoteList = %q", got)
	}
}

// Hiding an object from MQTT on the Access page and opening it again works without a reload:
// an access change makes new handles only and does not set mqtt = false on existing ones.
func TestAccessChangeKeepsHandleOptions(t *testing.T) {
	engine, _, _ := newHistoryEngine(t)
	syncCloud(t, engine, `{}`, false)
	luaOK(t, engine, `
		registerObject("CLU1.DOU1", VirtualSwitch:new("dou1"))
		exposeRegistry()
		LuaAPI.syncProfiles({ hide = { default = "hidden" } }, { mqtt = { "hide" } })
		LuaAPI.syncProfiles({}, {})`)
	if v := mustExec(t, engine, `tostring(ExposedObjects._objects["CLU1.DOU1"].options.mqtt ~= false)`); v != "true" {
		t.Error("hidden from MQTT and opened again: the handle stays off MQTT")
	}
}
