package lua

import "testing"

// A target sent to the topic of another type is refused: target_temp/set was not subscribed
// before climate, a switch or a number must not take it as its own command (#61).
func TestHAClimateTargetTopicOtherTypes(t *testing.T) {
	engine := NewLuaEngine()
	defer engine.Close()
	if _, err := engine.Execute(haClimateSetup); err != nil {
		t.Fatalf("setup failed: %v", err)
	}
	got := mustExec(t, engine, `(function()
		local clu = { address = "192.168.0.3",
			execute = function(s, i, cmd) table.insert(_calls, cmd) end,
			executeNoReply = function(s, i, cmd) table.insert(_calls, cmd) end }
		registerObject("CLU1.DOU1", RemoteSwitch:new(clu, "DOU1", "sw"))
		registerObject("CLU1.NUM1", RemoteNumber:new(clu, "NUM1", "num"))
		exposeRegistry()
		_calls = {}
		local out = {}
		for _, c in ipairs({
			{ "vclu/exposed/vclu/switch/clu1_dou1/target_temp/set", "ON" },
			{ "vclu/exposed/vclu/number/clu1_num1/target_temp/set", "21" },
		}) do
			local ok = HACommands.handleExposedMessage(c[1], c[2])
			table.insert(out, tostring(ok))
		end
		return table.concat(out, ",") .. "|" .. #_calls
	end)()`)
	if got != "false,false|0" {
		t.Errorf("target_temp/set on a switch and a number: ok|CLU calls = %s, want false,false|0", got)
	}
}

// Re-exposing and refreshing a climate keeps one CHANGE_EVENTS hook: a change of the target
// publishes it once, and the user's own OnSettingsChange handler still runs.
func TestHAClimateHookOnce(t *testing.T) {
	engine := NewLuaEngine()
	defer engine.Close()
	if _, err := engine.Execute(haClimateSetup); err != nil {
		t.Fatalf("setup failed: %v", err)
	}
	got := mustExec(t, engine, `(function()
		local seen = { n = 0 }
		TH:onEvent("OnSettingsChange", function() seen.n = seen.n + 1 end)
		report(1, 0, 21, 21, 20.4)
		for _ = 1, 3 do
			ExposedObjects:get("CLU1.THE1")
			TH:unexpose()
			expose(TH, "climate", { name = "Salon" })
			HAStatePublisher.refresh()
		end
		_pub = {}
		seen.n = 0
		report(1, 0, 21, 22, 20.4)
		return _count(BASE .. "/target_temp") .. "|" .. seen.n .. "|" .. _last(BASE .. "/target_temp")
	end)()`)
	if got != "1|1|22" {
		t.Errorf("target publishes|user handler calls|target = %s, want 1|1|22", got)
	}
}

// A hidden climate (AccessControl) publishes nothing and takes no target.
func TestHAClimateHidden(t *testing.T) {
	engine := NewLuaEngine()
	defer engine.Close()
	if _, err := engine.Execute(haClimateSetup); err != nil {
		t.Fatalf("setup failed: %v", err)
	}
	got := mustExec(t, engine, `(function()
		AccessControl.set("mqtt", "CLU1.THE1", "hidden")
		expose(TH, "climate", { name = "Salon" })
		_pub = {}
		report(1, 0, 21, 21, 20.4)
		local ok = HACommands.handleExposedMessage(BASE .. "/target_temp/set", "24")
		local n = 0
		for _, p in ipairs(_pub) do if p.topic:find("clu1_the1", 1, true) and p.payload ~= "" then n = n + 1 end end
		return n .. "|" .. tostring(ok) .. "|" .. #_calls
	end)()`)
	if got != "0|false|0" {
		t.Errorf("hidden climate: publishes|command ok|CLU calls = %s, want 0|false|0", got)
	}
}

// Payloads HA never sends but a broker can: refused or clamped, never written raw.
func TestHAClimateOddPayloads(t *testing.T) {
	engine := NewLuaEngine()
	defer engine.Close()
	if _, err := engine.Execute(haClimateSetup); err != nil {
		t.Fatalf("setup failed: %v", err)
	}
	got := mustExec(t, engine, `(function()
		expose(TH, "climate", { name = "Salon" })
		local out = {}
		for _, p in ipairs({ "21,5", "21.5 C", "", "inf", "-inf", "1e999", "nan", "99", " 22 " }) do
			_calls = {}
			local ok = HACommands.handleExposedMessage(BASE .. "/target_temp/set", p)
			table.insert(out, tostring(ok) .. ":" .. table.concat(_calls, ";"))
		end
		return table.concat(out, ",")
	end)()`)
	want := "false:,false:,false:,false:,false:,false:,false:,true:THE1:set(3, 35),true:THE1:set(3, 22)"
	if got != want {
		t.Errorf("odd payloads\n got: %s\nwant: %s", got, want)
	}
}

// Exposing a thermostat sends nothing to a CLU by the Go sender either (a clientRegister goes
// through it, not clu:execute) and leaves the session's objects as they are (#67).
func TestHAClimateExposeSendsNoRegister(t *testing.T) {
	engine := NewLuaEngine()
	defer engine.Close()
	if _, err := engine.Execute(haClimateSetup); err != nil {
		t.Fatalf("setup failed: %v", err)
	}
	got := mustExec(t, engine, `(function()
		local sent = {}
		__go_send_command_noreply = function(ip, script) table.insert(sent, script); return true end
		__go_send_command = function(ip, script) table.insert(sent, script); return "" end
		local sub = RemoteSubscription.activeSubscriptions["192.168.0.3:5"]
		local before = #sub.objects
		exposeRegistry()
		expose(TH, "climate", { name = "Salon" })
		HAStatePublisher.refresh()
		report(1, 0, 21, 21, 20.4)
		HACommands.handleExposedMessage(BASE .. "/target_temp/set", "22")
		local n = 0
		for _ in pairs(RemoteSubscription.activeSubscriptions) do n = n + 1 end
		return #sent .. "|" .. before .. "|" .. #sub.objects .. "|" .. n .. "|" .. table.concat(_calls, ";")
	end)()`)
	if got != "0|5|5|1|THE1:set(3, 22)" {
		t.Errorf("sent|objects before|after|sessions|CLU calls = %s, want 0|5|5|1|THE1:set(3, 22)", got)
	}
}

// Bounds given as strings are numbers in discovery, as in the clamp.
func TestHAClimateDiscoveryNumericBounds(t *testing.T) {
	engine := NewLuaEngine()
	defer engine.Close()
	if _, err := engine.Execute(haClimateSetup); err != nil {
		t.Fatalf("setup failed: %v", err)
	}
	mustExec(t, engine, `return TH:expose({ name = "Salon", min = "16", max = "24", step = "0.5" }).type`)
	d := climateDiscovery(t, engine)
	if d["min_temp"] != 16.0 || d["max_temp"] != 24.0 || d["temp_step"] != 0.5 {
		t.Errorf("min/max/step = %#v/%#v/%#v, want numbers 16/24/0.5", d["min_temp"], d["max_temp"], d["temp_step"])
	}
}
