package lua

import (
	"strings"
	"testing"
)

// Exposing an object again, by expose(obj, ...) or obj:expose(...), on a remote (CLU) and a
// virtual object: one handle, options merged, unexpose and isExposed on the object, a later
// expose after unexpose, and Home Assistant told about a new name.

const exposeAgainSetup = `
	_pub = {}
	HAStatePublisher.setup({
		publish = function(self, topic, payload) table.insert(_pub, { topic = topic, payload = payload }); return true end,
		subscribe = function() end, unsubscribe = function() end,
	})
	local clu = { executeNoReply = function() end }
	_objs = {
		remote = RemoteCover:new(clu, "ROL1", "salon"),
		virtual = VirtualSwitch:new("v1"),
	}
	_registry["CLU1.ROL1"] = _objs.remote
	_registry["virt.v1"] = _objs.virtual
	_paths = { remote = "CLU1.ROL1", virtual = "virt.v1" }
	_types = { remote = "cover", virtual = "switch" }

	-- the last discovery config published for a path, as text
	function _discovery(path)
		local id = ("vclu_" .. path):lower():gsub("[^%w]", "_")
		local v
		for _, p in ipairs(_pub) do
			if p.topic:match("^homeassistant/[%w_]+/" .. id .. "/config$") then v = p.payload end
		end
		return tostring(v)
	end
`

func exposeAgainEngine(t *testing.T) *LuaEngine {
	t.Helper()
	engine := NewLuaEngine()
	t.Cleanup(engine.Close)
	if _, err := engine.Execute(exposeAgainSetup); err != nil {
		t.Fatalf("setup failed: %v", err)
	}
	return engine
}

var exposeAgainKinds = []string{"remote", "virtual"}

// obj:expose(opts) again: the same handle, the new options merged into the old ones.
func TestObjExposeAgainMergesOptions(t *testing.T) {
	for _, kind := range exposeAgainKinds {
		engine := exposeAgainEngine(t)
		got := mustExec(t, engine, `(function()
			local o = _objs.`+kind+`
			local h1 = o:expose({ name = "First", area = "Salon" })
			local h2 = o:expose({ name = "Second" })
			local h3 = o:expose()
			return table.concat({ tostring(h1 == h2 and h2 == h3), h1.options.name, tostring(h1.options.area),
				h1:getPath(), tostring(ExposedObjects:count()), tostring(o:isExposed()) }, "|")
		end)()`)
		want := "true|Second|Salon|" + map[string]string{"remote": "CLU1.ROL1", "virtual": "virt.v1"}[kind] + "|1|true"
		if got != want {
			t.Errorf("%s: got %s, want %s", kind, got, want)
		}
	}
}

// A name given by obj:expose again reaches Home Assistant's discovery.
func TestObjExposeAgainRenamesInHA(t *testing.T) {
	for _, kind := range exposeAgainKinds {
		engine := exposeAgainEngine(t)
		got := mustExec(t, engine, `(function()
			local o = _objs.`+kind+`
			o:expose({ name = "Stara" })
			o:expose({ name = "Nowa" })
			return _discovery(_paths.`+kind+`)
		end)()`)
		if !strings.Contains(got, `"Nowa"`) {
			t.Errorf("%s: discovery after a rename: %s", kind, got)
		}
	}
}

// expose(obj, ...) and obj:expose(...) mixed give one handle.
func TestExposeFormsShareHandle(t *testing.T) {
	for _, kind := range exposeAgainKinds {
		engine := exposeAgainEngine(t)
		got := mustExec(t, engine, `(function()
			local o = _objs.`+kind+`
			local h1 = o:expose({ name = "A" })
			local h2 = expose(o, _types.`+kind+`, { area = "Kuchnia" })
			return tostring(h1 == h2) .. "|" .. h1.options.name .. "|" .. tostring(h1.options.area) ..
				"|" .. ExposedObjects:count()
		end)()`)
		if got != "true|A|Kuchnia|1" {
			t.Errorf("%s: got %s", kind, got)
		}
	}
}

// obj:unexpose() and handle:unexpose() take it out; obj:expose() puts it back at the same path.
func TestObjUnexposeAndExposeAgain(t *testing.T) {
	for _, kind := range exposeAgainKinds {
		engine := exposeAgainEngine(t)
		got := mustExec(t, engine, `(function()
			local o = _objs.`+kind+`
			local out = {}
			local h1 = o:expose({ name = "A" })
			table.insert(out, tostring(o:unexpose()))
			table.insert(out, tostring(o:unexpose()))
			table.insert(out, tostring(o:isExposed()))
			table.insert(out, tostring(ExposedObjects:get(_paths.`+kind+`) ~= nil))
			local h2 = o:expose({ name = "B" })
			table.insert(out, tostring(h2 ~= h1 and h2:isExposed()))
			table.insert(out, h2:getPath())
			h2:unexpose()
			table.insert(out, tostring(o:isExposed()))
			local h3 = o:expose()
			table.insert(out, tostring(h3:isExposed() and o:isExposed()))
			return table.concat(out, "|")
		end)()`)
		want := "true|false|false|false|true|" + map[string]string{"remote": "CLU1.ROL1", "virtual": "virt.v1"}[kind] + "|false|true"
		if got != want {
			t.Errorf("%s: got %s, want %s", kind, got, want)
		}
	}
}

// exposeRegistry() exposes registry objects; obj:expose(opts) afterwards updates that exposure.
func TestObjExposeAfterRegistry(t *testing.T) {
	for _, kind := range exposeAgainKinds {
		engine := exposeAgainEngine(t)
		got := mustExec(t, engine, `(function()
			local o = _objs.`+kind+`
			exposeRegistry()
			local before = ExposedObjects:get(_paths.`+kind+`)
			local h = o:expose({ name = "Z modułu", area = "Biuro" })
			return tostring(before ~= nil and h == before) .. "|" .. h.options.name .. "|" .. tostring(h.options.area) ..
				"|" .. tostring(o:isExposed())
		end)()`)
		if got != "true|Z modułu|Biuro|true" {
			t.Errorf("%s: got %s", kind, got)
		}
	}
}

// A name given by expose(obj, ...) again reaches Home Assistant too, as with obj:expose.
func TestExposeAgainRenamesInHA(t *testing.T) {
	for _, kind := range exposeAgainKinds {
		engine := exposeAgainEngine(t)
		got := mustExec(t, engine, `(function()
			local o = _objs.`+kind+`
			expose(o, _types.`+kind+`, { name = "Stara", path = _paths.`+kind+` })
			expose(o, _types.`+kind+`, { name = "Nowa" })
			return _discovery(_paths.`+kind+`)
		end)()`)
		if !strings.Contains(got, `"Nowa"`) {
			t.Errorf("%s: discovery after a rename: %s", kind, got)
		}
	}
}

// The object knows it is exposed whichever form exposed it, and obj:unexpose() takes it out.
func TestObjKnowsExposureFromAnyForm(t *testing.T) {
	for _, kind := range exposeAgainKinds {
		engine := exposeAgainEngine(t)
		got := mustExec(t, engine, `(function()
			local o = _objs.`+kind+`
			local out = {}
			expose(o, _types.`+kind+`, { name = "A" })
			table.insert(out, tostring(o:isExposed()))
			table.insert(out, tostring(o:unexpose()))
			table.insert(out, tostring(ExposedObjects:count()))
			exposeRegistry()
			table.insert(out, tostring(o:isExposed()))
			table.insert(out, tostring(o:unexpose()))
			table.insert(out, tostring(ExposedObjects:get(_paths.`+kind+`) ~= nil))
			return table.concat(out, "|")
		end)()`)
		if got != "true|true|0|true|true|false" {
			t.Errorf("%s: got %s, want true|true|0|true|true|false", kind, got)
		}
	}
}
