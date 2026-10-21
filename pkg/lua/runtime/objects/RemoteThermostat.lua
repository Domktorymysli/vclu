--- Remote thermostat class for a CLU's Thermostat objects (class 18, THE....).
-- The value is the temperature at the thermostat's sensor; the target, the set point,
-- the state and the mode are kept aside. Indices from Object Manager's device-interfaces
-- (object Thermostat v2): features 3 PointValue (set by hand), 6 State (1 active),
-- 8 Mode (0 PointValue, 1 holiday, 2 auto, 3 heat-up), 12 TargetTemp (in effect, with the
-- mode), 14 CurrentTemp; method set 3 SetPointValue.
-- @module objects.RemoteThermostat
-- @inherits RemoteObject

RemoteThermostat = setmetatable({}, {__index = RemoteObject})
RemoteThermostat.__index = RemoteThermostat

RemoteThermostat.FEATURE_POINT = 3
RemoteThermostat.FEATURE_STATE = 6
RemoteThermostat.FEATURE_MODE = 8
RemoteThermostat.FEATURE_TARGET = 12
RemoteThermostat.FEATURE_CURRENT = 14

-- Events besides OnChange that change what the object shows (a panel redraws on them)
RemoteThermostat.CHANGE_EVENTS = {"OnSettingsChange"}

local ASIDE = {
    [RemoteThermostat.FEATURE_POINT] = "_point",
    [RemoteThermostat.FEATURE_STATE] = "_state",
    [RemoteThermostat.FEATURE_MODE] = "_mode",
    [RemoteThermostat.FEATURE_TARGET] = "_target",
}

--- Creates a new RemoteThermostat instance.
-- @param clu CLU_REMOTE The CLU the thermostat runs on
-- @param objId string The thermostat's id (e.g. "THE0855")
-- @param name string Optional name
-- @return RemoteThermostat The new remote thermostat instance
function RemoteThermostat:new(clu, objId, name)
    local obj = RemoteObject.new(self, clu, objId, name)
    obj._type = "THERMOSTAT"
    obj._allowedMethods = {"getValue", "getTarget", "getSetpoint", "setSetpoint", "getState", "getMode"}
    return obj
end

--- Features to subscribe to, in report order: the settings first, the temperature last.
-- Thermostat v1 (CLU firmware 197 and 1f5, the OM import sets _version) has no CurrentTemp.
-- @return table Feature indexes
function RemoteThermostat:subscribeFeatures()
    local f = {RemoteThermostat.FEATURE_STATE, RemoteThermostat.FEATURE_MODE, RemoteThermostat.FEATURE_POINT,
        RemoteThermostat.FEATURE_TARGET}
    if self._version ~= 1 then f[#f + 1] = RemoteThermostat.FEATURE_CURRENT end
    return f
end

--- Turns a reported feature value into the object's value.
-- @param index number Feature index
-- @param value any Reported value
-- @return any The temperature, or nil for a setting (kept aside)
function RemoteThermostat:fromFeature(index, value)
    local field = ASIDE[index]
    if not field then return tonumber(value) end
    self:_setAside(field, tonumber(value))
    return nil
end

function RemoteThermostat:_setAside(field, value)
    local old = self[field]
    self[field] = value
    if value ~= old then self:emit("OnSettingsChange", field, value, old) end
end

--- Gets the temperature at the thermostat's sensor (from local cache).
-- @return number|nil Current temperature in °C, nil until reported
function RemoteThermostat:getValue()
    return self._lastState
end

--- Gets the target temperature in effect (the set point, the holiday value or the schedule's).
-- @return number|nil Target temperature in °C, nil until reported
function RemoteThermostat:getTarget()
    return self._target
end

--- Gets the temperature set by hand (PointValue).
-- @return number|nil Set point in °C, nil until reported
function RemoteThermostat:getSetpoint()
    return self._point
end

--- Sets the temperature by hand (SetPointValue); the target follows in manual mode.
-- @param value number Set point in °C
function RemoteThermostat:setSetpoint(value)
    value = tonumber(value)
    if not value then return end
    self:set(RemoteThermostat.FEATURE_POINT, value)
    self:_setAside("_point", value)
end

--- Gets the state: 1 active, 0 stopped.
-- @return number|nil State, nil until reported
function RemoteThermostat:getState()
    return self._state
end

--- Gets the mode: 0 set point, 1 holiday, 2 auto (schedule), 3 heat-up.
-- @return number|nil Mode, nil until reported
function RemoteThermostat:getMode()
    return self._mode
end

return RemoteThermostat
