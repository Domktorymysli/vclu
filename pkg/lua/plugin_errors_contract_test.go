package lua

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// Contract: a plugin's HTTP callback gets the error both as its second argument and as
// resp.err, the field the plugin docs describe and plugins check ("if resp.err then").
func TestPluginHTTPErrorInRespErr(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	refused := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	refusedURL := refused.URL
	refused.Close()

	engine := NewLuaEngine()
	defer engine.Close()
	engine.StartTimers()
	defer engine.StopTimers()

	_, err := engine.Execute(`
		results = {}
		local PluginHelpers = require("PluginHelpers")
		local function probe(tag, url)
			PluginHelpers.httpRequest("contract-err", { url = url, timeout = 3000 }, function(resp, err)
				results[tag] = tostring(resp.status) .. "|" .. tostring(resp.err) .. "|" .. tostring(err)
			end)
		end
		probe("refused", "` + refusedURL + `/x")
		probe("server-error", "` + srv.URL + `/x")
	`)
	if err != nil {
		t.Fatalf("setup: %v", err)
	}
	got := map[string]string{}
	for i := 0; i < 250 && len(got) < 2; i++ {
		time.Sleep(20 * time.Millisecond)
		engine.ProcessTimers()
		for _, tag := range []string{"refused", "server-error"} {
			if v, _ := engine.Execute(`results["` + tag + `"]`); v != "" && v != "nil" {
				got[tag] = v
			}
		}
	}

	parts := strings.SplitN(got["refused"], "|", 3)
	if len(parts) != 3 || parts[0] != "0" || parts[1] == "nil" || parts[1] != parts[2] || !strings.Contains(parts[1], "refused") {
		t.Errorf("refused: got %q, want status 0 and resp.err equal to the error argument", got["refused"])
	}
	if got["server-error"] != "500|HTTP 500|HTTP 500" {
		t.Errorf("server error: got %q, want 500|HTTP 500|HTTP 500", got["server-error"])
	}
}

// A successful request leaves resp.err nil.
func TestPluginHTTPSuccessHasNoRespErr(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("ok")) }))
	defer srv.Close()

	engine := NewLuaEngine()
	defer engine.Close()
	engine.StartTimers()
	defer engine.StopTimers()

	_, err := engine.Execute(`
		local PluginHelpers = require("PluginHelpers")
		PluginHelpers.httpRequest("contract-ok", { url = "` + srv.URL + `" }, function(resp, err)
			result = tostring(resp.status) .. "|" .. tostring(resp.err) .. "|" .. tostring(err)
		end)
	`)
	if err != nil {
		t.Fatalf("setup: %v", err)
	}
	var got string
	for i := 0; i < 250 && (got == "" || got == "nil"); i++ {
		time.Sleep(20 * time.Millisecond)
		engine.ProcessTimers()
		got, _ = engine.Execute(`result`)
	}
	if got != "200|nil|nil" {
		t.Errorf("success: got %q, want 200|nil|nil", got)
	}
}

// done("message") is data to the poller. When the poller has no onData to use it, that is
// almost always an error passed the wrong way: warn once, pointing to done(nil, err).
func TestPollerWarnsOnStringDataWithoutOnData(t *testing.T) {
	engine := NewLuaEngine()
	defer engine.Close()

	got, err := engine.Execute(`(function()
		local PluginHelpers = require("PluginHelpers")
		local warns = {}
		local origWarn = Logger.warn
		Logger.warn = function(_, msg) warns[#warns + 1] = tostring(msg) end

		local wrong = PluginHelpers.createPoller("contract-poller", "wrong", {
			onTick = function(done) done("connection refused") end })
		wrong:poll(); wrong:poll()
		local withData = PluginHelpers.createPoller("contract-poller", "with-data", {
			onTick = function(done) done("payload") end, onData = function() end })
		withData:poll()
		local right = PluginHelpers.createPoller("contract-poller", "right", {
			onTick = function(done) done(nil, "connection refused") end })
		right:poll()

		Logger.warn = origWarn
		local s = wrong:stats()
		return #warns .. "|" .. (warns[1] or "") .. "|" .. s.success .. "/" .. right:stats().failed
	end)()`)
	if err != nil {
		t.Fatalf("lua: %v", err)
	}
	parts := strings.SplitN(got, "|", 3)
	if len(parts) != 3 || parts[0] != "1" {
		t.Fatalf("got %q, want exactly one warning (once per poller, none with onData or done(nil, err))", got)
	}
	if !strings.Contains(parts[1], "'wrong'") || !strings.Contains(parts[1], "done(nil, err)") {
		t.Errorf("warning %q should name the poller and point to done(nil, err)", parts[1])
	}
	// Behaviour is unchanged: done("x") still counts as success, done(nil, err) as failure.
	if parts[2] != "2/1" {
		t.Errorf("stats = %q, want 2 successes for done(\"x\") and 1 failure for done(nil, err)", parts[2])
	}
}
