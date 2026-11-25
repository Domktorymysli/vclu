--- Objects of a Grenton GATE MODBUS (FwType 00000001, HwType 00000012): the Modbus registers
-- the GATE reads on its RS485 bus (or over Modbus TCP), as Object Manager declares them in the
-- GATE's om.lua (MOD0001 = GATE:new(203, "MOD0001")). The OM import registers them under
-- "<CLU ID>.<object ID>" with a kind its settings give (register type, access): a sensor, a
-- binary sensor, a switch or a number. Their value follows the GATE when they are on the
-- Sync list; the GATE polls the device, vclu reads the GATE.
--
-- Numbers from Object Manager's device interfaces (object_modbus_*.xml), not checked on a
-- GATE MODBUS yet:
--   200 Modbus (v1, v2)  value 14 (set 14 SetValue writes it), ErrorCode 13
--   201 ModbusValue      value 20 (a string), IsValueValid 22, ErrorCode 23; execute 1 WriteValue(str)
--   203 ModbusRTU        value 22, IsValueValid 24, ErrorCode 25; execute 1 WriteValue(str)
--   205 ModbusClient     value 19, IsValueValid 21, ErrorCode 22; execute 1 WriteValue(str)
--
-- Guard (RemoteObject._setGuard, as RemoteGateAlarm's): only these features are read, since a
-- CLU asked for a feature it has not resets; no setting is written (the GATE's register,
-- line and scaling); only a switch or a number writes its value, by its class's way.
-- @module objects.RemoteGateModbus
-- @inherits RemoteObject

RemoteGateModbus = setmetatable({}, {__index = RemoteObject})
RemoteGateModbus.__index = RemoteGateModbus

-- Per Modbus class: the value feature, the features read, how the value is written
local CLASSES = {
    [200] = { value = 14, read = { [13] = "ErrorCode", [14] = "Value" }, write = "set" },
    [201] = { value = 20, read = { [20] = "Value", [22] = "IsValueValid", [23] = "ErrorCode" }, write = "execute" },
    [203] = { value = 22, read = { [22] = "Value", [24] = "IsValueValid", [25] = "ErrorCode" }, write = "execute" },
    [205] = { value = 19, read = { [19] = "Value", [21] = "IsValueValid", [22] = "ErrorCode" }, write = "execute" },
}
-- WriteValue of ModbusValue, ModbusRTU and ModbusClient
local WRITE_VALUE = 1

-- The guard's rules per object, out of reach of scripts
local objectRules = setmetatable({}, {__mode = "k"})

local function label(self)
    return tostring(self._type) .. " " .. tostring(self._objId)
end

-- Why a read, write or method of this object is refused; nil when it is not
local function refused(self, kind, index)
    local r = objectRules[self]
    if not r then return "not a GATE MODBUS object" end
    if rawget(self, "_objId") ~= r.objId or rawget(self, "_clu") ~= r.clu then
        return label(self) .. ": the object's ID or CLU changed after it was created"
    end
    -- the number goes into the script for the GATE (the same rule as RemoteGateAlarm's)
    if type(index) ~= "number" or index ~= index or index < 0 or index >= 10000
       or index ~= math.floor(index) then
        return label(self) .. ": " .. tostring(kind) .. " takes a whole number 0-9999, got " .. tostring(index)
    end
    if kind == "get" then
        if not r.read[index] then
            return label(self) .. ": feature " .. tostring(index)
                .. " is not one vclu reads from a Modbus object (a CLU resets on a feature it has not)"
        end
        return nil
    end
    if not r.writable then
        return label(self) .. ": a read-only Modbus register; vclu does not write it"
    end
    if kind == "set" and (r.write ~= "set" or index ~= r.value) then
        return label(self) .. ": feature " .. tostring(index)
            .. " is a setting of the GATE; vclu does not write it (the value is written by setValue)"
    end
    if kind == "execute" and (r.write ~= "execute" or index ~= WRITE_VALUE) then
        return label(self) .. ": method " .. tostring(index) .. " is not one vclu sends to it"
    end
    return nil
end

--- Why a read, write or method of this object is refused; nil when it is not.
-- @param kind string "get", "set" or "execute"
-- @param index number The feature or method number
-- @return string|nil
function RemoteGateModbus:_refused(kind, index)
    return refused(self, kind, index)
end

--- The value the GATE reported.
-- @api public
-- @return number|nil
function RemoteGateModbus:getValue()
    return self._lastState
end

--- The feature the Sync list follows: the class's value.
-- @return table
function RemoteGateModbus:subscribeFeatures()
    local r = objectRules[self]
    return r and { r.value } or {}
end

--- A reported value as a number (ModbusValue reports a string).
-- @param index number The feature
-- @param value any The value as the GATE reported it
-- @return any
function RemoteGateModbus:fromFeature(index, value)
    return tonumber(value) or value
end

-- Writes the value: SetValue (set) on Modbus v1/v2, WriteValue(str) on the others
local function write(self, value)
    local r = objectRules[self]
    if r and r.write == "set" then
        return RemoteObject.set(self, r.value, value)
    end
    return RemoteObject.execute(self, WRITE_VALUE, tostring(value))
end

-- Value changes as Grenton's events: on after off, off after on
local function edges(self, newState, onEvent, offEvent)
    local on = newState ~= nil and newState ~= 0
    local known = self._lastOn
    self._lastOn = on
    if known == false and on then
        self:emit(onEvent)
    elseif known == true and not on then
        self:emit(offEvent)
    end
end

local function class(name, typ, methods, writable)
    local c = setmetatable({}, {__index = RemoteGateModbus})
    c.__index = c
    c.new = function(self, clu, objId, objName, modbusClass)
        local rules = CLASSES[modbusClass]
        if not rules then
            error(name .. ":new(clu, id, name, class): class " .. tostring(modbusClass)
                .. " is not a Modbus object vclu reads (200, 201, 203, 205)", 2)
        end
        local obj = RemoteObject.new(self, clu, objId, objName)
        obj._type = typ
        obj._modbusClass = modbusClass
        obj._allowedMethods = methods
        local read = {}
        for k, v in pairs(rules.read) do read[k] = v end
        objectRules[obj] = { objId = objId, clu = clu, value = rules.value, read = read,
            write = rules.write, writable = writable }
        RemoteObject._setGuard(obj, refused)
        return obj
    end
    _G[name] = c
    return c
end

--- A register read only: a measurement (input register, holding register without write).
-- Events OnChange. Exposed as a read-only number (Home Assistant shows a sensor; no unit,
-- Object Manager has none).
RemoteGateModbusSensor = class("RemoteGateModbusSensor", "GATE_MODBUS_SENSOR", {"getValue"}, false)

--- Exposed read-only, whatever the options.
-- @param options table Expose options
function RemoteGateModbusSensor:expose(options)
    local opts = {}
    for k, v in pairs(options or {}) do opts[k] = v end
    opts.readonly = true
    return RemoteObject.expose(self, opts)
end

--- A coil, a discrete input or a bit read only: 0 off, anything else on.
-- Events OnChange, OnSwitchOn, OnSwitchOff. Exposed like a DIN (a binary sensor).
RemoteGateModbusBinarySensor = class("RemoteGateModbusBinarySensor", "GATE_MODBUS_BINARY_SENSOR",
    {"getValue"}, false)

function RemoteGateModbusBinarySensor:updateState(newState)
    RemoteObject.updateState(self, newState)
    edges(self, self._lastState, "OnSwitchOn", "OnSwitchOff")
end

--- Registers a callback for when the value turns on.
-- @api public
function RemoteGateModbusBinarySensor:onSwitchOn(callback) return self:onEvent("OnSwitchOn", callback) end

--- Registers a callback for when the value turns off.
-- @api public
function RemoteGateModbusBinarySensor:onSwitchOff(callback) return self:onEvent("OnSwitchOff", callback) end

--- A coil or a bit the GATE writes: 0 off, 1 on. Events OnChange, OnSwitchOn, OnSwitchOff.
-- A switch only by expose(): writing to a device vclu knows nothing of is the user's choice.
RemoteGateModbusSwitch = class("RemoteGateModbusSwitch", "GATE_MODBUS_SWITCH",
    {"getValue", "setValue", "switchOn", "switchOff", "toggle"}, true)
RemoteGateModbusSwitch._explicitOnly = true

--- Writes 1; the GATE reports the value.
-- @api public
function RemoteGateModbusSwitch:switchOn() write(self, 1) end

--- Writes 0.
-- @api public
function RemoteGateModbusSwitch:switchOff() write(self, 0) end

--- Switches off when on, else on.
-- @api public
function RemoteGateModbusSwitch:toggle()
    if self._lastState ~= nil and self._lastState ~= 0 then self:switchOff() else self:switchOn() end
end

--- 1 (or true) switches on, anything else off.
-- @api public
-- @param value number 0 or 1
function RemoteGateModbusSwitch:setValue(value)
    if value == 1 or value == true then self:switchOn() else self:switchOff() end
end

--- set(0, v) switches; other features are refused by the guard.
-- @param index number Feature number
-- @param value any The value
function RemoteGateModbusSwitch:set(index, value)
    if index == 0 then return self:setValue(value) end
    return RemoteObject.set(self, index, value)
end

RemoteGateModbusSwitch.updateState = RemoteGateModbusBinarySensor.updateState
RemoteGateModbusSwitch.onSwitchOn = RemoteGateModbusBinarySensor.onSwitchOn
RemoteGateModbusSwitch.onSwitchOff = RemoteGateModbusBinarySensor.onSwitchOff

--- A holding register the GATE writes (a mode, a setpoint). Events OnChange. A number only
-- by expose(), as the switch.
RemoteGateModbusNumber = class("RemoteGateModbusNumber", "GATE_MODBUS_NUMBER",
    {"getValue", "setValue"}, true)
RemoteGateModbusNumber._explicitOnly = true

--- Writes the value to the register; the GATE reports it back.
-- @api public
-- @param value number The value (scaled as the object's Divisor in Object Manager)
function RemoteGateModbusNumber:setValue(value)
    local v = tonumber(value)
    if not v or v ~= v or v == math.huge or v == -math.huge then
        error(label(self) .. ": setValue takes a number, got " .. tostring(value), 2)
    end
    write(self, v)
end

--- set(0, v) writes the value; other features are refused by the guard.
-- @param index number Feature number
-- @param value any The value
function RemoteGateModbusNumber:set(index, value)
    if index == 0 then return self:setValue(value) end
    return RemoteObject.set(self, index, value)
end

return RemoteGateModbus
