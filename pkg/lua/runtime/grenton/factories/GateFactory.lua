---
-- @script
-- Internal GATE module (not part of public API)

GATE = {}


local __GATE__ = 5000
local __TIMER__ = 6

local __OBJECT_SATEL__ = 103
local __OBJECT_SATEL_INPUT__ = 101
local __OBJECT_SATEL_OUTPUT__ = 102
local __OBJECT_SATEL_ZONE__ = 100

local __OBJECT_HTTP_LISTENER__ = 120
local __OBJECT_HTTP_REQUEST__ = 121

function GATE:new(type, logicModule, addressOrIndex, logicModuleAsString, string1, string2)

    if (addressOrIndex == nil) then
        addressOrIndex = 0
    end

    local generatedModuleId = system:getNextModuleId() -- wazne!! generowanie unikatowych id dla modułów i eventów
    local eventBus = system:getEventBus()

    if (type == __GATE__) then
        local clu = CLU:new(generatedModuleId, logicModule, eventBus)
        system:registerModule(clu)

        return clu
    end

    if (type == __TIMER__) then
        local timer = Timer:new(generatedModuleId, logicModule, eventBus)
        system:registerModule(timer)

        return timer
    end

    if (type == __OBJECT_SATEL__) then
        local objSatel = Satel:new(generatedModuleId, logicModule, eventBus)
        system:registerModule(objSatel)

        return objSatel
    end

    if (type == __OBJECT_SATEL_INPUT__) then
        local objSatelInput = SatelInput:new(generatedModuleId, logicModule, eventBus)
        system:registerModule(objSatelInput)

        return objSatelInput
    end

    if (type == __OBJECT_SATEL_OUTPUT__) then
        local objSatelOutput = SatelOutput:new(generatedModuleId, logicModule, eventBus)
        system:registerModule(objSatelOutput)

        return objSatelOutput
    end

    if (type == __OBJECT_SATEL_ZONE__) then
        local objSatelZone = SatelZone:new(generatedModuleId, logicModule, eventBus)
        system:registerModule(objSatelZone)

        return objSatelZone
    end

    if (type == __OBJECT_HTTP_REQUEST__) then
        local objHttpRequest = HTTPRequest:new(generatedModuleId, logicModule, eventBus)
        system:registerModule(objHttpRequest)
        return objHttpRequest;
    end

    -- handle all undefined modules
    local undefined = UNDEFINED:new(generatedModuleId, logicModule, 0, eventBus)
    system:registerModule(undefined)
    return undefined

end

return GATE
