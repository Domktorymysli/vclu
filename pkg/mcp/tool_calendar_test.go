package mcp

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/Domktorymysli/vclu/pkg/config"
	"github.com/Domktorymysli/vclu/pkg/lua"
)

// calendarToolEngine builds a runtime with one calendar, ready (the engine runs no timers).
func calendarToolEngine(t *testing.T) *lua.LuaEngine {
	t.Helper()
	engine := lua.NewLuaEngine()
	t.Cleanup(engine.Close)
	if _, err := engine.Execute(`
		datetime.setTimezone("Europe/Warsaw")
		Calendar.new('dom'):name('Dom'):holidays('pl'):location(52.23, 21.01):clock({ wait = false })
			:event('bio', { title = 'Bio', every = 'tu', time = '06:30', tags = { 'smieci' } })
			:event('goscie', { title = 'Goście', date = '2026-11-14..2026-11-16' })
			:event('sport', { title = 'Siłownia', every = 'mo', time = '18:00', duration = '1h' })
			:schedule('pszok', { title = 'PSZOK', values = { 'zamknięty', 'otwarty' }, default = 'zamknięty',
				{ every = 'we', hours = '08:00-13:00', value = 'otwarty' } })
			:build()
		Calendar.get('dom'):_check()
	`); err != nil {
		t.Fatal(err)
	}
	return engine
}

type calendarToolAnswer struct {
	Error     string `json:"error"`
	Calendars []struct {
		ID, From, To string
		Ready        bool
		Error        string
		Days         []struct {
			Date, Weekday, Holiday string
			Workday                bool
			Namedays               []string
			Sun                    map[string]any
			Events                 []struct {
				ID, Title, Start, End, Last string
				AllDay                      bool `json:"allDay"`
			}
		}
		Schedules []struct {
			ID, Title  string
			Now        any
			NextChange *struct{ Value, At string } `json:"nextChange"`
			Segments   []struct{ From, To, Value string }
		}
	}
}

func callCalendarTool(t *testing.T, srv *MCPServer, ctx context.Context, args map[string]any) (calendarToolAnswer, string, bool) {
	t.Helper()
	res, err := srv.handleCalendar(ctx, makeCallRequest(args))
	if err != nil {
		t.Fatal(err)
	}
	text := extractText(t, res)
	var a calendarToolAnswer
	if !res.IsError {
		if err := json.Unmarshal([]byte(text), &a); err != nil {
			t.Fatalf("%v: %s", err, text)
		}
	}
	return a, text, res.IsError
}

// A range of days lists each day with its holiday and the events on it, in local time.
func TestCalendarTool_Range(t *testing.T) {
	srv := phase2Harness(calendarToolEngine(t), nil)
	ctx := devBotCtx(false)
	a, text, isErr := callCalendarTool(t, srv, ctx, map[string]any{"from": "2026-11-09", "to": "2026-11-16"})
	if isErr || len(a.Calendars) != 1 {
		t.Fatalf("%s", text)
	}
	c := a.Calendars[0]
	if c.ID != "dom" || !c.Ready || c.From != "2026-11-09" || c.To != "2026-11-16" || len(c.Days) != 8 {
		t.Fatalf("calendar: %s", text)
	}
	if d := c.Days[2]; d.Date != "2026-11-11" || d.Holiday != "Święto Niepodległości" || d.Workday {
		t.Errorf("11.11: %+v", d)
	}
	if ev := c.Days[1].Events; len(ev) != 1 || ev[0].ID != "bio" || ev[0].Start != "2026-11-10T06:30+01:00" {
		t.Errorf("Tuesday: %+v", ev)
	}
	if ev := c.Days[0].Events; len(ev) != 1 || ev[0].End != "2026-11-09T19:00+01:00" {
		t.Errorf("Monday: %+v", ev)
	}
	if ev := c.Days[5].Events; len(ev) != 1 || ev[0].ID != "goscie" || !ev[0].AllDay || ev[0].Last != "2026-11-16" {
		t.Errorf("Saturday: %+v", ev)
	}
	if c.Days[0].Sun != nil || c.Days[0].Namedays != nil {
		t.Errorf("sun and name days only for a single day: %+v", c.Days[0])
	}
	if len(c.Schedules) != 1 || c.Schedules[0].NextChange == nil || c.Schedules[0].Now == nil || c.Schedules[0].Segments != nil {
		t.Errorf("schedules: %+v", c.Schedules)
	}
}

// A single day adds sun times, name days and the hours of each schedule.
func TestCalendarTool_Day(t *testing.T) {
	srv := phase2Harness(calendarToolEngine(t), nil)
	a, text, isErr := callCalendarTool(t, srv, devBotCtx(false), map[string]any{"date": "2026-11-11"})
	if isErr {
		t.Fatal(text)
	}
	d := a.Calendars[0].Days
	if len(d) != 1 || d[0].Sun["sunrise"] == nil || d[0].Sun["dusk"] == nil || len(d[0].Namedays) == 0 {
		t.Fatalf("day: %s", text)
	}
	segs := a.Calendars[0].Schedules[0].Segments
	if len(segs) != 3 || segs[1].From != "08:00" || segs[1].To != "13:00" || segs[1].Value != "otwarty" || segs[2].To != "24:00" {
		t.Errorf("segments: %+v", segs)
	}
}

// Tags, today and tomorrow, and an unknown calendar.
func TestCalendarTool_Options(t *testing.T) {
	engine := calendarToolEngine(t)
	srv := phase2Harness(engine, nil)
	ctx := devBotCtx(false)

	a, text, _ := callCalendarTool(t, srv, ctx, map[string]any{"from": "2026-11-09", "to": "2026-11-16", "tags": []any{"smieci"}})
	n := 0
	for _, d := range a.Calendars[0].Days {
		for _, e := range d.Events {
			if e.ID != "bio" {
				t.Errorf("tag filter let %s through", e.ID)
			}
			n++
		}
	}
	if n != 1 {
		t.Errorf("tagged events: %d, %s", n, text)
	}

	today, _ := engine.Execute(`return Calendar.get('dom'):day().date`)
	tomorrow, _ := engine.Execute(`local c = Calendar.get('dom') return c:day(c:day().ends).date`)
	if a, _, _ := callCalendarTool(t, srv, ctx, nil); a.Calendars[0].From != today || len(a.Calendars[0].Days) != 7 {
		t.Errorf("default: from %s, %d days, want %s and 7", a.Calendars[0].From, len(a.Calendars[0].Days), today)
	}
	if a, _, _ := callCalendarTool(t, srv, ctx, map[string]any{"date": "tomorrow"}); a.Calendars[0].From != tomorrow || a.Calendars[0].To != tomorrow {
		t.Errorf("tomorrow: %+v, want %s", a.Calendars[0].From, tomorrow)
	}

	if _, text, isErr := callCalendarTool(t, srv, ctx, map[string]any{"calendar": "praca"}); !isErr || !strings.Contains(text, `not built`) || !strings.Contains(text, `dom`) {
		t.Errorf("unknown calendar: %v %s", isErr, text)
	}
}

// Bad arguments never reach Lua, and the range is bounded.
func TestCalendarTool_Refuses(t *testing.T) {
	srv := phase2Harness(calendarToolEngine(t), nil)
	ctx := devBotCtx(false)
	for _, args := range []map[string]any{
		{"calendar": `dom") os.exit(1) --`},
		{"from": "2026-11-09; x"},
		{"date": "jutro"},
		{"date": "2026-11-11", "from": "2026-11-10"},
		{"tags": []any{`a"`}},
		{"tags": []any{5}},
	} {
		if _, text, isErr := callCalendarTool(t, srv, ctx, args); !isErr {
			t.Errorf("%v accepted: %s", args, text)
		}
	}
	for args, want := range map[string]map[string]any{
		"to is before from":      {"from": "2026-11-10", "to": "2026-11-09"},
		"at most 62 days":        {"from": "2026-01-01", "to": "2026-03-04"},
		"expected as YYYY-MM-DD": {"date": "2026-02-31"},
	} {
		if _, text, isErr := callCalendarTool(t, srv, ctx, want); !isErr || !strings.Contains(text, args) {
			t.Errorf("%v: %v %s", want, isErr, text)
		}
	}
	if _, _, isErr := callCalendarTool(t, srv, ctx, map[string]any{"from": "2026-01-01", "to": "2026-03-03"}); isErr {
		t.Error("62 days refused")
	}
	// the spring change makes 63 days an hour short of 62 * 24 h
	if a, text, isErr := callCalendarTool(t, srv, ctx, map[string]any{"from": "2027-02-27", "to": "2027-04-30"}); !isErr || !strings.Contains(text, "at most 62 days") {
		t.Errorf("63 days over the spring change: %v, %d days", isErr, len(a.Calendars))
	}
	if _, text, isErr := callCalendarTool(t, srv, ctx, map[string]any{"from": "2027-02-28", "to": "2027-04-30"}); isErr {
		t.Errorf("62 days over the spring change refused: %s", text)
	}
}

// A bot without sandbox access gets nothing.
func TestCalendarTool_NeedsDevMode(t *testing.T) {
	srv := phase2Harness(calendarToolEngine(t), nil)
	bot := &config.MCPBot{ID: "b", Name: "Bot", Profiles: []string{"full-access"}}
	ctx := context.WithValue(context.Background(), botKey, bot)
	if _, text, isErr := callCalendarTool(t, srv, ctx, nil); !isErr || !strings.Contains(text, "sandbox access") {
		t.Errorf("%v %s", isErr, text)
	}
}
