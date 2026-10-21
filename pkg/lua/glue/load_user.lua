-- user.lua defines the functions om.lua events refer to; a failure is only a warning
local ok, err = pcall(function() require "user" end)
if not ok then
	Logger:getShared():warn("[LUA] user.lua skipped: " .. tostring(err))
end
