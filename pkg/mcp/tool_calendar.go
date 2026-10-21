package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"

	mcplib "github.com/mark3labs/mcp-go/mcp"
)

var (
	calendarIDRe   = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,40}$`)
	calendarDateRe = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)
)

// calendarMaxDays bounds one agenda request.
const calendarMaxDays = 62

func (s *MCPServer) registerCalendarTool() {
	s.addTool("vclu_calendar", "The house calendar: events day by day (holidays, waste collection, reminders), "+
		"schedules with their value now, next change and, for a single day, their hours; sun times for a single day. "+
		"Read only: to add or change an event, write a module draft (Calendar in ai/examples.md).", s.handleCalendar,
		map[string]any{
			"calendar": map[string]any{"type": "string", "description": "Calendar id (optional; all calendars when omitted)"},
			"date":     map[string]any{"type": "string", "description": "One day, YYYY-MM-DD, or today / tomorrow (adds schedule hours, sun times, name days)"},
			"from":     map[string]any{"type": "string", "description": "First day, YYYY-MM-DD (default today)"},
			"to":       map[string]any{"type": "string", "description": "Last day, YYYY-MM-DD (default from + 6 days, at most 62 days)"},
			"tags": map[string]any{"type": "array", "items": map[string]any{"type": "string"},
				"description": "Only events with one of these tags (optional)"},
		}, nil)
}

// handleCalendar returns the agenda of the house calendars for a day or a range of days.
func (s *MCPServer) handleCalendar(ctx context.Context, req mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
	start := time.Now()
	if err := s.requireDevMode(ctx); err != nil {
		return err, nil
	}
	if s.executor == nil {
		return toolError("Lua executor not configured"), nil
	}
	script, err := calendarScript(req.GetArguments())
	if err != nil {
		return toolError(err.Error()), nil
	}
	raw, err := s.executor.Execute(script)
	if err != nil {
		s.recordActivity("vclu_calendar", "", false, start, err.Error(), ctx)
		return toolError(err.Error()), nil
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		s.recordActivity("vclu_calendar", "", false, start, "bad JSON from Lua", ctx)
		return toolError("calendar answer is not JSON: " + raw), nil
	}
	if msg, ok := out["error"].(string); ok {
		s.recordActivity("vclu_calendar", "", false, start, msg, ctx)
		b, _ := json.Marshal(out)
		return toolError(string(b)), nil
	}
	s.recordActivity("vclu_calendar", "", true, start, "", ctx)
	out["env"] = "runtime"
	return toolJSON(out)
}

// calendarScript checks the arguments and builds the Lua call. Lua resolves today, tomorrow
// and the default range, in the runtime's zone.
func calendarScript(args map[string]any) (string, error) {
	str := func(name string) (string, error) {
		v, _ := args[name].(string)
		if v == "" || v == "today" || v == "tomorrow" || calendarDateRe.MatchString(v) {
			return v, nil
		}
		return "", fmt.Errorf("%s: expected YYYY-MM-DD, today or tomorrow", name)
	}
	lit := func(v string) string {
		if v == "" {
			return "nil"
		}
		return luaQuote(v)
	}
	id, _ := args["calendar"].(string)
	if id != "" && !calendarIDRe.MatchString(id) {
		return "", fmt.Errorf("calendar: letters, digits, _ - . (max 40)")
	}
	from, err := str("from")
	if err != nil {
		return "", err
	}
	to, err := str("to")
	if err != nil {
		return "", err
	}
	date, err := str("date")
	if err != nil {
		return "", err
	}
	if date != "" {
		if from != "" || to != "" {
			return "", fmt.Errorf("give date or from/to, not both")
		}
		from, to = date, date
	}
	tags := "nil"
	if raw, ok := args["tags"].([]any); ok && len(raw) > 0 {
		var quoted []string
		for _, v := range raw {
			tag, ok := v.(string)
			if !ok || !calendarIDRe.MatchString(tag) {
				return "", fmt.Errorf("tags: letters, digits, _ - . (max 40)")
			}
			quoted = append(quoted, luaQuote(tag))
		}
		tags = "{" + strings.Join(quoted, ", ") + "}"
	}
	return fmt.Sprintf("return Calendar.API.agenda(%s, %s, %s, %s, %d)", lit(id), lit(from), lit(to), tags, calendarMaxDays), nil
}
