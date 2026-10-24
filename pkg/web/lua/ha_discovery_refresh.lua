-- Republishes Home Assistant discovery; the number of exposed objects, -1 when
-- the HA integration is off, -2 when its MQTT client cannot publish (MQTT:publish
-- drops the Go "not connected" error, so refresh() would report nothing)
if not (HAStatePublisher and HAStatePublisher.isEnabled()) then return -1 end
local m = HAStatePublisher.mqttInstance
if not m or (m.isConnected and not m:isConnected()) then return -2 end
HAStatePublisher.refresh()
local n = 0
for _ in pairs(ExposedObjects:list()) do n = n + 1 end
return n
