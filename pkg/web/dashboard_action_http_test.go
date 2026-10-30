package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Domktorymysli/vclu/pkg/lua"
	"github.com/gin-gonic/gin"
	glua "github.com/yuin/gopher-lua"
)

// Whatever a page sends as an object's keys and values reaches Lua as one table holding exactly
// those strings: no character closes the literal or adds a key.
func TestActionValueLuaNoInjection(t *testing.T) {
	L := glua.NewState()
	defer L.Close()

	var all strings.Builder
	for r := rune(0); r < 0x80; r++ {
		all.WriteRune(r)
	}
	nasty := []string{
		all.String(), "\x00", "a\x00b", `\`, `\\`, `\"`, `"`, `"]=1,["x`, `"} os.exit() --`, "]]", "[[", "\\z",
		"\\\n", "\t\v\f\a\b", "  ", "zażółć 🔥", strings.Repeat(`"\`, 50000),
	}
	// the request body is JSON: invalid UTF-8 arrives as U+FFFD, as the handler gets it
	var fromJSON map[string]interface{}
	if err := json.Unmarshal([]byte("{\"k\xff\xfe\": \"v\xc3\x28\"}"), &fromJSON); err != nil {
		t.Fatal(err)
	}
	for k, v := range fromJSON {
		nasty = append(nasty, k, v.(string))
	}

	for _, s := range nasty {
		for _, obj := range []map[string]interface{}{{s: 1.0}, {"r": s}} {
			if err := L.DoString("return " + actionValueLua(obj)); err != nil {
				t.Fatalf("%q: %v", s, err)
			}
			tbl, ok := L.Get(-1).(*glua.LTable)
			L.Pop(1)
			if !ok {
				t.Fatalf("%q: not a table", s)
			}
			n := 0
			tbl.ForEach(func(k, v glua.LValue) {
				n++
				for want, wantV := range obj {
					if k.String() != want {
						t.Errorf("key %q, want %q", k.String(), want)
					}
					if got := v.String(); got != glua.LVAsString(luaOf(wantV)) {
						t.Errorf("value %q, want %v", got, wantV)
					}
				}
			})
			if n != 1 {
				t.Errorf("%q: %d keys, want 1", s, n)
			}
		}
	}
}

func luaOf(v interface{}) glua.LValue {
	switch x := v.(type) {
	case float64:
		return glua.LNumber(x)
	case string:
		return glua.LString(x)
	}
	return glua.LNil
}

// Through the endpoint: a colour reaches setColor as r, g, b; the other widgets keep refusing or
// ignoring an object (a slider, a toggle, a thermostat); a thermostat takes a number in range.
func TestDashboardActionEndpointValues(t *testing.T) {
	dashCache.Invalidate()
	saved := actionLimiter
	actionLimiter = &rateLimiter{buckets: make(map[string]*bucket), rate: 1000, burst: 1000}
	t.Cleanup(func() { actionLimiter = saved })
	engine := lua.NewLuaEngine()
	defer engine.Close()
	if _, err := engine.Execute(`
		GOT = {}
		Dashboard.new('a')
			:section('S', 'i')
				:color('c', 'C', nil, { setColor = function(r, g, b) GOT.rgb = r .. ',' .. g .. ',' .. b end })
				:slider('s', 'S', 0, 100, function() return 0 end, function(v) GOT.slider = v end)
				:toggle('t', 'T', nil, function() return false end, function(v) GOT.toggle = tostring(v) end)
				:thermostat('th', 'Th', nil, { getCurrent = function() return 20 end, getTarget = function() return 20 end,
					setTarget = function(v) GOT.target = v end }, { min = 15, max = 25 })
				:thermostat('ro', 'Ro', nil, { getCurrent = function() return 20 end, getTarget = function() return 20 end,
					setTarget = function(v) GOT.ro = v end }, { readonly = true })
				:thermostat('md', 'Md', nil, { getCurrent = function() return 20 end, getTarget = function() return 20 end,
					getMode = function() return GOT.mode or 'manual' end, setMode = function(v) GOT.mode = v end },
					{ modes = { manual = 'Ręczny', schedule = 'Harmonogram' } })
				:thermostat('mdro', 'MdRo', nil, { getCurrent = function() return 20 end, getTarget = function() return 20 end,
					getMode = function() return 'manual' end, setMode = function(v) GOT.mdro = v end },
					{ modes = { manual = 'Ręczny', schedule = 'Harmonogram' }, readonly = true })
			:build()`); err != nil {
		t.Fatal(err)
	}
	s := &Server{serverDir: t.TempDir(), sessions: newSessionStore(), luaExecutor: engine}
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.POST("/api/dashboard/:id/action", s.handleAPIDashboardAction)
	post := func(body string) map[string]interface{} {
		t.Helper()
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/dashboard/a/action", strings.NewReader(body)))
		var res map[string]interface{}
		if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &res) != nil {
			t.Fatalf("%s: %d %s", body, w.Code, w.Body)
		}
		return res
	}
	got := func(k string) string {
		t.Helper()
		v, err := engine.Execute(`return tostring(GOT.` + k + `)`)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}

	if res := post(`{"widgetId":"c","action":"setColor","value":{"r":255,"g":"x","b":300,"__index":1}}`); res["success"] != true {
		t.Errorf("setColor = %v", res)
	}
	if v := got("rgb"); v != "255,0,255" {
		t.Errorf("setColor got %s, want 255,0,255", v)
	}
	if res := post(`{"widgetId":"s","action":"set","value":{"r":1}}`); res["error"] != "Value must be a number" {
		t.Errorf("slider with an object = %v", res)
	}
	if res := post(`{"widgetId":"t","action":"toggle","value":{"r":1}}`); res["success"] != true || got("toggle") != "false" {
		t.Errorf("toggle with an object = %v (%s)", res, got("toggle"))
	}
	if res := post(`{"widgetId":"th","action":"set","value":{"v":21}}`); res["error"] != "Value must be a number" {
		t.Errorf("thermostat with an object = %v", res)
	}
	if res := post(`{"widgetId":"th","action":"set","value":"21"}`); res["error"] != "Value must be a number" {
		t.Errorf("thermostat with a string = %v", res)
	}
	if res := post(`{"widgetId":"th","action":"set","value":1e308}`); res["error"] != "Value out of range [15, 25]" {
		t.Errorf("thermostat with 1e308 = %v", res)
	}
	if res := post(`{"widgetId":"ro","action":"set","value":21}`); res["error"] != "Read-only thermostat" || got("ro") != "nil" {
		t.Errorf("read-only thermostat = %v (%s)", res, got("ro"))
	}
	if res := post(`{"widgetId":"th","action":"set","value":21.5}`); res["success"] != true || res["value"] != 21.5 {
		t.Errorf("thermostat set = %v", res)
	}
	if v := got("target"); v != "21.5" {
		t.Errorf("setTarget got %s, want 21.5", v)
	}
	// a mode: a JSON string reaches setMode as is; an object or an unlisted value is refused
	if res := post(`{"widgetId":"md","action":"mode","value":{"v":"schedule"}}`); res["error"] != "Unknown mode" {
		t.Errorf("mode with an object = %v", res)
	}
	if res := post(`{"widgetId":"md","action":"mode","value":"eco"}`); res["error"] != "Unknown mode" || got("mode") != "nil" {
		t.Errorf("unlisted mode = %v (%s)", res, got("mode"))
	}
	if res := post(`{"widgetId":"mdro","action":"mode","value":"schedule"}`); res["error"] != "Read-only thermostat" || got("mdro") != "nil" {
		t.Errorf("read-only mode = %v (%s)", res, got("mdro"))
	}
	if res := post(`{"widgetId":"md","action":"mode","value":"schedule"}`); res["success"] != true || res["mode"] != "schedule" {
		t.Errorf("mode = %v", res)
	}
	if v := got("mode"); v != "schedule" {
		t.Errorf("setMode got %s, want schedule", v)
	}
}
