--- Remote number class for AnalogOUT objects.
-- Provides control for analog output values with configurable ranges.
-- @module objects.RemoteNumber
-- @inherits RemoteObject

RemoteNumber = setmetatable({}, {__index = RemoteObject})
RemoteNumber.__index = RemoteNumber

--- Creates a new RemoteNumber instance.
-- @param clu CLU_REMOTE The CLU instance this number object belongs to
-- @param objId string The unique identifier of the number object
-- @param name string Optional name for the number object
-- @return RemoteNumber The new remote number instance
function RemoteNumber:new(clu, objId, name)
    local obj = RemoteObject.new(self, clu, objId, name)
    obj._type = "AnalogOUT"
    obj._allowedMethods = {"getValue", "setValue", "getMin", "getMax"}
    return obj
end

--- Gets the current numeric value (from local cache, updated by subscriptions).
-- @return number Current value
function RemoteNumber:getValue()
    return self._lastState
end

--- Sets the numeric value.
-- @param value number The value to set
function RemoteNumber:setValue(value)
    self:set(0, value)
end

--- Gets the minimum allowed value.
-- @return number Minimum value constraint
function RemoteNumber:getMin()
    return self:get(4)
end

--- Gets the maximum allowed value.
-- @return number Maximum value constraint
function RemoteNumber:getMax()
    return self:get(5)
end

return RemoteNumber
