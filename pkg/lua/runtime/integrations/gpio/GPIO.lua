---
--- GPIO - Low-level GPIO access module with EventBus integration
--- Wraps Go __go_gpio_* functions with pin registry and change polling.
-- @module GPIO
---

local GPIO = {}

-- Pin registry: pin -> {owner=string, mode=string}
GPIO._allocatedPins = {}

--- Setup a GPIO pin
-- @param pin number BCM pin number
-- @param mode string "input" or "output"
-- @param pull string "up", "down", or "off" (default: "off")
-- @param safeState boolean Physical state on Close() (default: false)
-- @return boolean, string|nil success and optional error
function GPIO.setup(pin, mode, pull, safeState)
    local existing = GPIO._allocatedPins[pin]
    if existing then
        error("gpio: pin " .. pin .. " already allocated by " .. existing.owner)
    end
    local ok, err = __go_gpio_setup(pin, mode, pull or "off", safeState or false)
    if not ok then return false, err end
    GPIO._allocatedPins[pin] = {owner = "direct", mode = mode}
    return true
end

--- Internal setup for GPIO_DOUT/GPIO_DIN modules — sets owner + safeState
function GPIO._setupForModule(pin, mode, pull, ownerName, safeState)
    local existing = GPIO._allocatedPins[pin]
    if existing then
        error("gpio: pin " .. pin .. " already allocated by " .. existing.owner)
    end
    local ok, err = __go_gpio_setup(pin, mode, pull or "off", safeState or false)
    if not ok then error("gpio: setup failed: " .. tostring(err)) end
    GPIO._allocatedPins[pin] = {owner = ownerName, mode = mode}
end

--- Write to an output pin
-- @param pin number BCM pin number
-- @param value boolean true=HIGH, false=LOW
function GPIO.write(pin, value)
    local ok, err = __go_gpio_write(pin, value)
    if not ok then error("gpio: write failed: " .. tostring(err)) end
end

--- Read a pin value
-- @param pin number BCM pin number
-- @return number 0 or 1
function GPIO.read(pin)
    local val, err = __go_gpio_read(pin)
    if val == -1 then error("gpio: read failed: " .. tostring(err)) end
    return val
end

--- Toggle an output pin
-- @param pin number BCM pin number
function GPIO.toggle(pin)
    local ok, err = __go_gpio_toggle(pin)
    if not ok then error("gpio: toggle failed: " .. tostring(err)) end
end

--- Release a pin (clear registry + reset in Go controller)
-- @param pin number BCM pin number
function GPIO.release(pin)
    GPIO._allocatedPins[pin] = nil
    if __go_gpio_release then
        __go_gpio_release(pin)
    end
end

--- Get GPIO statistics
-- @return table ManagerStats (decoded from JSON)
function GPIO.stats()
    if not __go_gpio_stats then return {} end
    local jsonStr = __go_gpio_stats()
    return JSON:decode(jsonStr) or {}
end

--- Poll for input changes and emit EventBus/StateBus events
-- Called by a 50ms interval timer set up in bootstrap.
-- Processes max 10 changes per tick to avoid blocking.
function GPIO.pollChanges()
    if not __go_gpio_poll then return end

    for _ = 1, 10 do
        local pin, oldVal, newVal = __go_gpio_poll()
        if not pin then break end

        -- Per-pin event
        EventBus:getShared():emit("gpio.pin." .. pin .. ".OnChange", {
            pin = pin, value = newVal, oldValue = oldVal
        })

        -- Global catch-all event
        EventBus:getShared():emit("gpio.OnChange", {
            pin = pin, value = newVal, oldValue = oldVal
        })

        -- StateBus for MQTT/HomeKit
        StateChange.publish("gpio.pin." .. pin, nil, oldVal, newVal,
            { type = "GPIO_INPUT", origin = "physical", actor = "device:gpio" })
    end
end

return GPIO
