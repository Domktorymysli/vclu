--- Remote RGB light class for LEDRGB objects.
-- Extends RemoteLight with RGB color control capabilities.
-- @module objects.RemoteRGBLight
-- @inherits RemoteLight

RemoteRGBLight = setmetatable({}, {__index = RemoteLight})
RemoteRGBLight.__index = RemoteRGBLight

--- Creates a new RemoteRGBLight instance.
-- @param clu CLU_REMOTE The CLU instance this RGB light belongs to
-- @param objId string The unique identifier of the RGB light
-- @param name string Optional name for the RGB light
-- @return RemoteRGBLight The new remote RGB light instance
function RemoteRGBLight:new(clu, objId, name)
    local obj = RemoteLight.new(self, clu, objId, name)
    obj._type = "LEDRGB"
    obj._allowedMethods = {"getValue", "setValue", "getBrightness", "setBrightness", "switchOn", "switchOff", "on", "off", "toggle", "getHue", "setHue", "getSaturation", "setSaturation", "getRGB", "setRGB", "setColor"}
    return obj
end

--- Gets the current hue value.
-- @return number Current hue (0 to 360 degrees)
function RemoteRGBLight:getHue()
    return self:get(1)
end

--- Sets the hue value.
-- @param hue number Hue value (0 to 360 degrees)
-- @param ramp number Optional ramp time
function RemoteRGBLight:setHue(hue, ramp)
    self:execute(1, hue, ramp)
end

--- Gets the current saturation value.
-- @return number Current saturation (0.0 to 1.0)
function RemoteRGBLight:getSaturation()
    return self:get(2)
end

--- Sets the saturation value.
-- @param sat number Saturation value (0.0 to 1.0)
-- @param ramp number Optional ramp time
function RemoteRGBLight:setSaturation(sat, ramp)
    self:execute(2, sat, ramp)
end

--- Gets the current RGB color as hex string.
-- @return string Current color in hex format
function RemoteRGBLight:getRGB()
    return self:get(6)
end

--- Sets the RGB color using hex string.
-- @param hex string Color in hex format (e.g., "#FF5500")
-- @param ramp number Optional ramp time
function RemoteRGBLight:setRGB(hex, ramp)
    self:execute(6, '"' .. hex .. '"', ramp)
end

--- Sets the RGB color using individual components.
-- @param r number Red component (0 to 255)
-- @param g number Green component (0 to 255)
-- @param b number Blue component (0 to 255)
-- @param ramp number Optional ramp time
function RemoteRGBLight:setColor(r, g, b, ramp)
    local hex = string.format("#%02x%02x%02x", r, g, b)
    self:setRGB(hex, ramp)
end

return RemoteRGBLight
