package history

import (
	"context"
	"fmt"
	"testing"
	"time"
)

// Three days of history: two on disk (one written, one pending), paged
// through newest first without gaps or duplicates.
func TestSearch_PagesAcrossDiskAndMemory(t *testing.T) {
	dir := t.TempDir()
	c := &clock{t: time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC)}
	s := diskStoreAt(t, dir, c)
	want := 0
	for day := 0; day < 3; day++ {
		for i := 0; i < 40; i++ {
			c.t = c.t.Add(time.Minute)
			s.Record(Entry{Path: fmt.Sprintf("o%d", i%4), Old: want, Value: want + 1, Actor: fmt.Sprintf("a%d", i%2)})
			want++
		}
		if day < 2 {
			s.Flush() // the last day stays pending
		}
		c.t = c.t.Add(24 * time.Hour)
	}
	// memory only keeps 24 h: older days come from disk
	seen := map[string]bool{}
	var last Entry
	cursor, pages := "", 0
	for {
		res, err := s.Search(SearchQuery{Limit: 25, Cursor: cursor})
		if err != nil {
			t.Fatal(err)
		}
		pages++
		for _, e := range res.Entries {
			if seen[e.ID] {
				t.Fatalf("duplicate %s", e.ID)
			}
			if last.ID != "" && !newer(last, e) {
				t.Fatalf("order broken: %v then %v", last.AtMs, e.AtMs)
			}
			seen[e.ID], last = true, e
		}
		if res.Next == "" {
			break
		}
		cursor = res.Next
	}
	if len(seen) != want || pages != 5 {
		t.Fatalf("got %d entries in %d pages, want %d in 5", len(seen), pages, want)
	}
}

func TestSearch_Filters(t *testing.T) {
	dir := t.TempDir()
	c := &clock{t: time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)}
	s := diskStoreAt(t, dir, c)
	s.Record(Entry{Path: "light", Old: 0, Value: 1, Origin: "lua", Actor: "automation:wiatrolap", CorrelationID: "c-1"})
	c.t = c.t.Add(time.Minute)
	s.Record(Entry{Path: "light", Old: 1, Value: 0, Origin: "grenton", Actor: "device:CLU1"})
	c.t = c.t.Add(time.Minute)
	s.Record(Entry{Path: "zone", Old: 0, Value: 1, Origin: "physical", Actor: "device:satel"})
	s.Flush()
	mid := c.t.Add(-90 * time.Second).UnixMilli()

	count := func(q SearchQuery) int {
		res, err := s.Search(q)
		if err != nil {
			t.Fatal(err)
		}
		return len(res.Entries)
	}
	cases := map[string]struct {
		q    SearchQuery
		want int
	}{
		"path":    {SearchQuery{Path: "light"}, 2},
		"origin":  {SearchQuery{Origin: "physical"}, 1},
		"actor":   {SearchQuery{Actor: "automation:wiatrolap"}, 1},
		"text":    {SearchQuery{Text: "WIATROLAP"}, 1},
		"corr":    {SearchQuery{Text: "c-1"}, 1},
		"since":   {SearchQuery{Since: mid}, 2},
		"until":   {SearchQuery{Until: mid}, 1},
		"allowed": {SearchQuery{Allow: func(p string) bool { return p != "zone" }}, 2},
	}
	for name, c := range cases {
		if got := count(c.q); got != c.want {
			t.Errorf("%s: %d, want %d", name, got, c.want)
		}
	}
	if _, err := s.Search(SearchQuery{Cursor: "garbage!"}); err == nil {
		t.Error("invalid cursor accepted")
	}
}

func TestSearch_MemoryOnly(t *testing.T) {
	s, _ := newStore(DefaultConfig())
	s.Record(Entry{Path: "a", Old: 0, Value: 1})
	res, err := s.Search(SearchQuery{})
	if err != nil || len(res.Entries) != 1 || res.Next != "" {
		t.Fatalf("%+v %v", res, err)
	}
}

// A rare object with one change 30 days ago: no call reads more than
// maxDaysPerSearch files, and following Next finds it.
func TestSearch_DayBudgetContinues(t *testing.T) {
	dir := t.TempDir()
	c := &clock{t: time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)}
	s := diskStoreAt(t, dir, c)
	s.Record(Entry{Path: "rare", Old: 0, Value: 1})
	for day := 0; day < 30; day++ {
		c.t = c.t.Add(24 * time.Hour)
		s.Record(Entry{Path: "busy", Old: day, Value: day + 1})
		s.Flush()
	}
	cursor, calls, found := "", 0, 0
	for {
		res, err := s.Search(SearchQuery{Path: "rare", Cursor: cursor})
		if err != nil {
			t.Fatal(err)
		}
		calls++
		found += len(res.Entries)
		if res.Next == "" || calls > 10 {
			break
		}
		cursor = res.Next
	}
	if found != 1 || calls < 2 {
		t.Fatalf("found %d in %d calls", found, calls)
	}
}

func TestSearch_DisabledIsEmpty(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Enabled = false
	s := New(cfg, nil)
	res, err := s.Search(SearchQuery{})
	if err != nil || len(res.Entries) != 0 {
		t.Fatalf("%+v %v", res, err)
	}
}

func TestSearch_CancelledContext(t *testing.T) {
	dir := t.TempDir()
	c := &clock{t: time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)}
	s := diskStoreAt(t, dir, c)
	s.Record(Entry{Path: "a", Old: 0, Value: 1})
	s.Flush()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := s.Search(SearchQuery{Ctx: ctx}); err == nil {
		t.Fatal("cancelled search must stop")
	}
}
