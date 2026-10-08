GenericButton = {}

GenericButton.MODE_MONO = 0   -- 0 - monostabilny
GenericButton.MODE_BI = 1     -- 1 - bistabilny
GenericButton.MODE_LOCKED = 2 -- 2 - locked

GenericButton.EVENT_ON_VALUE_CHANGE = 0
GenericButton.EVENT_ON_SWITCH_ON = 1
GenericButton.EVENT_ON_SWITCH_OFF = 2
GenericButton.EVENT_ON_SHORT_PRESS = 3
GenericButton.EVENT_ON_LONG_PRESS = 4
GenericButton.EVENT_ON_HOLD = 5
GenericButton.EVENT_ON_CLICK = 6

GenericButton.STATE_ON = 1
GenericButton.STATE_OFF = 0

function GenericButton:new(module, eventBus)

    local genericButton = {
        mode = 0,
        inertion = 0,

        holdDelay = 500,
        holdInterval = 100,

        module = module,
        eventBus = eventBus,

        description = module.description,
        address = module.address,

        value = GenericButton.STATE_OFF,

        shortPressInterval = 2000,
        longPressInterval = 5000,
        ultraLongPressInterval = 10000,

        onIntertion = nil,
        onHold = nil,
        onHoldDelay = nil,
        onShortPress = nil,
        onLongPress = nil,
        onUltraLongPress = nil,

        inertionFlag = false,
        holdFlag = false,
        shortPressFlag = false,
        longPressFlag = false
    }

    setmetatable(genericButton, self)
    self.__index = self

    return genericButton

end

function GenericButton:getValue()
    return self.value
end

function GenericButton:setMode(mode)
    if (mode ~= GenericButton.MODE_MONO and mode ~= GenericButton.MODE_BI and mode ~= GenericButton.MODE_LOCKED) then
        error("wrong button mode. given " .. mode)
    end
    self.mode = mode
end

function GenericButton:getMode()
    return self.mode
end

function GenericButton:setInertion(inertion)
    self.inertion = inertion
end

function GenericButton:getInertion()
    return self.inertion
end

function GenericButton:setHoldDelay(holdDelay)
    self.holdDelay = holdDelay
end

function GenericButton:getHoldDelay()
    return self.holdDelay
end

function GenericButton:setHoldInterval(holdInterval)
    self.holdInterval = holdInterval
end

function GenericButton:getHoldInterval()
    return self.holdInterval
end

function GenericButton:setShortPressInterval(value)
    self.shortPressInterval = value
end

function GenericButton:getShortPressInterval()
    return self.shortPressInterval
end

function GenericButton:setLongPressInterval(value)
    self.longPressInterval = value
end

function GenericButton:getLongPressInterval()
    return self.longPressInterval
end

function GenericButton:setUltraLongPressInterval(value)
    self.ultraLongPressInterval = value
end

function GenericButton:getUltraLongPressInterval()
    return self.ultraLongPressInterval
end

function GenericButton:press()
    if (self.MODE_LOCKED == self.mode) then
        return
    end

    if (self.MODE_BI == self.mode) then
        return
    end

    if (self.MODE_MONO == self.mode) then
        self.onIntertion = self.eventBus:setTimeout(function()
            self.inertionFlag = true
            self.eventBus:fire(self.module, GenericButton.EVENT_ON_CLICK)
            self:change(GenericButton.STATE_ON)
        end, self.inertion)

        self.onHoldDelay = self.eventBus:setTimeout(function()
            self.holdFlag = true
        end, self.holdDelay)

        self.onHold = self.eventBus:setInterval(function()
            if (self.holdFlag == true) then
                self.eventBus:fire(self.module, GenericButton.EVENT_ON_HOLD)
            end
        end, self.holdInterval)

        self.onShortPress = self.eventBus:setTimeout(function()
            self.shortPressFlag = true
        end, self.shortPressInterval)

        self.onLongPress = self.eventBus:setTimeout(function()
            self.longPressFlag = true
            self.shortPressFlag = false
        end, self.longPressInterval)

        self.onUltraLongPress = self.eventBus:setTimeout(function()
            self.longPressFlag = false
            self.shortPressFlag = false
        end, self.ultraLongPressInterval)
    end
end

function GenericButton:release()
    if (self.MODE_LOCKED == self.mode) then
        return
    end

    if (self.MODE_BI == self.mode) then
        self:change()
        return
    end

    if (self.MODE_MONO == self.mode) then
        if (self.inertionFlag == true) then
            self:change(GenericButton.STATE_OFF)
        end

        if (self.shortPressFlag == true) then
            Logger.getShared():debug("GenericButton.EVENT_ON_SHORT_PRESS")
            self.eventBus:fire(self.module, GenericButton.EVENT_ON_SHORT_PRESS)
        end

        if (self.longPressFlag == true) then
            Logger.getShared():debug("GenericButton.EVENT_ON_LONG_PRESS")
            self.eventBus:fire(self.module, GenericButton.EVENT_ON_LONG_PRESS)
        end

        self.eventBus:clearInterval(self.onIntertion)
        self.eventBus:clearInterval(self.onHold)
        self.eventBus:clearInterval(self.onShortPress)
        self.eventBus:clearInterval(self.onLongPress)
        self.eventBus:clearInterval(self.onUltraLongPress)

        self.holdFlag = false
        self.inertionFlag = false
        self.longPressFlag = false
        self.shortPressInterval = false
    end
end

function GenericButton:change(value)
    if (value == nil) then
        if (self.STATE_ON == self.value) then
            value = self.STATE_OFF
        else
            value = self.STATE_ON
        end
    end

    local oldValue = self.value

    self.value = value
    self.module.value = self.value

    if (oldValue ~= value) then
        Logger.getShared():debug("GenericButton.EVENT_ON_VALUE_CHANGE")
        self.eventBus:fire(self.module, GenericButton.EVENT_ON_VALUE_CHANGE);
        -- Notify onChange subscribers on the parent module (ExposedObjects emits state_changed via its callback)
        if self.module._onChangeCallbacks then
            for _, cb in ipairs(self.module._onChangeCallbacks) do
                cb(value, oldValue)
            end
        end
    end

    if (self.STATE_ON == value) then
        Logger.getShared():debug("GenericButton.EVENT_ON_SWITCH_ON")
        self.eventBus:fire(self.module, GenericButton.EVENT_ON_SWITCH_ON)
    end

    if (self.STATE_OFF == value) then
        Logger.getShared():debug("GenericButton.EVENT_ON_SWITCH_OFF")
        self.eventBus:fire(self.module, GenericButton.EVENT_ON_SWITCH_OFF)
    end
end

return GenericButton
