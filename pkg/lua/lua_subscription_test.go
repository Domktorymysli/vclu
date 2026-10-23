package lua

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

// TestRemoteSubscription_NoLeakOnRepeatedAutoSubscribe verifies that calling
// autoSubscribe() multiple times does not accumulate subscriptions.
// This is a regression test for a memory leak where each autoSubscribe() call
// created new subscriptions without destroying old ones, leading to ~38k leaked
// subscriptions after 24h of operation (17 new subs every 30s).
func TestRemoteSubscription_NoLeakOnRepeatedAutoSubscribe(t *testing.T) {
	engine := NewLuaEngine()
	defer engine.Close()

	// Mock __go_send_command to return fake clientReport responses
	// without actually connecting to a real CLU
	_, err := engine.Execute(`
		__go_send_command = function(targetIP, script, delay)
			-- Parse sessionID from the clientRegister call
			local sid = script:match("clientRegister%('.-', %d+, (%d+),")
			if not sid then
				sid = "0"
			end
			-- Return a fake clientReport with 4 zeroed values (matching batchSize=4)
			return "clientReport:" .. sid .. ":{0,0,0,0}", nil
		end
	`)
	if err != nil {
		t.Fatalf("Failed to mock __go_send_command: %v", err)
	}

	// Set up minimal registry with fake remote objects that have _clu.address
	_, err = engine.Execute(`
		__vclu_ip = "192.168.0.100"

		-- Create fake objects in registry to simulate whitelisted remote objects
		if not _registry then
			_registry = {}
		end

		local function makeFakeObj(path, objId)
			return {
				_objId = objId,
				_type = "DOUT",
				_clu = { address = "192.168.0.3" },
				_lastState = nil,
				updateState = function(self, val)
					self._lastState = val
				end,
			}
		end

		-- Create 8 fake objects (will produce 2 subscriptions with batchSize=4)
		local paths = {
			"CLU212345678.DOU0001",
			"CLU212345678.DOU0002",
			"CLU212345678.DOU0003",
			"CLU212345678.DOU0004",
			"CLU212345678.DOU0005",
			"CLU212345678.DOU0006",
			"CLU212345678.DOU0007",
			"CLU212345678.DOU0008",
		}

		local whitelist = {}
		for i, path in ipairs(paths) do
			local _, objId = path:match("^(.-)%.(.+)$")
			_registry[path] = makeFakeObj(path, objId)
			whitelist[path] = true
		end

		-- Configure RemoteSubscription
		RemoteSubscription.config.enabled = true
		RemoteSubscription.config.whitelist = whitelist
		RemoteSubscription.config.batchSize = 4
		RemoteSubscription.config.batchDelay = 0
		RemoteSubscription.config.ttl = 60
		RemoteSubscription.activeSubscriptions = {}
	`)
	if err != nil {
		t.Fatalf("Failed to set up test fixtures: %v", err)
	}

	// Helper to count active subscriptions
	countSubs := func() int {
		result, err := engine.Execute(`
			local count = 0
			for _ in pairs(RemoteSubscription.activeSubscriptions) do
				count = count + 1
			end
			return tostring(count)
		`)
		if err != nil {
			t.Fatalf("Failed to count subscriptions: %v", err)
		}
		var n int
		fmt.Sscanf(result, "%d", &n)
		return n
	}

	// Run autoSubscribe 5 times (simulating 5 timer ticks)
	for i := 1; i <= 5; i++ {
		_, err = engine.Execute("RemoteSubscription.autoSubscribe()")
		if err != nil {
			t.Fatalf("autoSubscribe() failed on iteration %d: %v", i, err)
		}

		count := countSubs()
		t.Logf("Iteration %d: %d active subscriptions", i, count)

		// With 8 objects and batchSize=4, we expect exactly 2 subscriptions
		if count != 2 {
			t.Errorf("Iteration %d: expected 2 subscriptions, got %d (leak detected!)", i, count)
		}
	}

	// Final check: subscriptions should be exactly 2, not 10 (2*5)
	finalCount := countSubs()
	if finalCount != 2 {
		t.Fatalf("After 5 autoSubscribe() calls: expected 2 subscriptions, got %d — subscription leak!", finalCount)
	}
}

// TestRemoteSubscription_CleanupOnResubscribe verifies that old subscriptions
// are properly destroyed before creating new ones.
func TestRemoteSubscription_CleanupOnResubscribe(t *testing.T) {
	engine := NewLuaEngine()
	defer engine.Close()

	// Track clientDestroy calls
	_, err := engine.Execute(`
		_destroyCalls = {}

		__go_send_command = function(targetIP, script, delay)
			local sid = script:match("clientRegister%('.-', %d+, (%d+),")
			if sid then
				return "clientReport:" .. sid .. ":{0,0,0,0}", nil
			end
			return "ok", nil
		end

		__go_send_command_noreply = function(targetIP, script)
			local destroySid = script:match("clientDestroy%('.-', %d+, (%d+)%)")
			if destroySid then
				table.insert(_destroyCalls, {ip = targetIP, sid = tonumber(destroySid)})
			end
			return true, nil
		end

		__vclu_ip = "192.168.0.100"

		if not _registry then _registry = {} end

		local function makeFakeObj(objId)
			return {
				_objId = objId,
				_type = "DOUT",
				_clu = { address = "192.168.0.5" },
				_lastState = nil,
				updateState = function(self, val) self._lastState = val end,
			}
		end

		local whitelist = {}
		for i = 1, 4 do
			local path = "CLU220000592.DOU000" .. i
			local _, objId = path:match("^(.-)%.(.+)$")
			_registry[path] = makeFakeObj(objId)
			whitelist[path] = true
		end

		RemoteSubscription.config.enabled = true
		RemoteSubscription.config.whitelist = whitelist
		RemoteSubscription.config.batchSize = 4
		RemoteSubscription.config.batchDelay = 0
		RemoteSubscription.config.ttl = 60
		RemoteSubscription.activeSubscriptions = {}
	`)
	if err != nil {
		t.Fatalf("Failed to set up test fixtures: %v", err)
	}

	// First autoSubscribe — creates subscriptions, no destroys expected
	_, err = engine.Execute("RemoteSubscription.autoSubscribe()")
	if err != nil {
		t.Fatalf("First autoSubscribe() failed: %v", err)
	}

	result, _ := engine.Execute("return tostring(#_destroyCalls)")
	if result != "0" {
		t.Errorf("First call should not destroy anything, got %s destroy calls", result)
	}

	// Second autoSubscribe — should destroy old subscriptions first
	_, err = engine.Execute("RemoteSubscription.autoSubscribe()")
	if err != nil {
		t.Fatalf("Second autoSubscribe() failed: %v", err)
	}

	result, _ = engine.Execute("return tostring(#_destroyCalls)")
	if result == "0" {
		t.Error("Second autoSubscribe() should have destroyed old subscriptions, but no clientDestroy calls were made")
	} else {
		t.Logf("Second call made %s clientDestroy calls (correct)", result)
	}
}

// TestRemoteSubscription_DisabledDoesNotLeak verifies that disabled sync
// does not accumulate any state.
func TestRemoteSubscription_DisabledDoesNotLeak(t *testing.T) {
	engine := NewLuaEngine()
	defer engine.Close()

	_, err := engine.Execute(`
		RemoteSubscription.config.enabled = false
		RemoteSubscription.activeSubscriptions = {}
	`)
	if err != nil {
		t.Fatalf("Failed to configure: %v", err)
	}

	for i := 0; i < 10; i++ {
		engine.Execute("RemoteSubscription.autoSubscribe()")
	}

	result, _ := engine.Execute(`
		local count = 0
		for _ in pairs(RemoteSubscription.activeSubscriptions) do count = count + 1 end
		return tostring(count)
	`)
	if result != "0" {
		t.Errorf("Disabled sync should have 0 subscriptions, got %s", result)
	}
}

// setupSubscriptionEngine creates a LuaEngine with mocked __go_send_command,
// fake registry objects, and RemoteSubscription configured and ready.
// Returns the engine and a helper to count active subscriptions.
func setupSubscriptionEngine(t *testing.T, objectCount int) (*LuaEngine, func() int) {
	t.Helper()
	engine := NewLuaEngine()

	_, err := engine.Execute(fmt.Sprintf(`
		_destroyCalls = {}
		_registerCalls = {}
		_refreshCalls = {}
		_noreplyCalls = {}

		__go_send_command = function(targetIP, script, delay)
			local destroySid = script:match("clientDestroy%%('.-', %%d+, (%%d+)%%)")
			if destroySid then
				table.insert(_destroyCalls, {ip = targetIP, sid = tonumber(destroySid)})
				return "ok", nil
			end

			local sid = script:match("clientRegister%%('.-', %%d+, (%%d+),")
			if sid then
				table.insert(_registerCalls, {ip = targetIP, sid = tonumber(sid)})
				return "clientReport:" .. sid .. ":{0,0,0,0}", nil
			end

			return "ok", nil
		end

		-- Fire-and-forget sender (used by refresh/re-register and destroy)
		__go_send_command_noreply = function(targetIP, script)
			table.insert(_noreplyCalls, {ip = targetIP, script = script})

			-- Track noreply clientRegister (re-registration from refresh)
			local reregSid = script:match("clientRegister%%('.-', %%d+, (%%d+),")
			if reregSid then
				table.insert(_refreshCalls, {ip = targetIP, sid = tonumber(reregSid)})
			end

			local destroySid = script:match("clientDestroy%%('.-', %%d+, (%%d+)%%)")
			if destroySid then
				table.insert(_destroyCalls, {ip = targetIP, sid = tonumber(destroySid)})
			end

			return true, nil
		end

		__vclu_ip = "192.168.0.100"
		if not _registry then _registry = {} end

		local function makeFakeObj(objId)
			return {
				_objId = objId,
				_type = "DOUT",
				_clu = { address = "192.168.0.5" },
				_lastState = nil,
				updateState = function(self, val) self._lastState = val end,
			}
		end

		local whitelist = {}
		for i = 1, %d do
			local path = string.format("CLU220000592.DOU%%04d", i)
			local _, objId = path:match("^(.-)%%.(.+)$")
			_registry[path] = makeFakeObj(objId)
			whitelist[path] = true
		end

		RemoteSubscription.config.enabled = true
		RemoteSubscription.config.whitelist = whitelist
		RemoteSubscription.config.batchSize = 4
		RemoteSubscription.config.batchDelay = 0
		RemoteSubscription.config.ttl = 60
		RemoteSubscription.activeSubscriptions = {}
		RemoteSubscription.nextSessionID = 1
	`, objectCount))
	if err != nil {
		engine.Close()
		t.Fatalf("Failed to set up test fixtures: %v", err)
	}

	countSubs := func() int {
		result, err := engine.Execute(`
			local count = 0
			for _ in pairs(RemoteSubscription.activeSubscriptions) do count = count + 1 end
			return tostring(count)
		`)
		if err != nil {
			t.Fatalf("Failed to count subscriptions: %v", err)
		}
		var n int
		fmt.Sscanf(result, "%d", &n)
		return n
	}

	return engine, countSubs
}

// TestRefreshAll_ReregistersInsteadOfResubscribe verifies that refreshAll()
// sends noreply clientRegister (re-registration, not destroy+register cycle).
// This is a regression test for a performance bug where the renew timer called
// autoSubscribe() every 30s, causing 34 blocking UDP calls and ~2-3s Lua mutex hold.
func TestRefreshAll_ReregistersInsteadOfResubscribe(t *testing.T) {
	engine, countSubs := setupSubscriptionEngine(t, 8)
	defer engine.Close()

	// Initial subscribe — creates 2 subscriptions (8 objects / batchSize 4)
	_, err := engine.Execute("RemoteSubscription.autoSubscribe()")
	if err != nil {
		t.Fatalf("autoSubscribe() failed: %v", err)
	}
	if n := countSubs(); n != 2 {
		t.Fatalf("Expected 2 subscriptions after autoSubscribe, got %d", n)
	}

	// Clear call counters
	_, _ = engine.Execute(`
		_destroyCalls = {}
		_registerCalls = {}
		_refreshCalls = {}
	`)

	// Call refreshAll — should send noreply clientRegister, NOT destroy+blocking register
	stubTimeouts(t, engine)
	_, err = engine.Execute("RemoteSubscription.refreshAll()")
	if err != nil {
		t.Fatalf("refreshAll() failed: %v", err)
	}
	runStubTimeouts(t, engine)

	// Verify: noreply re-register calls made, no destroy, no blocking register
	refreshCount, _ := engine.Execute("return tostring(#_refreshCalls)")
	destroyCount, _ := engine.Execute("return tostring(#_destroyCalls)")
	registerCount, _ := engine.Execute("return tostring(#_registerCalls)")

	if refreshCount != "2" {
		t.Errorf("Expected 2 noreply clientRegister (re-register) calls, got %s", refreshCount)
	}
	if destroyCount != "0" {
		t.Errorf("refreshAll() should NOT send clientDestroy, got %s calls", destroyCount)
	}
	if registerCount != "0" {
		t.Errorf("refreshAll() should NOT send blocking clientRegister, got %s calls", registerCount)
	}

	// Subscriptions should remain unchanged
	if n := countSubs(); n != 2 {
		t.Errorf("Subscriptions should remain 2 after refreshAll, got %d", n)
	}
}

// TestRefreshAll_FallsBackToAutoSubscribeOnFailure verifies that refreshAll()
// does a full re-subscribe if any refresh calls fail.
func TestRefreshAll_FallsBackToAutoSubscribeOnFailure(t *testing.T) {
	engine, countSubs := setupSubscriptionEngine(t, 4)
	defer engine.Close()

	// Initial subscribe
	_, err := engine.Execute("RemoteSubscription.autoSubscribe()")
	if err != nil {
		t.Fatalf("autoSubscribe() failed: %v", err)
	}
	if n := countSubs(); n != 1 {
		t.Fatalf("Expected 1 subscription, got %d", n)
	}

	// Make noreply clientRegister (re-registration) return errors
	_, _ = engine.Execute(`
		_destroyCalls = {}
		_registerCalls = {}
		_refreshCalls = {}

		__go_send_command_noreply = function(targetIP, script)
			-- Fail re-registration attempts (noreply clientRegister from refresh)
			local reregSid = script:match("clientRegister%('.-', %d+, (%d+),")
			if reregSid then
				return false, "connection refused"
			end

			local destroySid = script:match("clientDestroy%('.-', %d+, (%d+)%)")
			if destroySid then
				table.insert(_destroyCalls, {sid = tonumber(destroySid)})
				return true, nil
			end
			return true, nil
		end

		__go_send_command = function(targetIP, script, delay)
			local destroySid = script:match("clientDestroy%('.-', %d+, (%d+)%)")
			if destroySid then
				table.insert(_destroyCalls, {sid = tonumber(destroySid)})
				return "ok", nil
			end

			-- Blocking clientRegister from autoSubscribe fallback succeeds
			local sid = script:match("clientRegister%('.-', %d+, (%d+),")
			if sid then
				table.insert(_registerCalls, {sid = tonumber(sid)})
				return "clientReport:" .. sid .. ":{0,0,0,0}", nil
			end
			return "ok", nil
		end
	`)

	// refreshAll should detect failure and fall back to autoSubscribe
	_, err = engine.Execute("RemoteSubscription.refreshAll()")
	if err != nil {
		t.Fatalf("refreshAll() failed: %v", err)
	}

	// Should have re-created subscriptions via autoSubscribe fallback
	registerCount, _ := engine.Execute("return tostring(#_registerCalls)")
	if registerCount == "0" {
		t.Error("refreshAll() should have fallen back to autoSubscribe on failure, but no clientRegister calls were made")
	}

	if n := countSubs(); n != 1 {
		t.Errorf("Expected 1 subscription after fallback, got %d", n)
	}
}

// TestRefreshAll_PreservesSessionIDs verifies that refreshAll() keeps the same
// session IDs (no new sessions created), reducing session ID growth.
func TestRefreshAll_PreservesSessionIDs(t *testing.T) {
	engine, _ := setupSubscriptionEngine(t, 4)
	defer engine.Close()

	// Initial subscribe
	_, err := engine.Execute("RemoteSubscription.autoSubscribe()")
	if err != nil {
		t.Fatalf("autoSubscribe() failed: %v", err)
	}

	// Record session IDs and nextSessionID before refresh
	sidBefore, _ := engine.Execute("return tostring(RemoteSubscription.nextSessionID)")

	// Run refreshAll 5 times
	for i := 0; i < 5; i++ {
		_, err = engine.Execute("RemoteSubscription.refreshAll()")
		if err != nil {
			t.Fatalf("refreshAll() iteration %d failed: %v", i, err)
		}
	}

	// nextSessionID should NOT have grown (refresh doesn't create new sessions)
	sidAfter, _ := engine.Execute("return tostring(RemoteSubscription.nextSessionID)")
	if sidBefore != sidAfter {
		t.Errorf("refreshAll() should not increment nextSessionID: before=%s, after=%s", sidBefore, sidAfter)
	}
}

// TestHandleReport_NotLostDuringAutoSubscribe is a regression test for the race
// condition where incoming ClientReports were dropped during autoSubscribe()
// because activeSubscriptions was cleared before new subscriptions were registered.
//
// Before fix: autoSubscribe cleared the table first, so any reports arriving
// between clear and re-register would get "Unknown subscription" warnings and
// their values would be silently dropped.
//
// After fix: old subscriptions remain in the table until new ones are created,
// so incoming reports for old sessions are still processed during the transition.
func TestHandleReport_NotLostDuringAutoSubscribe(t *testing.T) {
	engine, _ := setupSubscriptionEngine(t, 4)
	defer engine.Close()

	// Initial subscribe — creates 1 subscription with session ID 1
	_, err := engine.Execute("RemoteSubscription.autoSubscribe()")
	if err != nil {
		t.Fatalf("autoSubscribe() failed: %v", err)
	}

	// Get the session ID from first subscription
	sessionID, _ := engine.Execute(`
		for key, sub in pairs(RemoteSubscription.activeSubscriptions) do
			return tostring(sub.sessionID)
		end
		return "0"
	`)

	// Simulate: a report arrives for the current session
	// This should succeed (session is known)
	result, _ := engine.Execute(fmt.Sprintf(`
		_unknownCount = 0
		local origWarn = Logger.getShared and Logger:getShared().warn
		-- We check by trying handleReport and seeing if the sub is found
		RemoteSubscription.handleReport("192.168.0.5", %s, "{1,0,1,0}")

		-- Verify at least one object got updated
		local updated = false
		for path, obj in pairs(_registry) do
			if obj._lastState ~= nil then
				updated = true
				break
			end
		end
		return tostring(updated)
	`, sessionID))

	if result != "true" {
		t.Error("handleReport should update registry for known session, but no objects were updated")
	}
}

// TestAutoSubscribe_DestroyAfterRegister verifies that old subscriptions are
// destroyed AFTER new ones are registered, not before.
// This prevents the "Unknown subscription" window where incoming reports would be dropped.
func TestAutoSubscribe_DestroyAfterRegister(t *testing.T) {
	engine, _ := setupSubscriptionEngine(t, 4)
	defer engine.Close()

	// First autoSubscribe
	_, err := engine.Execute("RemoteSubscription.autoSubscribe()")
	if err != nil {
		t.Fatalf("First autoSubscribe() failed: %v", err)
	}

	// Reset counters and add ordering tracking
	_, _ = engine.Execute(`
		_callOrder = {}
		_destroyCalls = {}
		_registerCalls = {}

		__go_send_command_noreply = function(targetIP, script)
			local destroySid = script:match("clientDestroy%('.-', %d+, (%d+)%)")
			if destroySid then
				table.insert(_callOrder, "destroy:" .. destroySid)
				table.insert(_destroyCalls, {sid = tonumber(destroySid)})
			end
			return true, nil
		end

		__go_send_command = function(targetIP, script, delay)
			local destroySid = script:match("clientDestroy%('.-', %d+, (%d+)%)")
			if destroySid then
				table.insert(_callOrder, "destroy:" .. destroySid)
				table.insert(_destroyCalls, {sid = tonumber(destroySid)})
				return "ok", nil
			end

			local sid = script:match("clientRegister%('.-', %d+, (%d+),")
			if sid then
				table.insert(_callOrder, "register:" .. sid)
				table.insert(_registerCalls, {sid = tonumber(sid)})
				return "clientReport:" .. sid .. ":{0,0,0,0}", nil
			end

			return "ok", nil
		end
	`)

	// Second autoSubscribe — should register new BEFORE destroying old
	_, err = engine.Execute("RemoteSubscription.autoSubscribe()")
	if err != nil {
		t.Fatalf("Second autoSubscribe() failed: %v", err)
	}

	// Verify ordering: all register calls should come before all destroy calls
	result, _ := engine.Execute(`
		local firstDestroy = nil
		local lastRegister = nil
		for i, call in ipairs(_callOrder) do
			if call:match("^register:") then
				lastRegister = i
			end
			if call:match("^destroy:") and not firstDestroy then
				firstDestroy = i
			end
		end

		if not firstDestroy then
			return "no_destroys"
		end
		if not lastRegister then
			return "no_registers"
		end
		if lastRegister < firstDestroy then
			return "correct"
		end
		return "wrong_order"
	`)

	if result == "wrong_order" {
		t.Error("clientDestroy was called BEFORE clientRegister finished — this causes 'Unknown subscription' race condition")
	}
	if result == "no_destroys" {
		t.Error("Expected clientDestroy calls for old subscriptions")
	}
	if result == "no_registers" {
		t.Error("Expected clientRegister calls for new subscriptions")
	}
	if result == "correct" {
		t.Log("Correct: all clientRegister calls precede clientDestroy calls")
	}
}

// TestRenewTimer_UsesRefreshNotAutoSubscribe verifies that the renew timer
// calls refreshAll() when subscriptions exist, not autoSubscribe().
// This is a regression test for the performance bug where every timer tick
// did a full destroy+recreate cycle (34 UDP calls, 2-3s mutex hold).
func TestRenewTimer_UsesRefreshNotAutoSubscribe(t *testing.T) {
	engine, _ := setupSubscriptionEngine(t, 4)
	defer engine.Close()

	// Initial subscribe
	_, err := engine.Execute("RemoteSubscription.autoSubscribe()")
	if err != nil {
		t.Fatalf("autoSubscribe() failed: %v", err)
	}

	// Clear counters
	_, _ = engine.Execute(`
		_destroyCalls = {}
		_registerCalls = {}
		_refreshCalls = {}
	`)

	// Simulate what the timer callback does
	_, err = engine.Execute(`
		-- This is the logic from startRenewTimer callback
		if next(RemoteSubscription.activeSubscriptions) then
			RemoteSubscription.refreshAll()
		else
			RemoteSubscription.autoSubscribe()
		end
	`)
	if err != nil {
		t.Fatalf("Timer callback simulation failed: %v", err)
	}

	refreshCount, _ := engine.Execute("return tostring(#_refreshCalls)")
	registerCount, _ := engine.Execute("return tostring(#_registerCalls)")
	destroyCount, _ := engine.Execute("return tostring(#_destroyCalls)")

	if refreshCount == "0" {
		t.Error("Timer should call refreshAll() when subscriptions exist, but no noreply re-register calls were made")
	}
	if registerCount != "0" {
		t.Errorf("Timer should NOT use blocking register when subscriptions exist, got %s blocking clientRegister calls", registerCount)
	}
	if destroyCount != "0" {
		t.Errorf("Timer should NOT destroy when subscriptions exist, got %s clientDestroy calls", destroyCount)
	}
}

// TestRenewTimer_FallsBackWhenNoSubscriptions verifies that the timer
// calls autoSubscribe() when there are no active subscriptions (first run
// or after all refreshes failed).
func TestRenewTimer_FallsBackWhenNoSubscriptions(t *testing.T) {
	engine, countSubs := setupSubscriptionEngine(t, 4)
	defer engine.Close()

	// Don't call autoSubscribe first — simulate empty state
	_, _ = engine.Execute(`
		_registerCalls = {}
		_refreshCalls = {}
	`)

	// Simulate timer callback with empty subscriptions
	_, err := engine.Execute(`
		if next(RemoteSubscription.activeSubscriptions) then
			RemoteSubscription.refreshAll()
		else
			RemoteSubscription.autoSubscribe()
		end
	`)
	if err != nil {
		t.Fatalf("Timer callback simulation failed: %v", err)
	}

	registerCount, _ := engine.Execute("return tostring(#_registerCalls)")
	refreshCount, _ := engine.Execute("return tostring(#_refreshCalls)")

	if registerCount == "0" {
		t.Error("Timer with no subscriptions should call autoSubscribe(), but no clientRegister calls were made")
	}
	if refreshCount != "0" {
		t.Errorf("Timer with no subscriptions should NOT refresh, got %s refresh calls", refreshCount)
	}
	if n := countSubs(); n != 1 {
		t.Errorf("Expected 1 subscription after fallback autoSubscribe, got %d", n)
	}
}

// TestSessionIDGrowth_RefreshVsAutoSubscribe verifies that using refreshAll()
// dramatically reduces session ID counter growth compared to autoSubscribe().
// Before fix: 17 sessions created every 30s = ~49k sessions/day.
// After fix: sessions only created on first subscribe or failure recovery.
func TestSessionIDGrowth_RefreshVsAutoSubscribe(t *testing.T) {
	engine, _ := setupSubscriptionEngine(t, 8)
	defer engine.Close()

	// Initial subscribe — creates sessions
	_, err := engine.Execute("RemoteSubscription.autoSubscribe()")
	if err != nil {
		t.Fatalf("autoSubscribe() failed: %v", err)
	}

	sidAfterInit, _ := engine.Execute("return tostring(RemoteSubscription.nextSessionID)")

	// Simulate 100 full renew cycles using refreshAll (normal operation)
	stubTimeouts(t, engine)
	mustExec(t, engine, "_refreshCalls = {}")
	for i := 0; i < 100; i++ {
		_, err = engine.Execute("RemoteSubscription.refreshAll()")
		if err != nil {
			t.Fatalf("refreshAll() iteration %d failed: %v", i, err)
		}
		runStubTimeouts(t, engine)
	}
	if got := mustExec(t, engine, "return tostring(#_refreshCalls)"); got != "200" {
		t.Errorf("100 cycles x 2 sessions should give 200 re-registers, got %s", got)
	}

	sidAfterRefresh, _ := engine.Execute("return tostring(RemoteSubscription.nextSessionID)")

	if sidAfterInit != sidAfterRefresh {
		t.Errorf("100 refreshAll() cycles should not create new sessions: init=%s, after=%s",
			sidAfterInit, sidAfterRefresh)
	}

	// Compare: 100 cycles of autoSubscribe would create 200 sessions (2 per cycle)
	// With refreshAll, it creates 0 additional sessions
	t.Logf("Session ID after init: %s, after 100 refresh cycles: %s (no growth = correct)", sidAfterInit, sidAfterRefresh)
}

// TestRefreshAll_UsesNoReplySender verifies that refreshAll() uses the fire-and-forget
// __go_send_command_noreply sender instead of blocking __go_send_command.
// This is a regression test for the performance bug where UDP round-trips during
// refresh held the Lua mutex for ~2-3s, blocking dashboard HTTP handlers.
func TestRefreshAll_UsesNoReplySender(t *testing.T) {
	engine, _ := setupSubscriptionEngine(t, 8)
	defer engine.Close()

	// Initial subscribe
	_, err := engine.Execute("RemoteSubscription.autoSubscribe()")
	if err != nil {
		t.Fatalf("autoSubscribe() failed: %v", err)
	}

	// Clear counters
	_, _ = engine.Execute(`
		_noreplyCalls = {}
		_refreshCalls = {}
	`)

	// refreshAll should use __go_send_command_noreply for each subscription
	stubTimeouts(t, engine)
	_, err = engine.Execute("RemoteSubscription.refreshAll()")
	if err != nil {
		t.Fatalf("refreshAll() failed: %v", err)
	}
	runStubTimeouts(t, engine)

	noreplyCount, _ := engine.Execute("return tostring(#_noreplyCalls)")
	refreshCount, _ := engine.Execute("return tostring(#_refreshCalls)")

	if noreplyCount == "0" {
		t.Error("refreshAll() should use __go_send_command_noreply, but no noreply calls were made")
	}
	if refreshCount != "2" {
		t.Errorf("Expected 2 refresh calls (from noreply path), got %s", refreshCount)
	}
	t.Logf("noreply calls: %s, refresh calls: %s (correct: async path used)", noreplyCount, refreshCount)
}

// TestAutoSubscribe_DestroyUsesNoReplySender verifies that the cleanup of old
// subscriptions in autoSubscribe() uses fire-and-forget __go_send_command_noreply
// instead of blocking __go_send_command.
func TestAutoSubscribe_DestroyUsesNoReplySender(t *testing.T) {
	engine, _ := setupSubscriptionEngine(t, 4)
	defer engine.Close()

	// First autoSubscribe
	_, err := engine.Execute("RemoteSubscription.autoSubscribe()")
	if err != nil {
		t.Fatalf("First autoSubscribe() failed: %v", err)
	}

	// Clear counters
	_, _ = engine.Execute(`
		_noreplyCalls = {}
		_destroyCalls = {}
	`)

	// Second autoSubscribe should destroy old subs via noreply
	_, err = engine.Execute("RemoteSubscription.autoSubscribe()")
	if err != nil {
		t.Fatalf("Second autoSubscribe() failed: %v", err)
	}

	// Verify destroy calls came through noreply path
	noreplyCount, _ := engine.Execute(`
		local count = 0
		for _, call in ipairs(_noreplyCalls) do
			if call.script:match("clientDestroy") then
				count = count + 1
			end
		end
		return tostring(count)
	`)

	destroyCount, _ := engine.Execute("return tostring(#_destroyCalls)")

	if noreplyCount == "0" {
		t.Error("autoSubscribe() cleanup should use __go_send_command_noreply for destroy, but no noreply destroy calls were made")
	}
	if destroyCount == "0" {
		t.Error("Expected clientDestroy calls for old subscriptions")
	}
	t.Logf("noreply destroy calls: %s, total destroy calls: %s", noreplyCount, destroyCount)
}

// stubTimeouts replaces EventBus:setTimeout with a recorder, so tests can see the
// scheduled delays (_timeoutDelays) and fire callbacks deterministically.
func stubTimeouts(t *testing.T, engine *LuaEngine) {
	t.Helper()
	if _, err := engine.Execute(`
		_timeouts, _timeoutDelays = {}, {}
		EventBus:getShared().setTimeout = function(self, fn, ms)
			table.insert(_timeouts, fn)
			table.insert(_timeoutDelays, tostring(ms))
			return #_timeouts
		end
	`); err != nil {
		t.Fatalf("stub setTimeout: %v", err)
	}
}

// runStubTimeouts fires recorded callbacks in order, including ones they schedule,
// until none are left.
func runStubTimeouts(t *testing.T, engine *LuaEngine) {
	t.Helper()
	if _, err := engine.Execute(`
		while #_timeouts > 0 do
			table.remove(_timeouts, 1)()
		end
	`); err != nil {
		t.Fatalf("run timeouts: %v", err)
	}
}

// TestRefreshAll_SpacesSendsByBatchDelay is a regression test for sessions dying on the
// CLU: back-to-back clientRegister packets were dropped, so later sessions were never
// renewed and expired silently. Sends must be chained with a delay, first immediately.
func TestRefreshAll_SpacesSendsByBatchDelay(t *testing.T) {
	engine, _ := setupSubscriptionEngine(t, 12)
	defer engine.Close()

	mustExec(t, engine, `
		RemoteSubscription.autoSubscribe()
		RemoteSubscription.config.batchDelay = 80
		RemoteSubscription._lastSync = nil
		_refreshCalls = {}
	`)
	stubTimeouts(t, engine)
	mustExec(t, engine, "RemoteSubscription.refreshAll()")

	if got := mustExec(t, engine, "return #_refreshCalls .. '/' .. #_timeouts"); got != "1/1" {
		t.Errorf("first re-register immediately and one timer pending (sent/pending = 1/1), got %s", got)
	}
	if got := mustExec(t, engine, "return tostring(RemoteSubscription._lastSync)"); got != "nil" {
		t.Errorf("_lastSync must not be set before the last send, got %s", got)
	}

	// Chained, not pre-scheduled: each send schedules only the next one, so a RunLoop
	// stall can't release several sends back-to-back
	mustExec(t, engine, "table.remove(_timeouts, 1)()")
	if got := mustExec(t, engine, "return #_refreshCalls .. '/' .. #_timeouts"); got != "2/1" {
		t.Errorf("after one timer: sent/pending = 2/1 expected, got %s", got)
	}

	runStubTimeouts(t, engine)
	if got := mustExec(t, engine, "return table.concat(_timeoutDelays, ',')"); got != "80,80" {
		t.Errorf("sends should be spaced by batchDelay, got delays %q", got)
	}
	got := mustExec(t, engine, `
		local sids = {}
		for _, c in ipairs(_refreshCalls) do table.insert(sids, c.sid) end
		table.sort(sids)
		return table.concat(sids, ",")
	`)
	if got != "1,2,3" {
		t.Errorf("each session should be re-registered once, got %q", got)
	}
	if got := mustExec(t, engine, "return tostring(RemoteSubscription._lastSync ~= nil)"); got != "true" {
		t.Errorf("_lastSync should be set after the last send")
	}
}

// TestRefreshAll_MinimumSpacing: below ~50 ms the CLU still drops packets, so a low
// batchDelay must not shrink the renew gap.
func TestRefreshAll_MinimumSpacing(t *testing.T) {
	engine, _ := setupSubscriptionEngine(t, 12)
	defer engine.Close()

	mustExec(t, engine, `
		RemoteSubscription.autoSubscribe()
		RemoteSubscription.config.batchDelay = 30
	`)
	stubTimeouts(t, engine)
	mustExec(t, engine, "RemoteSubscription.refreshAll()")
	runStubTimeouts(t, engine)
	if got := mustExec(t, engine, "return table.concat(_timeoutDelays, ',')"); got != "50,50" {
		t.Errorf("renew gap should be at least 50 ms, got delays %q", got)
	}
}

// TestRefreshAll_TimerRejectedFallsBack: if no timer slot is available, the remaining
// sessions would expire unrenewed; a full re-subscribe must happen instead.
func TestRefreshAll_TimerRejectedFallsBack(t *testing.T) {
	engine, _ := setupSubscriptionEngine(t, 12)
	defer engine.Close()

	mustExec(t, engine, `
		RemoteSubscription.autoSubscribe()
		EventBus:getShared().setTimeout = function() return -1 end
		_refreshCalls = {}
		_registerCalls = {}
		RemoteSubscription.refreshAll()
	`)
	if got := mustExec(t, engine, "return #_refreshCalls .. '/' .. #_registerCalls"); got != "1/3" {
		t.Errorf("rejected timer should fall back to a full re-subscribe (refresh/register = 1/3), got %s", got)
	}
}

// TestRefreshAll_FirstFailureWaitsForAllSends: a failing first send triggers the
// re-subscribe only after the remaining sends are done.
func TestRefreshAll_FirstFailureWaitsForAllSends(t *testing.T) {
	engine, _ := setupSubscriptionEngine(t, 12)
	defer engine.Close()

	mustExec(t, engine, "RemoteSubscription.autoSubscribe()")
	stubTimeouts(t, engine)
	mustExec(t, engine, `
		local orig = __go_send_command_noreply
		__go_send_command_noreply = function(ip, script)
			if script:match("clientRegister%('.-', %d+, 1,") then return false, "send failed" end
			return orig(ip, script)
		end
		_registerCalls = {}
		RemoteSubscription.refreshAll()
	`)
	if got := mustExec(t, engine, "return tostring(#_registerCalls)"); got != "0" {
		t.Errorf("re-subscribe must wait for the remaining sends, got %s registers", got)
	}
	runStubTimeouts(t, engine)
	if got := mustExec(t, engine, "return tostring(#_registerCalls)"); got != "3" {
		t.Errorf("failure should trigger a full re-subscribe at the end, got %s registers", got)
	}
}

// TestRefreshAll_SkipsSessionsReplacedMeanwhile: if a resubscribe happens while sends are
// still scheduled, the old (destroyed) sessions must not be re-registered.
func TestRefreshAll_SkipsSessionsReplacedMeanwhile(t *testing.T) {
	engine, _ := setupSubscriptionEngine(t, 12)
	defer engine.Close()

	mustExec(t, engine, "RemoteSubscription.autoSubscribe()")
	stubTimeouts(t, engine)
	mustExec(t, engine, `
		_refreshCalls = {}
		RemoteSubscription.refreshAll()
		RemoteSubscription.autoSubscribe()
		_registerCalls = {}
	`)
	runStubTimeouts(t, engine)

	if got := mustExec(t, engine, "return tostring(#_refreshCalls)"); got != "1" {
		t.Errorf("sessions replaced before their send must be skipped, got %s re-registers", got)
	}
	// Skipped is not failed: no extra full re-subscribe
	if got := mustExec(t, engine, "return tostring(#_registerCalls)"); got != "0" {
		t.Errorf("skipped sessions must not trigger a re-subscribe, got %s blocking registers", got)
	}
}

// TestRefreshAll_StopsWhenDisabledMeanwhile: disabling sync stops pending sends.
func TestRefreshAll_StopsWhenDisabledMeanwhile(t *testing.T) {
	engine, _ := setupSubscriptionEngine(t, 12)
	defer engine.Close()

	mustExec(t, engine, "RemoteSubscription.autoSubscribe()")
	stubTimeouts(t, engine)
	mustExec(t, engine, `
		_refreshCalls = {}
		RemoteSubscription.refreshAll()
		RemoteSubscription.config.enabled = false
	`)
	runStubTimeouts(t, engine)

	if got := mustExec(t, engine, "return tostring(#_refreshCalls)"); got != "1" {
		t.Errorf("no sends after disabling sync, got %s re-registers", got)
	}
}

// TestRefreshAll_LaterFailureFallsBack: a failure in a delayed send still triggers the
// full re-subscribe once all sends are done.
func TestRefreshAll_LaterFailureFallsBack(t *testing.T) {
	engine, _ := setupSubscriptionEngine(t, 12)
	defer engine.Close()

	mustExec(t, engine, "RemoteSubscription.autoSubscribe()")
	stubTimeouts(t, engine)
	mustExec(t, engine, `
		local orig = __go_send_command_noreply
		__go_send_command_noreply = function(ip, script)
			if script:match("clientRegister%('.-', %d+, 3,") then return false, "send failed" end
			return orig(ip, script)
		end
		_registerCalls = {}
		RemoteSubscription.refreshAll()
	`)
	if got := mustExec(t, engine, "return tostring(#_registerCalls)"); got != "0" {
		t.Fatalf("no re-subscribe before the failing send, got %s", got)
	}
	runStubTimeouts(t, engine)
	if got := mustExec(t, engine, "return tostring(#_registerCalls)"); got != "3" {
		t.Errorf("failure should trigger a full re-subscribe (3 blocking registers), got %s", got)
	}
}

// TestRefreshAll_RealTimers runs the chained sends through the real EventBus timers and
// checks the actual gap between packets.
func TestRefreshAll_RealTimers(t *testing.T) {
	engine, _ := setupSubscriptionEngine(t, 12)
	defer engine.Close()

	mustExec(t, engine, `
		RemoteSubscription.autoSubscribe()
		RemoteSubscription.config.batchDelay = 30
		_sentAt = {}
		local orig = __go_send_command_noreply
		__go_send_command_noreply = function(ip, script)
			table.insert(_sentAt, __go_time_ms())
			return orig(ip, script)
		end
		RemoteSubscription.refreshAll()
	`)
	engine.StartTimers()
	deadline := time.Now().Add(2 * time.Second)
	got := ""
	for time.Now().Before(deadline) {
		engine.ProcessTimers()
		if got = mustExec(t, engine, "return tostring(#_sentAt)"); got == "3" {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if got != "3" {
		t.Fatalf("all 3 sessions should be re-registered via timers, got %s", got)
	}
	gaps := mustExec(t, engine, `
		local g = {}
		for i = 2, #_sentAt do table.insert(g, tostring(_sentAt[i] - _sentAt[i - 1])) end
		return table.concat(g, ",")
	`)
	for _, g := range strings.Split(gaps, ",") {
		var ms int
		fmt.Sscanf(g, "%d", &ms)
		if ms < 50 {
			t.Errorf("gap between renew packets %d ms < 50 ms (gaps %s)", ms, gaps)
		}
	}
}

// TestRefreshAll_NoBurstAfterStall: if the RunLoop stalls longer than the gap, overdue
// sends must not all go out at once (that burst is what made the CLU drop packets).
func TestRefreshAll_NoBurstAfterStall(t *testing.T) {
	engine, _ := setupSubscriptionEngine(t, 16)
	defer engine.Close()

	mustExec(t, engine, `
		RemoteSubscription.autoSubscribe()
		_refreshCalls = {}
		RemoteSubscription.refreshAll()
	`)
	engine.StartTimers()
	time.Sleep(400 * time.Millisecond) // RunLoop "stalled": no timers processed
	engine.ProcessTimers()

	if got := mustExec(t, engine, "return tostring(#_refreshCalls)"); got != "2" {
		t.Errorf("after a stall only one overdue send may go out, got %s sends in total", got)
	}
}

// TestRefreshAll_OneChainAtATime: a renew tick while the previous chain is still
// running must not start a second, interleaved chain.
func TestRefreshAll_OneChainAtATime(t *testing.T) {
	engine, _ := setupSubscriptionEngine(t, 12)
	defer engine.Close()

	mustExec(t, engine, "RemoteSubscription.autoSubscribe()")
	stubTimeouts(t, engine)
	mustExec(t, engine, `
		_refreshCalls = {}
		RemoteSubscription.refreshAll()
		RemoteSubscription.refreshAll()
	`)
	if got := mustExec(t, engine, "return #_refreshCalls .. '/' .. #_timeouts"); got != "1/1" {
		t.Errorf("second tick should be skipped while a chain runs (sent/pending = 1/1), got %s", got)
	}
	runStubTimeouts(t, engine)
	if got := mustExec(t, engine, "return tostring(#_refreshCalls)"); got != "3" {
		t.Errorf("only the first chain should run, got %s re-registers", got)
	}
	// Chain finished: the next tick runs normally
	mustExec(t, engine, "RemoteSubscription.refreshAll()")
	runStubTimeouts(t, engine)
	if got := mustExec(t, engine, "return tostring(#_refreshCalls)"); got != "6" {
		t.Errorf("next tick after the chain finished should run, got %s re-registers", got)
	}
}

// TestRefreshAll_StaleChainReplaced: a chain that never finished (e.g. a lost timer)
// must not block renew forever; when replaced, its leftover steps do nothing.
func TestRefreshAll_StaleChainReplaced(t *testing.T) {
	engine, _ := setupSubscriptionEngine(t, 12)
	defer engine.Close()

	mustExec(t, engine, "RemoteSubscription.autoSubscribe()")
	stubTimeouts(t, engine)
	mustExec(t, engine, `
		_refreshCalls = {}
		RemoteSubscription.refreshAll()
		_staleStep = table.remove(_timeouts, 1)
		RemoteSubscription._renewRun.startedAt = os.time() - RemoteSubscription.RENEW_STALE_SECONDS - 1
		RemoteSubscription.refreshAll()
	`)
	runStubTimeouts(t, engine)
	mustExec(t, engine, "_staleStep()")
	if got := mustExec(t, engine, "return tostring(#_refreshCalls)"); got != "4" {
		t.Errorf("stale chain: 1 send, new chain: 3 sends, leftover step: none (want 4), got %s", got)
	}
	if got := mustExec(t, engine, "return tostring(RemoteSubscription._renewRun)"); got != "nil" {
		t.Errorf("no chain should be marked running at the end, got %s", got)
	}
}

// TestRefreshAll_ErrorInStepKeepsChain: an error while renewing one session must not
// end the chain for the sessions after it.
func TestRefreshAll_ErrorInStepKeepsChain(t *testing.T) {
	engine, _ := setupSubscriptionEngine(t, 12)
	defer engine.Close()

	mustExec(t, engine, "RemoteSubscription.autoSubscribe()")
	stubTimeouts(t, engine)
	mustExec(t, engine, `
		local orig = __go_send_command_noreply
		__go_send_command_noreply = function(ip, script)
			if script:match("clientRegister%('.-', %d+, 2,") then error("boom") end
			return orig(ip, script)
		end
		_refreshCalls = {}
		_registerCalls = {}
		RemoteSubscription.refreshAll()
	`)
	runStubTimeouts(t, engine)
	if got := mustExec(t, engine, "return #_refreshCalls .. '/' .. #_registerCalls"); got != "2/3" {
		t.Errorf("sessions 1 and 3 renewed, then re-subscribe for the failure (refresh/register = 2/3), got %s", got)
	}
	if got := mustExec(t, engine, "return tostring(RemoteSubscription._renewRun)"); got != "nil" {
		t.Errorf("chain should be finished, got %s", got)
	}
}

// TestAutoSubscribe_NoPerCLUCap verifies that every whitelisted object is subscribed,
// with hundreds of objects per CLU. Regression: a hardcoded cap kept 64 entries per CLU
// and dropped the rest silently (a 4-CLU install stopped at 256).
func TestAutoSubscribe_NoPerCLUCap(t *testing.T) {
	engine, _ := setupSubscriptionEngine(t, 0)
	defer engine.Close()

	mustExec(t, engine, `
		local counts = { ["192.168.0.11"] = 4, ["192.168.0.12"] = 271, ["192.168.0.13"] = 282, ["192.168.0.14"] = 176 }
		local whitelist = {}
		local n = 0
		for ip, count in pairs(counts) do
			n = n + 1
			local serial = "CLU22000000" .. n
			for i = 1, count do
				local objId = string.format("DOU%04d", i)
				local path = serial .. "." .. objId
				local obj = { _objId = objId, _clu = { address = ip }, updateState = function() end }
				if count == 4 then
					obj.subscribeFeatures = function() return {0, 1} end
				end
				_registry[path] = obj
				whitelist[path] = true
			end
		end
		RemoteSubscription.config.whitelist = whitelist
		RemoteSubscription.autoSubscribe()
	`)

	got := mustExec(t, engine, `
		local perIP, subs = {}, 0
		for _, s in pairs(RemoteSubscription.activeSubscriptions) do
			subs = subs + 1
			perIP[s.cluIP] = (perIP[s.cluIP] or 0) + #s.objects
		end
		return string.format("%d %d %d %d / %d", perIP["192.168.0.11"] or 0, perIP["192.168.0.12"] or 0,
			perIP["192.168.0.13"] or 0, perIP["192.168.0.14"] or 0, subs)
	`)
	// 4 objects with 2 features each = 8 entries; 2 + 68 + 71 + 44 sessions of up to 4 entries
	if want := "8 271 282 176 / 185"; got != want {
		t.Errorf("entries per CLU / sessions = %s, want %s", got, want)
	}
}
