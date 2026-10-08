---
--- SatelThermometer - zone temperature sensor (ATD-100).
--- Inherits VirtualObject with _type = "ONE_WIRE" (expose type "temperature").
--- Temperatures are polled by Go every tempPoll interval and pushed here.
-- @module SatelThermometer
-- @inherits VirtualObject
---

local SatelThermometer = setmetatable({}, {__index = VirtualObject})
SatelThermometer.__index = SatelThermometer

--- Creates a thermometer object.
-- @param number number 1-based zone number of the temperature sensor
-- @param name string Optional custom name (otherwise panel name is used)
function SatelThermometer:new(number, name)
    local obj = VirtualObject.new(self, name or ("Satel Temp " .. number))
    obj._type = "ONE_WIRE"
    obj._state = nil
    obj.number = number
    obj._customName = name ~= nil
    obj._allowedMethods = {"getValue"}
    return obj
end

--- Current temperature in °C (nil until the first reading).
function SatelThermometer:getValue()
    return self._state
end

return SatelThermometer
