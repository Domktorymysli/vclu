-- The default MQTT client on the embedded broker, with the HA integration;
-- server.go appends the call
local function connectBroker(port, username, password)
	MQTT:setHost("localhost"):setPort(port):setClientId("vclu_internal"):setCredentials(username, password)
	-- connect() only starts the connection: set HA up once, on the first connect
	local ha = { done = false }
	MQTT:onConnect(function()
		if ha.done then return end
		ha.done = true
		HAIntegration.setup(MQTT)
	end)
	return MQTT:connect()
end
