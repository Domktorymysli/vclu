--- DIN - Digital Input module (class 3).
-- Based on: module_din_fv10_1.xml.
-- @module DIN

DIN = {}

DIN.FEATURE_VALUE = 0          -- Input state 0 or 1, read-only
DIN.FEATURE_INTERTION = 1      -- Debounce time (ms)
DIN.FEATURE_HOLD_DELAY = 2     -- Hold delay before OnHold fires (ms)
DIN.FEATURE_HOLD_INTERVAL = 3  -- Cyclic hold interval (ms)
DIN.FEATURE_STATISTIC_STATE = 4
DIN.FEATURE_LOAD = 5
DIN.FEATURE_SAFE_MODE_GROUP = 6

DIN.EVENT_ON_CHANGE = 0
DIN.EVENT_ON_SWITCH_ON = 1
DIN.EVENT_ON_SWITCH_OFF = 2
DIN.EVENT_ON_SHORT_PRESS = 3
DIN.EVENT_ON_LONG_PRESS = 4
DIN.EVENT_ON_HOLD = 5
DIN.EVENT_ON_CLICK = 6

function DIN:new(id, module, index, eventBus)
    if (id == nil) then error("DIN: missing id") end
    if (index == nil) then error("DIN: missing index") end
    if (eventBus == nil) then error("DIN: missing eventBus") end

    local din = {
        id = id,
        description = "DIN",
        module = module,
        index = index,
        address = module.address .. ':' .. index,
        eventBus = eventBus,

        statisticState = 0,
        load = 0,
        value = GenericButton.STATE_OFF,
        genericButton = nil,
        safeModeGroup = 0,
    }

    setmetatable(din, self)
    self.__index = self

    din.genericButton = GenericButton:new(din, eventBus)
    din.genericButton:setInertion(0)
    din.genericButton:setHoldDelay(500)
    din.genericButton:setHoldInterval(50)

    return din
end

function DIN:set(key, value)
    if (value == nil) then
        error("DIN:set(" .. key .. ", nil)")
    end

    if (DIN.FEATURE_INTERTION == key) then
        self.genericButton:setInertion(value)
        return self
    end

    if (DIN.FEATURE_HOLD_DELAY == key) then
        self.genericButton:setHoldDelay(value)
        return self
    end

    if (DIN.FEATURE_HOLD_INTERVAL == key) then
        self.genericButton:setHoldInterval(value)
        return self
    end

    if (DIN.FEATURE_STATISTIC_STATE == key) then
        return self
    end

    if (DIN.FEATURE_LOAD == key) then
        return self
    end

    if (DIN.FEATURE_SAFE_MODE_GROUP == key) then
        self.safeModeGroup = value
        return self
    end
end

function DIN:get(key)
    if (DIN.FEATURE_VALUE == key) then
        return self.genericButton:getValue()
    end

    if (DIN.FEATURE_INTERTION == key) then
        return self.genericButton:getInertion()
    end

    if (DIN.FEATURE_HOLD_DELAY == key) then
        return self.genericButton:getHoldDelay()
    end

    if (DIN.FEATURE_HOLD_INTERVAL == key) then
        return self.genericButton:getHoldInterval()
    end

    if (DIN.FEATURE_STATISTIC_STATE == key) then
        return self.statisticState
    end

    if (DIN.FEATURE_LOAD == key) then
        return self.load
    end

    if (DIN.FEATURE_SAFE_MODE_GROUP == key) then
        return self.safeModeGroup
    end

    return 0
end

function DIN:execute(key, value)
end

--- Convenience: get current input value (0 or 1)
function DIN:getValue()
    return self:get(DIN.FEATURE_VALUE)
end

--- Convenience: check if input is active (pressed/high)
function DIN:isActive()
    return self:get(DIN.FEATURE_VALUE) == 1
end

function DIN:add_event(eventName, callback)
    self.eventBus:on(self, eventName, callback)
end

--- Register a callback for value changes (ExposedObjects integration)
-- @param callback function Called with (newValue, oldValue)
-- @return function Unsubscribe function
function DIN:onChange(callback)
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

function DIN:__getState()
    return {
        id = self.id,
        address = self.address,
        type = 'DIN',
        intertion = self:get(DIN.FEATURE_INTERTION),
        holdDelay = self:get(DIN.FEATURE_HOLD_DELAY),
        holdInterval = self:get(DIN.FEATURE_HOLD_INTERVAL),
        value = self:get(DIN.FEATURE_VALUE),
    }
end

function DIN:__press()
    self.genericButton:press()
end

function DIN:__release()
    self.genericButton:release()
end

return DIN
