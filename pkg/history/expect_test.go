package history

import (
	"testing"
	"time"
)

func TestTracker_MatchWithinWindow(t *testing.T) {
	c := &clock{t: time.Date(2026, 10, 7, 21, 0, 0, 0, time.UTC)}
	tr := NewTracker(c.now)
	due := c.t.Add(time.Minute).UnixMilli()
	tr.Expect(Expectation{Path: "light", Value: int64(0), DueAtMs: due, EarlyMs: 2000, LateMs: 15000, Actor: "automation:w"})

	e, ok := tr.Match("light", int64(0), due+12000)
	if !ok || e.Actor != "automation:w" {
		t.Fatalf("report 12 s late: %+v %v", e, ok)
	}
	if _, ok := tr.Match("light", int64(0), due+12000); ok {
		t.Error("an expectation matches once")
	}

	tr.Expect(Expectation{Path: "light", Value: int64(0), DueAtMs: due, EarlyMs: 2000, LateMs: 15000})
	if _, ok := tr.Match("light", int64(0), due-20000); ok {
		t.Error("a report 20 s early must not match (someone switched it off by hand)")
	}
	if _, ok := tr.Match("light", int64(0), due); ok {
		t.Error("an earlier unrelated change makes the expectation stale")
	}
}

func TestTracker_ReplaceForgetExpire(t *testing.T) {
	c := &clock{t: time.Date(2026, 10, 7, 21, 0, 0, 0, time.UTC)}
	tr := NewTracker(c.now)
	now := c.t.UnixMilli()
	tr.Expect(Expectation{Path: "light", Value: int64(0), DueAtMs: now + 60000, EarlyMs: 2000, LateMs: 15000, Actor: "first"})
	tr.Expect(Expectation{Path: "light", Value: int64(0), DueAtMs: now + 90000, EarlyMs: 2000, LateMs: 15000, Actor: "second"})
	// switchOn again replaces the earlier deadline
	if e, ok := tr.Match("light", int64(0), now+90000); !ok || e.Actor != "second" {
		t.Fatalf("replacement: %+v", e)
	}

	tr.Expect(Expectation{Path: "light", Value: int64(0), DueAtMs: now + 60000, EarlyMs: 2000, LateMs: 15000})
	tr.Forget("light")
	if _, ok := tr.Match("light", int64(0), now+60000); ok {
		t.Error("forgotten expectation matched")
	}

	tr.Expect(Expectation{Path: "old", Value: int64(0), DueAtMs: now, EarlyMs: 2000, LateMs: 15000})
	c.t = c.t.Add(time.Minute)
	tr.Expect(Expectation{Path: "x", Value: int64(0), DueAtMs: c.t.UnixMilli() + 1000, EarlyMs: 2000, LateMs: 15000})
	if len(tr.items) != 1 {
		t.Errorf("expired expectations kept: %d", len(tr.items))
	}
}
