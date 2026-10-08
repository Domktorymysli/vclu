package satel

import (
	"errors"
	"fmt"
	"log"
	"sync"
	"time"

	"golang.org/x/text/encoding/charmap"
)

// ErrDisarmNotAllowed is returned when disarm/clear alarm is attempted while
// the configuration does not allow it (allowDisarm: false, the default).
var ErrDisarmNotAllowed = errors.New("satel: disarm/clear alarm not allowed by configuration (set allowDisarm)")

// ErrForceArmNotAllowed is returned when force arm is attempted while the
// configuration does not allow it (allowForceArm: false, the default).
// Force arm bypasses violated zones, so it weakens protection.
var ErrForceArmNotAllowed = errors.New("satel: force arm not allowed by configuration (set allowForceArm)")

// ErrNotConnected is returned when a control command is issued while the
// panel connection is down.
var ErrNotConnected = errors.New("satel: not connected to panel")

// Config configures a Panel client.
type Config struct {
	Host          string
	Port          int           // default 7094
	UserCode      string        // PIN used for control commands
	EncryptionKey string        // integration key; empty = plain TCP
	PollInterval  time.Duration // state poll cycle, default 1s
	TempPoll      time.Duration // temperature poll cycle, default 30s
	Timeout       time.Duration // per-command timeout, default 5s

	// Monitored object numbers (1-based). Used for name reading; state events
	// are emitted for all objects regardless.
	Zones      []int
	Partitions []int
	Outputs    []int
	TempZones  []int // zones with temperature sensors (ATD-100)

	ReadNames     bool // read zone/partition/output names after connect
	AllowDisarm   bool // allow Disarm/ClearAlarm (default false = blocked)
	AllowForceArm bool // allow ForceArm (default false = blocked; it bypasses violated zones)
}

// ctrlRequest is an async control command queued for the session goroutine.
type ctrlRequest struct {
	op     string
	number int
	mode   ArmMode
	scanCh chan scanReply // only for op "scan"
}

// Panel is a Satel Integra client. All I/O runs in a single goroutine
// (the protocol is lockstep); state is cached under a RWMutex and changes
// are emitted as Events with delta detection.
type Panel struct {
	cfg Config

	mu        sync.RWMutex
	connected bool
	panelType string
	zoneCount int
	extended  bool
	maskLen   int // zone/output mask length in bytes (16 or 32)

	zViolated, zTamper, zAlarm, zBypass  []byte
	pArmed, pAlarm, pFire, pEntry, pExit []byte
	oState                               []byte
	temps                                map[int]float64
	names                                map[string]string // "zone.5" -> name
	scanProgress                         ScanProgress
	scanQueued                           bool // Scan reserved, not yet picked up by the session

	events  chan Event
	ctrl    chan ctrlRequest
	stop    chan struct{}
	done    chan struct{}
	started bool
	dropped int64
}

// New creates a Panel client (call Start to connect).
func New(cfg Config) *Panel {
	if cfg.Port == 0 {
		cfg.Port = 7094
	}
	if cfg.PollInterval <= 0 {
		cfg.PollInterval = time.Second
	}
	if cfg.TempPoll <= 0 {
		cfg.TempPoll = 30 * time.Second
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 5 * time.Second
	}
	return &Panel{
		cfg:     cfg,
		maskLen: 16,
		temps:   make(map[int]float64),
		names:   make(map[string]string),
		events:  make(chan Event, 256),
		ctrl:    make(chan ctrlRequest, 16),
		stop:    make(chan struct{}),
		done:    make(chan struct{}),
	}
}

// Start launches the connection goroutine (idempotent).
func (p *Panel) Start() {
	p.mu.Lock()
	if p.started {
		p.mu.Unlock()
		return
	}
	p.started = true
	p.mu.Unlock()
	go p.run()
}

// Stop terminates the connection goroutine and waits for it to finish.
func (p *Panel) Stop() {
	p.mu.Lock()
	started := p.started
	p.mu.Unlock()
	if !started {
		return
	}
	select {
	case <-p.stop:
		return // already stopped
	default:
		close(p.stop)
	}
	select {
	case <-p.done:
	case <-time.After(10 * time.Second):
		log.Printf("[SATEL] stop timeout — connection goroutine did not exit")
	}
}

// Events returns the channel of state change and result events.
func (p *Panel) Events() <-chan Event {
	return p.events
}

// Connected reports whether the panel session is up.
func (p *Panel) Connected() bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.connected
}

// PanelType returns the detected panel model name (empty until connected).
func (p *Panel) PanelType() string {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.panelType
}

// GetZone returns the cached state of a zone.
func (p *Panel) GetZone(n int) ZoneState {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.zoneStateLocked(n)
}

// GetPartition returns the cached state of a partition.
func (p *Panel) GetPartition(n int) PartitionState {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.partitionStateLocked(n)
}

// GetOutput returns the cached state of an output.
func (p *Panel) GetOutput(n int) OutputState {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.outputStateLocked(n)
}

// GetTemperature returns the cached temperature of a zone sensor.
func (p *Panel) GetTemperature(n int) (float64, bool) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	t, ok := p.temps[n]
	return t, ok
}

// Arm queues arming a partition in the given mode (0-3).
func (p *Panel) Arm(partition int, mode ArmMode) error {
	return p.enqueue(ctrlRequest{op: "arm", number: partition, mode: mode})
}

// ForceArm queues force-arming a partition in the given mode (0-3).
// Blocked unless cfg.AllowForceArm — force arm ignores violated zones.
func (p *Panel) ForceArm(partition int, mode ArmMode) error {
	if !p.cfg.AllowForceArm {
		return ErrForceArmNotAllowed
	}
	return p.enqueue(ctrlRequest{op: "force_arm", number: partition, mode: mode})
}

// Disarm queues disarming a partition. Blocked unless cfg.AllowDisarm.
func (p *Panel) Disarm(partition int) error {
	if !p.cfg.AllowDisarm {
		return ErrDisarmNotAllowed
	}
	return p.enqueue(ctrlRequest{op: "disarm", number: partition})
}

// ClearAlarm queues clearing an alarm. Blocked unless cfg.AllowDisarm.
func (p *Panel) ClearAlarm(partition int) error {
	if !p.cfg.AllowDisarm {
		return ErrDisarmNotAllowed
	}
	return p.enqueue(ctrlRequest{op: "clear_alarm", number: partition})
}

// SetOutput queues switching an output on or off.
func (p *Panel) SetOutput(output int, on bool) error {
	op := "output_off"
	if on {
		op = "output_on"
	}
	return p.enqueue(ctrlRequest{op: op, number: output})
}

func (p *Panel) enqueue(req ctrlRequest) error {
	if !p.Connected() {
		return ErrNotConnected
	}
	select {
	case p.ctrl <- req:
		return nil
	default:
		return errors.New("satel: control queue full")
	}
}

// emit sends an event without blocking; overflow is counted and logged.
func (p *Panel) emit(ev Event) {
	select {
	case p.events <- ev:
	default:
		p.mu.Lock()
		p.dropped++
		n := p.dropped
		p.mu.Unlock()
		log.Printf("[SATEL] event buffer full, dropping %s event (total dropped: %d)", ev.Type, n)
	}
}

// run is the connection loop: dial, identify, poll until error, reconnect
// with exponential backoff.
func (p *Panel) run() {
	defer close(p.done)
	backoff := time.Second

	for {
		select {
		case <-p.stop:
			return
		default:
		}

		conn, err := dialPanel(p.cfg.Host, p.cfg.Port, p.cfg.EncryptionKey, p.cfg.Timeout)
		if err != nil {
			p.emit(Event{Type: EventConnection, Connected: false, Message: err.Error()})
			if !p.sleep(backoff) {
				return
			}
			backoff *= 2
			if backoff > 30*time.Second {
				backoff = 30 * time.Second
			}
			continue
		}
		backoff = time.Second

		err = p.session(conn)
		conn.close()

		p.mu.Lock()
		p.connected = false
		p.mu.Unlock()

		msg := ""
		if err != nil {
			msg = err.Error()
			log.Printf("[SATEL] session ended: %v", err)
		}
		p.emit(Event{Type: EventConnection, Connected: false, Message: msg})

		select {
		case <-p.stop:
			return
		default:
		}
		if !p.sleep(time.Second) {
			return
		}
	}
}

// sleep waits d or until stop; returns false when stopping.
func (p *Panel) sleep(d time.Duration) bool {
	select {
	case <-p.stop:
		return false
	case <-time.After(d):
		return true
	}
}

// session runs one connected session: identify, read names, poll loop.
func (p *Panel) session(conn *connection) error {
	if err := p.identify(conn); err != nil {
		return fmt.Errorf("identify: %w", err)
	}

	// Drop control commands queued while disconnected.
	for {
		select {
		case req := <-p.ctrl:
			if req.scanCh != nil {
				p.setScanProgress(func(*ScanProgress) { p.scanQueued = false })
				req.scanCh <- scanReply{err: ErrNotConnected}
			}
			continue
		default:
		}
		break
	}

	p.mu.Lock()
	p.connected = true
	panelType := p.panelType
	p.mu.Unlock()
	log.Printf("[SATEL] connected to %s at %s:%d", panelType, p.cfg.Host, p.cfg.Port)
	p.emit(Event{Type: EventConnection, Connected: true, PanelType: panelType})

	if p.cfg.ReadNames {
		p.readNames(conn)
	}

	if err := p.pollState(conn); err != nil {
		return err
	}
	if err := p.pollTemps(conn); err != nil {
		return err
	}

	pollTicker := time.NewTicker(p.cfg.PollInterval)
	defer pollTicker.Stop()
	tempTicker := time.NewTicker(p.cfg.TempPoll)
	defer tempTicker.Stop()

	for {
		select {
		case <-p.stop:
			return nil
		case req := <-p.ctrl:
			if req.op == "scan" {
				if err := p.execScan(conn, req.scanCh); err != nil {
					return err
				}
				continue
			}
			if err := p.execCtrl(conn, req); err != nil {
				return err
			}
		case <-pollTicker.C:
			if err := p.pollState(conn); err != nil {
				return err
			}
		case <-tempTicker.C:
			if err := p.pollTemps(conn); err != nil {
				return err
			}
		}
	}
}

// identify reads panel type/version (0x7E) and module capabilities (0x7C).
func (p *Panel) identify(conn *connection) error {
	f, err := conn.execute(cmdPanelVersion, nil)
	if err != nil {
		return err
	}
	model := panelModel{Name: "INTEGRA (unknown)", Zones: 128}
	if f.Cmd == cmdPanelVersion && len(f.Data) >= 1 {
		if m, ok := panelModels[f.Data[0]]; ok {
			model = m
		}
	}

	extended := false
	if model.Extended {
		// Extended (32-byte) payloads also require module support (0x7C cap bit 0).
		if mf, err := conn.execute(cmdModuleVersion, nil); err == nil &&
			mf.Cmd == cmdModuleVersion && len(mf.Data) >= 12 && mf.Data[11]&0x01 != 0 {
			extended = true
		}
	}

	p.mu.Lock()
	p.panelType = model.Name
	p.zoneCount = model.Zones
	p.extended = extended
	if extended {
		p.maskLen = 32
	} else {
		p.maskLen = 16
	}
	p.mu.Unlock()
	return nil
}

// readState reads one state bitmask command (with extended payload if active).
func (p *Panel) readState(conn *connection, cmd byte) ([]byte, error) {
	var payload []byte
	p.mu.RLock()
	extended := p.extended
	p.mu.RUnlock()
	if extended && cmd != cmdPartsArmed && cmd != cmdPartsAlarm && cmd != cmdPartsFireAlarm &&
		cmd != cmdPartsEntryTime && cmd != cmdPartsExitTime {
		payload = []byte{0x00}
	}
	f, err := conn.execute(cmd, payload)
	if err != nil {
		return nil, err
	}
	if f.Cmd != cmd {
		return nil, fmt.Errorf("satel: unexpected response 0x%02X to 0x%02X", f.Cmd, cmd)
	}
	return f.Data, nil
}

// pollState reads all monitored bitmasks, performs delta detection and emits
// per-object change events.
func (p *Panel) pollState(conn *connection) error {
	zoneChanged := map[int]bool{}
	partChanged := map[int]bool{}
	outChanged := map[int]bool{}

	reads := []struct {
		cmd     byte
		dst     *[]byte
		changed map[int]bool
	}{
		{cmdZonesViolated, &p.zViolated, zoneChanged},
		{cmdZonesTamper, &p.zTamper, zoneChanged},
		{cmdZonesAlarm, &p.zAlarm, zoneChanged},
		{cmdZonesBypass, &p.zBypass, zoneChanged},
		{cmdPartsArmed, &p.pArmed, partChanged},
		{cmdPartsAlarm, &p.pAlarm, partChanged},
		{cmdPartsFireAlarm, &p.pFire, partChanged},
		{cmdPartsEntryTime, &p.pEntry, partChanged},
		{cmdPartsExitTime, &p.pExit, partChanged},
		{cmdOutputsState, &p.oState, outChanged},
	}

	for _, r := range reads {
		data, err := p.readState(conn, r.cmd)
		if err != nil {
			return err
		}
		p.mu.Lock()
		for _, n := range diffMask(*r.dst, data) {
			r.changed[n] = true
		}
		*r.dst = data
		p.mu.Unlock()
	}

	p.mu.RLock()
	var events []Event
	for n := range zoneChanged {
		z := p.zoneStateLocked(n)
		events = append(events, Event{Type: EventZone, Number: n, Zone: &z})
	}
	for n := range partChanged {
		pt := p.partitionStateLocked(n)
		events = append(events, Event{Type: EventPartition, Number: n, Partition: &pt})
	}
	for n := range outChanged {
		o := p.outputStateLocked(n)
		events = append(events, Event{Type: EventOutput, Number: n, Output: &o})
	}
	p.mu.RUnlock()

	for _, ev := range events {
		p.emit(ev)
	}
	return nil
}

// pollTemps reads temperatures for configured zones (cmd 0x7D).
func (p *Panel) pollTemps(conn *connection) error {
	for _, zone := range p.cfg.TempZones {
		zb := byte(zone)
		if zone == 256 {
			zb = 0
		}
		f, err := conn.execute(cmdZoneTemp, []byte{zb})
		if err != nil {
			return err
		}
		if f.Cmd != cmdZoneTemp || len(f.Data) < 3 {
			continue
		}
		raw := uint16(f.Data[1])<<8 | uint16(f.Data[2])
		if raw == 0xFFFF { // no reading
			continue
		}
		t := (float64(raw) - 110) / 2

		p.mu.Lock()
		old, known := p.temps[zone]
		p.temps[zone] = t
		p.mu.Unlock()

		if !known || old != t {
			tc := t
			p.emit(Event{Type: EventTemperature, Number: zone, Temp: &tc})
		}
	}
	return nil
}

// readNames reads device names (0xEE) for configured objects.
func (p *Panel) readNames(conn *connection) {
	type group struct {
		kind    string
		devType byte
		numbers []int
	}
	groups := []group{
		{"zone", deviceTypeZone, p.cfg.Zones},
		{"partition", deviceTypePartition, p.cfg.Partitions},
		{"output", deviceTypeOutput, p.cfg.Outputs},
	}
	for _, g := range groups {
		for _, n := range g.numbers {
			nb := byte(n)
			if g.devType == deviceTypeZone && n == 256 {
				nb = 0
			}
			f, err := conn.execute(cmdReadDeviceName, []byte{g.devType, nb})
			if err != nil {
				log.Printf("[SATEL] name read failed for %s %d: %v", g.kind, n, err)
				return // connection-level problem, names are best-effort
			}
			// 0xEF response = device does not exist; skip silently.
			if f.Cmd != cmdReadDeviceName || len(f.Data) < 19 {
				continue
			}
			name := decodeDeviceName(f.Data[3:19])
			if name == "" {
				continue
			}
			p.mu.Lock()
			p.names[fmt.Sprintf("%s.%d", g.kind, n)] = name
			p.mu.Unlock()
			p.emit(Event{Type: EventName, Kind: g.kind, Number: n, Message: name})
		}
	}
}

// decodeDeviceName trims a 16-byte name field (space/NUL padded) and decodes it
// from Windows-1250, as in openHAB's Satel binding; bytes undefined there become U+FFFD.
func decodeDeviceName(raw []byte) string {
	end := len(raw)
	for end > 0 && (raw[end-1] == ' ' || raw[end-1] == 0x00) {
		end--
	}
	name, _ := charmap.Windows1250.NewDecoder().Bytes(raw[:end]) // never fails for a charmap
	return string(name)
}

// execCtrl executes one queued control command and emits a result event.
// Returns an error only for connection-level failures.
func (p *Panel) execCtrl(conn *connection, req ctrlRequest) error {
	code, err := EncodeUserCode(p.cfg.UserCode)
	if err != nil {
		p.emit(Event{Type: EventResult, Op: req.op, Number: req.number, OK: false, Message: err.Error()})
		return nil
	}

	p.mu.RLock()
	maskLen := p.maskLen
	p.mu.RUnlock()

	var cmd byte
	var mask []byte
	switch req.op {
	case "arm":
		cmd = cmdArmMode0 + byte(req.mode)
		mask, err = numbersToMask(4, req.number)
	case "force_arm":
		cmd = cmdForceArmMode0 + byte(req.mode)
		mask, err = numbersToMask(4, req.number)
	case "disarm":
		cmd = cmdDisarm
		mask, err = numbersToMask(4, req.number)
	case "clear_alarm":
		cmd = cmdClearAlarm
		mask, err = numbersToMask(4, req.number)
	case "output_on":
		cmd = cmdOutputsOn
		mask, err = numbersToMask(maskLen, req.number)
	case "output_off":
		cmd = cmdOutputsOff
		mask, err = numbersToMask(maskLen, req.number)
	default:
		err = fmt.Errorf("satel: unknown control op %q", req.op)
	}
	if err != nil {
		p.emit(Event{Type: EventResult, Op: req.op, Number: req.number, OK: false, Message: err.Error()})
		return nil
	}

	payload := append(code[:], mask...)
	f, err := conn.execute(cmd, payload)
	if err != nil {
		p.emit(Event{Type: EventResult, Op: req.op, Number: req.number, OK: false, Message: err.Error()})
		return err
	}

	resultCode := byte(0x08) // "other error" if response is malformed
	if f.Cmd == cmdResult && len(f.Data) >= 1 {
		resultCode = f.Data[0]
	}
	ok := resultOK(resultCode)
	p.emit(Event{Type: EventResult, Op: req.op, Number: req.number, OK: ok, Code: resultCode, Message: resultMessage(resultCode)})
	if !ok {
		log.Printf("[SATEL] %s %d rejected: %s", req.op, req.number, resultMessage(resultCode))
	}

	// Refresh state right away so the change is visible before the next tick.
	return p.pollState(conn)
}

// zoneStateLocked builds a ZoneState from cached masks (caller holds lock).
func (p *Panel) zoneStateLocked(n int) ZoneState {
	return ZoneState{
		Number:   n,
		Violated: maskBit(p.zViolated, n),
		Tamper:   maskBit(p.zTamper, n),
		Alarm:    maskBit(p.zAlarm, n),
		Bypass:   maskBit(p.zBypass, n),
		Name:     p.names[fmt.Sprintf("zone.%d", n)],
	}
}

// partitionStateLocked builds a PartitionState (caller holds lock).
func (p *Panel) partitionStateLocked(n int) PartitionState {
	return PartitionState{
		Number:    n,
		Armed:     maskBit(p.pArmed, n),
		Alarm:     maskBit(p.pAlarm, n),
		FireAlarm: maskBit(p.pFire, n),
		EntryTime: maskBit(p.pEntry, n),
		ExitTime:  maskBit(p.pExit, n),
		Name:      p.names[fmt.Sprintf("partition.%d", n)],
	}
}

// outputStateLocked builds an OutputState (caller holds lock).
func (p *Panel) outputStateLocked(n int) OutputState {
	return OutputState{
		Number: n,
		Active: maskBit(p.oState, n),
		Name:   p.names[fmt.Sprintf("output.%d", n)],
	}
}
