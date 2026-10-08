package satel

import (
	"errors"
	"fmt"
	"log"
	"time"
)

// ScanDevice is one device discovered on the panel during a name scan.
type ScanDevice struct {
	Number int    `json:"number"`
	Name   string `json:"name"`
	Kind   byte   `json:"kind"` // raw type byte: zone reaction / output function / partition type
}

// ScanResult holds all devices discovered by Panel.Scan.
type ScanResult struct {
	PanelType  string       `json:"panelType"`
	Zones      []ScanDevice `json:"zones"`
	Partitions []ScanDevice `json:"partitions"`
	Outputs    []ScanDevice `json:"outputs"`
}

// scanReply carries the scan outcome back to the caller of Scan.
type scanReply struct {
	result *ScanResult
	err    error
}

// ErrScanRunning is returned by Scan while another scan is queued or running.
var ErrScanRunning = errors.New("satel: scan already running")

// ScanProgress reports a running name scan, for a progress bar.
type ScanProgress struct {
	Running bool   `json:"running"`
	Phase   string `json:"phase"` // "zones", "partitions" or "outputs"
	Done    int    `json:"done"`
	Total   int    `json:"total"`
}

// ScanProgress returns the progress of the running scan (Running=false if none).
func (p *Panel) ScanProgress() ScanProgress {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.scanProgress
}

func (p *Panel) setScanProgress(fn func(sp *ScanProgress)) {
	p.mu.Lock()
	fn(&p.scanProgress)
	p.mu.Unlock()
}

// Scan reads the names of all zones, partitions and outputs from the panel
// (cmd 0xEE per device). It runs inside the session goroutine because the
// protocol is lockstep; ~300 commands, over a minute on a real ETHM-1, during
// which state is not polled and control commands wait (polling mid-scan made the
// ETHM-1 stop answering).
func (p *Panel) Scan(timeout time.Duration) (*ScanResult, error) {
	if !p.Connected() {
		return nil, ErrNotConnected
	}
	// Reserve before queueing: Running is only set once the session picks the scan up
	p.mu.Lock()
	if p.scanQueued || p.scanProgress.Running {
		p.mu.Unlock()
		return nil, ErrScanRunning
	}
	p.scanQueued = true
	p.mu.Unlock()
	release := func() { p.setScanProgress(func(*ScanProgress) { p.scanQueued = false }) }

	ch := make(chan scanReply, 1)
	select {
	case p.ctrl <- ctrlRequest{op: "scan", scanCh: ch}:
	default:
		release()
		return nil, errors.New("satel: control queue full")
	}
	select {
	case r := <-ch:
		return r.result, r.err
	case <-time.After(timeout):
		release()
		return nil, errors.New("satel: scan timeout")
	case <-p.stop:
		release()
		return nil, errors.New("satel: panel stopping")
	}
}

// execScan performs the device name scan on the live connection.
// The reply is always sent on ch; a returned error means a connection-level
// failure (the session will reconnect).
func (p *Panel) execScan(conn *connection, ch chan scanReply) error {
	p.mu.RLock()
	zoneCount := p.zoneCount
	panelType := p.panelType
	p.mu.RUnlock()
	if zoneCount == 0 {
		zoneCount = 128
	}

	p.setScanProgress(func(sp *ScanProgress) {
		p.scanQueued = false
		*sp = ScanProgress{Running: true, Phase: "zones", Total: zoneCount + 32 + zoneCount}
	})
	defer p.setScanProgress(func(sp *ScanProgress) { sp.Running = false })
	stats := newScanStats(conn)

	scanKind := func(devType byte, count int, phase string) ([]ScanDevice, error) {
		p.setScanProgress(func(sp *ScanProgress) { sp.Phase = phase })
		devices := []ScanDevice{}
		for n := 1; n <= count; n++ {
			select {
			case <-p.stop:
				return nil, errors.New("satel: panel stopping")
			default:
			}
			nb := byte(n)
			if devType == deviceTypeZone && n == 256 {
				nb = 0
			}
			start := time.Now()
			f, err := conn.execute(cmdReadDeviceName, []byte{devType, nb})
			stats.add(phase, n, f, time.Since(start), err)
			if err != nil {
				return nil, err
			}
			p.setScanProgress(func(sp *ScanProgress) { sp.Done++ })
			// 0xEF result = device does not exist on this panel; skip.
			if f.Cmd != cmdReadDeviceName || len(f.Data) < 19 {
				continue
			}
			devices = append(devices, ScanDevice{
				Number: n,
				Name:   decodeDeviceName(f.Data[3:19]),
				Kind:   f.Data[2],
			})
		}
		return devices, nil
	}

	res := &ScanResult{PanelType: panelType}
	var err error
	if res.Zones, err = scanKind(deviceTypeZone, zoneCount, "zones"); err == nil {
		if res.Partitions, err = scanKind(deviceTypePartition, 32, "partitions"); err == nil {
			res.Outputs, err = scanKind(deviceTypeOutput, zoneCount, "outputs")
		}
	}
	stats.log()
	if err != nil {
		ch <- scanReply{err: err}
		return err
	}
	ch <- scanReply{result: res}
	return nil
}

// scanStats summarises a scan's replies in the log, to diagnose ETHM-1 timing:
// reply times, 0xEF result codes and frames execute had to skip.
type scanStats struct {
	conn     *connection
	skipped0 int
	n        int
	total    time.Duration
	min, max time.Duration
	results  map[byte]int // 0xEF result code -> count
	names    int
}

func newScanStats(conn *connection) *scanStats {
	return &scanStats{conn: conn, skipped0: conn.skipped, results: map[byte]int{}}
}

func (s *scanStats) add(phase string, n int, f Frame, d time.Duration, err error) {
	if err != nil {
		log.Printf("[SATEL] scan %s %d failed after %dms: %v", phase, n, d.Milliseconds(), err)
		return
	}
	s.n++
	s.total += d
	if s.min == 0 || d < s.min {
		s.min = d
	}
	if d > s.max {
		s.max = d
	}
	if f.Cmd == cmdResult && len(f.Data) > 0 {
		s.results[f.Data[0]]++
	} else {
		s.names++
	}
	if d > time.Second {
		log.Printf("[SATEL] scan %s %d: slow reply %dms (0x%02X % X)", phase, n, d.Milliseconds(), f.Cmd, f.Data)
	}
}

func (s *scanStats) log() {
	if s.n == 0 {
		return
	}
	log.Printf("[SATEL] scan: %d replies (%d names, 0xEF codes %v), reply ms min/avg/max %d/%d/%d, skipped frames %d",
		s.n, s.names, s.results, s.min.Milliseconds(), (s.total / time.Duration(s.n)).Milliseconds(),
		s.max.Milliseconds(), s.conn.skipped-s.skipped0)
}

// TestConnection dials the ETHM-1 module, reads the panel type/version (0x7E)
// and disconnects. It validates host, port and the encryption key without
// touching any panel state. Pure Go — safe to call from web handlers.
func TestConnection(host string, port int, encryptionKey string, timeout time.Duration) (string, error) {
	if host == "" {
		return "", errors.New("satel: host is empty")
	}
	if port == 0 {
		port = 7094
	}
	if timeout <= 0 {
		timeout = 5 * time.Second
	}

	conn, err := dialPanel(host, port, encryptionKey, timeout)
	if err != nil {
		return "", err
	}
	defer conn.close()

	f, err := conn.execute(cmdPanelVersion, nil)
	if err != nil {
		if encryptionKey != "" {
			return "", fmt.Errorf("satel: no valid response — check the integration key: %w", err)
		}
		return "", fmt.Errorf("satel: no valid response from panel: %w", err)
	}
	if f.Cmd != cmdPanelVersion || len(f.Data) < 12 {
		return "", fmt.Errorf("satel: unexpected response 0x%02X to version query", f.Cmd)
	}

	model := panelModel{Name: "INTEGRA (unknown)"}
	if m, ok := panelModels[f.Data[0]]; ok {
		model = m
	}
	// Data[1..11]: 11 ASCII digits "VNNYYYYMMDD" -> version V.NN (YYYY-MM-DD)
	v := f.Data[1:12]
	version := fmt.Sprintf("v%c.%c%c (%s-%s-%s)", v[0], v[1], v[2], v[3:7], v[7:9], v[9:11])
	return model.Name + " " + version, nil
}
