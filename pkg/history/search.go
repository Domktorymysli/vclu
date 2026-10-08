package history

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
)

// SearchQuery selects entries from memory and disk. Empty fields mean "any".
type SearchQuery struct {
	Path   string
	Origin string
	Actor  string
	Text   string // case-insensitive, in path, actor, origin, correlation id and values
	Since  int64  // atMs, inclusive
	Until  int64  // atMs, exclusive
	Limit  int    // default 100, max 1000
	Cursor string // Next of the previous page
	// Allow hides entries of objects the caller may not read (MCP bots)
	Allow func(path string) bool
	// Ctx stops the search when the caller goes away (nil = never)
	Ctx context.Context
}

// maxDaysPerSearch bounds the files one call reads: a query that matches
// nothing would otherwise parse 90 days of files on a Pi. A page that ends
// there has fewer entries and a Next cursor to continue further back.
const maxDaysPerSearch = 14

// SearchResult is one page, newest first; Next is empty on the last page.
type SearchResult struct {
	Entries []Entry `json:"entries"`
	Next    string  `json:"next,omitempty"`
}

// Search pages through the history, newest first, ordered by (atMs, id).
// Disk files are read one day at a time, newest first, only until the page
// is full, and merged with memory and the entries waiting for disk,
// deduplicated by id.
func (s *Store) Search(q SearchQuery) (SearchResult, error) {
	if !s.Config().Enabled {
		return SearchResult{Entries: []Entry{}}, nil
	}
	limit := q.Limit
	if limit <= 0 {
		limit = DefaultQueryLimit
	}
	if limit > MaxQueryLimit {
		limit = MaxQueryLimit
	}
	curAt, curID, err := parseCursor(q.Cursor)
	if err != nil {
		return SearchResult{}, err
	}
	until := q.Until
	if curAt > 0 && (until == 0 || curAt+1 < until) {
		until = curAt + 1 // the cursor's millisecond is filtered by id below
	}

	// memory, grouped by day
	s.mu.Lock()
	s.expire(s.now().UnixMilli())
	memByDay := map[string][]Entry{}
	for _, e := range s.order {
		if e.ID != "" {
			memByDay[dayOf(e.AtMs)] = append(memByDay[dayOf(e.AtMs)], *e)
		}
	}
	// not written yet and possibly already out of memory (disk failing)
	for _, e := range s.pending {
		memByDay[dayOf(e.AtMs)] = append(memByDay[dayOf(e.AtMs)], e)
	}
	disk := s.disk
	s.mu.Unlock()

	daySet := map[string]bool{}
	for d := range memByDay {
		daySet[d] = true
	}
	if disk != nil {
		for _, d := range disk.days() {
			daySet[d] = true
		}
	}
	days := make([]string, 0, len(daySet))
	for d := range daySet {
		days = append(days, d)
	}
	sort.Sort(sort.Reverse(sort.StringSlice(days)))

	text := strings.ToLower(q.Text)
	match := func(e Entry) bool {
		if (q.Since > 0 && e.AtMs < q.Since) || (until > 0 && e.AtMs >= until) {
			return false
		}
		if curAt > 0 && e.AtMs == curAt && e.ID >= curID {
			return false
		}
		if (q.Path != "" && e.Path != q.Path) || (q.Origin != "" && e.Origin != q.Origin) || (q.Actor != "" && e.Actor != q.Actor) {
			return false
		}
		if text != "" {
			hay := strings.ToLower(fmt.Sprint(e.Path, " ", e.Actor, " ", e.Origin, " ", e.CorrelationID, " ", e.Value, " ", e.Old))
			if !strings.Contains(hay, text) {
				return false
			}
		}
		return q.Allow == nil || q.Allow(e.Path)
	}

	keep := rawFilter(q)
	var out []Entry
	read := 0
	for _, day := range days {
		if q.Since > 0 && day < dayOf(q.Since) {
			break
		}
		if until > 0 && day > dayOf(until) {
			continue
		}
		if q.Ctx != nil && q.Ctx.Err() != nil {
			return SearchResult{}, q.Ctx.Err()
		}
		if read == maxDaysPerSearch {
			// continue from the start of this day on the next call
			start, _ := time.Parse(dayLayout, day)
			sort.Slice(out, func(i, j int) bool { return newer(out[i], out[j]) })
			if len(out) > limit {
				break // the page is full anyway
			}
			return SearchResult{Entries: nonNil(out), Next: encodeCursor(start.Add(24*time.Hour).UnixMilli(), "")}, nil
		}
		seen := map[string]bool{}
		var batch []Entry
		add := func(e Entry) {
			if !seen[e.ID] && match(e) {
				seen[e.ID] = true
				batch = append(batch, e)
			}
		}
		for _, e := range memByDay[day] {
			add(e)
		}
		if disk != nil {
			entries, err := disk.readDay(day, keep)
			if err != nil {
				return SearchResult{}, err
			}
			read++
			for _, e := range entries {
				add(e)
			}
		}
		sort.Slice(batch, func(i, j int) bool { return newer(batch[i], batch[j]) })
		out = append(out, batch...)
		if len(out) > limit {
			break
		}
	}

	res := SearchResult{Entries: nonNil(out)}
	if len(out) > limit {
		res.Entries = out[:limit]
		last := out[limit-1]
		res.Next = encodeCursor(last.AtMs, last.ID)
	}
	return res, nil
}

func nonNil(e []Entry) []Entry {
	if e == nil {
		return []Entry{}
	}
	return e
}

// encodeCursor: the next page has entries older than (atMs, id); an empty
// id excludes the whole millisecond (continuing at a day boundary).
func encodeCursor(atMs int64, id string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(strconv.FormatInt(atMs, 10) + ":" + id))
}

// rawFilter rejects lines of other paths, origins or actors before decoding.
func rawFilter(q SearchQuery) func([]byte) bool {
	var needles [][]byte
	for field, v := range map[string]string{"path": q.Path, "origin": q.Origin, "actor": q.Actor} {
		if v != "" {
			b, _ := json.Marshal(v)
			needles = append(needles, append([]byte(`"`+field+`":`), b...))
		}
	}
	if len(needles) == 0 {
		return nil
	}
	return func(line []byte) bool {
		for _, n := range needles {
			if !bytes.Contains(line, n) {
				return false
			}
		}
		return true
	}
}

func newer(a, b Entry) bool {
	if a.AtMs != b.AtMs {
		return a.AtMs > b.AtMs
	}
	return a.ID > b.ID
}

func parseCursor(c string) (int64, string, error) {
	if c == "" {
		return 0, "", nil
	}
	raw, err := base64.RawURLEncoding.DecodeString(c)
	if err != nil {
		return 0, "", fmt.Errorf("invalid cursor")
	}
	at, id, ok := strings.Cut(string(raw), ":")
	n, err := strconv.ParseInt(at, 10, 64)
	if !ok || err != nil {
		return 0, "", fmt.Errorf("invalid cursor")
	}
	return n, id, nil
}
