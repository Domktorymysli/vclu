-- user.lua in the sandbox: a failure is a warning, as in LoadCLUData
local ok, err = pcall(function() require "user" end)
if not ok then
	__sandbox_log("[WARN] user.lua skipped: " .. tostring(err))
end
