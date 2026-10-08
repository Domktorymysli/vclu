--- Remote switch class for DOUT (digital output) objects.
-- Provides control for binary on/off devices like relays and switches.
-- @module objects.RemoteSwitch
-- @inherits RemoteObject

RemoteSwitch = setmetatable({}, {__index = RemoteObject})
RemoteSwitch.__index = RemoteSwitch

--- Creates a new RemoteSwitch instance.
-- @param clu CLU_REMOTE The CLU instance this switch belongs to
-- @param objId string The unique identifier of the switch
-- @param name string Optional name for the switch
-- @return RemoteSwitch The new remote switch instance
function RemoteSwitch:new(clu, objId, name)
    local obj = RemoteObject.new(self, clu, objId, name)
    obj._type = "DOUT"
    obj._allowedMethods = {"getValue", "setValue", "switchOn", "switchOff", "on", "off", "toggle"}
    return obj
end

--- Gets the current value of the switch (from local cache, updated by subscriptions).
-- @return number Current state (0 = off, 1 = on)
function RemoteSwitch:getValue()
    return self._lastState
end

--- Sets the value of the switch.
-- @param value number The state to set (0 = off, 1 = on)
function RemoteSwitch:setValue(value)
    self:set(0, value)
end

--- Toggles the switch state.
-- @param time number Optional duration in milliseconds
function RemoteSwitch:switch(time)
    self:execute(0, time or 0)
    if self._lastState == 0 then self:updateState(1) else self:updateState(0) end
end

--- Turns the switch on.
-- @param time number Optional duration in milliseconds
function RemoteSwitch:switchOn(time)
    self:execute(1, time or 0)
    self:updateState(1)
    -- with a time the CLU switches it off by itself; that is still our doing
    if time and time > 0 then StateChange.expectLater(self, 0, time) else StateChange.forgetExpected(self) end
end

--- Turns the switch off.
-- @param time number Optional duration in milliseconds
function RemoteSwitch:switchOff(time)
    self:execute(2, time or 0)
    self:updateState(0)
    -- whether the CLU switches back on after time is unverified: no expectation
    StateChange.forgetExpected(self)
end

--- Shorthand to turn the switch on immediately.
function RemoteSwitch:on()
    self:switchOn(0)
end

--- Shorthand to turn the switch off immediately.
function RemoteSwitch:off()
    self:switchOff(0)
end

--- Shorthand to toggle the switch immediately.
function RemoteSwitch:toggle()
    self:switch(0)
end

--- Registers a callback for when the switch turns on.
-- @param callback function Function called when switch turns on
-- @return RemoteSwitch Self for method chaining
function RemoteSwitch:onSwitchOn(callback)
    return self:onEvent("OnSwitchOn", callback)
end

--- Registers a callback for when the switch turns off.
-- @param callback function Function called when switch turns off
-- @return RemoteSwitch Self for method chaining
function RemoteSwitch:onSwitchOff(callback)
    return self:onEvent("OnSwitchOff", callback)
end

--- Updates the switch state and triggers appropriate events.
-- @param newState number The new state value (0 or 1)
function RemoteSwitch:updateState(newState)
    local oldState = self._lastState
    RemoteObject.updateState(self, newState)
    if oldState ~= nil then
        if oldState == 0 and newState == 1 then
            self:emit("OnSwitchOn")
        elseif oldState == 1 and newState == 0 then
            self:emit("OnSwitchOff")
        end
    end
end

return RemoteSwitch
