---
--- SatelOutput - alarm panel output as a switch.
--- Inherits VirtualSwitch (_type = "DOUT", expose type "switch").
--- switchOn/switchOff queue an async command in Go and update state
--- optimistically; the 1s panel poll confirms or corrects it.
-- @module SatelOutput
-- @inherits VirtualSwitch
---

local SatelOutput = setmetatable({}, {__index = VirtualSwitch})
SatelOutput.__index = SatelOutput

--- Creates an output object.
-- @param number number 1-based output number on the panel
-- @param name string Optional custom name (otherwise panel name is used)
function SatelOutput:new(number, name)
    local obj = VirtualSwitch.new(self, name or ("Satel Output " .. number))
    obj._satel = true  -- HomeKit shows Satel objects read-only
    obj.number = number
    obj._customName = name ~= nil
    return obj
end

--- Switches the output on (async, optimistic).
function SatelOutput:switchOn()
    local ok, err = Satel.setOutput(self.number, true)
    if not ok then
        Logger:warn("[satel] output %d on failed: %s", self.number, tostring(err))
        return
    end
    self:updateState(1)
end

--- Switches the output off (async, optimistic).
function SatelOutput:switchOff()
    local ok, err = Satel.setOutput(self.number, false)
    if not ok then
        Logger:warn("[satel] output %d off failed: %s", self.number, tostring(err))
        return
    end
    self:updateState(0)
end

--- Sets the output state (0/1).
function SatelOutput:setValue(value)
    if value == 1 or value == true then self:switchOn() else self:switchOff() end
end

--- Toggles the output.
function SatelOutput:toggle()
    if self._state == 1 then self:switchOff() else self:switchOn() end
end

--- Applies an output state table from the panel ({ active, name }).
function SatelOutput:_applyState(s)
    if s.name and s.name ~= "" and not self._customName then self._name = s.name end
    self:updateState(s.active and 1 or 0)
end

return SatelOutput
