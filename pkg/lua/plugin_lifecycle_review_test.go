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
