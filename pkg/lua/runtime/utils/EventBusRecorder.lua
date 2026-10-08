local uv = require('luv')

EventBusRecorder = {}

local shared = nil

function EventBusRecorder:new()
    recorder = {
        events = {},
        httpRequest = {},
    }

    setmetatable(recorder, self)
    self.__index = self

    return recorder
end

function EventBusRecorder:fire(module, name)

    Logger.getShared():info(module.description .. ":" .. module.address .. ", event: " .. name)
    if (name ~= nil) then
        table.insert(self.events, name)
    end
end

function EventBusRecorder:getEvents()
    return self.events
end

-- implementacja timerow w LUA
-- https://github.com/luvit/luv/blob/master/docs.md#uv_timer_t--timer-handle
-- Creating a simple setTimeout wrapper
function EventBusRecorder:setTimeout(callback, timeout)
    local timer = uv.new_timer()
    timer:start(timeout, 0, function ()
        timer:stop()
        timer:close()
        callback()
    end)
    return timer
end

-- Creating a simple setInterval wrapper
function EventBusRecorder:setInterval(callback, interval)
    local timer = uv.new_timer()
    timer:start(interval, interval, function ()
        --timer:stop()
        --timer:close()
        callback()
    end)
    return timer
end

-- And clearInterval
function EventBusRecorder:clearInterval(timer)
    if (timer == nil) then
        return
    end

    if (uv.is_closing(timer)) then
        return
    end

    timer:stop()
    timer:close()
end

function EventBusRecorder:sendHttpRequest(module, request)
    self.httpRequest = request
end

function EventBusRecorder:getHttpRequest()
    return self.httpRequest
end

function EventBusRecorder:getShared()
    return shared
end

shared = EventBusRecorder:new()

return EventBusRecorder
