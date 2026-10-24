-- Runs a plugin's cleanup callback and forgets the instance; loader.go appends the call
local function unload(pluginID)
	if Plugin and Plugin.instances and Plugin.instances[pluginID] then
		local p = Plugin.instances[pluginID]
		if p._cleanupCallback then
			pcall(p._cleanupCallback)
		end
		Plugin.instances[pluginID] = nil
	end
end
