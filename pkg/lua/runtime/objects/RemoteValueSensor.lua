--- Remote read-only numeric sensor for Grenton sensor objects whose value sits at feature 0.
-- Used for panel temperature/light sensors and humidity sensors. The Grenton type is passed
-- in so it maps to the right expose type (temperature / illuminance / humidity) via TypeMap.
-- @module objects.RemoteValueSensor
-- @inherits RemoteObject

RemoteValueSensor = setmetatable({}, {__index = RemoteObject})
RemoteValueSensor.__index = RemoteValueSensor

--- Creates a new RemoteValueSensor instance.
-- @param clu CLU_REMOTE The CLU instance this sensor belongs to
-- @param objId string The unique identifier of the sensor
-- @param name string Optional name for the sensor
-- @param sensorType string Grenton type (e.g. "PANELSENSTEMP", "PANELSENSLIGHT", "HUMIDITY_SENSOR")
-- @return RemoteValueSensor The new remote sensor instance
function RemoteValueSensor:new(clu, objId, name, sensorType)
    local obj = RemoteObject.new(self, clu, objId, name)
    obj._type = sensorType or "ONE_WIRE"  -- mapped to temperature/illuminance/humidity via TypeMap
    obj._allowedMethods = {"getValue"}
    return obj
end

--- Gets the current measured value (from local cache, updated by subscriptions).
-- @return number Current sensor value
function RemoteValueSensor:getValue()
    return self._lastState
end

return RemoteValueSensor
