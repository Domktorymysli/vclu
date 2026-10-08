---
-- @script
-- Internal OBJECT factory module (not part of public API)

OBJECT = {}

local __CLU__ = 0
local __REMOTE_CLU__ = 1
local __MODULE__ = 2
local __DIN__ = 3
local __DOUT__ = 4
local __TIMER__ = 6
local __BUTTON__ = 7
local __ANALOG_IN__ = 12
local __ANALOG_OUT__ = 13
local __THERMOSTAT__ = 18
local __PANEL_SENSTEMP__ = 20
local __PANEL_SENSLIGHT__ = 21
local __ONEW_SENSOR__ = 23
local __SMART_PANEL__ = 29
local __PANEL_PAGE__ = 19
local __PANEL__ = 29
local __PANEL_BUTTON__ = 30

OBJECT.__types = {
    [__CLU__] = "CLU",
    [__REMOTE_CLU__] = "__REMOTE_CLU__",
    [__MODULE__] = "MODULE",
    [__DIN__] = "DIN",
    [__DOUT__] = "DOUT",
    [__TIMER__] = "Timer",
    [__SMART_PANEL__] = "PANEL2", -- smart panel
    [__PANEL__] = "PANEL",  -- stary panel
    [__BUTTON__] = "BUTTON",
    [__ANALOG_IN__] = "ANALOG_IN",
    [__ANALOG_OUT__] = "ANALOG_OUT",
    --[__THERMOSTAT__] = "Thermostat",
    --[__PANEL_SENSTEMP__] = "PanelSensTemp",
    --[__PANEL_SENSLIGHT__] = "PanelSensLight",
    --[__ONEW_SENSOR__] = "ONEW_SENSOR",
    [__PANEL_BUTTON__] = "PANEL_BUTTON",
}

--
--
-- @TODO Mozna uzyc logicModuleAsString do przypisywania modulowi roznych wersji plikow lua. Zeby dalo sie miec pliki lua dla kazdego interfejsu. Zeby symulator umial w wersje.
-- Czyli symulator przed pierwszym uruchomieniem by generowal sobie plik MainObject.LUA z ifami przygotowanymi specjlanie dla jego pliku OM.LUA
--
--

function OBJECT:new(type, logicModule, addressOrIndex, logicModuleAsString)

    local system = SYSTEM:getShared()
    local generatedModuleId = system:getNextModuleId() -- wazne!! generowanie unikatowych id dla modułów i eventów
    local eventBus = system:getEventBus()
    local logger = system:getLogger()

    if (eventBus == nil) then
        logger:error("eventBus can not be nil")
        error("eventBus can not be nil")
    end


    if (type == __CLU__) then
        if (addressOrIndex == nil) then
            addressOrIndex = 0
        end

        local clu = CLU:new(generatedModuleId, logicModule, eventBus)
        system:registerModule(clu)
        return clu
    end

    -- handle remote CLU

    if (type == __REMOTE_CLU__) then
        if (addressOrIndex == nil) then
            addressOrIndex = 0
        end

        local clu = CLU_REMOTE:new(generatedModuleId, logicModule, eventBus)
        system:registerModule(clu)
        return clu
    end

    if (type == __MODULE__) then
        -- @todo nazwy zmiennych dla modułu są dziwne
        local module = MODULE:new(generatedModuleId, type, logicModule, logicModuleAsString, eventBus)
        system:registerModule(module)
        return module
    end

    if (type == __BUTTON__) then
        local button = BUTTON:new(generatedModuleId, logicModule, addressOrIndex, eventBus)
        system:registerModule(button)
        return button
    end

    if (type == __ANALOG_IN__) then
        local analogIn = ANALOG_IN:new(generatedModuleId, logicModule, addressOrIndex, eventBus)
        system:registerModule(analogIn)
        return analogIn
    end

    if (type == __ANALOG_OUT__) then
        local analogOut = ANALOG_OUT:new(generatedModuleId, logicModule, addressOrIndex, eventBus)
        system:registerModule(analogOut)
        return analogOut
    end

    if (type == __DIN__) then
        local din = DIN:new(generatedModuleId, logicModule, addressOrIndex, eventBus)
        system:registerModule(din)
        return din
    end

    if (type == __DOUT__) then
        local dout = DOUT:new(generatedModuleId, logicModule, addressOrIndex, eventBus)
        system:registerModule(dout)
        return dout
    end

    if (type == __SMART_PANEL__) then
        local panel = PANEL2:new(generatedModuleId, logicModule, addressOrIndex, eventBus)
        system:registerModule(panel)
        return panel
    end

    if (type == __PANEL_PAGE__) then
        local panelPage = PANEL_PAGE:new(generatedModuleId, logicModule, addressOrIndex, eventBus)
        system:registerModule(panelPage)
        return panelPage
    end

    --if (type == __PANEL__) then
    --    local panel = PANEL:new(generatedModuleId, logicModule, addressOrIndex, eventBus)
    --    system:registerModule(panel)
    --    return panel
    --end

    if (type == __PANEL_BUTTON__) then
        local panelButton = PANEL_BUTTON:new(generatedModuleId, logicModule, addressOrIndex, eventBus)
        system:registerModule(panelButton)
        return panelButton
    end

    --if (type == __PANEL_SENSTEMP__) then
    --    local panelSentemp = PanelSensTemp:new(generatedModuleId, logicModule, addressOrIndex, eventBus)
    --    system:registerModule(panelSentemp)
    --    return panelSentemp
    --end

    --if (type == __PANEL_SENSLIGHT__) then
    --    local panelSenlight = PanelSensLight:new(generatedModuleId, logicModule, addressOrIndex, eventBus)
    --    system:registerModule(panelSenlight)
    --    return panelSenlight
    --end

    --if (type == __ONEW_SENSOR__) then
    --    local oNewSensor = OnewSensor:new(generatedModuleId, logicModule, addressOrIndex, eventBus)
    --    system:registerModule(oNewSensor)
    --    return oNewSensor
    --end

    --if (type == __THERMOSTAT__) then
    --    local thermostat = Thermostat:new(generatedModuleId, logicModule, eventBus)
    --    system:registerModule(thermostat)
    --    return thermostat
    --end

    if (type == __TIMER__) then
        local timer = Timer:new(generatedModuleId, logicModule, eventBus)
        system:registerModule(timer)
        return timer
    end

    -- Obsługa wszystkich innych modułów o których nie mam pojęcia
    local undefined = UNDEFINED:new(generatedModuleId, logicModule, addressOrIndex, eventBus)
    system:registerModule(undefined)
    return undefined

end

return OBJECT
