--- ANALOG_OUT - Analog Output module (class 13).
-- Based on: module_analog_f01_0.xml, 0-10V analog output.
-- Voltage output module (0-10V), used for dimming, motor control, valve control etc.
-- @module ANALOG_OUT

ANALOG_OUT = {}

ANALOG_OUT.FEATURE_VALUE = 0          -- Current output voltage (0-10V)
ANALOG_OUT.FEATURE_SCALED_VALUE = 1   -- Value * Scale
ANALOG_OUT.FEATURE_SCALE = 2          -- Scale multiplier, default 1
ANALOG_OUT.FEATURE_RAMP = 3           -- Ramp time in ms for value transitions
ANALOG_OUT.FEATURE_MIN_VALUE = 4      -- Minimum value (V)
ANALOG_OUT.FEATURE_MAX_VALUE = 5      -- Maximum value (V)

-- Legacy alias
ANALOG_OUT.FEATURE_VALUE_PERCENT = 1

ANALOG_OUT.METHOD_SET_VALUE = 0

ANALOG_OUT.EVENT_ON_CHANGE = 0
ANALOG_OUT.EVENT_ON_LOWER = 1
ANALOG_OUT.EVENT_ON_RISE = 2
ANALOG_OUT.EVENT_ON_OUT_RANGE = 3
ANALOG_OUT.EVENT_ON_SWITCH_ON = 4
ANALOG_OUT.EVENT_ON_SWITCH_OFF = 5

function ANALOG_OUT:new(id, module, index, eventBus)
    if (id == nil) then error("ANALOG_OUT: missing id") end
    if (eventBus == nil) then error("ANALOG_OUT: missing eventBus") end

    local analogOut = {
        id = id,
        description = "ANALOG_OUT",
        module = module,
        index = index or 0,
        address = module.address .. ':' .. (index or 0),
        eventBus = eventBus,

        value = 0,
        scale = 1,
        ramp = 500,
        minValue = 0,
        maxValue = 10,
    }

    setmetatable(analogOut, self)
    self.__index = self

    return analogOut
end

function ANALOG_OUT:set(key, value)
    if (value == nil) then
        error("ANALOG_OUT:set(" .. key .. ", nil)")
    end

    if (ANALOG_OUT.FEATURE_VALUE == key) then
        local oldValue = self.value
        self.value = value
        if (oldValue ~= value) then
            self.eventBus:fire(self, ANALOG_OUT.EVENT_ON_CHANGE)
            if value > oldValue then
                self.eventBus:fire(self, ANALOG_OUT.EVENT_ON_RISE)
            else
                self.eventBus:fire(self, ANALOG_OUT.EVENT_ON_LOWER)
            end
            if value >= self.maxValue then
                self.eventBus:fire(self, ANALOG_OUT.EVENT_ON_SWITCH_ON)
            elseif value <= self.minValue then
                self.eventBus:fire(self, ANALOG_OUT.EVENT_ON_SWITCH_OFF)
            end
            if self._onChangeCallbacks then
                for _, cb in ipairs(self._onChangeCallbacks) do
                    cb(value, oldValue)
                end
            end
        end
        return self
    end

    if (ANALOG_OUT.FEATURE_SCALED_VALUE == key) then
        -- SetScaledValue: value = scaledValue / scale
        if self.scale ~= 0 then
            self:set(ANALOG_OUT.FEATURE_VALUE, value / self.scale)
        end
        return self
    end

    if (ANALOG_OUT.FEATURE_SCALE == key) then
        self.scale = value
        return self
    end

    if (ANALOG_OUT.FEATURE_RAMP == key) then
        self.ramp = value
        return self
    end

    if (ANALOG_OUT.FEATURE_MIN_VALUE == key) then
        self.minValue = value
        return self
    end

    if (ANALOG_OUT.FEATURE_MAX_VALUE == key) then
        self.maxValue = value
        return self
    end
end

function ANALOG_OUT:get(key)
    if (ANALOG_OUT.FEATURE_VALUE == key) then
        return self.value
    end

    if (ANALOG_OUT.FEATURE_SCALED_VALUE == key) then
        return self.value * self.scale
    end

    if (ANALOG_OUT.FEATURE_SCALE == key) then
        return self.scale
    end

    if (ANALOG_OUT.FEATURE_RAMP == key) then
        return self.ramp
    end

    if (ANALOG_OUT.FEATURE_MIN_VALUE == key) then
        return self.minValue
    end

    if (ANALOG_OUT.FEATURE_MAX_VALUE == key) then
        return self.maxValue
    end

    return 0
end

function ANALOG_OUT:execute(method, value)
    if (ANALOG_OUT.METHOD_SET_VALUE == method) then
        self:set(ANALOG_OUT.FEATURE_VALUE, value or 0)
    end
end

--- Convenience methods (matching Grenton ANALOG_OUT interface)

function ANALOG_OUT:setValue(val)
    self:set(ANALOG_OUT.FEATURE_VALUE, val)
end

function ANALOG_OUT:getValue()
    return self:get(ANALOG_OUT.FEATURE_VALUE)
end

function ANALOG_OUT:getScaledValue()
    return self:get(ANALOG_OUT.FEATURE_SCALED_VALUE)
end

function ANALOG_OUT:getPercent()
    if (self.maxValue == self.minValue) then return 0 end
    return ((self.value - self.minValue) / (self.maxValue - self.minValue)) * 100
end

function ANALOG_OUT:switchOn(time)
    local oldValue = self.value
    self:setValue(self.maxValue)
    if time and time > 0 then
        self.eventBus:setTimeout(function()
            self:setValue(oldValue)
        end, time)
    end
end

function ANALOG_OUT:switchOff(time)
    local oldValue = self.value
    self:setValue(self.minValue)
    if time and time > 0 then
        self.eventBus:setTimeout(function()
            self:setValue(oldValue)
        end, time)
    end
end

function ANALOG_OUT:switch(time)
    if self.value > self.minValue then
        self:switchOff(time)
    else
        self:switchOn(time)
    end
end

function ANALOG_OUT:toggle(time)
    self:switch(time)
end

function ANALOG_OUT:on()
    self:switchOn(0)
end

function ANALOG_OUT:off()
    self:switchOff(0)
end

function ANALOG_OUT:add_event(eventName, callback)
    self.eventBus:on(self, eventName, callback)
end

--- Register a callback for value changes (ExposedObjects integration)
-- @param callback function Called with (newValue, oldValue)
-- @return function Unsubscribe function
function ANALOG_OUT:onChange(callback)
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

function ANALOG_OUT:__getState()
    return {
        id = self.id,
        address = self.address,
        type = 'ANALOG_OUT',
        value = self:get(ANALOG_OUT.FEATURE_VALUE),
        scaledValue = self:get(ANALOG_OUT.FEATURE_SCALED_VALUE),
        scale = self:get(ANALOG_OUT.FEATURE_SCALE),
        ramp = self:get(ANALOG_OUT.FEATURE_RAMP),
        minValue = self:get(ANALOG_OUT.FEATURE_MIN_VALUE),
        maxValue = self:get(ANALOG_OUT.FEATURE_MAX_VALUE),
    }
end

return ANALOG_OUT
