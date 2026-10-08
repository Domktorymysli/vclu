---
--- GPIO_DIN - GPIO pin as DIN-compatible input object
--- Bridges a GPIO input pin to the Grenton DIN API via GenericButton.
-- @module GPIO_DIN
---

local GPIO_DIN = {}
GPIO_DIN.__index = GPIO_DIN

--- Create a new GPIO_DIN object
-- @api public
-- @param name string Global name (e.g., "BTN1")
-- @param pin number BCM pin number
-- @param pull string "up", "down", or "off" (default: "up")
-- @return GPIO_DIN instance
function GPIO_DIN:new(name, pin, pull)
    gpio._setupForModule(pin, "input", pull or "up", name, false)

    local obj = {
        id = name,
        description = "DIN",
        pin = pin,
        value = 0,
        eventBus = EventBus:getShared(),
        _type = "DIN",
        _name = name,
    }

    setmetatable(obj, self)

    -- GenericButton: debounce + hold + press detection
    obj.genericButton = GenericButton:new(obj, EventBus:getShared())
    obj.genericButton:setInertion(50)
    obj.genericButton:setHoldDelay(500)
    obj.genericButton:setHoldInterval(50)

    _G[name] = obj

    -- Register in _registry for Access Control / exposeRegistry()
    if _registry and registerObject then
        registerObject("gpio." .. name, obj)
    end

    -- Listen for GPIO pin changes -> GenericButton press/release
    EventBus:getShared():on("gpio.pin." .. pin .. ".OnChange", function(data)
        if data.value == 1 then
            obj.genericButton:press()
        else
            obj.genericButton:release()
        end
    end)

    return obj
end

function GPIO_DIN:get(key)
    if key == DIN.FEATURE_VALUE then return self.genericButton:getValue() end
    if key == DIN.FEATURE_INTERTION then return self.genericButton:getInertion() end
    if key == DIN.FEATURE_HOLD_DELAY then return self.genericButton:getHoldDelay() end
    if key == DIN.FEATURE_HOLD_INTERVAL then return self.genericButton:getHoldInterval() end
    return 0
end

function GPIO_DIN:set(key, value)
    if key == DIN.FEATURE_INTERTION then self.genericButton:setInertion(value) end
    if key == DIN.FEATURE_HOLD_DELAY then self.genericButton:setHoldDelay(value) end
    if key == DIN.FEATURE_HOLD_INTERVAL then self.genericButton:setHoldInterval(value) end
    return self
end

function GPIO_DIN:execute(key, value)
end

function GPIO_DIN:add_event(eventName, callback)
    self.eventBus:on(self, eventName, callback)
end

function GPIO_DIN:onChange(callback)
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

-- Fluent API (compatible with VirtualSensor)
function GPIO_DIN:setDebounce(ms)   self:set(1, ms) return self end
function GPIO_DIN:setHoldDelay(ms)  self:set(2, ms) return self end
function GPIO_DIN:setHoldInterval(ms) self:set(3, ms) return self end

function GPIO_DIN:getValue()  return self:get(0) end
function GPIO_DIN:isActive()  return self:get(0) == 1 end

function GPIO_DIN:onSwitchOn(callback)
    self:add_event(DIN.EVENT_ON_SWITCH_ON, callback)
    return self
end

function GPIO_DIN:onSwitchOff(callback)
    self:add_event(DIN.EVENT_ON_SWITCH_OFF, callback)
    return self
end

function GPIO_DIN:onClick(callback)
    self:add_event(DIN.EVENT_ON_CLICK, callback)
    return self
end

function GPIO_DIN:onLongPress(callback)
    self:add_event(DIN.EVENT_ON_LONG_PRESS, callback)
    return self
end

function GPIO_DIN:onHold(callback)
    self:add_event(DIN.EVENT_ON_HOLD, callback)
    return self
end

function GPIO_DIN:__getState()
    return {
        id = self.id,
        type = "GPIO_DIN",
        pin = self.pin,
        intertion = self:get(DIN.FEATURE_INTERTION),
        holdDelay = self:get(DIN.FEATURE_HOLD_DELAY),
        holdInterval = self:get(DIN.FEATURE_HOLD_INTERVAL),
        value = self:get(DIN.FEATURE_VALUE),
    }
end

function GPIO_DIN:__press()
    self.genericButton:press()
end

function GPIO_DIN:__release()
    self.genericButton:release()
end

return GPIO_DIN
