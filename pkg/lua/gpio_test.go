package lua

import (
	"testing"
	"time"

	"github.com/Domktorymysli/vclu/pkg/gpio"
)

func newGPIOEngine(t *testing.T) (*LuaEngine, *gpio.MockController) {
	t.Helper()
	ctrl := gpio.NewMockController(gpio.Config{BCMValidation: true})
	gm := NewGPIOManager(ctrl)

	engine := NewLuaEngine()
	engine.SetGPIOManager(gm)
	return engine, ctrl
}

func TestGPIO_SetupWriteRead(t *testing.T) {
	engine, _ := newGPIOEngine(t)
	defer engine.Close()

	_, err := engine.Execute(`gpio.setup(17, "output")`)
	if err != nil {
		t.Fatalf("setup failed: %v", err)
	}

	_, err = engine.Execute(`gpio.write(17, true)`)
	if err != nil {
		t.Fatalf("write failed: %v", err)
	}

	result, err := engine.Execute(`gpio.read(17)`)
	if err != nil {
		t.Fatalf("read failed: %v", err)
	}
	if result != "1" {
		t.Fatalf("expected 1, got %s", result)
	}

	_, err = engine.Execute(`gpio.write(17, false)`)
	if err != nil {
		t.Fatalf("write false failed: %v", err)
	}

	result, err = engine.Execute(`gpio.read(17)`)
	if err != nil {
		t.Fatalf("read false failed: %v", err)
	}
	if result != "0" {
		t.Fatalf("expected 0, got %s", result)
	}
}

func TestGPIO_Toggle(t *testing.T) {
	engine, _ := newGPIOEngine(t)
	defer engine.Close()

	_, _ = engine.Execute(`gpio.setup(17, "output")`)
	_, _ = engine.Execute(`gpio.toggle(17)`)

	result, _ := engine.Execute(`gpio.read(17)`)
	if result != "1" {
		t.Fatalf("expected 1 after toggle, got %s", result)
	}
}

func TestGPIO_PinRegistry_Collision(t *testing.T) {
	engine, _ := newGPIOEngine(t)
	defer engine.Close()

	_, _ = engine.Execute(`gpio.setup(17, "output")`)
	_, err := engine.Execute(`gpio.setup(17, "output")`)
	if err == nil {
		t.Fatal("expected error on double setup, got nil")
	}
}

func TestGPIO_Release(t *testing.T) {
	engine, _ := newGPIOEngine(t)
	defer engine.Close()

	_, _ = engine.Execute(`gpio.setup(17, "output")`)
	_, _ = engine.Execute(`gpio.release(17)`)

	// Should be able to setup again after release
	_, err := engine.Execute(`gpio.setup(17, "output")`)
	if err != nil {
		t.Fatalf("re-setup after release failed: %v", err)
	}
}

func TestGPIO_Stats(t *testing.T) {
	engine, _ := newGPIOEngine(t)
	defer engine.Close()

	_, _ = engine.Execute(`gpio.setup(17, "output")`)
	_, _ = engine.Execute(`gpio.write(17, true)`)

	result, err := engine.Execute(`gpio.stats().platform`)
	if err != nil {
		t.Fatalf("stats failed: %v", err)
	}
	if result != "mock" {
		t.Fatalf("expected platform=mock, got %s", result)
	}
}

func TestGPIO_InputPolling(t *testing.T) {
	engine, ctrl := newGPIOEngine(t)
	defer engine.Close()

	_, err := engine.Execute(`gpio.setup(27, "input", "up")`)
	if err != nil {
		t.Fatalf("setup input failed: %v", err)
	}

	// Simulate input change from Go side
	ctrl.SimulateInput(27, true)

	// Poll inputs via manager
	engine.ProcessGPIO()

	// Check that poll returns the change
	_, _ = engine.Execute(`_test_pin, _test_old, _test_new = __go_gpio_poll()`)

	result, _ := engine.Execute(`_test_pin`)
	if result != "27" {
		t.Fatalf("expected pin=27, got %s", result)
	}
	result, _ = engine.Execute(`_test_old`)
	if result != "0" {
		t.Fatalf("expected oldVal=0, got %s", result)
	}
	result, _ = engine.Execute(`_test_new`)
	if result != "1" {
		t.Fatalf("expected newVal=1, got %s", result)
	}
}

func TestGPIO_DOUT_Basic(t *testing.T) {
	engine, _ := newGPIOEngine(t)
	defer engine.Close()

	_, err := engine.Execute(`GPIO_DOUT:new("R1", 17)`)
	if err != nil {
		t.Fatalf("GPIO_DOUT:new failed: %v", err)
	}

	_, err = engine.Execute(`R1:execute(DOUT.METHOD_SWITCH_ON)`)
	if err != nil {
		t.Fatalf("execute SWITCH_ON failed: %v", err)
	}

	result, _ := engine.Execute(`R1:get(DOUT.FEATURE_VALUE)`)
	if result != "1" {
		t.Fatalf("expected value=1 after SWITCH_ON, got %s", result)
	}
}

func TestGPIO_DOUT_ActiveLow(t *testing.T) {
	engine, ctrl := newGPIOEngine(t)
	defer engine.Close()

	_, err := engine.Execute(`GPIO_DOUT:new("R1", 17, {activeLow = true})`)
	if err != nil {
		t.Fatalf("GPIO_DOUT:new activeLow failed: %v", err)
	}

	_, err = engine.Execute(`R1:execute(DOUT.METHOD_SWITCH_ON)`)
	if err != nil {
		t.Fatalf("execute SWITCH_ON failed: %v", err)
	}

	// With activeLow, SWITCH_ON should write LOW to the physical pin
	val, err := ctrl.Read(17)
	if err != nil {
		t.Fatalf("ctrl.Read failed: %v", err)
	}
	if val {
		t.Fatal("activeLow relay: SWITCH_ON should set pin LOW, but got HIGH")
	}

	// Logical value should be 1 (ON)
	result, _ := engine.Execute(`R1:get(DOUT.FEATURE_VALUE)`)
	if result != "1" {
		t.Fatalf("expected logical value=1, got %s", result)
	}
}

func TestGPIO_DOUT_Switch(t *testing.T) {
	engine, _ := newGPIOEngine(t)
	defer engine.Close()

	_, _ = engine.Execute(`GPIO_DOUT:new("R1", 17)`)
	_, _ = engine.Execute(`R1:execute(DOUT.METHOD_SWITCH)`) // OFF -> ON

	result, _ := engine.Execute(`R1:get(DOUT.FEATURE_VALUE)`)
	if result != "1" {
		t.Fatalf("expected 1 after first switch, got %s", result)
	}

	_, _ = engine.Execute(`R1:execute(DOUT.METHOD_SWITCH)`) // ON -> OFF

	result, _ = engine.Execute(`R1:get(DOUT.FEATURE_VALUE)`)
	if result != "0" {
		t.Fatalf("expected 0 after second switch, got %s", result)
	}
}

func TestGPIO_DOUT_PinCollision(t *testing.T) {
	engine, _ := newGPIOEngine(t)
	defer engine.Close()

	_, _ = engine.Execute(`GPIO_DOUT:new("R1", 17)`)
	_, err := engine.Execute(`GPIO_DOUT:new("R2", 17)`)
	if err == nil {
		t.Fatal("expected pin collision error, got nil")
	}
}

func TestGPIO_DIN_Basic(t *testing.T) {
	engine, _ := newGPIOEngine(t)
	defer engine.Close()

	_, err := engine.Execute(`GPIO_DIN:new("BTN1", 27, "up")`)
	if err != nil {
		t.Fatalf("GPIO_DIN:new failed: %v", err)
	}

	result, _ := engine.Execute(`BTN1:get(DIN.FEATURE_VALUE)`)
	if result != "0" {
		t.Fatalf("expected initial value=0, got %s", result)
	}
}

func TestGPIO_DIN_EventBusIntegration(t *testing.T) {
	engine, ctrl := newGPIOEngine(t)
	defer engine.Close()

	// Create DIN and track clicks
	_, err := engine.Execute(`
		_test_click_count = 0
		GPIO_DIN:new("BTN1", 27, "up")
		BTN1:add_event(DIN.EVENT_ON_CLICK, function()
			_test_click_count = _test_click_count + 1
		end)
	`)
	if err != nil {
		t.Fatalf("setup failed: %v", err)
	}

	// Simulate button press via GPIO
	ctrl.SimulateInput(27, true)
	engine.ProcessGPIO()

	// Drain poll queue through Lua (EventBus fires -> GenericButton:press)
	_, _ = engine.Execute(`gpio.pollChanges()`)

	// Process timers (GenericButton uses setTimeout for inertion=0)
	engine.StartTimers()
	// the click fires from a timer; wait for it instead of a fixed sleep
	var result string
	for deadline := time.Now().Add(2 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
		engine.ProcessTimers()
		if result, _ = engine.Execute(`_test_click_count`); result != "0" {
			break
		}
	}
	if result != "1" {
		t.Fatalf("expected click_count=1, got %s", result)
	}
}

func TestGPIO_DOUT_GlobalRegistered(t *testing.T) {
	engine, _ := newGPIOEngine(t)
	defer engine.Close()

	_, _ = engine.Execute(`GPIO_DOUT:new("RELAY1", 17)`)

	result, _ := engine.Execute(`RELAY1 ~= nil`)
	if result != "true" {
		t.Fatal("expected RELAY1 to be registered as global")
	}
}

func TestGPIO_DOUT_OnChange(t *testing.T) {
	engine, _ := newGPIOEngine(t)
	defer engine.Close()

	_, _ = engine.Execute(`
		_test_changes = {}
		GPIO_DOUT:new("R1", 17)
		R1:onChange(function(newVal, oldVal)
			table.insert(_test_changes, newVal .. ":" .. oldVal)
		end)
	`)

	_, _ = engine.Execute(`R1:execute(DOUT.METHOD_SWITCH_ON)`)
	_, _ = engine.Execute(`R1:execute(DOUT.METHOD_SWITCH_OFF)`)

	result, _ := engine.Execute(`#_test_changes`)
	if result != "2" {
		t.Fatalf("expected 2 changes, got %s", result)
	}

	result, _ = engine.Execute(`_test_changes[1]`)
	if result != "1:0" {
		t.Fatalf("expected first change 1:0, got %s", result)
	}

	result, _ = engine.Execute(`_test_changes[2]`)
	if result != "0:1" {
		t.Fatalf("expected second change 0:1, got %s", result)
	}
}
