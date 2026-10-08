--- Remote sensor class for DIN (digital input) and AnalogIN objects.
-- Read-only interface for sensors and input devices.
-- @module objects.RemoteSensor
-- @inherits RemoteObject

RemoteSensor = setmetatable({}, {__index = RemoteObject})
RemoteSensor.__index = RemoteSensor

--- Creates a new RemoteSensor instance.
-- @param clu CLU_REMOTE The CLU instance this sensor belongs to
-- @param objId string The unique identifier of the sensor
-- @param name string Optional name for the sensor
-- @return RemoteSensor The new remote sensor instance
function RemoteSensor:new(clu, objId, name)
    local obj = RemoteObject.new(self, clu, objId, name)
    obj._type = "DIN"
    obj._allowedMethods = {"getValue"}
    return obj
end

--- Gets the current value of the sensor (from local cache, updated by subscriptions).
-- @return number Current sensor state
function RemoteSensor:getValue()
    return self._lastState
end

--- Registers a callback for when the sensor activates.
-- @param callback function Function called when sensor turns on
-- @return RemoteSensor Self for method chaining
function RemoteSensor:onSwitchOn(callback)
    return self:onEvent("OnSwitchOn", callback)
end

--- Registers a callback for when the sensor deactivates.
-- @param callback function Function called when sensor turns off
-- @return RemoteSensor Self for method chaining
function RemoteSensor:onSwitchOff(callback)
    return self:onEvent("OnSwitchOff", callback)
end

--- Registers a callback for click events (short press).
-- @param callback function Function called on button click
-- @return RemoteSensor Self for method chaining
function RemoteSensor:onClick(callback)
    return self:onEvent("OnClick", callback)
end

--- Registers a callback for long press events.
-- @param callback function Function called on button long press
-- @return RemoteSensor Self for method chaining
function RemoteSensor:onLongPress(callback)
    return self:onEvent("OnLongPress", callback)
end

--- Registers a callback for hold events (continuous press).
-- @param callback function Function called while button is held
-- @return RemoteSensor Self for method chaining
function RemoteSensor:onHold(callback)
    return self:onEvent("OnHold", callback)
end

--- Updates the sensor state and triggers appropriate events.
-- @param newState number The new state value
function RemoteSensor:updateState(newState)
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

return RemoteSensor
