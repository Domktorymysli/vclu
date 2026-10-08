package gpio

import (
	"fmt"
	"sync"
	"sync/atomic"
	"time"
)

type mockPin struct {
	mode      PinMode
	pull      PinPull
	value     bool
	safeState bool
}

// MockController is an in-memory GPIO controller for testing
type MockController struct {
	mu          sync.RWMutex
	pins        map[int]*mockPin
	cfg         Config
	readCount   atomic.Int64
	writeCount  atomic.Int64
	changeCount atomic.Int64
	startedAt   time.Time
}

// NewMockController creates a new mock GPIO controller
func NewMockController(cfg Config) *MockController {
	return &MockController{
		pins:      make(map[int]*mockPin),
		cfg:       cfg,
		startedAt: time.Now(),
	}
}

func (m *MockController) validatePin(pin int) error {
	if m.cfg.BCMValidation {
		return ValidateBCMPin(pin)
	}
	if pin < 0 {
		return fmt.Errorf("%w: pin must be >= 0, got %d", ErrInvalidPin, pin)
	}
	return nil
}

func (m *MockController) Setup(pin int, mode PinMode, pull PinPull, safeState bool) error {
	if err := m.validatePin(pin); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.pins[pin] = &mockPin{
		mode:      mode,
		pull:      pull,
		value:     false,
		safeState: safeState,
	}
	return nil
}

func (m *MockController) Write(pin int, value bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	p, ok := m.pins[pin]
	if !ok {
		return fmt.Errorf("%w: %d", ErrPinNotSetup, pin)
	}
	if p.mode != Output {
		return fmt.Errorf("%w: %d", ErrPinNotOutput, pin)
	}
	m.writeCount.Add(1)
	if p.value != value {
		m.changeCount.Add(1)
	}
	p.value = value
	return nil
}

func (m *MockController) Read(pin int) (bool, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	p, ok := m.pins[pin]
	if !ok {
		return false, fmt.Errorf("%w: %d", ErrPinNotSetup, pin)
	}
	m.readCount.Add(1)
	return p.value, nil
}

func (m *MockController) Toggle(pin int) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	p, ok := m.pins[pin]
	if !ok {
		return fmt.Errorf("%w: %d", ErrPinNotSetup, pin)
	}
	if p.mode != Output {
		return fmt.Errorf("%w: %d", ErrPinNotOutput, pin)
	}
	m.writeCount.Add(1)
	m.changeCount.Add(1)
	p.value = !p.value
	return nil
}

func (m *MockController) Release(pin int) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.pins[pin]; !ok {
		return fmt.Errorf("%w: %d", ErrPinNotSetup, pin)
	}
	delete(m.pins, pin)
	return nil
}

func (m *MockController) GetState(pin int) (PinState, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	p, ok := m.pins[pin]
	if !ok {
		return PinState{}, fmt.Errorf("%w: %d", ErrPinNotSetup, pin)
	}
	return PinState{
		Pin:       pin,
		Mode:      ModeString(p.mode),
		Pull:      PullString(p.pull),
		Value:     p.value,
		SafeState: p.safeState,
	}, nil
}

func (m *MockController) Stats() ControllerStats {
	m.mu.RLock()
	defer m.mu.RUnlock()
	stats := ControllerStats{
		Platform:    "mock",
		SetupPins:   len(m.pins),
		ReadCount:   m.readCount.Load(),
		WriteCount:  m.writeCount.Load(),
		ChangeCount: m.changeCount.Load(),
		UptimeMs:    time.Since(m.startedAt).Milliseconds(),
		Pins:        make([]PinState, 0, len(m.pins)),
	}
	for pin, p := range m.pins {
		if p.mode == Input {
			stats.Inputs++
		} else {
			stats.Outputs++
		}
		stats.Pins = append(stats.Pins, PinState{
			Pin:       pin,
			Mode:      ModeString(p.mode),
			Pull:      PullString(p.pull),
			Value:     p.value,
			SafeState: p.safeState,
		})
	}
	return stats
}

func (m *MockController) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, p := range m.pins {
		if p.mode == Output {
			p.value = p.safeState
		}
	}
	m.pins = make(map[int]*mockPin)
	return nil
}

// SimulateInput sets the value of an input pin (test helper)
func (m *MockController) SimulateInput(pin int, value bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	p, ok := m.pins[pin]
	if !ok {
		return fmt.Errorf("%w: %d", ErrPinNotSetup, pin)
	}
	if p.value != value {
		m.changeCount.Add(1)
	}
	p.value = value
	return nil
}
