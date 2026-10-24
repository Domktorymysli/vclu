-- The default MQTT client on the embedded broker, with the HA integration;
-- server.go appends the call
local function connectBroker(port, username, password)
	MQTT:setHost("localhost"):setPort(port):setClientId("vclu_internal"):setCredentials(username, password)
	local ok = MQTT:connect()
	if ok then HAIntegration.setup(MQTT) end
	return ok
end
