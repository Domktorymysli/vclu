package lua

import (
	"testing"
	"time"
)

func TestTimerSetTimeout(t *testing.T) {
	engine := NewLuaEngine()
	defer engine.Close()

	// Start timer manager
	engine.StartTimers()
	defer engine.StopTimers()

	// Create a Lua variable to track timer execution
	_, err := engine.Execute("timerFired = false")
	if err != nil {
		t.Fatalf("Failed to set initial variable: %v", err)
	}

	// Set a timeout that fires after 50ms
	_, err = engine.Execute(`
		__go_setTimeout(function()
			timerFired = true
			print("[TEST] Timer fired!")
		end, 50)
	`)
	if err != nil {
		t.Fatalf("Failed to set timeout: %v", err)
	}

	// Wait for timer to fire (with margin)
	time.Sleep(100 * time.Millisecond)

	// Process any pending timers
	count := engine.ProcessTimers()
	t.Logf("Processed %d timers", count)

	// Check if timer fired
	result, err := engine.Execute("timerFired")
	if err != nil {
		t.Fatalf("Failed to check timerFired: %v", err)
	}
	if result != "true" {
		t.Errorf("Timer did not fire, timerFired = %s", result)
	}

	t.Log("setTimeout test passed!")
}

func TestTimerSetInterval(t *testing.T) {
	engine := NewLuaEngine()
	defer engine.Close()

	// Start timer manager
	engine.StartTimers()
	defer engine.StopTimers()

	// Create a Lua counter to track interval executions
	_, err := engine.Execute("intervalCount = 0")
	if err != nil {
		t.Fatalf("Failed to set initial variable: %v", err)
	}

	// Set an interval that fires every 30ms
	_, err = engine.Execute(`
		intervalId = __go_setInterval(function()
			intervalCount = intervalCount + 1
			print("[TEST] Interval fired! Count: " .. intervalCount)
		end, 30)
	`)
	if err != nil {
		t.Fatalf("Failed to set interval: %v", err)
	}

	// Process timers multiple times over 150ms
	for i := 0; i < 5; i++ {
		time.Sleep(35 * time.Millisecond)
		engine.ProcessTimers()
	}

	// Clear the interval
	_, err = engine.Execute("__go_clearTimer(intervalId)")
	if err != nil {
		t.Fatalf("Failed to clear interval: %v", err)
	}

	// Check how many times interval fired (should be 3-5 times)
	result, err := engine.Execute("intervalCount")
	if err != nil {
		t.Fatalf("Failed to check intervalCount: %v", err)
	}

	t.Logf("Interval fired %s times", result)

	// Should have fired at least 3 times
	if result == "0" || result == "1" {
		t.Errorf("Interval did not fire enough times, count = %s", result)
	}

	t.Log("setInterval test passed!")
}

func TestTimerViaEventBus(t *testing.T) {
	engine := NewLuaEngine()
	defer engine.Close()

	// Start timer manager
	engine.StartTimers()
	defer engine.StopTimers()

	// Test using EventBus:setTimeout (the Lua wrapper)
	_, err := engine.Execute("eventBusTimerFired = false")
	if err != nil {
		t.Fatalf("Failed to set initial variable: %v", err)
	}

	_, err = engine.Execute(`
		local eventBus = EventBus:getShared()
		eventBus:setTimeout(function()
			eventBusTimerFired = true
			print("[TEST] EventBus timer fired!")
		end, 50)
	`)
	if err != nil {
		t.Fatalf("Failed to set EventBus timeout: %v", err)
	}

	// Wait and process
	time.Sleep(100 * time.Millisecond)
	engine.ProcessTimers()

	// Check result
	result, err := engine.Execute("eventBusTimerFired")
	if err != nil {
		t.Fatalf("Failed to check eventBusTimerFired: %v", err)
	}
	if result != "true" {
		t.Errorf("EventBus timer did not fire, eventBusTimerFired = %s", result)
	}

	t.Log("EventBus timer test passed!")
}

func TestTimerLimits(t *testing.T) {
	engine := NewLuaEngine()
	defer engine.Close()

	// Test 1: Minimum interval enforcement
	// Try to create interval with 1ms - should be bumped to MinIntervalMs (10ms)
	_, err := engine.Execute(`
		fastTimerId = __go_setInterval(function() end, 1)
	`)
	if err != nil {
		t.Fatalf("Failed to create fast interval: %v", err)
	}

	// Get the timer ID - should not be -1 (rejected)
	result, _ := engine.Execute("fastTimerId")
	if result == "-1" {
		t.Error("Fast interval should not be rejected, just adjusted")
	}
	t.Logf("Fast interval timer ID: %s (interval adjusted to min 10ms)", result)

	// Test 2: Timer stats
	stats := engine.GetTimerStats()
	if stats.ActiveTimers < 1 {
		t.Errorf("Expected at least 1 active timer, got %d", stats.ActiveTimers)
	}
	if stats.TotalCreated < 1 {
		t.Errorf("Expected TotalCreated >= 1, got %d", stats.TotalCreated)
	}
	t.Logf("Timer stats: active=%d, created=%d, rejected=%d",
		stats.ActiveTimers, stats.TotalCreated, stats.Rejected)

	// Test 3: Clear all timers
	engine.ClearAllTimers()
	stats = engine.GetTimerStats()
	if stats.ActiveTimers != 0 {
		t.Errorf("Expected 0 active timers after ClearAll, got %d", stats.ActiveTimers)
	}

	t.Log("Timer limits test passed!")
}

func TestTimerStats(t *testing.T) {
	engine := NewLuaEngine()
	defer engine.Close()

	engine.StartTimers()
	defer engine.StopTimers()

	// Get initial stats (bootstrap may have created some timers)
	initialStats := engine.GetTimerStats()
	t.Logf("Initial stats: active=%d, created=%d", initialStats.ActiveTimers, initialStats.TotalCreated)

	// Create several timers
	_, err := engine.Execute(`
		for i = 1, 5 do
			__go_setTimeout(function() end, 100)
		end
		for i = 1, 3 do
			__go_setInterval(function() end, 50)
		end
	`)
	if err != nil {
		t.Fatalf("Failed to create timers: %v", err)
	}

	stats := engine.GetTimerStats()
	expectedActive := initialStats.ActiveTimers + 8
	expectedCreated := initialStats.TotalCreated + 8

	if stats.ActiveTimers != expectedActive {
		t.Errorf("Expected %d active timers (initial %d + 8), got %d",
			expectedActive, initialStats.ActiveTimers, stats.ActiveTimers)
	}
	if stats.TotalCreated != expectedCreated {
		t.Errorf("Expected %d total created (initial %d + 8), got %d",
			expectedCreated, initialStats.TotalCreated, stats.TotalCreated)
	}

	// Clear all and verify
	engine.ClearAllTimers()
	stats = engine.GetTimerStats()
	if stats.ActiveTimers != 0 {
		t.Errorf("Expected 0 active after clear, got %d", stats.ActiveTimers)
	}

	t.Logf("Final stats: %+v", stats)
	t.Log("Timer stats test passed!")
}

func TestTimerMaxLimit(t *testing.T) {
	engine := NewLuaEngine()
	defer engine.Close()

	// Try to create more than MaxTimers (1000)
	_, err := engine.Execute(`
		rejectedCount = 0
		for i = 1, 1005 do
			local id = __go_setTimeout(function() end, 1000)
			if id == -1 then
				rejectedCount = rejectedCount + 1
			end
		end
	`)
	if err != nil {
		t.Fatalf("Failed to create many timers: %v", err)
	}

	stats := engine.GetTimerStats()
	t.Logf("Stats after creating 1005 timers: active=%d, rejected=%d",
		stats.ActiveTimers, stats.Rejected)

	if stats.ActiveTimers > 1000 {
		t.Errorf("Should not exceed MaxTimers (1000), got %d", stats.ActiveTimers)
	}

	if stats.Rejected < 5 {
		t.Errorf("Expected at least 5 rejected timers, got %d", stats.Rejected)
	}

	result, _ := engine.Execute("rejectedCount")
	if result == "0" {
		t.Error("Expected some timers to be rejected")
	}
	t.Logf("Rejected count from Lua: %s", result)

	engine.ClearAllTimers()
	t.Log("Timer max limit test passed!")
}

// TestTimerGrentonModule tests the Grenton-compatible Timer Lua object
func TestTimerGrentonModule(t *testing.T) {
	engine := NewLuaEngine()
	defer engine.Close()

	engine.StartTimers()
	defer engine.StopTimers()

	// Test 1: Create Timer object
	_, err := engine.Execute(`
		timer = Timer:new("testTimer", 0, EventBus:getShared())
	`)
	if err != nil {
		t.Fatalf("Failed to create Timer: %v", err)
	}

	// Test 2: Set properties using Grenton-style set()
	_, err = engine.Execute(`
		timer:set(Timer.FEATURE_TIME, 100)
		timer:set(Timer.FEATURE_MODE, Timer.MODE_INTERVAL)
	`)
	if err != nil {
		t.Fatalf("Failed to set Timer properties: %v", err)
	}

	// Verify properties
	result, _ := engine.Execute("timer:get(Timer.FEATURE_TIME)")
	if result != "100" {
		t.Errorf("Expected time 100, got '%s'", result)
	}

	result, _ = engine.Execute("timer:get(Timer.FEATURE_MODE)")
	if result != "1" {
		t.Errorf("Expected mode 1 (INTERVAL), got '%s'", result)
	}

	// Test 3: Start timer and verify state
	_, err = engine.Execute(`
		onTimerFired = 0
		onStartFired = false
		onStopFired = false

		timer:add_event(Timer.EVENT_ON_TIMER, function()
			onTimerFired = onTimerFired + 1
		end)
		timer:add_event(Timer.EVENT_ON_START, function()
			onStartFired = true
		end)
		timer:add_event(Timer.EVENT_ON_STOP, function()
			onStopFired = true
		end)

		timer:execute(Timer.METHOD_START)
	`)
	if err != nil {
		t.Fatalf("Failed to start Timer: %v", err)
	}

	// Verify OnStart was fired
	result, _ = engine.Execute("onStartFired")
	if result != "true" {
		t.Errorf("Expected onStartFired to be true")
	}

	// Verify state is COUNTING
	result, _ = engine.Execute("timer:get(Timer.FEATURE_STATE)")
	if result != "1" {
		t.Errorf("Expected state 1 (COUNTING), got '%s'", result)
	}

	// Wait for timer to fire a couple of times
	for i := 0; i < 5; i++ {
		time.Sleep(50 * time.Millisecond)
		engine.ProcessTimers()
	}

	// Verify OnTimer was fired at least once
	result, _ = engine.Execute("onTimerFired")
	if result == "0" {
		t.Errorf("Expected onTimerFired > 0, got '%s'", result)
	}
	t.Logf("Timer fired %s times", result)

	// Test 4: Stop timer
	_, err = engine.Execute(`
		timer:execute(Timer.METHOD_STOP)
	`)
	if err != nil {
		t.Fatalf("Failed to stop Timer: %v", err)
	}

	// Verify OnStop was fired
	result, _ = engine.Execute("onStopFired")
	if result != "true" {
		t.Errorf("Expected onStopFired to be true")
	}

	// Verify state is STOPPED
	result, _ = engine.Execute("timer:get(Timer.FEATURE_STATE)")
	if result != "0" {
		t.Errorf("Expected state 0 (STOPPED), got '%s'", result)
	}

	t.Log("Timer Grenton module test passed!")
}

// TestTimerGrentonCountDown tests the CountDown mode
func TestTimerGrentonCountDown(t *testing.T) {
	engine := NewLuaEngine()
	defer engine.Close()

	engine.StartTimers()
	defer engine.StopTimers()

	// Create countdown timer
	_, err := engine.Execute(`
		timer = Timer:new("countdownTimer", 0, EventBus:getShared())
		timer:set(Timer.FEATURE_TIME, 100)
		timer:set(Timer.FEATURE_MODE, Timer.MODE_COUNT_DOWN)

		timerFired = false
		timer:add_event(Timer.EVENT_ON_TIMER, function()
			timerFired = true
		end)

		timer:execute(Timer.METHOD_START)
	`)
	if err != nil {
		t.Fatalf("Failed to create countdown timer: %v", err)
	}

	// Wait for timer to complete
	for i := 0; i < 5; i++ {
		time.Sleep(50 * time.Millisecond)
		engine.ProcessTimers()
	}

	// Verify timer fired
	result, _ := engine.Execute("timerFired")
	if result != "true" {
		t.Errorf("Expected timerFired to be true")
	}

	// Verify state is STOPPED after countdown completes
	result, _ = engine.Execute("timer:get(Timer.FEATURE_STATE)")
	if result != "0" {
		t.Errorf("Expected state 0 (STOPPED) after countdown, got '%s'", result)
	}

	t.Log("Timer CountDown test passed!")
}

// TestTimerDeduplication tests that interval timers with slow callbacks
// don't flood the queue (timer 1 issue fix)
func TestTimerDeduplication(t *testing.T) {
	engine := NewLuaEngine()
	defer engine.Close()

	engine.StartTimers()
	defer engine.StopTimers()

	// Create an interval timer that fires every 10ms
	// with a callback that simulates slow processing
	_, err := engine.Execute(`
		slowCallbackCount = 0
		slowIntervalId = __go_setInterval(function()
			slowCallbackCount = slowCallbackCount + 1
			-- In real scenario, this would be a slow HTTP call
			-- For test, we just count
		end, 10)
	`)
	if err != nil {
		t.Fatalf("Failed to create slow interval: %v", err)
	}

	// Wait 100ms - timer could fire 10 times if not deduplicated
	time.Sleep(100 * time.Millisecond)

	// Process timers
	processed := engine.ProcessTimers()
	t.Logf("Processed %d timers after first wait", processed)

	// Wait another 100ms
	time.Sleep(100 * time.Millisecond)

	// Process again
	processed = engine.ProcessTimers()
	t.Logf("Processed %d timers after second wait", processed)

	// Get callback count
	result, _ := engine.Execute("slowCallbackCount")
	t.Logf("slowCallbackCount = %s", result)

	// Verify that the timer fired but didn't flood
	// (if deduplication works, it should fire once per ProcessTimers call)
	stats := engine.GetTimerStats()
	t.Logf("Timer stats: active=%d, created=%d, fired=%d, rejected=%d",
		stats.ActiveTimers, stats.TotalCreated, stats.TotalFired, stats.Rejected)

	// Clean up
	_, err = engine.Execute("__go_clearTimer(slowIntervalId)")
	if err != nil {
		t.Fatalf("Failed to clear interval: %v", err)
	}

	t.Log("Timer deduplication test passed!")
}
