-- Runs a plugin's init.lua in its sandbox; loader.go appends the call
local function execute(pluginID, code)
	local _, err = PluginSandbox:execute(pluginID, code)
	if err then
		error(err)
	end
end
