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

// The discovery endpoint republishes what the runtime publishes on start: the
// state topics of exposed objects, which the runtime keeps updating.
func TestHADiscoveryPublishUsesRuntime(t *testing.T) {
	engine := lua.NewLuaEngine()
	defer engine.Close()
	s := &Server{luaExecutor: engine}
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.POST("/api/ha/discovery/publish", s.handleAPIHADiscoveryPublish)
	post := func() *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/ha/discovery/publish", nil))
		return w
	}

	if w := post(); w.Code != http.StatusConflict {
		t.Fatalf("integration off: %d %s", w.Code, w.Body)
	}

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
	w := post()
	if w.Code != http.StatusOK {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	var res struct {
		Published int `json:"published"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &res); err != nil || res.Published != 1 {
		t.Fatalf("published: %s", w.Body)
	}
	cfg, err := engine.Execute(`(function()
		for _, p in ipairs(_pub) do
			if p.topic:find("/config$") and p.payload ~= "" then return p.payload end
		end
		return ""
	end)()`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(cfg, `"state_topic":"vclu/exposed/vclu/switch/salon_lampa/state"`) {
		t.Errorf("discovery not from the runtime: %s", cfg)
	}
}
