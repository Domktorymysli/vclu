package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Domktorymysli/vclu/pkg/lua"
	"github.com/gin-gonic/gin"
)

// haDiscoveryServer runs the discovery endpoint on a runtime with the HA publisher
// on a fake MQTT instance that records every publish in _pub, and one exposed
// switch (CLU1.DOU1 as salon.lampa) that is ON.
func haDiscoveryServer(t *testing.T) (*lua.LuaEngine, func() *httptest.ResponseRecorder) {
	t.Helper()
	engine := lua.NewLuaEngine()
	t.Cleanup(engine.Close)
	s := &Server{luaExecutor: engine}
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.POST("/api/ha/discovery/publish", s.handleAPIHADiscoveryPublish)
	if _, err := engine.Execute(`
		_pub = {}
		HAStatePublisher.setup({
			publish = function(self, topic, payload) table.insert(_pub, { topic = topic, payload = payload }) end,
			subscribe = function() end, unsubscribe = function() end,
		})
		local sw = RemoteSwitch:new({ executeNoReply = function() end }, "DOU1", "LAMPA")
		registerObject("CLU1.DOU1", sw)
		expose(sw, "switch", { path = "salon.lampa" })
		_:sync("CLU1.DOU1", 1)
		_pub = {}
	`); err != nil {
		t.Fatal(err)
	}
	return engine, func() *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/ha/discovery/publish", nil))
		return w
	}
}

// lastPublished returns the payload of the last publish to a topic, "-" when none.
func lastPublished(t *testing.T, engine *lua.LuaEngine, topic string) string {
	t.Helper()
	out, err := engine.Execute(`(function()
		local found = "-"
		for _, p in ipairs(_pub) do
			if p.topic == "` + topic + `" then found = p.payload end
		end
		return found
	end)()`)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

const lampaSwitchConfig = "homeassistant/switch/vclu_salon_lampa/config"

// An object hidden for MQTT gets its retained discovery cleared by the endpoint.
func TestHADiscoveryPublishClearsHidden(t *testing.T) {
	engine, post := haDiscoveryServer(t)
	if _, err := engine.Execute(`AccessControl.set("mqtt", "salon.lampa", "hidden"); _pub = {}`); err != nil {
		t.Fatal(err)
	}
	if w := post(); w.Code != http.StatusOK {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	if got := lastPublished(t, engine, lampaSwitchConfig); got != "" {
		t.Errorf("hidden object's discovery not cleared, last config payload: %q", got)
	}
	if got := lastPublished(t, engine, "vclu/exposed/vclu/switch/salon_lampa/state"); got != "-" {
		t.Errorf("hidden object's state was published: %q", got)
	}
}

// A read-only object is republished without a command topic: as a binary_sensor,
// with the switch config cleared.
func TestHADiscoveryPublishReadonlyHasNoCommandTopic(t *testing.T) {
	engine, post := haDiscoveryServer(t)
	if _, err := engine.Execute(`AccessControl.set("mqtt", "salon.lampa", "readonly"); _pub = {}`); err != nil {
		t.Fatal(err)
	}
	if w := post(); w.Code != http.StatusOK {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	cfg := lastPublished(t, engine, "homeassistant/binary_sensor/vclu_salon_lampa/config")
	if cfg == "-" || cfg == "" {
		t.Fatalf("no read-only discovery published: %q", cfg)
	}
	if strings.Contains(cfg, "command_topic") {
		t.Errorf("read-only object published with a command topic: %s", cfg)
	}
	if got := lastPublished(t, engine, lampaSwitchConfig); got != "" {
		t.Errorf("stale switch config not cleared: %q", got)
	}
}

// The endpoint publishes the current state along with the discovery, so HA shows
// a value right after re-discovery.
func TestHADiscoveryPublishPublishesCurrentState(t *testing.T) {
	engine, post := haDiscoveryServer(t)
	if w := post(); w.Code != http.StatusOK {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	if got := lastPublished(t, engine, "vclu/exposed/vclu/switch/salon_lampa/state"); got != "ON" {
		t.Errorf("state after re-discovery: %q, want ON", got)
	}
	if _, err := engine.Execute(`_:sync("CLU1.DOU1", 0); _pub = {}`); err != nil {
		t.Fatal(err)
	}
	post()
	if got := lastPublished(t, engine, "vclu/exposed/vclu/switch/salon_lampa/state"); got != "OFF" {
		t.Errorf("state after re-discovery: %q, want OFF", got)
	}
}

// A runtime error inside the refresh is reported, not swallowed as success.
func TestHADiscoveryPublishReportsLuaError(t *testing.T) {
	engine, post := haDiscoveryServer(t)
	if _, err := engine.Execute(`HAStatePublisher.refresh = function() error("broker exploded") end`); err != nil {
		t.Fatal(err)
	}
	w := post()
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	var res struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &res); err != nil || !strings.Contains(res.Error, "broker exploded") {
		t.Errorf("error not reported: %s", w.Body)
	}
}

// Without a Lua runtime the endpoint is unavailable.
func TestHADiscoveryPublishWithoutRuntime(t *testing.T) {
	s := &Server{}
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.POST("/api/ha/discovery/publish", s.handleAPIHADiscoveryPublish)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/ha/discovery/publish", nil))
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
}

// A publisher whose MQTT client cannot publish (gone, or not connected: the Lua
// client drops the Go "not connected" error) is reported, not counted as success.
func TestHADiscoveryPublishWithoutMQTTConnection(t *testing.T) {
	for name, setup := range map[string]string{
		"no instance":   `HAStatePublisher.mqttInstance = nil`,
		"not connected": `HAStatePublisher.mqttInstance.isConnected = function() return false end`,
	} {
		t.Run(name, func(t *testing.T) {
			engine, post := haDiscoveryServer(t)
			if _, err := engine.Execute(setup + `; _pub = {}`); err != nil {
				t.Fatal(err)
			}
			w := post()
			if w.Code != http.StatusServiceUnavailable || !strings.Contains(w.Body.String(), "not connected") {
				t.Errorf("%d %s", w.Code, w.Body)
			}
			if n, _ := engine.Execute(`#_pub`); n != "0" {
				t.Errorf("published %s messages to a dead client", n)
			}
		})
	}
}
