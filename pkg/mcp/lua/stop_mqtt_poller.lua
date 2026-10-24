-- MQTT is blocked in the sandbox, so its 100 ms poller would only burn timer
-- callbacks on every advance step
if MQTT and MQTT._pollTimerId then
	__go_clearTimer(MQTT._pollTimerId)
	MQTT._pollTimerId = nil
end
