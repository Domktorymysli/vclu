--- Type mapping utilities for vCLU
-- Central module for mapping between Grenton types, expose types, and Home Assistant components.
-- @module TypeMap

local TypeMap = {}

-- Grenton hardware type to abstract expose type mapping
TypeMap.grentonToExpose = {
    -- Switches
    DOUT = "switch",
    RemoteSwitch = "switch",
    -- Lights
    DIMMER = "dimmer",
    LEDRGB = "light",
    LED_CHANNEL = "dimmer",
    RemoteLight = "light",
    RemoteDimmer = "dimmer",
    -- Covers
    ROLLER = "cover",
    ROLLER_SH = "cover",
    RemoteCover = "cover",
    -- Sensors
    DIN = "sensor",
    RemoteSensor = "sensor",
    -- GPIO
    GPIO_DOUT = "switch",
    GPIO_DIN = "binary_sensor",
    -- Analog
    AnalogIN = "number",
    AnalogOUT = "number",
    -- Text
    TEXT = "text",
    -- Temperature
    ONE_WIRE = "temperature",
    PANELSENSTEMP = "temperature",
    RemoteThermometer = "temperature",
    -- Humidity
    HUMIDITY_SENSOR = "humidity",
    -- Light / illuminance
    PANELSENSLIGHT = "illuminance",
    LIGHT_SENSOR_LUX = "illuminance",
}

-- Expose type to Home Assistant component mapping
TypeMap.exposeToHA = {
    -- Basic types
    switch = "switch",
    light = "light",
    dimmer = "light",       -- HA uses light with brightness
    sensor = "binary_sensor",
    motion = "binary_sensor",
    temperature = "sensor",
    humidity = "sensor",
    illuminance = "sensor",
    cover = "cover",
    scene = "switch",       -- Stateless switch
    number = "number",
    text = "sensor",        -- HA has no text entity, display as sensor
    button = "button",
    -- Extended types
    lock = "lock",
    fan = "fan",
    climate = "climate",
    garage_door = "cover",  -- HA uses cover with device_class: garage
}

-- Grenton type to HA component direct mapping (legacy)
TypeMap.grentonToHA = {
    DOUT = "switch",
    DIN = "binary_sensor",
    GPIO_DOUT = "switch",
    GPIO_DIN = "binary_sensor",
    DIMMER = "light",
    LEDRGB = "light",
    LED_CHANNEL = "light",
    ROLLER = "cover",
    ROLLER_SH = "cover",
    AnalogIN = "sensor",
    AnalogOUT = "number",
    ONE_WIRE = "sensor",
    PANELSENSTEMP = "sensor",
    PANELSENSLIGHT = "sensor",
    HUMIDITY_SENSOR = "sensor",
}

--- Get expose type from Grenton type
-- @param grentonType string The Grenton hardware type (e.g., "DOUT", "DIMMER")
-- @param default string Optional default value (default: "switch")
-- @return string The expose type
function TypeMap.getExposeType(grentonType, default)
    return TypeMap.grentonToExpose[grentonType] or default or "switch"
end

--- Get HA component from expose type
-- @param exposeType string The expose type (e.g., "switch", "dimmer")
-- @param default string Optional default value (default: "sensor")
-- @return string The HA component type
function TypeMap.getHAComponent(exposeType, default)
    return TypeMap.exposeToHA[exposeType] or default or "sensor"
end

--- Get HA component directly from Grenton type (legacy registry objects)
-- @param grentonType string The Grenton hardware type
-- @param default string Optional default value (default: "sensor")
-- @return string The HA component type
function TypeMap.getHAComponentFromGreton(grentonType, default)
    return TypeMap.grentonToHA[grentonType] or default or "sensor"
end

--- Check if an expose type exists
-- @param exposeType string The expose type to check
-- @return boolean True if valid
function TypeMap.isValidExposeType(exposeType)
    return TypeMap.exposeToHA[exposeType] ~= nil
end

--- Get all supported expose types
-- @return table Array of expose type names
function TypeMap.getExposeTypes()
    local types = {}
    for t in pairs(TypeMap.exposeToHA) do
        table.insert(types, t)
    end
    table.sort(types)
    return types
end

--- Get all supported HA components
-- @return table Array of unique HA component names
function TypeMap.getHAComponents()
    local seen = {}
    local components = {}
    for _, comp in pairs(TypeMap.exposeToHA) do
        if not seen[comp] then
            seen[comp] = true
            table.insert(components, comp)
        end
    end
    table.sort(components)
    return components
end

return TypeMap
