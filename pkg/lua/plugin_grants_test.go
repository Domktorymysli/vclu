package lua

import (
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
	"time"
)

// A plugin narrowed to some local hosts reaches those and no other local address.
func TestHTTPManagerLANHosts(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("ok"))
	}))
	defer srv.Close()

	hm := NewHTTPManager()
	hm.SetLANAllowed("@t/a", true)
	for _, c := range []struct {
		hosts []netip.Prefix
		ok    bool
	}{
		{[]netip.Prefix{netip.MustParsePrefix("127.0.0.1/32")}, true},
		{[]netip.Prefix{netip.MustParsePrefix("127.0.0.0/8")}, true},
		{[]netip.Prefix{netip.MustParsePrefix("192.168.1.20/32")}, false},
		{[]netip.Prefix{}, false}, // an empty list: no local address
		{nil, true},               // no list: any
	} {
		hm.SetLANHosts("@t/a", c.hosts)
		_, _, _, err := hm.Request("@t/a", "GET", srv.URL, nil, "", 5000, true, true)
		if c.ok && err != nil {
			t.Errorf("hosts %v: %v", c.hosts, err)
		}
		if !c.ok && (err == nil || !strings.Contains(err.Error(), "not among the hosts")) {
			t.Errorf("hosts %v: want refusal, got %v", c.hosts, err)
		}
	}
}

// The hosts from the plugin's grants, end to end through its HTTP API.
func TestPluginSandboxLANHosts(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("hi")) }))
	defer ts.Close()
	engine := sandboxEngine(t, "@t/here", `{lan = true, limits = {lanHosts = {"127.0.0.1"}}}`)
	engine.StartTimers()
	defer engine.StopTimers()
	if _, err := engine.Execute(`PluginSandbox:create("@t/elsewhere", {lan = true, limits = {lanHosts = {"192.168.1.0/24"}}})`); err != nil {
		t.Fatal(err)
	}
	ids := []string{"@t/here", "@t/elsewhere"}
	for _, id := range ids {
		if _, e := runPlugin(t, engine, id, `
			local p = Plugin:new("x")
			p:httpGet("`+ts.URL+`", function(resp, err)
				p.result = tostring(err) .. "|" .. tostring(resp and resp.status)
			end)
		`); e != "nil" {
			t.Fatalf("%s: %s", id, e)
		}
	}
	results := map[string]string{}
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline) && len(results) < 2; {
		pumpTimers(engine, 50*time.Millisecond)
		for _, id := range ids {
			if v, _ := engine.Execute(`tostring(Plugin.getPlugin("` + id + `").result)`); v != "nil" {
				results[id] = v
			}
		}
	}
	if results["@t/here"] != "nil|200" {
		t.Errorf("allowed host: %q", results["@t/here"])
	}
	if !strings.Contains(results["@t/elsewhere"], "not among the hosts") {
		t.Errorf("host outside the list: %q", results["@t/elsewhere"])
	}
}

// MQTT topics narrowed by the grants: a subscription inside an allowed filter works, one
// wider or elsewhere is refused; an empty publish list allows no publishing.
func TestPluginSandboxMQTTTopics(t *testing.T) {
	engine := sandboxEngine(t, "@t/m", `{api = 2, mqtt = true, limits = {mqttSubscribe = {"shelly/#", "home/+/temp"}, mqttPublish = {}}}`)
	if _, err := engine.Execute(`
		__mqtt = {}
		MQTT = {
			subscribe = function(self, topic) table.insert(__mqtt, "sub " .. topic) end,
			publish = function(self, topic) table.insert(__mqtt, "pub " .. topic) end,
		}
	`); err != nil {
		t.Fatal(err)
	}
	_, e := runPlugin(t, engine, "@t/m", `
		local p = Plugin:new("m")
		local results = {}
		for _, topic in ipairs({"shelly/+/status", "shelly", "shelly/#", "home/kitchen/temp", "home/+/temp",
			"home/kitchen/hum", "home/#", "#", "other/x"}) do
			local ok = pcall(function() p:mqttSubscribe(topic, function() end) end)
			table.insert(results, topic .. "=" .. tostring(ok))
		end
		local ok = pcall(function() p:mqttPublish("shelly/x/command", "on") end)
		table.insert(results, "publish=" .. tostring(ok))
		p.results = table.concat(results, " ")
	`)
	if e != "nil" {
		t.Fatal(e)
	}
	mustLua(t, engine, `Plugin.getPlugin("@t/m").results`,
		"shelly/+/status=true shelly=true shelly/#=true home/kitchen/temp=true home/+/temp=true "+
			"home/kitchen/hum=false home/#=false #=false other/x=false publish=false")
	mustLua(t, engine, `table.concat(__mqtt, ",")`, "sub shelly/+/status,sub shelly,sub shelly/#,sub home/kitchen/temp,sub home/+/temp")
}
