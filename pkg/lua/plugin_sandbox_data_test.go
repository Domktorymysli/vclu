package lua

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// Data a plugin did not create (a registry object, an event payload) is read-only,
// and still works with the standard library and JSON, which read tables raw.

func pumpTimers(e *LuaEngine, d time.Duration) {
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		e.ProcessTimers()
		time.Sleep(10 * time.Millisecond)
	}
}

func TestPluginSandboxReadOnlyDataInStdlib(t *testing.T) {
	engine := sandboxEngine(t, "@t/a", "{official = true}")
	if _, err := engine.Execute(`
		ModObj = { name = "x", list = {1, 2, 3}, rows = { { x = 1 }, { x = 2 } },
			getValue = function(self) return 42 end }
		_registry["modobj"] = ModObj
	`); err != nil {
		t.Fatal(err)
	}
	r, e := runPlugin(t, engine, "@t/a", `
		local o = _:get("modobj")
		local out = {}
		out.json = JSON:encode(o)
		out.concat = table.concat(o.list, ",")
		out.unpackN = select("#", unpack(o.list))
		local first = unpack(o.rows)
		out.firstX = first.x
		out.firstWritable = pcall(function() first.x = 9 end)
		out.own = JSON:encode({ inner = o.rows })
		out.insert = pcall(table.insert, o.list, 4)
		out.sort = pcall(table.sort, o.list)
		out.remove = pcall(table.remove, o.list)
		return JSON:encode(out)
	`)
	if e != "nil" {
		t.Fatalf("plugin: %s", e)
	}
	for _, want := range []string{
		`"json":"{\"name\":\"x\",\"list\":[1,2,3],\"rows\":[{\"x\":1},{\"x\":2}]}"`,
		`"concat":"1,2,3"`, `"unpackN":3`, `"firstX":1`, `"firstWritable":false`,
		`"own":"{\"inner\":[{\"x\":1},{\"x\":2}]}"`, `"insert":false`, `"sort":false`, `"remove":false`,
	} {
		if !strings.Contains(r, want) {
			t.Errorf("missing %s in %s", want, r)
		}
	}
	mustLua(t, engine, `#ModObj.list .. ModObj.rows[1].x`, "31")

	// an event payload: encoded whole, and passed on to a module as plain data
	if _, err := engine.Execute(`
		EventBus:getShared():on("a:relay", function(payload)
			__relay = JSON:encode(payload) .. " " .. tostring(getmetatable(payload.items) == nil)
		end)
	`); err != nil {
		t.Fatal(err)
	}
	_, e = runPlugin(t, engine, "@t/a", `
		p = Plugin:new("a", { name = "A", version = "1" })
		p:on("mod:data", function(payload)
			p.seen = JSON:encode(payload) .. "|" .. p:encodeJson(payload.items) .. "|" .. table.concat(payload.items, ",")
			p:emit("a:relay", { items = payload.items, n = #payload.items })
		end)
	`)
	if e != "nil" {
		t.Fatalf("plugin: %s", e)
	}
	if _, err := engine.Execute(`EventBus:getShared():emit("mod:data", { items = {"a", "b"}, n = 1 })`); err != nil {
		t.Fatal(err)
	}
	mustLua(t, engine, `Plugin.getPlugin("@t/a").seen`, `{"items":["a","b"],"n":1}|["a","b"]|a,b`)
	mustLua(t, engine, `__relay`, `{"items":["a","b"],"n":2} true`)
}

// The LAN permission, end to end through the plugin's HTTP API.
func TestPluginSandboxLANGuard(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("hi")) }))
	defer ts.Close()
	engine := sandboxEngine(t, "@t/nolan", "{}")
	engine.StartTimers()
	defer engine.StopTimers()
	if _, err := engine.Execute(`PluginSandbox:create("@t/lan", {lan = true})`); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"@t/nolan", "@t/lan"} {
		_, e := runPlugin(t, engine, id, `
			local p = Plugin:new("x")
			p:httpGet("`+ts.URL+`", function(resp, err)
				p.result = tostring(err) .. "|" .. tostring(resp and resp.status)
			end)
		`)
		if e != "nil" {
			t.Fatalf("%s: %s", id, e)
		}
	}
	deadline := time.Now().Add(5 * time.Second)
	results := map[string]string{}
	for time.Now().Before(deadline) && len(results) < 2 {
		pumpTimers(engine, 50*time.Millisecond)
		for _, id := range []string{"@t/nolan", "@t/lan"} {
			if v, _ := engine.Execute(`tostring(Plugin.getPlugin("` + id + `").result)`); v != "nil" {
				results[id] = v
			}
		}
	}
	if !strings.Contains(results["@t/nolan"], `a local address needs "lan"`) {
		t.Errorf("no lan: %q", results["@t/nolan"])
	}
	if results["@t/lan"] != "nil|200" {
		t.Errorf("lan: %q", results["@t/lan"])
	}
}
