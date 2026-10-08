-- mocno polaczone z SYSTEM.lua
-- helper wyciagajacy rzeczy z modulow

ModuleData = {}

function ModuleData:new(module)

    o = {
        id = "ModuleData",
        module = module,
    }
    setmetatable(o, self)
    self.__index = self

    return o

end

function ModuleData:getAddress()

    if (self.module.address == nil) then
        return 'nil'
    end

    return self.module.address

end

function ModuleData:getDescription()
    if (self.module.description == nil) then
        return 'nil'
    end

    return self.module.description
end

function ModuleData:getId()
    if (self.module == nil) then
        return 'nil'
    end
    if (self.module.id == nil) then
        return 'nil'
    end

    return self.module.id
end

shared = ModuleData:new()

return ModuleData
