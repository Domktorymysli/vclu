MODULE = {}

function MODULE:new(id, type, firmware, address)
    local module = {
        description = 'MODULE',
        id = id,
        type = type,
        firmware = firmware,
        address = address,

        modules = {},
    }

    setmetatable(module, self)
    self.__index = self

    return module
end

function MODULE:add_event(eventName, callback)
    self.eventBus:on(self, eventName, callback)
end


function MODULE:set(key, value)
    print ("MODULE:set " .. key .. ': ' .. value)
end

function MODULE:get(key)
    print ("MODULE:get " .. key)
end

function MODULE:get_address()
    return this.address
end

function MODULE:__getState()
    return {
        id = self.id,
        address = self.address,
        type = 'MODULE',
        modules = self.modules,
    }
end

-- Nie ma tego w dokumentacji OM. SmartPanel ze stronami wymusił to. Przez ten mechanizm będą komunikowały się elementy między sobą.
-- Ta klasa ma nie miec juz wiecej odpowiedzialnosci. Tylko tyle ma wiedziec o modułach. To jest max. Ma rejestrowac i zwracac zarejstrowane moduly.
function MODULE:register(module)
    if (module == nil) then
        return
    end

    if (module.address == nil) then
        error("MODULE:register given module without address")
    end

    if (module.id == nil) then
        error("MODULE:register given module withoud id")
    end

    local key = module.id .. "-" .. module.address

    if (self.modules[key] ~= nil) then
        error("MODULE:register initialization error module registered more than one time! Address: " .. key)
    end

    self.modules[key] = module
end

function MODULE:getModules()
    return self.modules
end

-- pozwala pobrac po typie
function MODULE:getModulesByType(type)
    local tmp = {}

    if (type == nil) then
        return tmp
    end

    for index, value in pairs(self.modules) do -- ipairs for integer keys
        local state = value:__getState()

        if (state["type"] == type) then
            tmp[index] = value
        end
    end

    return tmp
end

function MODULE:getModulesByTypeAndIndex(type, index)
    if type == nil then
        return error("MODULE:getModulesByTypeAndIndex type cannot be nil")
    end

    if (index == nil) then
        return error("MODULE:getModulesByTypeAndIndex index cannot be nil")
    end

    local i = tonumber(index)

    if (i < 0 or i > 15) then
        error ("MODULE:getModulesByTypeAndIndex index out of range")
    end

    for index, value in pairs(self.modules) do -- ipairs for integer keys
        local state = value:__getState()

        if (state["type"] == type and state["index"] == i) then
            return value
        end
    end

    return nil
end

return MODULE
