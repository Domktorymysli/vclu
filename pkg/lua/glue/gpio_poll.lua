-- GPIO polling timer, once the GPIO manager is set
if __go_gpio_poll and gpio and gpio.pollChanges then
	EventBus:getShared():setInterval(function()
		gpio.pollChanges()
	end, 50)
end
