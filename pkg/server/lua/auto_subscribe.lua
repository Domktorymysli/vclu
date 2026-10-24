-- Subscribes to the remote CLUs at startup and after a reset
if RemoteSubscription and RemoteSubscription.autoSubscribe then
	RemoteSubscription.autoSubscribe()
	return "OK"
else
	return "RemoteSubscription not available"
end
