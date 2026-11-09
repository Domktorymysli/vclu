package cloud

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"sync"
	"time"

	"github.com/Domktorymysli/vclu-cloud/proto"
)

// Statistics read back (protocol section 10, "Reading them back"): a chart longer than vCLU keeps
// takes the older part from what it sent the cloud. One query runs at a time and the answers are
// cached for historyTTL, so a dashboard opened by many people, or reloaded, asks the cloud once;
// the part newer than the cached answer comes from vCLU's own files.

const (
	historyTTL     = 10 * time.Minute
	historyEntries = 64
	historyWait    = 10 * time.Second
)

// ErrNoHistory says the cloud has nothing to answer with now: no connection, statistics off,
// or it asked to wait. Charts then draw what vCLU keeps.
var ErrNoHistory = errors.New("cloud: statistics history not available")

type metricsHistory struct {
	mu      sync.Mutex // one query at a time; held while waiting for the cloud
	home    int64
	entries map[string]*historyEntry
	retryAt time.Time
}

type historyEntry struct {
	since, until int64
	points       [][5]float64
	at           time.Time
}

// MetricsHistory returns what the cloud stored of path from since to until (unix seconds), in
// steps of step seconds or more (proto.MetricsQuery), and how far it reaches: a cached answer
// can end before until, and the caller takes the rest from its own samples.
func (c *Client) MetricsHistory(ctx context.Context, path string, since, until int64, step int) ([][5]float64, int64, error) {
	if step < proto.MetricsBucketSeconds || step > proto.MaxMetricsStepS {
		return nil, 0, fmt.Errorf("cloud: step %d out of range", step)
	}
	c.mu.Lock()
	t := c.tun
	c.mu.Unlock()
	if t == nil || !t.metricsQuery {
		return nil, 0, ErrNoHistory
	}
	h := &c.history
	h.mu.Lock()
	defer h.mu.Unlock()
	now := c.clock()
	if h.home != t.home {
		h.home, h.entries, h.retryAt = t.home, map[string]*historyEntry{}, time.Time{}
	}
	key := path + "\x00" + strconv.Itoa(step)
	if e := h.entries[key]; e != nil && e.since <= since && now.Sub(e.at) < historyTTL {
		return pointsFrom(e.points, since, min(until, e.until)), min(until, e.until), nil
	}
	if now.Before(h.retryAt) {
		return nil, 0, ErrNoHistory
	}

	from := since / int64(step) * int64(step) // the same start for a while, so later requests hit the cache
	q := proto.MetricsQuery{Type: proto.TypeMetricsQuery, ID: randomID(), Path: path, Since: from, Until: until, StepS: step}
	select {
	case <-t.metricsPoints: // a late answer to a query that timed out
	default:
	}
	if err := t.send(q); err != nil {
		return nil, 0, ErrNoHistory
	}
	timeout := time.NewTimer(historyWait)
	defer timeout.Stop()
	var res proto.MetricsPoints
	for res.ID != q.ID {
		select {
		case res = <-t.metricsPoints:
		case <-timeout.C:
			h.retryAt = now.Add(time.Minute)
			return nil, 0, ErrNoHistory
		case <-ctx.Done():
			return nil, 0, ctx.Err()
		}
	}
	if res.Error != "" {
		if res.RetryAfterS > 0 {
			h.retryAt = now.Add(time.Duration(res.RetryAfterS) * time.Second)
		} else if res.Error == proto.MetricsOff {
			h.retryAt = now.Add(historyTTL)
		}
		return nil, 0, ErrNoHistory
	}
	if len(h.entries) >= historyEntries {
		var oldest string
		for k, e := range h.entries {
			if oldest == "" || e.at.Before(h.entries[oldest].at) {
				oldest = k
			}
		}
		delete(h.entries, oldest)
	}
	h.entries[key] = &historyEntry{since: from, until: until, points: res.Points, at: now}
	return pointsFrom(res.Points, since, until), until, nil
}

// pointsFrom are the points from since to until.
func pointsFrom(pts [][5]float64, since, until int64) [][5]float64 {
	var out [][5]float64
	for _, p := range pts {
		if at := int64(p[0]); at >= since && at < until {
			out = append(out, p)
		}
	}
	return out
}
