package cloud

import (
	"testing"
	"time"
)

// The cloud page's connection: pings in the window, outages newest first with the last reason,
// and the share of the window connected.
func TestLinkReport(t *testing.T) {
	t0 := time.Date(2026, 11, 8, 0, 0, 0, 0, time.UTC)
	l := newLinkLog(t0)
	if r := l.report(t0.Add(time.Hour)); r.Availability != -1 || len(r.Samples) != 0 {
		t.Fatalf("nothing measured yet = %+v", r)
	}

	l.ping(t0.Add(time.Minute), 42*time.Millisecond, true)
	l.ping(t0.Add(2*time.Minute), 0, false)
	l.down(t0.Add(10*time.Hour), "connection lost")
	l.down(t0.Add(10*time.Hour+time.Second), "cloud closed the connection (1012)")
	l.up(t0.Add(10*time.Hour + 6*time.Minute))
	l.down(t0.Add(30*time.Hour), "connection lost") // still open

	r := l.report(t0.Add(30*time.Hour + 6*time.Minute))
	if !r.Since.Equal(t0.Add(6*time.Hour + 6*time.Minute)) {
		t.Errorf("since = %v, want 24 h back", r.Since)
	}
	if len(r.Samples) != 0 {
		t.Errorf("pings older than the window kept: %+v", r.Samples)
	}
	if len(r.Outages) != 2 || !r.Outages[0].To.IsZero() || r.Outages[1].Reason != "cloud closed the connection (1012)" {
		t.Fatalf("outages = %+v", r.Outages)
	}
	// 12 minutes down out of 24 hours
	if want := 1 - 12.0/(24*60); r.Availability < want-1e-9 || r.Availability > want+1e-9 {
		t.Errorf("availability = %v, want %v", r.Availability, want)
	}

	r = l.report(t0.Add(3 * time.Minute))
	if len(r.Samples) != 2 || r.Samples[0].RTT != 42 || r.Samples[1].OK {
		t.Errorf("samples = %+v", r.Samples)
	}
}
