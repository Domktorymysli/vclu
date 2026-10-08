package gpio

import (
	"errors"
	"testing"
)

func TestMockController_Setup(t *testing.T) {
	ctrl := NewMockController(Config{BCMValidation: true})
	defer ctrl.Close()

	// Valid pin
	if err := ctrl.Setup(17, Output, PullOff, false); err != nil {
		t.Fatalf("Setup(17) failed: %v", err)
	}

	// Invalid pin (BCM validation)
	if err := ctrl.Setup(28, Output, PullOff, false); err == nil {
		t.Fatal("Setup(28) should fail with BCM validation")
	}

	// Negative pin
	if err := ctrl.Setup(-1, Output, PullOff, false); err == nil {
		t.Fatal("Setup(-1) should fail")
	}
}

func TestMockController_WriteRead(t *testing.T) {
	ctrl := NewMockController(Config{})
	defer ctrl.Close()

	ctrl.Setup(17, Output, PullOff, false)

	// Write true
	if err := ctrl.Write(17, true); err != nil {
		t.Fatalf("Write failed: %v", err)
	}
	val, err := ctrl.Read(17)
	if err != nil {
		t.Fatalf("Read failed: %v", err)
	}
	if !val {
		t.Fatal("expected true after Write(true)")
	}

	// Write false
	ctrl.Write(17, false)
	val, _ = ctrl.Read(17)
	if val {
		t.Fatal("expected false after Write(false)")
	}
}

func TestMockController_WriteNotSetup(t *testing.T) {
	ctrl := NewMockController(Config{})
	defer ctrl.Close()

	err := ctrl.Write(17, true)
	if !errors.Is(err, ErrPinNotSetup) {
		t.Fatalf("expected ErrPinNotSetup, got: %v", err)
	}
}

func TestMockController_WriteInputPin(t *testing.T) {
	ctrl := NewMockController(Config{})
	defer ctrl.Close()

	ctrl.Setup(17, Input, PullUp, false)
	err := ctrl.Write(17, true)
	if !errors.Is(err, ErrPinNotOutput) {
		t.Fatalf("expected ErrPinNotOutput, got: %v", err)
	}
}

func TestMockController_Toggle(t *testing.T) {
	ctrl := NewMockController(Config{})
	defer ctrl.Close()

	ctrl.Setup(17, Output, PullOff, false)

	ctrl.Toggle(17)
	val, _ := ctrl.Read(17)
	if !val {
		t.Fatal("expected true after first Toggle")
	}

	ctrl.Toggle(17)
	val, _ = ctrl.Read(17)
	if val {
		t.Fatal("expected false after second Toggle")
	}
}

func TestMockController_Release(t *testing.T) {
	ctrl := NewMockController(Config{})
	defer ctrl.Close()

	ctrl.Setup(17, Output, PullOff, false)

	if err := ctrl.Release(17); err != nil {
		t.Fatalf("Release failed: %v", err)
	}

	// Pin should no longer be accessible
	_, err := ctrl.Read(17)
	if !errors.Is(err, ErrPinNotSetup) {
		t.Fatalf("expected ErrPinNotSetup after Release, got: %v", err)
	}
}

func TestMockController_SimulateInput(t *testing.T) {
	ctrl := NewMockController(Config{})
	defer ctrl.Close()

	ctrl.Setup(27, Input, PullUp, false)

	ctrl.SimulateInput(27, true)
	val, _ := ctrl.Read(27)
	if !val {
		t.Fatal("expected true after SimulateInput(true)")
	}

	ctrl.SimulateInput(27, false)
	val, _ = ctrl.Read(27)
	if val {
		t.Fatal("expected false after SimulateInput(false)")
	}
}

func TestMockController_Stats(t *testing.T) {
	ctrl := NewMockController(Config{})
	defer ctrl.Close()

	ctrl.Setup(17, Output, PullOff, false)
	ctrl.Setup(27, Input, PullUp, false)

	ctrl.Write(17, true)
	ctrl.Write(17, false) // change
	ctrl.Read(27)
	ctrl.Read(27)

	stats := ctrl.Stats()
	if stats.Platform != "mock" {
		t.Fatalf("expected platform=mock, got %s", stats.Platform)
	}
	if stats.SetupPins != 2 {
		t.Fatalf("expected 2 pins, got %d", stats.SetupPins)
	}
	if stats.Inputs != 1 {
		t.Fatalf("expected 1 input, got %d", stats.Inputs)
	}
	if stats.Outputs != 1 {
		t.Fatalf("expected 1 output, got %d", stats.Outputs)
	}
	if stats.WriteCount != 2 {
		t.Fatalf("expected writeCount=2, got %d", stats.WriteCount)
	}
	if stats.ReadCount != 2 {
		t.Fatalf("expected readCount=2, got %d", stats.ReadCount)
	}
	if stats.ChangeCount != 2 {
		t.Fatalf("expected changeCount=2 (true→false), got %d", stats.ChangeCount)
	}
	if stats.UptimeMs < 0 {
		t.Fatal("uptimeMs should be >= 0")
	}
}

func TestMockController_Close_SafeState(t *testing.T) {
	ctrl := NewMockController(Config{})

	// Active-low relay: safeState=true (HIGH = relay OFF)
	ctrl.Setup(17, Output, PullOff, true)
	ctrl.Write(17, false) // relay ON (LOW)

	// Active-high LED: safeState=false (LOW = LED OFF)
	ctrl.Setup(18, Output, PullOff, false)
	ctrl.Write(18, true) // LED ON

	// Before Close, verify states
	val17, _ := ctrl.Read(17)
	if val17 {
		t.Fatal("pin 17 should be false (LOW) before Close")
	}
	val18, _ := ctrl.Read(18)
	if !val18 {
		t.Fatal("pin 18 should be true (HIGH) before Close")
	}

	// Close should set each pin to its safeState, then clear map
	ctrl.Close()

	stats := ctrl.Stats()
	if stats.SetupPins != 0 {
		t.Fatalf("expected 0 pins after Close, got %d", stats.SetupPins)
	}
}

func TestMockController_GetState(t *testing.T) {
	ctrl := NewMockController(Config{})
	defer ctrl.Close()

	ctrl.Setup(17, Output, PullOff, true)
	ctrl.Write(17, true)

	state, err := ctrl.GetState(17)
	if err != nil {
		t.Fatalf("GetState failed: %v", err)
	}
	if state.Pin != 17 {
		t.Fatalf("expected pin=17, got %d", state.Pin)
	}
	if state.Mode != "output" {
		t.Fatalf("expected mode=output, got %s", state.Mode)
	}
	if !state.Value {
		t.Fatal("expected value=true")
	}
	if !state.SafeState {
		t.Fatal("expected safeState=true")
	}
}

func TestMockController_NoBCMValidation(t *testing.T) {
	ctrl := NewMockController(Config{BCMValidation: false})
	defer ctrl.Close()

	// Pin 28+ should work without BCM validation
	if err := ctrl.Setup(28, Output, PullOff, false); err != nil {
		t.Fatalf("Setup(28) should work without BCM validation: %v", err)
	}
}

func TestRPiController_StubError(t *testing.T) {
	_, err := NewRPiController(Config{})
	if err == nil {
		t.Fatal("NewRPiController should fail on non-RPi platform")
	}
}
