--
-- Undefined
-- Nie wiadomo co to za moduł. Dodany żeby obsługiwać wszystkie inne dziwne moduły
--

UNDEFINED = {}

function UNDEFINED:new(id, module, index, eventBus)
    local undefined = {
        id = id,
        module = module,
        index = index,
        address = 0,
        eventBus = eventBus,
    }

    setmetatable(undefined, self)
    self.__index = self

    return undefined
end

function UNDEFINED:set(key, value)
    Logger.getShared():debug("Undefined:set " .. key)
end

function UNDEFINED:get(key)
    Logger.getShared():debug("Undefined:get " .. key)
    return 0
end

function UNDEFINED:add_event(eventName, callback)
    self.eventBus:on(self, eventName, callback)
end

function UNDEFINED:execute()
    Logger.getShared():debug("Undefined:execute")
end

function UNDEFINED:__getState()
    return {
        id = self.id,
        address = self.address,
        type = 'UNDEFINED'
    }
end

return UNDEFINED
