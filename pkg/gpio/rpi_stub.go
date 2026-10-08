//go:build !(linux && (arm || arm64))

package gpio

import (
	"fmt"
	"log"
)

// NewController creates the best available GPIO controller for this platform
func NewController(cfg Config) Controller {
	log.Printf("GPIO: using mock controller (hardware not available on this platform)")
	return NewMockController(cfg)
}

// NewRPiController returns an error on non-RPi platforms
func NewRPiController(cfg Config) (*RPiController, error) {
	return nil, fmt.Errorf("gpio: hardware not available on this platform")
}

// RPiController is a stub type for non-RPi platforms
type RPiController struct{}
