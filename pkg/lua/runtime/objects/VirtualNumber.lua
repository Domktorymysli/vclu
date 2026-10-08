--- Virtual number class for read/write analog values.
-- _type = "AnalogOUT" — must stay in sync with RemoteNumber.
-- Public API must match RemoteNumber (see objects/RemoteNumber.lua).
-- @module objects.VirtualNumber
-- @inherits VirtualObject
--
-- Usage:
--   local temp = VirtualNumber:new("TARGET_TEMP", {min = 16, max = 30, step = 0.5, unit = "°C"})
--   temp:expose()
--   temp:onChange(function(val, old) print("changed", old, "->", val) end)

VirtualNumber = setmetatable({}, {__index = VirtualObject})
VirtualNumber.__index = VirtualNumber

--- Creates a new VirtualNumber instance.
-- @api public
-- @param name string Optional name for the number object
-- @param opts table Optional config: min, max, step, unit, initial, persist
-- @return VirtualNumber The new virtual number instance
function VirtualNumber:new(name, opts)
    local obj = VirtualObject.new(self, name)
    obj._type = "AnalogOUT"
    obj._allowedMethods = {"getValue", "setValue"}

    opts = opts or {}
    obj._min = opts.min       -- nil = no lower bound
    obj._max = opts.max       -- nil = no upper bound
    obj._step = opts.step     -- nil = continuous
    obj._unit = opts.unit     -- nil = no unit

    -- Initial (or persisted) state, clamped to range if bounds set
    obj:_setup(opts, 0, VirtualNumber._clamp)

    return obj
end

--- Clamps and rounds value to configured bounds and step.
-- @param v number Raw value
-- @return number Clamped/rounded value
function VirtualNumber:_clamp(v)
    if type(v) ~= "number" then v = tonumber(v) or 0 end
    if self._min and v < self._min then v = self._min end
    if self._max and v > self._max then v = self._max end
    if self._step and self._step > 0 then
        local base = self._min or 0
        v = base + math.floor((v - base) / self._step + 0.5) * self._step
    end
    return v
end

--- Gets the current numeric value.
-- @api public
-- @return number Current value
function VirtualNumber:getValue()
    return self._state
end

--- Sets the numeric value (clamped to configured range).
-- @api public
-- @param value number The value to set
function VirtualNumber:setValue(value)
    self:updateState(self:_clamp(value))
end

--- Returns configured metadata (useful for dashboard widgets).
-- @api public
-- @return table {min, max, step, unit}
function VirtualNumber:getMeta()
    return {
        min = self._min,
        max = self._max,
        step = self._step,
        unit = self._unit,
    }
end

return VirtualNumber
