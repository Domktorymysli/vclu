package lua

import (
	"strings"
	"testing"
)

// Edge cases of exposing again, found reviewing the move of obj:expose() onto ExposedObjects.

// An object whose old path now belongs to another object does not take over that object's
// handle: a registry object is exposed again at its registry path, another one is refused,
// and the other object keeps its name, in HA too.
func TestExposeStalePathLeavesOtherObject(t *testing.T) {
	engine := exposeAgainEngine(t)
	got := mustExec(t, engine, `(function()
		local a, b = _objs.virtual, VirtualSwitch:new("v2")
		local loose = VirtualSwitch:new("v3")
		expose(loose, "switch", { path = "x:loose", name = "L" })
		ExposedObjects:get("x:loose"):unexpose()
		local hc = expose(VirtualSwitch:new("v4"), "switch", { path = "x:loose", name = "C" })
		local okLoose = pcall(function() expose(loose, "switch", { name = "L2" }) end)
		local looseKept = tostring(okLoose) .. "," .. hc.options.name .. "," .. tostring(ExposedObjects:get("x:loose") == hc)
		expose(a, "switch", { path = "x:shared", name = "A" })
		ExposedObjects:get("x:shared"):unexpose()
		local hb = expose(b, "switch", { path = "x:shared", name = "B" })
		local ok1 = pcall(function() a:expose({ name = "A2" }) end)
		local ok2 = pcall(function() expose(a, "switch", { name = "A3" }) end)
		return table.concat({ tostring(ok1), tostring(ok2), hb.options.name,
			tostring(ExposedObjects:get("x:shared") == hb), tostring(a:getExposeHandle() and a:getExposeHandle():getPath()),
			a:getExposeHandle().options.name, looseKept }, "|")
	end)()`)
	if want := "true|true|B|true|virt.v1|A3|false,C,true"; got != want {
		t.Errorf("got %s, want %s", got, want)
	}
	if d := mustExec(t, engine, `_discovery("x:shared")`); strings.Contains(d, `"A2"`) || strings.Contains(d, `"A3"`) {
		t.Errorf("another object's name reached HA: %s", d)
	}
}

// exposeRegistry() leaves an object a module already exposed under its own path as the module
// set it: one exposure, the module's name and area.
func TestExposeRegistryKeepsModuleExposure(t *testing.T) {
	for _, kind := range exposeAgainKinds {
		engine := exposeAgainEngine(t)
		got := mustExec(t, engine, `(function()
			local o = _objs.`+kind+`
			local h = expose(o, _types.`+kind+`, { path = "m:own", name = "Z modułu", area = "Biuro" })
			exposeRegistry()
			exposeRegistry()
			return table.concat({ tostring(ExposedObjects:get(_paths.`+kind+`) == nil),
				tostring(ExposedObjects:get("m:own") == h), h.options.name, tostring(h.options.area),
				tostring(o:isExposed()) }, "|")
		end)()`)
		if got != "true|true|Z modułu|Biuro|true" {
			t.Errorf("%s: got %s", kind, got)
		}
	}
}

// obj:expose() with no options on an exposed object changes nothing: no rebuild of the
// dashboards' rooms (generation), no HA republish.
func TestObjExposeNoOptionsChangesNothing(t *testing.T) {
	for _, kind := range exposeAgainKinds {
		engine := exposeAgainEngine(t)
		got := mustExec(t, engine, `(function()
			local o = _objs.`+kind+`
			local h = o:expose({ name = "A" })
			local gen, pubs = ExposedObjects._generation, #_pub
			local h2 = o:expose()
			return tostring(h2 == h) .. "|" .. tostring(ExposedObjects._generation == gen) .. "|" .. tostring(#_pub == pubs)
		end)()`)
		if got != "true|true|true" {
			t.Errorf("%s: same handle|same generation|no publish = %s", kind, got)
		}
	}
}

// A filter set by the first expose stays when the object is exposed again without one, by
// either form; filter = false removes it.
func TestExposeAgainKeepsFilter(t *testing.T) {
	engine := exposeAgainEngine(t)
	got := mustExec(t, engine, `(function()
		local o = VirtualObject:new("t1")
		_registry["virt.t1"] = o
		local out = {}
		expose(o, "temperature", { name = "A", filter = { delta = 5 } })
		StateChange.filter(o, nil, 10) -- the first reading is reported: 10
		table.insert(out, tostring((StateChange.filter(o, 10, 11))))
		o:expose({ name = "B" })
		table.insert(out, tostring((StateChange.filter(o, 11, 12))))
		expose(o, "temperature", { area = "Salon" })
		table.insert(out, tostring((StateChange.filter(o, 12, 13))))
		table.insert(out, tostring((StateChange.filter(o, 13, 16))))
		o:expose({ filter = false })
		table.insert(out, tostring((StateChange.filter(o, 16, 17))))
		return table.concat(out, "|")
	end)()`)
	// 11, 12, 13 are within 5 of 10: not reported, also after exposing again; 16 is; no filter: all are
	if got != "false|false|false|true|true" {
		t.Errorf("got %s, want false|false|false|true|true", got)
	}
}

// A hidden expose returns a handle the fluent calls work on, and a module may expose the
// object later: it comes back at its registry path.
func TestHiddenHandleFluentAndExposeLater(t *testing.T) {
	for _, kind := range exposeAgainKinds {
		engine := exposeAgainEngine(t)
		got := mustExec(t, engine, `(function()
			local o = _objs.`+kind+`
			exposeRegistry()
			local ok = pcall(function()
				local h = o:expose({ hidden = true })
				h:readonly(); h:rename("x")
			end)
			local hidden = tostring(o:isExposed())
			local h = o:expose({ name = "Wraca" })
			return table.concat({ tostring(ok), hidden, h:getPath(), h.options.name, tostring(o:isExposed()) }, "|")
		end)()`)
		want := "true|false|" + map[string]string{"remote": "CLU1.ROL1", "virtual": "virt.v1"}[kind] + "|Wraca|true"
		if got != want {
			t.Errorf("%s: got %s, want %s", kind, got, want)
		}
	}
}

// An exposure made by a group is seen by the object, and obj:unexpose() takes it out.
func TestGroupExposureSeenByObject(t *testing.T) {
	engine := exposeAgainEngine(t)
	got := mustExec(t, engine, `(function()
		local o = _objs.remote
		RemoteGroup:new({ o }):expose({ area = "Salon" })
		local seen = o:isExposed() and o:getExposeHandle().options.area
		local out = tostring(seen) .. "|" .. tostring(o:unexpose()) .. "|" .. tostring(ExposedObjects:get("CLU1.ROL1") == nil)
		return out
	end)()`)
	if got != "Salon|true|true" {
		t.Errorf("got %s, want Salon|true|true", got)
	}
}

// A repeated expose that fails the option check changes nothing.
func TestExposeAgainBadOptionChangesNothing(t *testing.T) {
	for _, kind := range exposeAgainKinds {
		engine := exposeAgainEngine(t)
		got := mustExec(t, engine, `(function()
			local o = _objs.`+kind+`
			local h = o:expose({ name = "A" })
			local ok = pcall(function() o:expose({ name = "B", filter = { delta = -1 } }) end)
			return tostring(ok) .. "|" .. h.options.name
		end)()`)
		if got != "false|A" {
			t.Errorf("%s: got %s, want false|A", kind, got)
		}
	}
}

// Access hidden in the panel on the registry path takes off that integration an exposure a
// module made under its own path, keeping the module's name (as the registry pass did before).
func TestExposeRegistryAppliesPanelHiddenToModuleExposure(t *testing.T) {
	for _, c := range []struct{ integration, want string }{
		{"mqtt", "false|true|Roleta|Salon"},
		{"homekit", "true|false|Roleta|Salon"},
		{"both", "false|false|Roleta|Salon"},
	} {
		engine := exposeAgainEngine(t)
		got := mustExec(t, engine, `(function()
			local o = _objs.remote
			local h = expose(o, "cover", { path = "vclu:salon_roleta", name = "Roleta", area = "Salon" })
			for _, i in ipairs(("`+c.integration+`" == "both") and { "mqtt", "homekit" } or { "`+c.integration+`" }) do
				AccessControl.set(i, "CLU1.ROL1", "hidden")
			end
			exposeRegistry()
			return table.concat({ tostring(h.options.mqtt), tostring(h.options.homekit), h.options.name,
				tostring(h.options.area) }, "|")
		end)()`)
		if got != c.want {
			t.Errorf("%s hidden: mqtt|homekit|name|area = %s, want %s", c.integration, got, c.want)
		}
	}
}
