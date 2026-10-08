--- Logger module for vCLU runtime.
-- Provides debug, info, error and clu-level logging.
-- @module Logger

Logger = {}
Logger.level = 0

local shared = nil

function Logger:new(javascriptBridge)

    local logger = {
        id = 'logger',
        javascriptBridge = javascriptBridge,
    }

    setmetatable(logger, self)
    self.__index = self

    return logger
end

function Logger:getShared()
    return shared
end

function Logger:debug(message)
    if (self.javascriptBridge ~= nil) then
        self.javascriptBridge:log('debug', self:toString(message))
    else
        print("debug: " .. self:toString(message))
    end
end

function Logger:error(message)
    if (self.javascriptBridge ~= nil) then
        self.javascriptBridge:log('error', message)
    else
        print("error: " .. self:toString(message))
    end
end

function Logger:info(message)
    if (self.javascriptBridge ~= nil) then
        self.javascriptBridge:log('info', message)
    else
        print("info: " .. self:toString(message))
    end
end

function Logger:cluLogger(message)
    if (self.javascriptBridge ~= nil) then
        self.javascriptBridge:log('clu', message)
    else
        print("clu: " .. self:toString(message))
    end
end

function Logger:toString(msg)
    if (msg == nil) then
        return "nil"
    end
    return msg
end

if (_CLU ~= nil) then
    -- sprawdzenie czy w przegladarce czy nie
    shared = Logger:new(JavascriptBridge:getShared())
else
    shared = Logger:new(nil)
end

return Logger
