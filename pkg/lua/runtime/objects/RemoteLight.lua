--- Remote light class for DIMMER objects.
-- Provides control for dimmable lights with brightness control.
-- @module objects.RemoteLight
-- @inherits RemoteObject

RemoteLight = setmetatable({}, {__index = RemoteObject})
RemoteLight.__index = RemoteLight

-- Execute indexes of DIMMER (class 5, Object Manager device-interfaces); LEDRGB has others
RemoteLight.METHODS = { hold = 0, switch = 1, switchOn = 2, switchOff = 3 }
-- DIMMER feature 1, set: the ramp of the next changes
RemoteLight.FEATURE_RAMP_TIME = 1

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
-- A dimmer has no SetValue with a ramp: a ramp is written to its RampTime first and stays
-- for later changes.
-- @param value number Brightness value
-- @param ramp number Optional ramp time in milliseconds
function RemoteLight:setValue(value, ramp)
    if ramp then
        self:set(RemoteLight.FEATURE_RAMP_TIME, ramp)
    end
    self:set(0, value)
end

--- Gets the current brightness as a percentage, rounded.
-- @return number|nil Brightness percentage (0 to 100), nil until reported
function RemoteLight:getBrightness()
    local v = tonumber(self:getValue())
    return v and math.floor(v * 100 + 0.5)
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
    self:execute(self.METHODS.switchOn, time or 0, ramp)
    self:updateState(1)
end

--- Turns the light off.
-- @param time number Optional duration in milliseconds
-- @param ramp number Optional ramp time
function RemoteLight:switchOff(time, ramp)
    self:execute(self.METHODS.switchOff, time or 0, ramp)
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
    self:execute(self.METHODS.switch, time or 0, ramp)
end

--- Starts dimming operation (hold function).
-- @param ramp number Ramp time for dimming
function RemoteLight:dim(ramp)
    self:execute(self.METHODS.hold, ramp)
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
