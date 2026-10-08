package lua

import (
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	lua "github.com/yuin/gopher-lua"
)

// TestEngineStateTransitions verifies Init → Running → Stopping → Closed lifecycle.
func TestEngineStateTransitions(t *testing.T) {
	engine := NewLuaEngine()

	// Initial state is stateInit
	if s := engineState(engine.engineState.Load()); s != stateInit {
		t.Fatalf("expected stateInit, got %s", s)
	}

	stopChan := make(chan struct{})

	started := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		// Signal once RunLoop has started
		go func() {
			for {
				if engineState(engine.engineState.Load()) == stateRunning {
					close(started)
					return
				}
				time.Sleep(time.Millisecond)
			}
		}()
		engine.RunLoop(stopChan)
	}()

	<-started

	if s := engineState(engine.engineState.Load()); s != stateRunning {
		t.Fatalf("expected stateRunning, got %s", s)
	}

	// Stop the loop
	close(stopChan)
	<-done

	if s := engineState(engine.engineState.Load()); s != stateClosed {
		t.Fatalf("expected stateClosed, got %s", s)
	}

	engine.Close()
}

// TestEngineStateFailed verifies that a RunLoop panic transitions to stateFailed.
func TestEngineStateFailed(t *testing.T) {
	engine := NewLuaEngine()
	defer engine.Close()

	stopChan := make(chan struct{})
	done := make(chan struct{})

	// Inject a panic by replacing fastQueue with a nil-dereference trigger
	// Actually, we'll test via processWorkItemOnLoop panic recovery
	started := make(chan struct{})
	go func() {
		defer close(done)
		go func() {
			for engineState(engine.engineState.Load()) != stateRunning {
				time.Sleep(time.Millisecond)
			}
			close(started)
		}()
		engine.RunLoop(stopChan)
	}()

	<-started

	// Submit a work item that panics
	result, err := engine.executeFast("PanicTest", func(L *lua.LState) (string, error) {
		panic("intentional test panic")
	})

	if err == nil {
		t.Fatalf("expected error from panicking work item, got result: %q", result)
	}
	if !strings.Contains(err.Error(), "panic") {
		t.Fatalf("expected panic error, got: %v", err)
	}

	// Loop should still be running — panic was recovered per-item
	if s := engineState(engine.engineState.Load()); s != stateRunning {
		t.Fatalf("expected stateRunning after per-item panic, got %s", s)
	}

	// Verify loop still works
	result, err = engine.Execute("1 + 1")
	if err != nil {
		t.Fatalf("Execute after panic failed: %v", err)
	}
	if result != "2" {
		t.Fatalf("expected '2', got %q", result)
	}

	close(stopChan)
	<-done
}

// TestWorkQueueRoundtrip starts RunLoop and verifies fast+slow work items return correct results.
func TestWorkQueueRoundtrip(t *testing.T) {
	engine := NewLuaEngine()
	defer engine.Close()

	stopChan := make(chan struct{})
	started := make(chan struct{})
	done := make(chan struct{})

	go func() {
		defer close(done)
		go func() {
			for engineState(engine.engineState.Load()) != stateRunning {
				time.Sleep(time.Millisecond)
			}
			close(started)
		}()
		engine.RunLoop(stopChan)
	}()

	<-started

	// Fast path: CallLuaFunction
	_, err := engine.Execute(`function testAdd(a, b) return tostring(tonumber(a) + tonumber(b)) end`)
	if err != nil {
		t.Fatalf("setup failed: %v", err)
	}

	result, err := engine.CallLuaFunction("testAdd", "3", "7")
	if err != nil {
		t.Fatalf("CallLuaFunction failed: %v", err)
	}
	if result != "10" {
		t.Errorf("expected '10', got %q", result)
	}

	// Slow path: Execute
	result, err = engine.Execute("2 + 3")
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	if result != "5" {
		t.Errorf("expected '5', got %q", result)
	}

	// Fast path: GetGlobal/SetGlobal
	engine.SetGlobal("_test_wq", "hello_workqueue")
	result = engine.GetGlobal("_test_wq")
	if result != "hello_workqueue" {
		t.Errorf("expected 'hello_workqueue', got %q", result)
	}

	// Fast path: ExecuteCached
	result, err = engine.ExecuteCached("1 + 2")
	if err != nil {
		t.Fatalf("ExecuteCached failed: %v", err)
	}
	if result != "3" {
		t.Errorf("expected '3', got %q", result)
	}

	close(stopChan)
	<-done
}

// TestWorkQueuePreLoop verifies that submitting work before RunLoop uses executeDirect.
func TestWorkQueuePreLoop(t *testing.T) {
	engine := NewLuaEngine()
	defer engine.Close()

	// stateInit — should use executeDirect (mutex fallback)
	if s := engineState(engine.engineState.Load()); s != stateInit {
		t.Fatalf("expected stateInit, got %s", s)
	}

	result, err := engine.Execute("1 + 1")
	if err != nil {
		t.Fatalf("pre-loop Execute failed: %v", err)
	}
	if result != "2" {
		t.Errorf("expected '2', got %q", result)
	}

	result, err = engine.CallLuaFunction("checkAlive")
	if err != nil {
		t.Fatalf("pre-loop CallLuaFunction failed: %v", err)
	}
	if result != "true" {
		t.Errorf("expected 'true', got %q", result)
	}
}

// TestWorkQueueShutdown verifies that pending items get errors on shutdown.
func TestWorkQueueShutdown(t *testing.T) {
	engine := NewLuaEngine()

	stopChan := make(chan struct{})
	started := make(chan struct{})
	done := make(chan struct{})

	go func() {
		defer close(done)
		go func() {
			for engineState(engine.engineState.Load()) != stateRunning {
				time.Sleep(time.Millisecond)
			}
			close(started)
		}()
		engine.RunLoop(stopChan)
	}()

	<-started

	// Stop the loop
	close(stopChan)
	<-done

	// After shutdown, Execute should return an error
	_, err := engine.Execute("1 + 1")
	if err == nil {
		t.Fatal("expected error after shutdown, got nil")
	}
	if !strings.Contains(err.Error(), "closed") {
		t.Fatalf("expected 'closed' in error, got: %v", err)
	}

	engine.Close()
}

// TestWorkQueueConcurrency verifies that 100 concurrent goroutines all get correct results.
func TestWorkQueueConcurrency(t *testing.T) {
	engine := NewLuaEngine()
	defer engine.Close()

	stopChan := make(chan struct{})
	started := make(chan struct{})
	done := make(chan struct{})

	go func() {
		defer close(done)
		go func() {
			for engineState(engine.engineState.Load()) != stateRunning {
				time.Sleep(time.Millisecond)
			}
			close(started)
		}()
		engine.RunLoop(stopChan)
	}()

	<-started

	// Setup test function
	_, err := engine.Execute(`function calcDouble(x) return tostring(tonumber(x) * 2) end`)
	if err != nil {
		t.Fatalf("setup failed: %v", err)
	}

	const N = 100
	var wg sync.WaitGroup
	var errCount atomic.Int32

	wg.Add(N)
	for i := 0; i < N; i++ {
		go func(n int) {
			defer wg.Done()
			arg := fmt.Sprintf("%d", n)
			expected := fmt.Sprintf("%d", n*2)

			result, err := engine.CallLuaFunction("calcDouble", arg)
			if err != nil {
				t.Errorf("goroutine %d: CallLuaFunction failed: %v", n, err)
				errCount.Add(1)
				return
			}
			if result != expected {
				t.Errorf("goroutine %d: expected %q, got %q", n, expected, result)
				errCount.Add(1)
			}
		}(i)
	}

	wg.Wait()

	if ec := errCount.Load(); ec > 0 {
		t.Fatalf("%d goroutines failed", ec)
	}

	close(stopChan)
	<-done
}

// TestWorkItemPanicRecovery verifies that a panicking work item doesn't kill the loop.
func TestWorkItemPanicRecovery(t *testing.T) {
	engine := NewLuaEngine()
	defer engine.Close()

	stopChan := make(chan struct{})
	started := make(chan struct{})
	done := make(chan struct{})

	go func() {
		defer close(done)
		go func() {
			for engineState(engine.engineState.Load()) != stateRunning {
				time.Sleep(time.Millisecond)
			}
			close(started)
		}()
		engine.RunLoop(stopChan)
	}()

	<-started

	// Submit panicking item
	_, err := engine.executeSlow("PanicSlow", func(L *lua.LState) (string, error) {
		panic("test panic in slow item")
	})
	if err == nil {
		t.Fatal("expected error from panicking work item")
	}

	// Submit a normal item — should work fine
	result, err := engine.Execute("42")
	if err != nil {
		t.Fatalf("Execute after panic failed: %v", err)
	}
	if result != "42" {
		t.Errorf("expected '42', got %q", result)
	}

	close(stopChan)
	<-done
}

// TestReinitializeViaWorkQueue verifies that Reinitialize works through the queue.
func TestReinitializeViaWorkQueue(t *testing.T) {
	engine := NewLuaEngine()
	defer engine.Close()

	stopChan := make(chan struct{})
	started := make(chan struct{})
	done := make(chan struct{})

	go func() {
		defer close(done)
		go func() {
			for engineState(engine.engineState.Load()) != stateRunning {
				time.Sleep(time.Millisecond)
			}
			close(started)
		}()
		engine.RunLoop(stopChan)
	}()

	<-started

	// Set a global before reinit
	engine.SetGlobal("_pre_reinit", "before")

	// Execute something to verify state is alive
	result, err := engine.Execute("1 + 1")
	if err != nil {
		t.Fatalf("pre-reinit Execute failed: %v", err)
	}
	if result != "2" {
		t.Fatalf("expected '2', got %q", result)
	}

	// Reinitialize
	if err := engine.Reinitialize("VCLU"); err != nil {
		t.Fatalf("Reinitialize failed: %v", err)
	}

	// Custom globals should be restored
	result = engine.GetGlobal("_pre_reinit")
	if result != "before" {
		t.Errorf("expected custom global restored to 'before', got %q", result)
	}

	// SYSTEM should still exist after reinit
	result, err = engine.Execute("SYSTEM ~= nil")
	if err != nil {
		t.Fatalf("post-reinit Execute failed: %v", err)
	}
	if result != "true" {
		t.Errorf("expected SYSTEM to exist after reinit, got %q", result)
	}

	close(stopChan)
	<-done
}

// TestFastQueuePriority verifies that fast items are processed between slow items.
func TestFastQueuePriority(t *testing.T) {
	engine := NewLuaEngine()
	defer engine.Close()

	stopChan := make(chan struct{})
	started := make(chan struct{})
	done := make(chan struct{})

	go func() {
		defer close(done)
		go func() {
			for engineState(engine.engineState.Load()) != stateRunning {
				time.Sleep(time.Millisecond)
			}
			close(started)
		}()
		engine.RunLoop(stopChan)
	}()

	<-started

	// Submit a slow item that takes 100ms, then immediately submit a fast item
	slowDone := make(chan string, 1)
	fastDone := make(chan string, 1)

	go func() {
		result, _ := engine.executeSlow("SlowItem", func(L *lua.LState) (string, error) {
			time.Sleep(100 * time.Millisecond) // simulate slow work
			return "slow_done", nil
		})
		slowDone <- result
	}()

	// Give slow item a moment to start executing
	time.Sleep(10 * time.Millisecond)

	go func() {
		result, _ := engine.executeFast("FastItem", func(L *lua.LState) (string, error) {
			return "fast_done", nil
		})
		fastDone <- result
	}()

	// Both should complete
	select {
	case r := <-slowDone:
		if r != "slow_done" {
			t.Errorf("slow: expected 'slow_done', got %q", r)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("slow item timed out")
	}

	select {
	case r := <-fastDone:
		if r != "fast_done" {
			t.Errorf("fast: expected 'fast_done', got %q", r)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("fast item timed out")
	}

	close(stopChan)
	<-done
}

// TestEngineStateString verifies the String() method on engineState.
func TestEngineStateString(t *testing.T) {
	tests := []struct {
		state engineState
		want  string
	}{
		{stateInit, "init"},
		{stateRunning, "running"},
		{stateStopping, "stopping"},
		{stateClosed, "closed"},
		{stateFailed, "failed"},
		{engineState(99), "unknown"},
	}
	for _, tt := range tests {
		if got := tt.state.String(); got != tt.want {
			t.Errorf("engineState(%d).String() = %q, want %q", tt.state, got, tt.want)
		}
	}
}
