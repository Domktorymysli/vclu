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

--- No expose type yet: a switch would write feature 0, the thermostat's sensor binding
-- (Source), so the registry does not expose it and expose() refuses it.
-- @return nil
function RemoteThermostat:_getExposeType()
    return nil
end

-- Features of each object version, in report order: the settings first, the temperature last
local T = RemoteThermostat
RemoteThermostat.FEATURES = {
    [1] = {T.FEATURE_STATE, T.FEATURE_MODE, T.FEATURE_POINT, T.FEATURE_TARGET}, -- v1: no CurrentTemp
    [2] = {T.FEATURE_STATE, T.FEATURE_MODE, T.FEATURE_POINT, T.FEATURE_TARGET, T.FEATURE_CURRENT},
}

-- Object version and how values are followed, by CLU firmware; for each field the first
-- matching row wins. om: FwType/FwVersion in om.lua, omType: FwType alone; major: the
-- firmware the CLU reports for get(17) (unknown matches no major row).
-- Versions from Object Manager 1.7 device-interfaces (clu_*_ft…_fv….xml, object Thermostat).
-- Subscribed only where known to be safe; firmware 4.x (CLU v1.0) is not verified (#67).
RemoteThermostat.FIRMWARE = {
    {om = "3/197", version = 1},           -- clu_ft00000003_fv197
    {om = "3/1f5", version = 1},           -- clu_ZWAVE_ft00000003_fv1f5
    {omType = "4", follow = "poll"},       -- CLU v1.0, clu_ZWAVE_ft00000004_* (04.08.05)
    {major = 5, follow = "subscribe"},     -- 5.x reported by the CLU (e.g. 05.15.12)
    {version = 2, follow = "poll"},        -- all others: 4.x, unknown, a major not listed
}

--- The object version and how RemoteSubscription follows its values on its CLU.
-- Only a row of FIRMWARE says "subscribe"; anything else, a CLU that has not reported
-- its firmware included, is polled.
-- @return number version, string "subscribe" or "poll"
function RemoteThermostat:_plan()
    local clu = self._clu or {}
    local om = clu._omFirmwareKey and clu:_omFirmwareKey()
    local omType = om and om:match("^(%w+)/")
    local major = clu._firmwareMajor and clu:_firmwareMajor()
    local version, follow
    for _, row in ipairs(RemoteThermostat.FIRMWARE) do
        if (row.om == nil or row.om == om) and (row.omType == nil or row.omType == omType)
           and (row.major == nil or row.major == major) then
            version = version or row.version
            follow = follow or row.follow
        end
    end
    -- proxies imported before #67 mark v1 with _version
    return self._version or version, follow
end

--- Features to subscribe to (or poll), in report order, for the object version on its CLU.
-- @return table Feature indexes
function RemoteThermostat:subscribeFeatures()
    local version = self:_plan()
    local f = {}
    for i, index in ipairs(RemoteThermostat.FEATURES[version] or RemoteThermostat.FEATURES[2]) do f[i] = index end
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
