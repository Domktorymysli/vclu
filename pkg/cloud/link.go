package cloud

import (
	"sync"
	"time"
)

// The connection as the cloud page shows it: the round trip to the cloud each PingEvery and the
// outages, for the last linkWindow. Kept in memory only: a restart starts it again.
const (
	linkWindow     = 24 * time.Hour
	linkMaxSamples = 24 * 60 // one a minute for a day
	linkMaxOutages = 50
)

// LinkSample is one ping; a failed one has OK false.
type LinkSample struct {
	At  time.Time `json:"at"`
	RTT float64   `json:"rttMs"`
	OK  bool      `json:"ok"`
}

// Outage is a time vCLU was paired and switched on but not connected; To is zero while it lasts.
type Outage struct {
	From   time.Time `json:"from"`
	To     time.Time `json:"to,omitzero"`
	Reason string    `json:"reason,omitempty"`
}

// LinkReport is what the cloud page draws.
type LinkReport struct {
	Since        time.Time    `json:"since"`        // where the window starts: 24 h ago, or this start
	Samples      []LinkSample `json:"samples"`      // oldest first
	Outages      []Outage     `json:"outages"`      // newest first, within the window
	Availability float64      `json:"availability"` // share of the window connected, 0..1; -1 with nothing to tell
}

type linkLog struct {
	mu      sync.Mutex
	started time.Time
	samples []LinkSample
	outages []Outage // oldest first
}

func newLinkLog(now time.Time) *linkLog { return &linkLog{started: now} }

func (l *linkLog) ping(at time.Time, rtt time.Duration, ok bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.samples = append(l.samples, LinkSample{At: at, RTT: float64(rtt.Microseconds()) / 1000, OK: ok})
	if over := len(l.samples) - linkMaxSamples; over > 0 {
		l.samples = append(l.samples[:0], l.samples[over:]...)
	}
}

// down opens an outage unless one is open; a later reason replaces an earlier one, since the
// first try often only says the connection was lost.
func (l *linkLog) down(at time.Time, reason string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if n := len(l.outages); n > 0 && l.outages[n-1].To.IsZero() {
		l.outages[n-1].Reason = reason
		return
	}
	l.outages = append(l.outages, Outage{From: at, Reason: reason})
	if over := len(l.outages) - linkMaxOutages; over > 0 {
		l.outages = append(l.outages[:0], l.outages[over:]...)
	}
}

// up closes the open outage, if any.
func (l *linkLog) up(at time.Time) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if n := len(l.outages); n > 0 && l.outages[n-1].To.IsZero() {
		l.outages[n-1].To = at
	}
}

// report covers the window up to now. Availability counts only the time vCLU was trying to be
// connected: before the first connection or attempt there is nothing to tell (-1).
func (l *linkLog) report(now time.Time) LinkReport {
	l.mu.Lock()
	defer l.mu.Unlock()
	since := now.Add(-linkWindow)
	if l.started.After(since) {
		since = l.started
	}
	r := LinkReport{Since: since, Samples: []LinkSample{}, Outages: []Outage{}, Availability: -1}
	for _, s := range l.samples {
		if !s.At.Before(since) {
			r.Samples = append(r.Samples, s)
		}
	}
	var downFor time.Duration
	for i := len(l.outages) - 1; i >= 0; i-- {
		o := l.outages[i]
		to := o.To
		if to.IsZero() {
			to = now
		}
		if to.Before(since) {
			break
		}
		r.Outages = append(r.Outages, o)
		from := o.From
		if from.Before(since) {
			from = since
		}
		downFor += to.Sub(from)
	}
	if total := now.Sub(since); total > 0 && (len(r.Samples) > 0 || len(r.Outages) > 0) {
		r.Availability = 1 - float64(downFor)/float64(total)
	}
	return r
}
