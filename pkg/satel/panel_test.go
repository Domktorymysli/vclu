package satel

import (
	"bufio"
	"io"
	"net"
	"sync"
	"testing"
	"time"
)

// fakeIntegra simulates an ETHM-1 Plus + INTEGRA 128 over TCP for tests.
type fakeIntegra struct {
	ln  net.Listener
	key string // integration key; "" = plain

	mu       sync.Mutex
	conns    []net.Conn
	violated [16]byte
	armed    [4]byte
	alarm    [4]byte
	outputs  [16]byte
	names    map[[2]byte]string // [devType, number] -> name
	nameWait time.Duration      // delay before answering 0xEE (real ETHM-1: ~275 ms)
	lastCode []byte             // user code bytes from last control command
}

func newFakeIntegra(t *testing.T, key string) *fakeIntegra {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	f := &fakeIntegra{ln: ln, key: key, names: map[[2]byte]string{}}
	go f.serve()
	t.Cleanup(func() { ln.Close() })
	return f
}

func (f *fakeIntegra) port() int {
	return f.ln.Addr().(*net.TCPAddr).Port
}

func (f *fakeIntegra) serve() {
	for {
		conn, err := f.ln.Accept()
		if err != nil {
			return
		}
		f.mu.Lock()
		f.conns = append(f.conns, conn)
		f.mu.Unlock()
		go f.handle(conn)
	}
}

func (f *fakeIntegra) handle(conn net.Conn) {
	defer conn.Close()
	r := bufio.NewReader(conn)
	var enc *encrypter
	if f.key != "" {
		enc, _ = newEncrypter(f.key)
	}
	var dec frameDecoder

	for {
		var raw []byte
		if enc != nil {
			n, err := r.ReadByte()
			if err != nil {
				return
			}
			pdu := make([]byte, int(n))
			if _, err := io.ReadFull(r, pdu); err != nil {
				return
			}
			raw, err = enc.unwrap(pdu)
			if err != nil {
				return
			}
		} else {
			buf := make([]byte, 1024)
			n, err := r.Read(buf)
			if err != nil {
				return
			}
			raw = buf[:n]
		}

		frames, _ := dec.Feed(raw)
		for _, fr := range frames {
			cmd, data := f.respond(fr)
			out := EncodeFrame(cmd, data)
			if enc != nil {
				pdu, err := enc.wrap(out)
				if err != nil {
					return
				}
				out = pdu
			}
			if _, err := conn.Write(out); err != nil {
				return
			}
		}
	}
}

// respond computes the response for one request frame.
func (f *fakeIntegra) respond(fr Frame) (byte, []byte) {
	if fr.Cmd == cmdReadDeviceName {
		f.mu.Lock()
		wait := f.nameWait
		f.mu.Unlock()
		time.Sleep(wait)
	}
	f.mu.Lock()
	defer f.mu.Unlock()

	switch fr.Cmd {
	case cmdPanelVersion:
		// INTEGRA 128 (type 3), version 1.23 2012-05-27, lang 1, flash
		return fr.Cmd, []byte{3, '1', '2', '3', '2', '0', '1', '2', '0', '5', '2', '7', 1, 0xFF}
	case cmdModuleVersion:
		return fr.Cmd, []byte{'2', '0', '0', '2', '0', '2', '0', '1', '0', '1', '5', 0x00}
	case cmdZonesViolated:
		return fr.Cmd, f.violated[:]
	case cmdZonesTamper, cmdZonesAlarm, cmdZonesBypass:
		return fr.Cmd, make([]byte, 16)
	case cmdPartsArmed:
		return fr.Cmd, f.armed[:]
	case cmdPartsAlarm:
		return fr.Cmd, f.alarm[:]
	case cmdPartsFireAlarm, cmdPartsEntryTime, cmdPartsExitTime:
		return fr.Cmd, make([]byte, 4)
	case cmdOutputsState:
		return fr.Cmd, f.outputs[:]
	case cmdZoneTemp:
		// zone 7: 22.5 °C -> raw = 22.5*2+110 = 155
		if len(fr.Data) == 1 && fr.Data[0] == 7 {
			return fr.Cmd, []byte{7, 0x00, 155}
		}
		return fr.Cmd, []byte{fr.Data[0], 0xFF, 0xFF}
	case cmdReadDeviceName:
		if len(fr.Data) == 2 {
			if name, ok := f.names[[2]byte{fr.Data[0], fr.Data[1]}]; ok {
				data := []byte{fr.Data[0], fr.Data[1], 0x00}
				padded := make([]byte, 16)
				copy(padded, name)
				for i := len(name); i < 16; i++ {
					padded[i] = ' '
				}
				return fr.Cmd, append(data, padded...)
			}
		}
		return cmdResult, []byte{0x03}
	case cmdOutputsOn, cmdOutputsOff:
		if len(fr.Data) != 8+16 {
			return cmdResult, []byte{0x08}
		}
		f.lastCode = append([]byte{}, fr.Data[:8]...)
		for n := 1; n <= 128; n++ {
			if maskBit(fr.Data[8:], n) {
				if fr.Cmd == cmdOutputsOn {
					f.outputs[(n-1)/8] |= 1 << uint((n-1)%8)
				} else {
					f.outputs[(n-1)/8] &^= 1 << uint((n-1)%8)
				}
			}
		}
		return cmdResult, []byte{0x00}
	case cmdArmMode0, cmdArmMode0 + 1, cmdArmMode0 + 2, cmdArmMode0 + 3,
		cmdForceArmMode0, cmdForceArmMode0 + 1, cmdForceArmMode0 + 2, cmdForceArmMode0 + 3:
		if len(fr.Data) != 8+4 {
			return cmdResult, []byte{0x08}
		}
		f.lastCode = append([]byte{}, fr.Data[:8]...)
		for i := range f.armed {
			f.armed[i] |= fr.Data[8+i]
		}
		return cmdResult, []byte{0x00}
	case cmdDisarm:
		f.lastCode = append([]byte{}, fr.Data[:8]...)
		for i := range f.armed {
			f.armed[i] &^= fr.Data[8+i]
		}
		return cmdResult, []byte{0x00}
	default:
		return cmdResult, []byte{0x08}
	}
}

func (f *fakeIntegra) setViolated(zone int, v bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if v {
		f.violated[(zone-1)/8] |= 1 << uint((zone-1)%8)
	} else {
		f.violated[(zone-1)/8] &^= 1 << uint((zone-1)%8)
	}
}

// waitEvent reads panel events until match returns true or the timeout hits.
func waitEvent(t *testing.T, p *Panel, what string, match func(Event) bool) Event {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		select {
		case ev := <-p.Events():
			if match(ev) {
				return ev
			}
		case <-deadline:
			t.Fatalf("timeout waiting for %s event", what)
			return Event{}
		}
	}
}

func testPanelAgainstFake(t *testing.T, key string) {
	fake := newFakeIntegra(t, key)
	fake.setViolated(3, true)
	fake.names[[2]byte{deviceTypeZone, 3}] = "Salon PIR"

	p := New(Config{
		Host:          "127.0.0.1",
		Port:          fake.port(),
		UserCode:      "1234",
		EncryptionKey: key,
		PollInterval:  30 * time.Millisecond,
		TempPoll:      50 * time.Millisecond,
		Timeout:       2 * time.Second,
		Zones:         []int{3},
		TempZones:     []int{7},
		ReadNames:     true,
	})
	p.Start()
	defer p.Stop()

	ev := waitEvent(t, p, "connection", func(e Event) bool { return e.Type == EventConnection && e.Connected })
	if ev.PanelType != "INTEGRA 128" {
		t.Errorf("panelType = %q", ev.PanelType)
	}

	waitEvent(t, p, "zone name", func(e Event) bool {
		return e.Type == EventName && e.Kind == "zone" && e.Number == 3 && e.Message == "Salon PIR"
	})
	waitEvent(t, p, "zone violated", func(e Event) bool {
		return e.Type == EventZone && e.Number == 3 && e.Zone != nil && e.Zone.Violated
	})
	waitEvent(t, p, "temperature", func(e Event) bool {
		return e.Type == EventTemperature && e.Number == 7 && e.Temp != nil && *e.Temp == 22.5
	})

	if z := p.GetZone(3); !z.Violated || z.Name != "Salon PIR" {
		t.Errorf("GetZone(3) = %+v", z)
	}
	if temp, ok := p.GetTemperature(7); !ok || temp != 22.5 {
		t.Errorf("GetTemperature(7) = %v %v", temp, ok)
	}

	// Zone change propagates.
	fake.setViolated(3, false)
	waitEvent(t, p, "zone restored", func(e Event) bool {
		return e.Type == EventZone && e.Number == 3 && e.Zone != nil && !e.Zone.Violated
	})

	// Output control: result event + state change.
	if err := p.SetOutput(5, true); err != nil {
		t.Fatalf("SetOutput: %v", err)
	}
	res := waitEvent(t, p, "output result", func(e Event) bool { return e.Type == EventResult && e.Op == "output_on" })
	if !res.OK {
		t.Errorf("output_on result = %+v", res)
	}
	waitEvent(t, p, "output active", func(e Event) bool {
		return e.Type == EventOutput && e.Number == 5 && e.Output != nil && e.Output.Active
	})
	if !p.GetOutput(5).Active {
		t.Error("GetOutput(5) should be active")
	}

	// The user code must have been sent correctly.
	fake.mu.Lock()
	code := fake.lastCode
	fake.mu.Unlock()
	wantCode, _ := EncodeUserCode("1234")
	if string(code) != string(wantCode[:]) {
		t.Errorf("user code on wire = % X, want % X", code, wantCode)
	}

	// Arm partition 2.
	if err := p.Arm(2, 0); err != nil {
		t.Fatalf("Arm: %v", err)
	}
	waitEvent(t, p, "partition armed", func(e Event) bool {
		return e.Type == EventPartition && e.Number == 2 && e.Partition != nil && e.Partition.Armed
	})

	// Disarm is blocked by default config.
	if err := p.Disarm(2); err != ErrDisarmNotAllowed {
		t.Errorf("Disarm should be blocked, got %v", err)
	}
	if err := p.ClearAlarm(2); err != ErrDisarmNotAllowed {
		t.Errorf("ClearAlarm should be blocked, got %v", err)
	}

	// Force arm is blocked by default too (it bypasses violated zones).
	if err := p.ForceArm(2, 0); err != ErrForceArmNotAllowed {
		t.Errorf("ForceArm should be blocked, got %v", err)
	}
}

func TestPanelPlain(t *testing.T) {
	testPanelAgainstFake(t, "")
}

func TestPanelEncrypted(t *testing.T) {
	testPanelAgainstFake(t, "tajny_klucz")
}

func TestPanelDisarmAllowed(t *testing.T) {
	fake := newFakeIntegra(t, "")
	fake.mu.Lock()
	fake.armed[0] = 0x01
	fake.mu.Unlock()

	p := New(Config{
		Host:          "127.0.0.1",
		Port:          fake.port(),
		UserCode:      "1234",
		PollInterval:  30 * time.Millisecond,
		Timeout:       2 * time.Second,
		AllowDisarm:   true,
		AllowForceArm: true,
	})
	p.Start()
	defer p.Stop()

	waitEvent(t, p, "partition armed", func(e Event) bool {
		return e.Type == EventPartition && e.Number == 1 && e.Partition != nil && e.Partition.Armed
	})
	if err := p.Disarm(1); err != nil {
		t.Fatalf("Disarm: %v", err)
	}
	waitEvent(t, p, "partition disarmed", func(e Event) bool {
		return e.Type == EventPartition && e.Number == 1 && e.Partition != nil && !e.Partition.Armed
	})

	// Force arm works when explicitly allowed.
	if err := p.ForceArm(3, 0); err != nil {
		t.Fatalf("ForceArm: %v", err)
	}
	res := waitEvent(t, p, "force arm result", func(e Event) bool {
		return e.Type == EventResult && e.Op == "force_arm" && e.Number == 3
	})
	if !res.OK {
		t.Errorf("force_arm result = %+v", res)
	}
	waitEvent(t, p, "partition 3 armed", func(e Event) bool {
		return e.Type == EventPartition && e.Number == 3 && e.Partition != nil && e.Partition.Armed
	})
}

func TestPanelControlWhileDisconnected(t *testing.T) {
	p := New(Config{Host: "127.0.0.1", Port: 1}) // nothing listening
	if err := p.SetOutput(1, true); err != ErrNotConnected {
		t.Errorf("expected ErrNotConnected, got %v", err)
	}
}

func TestPanelReconnect(t *testing.T) {
	fake := newFakeIntegra(t, "")
	p := New(Config{
		Host:         "127.0.0.1",
		Port:         fake.port(),
		UserCode:     "1234",
		PollInterval: 30 * time.Millisecond,
		Timeout:      time.Second,
	})
	p.Start()
	defer p.Stop()

	waitEvent(t, p, "connected", func(e Event) bool { return e.Type == EventConnection && e.Connected })

	// Kill the listener and all sessions; panel should report disconnect, then reconnect.
	fake.ln.Close()
	fake.mu.Lock()
	for _, c := range fake.conns {
		c.Close()
	}
	fake.conns = nil
	fake.mu.Unlock()
	waitEvent(t, p, "disconnected", func(e Event) bool { return e.Type == EventConnection && !e.Connected })

	// Restart the listener on the same port — the panel must reconnect on its own.
	ln, err := net.Listen("tcp", fake.ln.Addr().String())
	if err != nil {
		t.Skipf("could not rebind port: %v", err)
	}
	fake.ln = ln
	go fake.serve()

	waitEvent(t, p, "reconnected", func(e Event) bool { return e.Type == EventConnection && e.Connected })
}
