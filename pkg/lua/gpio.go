package lua

import (
	"encoding/json"
	"sync"

	"github.com/Domktorymysli/vclu/pkg/gpio"
	lua "github.com/yuin/gopher-lua"
)

// PinChange represents a detected input pin state change
type PinChange struct {
	Pin      int
	OldValue int // 0 or 1
	NewValue int // 0 or 1
}

// ManagerStats extends ControllerStats with queue info
type ManagerStats struct {
	gpio.ControllerStats
	QueueDepth int `json:"queueDepth"`
}

// GPIOManager wraps a GPIO Controller with input polling and change detection
type GPIOManager struct {
	ctrl        gpio.Controller
	mu          sync.Mutex
	lastValues  map[int]bool
	inputPins   []int
	changeQueue chan PinChange
}

// NewGPIOManager creates a new GPIOManager wrapping the given controller
func NewGPIOManager(ctrl gpio.Controller) *GPIOManager {
	return &GPIOManager{
		ctrl:        ctrl,
		lastValues:  make(map[int]bool),
		inputPins:   make([]int, 0),
		changeQueue: make(chan PinChange, 100),
	}
}

// Controller returns the underlying GPIO controller
func (gm *GPIOManager) Controller() gpio.Controller {
	return gm.ctrl
}

// RegisterInput registers a pin for input polling
func (gm *GPIOManager) RegisterInput(pin int) {
	gm.mu.Lock()
	defer gm.mu.Unlock()
	// Avoid duplicates
	for _, p := range gm.inputPins {
		if p == pin {
			return
		}
	}
	gm.inputPins = append(gm.inputPins, pin)
	// Read initial value so first poll detects changes from this baseline
	if val, err := gm.ctrl.Read(pin); err == nil {
		gm.lastValues[pin] = val
	}
}

// UnregisterInput removes a pin from input polling
func (gm *GPIOManager) UnregisterInput(pin int) {
	gm.mu.Lock()
	defer gm.mu.Unlock()
	for i, p := range gm.inputPins {
		if p == pin {
			gm.inputPins = append(gm.inputPins[:i], gm.inputPins[i+1:]...)
			delete(gm.lastValues, pin)
			return
		}
	}
}

// PollInputs reads all registered input pins and queues changes
func (gm *GPIOManager) PollInputs() {
	gm.mu.Lock()
	pins := make([]int, len(gm.inputPins))
	copy(pins, gm.inputPins)
	gm.mu.Unlock()

	for _, pin := range pins {
		val, err := gm.ctrl.Read(pin)
		if err != nil {
			continue
		}

		gm.mu.Lock()
		lastVal, known := gm.lastValues[pin]
		gm.lastValues[pin] = val
		gm.mu.Unlock()

		if known && lastVal != val {
			oldInt, newInt := 0, 0
			if lastVal {
				oldInt = 1
			}
			if val {
				newInt = 1
			}
			// Non-blocking send
			select {
			case gm.changeQueue <- PinChange{Pin: pin, OldValue: oldInt, NewValue: newInt}:
			default:
				// Queue full, drop oldest
			}
		}
	}
}

// GetPendingChange returns the next pending change or nil
func (gm *GPIOManager) GetPendingChange() *PinChange {
	select {
	case change := <-gm.changeQueue:
		return &change
	default:
		return nil
	}
}

// Stats returns combined controller + manager statistics
func (gm *GPIOManager) Stats() ManagerStats {
	return ManagerStats{
		ControllerStats: gm.ctrl.Stats(),
		QueueDepth:      len(gm.changeQueue),
	}
}

// Close closes the underlying controller
func (gm *GPIOManager) Close() error {
	return gm.ctrl.Close()
}

// registerGPIOFunctions registers Go GPIO functions in Lua
func registerGPIOFunctions(L *lua.LState, gm *GPIOManager) {
	if gm == nil {
		return
	}

	// __go_gpio_setup(pin, mode, pull, safeState) -> (bool, string|nil)
	L.SetGlobal("__go_gpio_setup", L.NewFunction(func(L *lua.LState) int {
		pin := L.CheckInt(1)
		modeStr := L.CheckString(2)
		pullStr := L.OptString(3, "off")
		safeState := L.OptBool(4, false)

		var mode gpio.PinMode
		if modeStr == "output" {
			mode = gpio.Output
		} else {
			mode = gpio.Input
		}

		var pull gpio.PinPull
		switch pullStr {
		case "up":
			pull = gpio.PullUp
		case "down":
			pull = gpio.PullDown
		default:
			pull = gpio.PullOff
		}

		if err := gm.ctrl.Setup(pin, mode, pull, safeState); err != nil {
			L.Push(lua.LFalse)
			L.Push(lua.LString(err.Error()))
			return 2
		}

		// Register input pins for polling
		if mode == gpio.Input {
			gm.RegisterInput(pin)
		}

		L.Push(lua.LTrue)
		L.Push(lua.LNil)
		return 2
	}))

	// __go_gpio_write(pin, value) -> (bool, string|nil)
	L.SetGlobal("__go_gpio_write", L.NewFunction(func(L *lua.LState) int {
		pin := L.CheckInt(1)
		value := L.CheckBool(2)

		if err := gm.ctrl.Write(pin, value); err != nil {
			L.Push(lua.LFalse)
			L.Push(lua.LString(err.Error()))
			return 2
		}
		L.Push(lua.LTrue)
		L.Push(lua.LNil)
		return 2
	}))

	// __go_gpio_read(pin) -> (0|1, string|nil)
	L.SetGlobal("__go_gpio_read", L.NewFunction(func(L *lua.LState) int {
		pin := L.CheckInt(1)

		val, err := gm.ctrl.Read(pin)
		if err != nil {
			L.Push(lua.LNumber(-1))
			L.Push(lua.LString(err.Error()))
			return 2
		}
		if val {
			L.Push(lua.LNumber(1))
		} else {
			L.Push(lua.LNumber(0))
		}
		L.Push(lua.LNil)
		return 2
	}))

	// __go_gpio_toggle(pin) -> (bool, string|nil)
	L.SetGlobal("__go_gpio_toggle", L.NewFunction(func(L *lua.LState) int {
		pin := L.CheckInt(1)

		if err := gm.ctrl.Toggle(pin); err != nil {
			L.Push(lua.LFalse)
			L.Push(lua.LString(err.Error()))
			return 2
		}
		L.Push(lua.LTrue)
		L.Push(lua.LNil)
		return 2
	}))

	// __go_gpio_release(pin) -> (bool, string|nil)
	L.SetGlobal("__go_gpio_release", L.NewFunction(func(L *lua.LState) int {
		pin := L.CheckInt(1)

		gm.UnregisterInput(pin)

		if err := gm.ctrl.Release(pin); err != nil {
			L.Push(lua.LFalse)
			L.Push(lua.LString(err.Error()))
			return 2
		}
		L.Push(lua.LTrue)
		L.Push(lua.LNil)
		return 2
	}))

	// __go_gpio_poll() -> (pin, oldValue, newValue) or nil
	L.SetGlobal("__go_gpio_poll", L.NewFunction(func(L *lua.LState) int {
		change := gm.GetPendingChange()
		if change == nil {
			return 0
		}
		L.Push(lua.LNumber(change.Pin))
		L.Push(lua.LNumber(change.OldValue))
		L.Push(lua.LNumber(change.NewValue))
		return 3
	}))

	// __go_gpio_stats() -> JSON string
	L.SetGlobal("__go_gpio_stats", L.NewFunction(func(L *lua.LState) int {
		stats := gm.Stats()
		jsonBytes, err := json.Marshal(stats)
		if err != nil {
			L.Push(lua.LString("{}"))
			return 1
		}
		L.Push(lua.LString(string(jsonBytes)))
		return 1
	}))
}
