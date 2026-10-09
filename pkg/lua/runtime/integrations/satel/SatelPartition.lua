---
--- SatelPartition - alarm partition as a switch (on = armed).
--- Inherits VirtualSwitch (_type = "DOUT", expose type "switch").
--- Arming is NOT optimistic — the armed state is confirmed by the panel
--- (exit delay may apply); disarm/clearAlarm are blocked in Go unless
--- allowDisarm is set in the configuration.
-- @module SatelPartition
-- @inherits VirtualSwitch
---

local SatelPartition = setmetatable({}, {__index = VirtualSwitch})
SatelPartition.__index = SatelPartition

--- Creates a partition object.
-- @param number number 1-based partition number on the panel
-- @param name string Optional custom name (otherwise panel name is used)
-- @param opts table Optional: { armMode = 0-3 } (default arm mode)
function SatelPartition:new(number, name, opts)
    local obj = VirtualSwitch.new(self, name or ("Satel Partition " .. number))
    obj._satel = true  -- HomeKit shows Satel objects read-only
    obj.number = number
    obj._customName = name ~= nil
    obj._armMode = (opts and opts.armMode) or 0
    obj._alarm = false
    obj._fireAlarm = false
    obj._entryTime = false
    obj._exitTime = false
    obj._allowedMethods = {"getValue", "isArmed", "isAlarm", "arm", "forceArm",
                           "disarm", "clearAlarm", "switchOn", "switchOff", "setValue"}
    return obj
end

--- Arms the partition (async; state confirmed by the panel).
-- @param mode number Optional arm mode 0-3 (default from config)
function SatelPartition:arm(mode)
    local ok, err = Satel.armPartition(self.number, mode or self._armMode)
    if not ok then
        Logger:warn("[satel] arm partition %d failed: %s", self.number, tostring(err))
    end
    return ok, err
end

--- Force-arms the partition (async).
function SatelPartition:forceArm(mode)
    local ok, err = Satel.forceArm(self.number, mode or self._armMode)
    if not ok then
        Logger:warn("[satel] force arm partition %d failed: %s", self.number, tostring(err))
    end
    return ok, err
end

--- Disarms the partition. Blocked unless allowDisarm in config.
function SatelPartition:disarm()
    local ok, err = Satel.disarmPartition(self.number)
    if not ok then
        Logger:warn("[satel] disarm partition %d failed: %s", self.number, tostring(err))
    end
    return ok, err
end

--- Clears the partition alarm. Blocked unless allowDisarm in config.
function SatelPartition:clearAlarm()
    local ok, err = Satel.clearAlarm(self.number)
    if not ok then
        Logger:warn("[satel] clear alarm partition %d failed: %s", self.number, tostring(err))
    end
    return ok, err
end

-- Switch semantics: on = arm, off = disarm.
function SatelPartition:switchOn() self:arm() end
function SatelPartition:switchOff() self:disarm() end

function SatelPartition:setValue(value)
    if value == 1 or value == true then self:arm() else self:disarm() end
end

function SatelPartition:toggle()
    if self._state == 1 then self:disarm() else self:arm() end
end

--- Whether the partition is armed.
function SatelPartition:isArmed() return self._state == 1 end

--- Whether the partition is in alarm.
function SatelPartition:isAlarm() return self._alarm end

--- Whether the partition is in fire alarm.
function SatelPartition:isFireAlarm() return self._fireAlarm end

--- Whether the partition is counting entry time.
function SatelPartition:isEntryTime() return self._entryTime end

--- Whether the partition is counting exit time.
function SatelPartition:isExitTime() return self._exitTime end

--- Applies a partition state table from the panel
--- ({ armed, alarm, fireAlarm, entryTime, exitTime, name }).
function SatelPartition:_applyState(s)
    if s.name and s.name ~= "" and not self._customName then self._name = s.name end
    self._fireAlarm = s.fireAlarm or false
    self._entryTime = s.entryTime or false
    self._exitTime = s.exitTime or false

    local wasAlarm = self._alarm
    self._alarm = s.alarm or false
    self:updateState(s.armed and 1 or 0)
    if self._alarm and not wasAlarm then
        self:emit("OnAlarm")
    end
end

--- Registers a callback fired when the partition enters alarm state.
function SatelPartition:onAlarm(callback)
    return self:onEvent("OnAlarm", callback)
end

--- Registers a callback fired when the partition is armed.
function SatelPartition:onArmed(callback)
    return self:onEvent("OnSwitchOn", callback)
end

--- Registers a callback fired when the partition is disarmed.
function SatelPartition:onDisarmed(callback)
    return self:onEvent("OnSwitchOff", callback)
end

return SatelPartition
