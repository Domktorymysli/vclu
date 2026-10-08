--- Virtual text class for read/write string values.
-- _type = "TEXT" — no Grenton hardware equivalent.
-- Use with status dashboard widget for display, or onChange for logic.
-- @module objects.VirtualText
-- @inherits VirtualObject
--
-- Usage:
--   local label = VirtualText:new("ALARM_STATUS", {initial = "OK"})
--   label:expose()
--   label:onChange(function(val, old) print("status:", val) end)

VirtualText = setmetatable({}, {__index = VirtualObject})
VirtualText.__index = VirtualText

--- Creates a new VirtualText instance.
-- @api public
-- @param name string Optional name for the text object
-- @param opts table Optional config: initial (default ""), maxLength, persist
-- @return VirtualText The new virtual text instance
function VirtualText:new(name, opts)
    local obj = VirtualObject.new(self, name)
    obj._type = "TEXT"
    obj._allowedMethods = {"getValue", "setValue"}

    opts = opts or {}
    obj._maxLength = opts.maxLength  -- nil = unlimited
    obj:_setup(opts, "", function(_, v) return tostring(v) end)

    return obj
end

--- Gets the current text value.
-- @api public
-- @return string Current value
function VirtualText:getValue()
    return self._state
end

--- Sets the text value.
-- @api public
-- @param value string The value to set
function VirtualText:setValue(value)
    local s = tostring(value or "")
    if self._maxLength and #s > self._maxLength then
        s = s:sub(1, self._maxLength)
    end
    self:updateState(s)
end

return VirtualText
