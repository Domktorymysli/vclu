//go:build linux && (arm || arm64)

package gpio

import (
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/stianeikeland/go-rpio/v4"
)

type rpiPin struct {
	pin       rpio.Pin
	mode      PinMode
	pull      PinPull
	safeState bool
}

// RPiController controls GPIO pins on a Raspberry Pi via /dev/gpiomem
type RPiController struct {
	mu          sync.RWMutex
	pins        map[int]*rpiPin
	cfg         Config
	readCount   atomic.Int64
	writeCount  atomic.Int64
	changeCount atomic.Int64
	startedAt   time.Time
	lastValues  map[int]bool // for change detection
}

// NewController creates the best available GPIO controller for this platform
func NewController(cfg Config) Controller {
	ctrl, err := NewRPiController(cfg)
	if err != nil {
		return NewMockController(cfg)
	}
	return ctrl
}

// NewRPiController creates a new RPi GPIO controller
func NewRPiController(cfg Config) (*RPiController, error) {
	if err := rpio.Open(); err != nil {
		return nil, fmt.Errorf("gpio: failed to open /dev/gpiomem: %w", err)
	}
	return &RPiController{
		pins:       make(map[int]*rpiPin),
		cfg:        cfg,
		startedAt:  time.Now(),
		lastValues: make(map[int]bool),
	}, nil
}

func (r *RPiController) validatePin(pin int) error {
	if r.cfg.BCMValidation {
		return ValidateBCMPin(pin)
	}
	if pin < 0 {
		return fmt.Errorf("%w: pin must be >= 0, got %d", ErrInvalidPin, pin)
	}
	return nil
}

func (r *RPiController) Setup(pin int, mode PinMode, pull PinPull, safeState bool) error {
	if err := r.validatePin(pin); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()

	p := rpio.Pin(pin)
	if mode == Output {
		p.Output()
	} else {
		p.Input()
		switch pull {
		case PullUp:
			p.PullUp()
		case PullDown:
			p.PullDown()
		default:
			p.PullOff()
		}
	}

	r.pins[pin] = &rpiPin{
		pin:       p,
		mode:      mode,
		pull:      pull,
		safeState: safeState,
	}
	return nil
}

func (r *RPiController) Write(pin int, value bool) error {
	r.mu.RLock()
	p, ok := r.pins[pin]
	r.mu.RUnlock()
	if !ok {
		return fmt.Errorf("%w: %d", ErrPinNotSetup, pin)
	}
	if p.mode != Output {
		return fmt.Errorf("%w: %d", ErrPinNotOutput, pin)
	}
	r.writeCount.Add(1)
	if value {
		p.pin.High()
	} else {
		p.pin.Low()
	}
	// Track change
	r.mu.Lock()
	old := r.lastValues[pin]
	r.lastValues[pin] = value
	r.mu.Unlock()
	if old != value {
		r.changeCount.Add(1)
	}
	return nil
}

func (r *RPiController) Read(pin int) (bool, error) {
	r.mu.RLock()
	p, ok := r.pins[pin]
	r.mu.RUnlock()
	if !ok {
		return false, fmt.Errorf("%w: %d", ErrPinNotSetup, pin)
	}
	r.readCount.Add(1)
	return p.pin.Read() == rpio.High, nil
}

func (r *RPiController) Toggle(pin int) error {
	r.mu.RLock()
	p, ok := r.pins[pin]
	r.mu.RUnlock()
	if !ok {
		return fmt.Errorf("%w: %d", ErrPinNotSetup, pin)
	}
	if p.mode != Output {
		return fmt.Errorf("%w: %d", ErrPinNotOutput, pin)
	}
	r.writeCount.Add(1)
	r.changeCount.Add(1)
	p.pin.Toggle()
	return nil
}

func (r *RPiController) Release(pin int) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.pins[pin]; !ok {
		return fmt.Errorf("%w: %d", ErrPinNotSetup, pin)
	}
	delete(r.pins, pin)
	delete(r.lastValues, pin)
	return nil
}

func (r *RPiController) GetState(pin int) (PinState, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	p, ok := r.pins[pin]
	if !ok {
		return PinState{}, fmt.Errorf("%w: %d", ErrPinNotSetup, pin)
	}
	return PinState{
		Pin:       pin,
		Mode:      ModeString(p.mode),
		Pull:      PullString(p.pull),
		Value:     p.pin.Read() == rpio.High,
		SafeState: p.safeState,
	}, nil
}

func (r *RPiController) Stats() ControllerStats {
	r.mu.RLock()
	defer r.mu.RUnlock()
	stats := ControllerStats{
		Platform:    "rpi",
		SetupPins:   len(r.pins),
		ReadCount:   r.readCount.Load(),
		WriteCount:  r.writeCount.Load(),
		ChangeCount: r.changeCount.Load(),
		UptimeMs:    time.Since(r.startedAt).Milliseconds(),
		Pins:        make([]PinState, 0, len(r.pins)),
	}
	for pin, p := range r.pins {
		if p.mode == Input {
			stats.Inputs++
		} else {
			stats.Outputs++
		}
		stats.Pins = append(stats.Pins, PinState{
			Pin:       pin,
			Mode:      ModeString(p.mode),
			Pull:      PullString(p.pull),
			Value:     p.pin.Read() == rpio.High,
			SafeState: p.safeState,
		})
	}
	return stats
}

func (r *RPiController) Close() error {
	r.mu.Lock()
	for _, p := range r.pins {
		if p.mode == Output {
			if p.safeState {
				p.pin.High()
			} else {
				p.pin.Low()
			}
		}
	}
	r.pins = make(map[int]*rpiPin)
	r.lastValues = make(map[int]bool)
	r.mu.Unlock()
	return rpio.Close()
}
