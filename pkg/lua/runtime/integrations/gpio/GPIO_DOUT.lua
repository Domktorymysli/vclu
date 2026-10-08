---
--- GPIO_DOUT - GPIO pin as DOUT-compatible output object
--- Bridges a GPIO output pin to the Grenton DOUT API with activeLow support.
-- @module GPIO_DOUT
---

local GPIO_DOUT = {}
GPIO_DOUT.__index = GPIO_DOUT

--- Create a new GPIO_DOUT object
-- @api public
-- @param name string Global name (e.g., "RELAY1")
-- @param pin number BCM pin number
-- @param opts table Optional: {activeLow = bool}
-- @return GPIO_DOUT instance
function GPIO_DOUT:new(name, pin, opts)
    opts = opts or {}
    local activeLow = opts.activeLow or false
    -- safeState = physical state for "relay OFF":
    --   activeLow: safe=true (HIGH = relay OFF)
    --   activeHigh: safe=false (LOW = OFF)
    local safeState = activeLow

    gpio._setupForModule(pin, "output", "off", name, safeState)

    local obj = {
        id = name,
        description = "DOUT",
        pin = pin,
        activeLow = activeLow,
        value = 0,
        eventBus = EventBus:getShared(),
        _type = "DOUT",
        _name = opts.name or name,
    }

    setmetatable(obj, self)

    -- Set safe startup state (relay OFF)
    obj:_writePin(false)

    _G[name] = obj

    -- Register in _registry for Access Control / exposeRegistry()
    if _registry and registerObject then
        registerObject("gpio." .. name, obj)
    end

    return obj
end

--- Convert logical ON/OFF to physical pin state and write
function GPIO_DOUT:_writePin(logicalOn)
    if self.activeLow then
        gpio.write(self.pin, not logicalOn) -- invert
    else
        gpio.write(self.pin, logicalOn)
    end
end

--- Read pin and convert physical state to logical value
function GPIO_DOUT:_readPin()
    local raw = gpio.read(self.pin)
    if self.activeLow then
        return (raw == 0) and 1 or 0 -- invert: LOW=ON, HIGH=OFF
    end
    return raw
end

function GPIO_DOUT:execute(method, time)
    local oldValue = self.value

    if time == nil then time = 0 end

    if method == DOUT.METHOD_SWITCH then
        if self.value == DOUT.STATE_ON then
            self:_writePin(false)
            self:change(DOUT.STATE_OFF)
            self.eventBus:fire(self, DOUT.EVENT_ON_SWITCH_OFF)
        else
            self:_writePin(true)
            self:change(DOUT.STATE_ON)
            self.eventBus:fire(self, DOUT.EVENT_ON_SWITCH_ON)
        end

        if time > 0 then
            self.eventBus:setTimeout(function()
                self:_writePin(oldValue == 1)
                self:change(oldValue)
            end, time)
        end
    end

    if method == DOUT.METHOD_SWITCH_ON then
        self:_writePin(true)
        self:change(DOUT.STATE_ON)
        self.eventBus:fire(self, DOUT.EVENT_ON_SWITCH_ON)
        if time > 0 then
            self.eventBus:setTimeout(function()
                self:_writePin(false)
                self:change(DOUT.STATE_OFF)
                self.eventBus:fire(self, DOUT.EVENT_ON_SWITCH_OFF)
            end, time)
        end
    end

    if method == DOUT.METHOD_SWITCH_OFF then
        self:_writePin(false)
        self:change(DOUT.STATE_OFF)
        self.eventBus:fire(self, DOUT.EVENT_ON_SWITCH_OFF)
        if time > 0 then
            self.eventBus:setTimeout(function()
                self:_writePin(true)
                self:change(DOUT.STATE_ON)
                self.eventBus:fire(self, DOUT.EVENT_ON_SWITCH_ON)
            end, time)
        end
    end
end

function GPIO_DOUT:get(key)
    if key == DOUT.FEATURE_VALUE then
        return self:_readPin()
    end
    return 0
end

function GPIO_DOUT:set(key, value)
    if key == DOUT.FEATURE_VALUE then
        self:_writePin(value == 1)
        self:change(value)
    end
    return self
end

function GPIO_DOUT:change(value)
    local oldValue = self.value
    self.value = value
    if oldValue ~= value then
        self.eventBus:fire(self, DOUT.EVENT_ON_VALUE_CHANGE)
        if self._onChangeCallbacks then
            for _, cb in ipairs(self._onChangeCallbacks) do
                cb(value, oldValue)
            end
        end
    end
end

function GPIO_DOUT:add_event(eventName, callback)
    self.eventBus:on(self, eventName, callback)
end

function GPIO_DOUT:onChange(callback)
    if not self._onChangeCallbacks then
        self._onChangeCallbacks = {}
    end
    table.insert(self._onChangeCallbacks, callback)
    return function()
        for i, cb in ipairs(self._onChangeCallbacks or {}) do
            if cb == callback then
                table.remove(self._onChangeCallbacks, i)
                break
            end
        end
    end
end

-- Fluent API (compatible with VirtualSwitch)
function GPIO_DOUT:switchOn()  self:set(0, 1) end
function GPIO_DOUT:switchOff() self:set(0, 0) end
function GPIO_DOUT:on()        self:set(0, 1) end
function GPIO_DOUT:off()       self:set(0, 0) end
function GPIO_DOUT:toggle()    self:set(0, self.value == 1 and 0 or 1) end
function GPIO_DOUT:getValue()  return self:get(0) end
function GPIO_DOUT:setValue(v) self:set(0, v) end
function GPIO_DOUT:isOn()      return self.value == 1 end

function GPIO_DOUT:__getState()
    return {
        id = self.id,
        type = "GPIO_DOUT",
        pin = self.pin,
        activeLow = self.activeLow,
        value = self:get(DOUT.FEATURE_VALUE),
    }
end

return GPIO_DOUT
