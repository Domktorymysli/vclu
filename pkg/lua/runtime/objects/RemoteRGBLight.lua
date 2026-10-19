--- Remote RGB light class for LEDRGB objects.
-- Extends RemoteLight with RGB color control capabilities.
-- @module objects.RemoteRGBLight
-- @inherits RemoteLight

RemoteRGBLight = setmetatable({}, {__index = RemoteLight})
RemoteRGBLight.__index = RemoteRGBLight

-- LEDRGB (class 9, LED RGB and LED RGBW 2.0; Object Manager device-interfaces) numbers its
-- methods unlike DIMMER: execute 0 SetValue (Value is read-only), 7 HoldValue, 9 SwitchOn,
-- 10 SwitchOff, 11 Switch, 12 SetWhiteValue; feature 15 WhiteValue (RGBW only).
RemoteRGBLight.METHODS = { setValue = 0, hold = 7, switch = 11, switchOn = 9, switchOff = 10, setWhite = 12 }
-- Feature 6 RGB ("#rrggbb"), 15 WhiteValue 0..255; the OM import sets _hasWhite on an RGBW module
RemoteRGBLight.FEATURE_RGB = 6
RemoteRGBLight.FEATURE_WHITE = 15

--- Creates a new RemoteRGBLight instance.
-- @param clu CLU_REMOTE The CLU instance this RGB light belongs to
-- @param objId string The unique identifier of the RGB light
-- @param name string Optional name for the RGB light
-- @return RemoteRGBLight The new remote RGB light instance
function RemoteRGBLight:new(clu, objId, name)
    local obj = RemoteLight.new(self, clu, objId, name)
    obj._type = "LEDRGB"
    obj._allowedMethods = {"getValue", "setValue", "getBrightness", "setBrightness", "switchOn", "switchOff", "on", "off", "toggle", "getHue", "setHue", "getSaturation", "setSaturation", "getRGB", "setRGB", "getColor", "setColor", "getWhite", "setWhite"}
    return obj
end

--- Features to subscribe to: the value first, then the colour (and the white of an RGBW
-- module): the CLU reports the colour scaled by the value, so it is read with the value of
-- the same report.
-- @return table Feature indexes
function RemoteRGBLight:subscribeFeatures()
    local features = { 0, RemoteRGBLight.FEATURE_RGB }
    if self._hasWhite then features[#features + 1] = RemoteRGBLight.FEATURE_WHITE end
    return features
end

--- Turns a reported feature into the object's value; the colour and the white are kept aside.
-- @param index number Feature index
-- @param value any Reported value
-- @return any The value to store, or nil for the colour and the white
function RemoteRGBLight:fromFeature(index, value)
    if index == RemoteRGBLight.FEATURE_RGB then
        self:_colorFromReport(RemoteRGBLight.parseHex(value))
        return nil
    elseif index == RemoteRGBLight.FEATURE_WHITE then
        self:_setWhite(tonumber(value))
        return nil
    end
    return value
end

-- The CLU reports the colour it outputs, scaled by the value (a red at 0.49 is #7c0000,
-- off is #000000; LED RGBW DIN fv01, 2026-10-19). The light's colour is that divided by the
-- value (reported just before it); off or black keeps the last one, and a report that
-- matches the colour at this value (rounding) keeps it exactly.
function RemoteRGBLight:_colorFromReport(r, g, b)
    if not r or (r == 0 and g == 0 and b == 0) then return end
    local v = tonumber(self._lastState) or 1
    if v <= 0 then return end
    local c = self._color
    if c and math.abs(c.r * v - r) <= 2 and math.abs(c.g * v - g) <= 2 and math.abs(c.b * v - b) <= 2 then
        return
    end
    local function up(x) return math.min(255, math.floor(x / v + 0.5)) end
    self:_setColor(up(r), up(g), up(b))
end

--- "#rrggbb" to its components; the # is optional, and so are the quotes a CLU puts
-- around a string in a report.
-- @param hex string
-- @return number, number, number r, g, b (0..255), or nil
function RemoteRGBLight.parseHex(hex)
    local r, g, b = tostring(hex):match('^"?#?(%x%x)(%x%x)(%x%x)"?$')
    if not r then return nil end
    return tonumber(r, 16), tonumber(g, 16), tonumber(b, 16)
end

-- The colour and the white aside from the value; integrations (Home Assistant) listen
-- for OnColorChange
function RemoteRGBLight:_setColor(r, g, b)
    if not r then return end
    local c = self._color
    if c and c.r == r and c.g == g and c.b == b then return end
    self._color = { r = r, g = g, b = b }
    self:emit("OnColorChange")
end

function RemoteRGBLight:_setWhite(w)
    if w == nil or w == self._white then return end
    self._white = w
    self:emit("OnColorChange")
end

--- Gets the last reported or set colour.
-- @return number, number, number r, g, b (0..255), or nil before the first report
function RemoteRGBLight:getColor()
    local c = self._color
    if not c then return nil end
    return c.r, c.g, c.b
end

--- Sets the brightness value (0.0 to 1.0): SetValue, with an optional ramp.
-- @param value number Brightness value
-- @param ramp number Optional ramp time in milliseconds
function RemoteRGBLight:setValue(value, ramp)
    self:execute(self.METHODS.setValue, value, ramp)
    self:updateState(value)
end

--- Gets the white channel of an RGBW module: the last reported or set value, else from the CLU.
-- @return number White value (0 to 255)
function RemoteRGBLight:getWhite()
    return self._white or self:get(RemoteRGBLight.FEATURE_WHITE)
end

--- Sets the white channel of an RGBW module.
-- @param value number White value (0 to 255)
-- @param ramp number Optional ramp time in milliseconds
function RemoteRGBLight:setWhite(value, ramp)
    value = math.floor(math.max(0, math.min(255, value)) + 0.5)
    self:execute(self.METHODS.setWhite, value, ramp)
    self:_setWhite(value)
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
    self:execute(6, hex, ramp)
    self:_setColor(RemoteRGBLight.parseHex(hex))
end

--- Sets the RGB color using individual components.
-- @param r number Red component (0 to 255)
-- @param g number Green component (0 to 255)
-- @param b number Blue component (0 to 255)
-- @param ramp number Optional ramp time
function RemoteRGBLight:setColor(r, g, b, ramp)
    -- The CLU takes the colour as its output and makes the brightest channel / 255 the
    -- value, so the colour goes out scaled by the brightness now (off: full, which
    -- switches it on); the light's colour is kept with its brightest channel at 255.
    r, g, b = tonumber(r) or 0, tonumber(g) or 0, tonumber(b) or 0
    local m = math.max(r, g, b)
    if m <= 0 then return end
    local v = tonumber(self._lastState)
    local k = (v and v > 0) and v or 1
    local function out(x, f) return math.max(0, math.min(255, math.floor(x * f + 0.5))) end
    local hex = string.format("#%02x%02x%02x", out(r, 255 * k / m), out(g, 255 * k / m), out(b, 255 * k / m))
    self:execute(6, hex, ramp)
    self:_setColor(out(r, 255 / m), out(g, 255 / m), out(b, 255 / m))
end

return RemoteRGBLight
