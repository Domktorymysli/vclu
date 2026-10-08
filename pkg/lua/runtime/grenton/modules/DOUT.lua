--- DOUT - Digital Output module (class 4).
-- Based on: module_dout_fv09_1.xml.
-- @module DOUT

DOUT = {}

DOUT.FEATURE_VALUE = 0          -- Output state: 1=On, 0=Off
DOUT.FEATURE_VOLTAGE_TYPE = 3   -- 0=AC, 1=DC, 2=Signal
DOUT.FEATURE_VOLTAGE_VALUE = 4  -- Voltage value
DOUT.FEATURE_POWER = 5          -- Instantaneous power (read-only)
DOUT.FEATURE_SAFE_MODE_GROUP = 6
DOUT.FEATURE_OVERLOAD = 7       -- Overload threshold

DOUT.VALUE_OFF = 0
DOUT.VALUE_ON = 1

DOUT.METHOD_SWITCH = 0      -- Toggle output
DOUT.METHOD_SWITCH_ON = 1   -- Turn on
DOUT.METHOD_SWITCH_OFF = 2  -- Turn off

DOUT.EVENT_ON_VALUE_CHANGE = 0
DOUT.EVENT_ON_SWITCH_ON = 1
DOUT.EVENT_ON_SWITCH_OFF = 2
DOUT.EVENT_ON_OVERLOAD = 3

DOUT.STATE_ON = 1
DOUT.STATE_OFF = 0

function DOUT:new(id, module, index, eventBus)
    if (id == nil) then error("DOUT: missing id") end
    if (index == nil) then error("DOUT: missing index") end
    if (eventBus == nil) then error("DOUT: missing eventBus") end

    local dout = {
        id = id,
        description = "DOUT",
        module = module,
        index = index,
        address = module.address .. ':' .. index,
        eventBus = eventBus,

        value = DOUT.STATE_OFF,
        voltageType = 2,
        voltageValue = 230,
        power = 0,
        overload = 3000,
        safeModeGroup = 0,
    }

    setmetatable(dout, self)
    self.__index = self

    return dout
end

function DOUT:set(key, value)
    if (value == nil) then
        error("DOUT:set(" .. key .. ", nil)")
    end

    if (DOUT.FEATURE_VALUE == key) then
        -- SetValue method: change state with full event propagation
        self:change(value)
        return self
    end

    if (DOUT.FEATURE_VOLTAGE_TYPE == key) then
        self.voltageType = value
        return self
    end

    if (DOUT.FEATURE_VOLTAGE_VALUE == key) then
        self.voltageValue = value
        return self
    end

    if (DOUT.FEATURE_OVERLOAD == key) then
        self.overload = value
        return self
    end

    if (DOUT.FEATURE_SAFE_MODE_GROUP == key) then
        self.safeModeGroup = value
        return self
    end
end

function DOUT:get(key)
    if (DOUT.FEATURE_VALUE == key) then
        return self.value
    end

    if (DOUT.FEATURE_VOLTAGE_TYPE == key) then
        return self.voltageType
    end

    if (DOUT.FEATURE_VOLTAGE_VALUE == key) then
        return self.voltageValue
    end

    if (DOUT.FEATURE_POWER == key) then
        return self.power
    end

    if (DOUT.FEATURE_OVERLOAD == key) then
        return self.overload
    end

    if (DOUT.FEATURE_SAFE_MODE_GROUP == key) then
        return self.safeModeGroup
    end

    return 0
end

function DOUT:execute(method, time)
    local oldValue = self.value

    if (time == nil) then
        time = 0
    end

    if (DOUT.METHOD_SWITCH == method) then
        if (self.value == DOUT.STATE_ON) then
            self:change(DOUT.STATE_OFF)
            self.eventBus:fire(self, DOUT.EVENT_ON_SWITCH_OFF)
        else
            self:change(DOUT.STATE_ON)
            self.eventBus:fire(self, DOUT.EVENT_ON_SWITCH_ON)
        end

        if (time > 0) then
            self.eventBus:setTimeout(function()
                self:change(oldValue)
            end, time)
        end
    end

    if (DOUT.METHOD_SWITCH_ON == method) then
        self:change(DOUT.STATE_ON)
        self.eventBus:fire(self, DOUT.EVENT_ON_SWITCH_ON)
        if (time > 0) then
            self.eventBus:setTimeout(function()
                self:change(DOUT.STATE_OFF)
                self.eventBus:fire(self, DOUT.EVENT_ON_SWITCH_OFF)
            end, time)
        end
    end

    if (DOUT.METHOD_SWITCH_OFF == method) then
        self:change(DOUT.STATE_OFF)
        self.eventBus:fire(self, DOUT.EVENT_ON_SWITCH_OFF)
        if (time > 0) then
            self.eventBus:setTimeout(function()
                self:change(DOUT.STATE_ON)
                self.eventBus:fire(self, DOUT.EVENT_ON_SWITCH_ON)
            end, time)
        end
    end
end

--- Convenience methods (same API as RemoteSwitch)
function DOUT:switchOn(time)
    self:execute(DOUT.METHOD_SWITCH_ON, time)
end

function DOUT:switchOff(time)
    self:execute(DOUT.METHOD_SWITCH_OFF, time)
end

function DOUT:switch(time)
    self:execute(DOUT.METHOD_SWITCH, time)
end

function DOUT:toggle(time)
    self:execute(DOUT.METHOD_SWITCH, time)
end

function DOUT:on()
    self:switchOn(0)
end

function DOUT:off()
    self:switchOff(0)
end

function DOUT:getValue()
    return self:get(DOUT.FEATURE_VALUE)
end

function DOUT:setValue(value)
    self:set(DOUT.FEATURE_VALUE, value)
end

function DOUT:isOn()
    return self:get(DOUT.FEATURE_VALUE) == DOUT.STATE_ON
end

function DOUT:add_event(eventName, callback)
    self.eventBus:on(self, eventName, callback)
end

--- Register a callback for value changes (ExposedObjects integration)
-- @param callback function Called with (newValue, oldValue)
-- @return function Unsubscribe function
function DOUT:onChange(callback)
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

function DOUT:__getState()
    return {
        id = self.id,
        address = self.address,
        type = 'DOUT',
        value = self:get(DOUT.FEATURE_VALUE),
        voltageType = self:get(DOUT.FEATURE_VOLTAGE_TYPE),
        voltageValue = self:get(DOUT.FEATURE_VOLTAGE_VALUE),
        power = self:get(DOUT.FEATURE_POWER),
        overload = self:get(DOUT.FEATURE_OVERLOAD),
    }
end

function DOUT:change(value)
    local oldValue = self.value
    self.value = value
    if (oldValue ~= value) then
        self.eventBus:fire(self, DOUT.EVENT_ON_VALUE_CHANGE)
        -- Notify onChange subscribers (ExposedObjects emits state_changed via its callback)
        if self._onChangeCallbacks then
            for _, cb in ipairs(self._onChangeCallbacks) do
                cb(value, oldValue)
            end
        end
    end
end

return DOUT
