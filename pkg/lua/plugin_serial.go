package lua

import (
	"bytes"
	"fmt"
	"log"
	"path/filepath"

	"go.bug.st/serial"
)

type serialSettings struct {
	baud, data, stop int
	parity           string
}

func (s serialSettings) mode() (*serial.Mode, error) {
	m := &serial.Mode{BaudRate: s.baud, DataBits: s.data}
	if s.baud < 50 || s.baud > 4000000 {
		return nil, fmt.Errorf("baud %d out of range", s.baud)
	}
	if s.data < 5 || s.data > 8 {
		return nil, fmt.Errorf("data bits %d out of range (5-8)", s.data)
	}
	switch s.parity {
	case "none":
		m.Parity = serial.NoParity
	case "even":
		m.Parity = serial.EvenParity
	case "odd":
		m.Parity = serial.OddParity
	default:
		return nil, fmt.Errorf("parity %q: none, even or odd", s.parity)
	}
	switch s.stop {
	case 1:
		m.StopBits = serial.OneStopBit
	case 2:
		m.StopBits = serial.TwoStopBits
	default:
		return nil, fmt.Errorf("stop bits %d: 1 or 2", s.stop)
	}
	return m, nil
}

// realPath: the device behind a name such as /dev/serial/by-id/..., so two names of
// one port are one port.
func realPath(path string) string {
	if r, err := filepath.EvalSymlinks(path); err == nil {
		return r
	}
	return filepath.Clean(path)
}

// OpenSerial opens a port the user assigned to the plugin, unless another plugin holds it.
func (p *PluginIO) OpenSerial(plugin, path string, s serialSettings) (int, error) {
	mode, err := s.mode()
	if err != nil {
		return 0, err
	}
	real := realPath(path)
	h, err := p.add(plugin, "serial", func(g pluginIOGrant) error {
		if err := needs(g.serial, plugin, "serial"); err != nil {
			return err
		}
		assigned := false
		for _, a := range g.serialPorts {
			if realPath(a) == real {
				assigned = true
			}
		}
		if !assigned {
			return fmt.Errorf("%s is not assigned to %s in the plugin's settings", path, plugin)
		}
		if _, held := p.serial[real]; held {
			return fmt.Errorf("%s is already open", path)
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	p.mu.Lock()
	p.serial[real] = h.id
	h.path = real
	p.mu.Unlock()

	port, err := serial.Open(real, mode)
	if err != nil {
		p.close(h)
		return 0, fmt.Errorf("open %s: %w", path, err)
	}
	h.port = port
	go p.pump(h)
	go func() {
		buf := make([]byte, 4096)
		for {
			n, err := port.Read(buf)
			if n > 0 {
				h.q.push(ioEvent{data: bytes.Clone(buf[:n])}, true)
			}
			if err != nil || n == 0 {
				if !h.closed() {
					why := "port closed"
					if err != nil {
						why = err.Error()
					}
					log.Printf("[PLUGINS] %s: %s: %s", plugin, path, why)
					h.q.push(ioEvent{err: why}, false)
				}
				return
			}
		}
	}()
	return h.id, nil
}

// WriteSerial writes to the plugin's open port.
func (p *PluginIO) WriteSerial(id int, plugin string, data []byte) error {
	h, err := p.handle(id, plugin)
	if err != nil || h.port == nil {
		return fmt.Errorf("port closed")
	}
	_, err = h.port.Write(data)
	return err
}
