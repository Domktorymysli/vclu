package lua

import (
	"strings"
	"testing"
	"time"

	lua "github.com/yuin/gopher-lua"
)

func budgetEngine(t *testing.T, id string) *LuaEngine {
	t.Helper()
	engine := sandboxEngine(t, id, `{api = 2}`)
	engine.SetPluginTimeBudget(300*time.Millisecond, time.Second)
	engine.StartTimers()
	t.Cleanup(engine.StopTimers)
	return engine
}

// A plugin's loop in a timer stops at its budget, not at the callback limit, and the next
// timers run on.
func TestPluginBudgetStopsALoopInATimer(t *testing.T) {
	engine := budgetEngine(t, "@t/loop")
	if _, e := runPlugin(t, engine, "@t/loop", `
		local p = Plugin:new("loop")
		p.after = 0
		p:setTimeout(10, function() while true do end end)
		p:setTimeout(50, function() p.after = p.after + 1 end)
	`); e != "nil" {
		t.Fatal(e)
	}
	start := time.Now()
	waitPlugin(t, engine, `Plugin._lookup("@t/loop").after`, "1")
	if d := time.Since(start); d > 3*time.Second {
		t.Errorf("the next timer ran after %v", d)
	}
	u := engine.PluginUsage()["@t/loop"]
	if u.Overruns != 1 || u.CPU < 250*time.Millisecond {
		t.Errorf("usage %+v", u)
	}
	// a module's code keeps the callback limit
	if _, err := engine.ExecuteWithTimeout(`local t = os.clock() + 0.6 while os.clock() < t do end return "done"`, 5*time.Second); err != nil {
		t.Errorf("a module's 0.6 s: %v", err)
	}
	if got, err := engine.Execute(`local t = os.clock() + 0.6 while os.clock() < t do end return "done"`); err != nil || got != "done" {
		t.Errorf("a module's 0.6 s in a work item: %q %v", got, err)
	}
}

// A module calling a slow method of a plugin gets an error after the plugin's budget and goes
// on; a module's callback the plugin calls is the module's time.
func TestPluginBudgetMethodCalledFromAModule(t *testing.T) {
	engine := budgetEngine(t, "@t/slow")
	if _, e := runPlugin(t, engine, "@t/slow", `
		local p = Plugin:new("slow")
		function p:spin() while true do end end
		function p:call(fn) fn() return "ok" end
	`); e != "nil" {
		t.Fatal(e)
	}
	got, err := engine.Execute(`
		local ok, e = pcall(function() return Plugin.getPlugin("@t/slow"):spin() end)
		return tostring(ok) .. "|" .. tostring(e)
	`)
	if err != nil || !strings.HasPrefix(got, "false|") || !strings.Contains(got, "@t/slow: callback over 300ms, stopped") {
		t.Fatalf("spin: %q %v", got, err)
	}
	// the same callback goes on with the module's code, and a later call is the plugin's anew
	got, err = engine.Execute(`
		local f = assert(loadstring([[
			local busy = function() local t = os.clock() + 0.6 while os.clock() < t do end end
			return Plugin.getPlugin("@t/slow"):call(busy)
		]], "modules/m.lua"))
		return f()
	`)
	if err != nil || got != "ok" {
		t.Errorf("a module's 0.6 s callback inside the plugin: %q %v", got, err)
	}
	if u := engine.PluginUsage()["@t/slow"]; u.Overruns != 1 {
		t.Errorf("overruns %d, want 1", u.Overruns)
	}
}

// pcall, xpcall, a Go function calling back or the core's way around the limit do not take the plugin past its budget.
func TestPluginBudgetCannotBeLifted(t *testing.T) {
	engine := budgetEngine(t, "@t/sly")
	if r, e := runPlugin(t, engine, "@t/sly", `return type(__vclu_without_limit) .. type(__go_plugin_budget_loading) .. type(__go_plugin_budget_register)`); r != "nilnilnil" {
		t.Fatalf("plugin sees %q %s", r, e)
	}
	for _, loop := range []string{
		`while true do pcall(function() while true do end end) end`,
		`while true do if xpcall then xpcall(function() while true do end end, function() end) end end`,
		`string.gsub(string.rep("a", 1000), "a", function() while true do end end)`,
	} {
		start := time.Now()
		_, e := runPlugin(t, engine, "@t/sly", loop)
		if !strings.Contains(e, "callback over 300ms") {
			t.Errorf("%s: %s", loop, e)
		}
		if d := time.Since(start); d > 3*time.Second {
			t.Errorf("%s: stopped after %v", loop, d)
		}
		engine.ResumePlugin("@t/sly")
	}
}

// Three overruns in ten minutes suspend the plugin: its code stops at once until resumed.
func TestPluginBudgetSuspends(t *testing.T) {
	engine := budgetEngine(t, "@t/bad")
	suspended := make(chan string, 1)
	engine.SetOnPluginSuspend(func(id string) { suspended <- id })
	if _, e := runPlugin(t, engine, "@t/bad", `
		local p = Plugin:new("bad")
		function p:spin() while true do end end
		function p:hello() return "hi" end
	`); e != "nil" {
		t.Fatal(e)
	}
	spin := `local ok, e = pcall(function() return Plugin.getPlugin("@t/bad"):spin() end) return tostring(e)`
	for i := 0; i < 3; i++ {
		if got, _ := engine.Execute(spin); !strings.Contains(got, "callback over") {
			t.Fatalf("spin %d: %q", i, got)
		}
	}
	select {
	case id := <-suspended:
		if id != "@t/bad" {
			t.Errorf("suspended %q", id)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("not suspended after 3 overruns")
	}
	if !engine.PluginUsage()["@t/bad"].Suspended {
		t.Error("usage does not say suspended")
	}
	hello := `local t = os.clock() + 0.01 while os.clock() < t do end local ok, e = pcall(function() local s = "" for i = 1, 20000 do s = Plugin.getPlugin("@t/bad"):hello() end return s end) return tostring(ok) .. "|" .. tostring(e)`
	if got, _ := engine.Execute(hello); !strings.Contains(got, "is suspended") {
		t.Errorf("a suspended plugin's code: %q", got)
	}
	engine.ResumePlugin("@t/bad")
	if got, _ := engine.Execute(hello); got != "true|hi" {
		t.Errorf("after resume: %q", got)
	}
}

// init.lua gets the load budget; over it, the load fails with the reason.
func TestPluginBudgetAtLoad(t *testing.T) {
	engine := budgetEngine(t, "@t/init")
	if _, err := engine.Execute(`__go_plugin_budget_loading("@t/init", true)`); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	if _, e := runPlugin(t, engine, "@t/init", `local t = os.clock() + 0.6 while os.clock() < t do end return "ok"`); e != "nil" {
		t.Errorf("0.6 s at load (budget 1 s): %s", e)
	}
	if _, e := runPlugin(t, engine, "@t/init", `while true do end`); !strings.Contains(e, "callback over 1s, stopped") {
		t.Errorf("a loop at load: %s", e)
	}
	if d := time.Since(start); d > 4*time.Second {
		t.Errorf("load stopped after %v", d)
	}
}

// The budget's cost per instruction: a module's loop with and without plugins registered.
func BenchmarkPluginBudget(b *testing.B) {
	for _, withPlugin := range []bool{false, true} {
		name := "none"
		if withPlugin {
			name = "plugin"
		}
		b.Run(name, func(b *testing.B) {
			L := lua.NewState()
			defer L.Close()
			budget := newPluginBudget("")
			budgets.Store(L, budget)
			defer budgets.Delete(L)
			if withPlugin {
				budget.register("@t/x", L.NewTable())
			}
			fn, err := L.LoadString(`local x = 0 for i = 1, 100000 do x = x + i * 2 end return x`)
			if err != nil {
				b.Fatal(err)
			}
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if _, err := runLimited(L, time.Minute, func() error {
					L.Push(fn)
					return L.PCall(0, 0, nil)
				}); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
