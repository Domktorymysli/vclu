--- Objects of a Grenton GATE Alarm (FwType 00000002, HwType 00000012): the Satel or
-- Jablotron panel the GATE is connected to, as Object Manager declares them in the GATE's
-- om.lua (SAT0001 = GATE:new(100, "SAT0001")). The OM import registers them under
-- "<CLU ID>.<object ID>"; their value (feature 0) follows the GATE when they are on the
-- Sync list, and their events are told from the value.
--
-- Names: Grenton's SatelZone is a Satel partition and its SatelInput a Satel zone. vCLU's
-- own Satel integration (Satel, SatelPartition, SatelZone), which talks to the panel's
-- ETHM module itself, uses Satel's names; these objects go through the GATE and are named
-- RemoteGate* so the two never mix.
--
-- Numbers (class, features, methods, events) from Object Manager's device interfaces
-- (clu_GATE_ALARM_ft00000002_*), not checked on a GATE Alarm yet:
--   100 SatelZone      value 1 armed, 0 disarmed, -1 unknown; execute 0 ArmZone, 1 DisarmZone
--   101 SatelInput     value 1 violated; read only
--   102 SatelOutput    value 1 on; execute 0 SwitchOn, 1 SwitchOff
--   103 Satel          value 1 connected; settings only
--   150 Jablotron      settings only (feature 0 is the admin code: no value)
--   151 JablotronSection value State; execute 0 Arm, 1 ArmPartially, 2 Disarm
--   152 JablotronOutput value 1 on; execute 0 SwitchOn, 1 SwitchOff
--   153 JablotronDevice value 1 active; read only
--
-- Guards (given to RemoteObject._setGuard and checked by RemoteObject's get, set and execute,
-- so by every way in: a method here, obj:execute(n), obj:set(n, v), MCP, Home Assistant,
-- Wire). The tables below are read-only; the guard keeps its own copy, taken when this file
-- loads, and the object's ID and CLU from its creation:
-- - only the methods listed above are sent (METHODS per class); the connections (Satel,
--   Jablotron) and the inputs and devices take none;
-- - no feature is written: they are the GATE's settings (panel address, port, encryption,
--   Nr, which repoints an object) or secrets; set(0, v) on a zone, section or output is
--   turned into its methods;
-- - disarming (DisarmZone, a section's Disarm) is refused unless
--   grenton.gateAlarm.allowDisarm is true in .vclu.json; arming always works;
-- - the features that hold the panel's passwords, codes, key and address are never read.
-- Raw paths are out of reach of any guard: a script sent to the GATE
-- (clu:execute(0, "...")) and a subscription asked by hand
-- (RemoteSubscription.subscribe(cluIP, { { objId = "SAT0001", featureIndex = 4 } }, ...)).
-- @module objects.RemoteGateAlarm
-- @inherits RemoteObject

RemoteGateAlarm = setmetatable({}, {__index = RemoteObject})
RemoteGateAlarm.__index = RemoteGateAlarm

-- Feature numbers never read or written, by name (per class)
RemoteGateAlarm.SECRET = {}
-- Method numbers that disarm, by name (per class)
RemoteGateAlarm.DISARM = {}
-- Method numbers vCLU sends, by name (per class); any other is refused
RemoteGateAlarm.METHODS = {}

-- The guard's rules per class, taken by seal() at the end of this file, and per object
local classRules = {}
local objectRules = setmetatable({}, {__mode = "k"})

-- Go's setting, taken when the runtime loads: a script that replaces the global (or
-- RemoteGateAlarm.disarmAllowed) does not change what the guard asks
local goAllowDisarm = __go_gate_alarm_allow_disarm

local function allowDisarm()
    return type(goAllowDisarm) == "function" and goAllowDisarm() == true
end

--- Whether the configuration allows disarming a GATE Alarm's zones and sections
-- (grenton.gateAlarm.allowDisarm in .vclu.json, default false).
-- @api public
-- @return boolean
function RemoteGateAlarm.disarmAllowed()
    return allowDisarm()
end

local function label(self)
    return tostring(self._type) .. " " .. tostring(self._objId)
end

-- Why a read, write or method of this object is refused; nil when it is not
local function refused(self, kind, index)
    local r = objectRules[self]
    if not r then return "not a GATE Alarm object" end
    if rawget(self, "_objId") ~= r.objId or rawget(self, "_clu") ~= r.clu then
        return label(self) .. ": the object's ID or CLU changed after it was created"
    end
    -- the number goes into the script for the GATE: "1", "1) x(", NaN or math.huge would
    -- pass the tables; the same rule as Wire's (a whole number 0-9999)
    if type(index) ~= "number" or index ~= index or index < 0 or index >= 10000
       or index ~= math.floor(index) then
        return label(self) .. ": " .. tostring(kind) .. " takes a whole number 0-9999, got " .. tostring(index)
    end
    if (kind == "get" or kind == "set") and r.secret[index] then
        return label(self) .. ": feature " .. tostring(index) .. " (" .. r.secret[index]
            .. ") holds a secret of the alarm panel; vCLU never reads or writes it"
    end
    if kind == "set" then
        return label(self) .. ": feature " .. tostring(index)
            .. " is a setting of the GATE; vCLU does not write it (commands are the object's methods)"
    end
    if kind == "execute" then
        if not r.methods[index] then
            return label(self) .. ": method " .. tostring(index) .. " is not one vCLU sends to it"
        end
        if r.disarm[index] and not allowDisarm() then
            return label(self) .. ": " .. r.disarm[index]
                .. " is refused; disarming needs grenton.gateAlarm.allowDisarm in .vclu.json"
        end
    end
    return nil
end

--- Why a read, write or method of this object is refused; nil when it is not.
-- @param kind string "get", "set" or "execute"
-- @param index number The feature or method number
-- @return string|nil
function RemoteGateAlarm:_refused(kind, index)
    return refused(self, kind, index)
end

--- The value the GATE reported (feature 0).
-- @api public
-- @return number|nil
function RemoteGateAlarm:getValue()
    return self._lastState
end

-- Value changes as Grenton's events: on[new] after off, off[old] after on
local function edges(self, oldState, newState, onEvent, offEvent)
    if oldState == nil or oldState == newState then return end
    if oldState == 0 and newState == 1 then
        self:emit(onEvent)
    elseif oldState == 1 and newState == 0 then
        self:emit(offEvent)
    end
end

local function class(name, typ, methods)
    local c = setmetatable({}, {__index = RemoteGateAlarm})
    c.__index = c
    c.SECRET = {}
    c.DISARM = {}
    c.METHODS = {}
    c.new = function(self, clu, objId, objName)
        local obj = RemoteObject.new(self, clu, objId, objName)
        obj._type = typ
        obj._allowedMethods = methods
        local r = classRules[c]
        objectRules[obj] = { objId = objId, clu = clu, secret = r.secret, disarm = r.disarm, methods = r.methods }
        RemoteObject._setGuard(obj, refused)
        return obj
    end
    _G[name] = c
    return c
end

-- ==========================================================================
-- Satel (through the GATE)
-- ==========================================================================

--- A Satel partition (Grenton's SatelZone, class 100): 1 armed, 0 disarmed, -1 unknown.
-- Events OnChange, OnArm, OnDisarm. Not exposed to Home Assistant or HomeKit unless by
-- expose() (a switch: on arms, off disarms; HomeKit shows it read-only).
RemoteGateSatelZone = class("RemoteGateSatelZone", "GATE_SATEL_ZONE",
    {"getValue", "isArmed", "armZone", "disarmZone"})
RemoteGateSatelZone.ARM = 0
RemoteGateSatelZone.DISARM = { [1] = "DisarmZone" }
RemoteGateSatelZone.METHODS = { [0] = "ArmZone", [1] = "DisarmZone" }
RemoteGateSatelZone.SECRET = { [2] = "UserPassword" }
RemoteGateSatelZone._explicitOnly = true -- exposeRegistry() leaves it out
RemoteGateSatelZone._satel = true        -- HomeKit read-only, as the Satel integration's

--- Arms the partition (execute 0 ArmZone); the GATE reports the state.
-- @api public
function RemoteGateSatelZone:armZone()
    self:execute(RemoteGateSatelZone.ARM)
end

--- Disarms the partition (execute 1 DisarmZone). Refused unless
-- grenton.gateAlarm.allowDisarm is true in .vclu.json.
-- @api public
function RemoteGateSatelZone:disarmZone()
    self:execute(1)
end

--- Whether the partition is armed (nil while the GATE has not reported it).
-- @api public
-- @return boolean|nil
function RemoteGateSatelZone:isArmed()
    if self._lastState == nil or self._lastState == -1 then return nil end
    return self._lastState == 1
end

--- set(0, 1) arms and set(0, 0) disarms, by the methods (Home Assistant's switch writes
-- set(0, v)); other features are refused (RemoteGateAlarm:_refused).
-- @param index number Feature number
-- @param value any The value
function RemoteGateSatelZone:set(index, value)
    if index == 0 then
        if value == 1 or value == true then return self:armZone() end
        if value == 0 or value == false then return self:disarmZone() end
        error(label(self) .. ": set(0, v) takes 1 (arm) or 0 (disarm)", 2)
    end
    return RemoteObject.set(self, index, value)
end

function RemoteGateSatelZone:updateState(newState)
    local oldState = self._lastState
    RemoteObject.updateState(self, newState)
    edges(self, oldState, newState, "OnArm", "OnDisarm")
end

--- Registers a callback for when the partition is armed.
-- @api public
function RemoteGateSatelZone:onArm(callback) return self:onEvent("OnArm", callback) end

--- Registers a callback for when the partition is disarmed.
-- @api public
function RemoteGateSatelZone:onDisarm(callback) return self:onEvent("OnDisarm", callback) end

--- A Satel zone, a detector (Grenton's SatelInput, class 101): 0 normal, 1 violated.
-- Events OnChange, OnSwitchOn, OnSwitchOff. Exposed like a DIN (a binary sensor).
RemoteGateSatelInput = class("RemoteGateSatelInput", "GATE_SATEL_INPUT", {"getValue"})

function RemoteGateSatelInput:updateState(newState)
    local oldState = self._lastState
    RemoteObject.updateState(self, newState)
    edges(self, oldState, newState, "OnSwitchOn", "OnSwitchOff")
end

--- Registers a callback for when the input is violated.
-- @api public
function RemoteGateSatelInput:onSwitchOn(callback) return self:onEvent("OnSwitchOn", callback) end

--- Registers a callback for when the input is back to normal.
-- @api public
function RemoteGateSatelInput:onSwitchOff(callback) return self:onEvent("OnSwitchOff", callback) end

--- A Satel output (Grenton's SatelOutput, class 102): 0 off, 1 on.
-- Events OnChange, OnSwitchOn, OnSwitchOff. Exposed like a DOUT (a switch; HomeKit read-only).
RemoteGateSatelOutput = class("RemoteGateSatelOutput", "GATE_SATEL_OUTPUT",
    {"getValue", "setValue", "switchOn", "switchOff", "toggle"})
RemoteGateSatelOutput.SWITCH_ON = 0
RemoteGateSatelOutput.SWITCH_OFF = 1
RemoteGateSatelOutput.SECRET = { [2] = "UserPassword" }
RemoteGateSatelOutput.METHODS = { [0] = "SwitchOn", [1] = "SwitchOff" }
RemoteGateSatelOutput._satel = true

--- Switches the output on (execute 0); the GATE reports the state.
-- @api public
function RemoteGateSatelOutput:switchOn() self:execute(self.SWITCH_ON) end

--- Switches the output off (execute 1).
-- @api public
function RemoteGateSatelOutput:switchOff() self:execute(self.SWITCH_OFF) end

--- Switches the output off when it is on, else on.
-- @api public
function RemoteGateSatelOutput:toggle()
    if self._lastState == 1 then self:switchOff() else self:switchOn() end
end

--- 1 (or true) switches on, anything else off.
-- @api public
-- @param value number 0 or 1
function RemoteGateSatelOutput:setValue(value)
    if value == 1 or value == true then self:switchOn() else self:switchOff() end
end

--- set(0, v) switches by the methods; other features are refused (RemoteGateAlarm:_refused).
-- @param index number Feature number
-- @param value any The value
function RemoteGateSatelOutput:set(index, value)
    if index == 0 then return self:setValue(value) end
    return RemoteObject.set(self, index, value)
end

function RemoteGateSatelOutput:updateState(newState)
    local oldState = self._lastState
    RemoteObject.updateState(self, newState)
    edges(self, oldState, newState, "OnSwitchOn", "OnSwitchOff")
end

--- Registers a callback for when the output switches on.
-- @api public
function RemoteGateSatelOutput:onSwitchOn(callback) return self:onEvent("OnSwitchOn", callback) end

--- Registers a callback for when the output switches off.
-- @api public
function RemoteGateSatelOutput:onSwitchOff(callback) return self:onEvent("OnSwitchOff", callback) end

--- The GATE's connection to the Satel panel (class 103): 0 disconnected, 1 connected.
-- Events OnConnected, OnDisconnected (OnError only reaches vCLU if the GATE sends it).
-- Not exposed.
RemoteGateSatel = class("RemoteGateSatel", "GATE_SATEL", {"getValue", "isConnected"})
RemoteGateSatel.SECRET = { [2] = "IP", [4] = "AdminPassword", [7] = "EncryptionKey" }

--- Whether the GATE is connected to the panel (nil while not reported).
-- @api public
-- @return boolean|nil
function RemoteGateSatel:isConnected()
    if self._lastState == nil then return nil end
    return self._lastState == 1
end

function RemoteGateSatel:_getExposeType() return nil end

function RemoteGateSatel:updateState(newState)
    local oldState = self._lastState
    RemoteObject.updateState(self, newState)
    edges(self, oldState, newState, "OnConnected", "OnDisconnected")
end

--- Registers a callback for when the GATE connects to the panel.
-- @api public
function RemoteGateSatel:onConnected(callback) return self:onEvent("OnConnected", callback) end

--- Registers a callback for when the GATE loses the panel.
-- @api public
function RemoteGateSatel:onDisconnected(callback) return self:onEvent("OnDisconnected", callback) end

-- ==========================================================================
-- Jablotron (through the GATE)
-- ==========================================================================

--- The GATE's connection to a Jablotron panel (class 150): settings only. Its feature 0
-- is the admin code, so it has no value and is never subscribed. Not exposed.
RemoteGateJablotron = class("RemoteGateJablotron", "GATE_JABLOTRON", {})
RemoteGateJablotron.SECRET = { [0] = "AdminCode" }

--- No value: feature 0 is the admin code.
-- @return nil
function RemoteGateJablotron:getValue() return nil end

function RemoteGateJablotron:_getExposeType() return nil end

--- Nothing to follow: feature 0 is the admin code.
-- @return table
function RemoteGateJablotron:subscribeFeatures() return {} end

--- A Jablotron section (class 151): its State. Events OnChange (and OnStateChange), OnArm
-- (from 0 to another state), OnDisarm (back to 0). Not exposed unless by expose()
-- (a switch: on arms, off disarms; HomeKit read-only). UNVERIFIED: which number is which
-- state is not known (0 is taken as disarmed); OnArm/OnDisarm may be wrong for alarm or
-- exit-delay states.
RemoteGateJablotronSection = class("RemoteGateJablotronSection", "GATE_JABLOTRON_SECTION",
    {"getValue", "arm", "armPartially", "disarm"})
RemoteGateJablotronSection.ARM = 0
RemoteGateJablotronSection.ARM_PARTIALLY = 1
RemoteGateJablotronSection.DISARM = { [2] = "Disarm" }
RemoteGateJablotronSection.METHODS = { [0] = "Arm", [1] = "ArmPartially", [2] = "Disarm" }
RemoteGateJablotronSection.SECRET = { [2] = "AccessCode" }
RemoteGateJablotronSection._explicitOnly = true
RemoteGateJablotronSection._satel = true

--- Arms the section (execute 0).
-- @api public
function RemoteGateJablotronSection:arm() self:execute(RemoteGateJablotronSection.ARM) end

--- Arms the section partially (execute 1).
-- @api public
function RemoteGateJablotronSection:armPartially() self:execute(RemoteGateJablotronSection.ARM_PARTIALLY) end

--- Disarms the section (execute 2). Refused unless grenton.gateAlarm.allowDisarm is true
-- in .vclu.json.
-- @api public
function RemoteGateJablotronSection:disarm() self:execute(2) end

--- set(0, 1) arms and set(0, 0) disarms, by the methods; other features are refused.
-- @param index number Feature number
-- @param value any The value
function RemoteGateJablotronSection:set(index, value)
    if index == 0 then
        if value == 1 or value == true then return self:arm() end
        if value == 0 or value == false then return self:disarm() end
        error(label(self) .. ": set(0, v) takes 1 (arm) or 0 (disarm)", 2)
    end
    return RemoteObject.set(self, index, value)
end

function RemoteGateJablotronSection:updateState(newState)
    local oldState = self._lastState
    RemoteObject.updateState(self, newState)
    if oldState == nil or oldState == newState then return end
    self:emit("OnStateChange", newState, oldState)
    -- UNVERIFIED: Object Manager's definition gives State no meanings. 0 is taken as
    -- disarmed and any other state as armed, so an alarm or exit-delay state (if the GATE
    -- reports one) would fire OnArm too. Check on a GATE Alarm with a Jablotron panel.
    if oldState == 0 then
        self:emit("OnArm")
    elseif newState == 0 then
        self:emit("OnDisarm")
    end
end

--- Registers a callback for when the section is armed.
-- @api public
function RemoteGateJablotronSection:onArm(callback) return self:onEvent("OnArm", callback) end

--- Registers a callback for when the section is disarmed.
-- @api public
function RemoteGateJablotronSection:onDisarm(callback) return self:onEvent("OnDisarm", callback) end

--- A Jablotron PG output (class 152): 0 off, 1 on. Execute 0 SwitchOn, 1 SwitchOff, as a
-- Satel output. Exposed like a DOUT (a switch; HomeKit read-only).
RemoteGateJablotronOutput = class("RemoteGateJablotronOutput", "GATE_JABLOTRON_OUTPUT",
    {"getValue", "setValue", "switchOn", "switchOff", "toggle"})
RemoteGateJablotronOutput.SWITCH_ON = 0
RemoteGateJablotronOutput.SWITCH_OFF = 1
RemoteGateJablotronOutput.SECRET = { [2] = "AccessCode" }
RemoteGateJablotronOutput.METHODS = RemoteGateSatelOutput.METHODS
RemoteGateJablotronOutput._satel = true
RemoteGateJablotronOutput.switchOn = RemoteGateSatelOutput.switchOn
RemoteGateJablotronOutput.switchOff = RemoteGateSatelOutput.switchOff
RemoteGateJablotronOutput.toggle = RemoteGateSatelOutput.toggle
RemoteGateJablotronOutput.setValue = RemoteGateSatelOutput.setValue
RemoteGateJablotronOutput.set = RemoteGateSatelOutput.set
RemoteGateJablotronOutput.updateState = RemoteGateSatelOutput.updateState
RemoteGateJablotronOutput.onSwitchOn = RemoteGateSatelOutput.onSwitchOn
RemoteGateJablotronOutput.onSwitchOff = RemoteGateSatelOutput.onSwitchOff

--- A Jablotron device, a detector (class 153): 0 idle, 1 active. Events OnChange,
-- OnSwitchOn, OnSwitchOff. Exposed like a DIN (a binary sensor).
RemoteGateJablotronDevice = class("RemoteGateJablotronDevice", "GATE_JABLOTRON_DEVICE", {"getValue"})
RemoteGateJablotronDevice.updateState = RemoteGateSatelInput.updateState
RemoteGateJablotronDevice.onSwitchOn = RemoteGateSatelInput.onSwitchOn
RemoteGateJablotronDevice.onSwitchOff = RemoteGateSatelInput.onSwitchOff

-- ==========================================================================
-- The guard's rules: a copy for the guard, read-only tables for scripts
-- ==========================================================================

local function copy(t)
    local out = {}
    for k, v in pairs(t) do out[k] = v end
    return out
end

local function readOnly(t, what)
    return setmetatable({}, {
        __index = t,
        __newindex = function() error(what .. " is read-only", 2) end,
        __metatable = false,
    })
end

for _, name in ipairs({ "RemoteGateSatelZone", "RemoteGateSatelInput", "RemoteGateSatelOutput",
        "RemoteGateSatel", "RemoteGateJablotron", "RemoteGateJablotronSection",
        "RemoteGateJablotronOutput", "RemoteGateJablotronDevice" }) do
    local c = _G[name]
    local r = { secret = copy(c.SECRET), disarm = copy(c.DISARM), methods = copy(c.METHODS) }
    classRules[c] = r
    c.SECRET = readOnly(r.secret, name .. ".SECRET")
    c.DISARM = readOnly(r.disarm, name .. ".DISARM")
    c.METHODS = readOnly(r.methods, name .. ".METHODS")
end

return RemoteGateAlarm
