package lua

import (
	"strings"
	"testing"
)

// An object a module exposes with hidden = true stays out of MQTT, Home Assistant and
// HomeKit whatever the order: exposeRegistry() (om.lua) before the module or after it,
// expose(obj, ...) or obj:expose(...). It takes no commands under its registry path.

const exposeHiddenSetup = `
	_pub = {}
	HAStatePublisher.setup({
		publish = function(self, topic, payload) table.insert(_pub, { topic = topic, payload = payload }); return true end,
		subscribe = function() end, unsubscribe = function() end,
	})
	_sent = {}
	local clu = { executeNoReply = function(s, i, cmd) table.insert(_sent, cmd) end }
	_shown = RemoteCover:new(clu, "ROL1", "salon")
	_hidden = RemoteCover:new(clu, "ROL2", "strych")
	_registry["CLU1.ROL1"] = _shown
	_registry["CLU1.ROL2"] = _hidden
	_virtual = VirtualSwitch:new("v1")
	_registry["virt.v1"] = _virtual

	function _cleared(topic)
		local v
		for _, p in ipairs(_pub) do if p.topic == topic then v = p.payload end end
		return v == ""
	end
	function _state()
		_sent = {}
		HACommands.handleExposedMessage("vclu/exposed/vclu/cover/clu1_rol2/set", "OPEN")
		return tostring(ExposedObjects:get("CLU1.ROL2") ~= nil) .. "|" ..
			tostring(ExposedObjects:get("CLU1.ROL1") ~= nil) .. "|" .. #_sent
	end
`

func TestExposeHiddenStaysOut(t *testing.T) {
	cases := []struct{ name, script string }{
		{"hidden before exposeRegistry", `
			expose(_hidden, "cover", { hidden = true })
			exposeRegistry()`},
		{"exposeRegistry before hidden", `
			exposeRegistry()
			expose(_hidden, "cover", { hidden = true })`},
		{"exposeRegistry before obj:expose hidden", `
			exposeRegistry()
			_hidden:expose({ hidden = true })`},
		{"obj:expose hidden before exposeRegistry", `
			_hidden:expose({ hidden = true })
			exposeRegistry()`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			engine := NewLuaEngine()
			defer engine.Close()
			if _, err := engine.Execute(exposeHiddenSetup); err != nil {
				t.Fatalf("setup failed: %v", err)
			}
			if _, err := engine.Execute(c.script); err != nil {
				t.Fatalf("script failed: %v", err)
			}
			// hidden one not exposed, the other one is, a command under its registry path sends nothing
			if got := mustExec(t, engine, `_state()`); got != "false|true|0" {
				t.Errorf("hidden exposed|shown exposed|commands sent = %s, want false|true|0", got)
			}
			if strings.HasPrefix(c.name, "exposeRegistry before") {
				if got := mustExec(t, engine, `tostring(_cleared("homeassistant/cover/vclu_clu1_rol2/config"))`); got != "true" {
					t.Errorf("Home Assistant discovery of the hidden cover not cleared")
				}
			}
		})
	}
}

// The same for a virtual object in the registry (Satel outputs, zones, partitions are ones):
// obj:expose({hidden = true}) after exposeRegistry() takes it back.
func TestExposeHiddenVirtualObject(t *testing.T) {
	for _, order := range []string{
		"exposeRegistry(); _virtual:expose({ hidden = true })",
		"_virtual:expose({ hidden = true }); exposeRegistry()",
	} {
		engine := NewLuaEngine()
		if _, err := engine.Execute(exposeHiddenSetup); err != nil {
			engine.Close()
			t.Fatalf("setup failed: %v", err)
		}
		if _, err := engine.Execute(order); err != nil {
			engine.Close()
			t.Fatalf("%s: %v", order, err)
		}
		if got := mustExec(t, engine, `tostring(ExposedObjects:get("virt.v1") ~= nil)`); got != "false" {
			t.Errorf("%s: hidden virtual object still exposed", order)
		}
		engine.Close()
	}
}

// expose() without hidden after a hidden one exposes the object: the module decides.
func TestExposeAfterHiddenExposes(t *testing.T) {
	engine := NewLuaEngine()
	defer engine.Close()
	if _, err := engine.Execute(exposeHiddenSetup); err != nil {
		t.Fatalf("setup failed: %v", err)
	}
	got := mustExec(t, engine, `(function()
		expose(_hidden, "cover", { hidden = true })
		expose(_hidden, "cover", { name = "Strych" })
		exposeRegistry()
		local at = ExposedObjects._objectToPath[_hidden]
		return tostring(at ~= nil and ExposedObjects:get(at) ~= nil)
	end)()`)
	if got != "true" {
		t.Errorf("object exposed after hidden: got %s", got)
	}
}
