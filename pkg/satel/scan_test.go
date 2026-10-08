package satel

import (
	"bytes"
	"errors"
	"log"
	"net"
	"os"
	"strings"
	"testing"
	"time"
)

func TestTestConnection(t *testing.T) {
	fake := newFakeIntegra(t, "")
	info, err := TestConnection("127.0.0.1", fake.port(), "", 2*time.Second)
	if err != nil {
		t.Fatalf("TestConnection: %v", err)
	}
	if !strings.Contains(info, "INTEGRA 128") || !strings.Contains(info, "v1.23") {
		t.Errorf("info = %q", info)
	}
}

func TestTestConnectionEncrypted(t *testing.T) {
	fake := newFakeIntegra(t, "klucz123")

	info, err := TestConnection("127.0.0.1", fake.port(), "klucz123", 2*time.Second)
	if err != nil {
		t.Fatalf("TestConnection (encrypted): %v", err)
	}
	if !strings.Contains(info, "INTEGRA 128") {
		t.Errorf("info = %q", info)
	}

	// Wrong key must fail with a key hint, not hang.
	if _, err := TestConnection("127.0.0.1", fake.port(), "zly_klucz", 2*time.Second); err == nil {
		t.Error("expected error for wrong integration key")
	}
}

func TestTestConnectionRefused(t *testing.T) {
	if _, err := TestConnection("127.0.0.1", 1, "", time.Second); err == nil {
		t.Error("expected connection error")
	}
}

func TestPanelScan(t *testing.T) {
	fake := newFakeIntegra(t, "")
	fake.names[[2]byte{deviceTypeZone, 1}] = "Czujka Salon"
	fake.names[[2]byte{deviceTypeZone, 7}] = "Kontaktron"
	fake.names[[2]byte{deviceTypePartition, 1}] = "Parter"
	fake.names[[2]byte{deviceTypeOutput, 3}] = "Syrena"

	p := New(Config{
		Host:         "127.0.0.1",
		Port:         fake.port(),
		PollInterval: 50 * time.Millisecond,
		Timeout:      2 * time.Second,
	})
	p.Start()
	defer p.Stop()

	waitEvent(t, p, "connected", func(e Event) bool { return e.Type == EventConnection && e.Connected })

	res, err := p.Scan(30 * time.Second)
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if res.PanelType != "INTEGRA 128" {
		t.Errorf("PanelType = %q", res.PanelType)
	}
	// Fake replies 0xEF for unknown devices, so only named ones come back.
	if len(res.Zones) != 2 || res.Zones[0].Name != "Czujka Salon" || res.Zones[1].Number != 7 {
		t.Errorf("Zones = %+v", res.Zones)
	}
	if len(res.Partitions) != 1 || res.Partitions[0].Name != "Parter" {
		t.Errorf("Partitions = %+v", res.Partitions)
	}
	if len(res.Outputs) != 1 || res.Outputs[0].Number != 3 || res.Outputs[0].Name != "Syrena" {
		t.Errorf("Outputs = %+v", res.Outputs)
	}

	// Polling still works after the scan.
	fake.setViolated(2, true)
	waitEvent(t, p, "zone after scan", func(e Event) bool {
		return e.Type == EventZone && e.Number == 2 && e.Zone != nil && e.Zone.Violated
	})
}

func TestPanelScanDisconnected(t *testing.T) {
	p := New(Config{Host: "127.0.0.1", Port: 1})
	if _, err := p.Scan(time.Second); err != ErrNotConnected {
		t.Errorf("expected ErrNotConnected, got %v", err)
	}
}

// startSlowScan starts a scan against a fake panel whose name reads take wait each.
// Returns the panel and the scan result channel.
func startSlowScan(t *testing.T, fake *fakeIntegra, wait time.Duration) (*Panel, chan scanReply) {
	t.Helper()
	fake.mu.Lock()
	fake.nameWait = wait
	fake.mu.Unlock()

	p := New(Config{
		Host:         "127.0.0.1",
		Port:         fake.port(),
		PollInterval: 50 * time.Millisecond,
		Timeout:      2 * time.Second,
		UserCode:     "1234",
	})
	p.Start()
	t.Cleanup(p.Stop)
	waitEvent(t, p, "connected", func(e Event) bool { return e.Type == EventConnection && e.Connected })

	done := make(chan scanReply, 1)
	go func() {
		res, err := p.Scan(30 * time.Second)
		done <- scanReply{result: res, err: err}
	}()
	deadline := time.Now().Add(2 * time.Second)
	for !p.ScanProgress().Running {
		if time.Now().After(deadline) {
			t.Fatal("scan did not start")
		}
		time.Sleep(5 * time.Millisecond)
	}
	return p, done
}

func TestPanelScanProgress(t *testing.T) {
	fake := newFakeIntegra(t, "")
	p, done := startSlowScan(t, fake, 2*time.Millisecond)

	sp := p.ScanProgress()
	if sp.Total != 128+32+128 {
		t.Errorf("Total = %d, want 288 (INTEGRA 128: zones + 32 partitions + outputs)", sp.Total)
	}
	phases := map[string]bool{}
	for {
		select {
		case r := <-done:
			if r.err != nil {
				t.Fatalf("Scan: %v", r.err)
			}
			if got := p.ScanProgress(); got.Running || got.Done != got.Total {
				t.Errorf("after scan: %+v, want not running and done == total", got)
			}
			for _, ph := range []string{"zones", "partitions", "outputs"} {
				if !phases[ph] {
					t.Errorf("phase %q never reported (saw %v)", ph, phases)
				}
			}
			return
		default:
			phases[p.ScanProgress().Phase] = true
			time.Sleep(time.Millisecond)
		}
	}
}

func TestPanelScanRefusesSecondScan(t *testing.T) {
	fake := newFakeIntegra(t, "")
	p, done := startSlowScan(t, fake, 2*time.Millisecond)

	if _, err := p.Scan(time.Second); err == nil || !strings.Contains(err.Error(), "already running") {
		t.Errorf("second scan: err = %v, want 'already running'", err)
	}
	if r := <-done; r.err != nil {
		t.Fatalf("first scan: %v", r.err)
	}
}

// Names come from the panel in Windows-1250; Polish letters must survive into HA.
func TestDecodeDeviceNamePolish(t *testing.T) {
	cases := map[string]string{
		"PIR WIATRO\xA3AP  ":                 "PIR WIATROŁAP",
		"PIR 1 GARA\xAF\x00\x00":             "PIR 1 GARAŻ",
		"OKNO \xA3AZIENKA 1P":                "OKNO ŁAZIENKA 1P",
		"BRAMA TY\xA3      ":                 "BRAMA TYŁ",
		"\xbf\xf3\xb3w \x9c\xe6\xea\xb9\xf1": "żółw śćęąń",
		"Parter          ":                   "Parter",
	}
	for raw, want := range cases {
		if got := decodeDeviceName([]byte(raw)); got != want {
			t.Errorf("decodeDeviceName(%q) = %q, want %q", raw, got, want)
		}
	}
}

// A panel that stops answering must produce an error naming the command.
func TestExecuteTimeoutNamesCommand(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		c, err := ln.Accept()
		if err == nil {
			defer c.Close()
			time.Sleep(2 * time.Second) // accept, never answer
		}
	}()

	conn, err := dialPanel("127.0.0.1", ln.Addr().(*net.TCPAddr).Port, "", 300*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.close()
	_, err = conn.execute(cmdReadDeviceName, []byte{deviceTypeZone, 73})
	if err == nil || !strings.Contains(err.Error(), "no response to 0xEE 0149") {
		t.Errorf("err = %v, want it to name command 0xEE and its data", err)
	}
}

// The scan logs a one-line summary used to diagnose ETHM-1 timing.
func TestPanelScanLogsSummary(t *testing.T) {
	var buf bytes.Buffer
	log.SetOutput(&buf)
	defer log.SetOutput(os.Stderr)

	fake := newFakeIntegra(t, "")
	fake.names[[2]byte{deviceTypeZone, 1}] = "Czujka Salon"
	_, done := startSlowScan(t, fake, 0)
	if r := <-done; r.err != nil {
		t.Fatalf("Scan: %v", r.err)
	}
	out := buf.String()
	for _, want := range []string{"[SATEL] scan: 288 replies (1 names, 0xEF codes map[3:287])", "skipped frames 0"} {
		if !strings.Contains(out, want) {
			t.Errorf("log missing %q:\n%s", want, out)
		}
	}
}

// A control command that gets no answer must not put the user code (PIN) into the
// error: it ends up in logs, result events and MCP-readable log buffers.
func TestExecuteErrorHidesUserCode(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		c, err := ln.Accept()
		if err == nil {
			defer c.Close()
			time.Sleep(2 * time.Second) // accept, never answer
		}
	}()

	conn, err := dialPanel("127.0.0.1", ln.Addr().(*net.TCPAddr).Port, "", 300*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.close()
	code, err := EncodeUserCode("4711")
	if err != nil {
		t.Fatal(err)
	}
	_, err = conn.execute(cmdDisarm, append(code[:], 0x01, 0, 0, 0))
	if err == nil {
		t.Fatal("expected a timeout error")
	}
	if strings.Contains(err.Error(), "4711") {
		t.Errorf("error leaks the user code: %v", err)
	}
	if !strings.Contains(err.Error(), "no response to 0x84") {
		t.Errorf("error should still name the command: %v", err)
	}
}

// Two scans requested at the same moment (two browser tabs, API, MCP): only one runs.
func TestPanelScanConcurrentRequests(t *testing.T) {
	fake := newFakeIntegra(t, "")
	fake.mu.Lock()
	fake.nameWait = time.Millisecond
	fake.mu.Unlock()
	p := New(Config{Host: "127.0.0.1", Port: fake.port(), PollInterval: 50 * time.Millisecond, Timeout: 2 * time.Second})
	p.Start()
	defer p.Stop()
	waitEvent(t, p, "connected", func(e Event) bool { return e.Type == EventConnection && e.Connected })

	errs := make(chan error, 2)
	for i := 0; i < 2; i++ {
		go func() {
			_, err := p.Scan(30 * time.Second)
			errs <- err
		}()
	}
	ok, refused := 0, 0
	for i := 0; i < 2; i++ {
		switch err := <-errs; {
		case err == nil:
			ok++
		case errors.Is(err, ErrScanRunning):
			refused++
		default:
			t.Errorf("unexpected error: %v", err)
		}
	}
	if ok != 1 || refused != 1 {
		t.Errorf("ok=%d refused=%d, want exactly one scan and one refusal", ok, refused)
	}
	// After both finished a new scan is accepted again
	if _, err := p.Scan(30 * time.Second); err != nil {
		t.Errorf("scan after the first finished: %v", err)
	}
}

// Stop during a long scan must end it promptly, not wait out the whole scan.
func TestPanelStopDuringScan(t *testing.T) {
	fake := newFakeIntegra(t, "")
	p, done := startSlowScan(t, fake, 20*time.Millisecond) // ~6 s scan

	start := time.Now()
	p.Stop()
	if d := time.Since(start); d > 2*time.Second {
		t.Errorf("Stop took %v during a scan", d)
	}
	if r := <-done; r.err == nil {
		t.Error("interrupted scan should return an error")
	}
	if p.ScanProgress().Running {
		t.Error("scan still marked running after Stop")
	}
}
