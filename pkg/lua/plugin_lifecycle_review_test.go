package lua

import (
	"testing"
)

// Review of #206: a sandbox made again for the same plugin does not leave the old one running.
func TestReviewExecSandboxCreatedAgainStopsTheOld(t *testing.T) {
	engine := sandboxEngine(t, "@t/a", "")
	before := activeTimers(engine)
	if _, e := runPlugin(t, engine, "@t/a", `
		p = Plugin:new("a", {name = "A"})
		setInterval(100000, function() end)
		EventBus.on("t:x", function() end)
	`); e != "nil" {
		t.Fatal(e)
	}
	if activeTimers(engine) != before+1 {
		t.Fatalf("timers: %d -> %d", before, activeTimers(engine))
	}
	if _, err := engine.Execute(`
		__old = PluginSandbox.sandboxes["@t/a"]
		PluginSandbox:create("@t/a", nil)
	`); err != nil {
		t.Fatal(err)
	}
	if n := activeTimers(engine); n != before {
		t.Errorf("the old sandbox's interval survives: %d timers, want %d", n, before)
	}
	mustLua(t, engine, `tostring(__old.dead)`, "true")
	mustLua(t, engine, `tostring(Plugin.instances["@t/a"])`, "nil")
}

// Enabling a plugin that is running already (the toggle sent twice) restarts it once.
func TestReviewExecLoadPluginTwiceRunsOne(t *testing.T) {
	dir := t.TempDir()
	loader := installTestPlugin(t, dir, "@jan/twice", map[string]interface{}{"api": 2}, `
local plugin = Plugin:new("twice", {})
plugin:onInit(function() setInterval(100000, function() end) end)
plugin:onCleanup(function() __cleanups = (__cleanups or 0) + 1 end)
return plugin`, false)
	engine := apiEngine(t, loader, dir)
	before := activeTimers(engine)
	for i := 0; i < 2; i++ {
		if err := loader.LoadPlugin("@jan/twice", map[string]any{}); err != nil {
			t.Fatal(err)
		}
	}
	if n := activeTimers(engine); n != before+1 {
		t.Errorf("timers after loading twice: %d, want %d", n, before+1)
	}
}

// Code a module still holds runs after cleanup but starts no Modbus request.
func TestReviewExecModbusAfterCleanup(t *testing.T) {
	engine := sandboxEngine(t, "@t/a", `{modbus = true}`)
	if _, e := runPlugin(t, engine, "@t/a", `
		p = Plugin:new("a", {name = "A"})
		poll = function() Modbus.readHolding({ host = "192.168.1.9", addr = 0, qty = 1 }, function() end) end
		f32 = function() return Modbus.toFloat32(0x4148, 0) end
	`); e != "nil" {
		t.Fatal(e)
	}
	if _, err := engine.Execute(`
		__mb = { n = 0 }
		__go_modbus_request = function() __mb.n = __mb.n + 1; return true end
		local env = PluginSandbox.sandboxes["@t/a"].environment
		env.poll()
		__poll, __f32 = env.poll, env.f32
		PluginSandbox:cleanup("@t/a")
		pcall(__poll)
	`); err != nil {
		t.Fatal(err)
	}
	mustLua(t, engine, `__mb.n`, "1")
	mustLua(t, engine, `__f32()`, "12.5")
}

// The shared MQTT client keeps one callback per topic: a plugin neither takes a module's
// topic nor, when it stops, removes one a module subscribed to since.
func TestReviewExecMqttTopicOfAModule(t *testing.T) {
	engine := sandboxEngine(t, "@t/a", `{mqtt = true}`)
	if _, err := engine.Execute(`
		__got = { n = 0 }
		MQTT:subscribe("dom/x", function() __got.n = __got.n + 1 end)
	`); err != nil {
		t.Fatal(err)
	}
	_, e := runPlugin(t, engine, "@t/a", `p = Plugin:new("a", {}); p:mqttSubscribe("dom/x", function() end)`)
	if e == "nil" {
		t.Error("a module's topic taken over")
	}
	if _, e := runPlugin(t, engine, "@t/a", `
		p:mqttSubscribe("dom/y", function() end)
		p:mqttSubscribe("dom/y", function() end)
		p:mqttSubscribe("dom/z", function() end)
	`); e != "nil" {
		t.Fatalf("own topics: %s", e)
	}
	if _, err := engine.Execute(`
		MQTT:subscribe("dom/z", function() __got.n = __got.n + 10 end)
		PluginSandbox:cleanup("@t/a")
		MQTT:_dispatchMessage("dom/x", "1")
		MQTT:_dispatchMessage("dom/z", "1")
	`); err != nil {
		t.Fatal(err)
	}
	mustLua(t, engine, `__got.n`, "11")
	mustLua(t, engine, `tostring(MQTT._subscriptions["dom/y"])`, "nil")
}
