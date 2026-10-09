package homekit

import (
	"context"
	"log"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	dnssdlog "github.com/brutella/dnssd/log"
)

// dnssd (pulled in by hap for mDNS advertising) watches network interfaces via
// netlink. When the netlink receive goroutine dies — typically ENOBUFS on a
// burst of link events, e.g. a Docker host churning veth pairs — it closes the
// update channel on the way out (netlink/link_linux.go: `defer close(ch)`), and
// dnssd's linkSubscribe reads that closed channel without checking `ok`:
//
//	case update := <-ch:
//	    iface, err := net.InterfaceByIndex(int(update.Index))  // Index == 0
//	    if err != nil { log.Info.Println(err); continue }
//
// The result is a tight loop burning a core and emitting the same line at
// ~1000/s (observed: 4 GB of journal, 4 days of CPU over 13 days of uptime).
// dnssd v1.2.14 is the latest release and does not fix this, and the spinning
// goroutine belongs to hap's responder, so we cannot stop it directly — the
// only lever we have is restarting the bridge, which builds a fresh responder
// with a fresh netlink subscription.
//
// So: filter dnssd's logger to keep the flood out of the journal, count the
// spin, and restart the bridge when the rate says we are looping rather than
// seeing genuine interface churn.
const netlinkSpinMarker = "invalid network interface index"

const (
	// dnssdSpinReportInterval caps how often a suppressed spin line is echoed.
	dnssdSpinReportInterval = time.Minute
	// dnssdSpinCheckInterval is how often the watchdog samples the counter.
	dnssdSpinCheckInterval = 15 * time.Second
	// dnssdSpinThreshold is the number of spin messages per check interval that
	// means a loop. Real link churn produces a handful; the loop produces
	// thousands.
	dnssdSpinThreshold = 100
	// dnssdRecoverCooldown keeps a persistently broken netlink from turning
	// into a bridge-restart loop.
	dnssdRecoverCooldown = 5 * time.Minute
)

var (
	dnssdWriter       = &dnssdLogWriter{}
	dnssdInstallOnce  sync.Once
	lastDnssdRecovery atomic.Int64 // unix nanos, 0 = never
)

// dnssdLogWriter receives dnssd's log.Info output. Spin lines are counted and
// throttled to one per dnssdSpinReportInterval; everything else is forwarded.
type dnssdLogWriter struct {
	spins atomic.Int64

	mu         sync.Mutex
	suppressed int
	lastReport time.Time
}

func (w *dnssdLogWriter) Write(p []byte) (int, error) {
	line := strings.TrimRight(string(p), "\n")

	if !strings.Contains(line, netlinkSpinMarker) {
		log.Printf("[HomeKit/dnssd] %s", line)
		return len(p), nil
	}

	w.spins.Add(1)

	w.mu.Lock()
	w.suppressed++
	count := w.suppressed
	now := time.Now()
	report := w.lastReport.IsZero() || now.Sub(w.lastReport) >= dnssdSpinReportInterval
	if report {
		w.suppressed = 0
		w.lastReport = now
	}
	w.mu.Unlock()

	if report {
		log.Printf("[HomeKit] dnssd netlink watch looping (%d message(s) since last report): %s", count, line)
	}
	return len(p), nil
}

// installDnssdLogFilter redirects dnssd's info logger into dnssdWriter. dnssd's
// logger is a process-wide global, so this is done once.
func installDnssdLogFilter() {
	dnssdInstallOnce.Do(func() {
		dnssdlog.Info.SetOutput(dnssdWriter)
	})
}

// allowDnssdRecovery reports whether a bridge restart may be triggered now,
// enforcing dnssdRecoverCooldown. It uses an atomic so it never waits on s.mu.
func allowDnssdRecovery(now time.Time) bool {
	prev := lastDnssdRecovery.Load()
	if prev != 0 && now.Sub(time.Unix(0, prev)) < dnssdRecoverCooldown {
		return false
	}
	return lastDnssdRecovery.CompareAndSwap(prev, now.UnixNano())
}

// startDnssdWatchdog samples the spin counter and restarts the bridge when the
// rate indicates a loop.
func (s *Server) startDnssdWatchdog(ctx context.Context) {
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()

		ticker := time.NewTicker(dnssdSpinCheckInterval)
		defer ticker.Stop()

		last := dnssdWriter.spins.Load()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				current := dnssdWriter.spins.Load()
				delta := current - last
				last = current

				if delta < dnssdSpinThreshold {
					continue
				}
				if !allowDnssdRecovery(time.Now()) {
					log.Printf("[HomeKit] dnssd netlink watch still looping (%d in %s) — restart on cooldown", delta, dnssdSpinCheckInterval)
					continue
				}

				log.Printf("[HomeKit] dnssd netlink watch looping (%d in %s) — restarting bridge to renew the subscription", delta, dnssdSpinCheckInterval)
				// Detached: Reload() -> Stop() waits on s.wg, which this
				// goroutine is part of, so it must return first.
				go func() {
					if err := s.reloadUnlessStopped(); err != nil {
						log.Printf("[HomeKit] bridge restart after dnssd loop failed: %v", err)
					}
				}()
				return
			}
		}
	}()
}
