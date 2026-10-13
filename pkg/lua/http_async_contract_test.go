package lua

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"
)

// Contract: every async HTTP request made from Lua gets its own response, whoever else has
// requests in flight. Plugins (PluginHelpers.httpRequest), HTTP clients (HTTP:asyncGet) and
// Grenton HTTPRequest objects share one Go backend; a slow request must not lose its response
// to a fast one that completes first.
func TestAsyncHTTPEachRequestGetsItsOwnResponse(t *testing.T) {
	// /<delay ms>/<tag> answers with the tag after the delay.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		parts := strings.SplitN(strings.TrimPrefix(r.URL.Path, "/"), "/", 2)
		ms, _ := strconv.Atoi(parts[0])
		time.Sleep(time.Duration(ms) * time.Millisecond)
		_, _ = fmt.Fprint(w, parts[1])
	}))
	defer srv.Close()

	engine := NewLuaEngine()
	defer engine.Close()
	engine.StartTimers()
	defer engine.StopTimers()

	base := srv.URL
	host := strings.TrimPrefix(base, "http://")
	_, err := engine.Execute(`
		results = {}
		local PluginHelpers = require("PluginHelpers")
		local function plugin(tag, delay)
			PluginHelpers.httpRequest("contract-plugin", { url = "` + base + `/" .. delay .. "/" .. tag, timeout = 5000 },
				function(resp, err) results[tag] = tostring(resp and resp.status) .. ":" .. tostring(resp and resp.body) .. ":" .. tostring(err) end)
		end
		-- Two requests from one plugin in the same second, finishing in reverse order.
		plugin("plugin-slow", 400)
		plugin("plugin-fast", 50)

		local c1, c2 = HTTP:new("contract-client-1"), HTTP:new("contract-client-2")
		c1:setTimeout(5000); c2:setTimeout(5000)
		c1:asyncGet("` + base + `/250/client-1", function(resp, err)
			results["client-1"] = tostring(resp.status) .. ":" .. tostring(resp.body) .. ":" .. tostring(err) end)
		c2:asyncGet("` + base + `/100/client-2", function(resp, err)
			results["client-2"] = tostring(resp.status) .. ":" .. tostring(resp.body) .. ":" .. tostring(err) end)

		req = HTTPRequest:new("contract-req", nil, EventBus:getShared())
		req:set(HTTPRequest.FEATURE_HOST, "` + host + `")
		req:set(HTTPRequest.FEATURE_PATH, "/150/grenton-request")
		req:set(HTTPRequest.FEATURE_RESPONSE_TYPE, HTTPRequest.RESPONSE_TYPE_TEXT)
		req:add_event(HTTPRequest.EVENT_ON_RESPONSE, function()
			results["grenton-request"] = tostring(req:get(HTTPRequest.FEATURE_STATUS_CODE)) .. ":" ..
				tostring(req:get(HTTPRequest.FEATURE_RESPONSE_BODY)) .. ":nil"
		end)
		req:execute(HTTPRequest.METHOD_SEND_REQUEST)
	`)
	if err != nil {
		t.Fatalf("setup: %v", err)
	}

	want := map[string]string{
		"plugin-slow":     "200:plugin-slow:nil",
		"plugin-fast":     "200:plugin-fast:nil",
		"client-1":        "200:client-1:nil",
		"client-2":        "200:client-2:nil",
		"grenton-request": "200:grenton-request:nil",
	}
	got := map[string]string{}
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) && len(got) < len(want) {
		time.Sleep(20 * time.Millisecond)
		engine.ProcessTimers()
		for tag := range want {
			if v, _ := engine.Execute(`results["` + tag + `"]`); v != "" && v != "nil" {
				got[tag] = v
			}
		}
	}
	for tag, w := range want {
		if got[tag] != w {
			t.Errorf("%s: got %q, want %q", tag, got[tag], w)
		}
	}
}

// A transport error reaches the HTTP client's callback as the error text, not as "HTTP 0".
func TestAsyncHTTPClientReportsTransportError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	url := srv.URL
	srv.Close() // connection refused from now on

	engine := NewLuaEngine()
	defer engine.Close()
	engine.StartTimers()
	defer engine.StopTimers()
	_, err := engine.Execute(`
		c = HTTP:new("contract-refused")
		c:setTimeout(3000)
		c:asyncGet("` + url + `/x", function(resp, err) refused = tostring(resp.status) .. "|" .. tostring(err) end)
	`)
	if err != nil {
		t.Fatalf("setup: %v", err)
	}
	var got string
	for i := 0; i < 200 && (got == "" || got == "nil"); i++ {
		time.Sleep(20 * time.Millisecond)
		engine.ProcessTimers()
		got, _ = engine.Execute(`refused`)
	}
	if !strings.HasPrefix(got, "0|") || strings.Contains(got, "HTTP 0") || got == "0|nil" {
		t.Errorf("refused connection = %q, want status 0 and the transport error", got)
	}
}

// A response nobody collects (its waiter timed out or was aborted) does not stay in memory forever.
func TestAsyncHTTPUncollectedResponsesExpire(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = fmt.Fprint(w, "ok") }))
	defer srv.Close()

	hm := NewHTTPManager()
	hm.asyncMu.Lock()
	hm.asyncResponses["abandoned"] = &asyncHTTPResponse{Status: 200, doneAt: time.Now().Add(-asyncResponseTTL - time.Minute)}
	hm.asyncMu.Unlock()

	hm.AsyncRequest("ttl", "fresh", "GET", srv.URL, nil, "", 5000, true, true)
	var resp *asyncHTTPResponse
	for i := 0; i < 250 && resp == nil; i++ {
		time.Sleep(20 * time.Millisecond)
		resp = hm.GetAsyncResponse("fresh")
	}
	if resp == nil || resp.Body != "ok" {
		t.Fatalf("fresh response = %+v, want body ok", resp)
	}
	if hm.GetAsyncResponse("abandoned") != nil {
		t.Error("a response older than asyncResponseTTL was not removed")
	}
}
