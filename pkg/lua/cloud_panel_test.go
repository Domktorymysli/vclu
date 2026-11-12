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
