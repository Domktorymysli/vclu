--- Remote LED channel class for LED_CHANNEL objects.
-- One channel (R, G, B or W) of an LED RGBW 2.0 module, driven as a dimmer.
-- @module objects.RemoteLedChannel
-- @inherits RemoteLight

RemoteLedChannel = setmetatable({}, {__index = RemoteLight})
RemoteLedChannel.__index = RemoteLedChannel

-- LED_CHANNEL (class 49, Object Manager device-interfaces): execute 0 SetValue(0..255, ramp),
-- 1 SwitchOn, 2 SwitchOff, 3 Switch, 4 HoldValue; feature 0 Value is 0..255 and read-only.
RemoteLedChannel.METHODS = { setValue = 0, switchOn = 1, switchOff = 2, switch = 3, hold = 4 }
RemoteLedChannel.MAX = 255

--- Creates a new RemoteLedChannel instance.
-- @param clu CLU_REMOTE The CLU instance this channel belongs to
-- @param objId string The unique identifier of the channel
-- @param name string Optional name for the channel
-- @return RemoteLedChannel The new remote LED channel instance
function RemoteLedChannel:new(clu, objId, name)
    local obj = RemoteLight.new(self, clu, objId, name)
    obj._type = "LED_CHANNEL"
    return obj
end

--- A reported Value (0..255) on vCLU's dimmer scale (0..1).
function RemoteLedChannel:fromFeature(index, value)
    local v = tonumber(value)
    if index ~= 0 or not v then return value end
    return v / RemoteLedChannel.MAX
end

--- Sets the brightness value (0.0 to 1.0): SetValue on 0..255, with an optional ramp.
-- @param value number Brightness value
-- @param ramp number Optional ramp time in milliseconds
function RemoteLedChannel:setValue(value, ramp)
    value = math.max(0, math.min(1, tonumber(value) or 0))
    self:execute(self.METHODS.setValue, math.floor(value * RemoteLedChannel.MAX + 0.5), ramp)
    self:updateState(value)
end
