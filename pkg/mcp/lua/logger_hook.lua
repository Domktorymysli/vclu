-- Errors the runtime only logs also go to __sandbox_error:
-- Logger:error (class), Logger:getShared():error and log.error
local function hook(owner, format)
	local orig = owner and rawget(owner, "error")
	if not orig then return end
	owner.error = function(...)
		local ok, msg = pcall(format, ...)
		__sandbox_error(ok and msg or tostring((select(2, ...))))
		return orig(...)
	end
end
local loggerFormat = function(self, message, ...) return self:format(message, ...) end
hook(Logger, loggerFormat)
local shared = Logger:getShared()
if shared ~= Logger then hook(shared, loggerFormat) end
hook(log, function(fmt, ...) return string.format(fmt, ...) end)
