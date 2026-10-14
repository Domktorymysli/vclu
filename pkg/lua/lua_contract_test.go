package lua

// Contract tests — verify stable behavior at layer boundaries.
// These test USER-VISIBLE outcomes, not implementation details.
// If any of these break, user code (modules/, plugins) is affected.
//
// Rules:
//  1. Test the public Lua API, not Go internals
//  2. Simulate from the hardware/external side, assert from the Lua side
//  3. Every test must describe WHAT contract it guards (not HOW it works)
//  4. Do NOT test implementation details (queue depth, timer IDs, etc.)

import (
	"fmt"
	"net"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Domktorymysli/vclu/pkg/broker"
	"github.com/Domktorymysli/vclu/pkg/gpio"
)

// =============================================================================
// GPIO Contracts
// =============================================================================

// Contract: GPIO_DIN:onClick fires when a physical pin changes.
// Guards the full chain: Go PollInputs → changeQueue → __go_gpio_poll → EventBus → GenericButton → callback
func TestContract_GPIO_DIN_OnClick(t *testing.T) {
	engine, ctrl := newGPIOEngine(t)
	defer engine.Close()
	engine.StartTimers()
	defer engine.StopTimers()

	_, err := engine.Execute(`
		_clicked = 0
		btn = GPIO_DIN:new("BTN", 17)
		btn:onClick(function() _clicked = _clicked + 1 end)
	`)
	if err != nil {
		t.Fatalf("setup failed: %v", err)
	}

	// Simulate button press: mock starts at false, change to true to trigger
	ctrl.SimulateInput(17, true)
	engine.ProcessGPIO()
	engine.Execute(`gpio.pollChanges()`)

	// Wait for debounce (inertion=50ms in GPIO_DIN:new)
	time.Sleep(80 * time.Millisecond)
	engine.ProcessTimers()

	result, _ := engine.Execute(`tostring(_clicked)`)
	if result != "1" {
		t.Errorf("expected onClick to fire once, got _clicked=%s", result)
	}
}

// Contract: GPIO_DOUT:toggle changes pin value and fires onChange.
func TestContract_GPIO_DOUT_Toggle(t *testing.T) {
	engine, _ := newGPIOEngine(t)
	defer engine.Close()

	_, err := engine.Execute(`
		_changes = 0
		led = GPIO_DOUT:new("LED", 18)
		led:onChange(function() _changes = _changes + 1 end)
	`)
	if err != nil {
		t.Fatalf("setup failed: %v", err)
	}

	engine.Execute(`led:toggle()`)
	result, _ := engine.Execute(`tostring(led:getValue())`)
	if result != "1" {
		t.Errorf("expected value=1 after toggle, got %s", result)
	}

	result, _ = engine.Execute(`tostring(_changes)`)
	if result != "1" {
		t.Errorf("expected onChange to fire once, got %s", result)
	}

	engine.Execute(`led:toggle()`)
	result, _ = engine.Execute(`tostring(led:getValue())`)
	if result != "0" {
		t.Errorf("expected value=0 after second toggle, got %s", result)
	}
}

// Contract: GPIO poll timer starts automatically after SetGPIOManager.
// (Regression: bootstrap ran before SetGPIOManager, timer never started.)
func TestContract_GPIO_PollTimerStarts(t *testing.T) {
	engine, ctrl := newGPIOEngine(t)
	defer engine.Close()
	engine.StartTimers()
	defer engine.StopTimers()

	_, err := engine.Execute(`
		_pin_changed = false
		GPIO_DIN:new("BTN", 17)
		EventBus:getShared():on("gpio.pin.17.OnChange", function()
			_pin_changed = true
		end)
	`)
	if err != nil {
		t.Fatalf("setup failed: %v", err)
	}

	// Simulate pin change — poll timer should drain it automatically
	ctrl.SimulateInput(17, true)
	engine.ProcessGPIO()

	// Wait for 50ms poll interval to fire
	time.Sleep(80 * time.Millisecond)
	engine.ProcessTimers()

	result, _ := engine.Execute(`tostring(_pin_changed)`)
	if result != "true" {
		t.Errorf("expected gpio.pin.17.OnChange to fire via poll timer, got _pin_changed=%s", result)
	}
}

// =============================================================================
// MQTT Contracts
// =============================================================================

// Contract: MQTT manager survives Lua reinitialize (reload).
// (Regression: Reinitialize destroyed MQTTManager, connection lost.)
func TestContract_MQTT_SurvivesReload(t *testing.T) {
	engine := NewLuaEngine()
	defer engine.Close()

	mm := engine.GetMQTTManager()
	if mm == nil {
		t.Fatal("expected MQTTManager to exist")
	}

	// Capture the manager pointer
	original := mm

	// Reinitialize (simulates Reload)
	if err := engine.Reinitialize("VCLU"); err != nil {
		t.Fatalf("reinitialize failed: %v", err)
	}

	// Same manager instance must survive
	after := engine.GetMQTTManager()
	if after != original {
		t.Error("expected MQTTManager to survive Reinitialize, got a new instance")
	}
}

// Contract: MQTT poll timer runs after bootstrap (messages get delivered).
func TestContract_MQTT_PollTimerRuns(t *testing.T) {
	engine := NewLuaEngine()
	defer engine.Close()

	// Verify __go_mqtt_poll is registered and MQTT.pollMessages exists
	result, err := engine.Execute(`
		return tostring(type(__go_mqtt_poll) == "function" and type(MQTT.pollMessages) == "function")
	`)
	if err != nil {
		t.Fatalf("execute failed: %v", err)
	}
	if result != "true" {
		t.Errorf("expected MQTT poll infrastructure to be available, got %s", result)
	}
}

// Contract: MQTT:subscribe callback receives messages.
func TestContract_MQTT_SubscribeCallback(t *testing.T) {
	engine := NewLuaEngine()
	defer engine.Close()

	_, err := engine.Execute(`
		_received = nil
		MQTT:subscribe("test/topic", function(topic, payload)
			_received = payload
		end)
		MQTT:simulateMessage("test/topic", "hello")
	`)
	if err != nil {
		t.Fatalf("execute failed: %v", err)
	}

	result, _ := engine.Execute(`tostring(_received)`)
	if result != "hello" {
		t.Errorf("expected callback to receive 'hello', got %s", result)
	}
}

// startTestBroker starts an embedded MQTT broker on a free local port.
func startTestBroker(t *testing.T) (*broker.Broker, int) {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	l.Close()
	b := broker.New(&broker.Config{Enabled: true, Port: port})
	if err := b.Start(); err != nil {
		t.Fatalf("broker start: %v", err)
	}
	t.Cleanup(func() { b.Stop() })
	return b, port
}

// brokerSubs returns the broker-side subscriptions of a client session (also a closed persistent one).
func brokerSubs(b *broker.Broker, clientID string) []string {
	for _, c := range b.GetClients() {
		if c.ID == clientID {
			return c.Subscriptions
		}
	}
	return nil
}

// waitMQTT runs the MQTT poll (as the poll timer does) until cond holds or 5 s pass.
func waitMQTT(t *testing.T, engine *LuaEngine, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timeout waiting for %s", what)
		}
		engine.Execute(`MQTT.pollEvents(); MQTT.pollMessages()`)
		time.Sleep(20 * time.Millisecond)
	}
}

// connectTestClient creates a client with a persistent session (so broker-side subscriptions
// outlive the connection), subscribes topic and waits until the broker has the subscription.
func connectTestClient(t *testing.T, engine *LuaEngine, b *broker.Broker, port int, varName, id, topic string) {
	t.Helper()
	_, err := engine.Execute(fmt.Sprintf(`
		%[1]s = MQTT:new(%[2]q)
		%[1]s:setHost("127.0.0.1"):setPort(%[3]d)
		%[1]s._props.cleanSession = false
		%[1]s:subscribe(%[4]q, function() _c[%[1]q] = (_c[%[1]q] or 0) + 1 end)
		%[1]s:onMessage(function() _c[%[1]q .. "_any"] = (_c[%[1]q .. "_any"] or 0) + 1 end)
		%[1]s:connect()
	`, varName, id, port, topic))
	if err != nil {
		t.Fatalf("lua: %v", err)
	}
	waitMQTT(t, engine, "broker subscription "+topic, func() bool {
		return slices.Contains(brokerSubs(b, "vclu_"+id), topic)
	})
}

// Contract: MQTT:destroy() releases a client: broker subscriptions and connection, Lua callbacks,
// the registry entry and the Go-side client. A later message on its topic reaches no callback.
func TestContract_MQTT_Destroy(t *testing.T) {
	b, port := startTestBroker(t)
	engine := NewLuaEngine()
	defer engine.Close()
	engine.Execute(`_c = {}`)

	connectTestClient(t, engine, b, port, "cli", "destroy_t", "t/destroy")
	b.Publish("t/destroy", []byte("1"), false)
	waitMQTT(t, engine, "first message", func() bool {
		got, _ := engine.Execute(`return tostring(_c.cli)`)
		return got == "1"
	})

	got, err := engine.Execute(`
		cli:destroy()
		return tostring(MQTT.getInstance("destroy_t") == nil)`)
	if err != nil || got != "true" {
		t.Fatalf("after destroy: instance removed = %q, err %v", got, err)
	}
	waitMQTT(t, engine, "broker unsubscribe", func() bool {
		return !slices.Contains(brokerSubs(b, "vclu_destroy_t"), "t/destroy")
	})
	for _, c := range engine.GetMQTTManager().Stats().Clients {
		if c.InstanceID == "destroy_t" {
			t.Errorf("Go-side client destroy_t still registered")
		}
	}

	// Neither a broker message nor one handed to the old object reaches a callback.
	b.Publish("t/destroy", []byte("2"), false)
	for i := 0; i < 10; i++ {
		engine.Execute(`MQTT.pollEvents(); MQTT.pollMessages()`)
		time.Sleep(20 * time.Millisecond)
	}
	got, _ = engine.Execute(`
		cli:simulateMessage("t/destroy", "3")
		return tostring(_c.cli) .. "," .. tostring(_c.cli_any)`)
	if got != "1,1" {
		t.Errorf("callbacks after destroy: got %s (subscription,onMessage), want 1,1", got)
	}
}

// Contract: MQTT:new(id) with an id in use destroys the old client and warns with the id;
// the new client works on its own.
func TestContract_MQTT_NewWithIdInUse(t *testing.T) {
	b, port := startTestBroker(t)
	engine := NewLuaEngine()
	defer engine.Close()
	engine.Execute(`_c = {}`)

	connectTestClient(t, engine, b, port, "old", "dup_t", "t/old")

	got, err := engine.Execute(`
		local warns = {}
		local origWarn = Logger.warn
		Logger.warn = function(_, msg, ...) warns[#warns + 1] = Logger:format(msg, ...) end
		local ok, e = pcall(function() _G.nw = MQTT:new("dup_t") end)
		Logger.warn = origWarn
		if not ok then error(e) end
		return tostring(MQTT.getInstance("dup_t") == nw and nw ~= old) .. "|" .. table.concat(warns, "\n")`)
	if err != nil {
		t.Fatalf("lua: %v", err)
	}
	parts := strings.SplitN(got, "|", 2)
	if parts[0] != "true" {
		t.Errorf("registry does not hold the new client: %q", got)
	}
	if len(parts) < 2 || !strings.Contains(parts[1], "dup_t") {
		t.Errorf("want a warning naming dup_t, got %q", got)
	}
	waitMQTT(t, engine, "old client's broker subscription removed", func() bool {
		return !slices.Contains(brokerSubs(b, "vclu_dup_t"), "t/old")
	})

	// The new client connects with the same id and gets its own messages; the old one gets none.
	_, err = engine.Execute(fmt.Sprintf(`
		nw:setHost("127.0.0.1"):setPort(%d)
		nw:subscribe("t/new", function() _c.nw = (_c.nw or 0) + 1 end)
		nw:connect()`, port))
	if err != nil {
		t.Fatalf("lua: %v", err)
	}
	waitMQTT(t, engine, "new client's subscription", func() bool {
		return slices.Contains(brokerSubs(b, "vclu_dup_t"), "t/new")
	})
	b.Publish("t/old", []byte("x"), false)
	b.Publish("t/new", []byte("y"), false)
	waitMQTT(t, engine, "message for the new client", func() bool {
		got, _ := engine.Execute(`return tostring(_c.nw)`)
		return got == "1"
	})
	got, _ = engine.Execute(`
		old:simulateMessage("t/old", "z")
		return tostring(_c.old) .. "," .. tostring(_c.old_any)`)
	if got != "nil,nil" {
		t.Errorf("old client callbacks ran: %s (subscription,onMessage), want nil,nil", got)
	}
}

// =============================================================================
// VirtualObject Contracts
// =============================================================================

// Contract: VirtualSwitch:toggle fires onSwitchOn/onSwitchOff and onChange.
func TestContract_VirtualSwitch_Events(t *testing.T) {
	engine := NewLuaEngine()
	defer engine.Close()

	_, err := engine.Execute(`
		_events = {}
		sw = VirtualSwitch:new("SW1")
		sw:onSwitchOn(function() table.insert(_events, "on") end)
		sw:onSwitchOff(function() table.insert(_events, "off") end)
		sw:onChange(function(val) table.insert(_events, "change:" .. val) end)

		sw:toggle()    -- 0→1
		sw:toggle()    -- 1→0
	`)
	if err != nil {
		t.Fatalf("execute failed: %v", err)
	}

	result, _ := engine.Execute(`table.concat(_events, ",")`)
	// VirtualObject.updateState fires onChange before subclass emits OnSwitchOn/Off
	expected := "change:1,on,change:0,off"
	if result != expected {
		t.Errorf("expected events [%s], got [%s]", expected, result)
	}
}

// Contract: VirtualNumber clamps to configured range.
func TestContract_VirtualNumber_Clamp(t *testing.T) {
	engine := NewLuaEngine()
	defer engine.Close()

	_, err := engine.Execute(`
		temp = VirtualNumber:new("TEMP", {min = 16, max = 30, step = 0.5})
		temp:setValue(15)    -- below min
		_v1 = temp:getValue()
		temp:setValue(35)    -- above max
		_v2 = temp:getValue()
		temp:setValue(22.3)  -- rounded to step
		_v3 = temp:getValue()
	`)
	if err != nil {
		t.Fatalf("execute failed: %v", err)
	}

	assertLua(t, engine, "_v1", "16")
	assertLua(t, engine, "_v2", "30")
	assertLua(t, engine, "_v3", "22.5")
}

// Contract: a VirtualNumber with a fractional step holds the value on the step
// without floating-point noise, so scripts and integrations read 53.9 and not
// 53.900000000000006.
func TestContract_VirtualNumber_FractionalStep(t *testing.T) {
	engine := NewLuaEngine()
	defer engine.Close()

	_, err := engine.Execute(`
		hum = VirtualNumber:new("HUM", {step = 0.1})
		hum:setValue(53.9)
		_s1 = tostring(hum:getValue())

		capped = VirtualNumber:new("CAPPED", {min = 16, step = 0.1})
		capped:setValue(22.9)
		_s2 = tostring(capped:getValue())

		half = VirtualNumber:new("HALF", {step = 0.5})
		half:setValue(0.3)
		_s3 = tostring(half:getValue())

		quarter = VirtualNumber:new("QUARTER", {step = 0.25})
		quarter:setValue(2.3)
		_s4 = tostring(quarter:getValue())

		whole = VirtualNumber:new("WHOLE", {step = 1})
		whole:setValue(2.4)
		_s5 = tostring(whole:getValue())

		neg = VirtualNumber:new("NEG", {step = 0.1})
		neg:setValue(-12.3)
		_s6 = tostring(neg:getValue())
	`)
	if err != nil {
		t.Fatalf("execute failed: %v", err)
	}

	assertLua(t, engine, "_s1", "53.9")
	assertLua(t, engine, "_s2", "22.9")
	assertLua(t, engine, "_s3", "0.5")
	assertLua(t, engine, "_s4", "2.25")
	assertLua(t, engine, "_s5", "2")
	assertLua(t, engine, "_s6", "-12.3")
}

// =============================================================================
// expose() Contracts
// =============================================================================

// Contract: expose() makes object visible in ExposedObjects and fires state_changed on change.
func TestContract_Expose_StateChanged(t *testing.T) {
	engine := NewLuaEngine()
	defer engine.Close()

	_, err := engine.Execute(`
		_state_events = 0
		StateBus:getShared():on("state_changed", function()
			_state_events = _state_events + 1
		end)

		sw = VirtualSwitch:new("SW1")
		sw:expose({ name = "Test Switch", type = "switch" })
		sw:toggle()
	`)
	if err != nil {
		t.Fatalf("execute failed: %v", err)
	}

	result, _ := engine.Execute(`tostring(_state_events)`)
	if result == "0" {
		t.Error("expected state_changed event after toggle on exposed object")
	}
}

// Contract: expose() registers object so ExposedObjects:getAll() includes it.
func TestContract_Expose_GetAll(t *testing.T) {
	engine := NewLuaEngine()
	defer engine.Close()

	_, err := engine.Execute(`
		sw = VirtualSwitch:new("SW1")
		sw:expose({ name = "Test", type = "switch" })
	`)
	if err != nil {
		t.Fatalf("execute failed: %v", err)
	}

	result, _ := engine.Execute(`
		local all = ExposedObjects:list()
		for path, handle in pairs(all) do
			if handle.options and handle.options.name == "Test" then return "found" end
		end
		return "not_found"
	`)
	if result != "found" {
		t.Error("expected exposed object to appear in ExposedObjects:getAll()")
	}
}

// =============================================================================
// inspect() Contracts
// =============================================================================

// Contract: inspect() shows methods for GPIO_DIN and GPIO_DOUT objects.
// (Regression: metatable methods were lost due to array/hash key mismatch.)
func TestContract_Inspect_GPIO_Methods(t *testing.T) {
	engine, _ := newGPIOEngine(t)
	defer engine.Close()

	_, err := engine.Execute(`
		GPIO_DOUT:new("LED1", 18)
		GPIO_DIN:new("BTN1", 17)
	`)
	if err != nil {
		t.Fatalf("setup failed: %v", err)
	}

	// DOUT must show toggle
	result, _ := engine.Execute(`inspect(LED1)`)
	if result == "" {
		t.Fatal("inspect(LED1) returned empty")
	}
	if !strings.Contains(result, "toggle") {
		t.Errorf("inspect(GPIO_DOUT) missing 'toggle' method:\n%s", result)
	}

	// DIN must show onClick
	result, _ = engine.Execute(`inspect(BTN1)`)
	if !strings.Contains(result, "onClick") {
		t.Errorf("inspect(GPIO_DIN) missing 'onClick' method:\n%s", result)
	}
}

// Contract: inspect() shows methods for VirtualSwitch.
func TestContract_Inspect_VirtualSwitch(t *testing.T) {
	engine := NewLuaEngine()
	defer engine.Close()

	engine.Execute(`sw = VirtualSwitch:new("SW1")`)

	result, _ := engine.Execute(`inspect(sw)`)
	for _, method := range []string{"toggle", "switchOn", "switchOff", "getValue"} {
		if !strings.Contains(result, method) {
			t.Errorf("inspect(VirtualSwitch) missing '%s' method:\n%s", method, result)
		}
	}
}

// =============================================================================
// Timer Contracts
// =============================================================================

// Contract: after(ms, fn) fires callback once after delay.
func TestContract_Timer_After(t *testing.T) {
	engine := NewLuaEngine()
	defer engine.Close()
	engine.StartTimers()
	defer engine.StopTimers()

	_, err := engine.Execute(`
		_fired = 0
		after(20, function() _fired = _fired + 1 end)
	`)
	if err != nil {
		t.Fatalf("execute failed: %v", err)
	}

	time.Sleep(50 * time.Millisecond)
	engine.ProcessTimers()

	result, _ := engine.Execute(`tostring(_fired)`)
	if result != "1" {
		t.Errorf("expected after() to fire once, got %s", result)
	}

	// Should NOT fire again
	time.Sleep(50 * time.Millisecond)
	engine.ProcessTimers()

	result, _ = engine.Execute(`tostring(_fired)`)
	if result != "1" {
		t.Errorf("expected after() to fire only once, got %s", result)
	}
}

// Contract: every(ms, fn) fires repeatedly; cancel() stops it.
func TestContract_Timer_Every(t *testing.T) {
	engine := NewLuaEngine()
	defer engine.Close()
	engine.StartTimers()
	defer engine.StopTimers()

	_, err := engine.Execute(`
		_ticks = 0
		_id = every(20, function() _ticks = _ticks + 1 end)
	`)
	if err != nil {
		t.Fatalf("execute failed: %v", err)
	}

	time.Sleep(70 * time.Millisecond)
	engine.ProcessTimers()

	result, _ := engine.Execute(`tostring(_ticks)`)
	if result == "0" {
		t.Error("expected every() to fire at least once")
	}

	// Cancel and verify it stops
	engine.Execute(`cancel(_id)`)
	engine.Execute(`_ticks = 0`)

	time.Sleep(50 * time.Millisecond)
	engine.ProcessTimers()

	result, _ = engine.Execute(`tostring(_ticks)`)
	if result != "0" {
		t.Errorf("expected cancel() to stop timer, got ticks=%s", result)
	}
}

// =============================================================================
// Reload Contracts
// =============================================================================

// Contract: GPIO poll timer works after Reinitialize.
func TestContract_GPIO_PollTimer_SurvivesReload(t *testing.T) {
	ctrl := gpio.NewMockController(gpio.Config{BCMValidation: true})
	gm := NewGPIOManager(ctrl)

	engine := NewLuaEngine()
	engine.SetGPIOManager(gm)
	defer engine.Close()

	// Reinitialize (like Reload)
	if err := engine.Reinitialize("VCLU"); err != nil {
		t.Fatalf("reinitialize failed: %v", err)
	}
	engine.StartTimers()
	defer engine.StopTimers()

	// SetGPIOManager again (Reload path re-registers)
	engine.SetGPIOManager(gm)

	_, err := engine.Execute(`
		_pin_changed = false
		GPIO_DIN:new("BTN", 17)
		EventBus:getShared():on("gpio.pin.17.OnChange", function()
			_pin_changed = true
		end)
	`)
	if err != nil {
		t.Fatalf("setup failed: %v", err)
	}

	ctrl.SimulateInput(17, true)
	engine.ProcessGPIO()

	time.Sleep(80 * time.Millisecond)
	engine.ProcessTimers()

	result, _ := engine.Execute(`tostring(_pin_changed)`)
	if result != "true" {
		t.Errorf("expected GPIO poll timer to work after Reinitialize, got _pin_changed=%s", result)
	}
}

// Contract: Lua globals (__go_*) are available after Reinitialize.
func TestContract_GoFunctions_SurviveReload(t *testing.T) {
	engine := NewLuaEngine()
	defer engine.Close()

	if err := engine.Reinitialize("VCLU"); err != nil {
		t.Fatalf("reinitialize failed: %v", err)
	}

	checks := []string{
		"__go_setTimeout",
		"__go_setInterval",
		"__go_clearTimer",
		"__go_mqtt_poll",
	}

	for _, fn := range checks {
		result, _ := engine.Execute(`return type(` + fn + `)`)
		if result != "function" {
			t.Errorf("expected %s to be function after Reinitialize, got %s", fn, result)
		}
	}
}

// =============================================================================
// Helpers
// =============================================================================

func assertLua(t *testing.T, engine *LuaEngine, expr string, expected string) {
	t.Helper()
	result, err := engine.Execute(`return tostring(` + expr + `)`)
	if err != nil {
		t.Fatalf("assertLua(%s): %v", expr, err)
	}
	if result != expected {
		t.Errorf("assertLua(%s): expected %q, got %q", expr, expected, result)
	}
}
