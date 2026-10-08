--
-- EventBus for Javascript runtime
-- Handles events, timers via Javascript
--

EventBus = {}
EventBus.callbacks = {}
EventBus.timers = {}

local shared = nil

function EventBus:new(javascriptBridge)
    if (javascriptBridge == nil) then error("Missing javascript Bridge!") end

    o = {
        id = "EventBus",
        javascriptBridge = javascriptBridge
    }
    setmetatable(o, self)
    self.__index = self

    return o
end

function EventBus:getShared()
    return shared
end

function EventBus:getKey(id, event)
    if (id == nil) then
        id = ''
    end
    if (event == nil) then
        event = ''
    end
    return id .. '-' .. event
    end

function EventBus:on(module, name, callback)
    local eventName = EventBus:getKey(module.id, name);
    Logger.getShared():debug("EventBus:on, Module: " .. module.description .. ", id: " .. module.id .. ", event: " .. eventName)
    if (self.callbacks[eventName] == nil) then
        self.callbacks[eventName] = {}
    end
    table.insert(self.callbacks[eventName], callback)
end

function EventBus:fire(module, name) -- eventy w OM nie mają parametrów
    local eventName = EventBus:getKey(module.id, name);
    Logger.getShared():debug("EventBus:fire, Module: " .. module.description .. ", id: " .. module.id .. ", eventName: " .. eventName)
    self.javascriptBridge:fire(module);

    if(self.callbacks[eventName] == nil) then return end

    for i,v in pairs(self.callbacks[eventName]) do
        v()
    end
end

function EventBus:debug()
    Logger.getShared():debug("-----------------------------")
    for i,v in pairs(self.callbacks) do
        local count = 0
        for _ in pairs(v) do count = count + 1 end
        Logger.getShared():debug("Event: " .. i .. ", callbacks: " .. count)
    end
    Logger.getShared():debug("-----------------------------")
end

function EventBus:setTimeout(callback, period)
    Logger.getShared():debug("EventBus:setTimeout")
    local p = tonumber(period)
    self.javascriptBridge:setTimeout(callback, p)
end

function EventBus:setInterval(callback, period)
    Logger.getShared():debug("EventBus:setInterval")
    local p = tonumber(period)
    return self.javascriptBridge:setInterval(callback, p)
end

function EventBus:clearInterval(interval)
    if (interval == nil) then
        return
    end
    self.javascriptBridge:clearInterval(interval)
end

function EventBus:sendHttpRequest(module, request)
    self.javascriptBridge:sendHttpRequest(module, request)
end

shared = EventBus:new(JavascriptBridge:getShared())

return EventBus
