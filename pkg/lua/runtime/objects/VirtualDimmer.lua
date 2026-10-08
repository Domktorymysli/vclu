--- Virtual dimmer class for dimmable lights.
-- State is brightness 0.0-1.0. getBrightness/setBrightness use 0-100%.
-- _type = "DIMMER" — must stay in sync with RemoteLight.
-- Public API must match RemoteLight (see objects/RemoteLight.lua).
-- @module objects.VirtualDimmer
-- @inherits VirtualObject

VirtualDimmer = setmetatable({}, {__index = VirtualObject})
VirtualDimmer.__index = VirtualDimmer

--- Creates a new VirtualDimmer instance.
-- @api public
-- @param name string Optional name for the dimmer
-- @param opts table Optional: initial (default 0), persist (true or a key: keep the value across reloads and restarts)
-- @return VirtualDimmer The new virtual dimmer instance
function VirtualDimmer:new(name, opts)
    local obj = VirtualObject.new(self, name)
    obj._type = "DIMMER"
    obj:_setup(opts, 0)
    obj._lastBrightness = 1.0
    obj._allowedMethods = {"getValue", "setValue", "getBrightness", "setBrightness", "switchOn", "switchOff", "on", "off", "toggle"}
    return obj
end

--- Gets the current brightness value (0.0 to 1.0).
-- @api public
-- @return number Current brightness
function VirtualDimmer:getValue()
    return self._state
end

--- Sets the brightness value (0.0 to 1.0).
-- @api public
-- @param value number Brightness value
function VirtualDimmer:setValue(value)
    self:updateState(value)
end

--- Gets the current brightness as a percentage.
-- @api public
-- @return number Brightness percentage (0 to 100)
function VirtualDimmer:getBrightness()
    return math.floor((self._state or 0) * 100)
end

--- Sets the brightness as a percentage.
-- @api public
-- @param percent number Brightness percentage (0 to 100)
function VirtualDimmer:setBrightness(percent)
    self:updateState(percent / 100)
end

--- Turns the dimmer on, restoring last brightness.
-- @api public
function VirtualDimmer:switchOn()
    self:updateState(self._lastBrightness or 1.0)
end

--- Turns the dimmer off, saving current brightness for restore.
-- @api public
function VirtualDimmer:switchOff()
    if self._state > 0 then
        self._lastBrightness = self._state
    end
    self:updateState(0)
end

--- Shorthand to turn the dimmer on.
-- @api public
function VirtualDimmer:on()
    self:switchOn()
end

--- Shorthand to turn the dimmer off.
-- @api public
function VirtualDimmer:off()
    self:switchOff()
end

--- Toggles the dimmer state.
-- @api public
function VirtualDimmer:toggle()
    if self._state > 0 then
        self:switchOff()
    else
        self:switchOn()
    end
end

--- Checks if the dimmer is on (brightness > 0).
-- @api public
-- @return boolean True if brightness > 0
function VirtualDimmer:isOn()
    return self._state > 0
end

--- Updates the dimmer state and triggers appropriate events.
-- @api public
-- @param newValue number The new brightness value (0.0 to 1.0)
function VirtualDimmer:updateState(newValue)
    local old = self._state
    VirtualObject.updateState(self, newValue)
    if old ~= newValue then
        if old == 0 and newValue > 0 then
            self:emit("OnSwitchOn")
        elseif old > 0 and newValue == 0 then
            self:emit("OnSwitchOff")
        end
    end
end

--- Registers a callback for when the dimmer turns on.
-- @api public
-- @param callback function Function called when dimmer turns on
-- @return VirtualDimmer Self for method chaining
function VirtualDimmer:onSwitchOn(callback)
    return self:onEvent("OnSwitchOn", callback)
end

--- Registers a callback for when the dimmer turns off.
-- @api public
-- @param callback function Function called when dimmer turns off
-- @return VirtualDimmer Self for method chaining
function VirtualDimmer:onSwitchOff(callback)
    return self:onEvent("OnSwitchOff", callback)
end

return VirtualDimmer
