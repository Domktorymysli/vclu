--- Remote light class for DIMMER objects.
-- Provides control for dimmable lights with brightness control.
-- @module objects.RemoteLight
-- @inherits RemoteObject

RemoteLight = setmetatable({}, {__index = RemoteObject})
RemoteLight.__index = RemoteLight

--- Creates a new RemoteLight instance.
-- @param clu CLU_REMOTE The CLU instance this light belongs to
-- @param objId string The unique identifier of the light
-- @param name string Optional name for the light
-- @return RemoteLight The new remote light instance
function RemoteLight:new(clu, objId, name)
    local obj = RemoteObject.new(self, clu, objId, name)
    obj._type = "DIMMER"
    obj._allowedMethods = {"getValue", "setValue", "getBrightness", "setBrightness", "switchOn", "switchOff", "on", "off", "toggle", "dim"}
    return obj
end

--- Gets the current brightness value (from local cache, updated by subscriptions).
-- @return number Current brightness value (0.0 to 1.0)
function RemoteLight:getValue()
    return self._lastState
end

--- Sets the brightness value (0.0 to 1.0).
-- @param value number Brightness value
-- @param ramp number Optional ramp time in milliseconds
function RemoteLight:setValue(value, ramp)
    if ramp then
        self:execute(0, value, ramp)
    else
        self:set(0, value)
    end
end

--- Gets the current brightness as a percentage.
-- @return number Brightness percentage (0 to 100)
function RemoteLight:getBrightness()
    return math.floor(self:getValue() * 100)
end

--- Sets the brightness as a percentage.
-- @param percent number Brightness percentage (0 to 100)
-- @param ramp number Optional ramp time in milliseconds
function RemoteLight:setBrightness(percent, ramp)
    self:setValue(percent / 100, ramp)
end

--- Turns the light on.
-- @param time number Optional duration in milliseconds
-- @param ramp number Optional ramp time
function RemoteLight:switchOn(time, ramp)
    self:execute(2, time or 0, ramp)
    self:updateState(1)
end

--- Turns the light off.
-- @param time number Optional duration in milliseconds
-- @param ramp number Optional ramp time
function RemoteLight:switchOff(time, ramp)
    self:execute(3, time or 0, ramp)
    self:updateState(0)
end

--- Shorthand to turn the light on immediately.
-- @param time number Optional duration in milliseconds
-- @param ramp number Optional ramp time
function RemoteLight:on(time, ramp)
    self:switchOn(time or 0, ramp)
end

--- Turns the light off.
-- @param time number Optional duration in milliseconds
-- @param ramp number Optional ramp time
function RemoteLight:off(time, ramp)
    self:switchOff(time or 0, ramp)
end

--- Toggles the light state.
-- @param time number Optional duration in milliseconds
-- @param ramp number Optional ramp time
function RemoteLight:toggle(time, ramp)
    self:execute(1, time or 0, ramp)
end

--- Starts dimming operation (hold function).
-- @param ramp number Ramp time for dimming
function RemoteLight:dim(ramp)
    self:execute(0, ramp)
end

--- Registers a callback for when the light turns on.
-- @param callback function Function called when light turns on
-- @return RemoteLight Self for method chaining
function RemoteLight:onSwitchOn(callback)
    return self:onEvent("OnSwitchOn", callback)
end

--- Registers a callback for when the light turns off.
-- @param callback function Function called when light turns off
-- @return RemoteLight Self for method chaining
function RemoteLight:onSwitchOff(callback)
    return self:onEvent("OnSwitchOff", callback)
end

return RemoteLight
