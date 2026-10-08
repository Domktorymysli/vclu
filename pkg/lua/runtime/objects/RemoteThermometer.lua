--- Remote thermometer class for temperature sensors (ONE_WIRE, peripheral thermometers).
-- Read-only interface that reports a temperature value.
-- @module objects.RemoteThermometer
-- @inherits RemoteObject

RemoteThermometer = setmetatable({}, {__index = RemoteObject})
RemoteThermometer.__index = RemoteThermometer

--- Creates a new RemoteThermometer instance.
-- @param clu CLU_REMOTE The CLU instance this thermometer belongs to
-- @param objId string The unique identifier of the thermometer
-- @param name string Optional name for the thermometer
-- @return RemoteThermometer The new remote thermometer instance
function RemoteThermometer:new(clu, objId, name)
    local obj = RemoteObject.new(self, clu, objId, name)
    obj._type = "ONE_WIRE"  -- maps to "temperature" via TypeMap
    obj._allowedMethods = {"getValue"}
    return obj
end

--- Gets the current temperature (from local cache, updated by subscriptions).
-- @return number Current temperature value
function RemoteThermometer:getValue()
    return self._lastState
end

return RemoteThermometer
