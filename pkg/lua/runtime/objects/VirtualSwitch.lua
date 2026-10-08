--- Virtual switch class for binary on/off devices.
-- Self-contained state management, no CLU/UDP dependency.
-- _type = "DOUT" — must stay in sync with RemoteSwitch.
-- Public API must match RemoteSwitch (see objects/RemoteSwitch.lua).
-- @module objects.VirtualSwitch
-- @inherits VirtualObject

VirtualSwitch = setmetatable({}, {__index = VirtualObject})
VirtualSwitch.__index = VirtualSwitch

--- Creates a new VirtualSwitch instance.
-- @api public
-- @param name string Optional name for the switch
-- @param opts table Optional: initial (default 0), persist (true or a key: keep the value across reloads and restarts)
-- @return VirtualSwitch The new virtual switch instance
function VirtualSwitch:new(name, opts)
    local obj = VirtualObject.new(self, name)
    obj._type = "DOUT"
    obj:_setup(opts, 0)
    obj._allowedMethods = {"getValue", "setValue", "switchOn", "switchOff", "on", "off", "toggle"}
    return obj
end

--- Gets the current value of the switch.
-- @api public
-- @return number Current state (0 = off, 1 = on)
function VirtualSwitch:getValue()
    return self._state
end

--- Sets the value of the switch.
-- @api public
-- @param value number The state to set (0 = off, 1 = on)
function VirtualSwitch:setValue(value)
    self:updateState(value)
end

--- Turns the switch on.
-- @api public
function VirtualSwitch:switchOn()
    self:updateState(1)
end

--- Turns the switch off.
-- @api public
function VirtualSwitch:switchOff()
    self:updateState(0)
end

--- Shorthand to turn the switch on.
-- @api public
function VirtualSwitch:on()
    self:switchOn()
end

--- Shorthand to turn the switch off.
-- @api public
function VirtualSwitch:off()
    self:switchOff()
end

--- Toggles the switch state.
-- @api public
function VirtualSwitch:toggle()
    self:updateState(self._state == 1 and 0 or 1)
end

--- Checks if the switch is on.
-- @api public
-- @return boolean True if state == 1
function VirtualSwitch:isOn()
    return self._state == 1
end

--- Updates the switch state and triggers appropriate events.
-- @api public
-- @param newValue number The new state value (0 or 1)
function VirtualSwitch:updateState(newValue)
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

--- Registers a callback for when the switch turns on.
-- @api public
-- @param callback function Function called when switch turns on
-- @return VirtualSwitch Self for method chaining
function VirtualSwitch:onSwitchOn(callback)
    return self:onEvent("OnSwitchOn", callback)
end

--- Registers a callback for when the switch turns off.
-- @api public
-- @param callback function Function called when switch turns off
-- @return VirtualSwitch Self for method chaining
function VirtualSwitch:onSwitchOff(callback)
    return self:onEvent("OnSwitchOff", callback)
end

return VirtualSwitch
