-- Satel objects and the event polling timer, once the Satel manager is set
if __go_satel_poll_event and Satel then
	Satel.setup()
	EventBus:getShared():setInterval(function()
		Satel.pollEvents()
	end, 100)
end
