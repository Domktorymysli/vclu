package lua

import (
	"strings"
	"testing"
	"time"
)

// Review of #206 (sandbox runtime and network): what a plugin still reaches or does.

// test: the user's PIN and remote settings on a plugin's exposed handle stay the user's; the
// plugin reads neither through the handle's fields nor through JSON of the handle.
func TestFableHandleFacadeHidesUserSettings(t *testing.T) {
	engine := sandboxEngine(t, "@t/h", `{api = 2}`)
	if _, e := runPlugin(t, engine, "@t/h", `
		p = Plugin:new("h", {name = "H"})
		state = { on = false }
		local sw = { get = function() return state.on end, set = function(_, v) state.on = v end,
			on = function() end }
		h = p:expose(sw, "switch", { id = "sw" })
	`); e != "nil" {
		t.Fatal(e)
	}
	// the user's module sets the PIN and the remote access on the real handle
	if _, err := engine.Execute(`
		local h = Plugin.getPlugin("@t/h"):getExposed()[1]
		h:remote({ pin = true }):pin("4711")
	`); err != nil {
		t.Fatal(err)
	}
	for _, code := range []string{
		`return tostring(h._pin)`,
		`return tostring(h._remote and h._remote.pin)`,
		`local s = {} for k, v in pairs(h) do s[#s + 1] = tostring(k) .. "=" .. tostring(v) end return table.concat(s, ",")`,
		`return tostring(JSON:encode(h))`,
		`return tostring(p:encodeJson({ h = h }))`,
		`local l = p:getExposed() return tostring(l[1]._pin) .. tostring(JSON:encode(l))`,
	} {
		r, e := runPlugin(t, engine, "@t/h", code)
		if strings.Contains(r, "4711") || strings.Contains(r, "pin") {
			t.Errorf("%s: %q (e=%s)", code, r, e)
		}
	}
	// the handle still works for the plugin
	if r, e := runPlugin(t, engine, "@t/h", `return h:getPath() .. "|" .. tostring(h:isExposed()) .. "|" .. JSON:encode(h):sub(1, 1)`); e != "nil" || r != "@t/h:sw|true|{" {
		t.Errorf("handle methods: %q %s", r, e)
	}
}

// test: an API 1 plugin's HTTP:new() requests run under the plugin's policy (its id as the
// instance), so vclu's own ports stay out of reach as with plugin:httpRequest; the client
// does not hand out the shared HTTP class.
func TestFableAPI1HTTPNewUsesPluginPolicy(t *testing.T) {
	panelURL, panelPort := localServer(t)
	otherURL, _ := localServer(t)
	engine := sandboxEngine(t, "@t/old", `{api = 1, lan = true}`)
	engine.httpManager.ownAddr = ownAddr
	engine.httpManager.SetOwnPorts(func() []int { return []int{panelPort} })
	r, e := runPlugin(t, engine, "@t/old", `
		local c = HTTP:new()
		local c2 = HttpClient:new("@t/other")
		c:setTimeout(3000)
		return c:getId() .. "|" .. c2:getId() .. "|" .. tostring(c.__index) .. "|" .. tostring(c._props) .. "|" .. tostring(c.setTimeout == c2.setTimeout)
	`)
	if e != "nil" || r != "@t/old|@t/old|nil|nil|false" {
		t.Fatalf("%q %s", r, e)
	}
	for _, id := range []string{"@t/old", "http_1", "@t/other"} {
		_, _, _, err := engine.httpManager.Request(id, "GET", panelURL, nil, "", 3000, true, true)
		refused := err != nil && strings.Contains(err.Error(), "an address of this vclu")
		if (id == "@t/old") != refused {
			t.Errorf("instance %q: %v", id, err)
		}
	}
	// a request from the plugin's client: another local port works, the panel port not
	r, e = runPlugin(t, engine, "@t/old", `
		local c = HTTP:new()
		local res = c:get("`+otherURL+`")
		local ok2, res2 = pcall(function() return c:get("`+panelURL+`") end)
		return tostring(res and res.status) .. "|" .. tostring(ok2 and res2.status or res2)
	`)
	if e != "nil" || !strings.HasPrefix(r, "200|") || strings.HasPrefix(r, "200|200") {
		t.Errorf("requests: %q %s", r, e)
	}
	for _, code := range []string{`return HTTP.getInstance("@t/old")`, `HTTP.new = nil`, `return HTTP._instances`} {
		if _, e := runPlugin(t, engine, "@t/old", code); e == "nil" {
			t.Errorf("%s: not refused", code)
		}
	}
}

// test: a function held by a module's object (in a list, or returned by a getter) does not
// leave the proxy raw through unpack or a read method.
func TestFableProxyKeepsFunctionsIn(t *testing.T) {
	engine := sandboxEngine(t, "@t/f", `{api = 2}`)
	if _, err := engine.Execute(`
		_registry["mod.obj"] = {
			fns = { function() return _G end },
			getFn = function(self) return self.fns[1] end,
			getPair = function(self) return 1, self.fns[1] end,
			getValue = function() return 21 end,
		}
	`); err != nil {
		t.Fatal(err)
	}
	for _, code := range []string{
		`local f = unpack(_registry["mod.obj"].fns) return tostring(f() == nil) .. type(f)`,
		`local f = _registry["mod.obj"]:getFn() return type(f) .. tostring(select("#", f()))`,
		`local _, f = _registry["mod.obj"]:getPair() return type(f) .. tostring(select("#", f()))`,
		`local f = _:get("mod.obj").fns[1] return type(f) .. tostring(select("#", f()))`,
	} {
		r, e := runPlugin(t, engine, "@t/f", code)
		if e == "nil" {
			t.Errorf("%s: a module's function ran raw: %q", code, r)
		}
	}
	if r, e := runPlugin(t, engine, "@t/f", `local a, f = _registry["mod.obj"]:getPair() return tostring(a) .. type(f) .. tostring(_:get("mod.obj"):getValue())`); e != "nil" || r != "1function21" {
		t.Errorf("reads: %q %s", r, e)
	}
}

// test: an API 1 plugin's MQTT global does not take over a module's topic, what it subscribed
// goes with the plugin, and the shared client itself stays out of reach.
func TestFableAPI1MQTTGlobalIsTracked(t *testing.T) {
	engine := sandboxEngine(t, "@t/m1", `{api = 1, mqtt = true}`)
	if _, err := engine.Execute(`
		__got = { n = 0 }
		MQTT:subscribe("dom/x", function() __got.n = __got.n + 1 end)
	`); err != nil {
		t.Fatal(err)
	}
	_, e := runPlugin(t, engine, "@t/m1", `MQTT:subscribe("dom/x", function() end)`)
	if e == "nil" {
		t.Error("a module's topic taken over through the API 1 MQTT global")
	}
	if r, e := runPlugin(t, engine, "@t/m1", `
		MQTT:subscribe("dom/y", function() __plugin_hit = true end)
		MQTT:subscribe("dom/z", function() end)
		MQTT:publish("dom/out", "1")
		return tostring(MQTT:unsubscribe("dom/x")) .. tostring(MQTT:unsubscribe("dom/z")) .. tostring(MQTT:isConnected())
	`); e != "nil" || r != "falsetruefalse" {
		t.Errorf("own topics: %q %s", r, e)
	}
	for _, code := range []string{`return MQTT:new("x")`, `return MQTT.getInstance("mqtt")`, `MQTT:setCredentials("a", "b")`, `return MQTT._subscriptions`} {
		if _, e := runPlugin(t, engine, "@t/m1", code); e == "nil" {
			t.Errorf("%s: not refused", code)
		}
	}
	if _, err := engine.Execute(`
		PluginSandbox:cleanup("@t/m1")
		MQTT:_dispatchMessage("dom/x", "1")
		MQTT:_dispatchMessage("dom/y", "1")
	`); err != nil {
		t.Fatal(err)
	}
	mustLua(t, engine, `__got.n`, "1")
	mustLua(t, engine, `tostring(MQTT._subscriptions["dom/y"])`, "nil")
}

// test: a pattern that backtracks for a second in one string.find is one Go call, which the
// budget cannot interrupt; its time is still the plugin's, so the callback stops right after
// it and the plugin counts an overrun, as a method of a string or from its string library.
func TestFableBlockingGoCallIsCharged(t *testing.T) {
	engine := budgetEngine(t, "@t/pm")
	if _, e := runPlugin(t, engine, "@t/pm", `
		local p = Plugin:new("pm")
		p.after, p.done = 0, 0
		p:setTimeout(10, function()
			string.find(string.rep("a", 50), "(.-)(.-)(.-)(.-)(.-)b")
			p.done = p.done + 1
		end)
		p:setTimeout(20, function()
			local s = string.rep("a", 50)
			s:find("(.-)(.-)(.-)(.-)(.-)b")
			p.done = p.done + 1
		end)
		p:setTimeout(30, function() p.after = p.after + 1 end)
	`); e != "nil" {
		t.Fatal(e)
	}
	start := time.Now()
	waitPlugin(t, engine, `Plugin._lookup("@t/pm").after`, "1")
	t.Logf("two blocking calls took %v", time.Since(start))
	mustLua(t, engine, `Plugin._lookup("@t/pm").done`, "0")
	if u := engine.PluginUsage()["@t/pm"]; u.Overruns != 2 || u.CPU < 600*time.Millisecond {
		t.Errorf("usage %+v", u)
	}
	// a module's call through the same timed method is not a plugin's time
	if got, err := engine.Execute(`return (("a"):rep(30)):find("(.-)(.-)(.-)(.-)b") == nil and "ok" or "?"`); err != nil || got != "ok" {
		t.Errorf("module: %q %v", got, err)
	}
	if u := engine.PluginUsage()["@t/pm"]; u.Overruns != 2 {
		t.Errorf("a module's time charged to the plugin: %+v", u)
	}
}

// test: a gsub whose Lua callback does the work is charged once, not as a sample and as a
// Go call both: a plugin within its budget stays within it.
func TestFableTimedCallNotChargedTwice(t *testing.T) {
	engine := budgetEngine(t, "@t/tw")
	if _, e := runPlugin(t, engine, "@t/tw", `
		local p = Plugin:new("tw")
		p.r = ""
		p:setTimeout(10, function()
			local t0 = os.clock()
			-- about 0.2 s of Lua inside one gsub: under the 0.3 s budget
			local s = string.rep("x", 200)
			s:gsub(".", function() local u = os.clock() + 0.001 while os.clock() < u do end return "" end)
			p.r = (os.clock() - t0 < 0.3) and "ok" or "slow"
		end)
	`); e != "nil" {
		t.Fatal(e)
	}
	waitPlugin(t, engine, `Plugin._lookup("@t/tw").r`, "ok")
	if u := engine.PluginUsage()["@t/tw"]; u.Overruns != 0 {
		t.Errorf("usage %+v", u)
	}
}
