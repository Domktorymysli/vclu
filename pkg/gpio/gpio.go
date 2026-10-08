package gpio

import (
	"errors"
	"fmt"
)

// PinMode represents the direction of a GPIO pin
type PinMode int

const (
	Input  PinMode = 0
	Output PinMode = 1
)

// PinPull represents the pull-up/pull-down resistor configuration
type PinPull int

const (
	PullOff  PinPull = 0
	PullUp   PinPull = 1
	PullDown PinPull = 2
)

// Errors
var (
	ErrPinNotSetup  = errors.New("gpio: pin not setup")
	ErrPinNotOutput = errors.New("gpio: pin is not an output")
	ErrInvalidPin   = errors.New("gpio: invalid pin number")
)

// Config holds GPIO controller configuration
type Config struct {
	BCMValidation bool // validate pins 0-27 (false = extended range for MCP23017)
}

// PinState represents the current state of a single pin
type PinState struct {
	Pin       int    `json:"pin"`
	Mode      string `json:"mode"` // "input" or "output"
	Pull      string `json:"pull"` // "off", "up", "down"
	Value     bool   `json:"value"`
	SafeState bool   `json:"safeState"`
}

// ControllerStats holds hardware-level statistics
type ControllerStats struct {
	Platform    string     `json:"platform"`
	SetupPins   int        `json:"setupPins"`
	Inputs      int        `json:"inputs"`
	Outputs     int        `json:"outputs"`
	ReadCount   int64      `json:"readCount"`
	WriteCount  int64      `json:"writeCount"`
	ChangeCount int64      `json:"changeCount"`
	UptimeMs    int64      `json:"uptimeMs"`
	Pins        []PinState `json:"pins"`
}

// Controller is the interface for GPIO hardware access
type Controller interface {
	// Setup configures a pin. safeState is the physical state applied on Close().
	Setup(pin int, mode PinMode, pull PinPull, safeState bool) error
	Write(pin int, value bool) error
	Read(pin int) (bool, error)
	Toggle(pin int) error
	Release(pin int) error
	GetState(pin int) (PinState, error)
	Stats() ControllerStats
	Close() error
}

// ValidateBCMPin checks if a pin number is valid for BCM numbering (0-27)
func ValidateBCMPin(pin int) error {
	if pin < 0 || pin > 27 {
		return fmt.Errorf("%w: BCM pin must be 0-27, got %d", ErrInvalidPin, pin)
	}
	return nil
}

// ModeString returns the string representation of a PinMode
func ModeString(m PinMode) string {
	if m == Output {
		return "output"
	}
	return "input"
}

// PullString returns the string representation of a PinPull
func PullString(p PinPull) string {
	switch p {
	case PullUp:
		return "up"
	case PullDown:
		return "down"
	default:
		return "off"
	}
}
