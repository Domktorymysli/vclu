package homekit

import (
	"strings"
	"testing"
	"time"
)

func TestDnssdLogWriterCountsAndThrottlesSpin(t *testing.T) {
	w := &dnssdLogWriter{}
	line := []byte("INFO 2026/08/19 04:57:31 netlink_linux.go:29: route ip+net: invalid network interface index\n")

	for i := 0; i < 5000; i++ {
		if _, err := w.Write(line); err != nil {
			t.Fatalf("Write: %v", err)
		}
	}

	if got := w.spins.Load(); got != 5000 {
		t.Fatalf("spins = %d, want 5000", got)
	}

	// The first line reports immediately and resets the counter; the remaining
	// 4999 stay suppressed until dnssdSpinReportInterval elapses.
	w.mu.Lock()
	suppressed := w.suppressed
	w.mu.Unlock()
	if suppressed != 4999 {
		t.Fatalf("suppressed = %d, want 4999", suppressed)
	}
}

func TestDnssdLogWriterForwardsOtherLines(t *testing.T) {
	w := &dnssdLogWriter{}
	line := "INFO 2026/08/19 04:57:31 responder.go:80: no suitable interface\n"

	n, err := w.Write([]byte(line))
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	if n != len(line) {
		t.Fatalf("n = %d, want %d", n, len(line))
	}
	if got := w.spins.Load(); got != 0 {
		t.Fatalf("spins = %d, want 0 for a non-spin line", got)
	}
	if strings.Contains(line, netlinkSpinMarker) {
		t.Fatal("test fixture unexpectedly matches the spin marker")
	}
}

func TestAllowDnssdRecoveryCooldown(t *testing.T) {
	lastDnssdRecovery.Store(0)
	t.Cleanup(func() { lastDnssdRecovery.Store(0) })

	now := time.Date(2026, 8, 19, 12, 0, 0, 0, time.UTC)

	if !allowDnssdRecovery(now) {
		t.Fatal("first recovery should be allowed")
	}
	if allowDnssdRecovery(now.Add(dnssdRecoverCooldown - time.Second)) {
		t.Fatal("recovery within the cooldown should be refused")
	}
	if !allowDnssdRecovery(now.Add(dnssdRecoverCooldown)) {
		t.Fatal("recovery after the cooldown should be allowed")
	}
}
