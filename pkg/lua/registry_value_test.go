package lua

import (
	"strings"
	"testing"
)

// Contract of _:value(pathOrName): the object's getValue(), or nil when there is nothing to read.
// Modules (status messages, panels, bots) call it with fixed paths that may be gone after an
// OM change, so a missing object must give nil, never an error.

func newRegistryValueEngine(t *testing.T) *LuaEngine {
	t.Helper()
	engine := NewLuaEngine()
	t.Cleanup(engine.Close)
	setup := `
		local clu = { execute = function() end }
		registerObject("CLU123.DOU0001", RemoteSwitch:new(clu, "DOU0001", "lampa"))
		registerObject("CLU123.DOU0002", RemoteSwitch:new(clu, "DOU0002", "bez_odczytu"))
		registerObject("CLU123.DIN0003", RemoteSwitch:new(clu, "DIN0003", "list"))
		_:sync("CLU123.DOU0001", 1)
	`
	if _, err := engine.Execute(setup); err != nil {
		t.Fatalf("setup: %v", err)
	}
	return engine
}

func registryValue(t *testing.T, engine *LuaEngine, script string) string {
	t.Helper()
	got, err := engine.Execute(script)
	if err != nil {
		t.Fatalf("%s: %v", script, err)
	}
	return got
}

func TestRegistryValueReturnsObjectValue(t *testing.T) {
	engine := newRegistryValueEngine(t)

	if got := registryValue(t, engine, `return tostring(_:value("CLU123.DOU0001"))`); got != "1" {
		t.Errorf("_:value(path) = %q, want 1", got)
	}
	if got := registryValue(t, engine, `return tostring(_:value("lampa"))`); got != "1" {
		t.Errorf("_:value(name) = %q, want 1", got)
	}
	// Same value as the long form, for every registered object
	got := registryValue(t, engine, `
		for path, obj in pairs(_registry) do
			if type(path) == "string" and _:value(path) ~= obj:getValue() then return path end
		end
		return "same"`)
	if got != "same" {
		t.Errorf("_:value(%s) differs from _:get(path):getValue()", got)
	}
}

func TestRegistryValueKeepsZero(t *testing.T) {
	engine := newRegistryValueEngine(t)
	// 0 is a reading, not a missing value: "off", "closed", "disarmed"
	registryValue(t, engine, `_:sync("CLU123.DOU0001", 0)`)
	if got := registryValue(t, engine, `return type(_:value("CLU123.DOU0001")) .. " " .. tostring(_:value("CLU123.DOU0001"))`); got != "number 0" {
		t.Errorf("_:value after sync 0 = %q, want number 0", got)
	}
}

func TestRegistryValueFollowsChanges(t *testing.T) {
	engine := newRegistryValueEngine(t)
	// Not cached by _:value itself: every call reads the object
	got := registryValue(t, engine, `
		local seen = {}
		for i, v in ipairs({ 0, 1, 0 }) do
			_:sync("lampa", v)
			table.insert(seen, tostring(_:value("lampa")))
		end
		return table.concat(seen, ",")`)
	if got != "0,1,0" {
		t.Errorf("_:value after each sync = %q, want 0,1,0", got)
	}
}

func TestRegistryValueNilWithoutReading(t *testing.T) {
	engine := newRegistryValueEngine(t)
	if got := registryValue(t, engine, `return type(_:value("CLU123.DOU0002"))`); got != "nil" {
		t.Errorf("_:value of an object without a reading = %q, want nil", got)
	}
}

func TestRegistryValueNilForUnknownPath(t *testing.T) {
	engine := newRegistryValueEngine(t)
	for _, path := range []string{"CLU123.DOU9999", "CLU999.DOU0001", "nie_ma_takiej_nazwy", "", "."} {
		got, err := engine.Execute(`return type(_:value("` + path + `"))`)
		if err != nil {
			t.Errorf("_:value(%q) raised an error: %v", path, err)
			continue
		}
		if got != "nil" {
			t.Errorf("_:value(%q) = %q, want nil", path, got)
		}
	}
}

func TestRegistryValueNilAfterObjectRemoved(t *testing.T) {
	engine := newRegistryValueEngine(t)
	// An OM file deleted or re-imported: modules keep their paths, the objects go
	registryValue(t, engine, `removeByPrefix("CLU123")`)
	for _, path := range []string{"CLU123.DOU0001", "lampa"} {
		if got := registryValue(t, engine, `return type(_:value("`+path+`"))`); got != "nil" {
			t.Errorf("_:value(%q) after removeByPrefix = %q, want nil", path, got)
		}
	}
}

func TestRegistryValueNilForObjectWithoutGetValue(t *testing.T) {
	engine := newRegistryValueEngine(t)
	registryValue(t, engine, `
		registerObject("scene:film", { execute = function() end })
		registerObject("custom:broken", { getValue = 5 })`)
	for _, path := range []string{"scene:film", "custom:broken"} {
		if got := registryValue(t, engine, `return type(_:value("`+path+`"))`); got != "nil" {
			t.Errorf("_:value(%q) = %q, want nil", path, got)
		}
	}
}

func TestRegistryValueDoesNotReturnRegistryMethods(t *testing.T) {
	engine := newRegistryValueEngine(t)
	// Registry methods live in the metatable; a path with a method's name is not an object.
	// _:get("list") returns the method, _:value must not call into it.
	for _, name := range []string{"get", "value", "sync", "toJSON", "info"} {
		got, err := engine.Execute(`return type(_:value("` + name + `"))`)
		if err != nil {
			t.Errorf("_:value(%q) raised an error: %v", name, err)
			continue
		}
		if got != "nil" {
			t.Errorf("_:value(%q) = %q, want nil", name, got)
		}
	}
	// An object whose name is a method's name resolves through the name index
	registryValue(t, engine, `_:sync("CLU123.DIN0003", 1)`)
	if got := registryValue(t, engine, `return tostring(_:value("list"))`); got != "1" {
		t.Errorf(`_:value("list") for an object named list = %q, want 1`, got)
	}
}

func TestRegistryValueRejectsNonString(t *testing.T) {
	engine := newRegistryValueEngine(t)
	// A wrong argument is a bug in the module: say so instead of returning nil
	for _, arg := range []string{"nil", "42", "{}", "true"} {
		got := registryValue(t, engine, `
			local ok, err = pcall(function() return _:value(`+arg+`) end)
			if ok then return "no error" end
			return tostring(err)`)
		// Which line the error names is error()'s level handling, not this function's (#77)
		if !strings.Contains(got, "_:value: path must be a string") {
			t.Errorf("_:value(%s) error = %q, want the path message", arg, got)
		}
	}
}

func TestRegistryValuePropagatesGetValueError(t *testing.T) {
	engine := newRegistryValueEngine(t)
	// A failing getValue is a real fault of the object: it is not hidden as "no reading"
	registryValue(t, engine, `registerObject("custom:faulty", { getValue = function() error("sensor fault") end })`)
	got := registryValue(t, engine, `
		local ok, err = pcall(function() return _:value("custom:faulty") end)
		if ok then return "no error" end
		return tostring(err)`)
	if !strings.Contains(got, "sensor fault") {
		t.Errorf("_:value of a failing getValue = %q, want the error", got)
	}
}

func TestRegistryValueIsNotAnObject(t *testing.T) {
	engine := newRegistryValueEngine(t)
	// The method must not show up as a registered object anywhere the registry is listed
	if got := registryValue(t, engine, `return #_:list()`); got != "3" {
		t.Errorf("#_:list() = %q, want 3", got)
	}
	if got := registryValue(t, engine, `return tostring(rawget(_registry, "value"))`); got != "nil" {
		t.Errorf("rawget(_registry, \"value\") = %q, want nil", got)
	}
	if got := registryValue(t, engine, `return tostring(#_:find("value"))`); got != "0" {
		t.Errorf("#_:find(\"value\") = %q, want 0", got)
	}
	if got := registryValue(t, engine, `return _:toJSON()`); strings.Contains(got, `"value"`) && strings.Contains(got, `"path":"value"`) {
		t.Errorf("_:toJSON() lists value as an object: %s", got)
	}
}

func TestRegistryValueKeepsFalse(t *testing.T) {
	engine := newRegistryValueEngine(t)
	// false is a reading too; only a missing object or reading gives nil
	registryValue(t, engine, `registerObject("custom:flag", { getValue = function() return false end })`)
	if got := registryValue(t, engine, `return type(_:value("custom:flag")) .. " " .. tostring(_:value("custom:flag"))`); got != "boolean false" {
		t.Errorf("_:value of a false reading = %q, want boolean false", got)
	}
}

func TestRegistryValueVirtualAndExposedObjects(t *testing.T) {
	engine := newRegistryValueEngine(t)
	// A virtual object by its name, and an object registered only by expose() (no name index entry)
	registryValue(t, engine, `
		registerObject("virtual.licznik", VirtualNumber:new("licznik", { initial = 123.4 }))
		local pompa = VirtualSwitch:new("pompa")
		pompa:switchOn()
		expose(pompa, "switch", { name = "Pompa", path = "dom.pompa" })`)
	for path, want := range map[string]string{"virtual.licznik": "123.4", "licznik": "123.4", "dom.pompa": "1"} {
		if got := registryValue(t, engine, `return tostring(_:value("`+path+`"))`); got != want {
			t.Errorf("_:value(%q) = %q, want %s", path, got, want)
		}
	}
}

func TestRegistryValueSatelObjects(t *testing.T) {
	engine := newSatelTestEngine(t)
	// Integration paths have several dots (satel.partition.1.alarm) and are taken as paths
	registryValue(t, engine, `
		table.insert(__satel_events,
			'{"type":"zone","number":1,"zone":{"number":1,"violated":true,"tamper":false,"alarm":false,"bypass":false}}')
		Satel.pollEvents()`)
	if got := registryValue(t, engine, `return tostring(_:value("satel.zone.1"))`); got != "1" {
		t.Errorf(`_:value("satel.zone.1") = %q, want 1`, got)
	}
	got := registryValue(t, engine, `
		for path, obj in pairs(_registry) do
			if type(path) == "string" and path:find("^satel%.") and _:value(path) ~= _:get(path):getValue() then
				return path
			end
		end
		return "same"`)
	if got != "same" {
		t.Errorf("_:value(%s) differs from _:get(path):getValue()", got)
	}
}

func TestRegistryValueInPluginSandbox(t *testing.T) {
	engine := newRegistryValueEngine(t)
	// A plugin's _ is a read-only proxy; _:value must work there as it does in modules
	if _, err := engine.Execute(`PluginSandbox:create("@test/reader")`); err != nil {
		t.Fatalf("sandbox create: %v", err)
	}
	cases := map[string]string{
		`return tostring(_:value("CLU123.DOU0001"))`: "1",
		`return tostring(_:value("lampa"))`:          "1",
		`return type(_:value("CLU123.DOU0002"))`:     "nil",
		`return type(_:value("CLU123.DOU9999"))`:     "nil",
		`return type(_:value("list"))`:               "number", // the object named list, not the method
		`return type(_:get("info"))`:                 "nil",    // a method's name is not an object in a plugin either
	}
	registryValue(t, engine, `_:sync("CLU123.DIN0003", 0)`)
	for script, want := range cases {
		got := registryValue(t, engine, `
			local r, e = PluginSandbox:execute("@test/reader", [[`+script+`]])
			if e then return "error: " .. tostring(e) end
			return tostring(r)`)
		if got != want {
			t.Errorf("in a plugin, %s = %q, want %q", script, got, want)
		}
	}
	got := registryValue(t, engine, `
		local r, e = PluginSandbox:execute("@test/reader", [[return _:value(42)]])
		return tostring(e)`)
	if !strings.Contains(got, "_:value: path must be a string") {
		t.Errorf("in a plugin, _:value(42) error = %q, want the path message", got)
	}
}
