package lua

import (
	"testing"
)

// Remote access (#39): what the remote side sees and does is decided here, where the
// command executes. The remote side is not trusted: a command for an object it never
// received is checked like any other.

const remoteAccessSetup = `
	_calls = {}
	_events = {}
	local function spy(obj, name)
		local orig = obj[name]
		obj[name] = function(self, ...)
			table.insert(_calls, name .. "(" .. table.concat({ ... }, ",") .. ")")
			return orig(self, ...)
		end
	end

	_lamp = VirtualSwitch:new("lamp")
	_dim = VirtualDimmer:new("dim")
	_cover = VirtualCover:new("cover")
	_boiler = VirtualSwitch:new("boiler")
	_temp = VirtualSwitch:new("temp")
	_locked = VirtualSwitch:new("locked")
	_unexposed = VirtualSwitch:new("unexposed")
	spy(_lamp, "switchOn"); spy(_lamp, "switchOff")
	spy(_dim, "setBrightness"); spy(_dim, "switchOn")
	spy(_cover, "setPosition"); spy(_cover, "open"); spy(_cover, "close"); spy(_cover, "stop")
	spy(_boiler, "switchOn"); spy(_temp, "switchOn"); spy(_locked, "switchOn")
	registerObject("v.unexposed", _unexposed)

	expose(_lamp, "switch", { path = "v.lamp", name = "Lamp", area = "Salon" })
	expose(_dim, "dimmer", { path = "v.dim", name = "Dimmer" })
	expose(_cover, "cover", { path = "v.cover", name = "Cover" })
	expose(_boiler, "switch", { path = "v.boiler", name = "Boiler" })
	expose(_temp, "switch", { path = "v.temp", name = "Temp" })
	expose(_locked, "switch", { path = "v.locked", name = "Locked", readonly = true })

	for _, p in ipairs({ "v.lamp", "v.dim", "v.cover", "v.boiler", "v.temp", "v.locked" }) do
		StateBus:watch(p, function(e) table.insert(_events, e) end)
	end

	-- as Go syncs them: remote gets the built-in hidden profile, the rest full-access
	function _loadDefault()
		AccessControl.loadProfiles({
			["full-access"] = { ["default"] = "full" },
			["no-access"] = { ["default"] = "hidden" },
			["outside"] = { ["default"] = "hidden", objects = {
				["v.lamp"] = "full", ["v.dim"] = "full", ["v.cover"] = "full",
				["v.temp"] = "readonly", ["v.locked"] = "full", ["v.unexposed"] = "full",
			} },
		}, { mqtt = { "full-access" }, homekit = { "full-access" }, mcp = { "full-access" }, remote = { "no-access" } })
	end
	function _assignRemote(...)
		local a = AccessControl._assignments
		a.remote = { ... }
		AccessControl.loadProfiles(AccessControl._profiles, a)
	end

	-- path=level of the visible objects, in order
	function _visible()
		local out = {}
		for _, o in ipairs(RemoteAccess.objects()) do table.insert(out, o.path .. "=" .. o.access) end
		return table.concat(out, ",")
	end
	function _take()
		local c, e = _calls, _events
		_calls, _events = {}, {}
		return c, e
	end
`

func newRemoteAccessEngine(t *testing.T) *LuaEngine {
	t.Helper()
	engine := NewLuaEngine()
	t.Cleanup(engine.Close)
	if _, err := engine.Execute(remoteAccessSetup); err != nil {
		t.Fatalf("setup: %v", err)
	}
	return engine
}

// Before any profile reaches Lua, and with the default profile, remote sees nothing.
func TestRemoteAccess_ClosedByDefault(t *testing.T) {
	e := newRemoteAccessEngine(t)
	luaOK(t, e, `
		assert(_visible() == "", "before sync: " .. _visible())
		local ok = RemoteAccess.command("v.lamp", "on", nil, "user:anna")
		assert(not ok and _lamp:getValue() == 0, "command before sync")

		_loadDefault()
		assert(_visible() == "", "default profile: " .. _visible())
		ok = RemoteAccess.command("v.lamp", "on", nil, "user:anna")
		assert(not ok and _lamp:getValue() == 0, "command with the default profile")
		local c = _take()
		assert(#c == 0, "a method was called: " .. table.concat(c, ";"))

		-- the other integrations keep full access
		assert(AccessControl.canControl("mqtt", "v.lamp"), "mqtt")
		assert(AccessControl.canControl("homekit", "v.lamp"), "homekit")
		assert(AccessControl.canControl("mcp", "v.lamp"), "mcp")
	`)
}

func TestRemoteAccess_ListsOnlyVisibleExposedObjects(t *testing.T) {
	e := newRemoteAccessEngine(t)
	luaOK(t, e, `
		_loadDefault()
		_assignRemote("outside")
		local got = _visible()
		-- boiler is hidden, v.unexposed is not exposed, locked is readonly by its expose option
		assert(got == "v.cover=full,v.dim=full,v.lamp=full,v.locked=readonly,v.temp=readonly", got)

		local lamp
		for _, o in ipairs(RemoteAccess.objects()) do if o.path == "v.lamp" then lamp = o end end
		assert(lamp.name == "Lamp" and lamp.type == "switch" and lamp.area == "Salon" and lamp.value == 0,
			"lamp entry: " .. tostring(lamp.name) .. " " .. tostring(lamp.type) .. " " .. tostring(lamp.value))
	`)
}

func TestRemoteAccess_RefusesHiddenAndReadonly(t *testing.T) {
	e := newRemoteAccessEngine(t)
	luaOK(t, e, `
		_loadDefault()
		_assignRemote("outside")
		_take()
		for _, c in ipairs({
			{ "v.boiler", "on" },     -- hidden for remote
			{ "v.unexposed", "on" },  -- in the registry, not exposed
			{ "v.temp", "on" },       -- readonly for remote
			{ "v.locked", "on" },     -- full for remote, readonly by expose()
			{ "v.nothing", "on" },
		}) do
			local ok, err = RemoteAccess.command(c[1], c[2], nil, "user:anna")
			assert(ok == false and type(err) == "string", c[1] .. " not refused")
		end
		local calls, events = _take()
		assert(#calls == 0, "methods called: " .. table.concat(calls, ";"))
		assert(#events == 0, "state changed: " .. #events)
		assert(_boiler:getValue() == 0 and _temp:getValue() == 0 and _locked:getValue() == 0
			and _unexposed:getValue() == 0, "a value changed")

		-- a hidden object is refused like one that does not exist
		local _, e1 = RemoteAccess.command("v.boiler", "on", nil, "user:anna")
		local _, e2 = RemoteAccess.command("v.nothing", "on", nil, "user:anna")
		assert(e1:gsub("v.boiler", "X") == e2:gsub("v.nothing", "X"), e1 .. " vs " .. e2)
	`)
}

func TestRemoteAccess_FullCommandCallsOwnMethodWithOriginAndActor(t *testing.T) {
	e := newRemoteAccessEngine(t)
	luaOK(t, e, `
		_loadDefault()
		_assignRemote("outside")
		_take()
		local cases = {
			{ "v.lamp", "on", nil, "switchOn()" },
			{ "v.lamp", "off", nil, "switchOff()" },
			{ "v.dim", "brightness", 40, "setBrightness(40)" },
			{ "v.cover", "position", 30, "setPosition(30)" },
			{ "v.cover", "open", nil, "open()" },
			{ "v.cover", "close", nil, "close()" },
			{ "v.cover", "stop", nil, "stop()" },
		}
		for _, c in ipairs(cases) do
			local ok, err = RemoteAccess.command(c[1], c[2], c[3], "user:anna")
			assert(ok == true, c[1] .. " " .. c[2] .. ": " .. tostring(err))
			local calls = _take()
			assert(calls[1] == c[4], c[1] .. " " .. c[2] .. ": called " .. table.concat(calls, ";"))
		end

		RemoteAccess.command("v.lamp", "on", nil, "user:anna")
		local _, events = _take()
		assert(#events == 1, "events: " .. #events)
		local ev = events[1]
		assert(ev.origin == "remote" and ev.actor == "user:anna", "context: " .. tostring(ev.origin) .. " " .. tostring(ev.actor))
		assert(_lamp:getValue() == 1, "lamp not on")

		-- the context ends with the command
		_lamp:switchOff()
		_, events = _take()
		assert(events[1].origin == "lua", "context leaked: " .. tostring(events[1].origin))
	`)
}

func TestRemoteAccess_RefusesBadCommands(t *testing.T) {
	e := newRemoteAccessEngine(t)
	luaOK(t, e, `
		_loadDefault()
		_assignRemote("outside")
		_take()
		for _, c in ipairs({
			{ "v.lamp", "on", nil, nil },          -- no actor
			{ "v.lamp", "on", nil, "agent:x" },    -- not a person
			{ "v.lamp", "on", nil, "user:" },
			{ "v.lamp", "explode", nil, "user:anna" },
			{ "v.lamp", "position", 10, "user:anna" }, -- not a switch command
			{ "v.dim", "brightness", "abc", "user:anna" },
			{ "v.cover", "on", nil, "user:anna" },
		}) do
			local ok, err = RemoteAccess.command(c[1], c[2], c[3], c[4])
			assert(ok == false and type(err) == "string", c[1] .. " " .. c[2] .. " " .. tostring(c[4]) .. " not refused")
		end
		local calls, events = _take()
		assert(#calls == 0, "methods called: " .. table.concat(calls, ";"))
		assert(#events == 0, "state changed")
	`)
}

// A profile change reaches remote access at once, as for the other integrations.
func TestRemoteAccess_ProfileChangeTakesEffectLive(t *testing.T) {
	e := newRemoteAccessEngine(t)
	luaOK(t, e, `
		_loadDefault()
		_assignRemote("outside")
		assert(RemoteAccess.command("v.lamp", "on", nil, "user:anna"), "full lamp refused")

		local p = AccessControl._profiles
		p.outside.objects["v.lamp"] = "readonly"
		p.outside.objects["v.boiler"] = "full"
		AccessControl.loadProfiles(p, AccessControl._assignments)

		assert(not RemoteAccess.command("v.lamp", "off", nil, "user:anna"), "readonly lamp controlled")
		assert(_lamp:getValue() == 1, "lamp changed")
		assert(RemoteAccess.command("v.boiler", "on", nil, "user:anna"), "boiler opened but refused")
		assert(_visible():find("v.boiler=full", 1, true), _visible())

		_assignRemote("no-access")
		assert(_visible() == "", "after closing: " .. _visible())
		assert(not RemoteAccess.command("v.boiler", "off", nil, "user:anna"), "closed boiler controlled")
	`)
}
