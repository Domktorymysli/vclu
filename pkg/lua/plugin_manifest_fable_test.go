package lua

import (
	"strings"
	"testing"
)

// An action plugin.json declares but the user has not allowed is not listed and does not
// run, confirmed or not; the plugin cannot allow it from code.
func TestFableActionNotAllowedStaysOff(t *testing.T) {
	engine := sandboxEngine(t, "@t/app", `{api = 2, actions = {
		later = {allowed = false, confirm = true, description = "x"},
		now = {allowed = true, description = "y"}}}`)
	r, e := runPlugin(t, engine, "@t/app", `
		local p = Plugin:new("app", {})
		p:action("later", function() return "ran" end)
		p:action("now", function() return "ok" end)
		return "registered"`)
	if r != "registered" || e != "nil" {
		t.Fatalf("%s (%s)", r, e)
	}
	for _, confirmed := range []string{"false", "true"} {
		got := luaOut(t, engine, `PluginSandbox:callAction("@t/app", "later", "{}", `+confirmed+`)`)
		if !strings.Contains(got, `"code":"not_found"`) || strings.Contains(got, "ran") {
			t.Errorf("confirmed=%s: %s", confirmed, got)
		}
	}
	if got := luaOut(t, engine, `PluginSandbox:listActions("@t/app")`); strings.Contains(got, "later") || !strings.Contains(got, `"name":"now"`) {
		t.Errorf("list: %s", got)
	}
	// the sandbox's tables are out of the plugin's reach
	r, e = runPlugin(t, engine, "@t/app", `return tostring(PluginSandbox)`)
	if e != "nil" || r != "nil" {
		t.Errorf("PluginSandbox visible: %s (%s)", r, e)
	}
}

// plugin:has answers only for what this vclu reads; nothing private, never an error.
func TestFablePluginHas(t *testing.T) {
	engine := sandboxEngine(t, "@t/has", `{api = 2}`)
	r, e := runPlugin(t, engine, "@t/has", `
		local p = Plugin:new("has", {})
		local out = {}
		for _, n in ipairs({"permission:lan.ports", "permission:webhooks", "permission:lan.hosts", "websocket", "has",
			"_lookup", "form:host", "form:regex", "PERMISSION:LAN", "nope"}) do
			out[#out + 1] = n .. "=" .. tostring(p:has(n))
		end
		out[#out + 1] = "nil=" .. tostring(p:has(nil))
		out[#out + 1] = "table=" .. tostring(p:has({}))
		return table.concat(out, " ")`)
	want := "permission:lan.ports=true permission:webhooks=false permission:lan.hosts=false websocket=true has=true " +
		"_lookup=false form:host=true form:regex=false PERMISSION:LAN=false nope=false nil=false table=false"
	if e != "nil" || r != want {
		t.Errorf("%s (%s)", r, e)
	}
}

// Topic filters at the edges: "#" and "$SYS/#" are not within "z2m/#", an empty topic is
// refused, an empty level counts as a level.
func TestFableMQTTTopicEdges(t *testing.T) {
	engine := sandboxEngine(t, "@t/mq", `{api = 2, mqtt = true, limits = {mqttSubscribe = {"z2m/#"}, mqttPublish = {"z2m/+/set"}}}`)
	if _, err := engine.Execute(`MQTT = {subscribe = function() end, publish = function() end}`); err != nil {
		t.Fatal(err)
	}
	r, e := runPlugin(t, engine, "@t/mq", `
		local p = Plugin:new("mq", {})
		local out = {}
		for _, topic in ipairs({"z2m/#", "z2m", "z2m/", "z2m//x", "#", "$SYS/#", "", "z2m/+", "+/#", "z2mx/#"}) do
			out[#out + 1] = topic .. "=" .. tostring((pcall(p.mqttSubscribe, p, topic, function() end)))
		end
		for _, topic in ipairs({"z2m/lamp/set", "z2m//set", "z2m/lamp/get", "z2m/+/set", "z2m/a/b/set"}) do
			out[#out + 1] = "pub:" .. topic .. "=" .. tostring((pcall(p.mqttPublish, p, topic, "1")))
		end
		return table.concat(out, " ")`)
	want := "z2m/#=true z2m=true z2m/=true z2m//x=true #=false $SYS/#=false =false z2m/+=true +/#=false z2mx/#=false " +
		"pub:z2m/lamp/set=true pub:z2m//set=true pub:z2m/lamp/get=false pub:z2m/+/set=true pub:z2m/a/b/set=false"
	if e != "nil" || r != want {
		t.Errorf("%s (%s)", r, e)
	}
}
