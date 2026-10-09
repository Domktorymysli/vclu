---
--- SatelZone - alarm zone as a read-only binary sensor.
--- Inherits VirtualSensor (_type = "DIN", expose type "sensor").
--- State is pushed from Satel.pollEvents(); getValue() never blocks.
-- @module SatelZone
-- @inherits VirtualSensor
---

local SatelZone = setmetatable({}, {__index = VirtualSensor})
SatelZone.__index = SatelZone

--- Creates a zone object.
-- @param number number 1-based zone number on the panel
-- @param name string Optional custom name (otherwise panel name is used)
function SatelZone:new(number, name)
    local obj = VirtualSensor.new(self, name or ("Satel Zone " .. number))
    obj._satel = true  -- HomeKit shows Satel objects read-only
    obj.number = number
    obj._customName = name ~= nil
    obj._tamper = false
    obj._alarm = false
    obj._bypass = false
    obj._allowedMethods = {"getValue", "isActive", "isTamper", "isAlarm", "isBypassed"}
    return obj
end

--- Applies a zone state table from the panel ({ violated, tamper, alarm, bypass, name }).
function SatelZone:_applyState(s)
    self._tamper = s.tamper or false
    self._bypass = s.bypass or false
    if s.name and s.name ~= "" and not self._customName then self._name = s.name end

    local wasAlarm = self._alarm
    self._alarm = s.alarm or false
    self:updateState(s.violated and 1 or 0)
    if self._alarm and not wasAlarm then
        self:emit("OnAlarm")
    end
end

--- Whether the zone reports tamper.
function SatelZone:isTamper() return self._tamper end

--- Whether the zone is in alarm.
function SatelZone:isAlarm() return self._alarm end

--- Whether the zone is bypassed.
function SatelZone:isBypassed() return self._bypass end

--- Registers a callback fired when the zone enters alarm state.
function SatelZone:onAlarm(callback)
    return self:onEvent("OnAlarm", callback)
end

--- Descriptor for the MCP sandbox, which rebuilds the zone without a panel.
-- @return table {kind, state, config}
function SatelZone:describe()
    return { kind = "satel_zone", state = self._state, config = { number = self.number, name = self._name } }
end

--- Sandbox factory for describe() output.
-- @param d table Descriptor {state, config = {number, name}}
-- @return SatelZone
function SatelZone.fromDescriptor(d)
    local cfg = d.config or {}
    if type(cfg.number) ~= "number" then error("satel_zone: config.number required") end
    local zone = SatelZone:new(cfg.number, cfg.name)
    zone._state = d.state or 0
    return zone
end

return SatelZone
