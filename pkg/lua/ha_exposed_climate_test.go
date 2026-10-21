package lua

import (
	"encoding/json"
	"sort"
	"strings"
	"testing"
)

// A thermostat exposed as climate is a climate entity in Home Assistant: current and target
// temperature and the mode (off/heat) are published, a target from HA reaches setSetpoint
// clamped to min/max, and discovery announces only the topics that are published or taken (#61).

const haClimateSetup = `
	_pub = {}
	_subs = {}
	HAStatePublisher.setup({
		publish = function(self, topic, payload) table.insert(_pub, { topic = topic, payload = payload }); return true end,
		subscribe = function(self, pattern) table.insert(_subs, pattern) end, unsubscribe = function() end,
	})
	HACommands.setup({
		subscribe = function(self, pattern) table.insert(_subs, pattern) end,
	})
	_calls = {}
	local clu = {
		address = "192.168.0.3",
		execute = function(s, i, cmd) table.insert(_calls, cmd) end,
		executeNoReply = function(s, i, cmd) table.insert(_calls, cmd) end,
	}
	TH = RemoteThermostat:new(clu, "THE1", "aSalon")
	registerObject("CLU1.THE1", TH)
	RemoteSubscription.activeSubscriptions = {}
	local objs = {}
	for _, f in ipairs(TH:subscribeFeatures()) do objs[#objs + 1] = { objId = "THE1", featureIndex = f } end
	RemoteSubscription.activeSubscriptions["192.168.0.3:5"] = { cluSerial = "CLU1", objects = objs }
	-- a report as the CLU sends it: State, Mode, PointValue, TargetTemp, CurrentTemp
	function report(state, mode, point, target, current)
		RemoteSubscription.updateRegistry("192.168.0.3", 5, { state, mode, point, target, current })
	end
	function _last(topic)
		local v
		for _, p in ipairs(_pub) do if p.topic == topic then v = p.payload end end
		return tostring(v)
	end
	function _count(topic)
		local n = 0
		for _, p in ipairs(_pub) do if p.topic == topic then n = n + 1 end end
		return n
	end
	BASE = "vclu/exposed/vclu/climate/clu1_the1"
`

// climateDiscovery decodes the climate config published for the thermostat.
func climateDiscovery(t *testing.T, engine *LuaEngine) map[string]any {
	t.Helper()
	payload := mustExec(t, engine, `(function()
		local found = ""
		for _, p in ipairs(_pub) do
			if p.topic == "homeassistant/climate/vclu_clu1_the1/config" and p.payload ~= "" then found = p.payload end
		end
		return found
	end)()`)
	if payload == "" {
		t.Fatal("no climate discovery published")
	}
	var d map[string]any
	if err := json.Unmarshal([]byte(payload), &d); err != nil {
		t.Fatalf("discovery is not JSON: %v\n%s", err, payload)
	}
	return d
}

// topicKeys lists the keys of a discovery payload that name a topic.
func topicKeys(d map[string]any) []string {
	var keys []string
	for k := range d {
		if strings.HasSuffix(k, "_topic") {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	return keys
}

func TestHAClimateDiscovery(t *testing.T) {
	engine := NewLuaEngine()
	defer engine.Close()
	if _, err := engine.Execute(haClimateSetup); err != nil {
		t.Fatalf("setup failed: %v", err)
	}
	if got := mustExec(t, engine, `return TH:expose({ name = "Salon", area = "Salon", min = 15, max = 25 }).type`); got != "climate" {
		t.Fatalf("th:expose() type = %s, want climate", got)
	}

	d := climateDiscovery(t, engine)
	want := []string{"availability_topic", "current_temperature_topic", "mode_state_topic",
		"temperature_command_topic", "temperature_state_topic"}
	if got := topicKeys(d); strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("announced topics = %v, want %v", got, want)
	}
	for k, v := range map[string]string{
		"current_temperature_topic": "vclu/exposed/vclu/climate/clu1_the1/current_temp",
		"temperature_state_topic":   "vclu/exposed/vclu/climate/clu1_the1/target_temp",
		"temperature_command_topic": "vclu/exposed/vclu/climate/clu1_the1/target_temp/set",
		"mode_state_topic":          "vclu/exposed/vclu/climate/clu1_the1/mode",
		"temperature_unit":          "C",
	} {
		if d[k] != v {
			t.Errorf("%s = %v, want %s", k, d[k], v)
		}
	}
	if d["min_temp"] != 15.0 || d["max_temp"] != 25.0 {
		t.Errorf("min_temp/max_temp = %v/%v, want 15/25", d["min_temp"], d["max_temp"])
	}
	if modes, _ := json.Marshal(d["modes"]); string(modes) != `["off","heat"]` {
		t.Errorf("modes = %s, want [\"off\",\"heat\"]", modes)
	}
	if dev, _ := d["device"].(map[string]any); dev == nil || dev["suggested_area"] != "Salon" {
		t.Errorf("device = %v, want suggested_area Salon", d["device"])
	}

	// every announced state topic gets published after a report, and the command topic is taken
	got := mustExec(t, engine, `(function()
		_pub = {}
		report(1, 0, 21, 21, 20.4)
		local out = {}
		for _, s in ipairs({ "current_temp", "target_temp", "mode" }) do
			table.insert(out, _count(BASE .. "/" .. s) > 0 and "y" or "n")
		end
		local taken = false
		for _, p in ipairs(_subs) do if p == "vclu/exposed/+/+/+/target_temp/set" then taken = true end end
		table.insert(out, tostring(taken))
		return table.concat(out, ",")
	end)()`)
	if got != "y,y,y,true" {
		t.Errorf("published current/target/mode, command subscribed = %s, want y,y,y,true", got)
	}
}

func TestHAClimatePublishes(t *testing.T) {
	engine := NewLuaEngine()
	defer engine.Close()
	if _, err := engine.Execute(haClimateSetup); err != nil {
		t.Fatalf("setup failed: %v", err)
	}
	mustExec(t, engine, `expose(TH, "climate", { name = "Salon" })`)

	// before any report nothing is published (HA rejects "nil" for a temperature)
	if got := mustExec(t, engine, `return _count(BASE .. "/current_temp") .. _count(BASE .. "/target_temp") .. _count(BASE .. "/mode")`); got != "000" {
		t.Errorf("published before a report: %s, want 000", got)
	}

	got := mustExec(t, engine, `(function()
		local out = {}
		local function snap()
			table.insert(out, _last(BASE .. "/current_temp") .. "/" .. _last(BASE .. "/target_temp") .. "/" .. _last(BASE .. "/mode"))
		end
		report(1, 0, 21, 21, 20.4); snap()
		-- the target alone (holiday mode): the value is the same
		_pub = {}
		report(1, 1, 21, 17, 20.4); snap()
		-- stopped
		_pub = {}
		report(0, 1, 21, 17, 20.4); snap()
		-- the temperature alone
		_pub = {}
		report(0, 1, 21, 17, 20.9); snap()
		return table.concat(out, ",")
	end)()`)
	if got != "20.4/21/heat,20.4/17/heat,20.4/17/off,20.9/17/off" {
		t.Errorf("published current/target/mode = %s", got)
	}

	// a report that changes nothing publishes nothing new
	if n := mustExec(t, engine, `(function() _pub = {}; report(0, 1, 21, 17, 20.9); return #_pub end)()`); n != "0" {
		t.Errorf("an unchanged report published %s messages, want 0", n)
	}

	// unexposed: a change of the target publishes nothing
	if n := mustExec(t, engine, `(function()
		ExposedObjects:get("CLU1.THE1"):unexpose()
		_pub = {}
		report(1, 0, 22, 22, 21)
		return _count(BASE .. "/target_temp")
	end)()`); n != "0" {
		t.Errorf("unexposed thermostat published its target %s times, want 0", n)
	}
}

func TestHAClimateTargetCommand(t *testing.T) {
	engine := NewLuaEngine()
	defer engine.Close()
	if _, err := engine.Execute(haClimateSetup); err != nil {
		t.Fatalf("setup failed: %v", err)
	}
	mustExec(t, engine, `expose(TH, "climate", { name = "Salon", min = 15, max = 25 })`)

	// the target goes to SetPointValue (set 3), clamped to min/max; nothing writes feature 0
	got := mustExec(t, engine, `(function()
		local out = {}
		for _, p in ipairs({ "21.5", "30", "3", "20" }) do
			local ok, err = HACommands.handleExposedMessage(BASE .. "/target_temp/set", p)
			table.insert(out, tostring(ok) .. (err and (":" .. err) or ""))
		end
		return table.concat(out, ",") .. "|" .. table.concat(_calls, ";") .. "|" .. tostring(TH:getSetpoint())
	end)()`)
	want := "true,true,true,true|THE1:set(3, 21.5);THE1:set(3, 25);THE1:set(3, 15);THE1:set(3, 20)|20"
	if got != want {
		t.Errorf("target commands\n got: %s\nwant: %s", got, want)
	}

	// refused: not a number, a mode, the plain command topic; nothing is sent
	refused := mustExec(t, engine, `(function()
		_calls = {}
		local out = {}
		for _, c in ipairs({
			{ BASE .. "/target_temp/set", "abc" },
			{ BASE .. "/target_temp/set", "nan" },
			{ BASE .. "/mode/set", "off" },
			{ BASE .. "/set", "21" },
		}) do
			local ok, err = HACommands.handleExposedMessage(c[1], c[2])
			table.insert(out, tostring(ok) .. ":" .. tostring(err))
		end
		return table.concat(out, ",") .. "|" .. #_calls
	end)()`)
	want = "false:invalid temperature value,false:invalid temperature value,false:climate takes only target_temp," +
		"false:climate takes only target_temp|0"
	if refused != want {
		t.Errorf("refused commands\n got: %s\nwant: %s", refused, want)
	}

	// without min/max the type's range (5..35) holds
	if got := mustExec(t, engine, `(function()
		ExposedObjects:get("CLU1.THE1"):unexpose()
		expose(TH, "climate", { name = "Salon" })
		_calls = {}
		HACommands.handleExposedMessage(BASE .. "/target_temp/set", "50")
		HACommands.handleExposedMessage(BASE .. "/target_temp/set", "-4")
		return table.concat(_calls, ";")
	end)()`); got != "THE1:set(3, 35);THE1:set(3, 5)" {
		t.Errorf("default clamp = %s, want THE1:set(3, 35);THE1:set(3, 5)", got)
	}
}

// A thermostat is never a switch: exposeRegistry() leaves it out (feature 0 is its sensor
// binding), expose() without a type makes it a climate.
func TestHAClimateRegistryNotSwitch(t *testing.T) {
	engine := NewLuaEngine()
	defer engine.Close()
	if _, err := engine.Execute(haClimateSetup); err != nil {
		t.Fatalf("setup failed: %v", err)
	}
	got := mustExec(t, engine, `(function()
		local clu = { execute = function() end, executeNoReply = function() end }
		registerObject("CLU1.DOU1", RemoteSwitch:new(clu, "DOU1", "lampa"))
		exposeRegistry()
		local th, dou = ExposedObjects:get("CLU1.THE1"), ExposedObjects:get("CLU1.DOU1")
		local configs = 0
		for _, p in ipairs(_pub) do
			if p.topic:find("clu1_the1/config", 1, true) and p.payload ~= "" then configs = configs + 1 end
		end
		local out = tostring(th and th.type) .. "|" .. tostring(dou and dou.type) .. "|" .. configs
		out = out .. "|" .. TH:expose().type
		exposeRegistry()
		return out .. "|" .. ExposedObjects:get("CLU1.THE1").type
	end)()`)
	if got != "nil|switch|0|climate|climate" {
		t.Errorf("registry/expose types = %s, want nil|switch|0|climate|climate", got)
	}
}

// Exposing a thermostat must not talk to its CLU nor grow what is subscribed: on an old CLU
// a thermostat subscription can reset it (#67), so the features stay those of #44.
func TestHAClimateExposeSubscribesNothingNew(t *testing.T) {
	engine := NewLuaEngine()
	defer engine.Close()
	if _, err := engine.Execute(haClimateSetup); err != nil {
		t.Fatalf("setup failed: %v", err)
	}
	got := mustExec(t, engine, `(function()
		RemoteSubscription.config.whitelist = { ["CLU1.DOU9"] = true }
		local before = table.concat(TH:subscribeFeatures(), ",")
		exposeRegistry()
		expose(TH, "climate", { name = "Salon" })
		HAStatePublisher.refresh()
		report(1, 0, 21, 21, 20.4)
		local wl = {}
		for p in pairs(RemoteSubscription.config.whitelist) do table.insert(wl, p) end
		local v1 = RemoteThermostat:new(TH._clu, "THE2", "old")
		v1._version = 1
		return before .. "|" .. table.concat(TH:subscribeFeatures(), ",") .. "|" .. table.concat(v1:subscribeFeatures(), ",") ..
			"|" .. table.concat(wl, ",") .. "|" .. #_calls
	end)()`)
	if got != "6,8,3,12,14|6,8,3,12,14|6,8,3,12|CLU1.DOU9|0" {
		t.Errorf("features before|after|v1|whitelist|CLU calls = %s, want 6,8,3,12,14|6,8,3,12,14|6,8,3,12|CLU1.DOU9|0", got)
	}
}
