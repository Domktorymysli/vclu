package web

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Domktorymysli/vclu/pkg/lua"
	"github.com/gin-gonic/gin"
)

const calendarEditDashboards = `
dom = Calendar.new('dom'):editable():roles('rodzice'):clock({ wait = false })
  :event('szkola', { title = 'Szkoła', every = 'day', time = '08:00', duration = '6h', tags = 'dzieci' })
  :event('podlewanie', { title = 'Podlewanie', every = 'day', time = '05:00', tags = 'ogrod' })
  :build()
edits = {}
dom:on('edit', function(e) edits[#edits + 1] = e.action .. ' ' .. tostring(e.id) .. ' ' .. tostring(e.actor) end)
Dashboard.new('kal'):remote():section('S', 'calendar')
  :calendar('c', 'Dom', dom, { tags = 'dzieci' })
  :build()
Dashboard.new('pub'):section('S', 'calendar'):calendar('c', 'Dom', dom):build()
fixed = Calendar.new('staly'):clock({ wait = false }):event('x', { every = 'day', time = '10:00' }):build()
Dashboard.new('fixed'):remote():section('S', 'calendar'):calendar('c', 'Stały', fixed):build()
`

// Through the cloud: who the calendar's roles name adds, changes and removes their own events
// and skips a day of the module's, which stays the module's; the range says who may and shows
// the skipped days only to them.
func TestCalendarEditThroughTunnel(t *testing.T) {
	dashCache.Invalidate()
	actionLimiter = &rateLimiter{buckets: make(map[string]*bucket), rate: 100, burst: 100}
	engine := lua.NewLuaEngine()
	t.Cleanup(engine.Close)
	if _, err := engine.Execute(calendarEditDashboards); err != nil {
		t.Fatal(err)
	}
	s := &Server{serverDir: t.TempDir(), sessions: newSessionStore(), luaExecutor: engine, calendarLimiter: newCalendarLimiter(), live: newLiveFeeds()}
	t.Cleanup(s.live.shutdown)
	gin.SetMode(gin.TestMode)
	tunnel := s.TunnelHandler()
	s.SetLocalAccess(newLocal(t, t.TempDir()))
	s.local.SetMembers(members(1, "member"))

	act := func(dash, action, value string) tunnelResp {
		return do(tunnel, "POST", "/api/dashboard/"+dash+"/action", "member", `{"widgetId":"c","action":"`+action+`","value":`+value+`}`)
	}
	rng := func(dash string) tunnelResp {
		return do(tunnel, "GET", "/api/dashboard/"+dash+"/calendar?widget=c&from=2026-11-20&to=2026-11-20&skipped=1", "member", "")
	}
	if r := rng("kal"); r.code != http.StatusOK || r.body["editable"] != nil {
		t.Errorf("without the role: %d %s", r.code, r.raw)
	}
	if r := act("kal", "add", `{"title":"Wizyta","date":"2026-11-20","time":"10:00","duration":"1h"}`); r.code != http.StatusForbidden {
		t.Errorf("add without the role: %d %s", r.code, r.raw)
	}

	m := members(2, "member")
	m.Members[0].Roles = []string{"rodzice"}
	s.local.SetMembers(m)
	if r := rng("kal"); r.body["editable"] != true {
		t.Errorf("with the role, the range: %s", r.raw)
	}
	add := act("kal", "add", `{"title":"Wizyta","date":"2026-11-20","time":"10:00","duration":"1h"}`)
	if add.code != http.StatusOK {
		t.Fatalf("add: %d %s", add.code, add.raw)
	}
	id, _ := add.body["value"].(map[string]any)["id"].(string)
	if id == "" {
		t.Fatalf("add gave no id: %s", add.raw)
	}
	// added on a widget with tags, it shows there
	if r := rng("kal"); !strings.Contains(r.raw, `"title":"Wizyta"`) {
		t.Errorf("the added event is not on the widget: %s", r.raw)
	}
	if r := act("kal", "update", `{"id":"`+id+`","title":"Lekarz","date":"2026-11-20","time":"11:00"}`); r.code != http.StatusOK {
		t.Errorf("update: %d %s", r.code, r.raw)
	}
	if r := act("kal", "update", `{"id":"szkola","title":"Hack","date":"2026-11-20"}`); r.code != http.StatusForbidden {
		t.Errorf("update of the module's event: %d %s", r.code, r.raw)
	}
	if r := act("kal", "remove", `{"id":"szkola"}`); r.code != http.StatusForbidden {
		t.Errorf("remove of the module's event: %d %s", r.code, r.raw)
	}
	if r := act("kal", "add", `{"title":"x","date":"2026-11-20","notify":"y"}`); r.code != http.StatusBadRequest {
		t.Errorf("a field the page may not set: %d %s", r.code, r.raw)
	}
	if r := act("kal", "skip", `{"id":"szkola","date":"2026-11-20"}`); r.code != http.StatusOK {
		t.Errorf("skip: %d %s", r.code, r.raw)
	}
	// an event the widget does not show (another tag) is out of reach
	if r := act("kal", "skip", `{"id":"podlewanie","date":"2026-11-20"}`); r.code != http.StatusNotFound {
		t.Errorf("skip of an event the widget does not show: %d %s", r.code, r.raw)
	}
	// a client that does not ask for skipped days gets none
	if r := do(tunnel, "GET", "/api/dashboard/kal/calendar?widget=c&from=2026-11-20&to=2026-11-20", "member", ""); strings.Contains(r.raw, `"id":"szkola"`) {
		t.Errorf("skipped day sent without skipped=1: %s", r.raw)
	}
	// 80 letters, not bytes
	long := strings.Repeat("ą", 80)
	if r := act("kal", "add", `{"title":"`+long+`","date":"2026-11-21"}`); r.code != http.StatusOK {
		t.Errorf("80 Polish letters: %d %s", r.code, r.raw)
	} else if r := act("kal", "remove", `{"id":"`+r.body["value"].(map[string]any)["id"].(string)+`"}`); r.code != http.StatusOK {
		t.Errorf("remove: %d %s", r.code, r.raw)
	}
	if r := act("kal", "add", `{"title":"`+long+`x","date":"2026-11-21"}`); r.code != http.StatusBadRequest {
		t.Errorf("81 letters: %d %s", r.code, r.raw)
	}
	if r := rng("kal"); !strings.Contains(r.raw, `"skipped":true`) {
		t.Errorf("the skipped day is not shown to who may edit: %s", r.raw)
	}
	s.local.SetMembers(members(3, "member"))
	if r := rng("kal"); strings.Contains(r.raw, `"skipped"`) || strings.Contains(r.raw, `"id":"szkola"`) {
		t.Errorf("without the role the skipped day shows: %s", r.raw)
	}
	m.Version = 4 // a list older than the last one is ignored
	s.local.SetMembers(m)
	if r := act("kal", "unskip", `{"id":"szkola","date":"2026-11-20"}`); r.code != http.StatusOK {
		t.Errorf("unskip: %d %s", r.code, r.raw)
	}
	if r := act("kal", "remove", `{"id":"`+id+`"}`); r.code != http.StatusOK {
		t.Errorf("remove: %d %s", r.code, r.raw)
	}
	// a calendar the module did not make editable
	if r := act("fixed", "skip", `{"id":"x","date":"2026-11-20"}`); r.code != http.StatusForbidden {
		t.Errorf("not editable: %d %s", r.code, r.raw)
	}
	got := luaString(t, engine, `return table.concat(edits, '|')`)
	want := "add " + id + " cloud:12|update " + id + " cloud:12|skip szkola cloud:12|add u2 cloud:12|remove u2 cloud:12|unskip szkola cloud:12|remove " + id + " cloud:12"
	if got != want {
		t.Errorf("edits\n got %s\nwant %s", got, want)
	}
}

// On the LAN without a token a public page does not get round the calendar's roles.
func TestCalendarEditOnTheLAN(t *testing.T) {
	dashCache.Invalidate()
	actionLimiter = &rateLimiter{buckets: make(map[string]*bucket), rate: 100, burst: 100}
	engine := lua.NewLuaEngine()
	t.Cleanup(engine.Close)
	if _, err := engine.Execute(calendarEditDashboards); err != nil {
		t.Fatal(err)
	}
	s := &Server{serverDir: t.TempDir(), sessions: newSessionStore(), luaExecutor: engine, calendarLimiter: newCalendarLimiter(), live: newLiveFeeds()}
	t.Cleanup(s.live.shutdown)
	gin.SetMode(gin.TestMode)
	lan := gin.New()
	lan.POST("/api/dashboard/:id/action", s.handleAPIDashboardAction)
	w := httptest.NewRecorder()
	lan.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/dashboard/pub/action",
		strings.NewReader(`{"widgetId":"c","action":"skip","value":{"id":"szkola","date":"2026-11-20"}}`)))
	if w.Code != http.StatusForbidden {
		t.Errorf("public page: %d %s", w.Code, w.Body)
	}
}

// A calendar the module did not make editable answers as before, also to a page that asks for
// the skipped days: none of them, and no editable or edit fields.
func TestCalendarNotEditableRangeUnchanged(t *testing.T) {
	dashCache.Invalidate()
	engine := lua.NewLuaEngine()
	t.Cleanup(engine.Close)
	if _, err := engine.Execute(calendarEditDashboards + `fixed:skip('x', '2026-11-20')`); err != nil {
		t.Fatal(err)
	}
	s := &Server{serverDir: t.TempDir(), sessions: newSessionStore(), luaExecutor: engine, calendarLimiter: newCalendarLimiter(), live: newLiveFeeds()}
	t.Cleanup(s.live.shutdown)
	gin.SetMode(gin.TestMode)
	lan := gin.New()
	lan.GET("/api/dashboard/:id/calendar", s.handleAPIDashboardCalendar)
	for _, q := range []string{"", "&skipped=1"} {
		w := httptest.NewRecorder()
		lan.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/dashboard/fixed/calendar?widget=c&from=2026-11-20&to=2026-11-21"+q, nil))
		body := w.Body.String()
		if w.Code != http.StatusOK || strings.Count(body, `"id":"x"`) != 1 || !strings.Contains(body, `"date":"2026-11-21"`) {
			t.Errorf("%q: %d %s", q, w.Code, body)
		}
		for _, f := range []string{`"skipped"`, `"editable"`, `"edit"`} {
			if strings.Contains(body, f) {
				t.Errorf("%q: %s in %s", q, f, body)
			}
		}
	}
}

// update on a widget with tags keeps the event's own tags (it may show on other widgets too),
// and the page's time replaces saved hours instead of clashing with them.
func TestCalendarUpdateKeepsTags(t *testing.T) {
	engine := lua.NewLuaEngine()
	t.Cleanup(engine.Close)
	if _, err := engine.Execute(`
dom = Calendar.new('dom'):editable():clock({ wait = false }):build()
dom:add({ id = 'a', title = 'A', every = 'mo', from = '2026-11-02', time = '10:00', tags = { 'dzieci', 'wazne' }, icon = 'x' })
dom:add({ id = 'b', title = 'B', every = 'tu', hours = '10:00-11:00', tags = 'dzieci' })
Dashboard.new('pub'):section('S', 'calendar'):calendar('c', 'Dom', dom, { tags = 'dzieci' }):build()
`); err != nil {
		t.Fatal(err)
	}
	got := luaString(t, engine, `
local a = Dashboard.API.executeAction('pub', 'c', 'update', nil, { valueJSON = '{"id":"a","title":"A2","every":"mo","from":"2026-11-02","time":"11:00"}' })
local b = Dashboard.API.executeAction('pub', 'c', 'update', nil, { valueJSON = '{"id":"b","title":"B2","every":"tu","from":"2026-11-03","time":"12:00","duration":"1h"}' })
local s = dom:_saved('a')
return a .. ' ' .. b .. ' ' .. table.concat(s.tags, ',') .. ' ' .. s.icon .. ' ' .. tostring(dom:_saved('b').hours)`)
	if got != `{"success":true,"value":{"id":"a"}} {"success":true,"value":{"id":"b"}} dzieci,wazne x nil` {
		t.Errorf("got %s", got)
	}
}
