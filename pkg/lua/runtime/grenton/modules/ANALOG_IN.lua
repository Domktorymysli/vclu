--- ANALOG_IN - Analog Input module (class 12).
-- Based on: module_analog_f01_0.xml, 0-10V analog input.
-- @module ANALOG_IN

ANALOG_IN = {}

ANALOG_IN.FEATURE_VALUE = 0          -- Current analog value (read-only)
ANALOG_IN.FEATURE_VALUE_PERCENT = 1  -- Value as percentage 0-100% (read-only)
ANALOG_IN.FEATURE_SCALE = 2          -- Scale multiplier, default 1
ANALOG_IN.FEATURE_SENSITIVITY = 3    -- Change sensitivity threshold
ANALOG_IN.FEATURE_INERTIA = 4        -- Inertia / smoothing
ANALOG_IN.FEATURE_MIN_VALUE = 5      -- Minimum value
ANALOG_IN.FEATURE_MAX_VALUE = 6      -- Maximum value

ANALOG_IN.EVENT_ON_CHANGE = 0

function ANALOG_IN:new(id, module, index, eventBus)
    if (id == nil) then error("ANALOG_IN: missing id") end
    if (eventBus == nil) then error("ANALOG_IN: missing eventBus") end

    local analogIn = {
        id = id,
        description = "ANALOG_IN",
        module = module,
        index = index or 0,
        address = module.address .. ':' .. (index or 0),
        eventBus = eventBus,

        value = 0,
        scale = 1,
        sensitivity = 0,
        inertia = 0,
        minValue = 0,
        maxValue = 10,
    }

    setmetatable(analogIn, self)
    self.__index = self

    return analogIn
end

function ANALOG_IN:set(key, value)
    if (value == nil) then
        error("ANALOG_IN:set(" .. key .. ", nil)")
    end

    if (ANALOG_IN.FEATURE_SCALE == key) then
        self.scale = value
        return self
    end

    if (ANALOG_IN.FEATURE_SENSITIVITY == key) then
        self.sensitivity = value
        return self
    end

    if (ANALOG_IN.FEATURE_INERTIA == key) then
        self.inertia = value
        return self
    end

    if (ANALOG_IN.FEATURE_MIN_VALUE == key) then
        self.minValue = value
        return self
    end

    if (ANALOG_IN.FEATURE_MAX_VALUE == key) then
        self.maxValue = value
        return self
    end
end

function ANALOG_IN:get(key)
    if (ANALOG_IN.FEATURE_VALUE == key) then
        return self.value
    end

    if (ANALOG_IN.FEATURE_VALUE_PERCENT == key) then
        if (self.maxValue == self.minValue) then
            return 0
        end
        return ((self.value - self.minValue) / (self.maxValue - self.minValue)) * 100
    end

    if (ANALOG_IN.FEATURE_SCALE == key) then
        return self.scale
    end

    if (ANALOG_IN.FEATURE_SENSITIVITY == key) then
        return self.sensitivity
    end

    if (ANALOG_IN.FEATURE_INERTIA == key) then
        return self.inertia
    end

    if (ANALOG_IN.FEATURE_MIN_VALUE == key) then
        return self.minValue
    end

    if (ANALOG_IN.FEATURE_MAX_VALUE == key) then
        return self.maxValue
    end

    return 0
end

function ANALOG_IN:execute(key, value)
end

--- Convenience: get current value
function ANALOG_IN:getValue()
    return self:get(ANALOG_IN.FEATURE_VALUE)
end

--- Convenience: get value as percentage (0-100)
function ANALOG_IN:getPercent()
    return self:get(ANALOG_IN.FEATURE_VALUE_PERCENT)
end

function ANALOG_IN:add_event(eventName, callback)
    self.eventBus:on(self, eventName, callback)
end

function ANALOG_IN:__getState()
    return {
        id = self.id,
        address = self.address,
        type = 'ANALOG_IN',
        value = self:get(ANALOG_IN.FEATURE_VALUE),
        scale = self:get(ANALOG_IN.FEATURE_SCALE),
        sensitivity = self:get(ANALOG_IN.FEATURE_SENSITIVITY),
        inertia = self:get(ANALOG_IN.FEATURE_INERTIA),
        minValue = self:get(ANALOG_IN.FEATURE_MIN_VALUE),
        maxValue = self:get(ANALOG_IN.FEATURE_MAX_VALUE),
    }
end

return ANALOG_IN
