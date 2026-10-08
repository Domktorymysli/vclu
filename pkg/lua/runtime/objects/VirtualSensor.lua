--- Virtual sensor class for read-only binary inputs.
-- _type = "DIN" — must stay in sync with RemoteSensor.
-- Public API must match RemoteSensor (see objects/RemoteSensor.lua).
-- @module objects.VirtualSensor
-- @inherits VirtualObject

VirtualSensor = setmetatable({}, {__index = VirtualObject})
VirtualSensor.__index = VirtualSensor

--- Creates a new VirtualSensor instance.
-- @api public
-- @param name string Optional name for the sensor
-- @param opts table Optional: initial (default 0), persist (true or a key: keep the value across reloads and restarts)
-- @return VirtualSensor The new virtual sensor instance
function VirtualSensor:new(name, opts)
    local obj = VirtualObject.new(self, name)
    obj._type = "DIN"
    obj:_setup(opts, 0)
    obj._allowedMethods = {"getValue"}
    return obj
end

--- Gets the current value of the sensor.
-- @api public
-- @return number Current state (0 = inactive, 1 = active)
function VirtualSensor:getValue()
    return self._state
end

--- Checks if the sensor is active.
-- @api public
-- @return boolean True if state == 1
function VirtualSensor:isActive()
    return self._state == 1
end

--- Updates the sensor state and triggers appropriate events.
-- @api public
-- @param newValue number The new state value (0 or 1)
function VirtualSensor:updateState(newValue)
    local old = self._state
    VirtualObject.updateState(self, newValue)
    if old ~= newValue then
        if newValue == 1 then
            self:emit("OnSwitchOn")
        elseif newValue == 0 then
            self:emit("OnSwitchOff")
        end
    end
end

--- Registers a callback for when the sensor activates.
-- @api public
-- @param callback function Function called when sensor turns on
-- @return VirtualSensor Self for method chaining
function VirtualSensor:onSwitchOn(callback)
    return self:onEvent("OnSwitchOn", callback)
end

--- Registers a callback for when the sensor deactivates.
-- @api public
-- @param callback function Function called when sensor turns off
-- @return VirtualSensor Self for method chaining
function VirtualSensor:onSwitchOff(callback)
    return self:onEvent("OnSwitchOff", callback)
end

return VirtualSensor
