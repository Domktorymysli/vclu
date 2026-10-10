package mcp

import (
	"runtime"
	"strings"
	"testing"
	"time"
)

// A runaway draft must be stopped before it takes the process (and with it
// the house) down; the sandbox shares memory with production.
func TestSandbox_MemoryLimit(t *testing.T) {
	old := sandboxMemoryLimit
	sandboxMemoryLimit = 64 << 20
	defer func() { sandboxMemoryLimit = old }()

	for name, code := range map[string]string{
		"loop":       `local t = {} while true do t[#t+1] = string.rep("x", 1000000) .. #t end`,
		"string.rep": `local s = string.rep("x", 1e10)`,
	} {
		t.Run(name, func(t *testing.T) {
			se, ws, _ := newTestSandbox(t)
			writeDraft(t, ws, "modules/probe.lua", code)
			start := time.Now()
			res, err := se.RunScenario([]string{"modules/probe.lua"}, SandboxScenario{})
			if err != nil {
				t.Fatal(err)
			}
			if res.Success || !strings.Contains(strings.Join(res.Errors, "\n"), "memory limit") {
				t.Fatalf("want a memory limit error, got success=%v errors=%v", res.Success, res.Errors)
			}
			if d := time.Since(start); d > 3*time.Second {
				t.Errorf("stopped after %v, the guard should act long before the timeout", d)
			}
			runtime.GC()
		})
	}

	// an ordinary draft is not affected, nor one that churns garbage well past
	// the limit while keeping little alive
	se, ws, _ := newTestSandbox(t)
	writeDraft(t, ws, "modules/ok.lua", `
		local s = string.rep("ab", 1000) assert(#s == 2000)
		for i = 1, 400 do local g = string.rep("x", 1000000) .. i end
	`)
	res, err := se.RunScenario([]string{"modules/ok.lua"}, SandboxScenario{})
	if err != nil || !res.Success {
		t.Fatalf("plain draft failed: %v %v", err, res.Errors)
	}
}

// A draft holding a lot just under the limit while churning garbage must not
// make the guard force a GC on every tick (that would load production).
func TestSandbox_MemoryGuardBacksOff(t *testing.T) {
	old := sandboxMemoryLimit
	sandboxMemoryLimit = 64 << 20
	defer func() { sandboxMemoryLimit = old }()

	se, ws, _ := newTestSandbox(t)
	writeDraft(t, ws, "modules/churn.lua", `
		local keep = {}
		for i = 1, 30 do keep[i] = string.rep("k", 1000000) .. i end
		local t0 = os.clock()
		while os.clock() - t0 < 1.5 do local g = string.rep("x", 1000) .. t0 end
	`)
	before := guardForcedGCs.Load()
	res, err := se.RunScenario([]string{"modules/churn.lua"}, SandboxScenario{})
	if err != nil {
		t.Fatal(err)
	}
	gcs := guardForcedGCs.Load() - before
	t.Logf("forced GCs: %d, errors: %v", gcs, res.Errors)
	if len(res.Errors) > 0 {
		t.Fatalf("a draft under the limit was stopped: %v", res.Errors)
	}
	// false alarms happened (the back-off path ran), but not on every tick
	if gcs < 2 || gcs > 12 {
		t.Errorf("guard forced %d GCs in 1.5 s, want 2..12", gcs)
	}
}

// Loading a draft must not drop the scenario context: event handlers and
// timers run later and still have to stop on timeout and on the memory limit.
func TestSandbox_LimitsApplyAfterLoad(t *testing.T) {
	oldLimit, oldTimeout := sandboxMemoryLimit, sandboxTimeout
	// the race detector slows allocation: give "grow" time to hit the limit
	sandboxMemoryLimit, sandboxTimeout = 64<<20, 3*time.Second
	defer func() { sandboxMemoryLimit, sandboxTimeout = oldLimit, oldTimeout }()

	se, ws, _ := newTestSandbox(t)
	writeDraft(t, ws, "modules/probe.lua", `
		EventBus:getShared():on("spin", function() while true do end end)
		EventBus:getShared():on("grow", function()
			local t = {} while true do t[#t+1] = string.rep("x", 1000000) .. #t end
		end)
	`)
	for _, c := range []struct{ event, want string }{{"spin", "timeout"}, {"grow", "memory limit"}} {
		res, err := se.RunScenario([]string{"modules/probe.lua"}, SandboxScenario{Steps: mustSteps(t, `[{"emit": "`+c.event+`", "data": {}}]`)})
		if err != nil {
			t.Fatal(err)
		}
		if res.Success || !strings.Contains(strings.Join(res.Errors, "\n"), c.want) {
			t.Errorf("%s: want %q error, got %v", c.event, c.want, res.Errors)
		}
		// the Lua goroutine has stopped, not left spinning
		time.Sleep(100 * time.Millisecond)
		buf := make([]byte, 1<<20)
		if n := runtime.Stack(buf, true); strings.Contains(string(buf[:n]), "injectEvent") {
			t.Errorf("%s: Lua still running after RunScenario returned", c.event)
		}
		runtime.GC()
	}
}
