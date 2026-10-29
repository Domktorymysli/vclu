--
-- Virtual CLU - Lua Runtime Bootstrap for Go
-- Loads all necessary modules for CLU emulation (without JavaScript dependencies)
--

-- Set up package path for our modules
local runtime_path = _VCLU_RUNTIME_PATH or "./pkg/lua/runtime"
package.path = runtime_path .. "/?.lua;" ..
               runtime_path .. "/utils/?.lua;" ..
               runtime_path .. "/core/?.lua;" ..
               runtime_path .. "/grenton/?.lua;" ..
               runtime_path .. "/grenton/modules/?.lua;" ..
               runtime_path .. "/grenton/factories/?.lua;" ..
               runtime_path .. "/grenton/protocol/?.lua;" ..
               runtime_path .. "/integrations/http/?.lua;" ..
               runtime_path .. "/integrations/mqtt/?.lua;" ..
               runtime_path .. "/integrations/modbus/?.lua;" ..
               runtime_path .. "/integrations/gpio/?.lua;" ..
               runtime_path .. "/integrations/satel/?.lua;" ..
               runtime_path .. "/integrations/mcp/?.lua;" ..
               runtime_path .. "/integrations/homeassistant/?.lua;" ..
               runtime_path .. "/integrations/remote/?.lua;" ..
               runtime_path .. "/objects/?.lua;" ..
               package.path

-- LAYER 1: Utils (no dependencies)
Logger = require("LoggerGo")
EventBus = require("EventBusGo")
StateBus = require("StateBus")
StateChange = require("StateChange")
StateContext = require("StateContext")
Persist = require("Persist")
JSON = require("JSON")
ModuleData = require("ModuleData")
Module = require("Module")

-- LAYER 2: OM Modules (CLU, Timer, I/O, etc.)
GenericButton = require("GenericButton")
CLU = require("CLU")
CLU_REMOTE = require("CLU_REMOTE")
Timer = require("Timer")
DIN = require("DIN")
DOUT = require("DOUT")
ANALOG_IN = require("ANALOG_IN")
ANALOG_OUT = require("ANALOG_OUT")
UNDEFINED = require("UNDEFINED")

-- LAYER 3: Exposed Objects API
ExposedObjects = require("ExposedObjects")

-- LAYER 4: Access Control (unified)
AccessControl = require("AccessControl")

-- LAYER 5: Integrations (HTTP, MQTT, HA)
HTTPRequest = require("HTTPRequest")
HTTP = require("HttpClient")
HTTPServer = require("HTTPServer")
HTTPListener = require("HTTPListener")
MQTT = require("MqttClient")
Modbus = require("Modbus")
MqttHandler = require("MqttHandler")
MqttAccess = require("MqttAccess")
HACommands = require("Commands")
HAStatePublisher = require("StatePublisher")
HAIntegration = require("Discovery")

-- LAYER 5a: Remote access (exposed objects through the "remote" profile)
RemoteAccess = require("RemoteAccess")

-- LAYER 5b: MCP (Model Context Protocol)
MCP = require("MCP")

-- LAYER 5c: GPIO
gpio = require("GPIO")
GPIO_DOUT = require("GPIO_DOUT")
GPIO_DIN = require("GPIO_DIN")

-- Start GPIO change polling (50ms interval)
if __go_gpio_poll then
    EventBus:getShared():setInterval(function()
        gpio.pollChanges()
    end, 50)
end

-- LAYER 6: Protocol (Remote subscriptions)
RemoteSubscription = require("RemoteSubscription")

-- LAYER 7: Core (LuaAPI for Go handlers)
LuaAPI = require("LuaAPI")
PluginSandbox = require("PluginSandbox")
Plugin = require("PluginAPI")

-- LAYER 7b: Dashboards
Dashboard = require("Dashboard")
Calendar = require("Calendar")

-- LAYER 7c: Metrics
Metrics = require("Metrics")

-- LAYER 7d: Camera snapshot proxy
Camera = require("Camera")

-- LAYER 8: Factories (OM compatibility)
OBJECT = require("ObjectFactory")
GATE = require("GateFactory")

-- LAYER 9: Objects (RemoteObject hierarchy)
require("objects.init")

-- LAYER 10: Core (Registry, RemoteGroup)
require("RemoteGroup")
require("Registry")

-- LAYER 10b: Satel Integra (objects need Virtual*, setup needs Registry)
Satel = require("Satel")
SatelZone = require("SatelZone")
SatelOutput = require("SatelOutput")
SatelPartition = require("SatelPartition")
SatelThermometer = require("SatelThermometer")
SandboxObjects = require("SandboxObjects")

-- Create Satel objects + start event polling (100ms). On first init the
-- manager is injected later (SetSatelManager starts this); this guard covers
-- Reinitialize, where __go_satel_* functions are already registered.
if __go_satel_poll_event then
    Satel.setup()
    EventBus:getShared():setInterval(function()
        Satel.pollEvents()
    end, 100)
end

-- LAYER 11: Helpers (requires RemoteObject, RemoteGroup)
require("Helpers")

-- Load system
SYSTEM = require("System")
system = SYSTEM:getShared()

-- Global checkAlive function (used by Object Manager)
function checkAlive()
    return "true"
end

-- ============================================
-- HomeKit Integration Hook
-- ============================================
-- Listen for state changes and notify Go HomeKit server
StateBus:getShared():on("state_changed", function(data)
    -- Only call if Go handler is registered
    if __go_homekit_notify then
        local value = data.value
        -- Convert to number if possible
        if type(value) == "boolean" then
            value = value and 1 or 0
        elseif type(value) ~= "number" then
            value = tonumber(value) or 0
        end
        __go_homekit_notify(data.path, value, data.type or "")
    end
end)

-- A colour change without a value change (OnColorChange) reaches HomeKit too
StateBus:getShared():on("object_exposed", function(data)
    local h = ExposedObjects:get(data.path)
    local o = h and h.object
    if not h or not ExposedObjects.hasColor(h) or o._hkColorHook or type(o.onEvent) ~= "function" then
        return
    end
    o._hkColorHook = true
    o:onEvent("OnColorChange", function()
        local path = StateChange.pathOf(o)
        local cur = path and ExposedObjects:get(path)
        if __go_homekit_notify and cur and cur.options.homekit ~= false then
            __go_homekit_notify(path, tonumber(cur:getValue()) or 0, cur.type)
        end
    end)
end)

-- ============================================
-- Client Push Notification Hook
-- ============================================
-- Signal Go to send immediate clientReport when any state changes
StateBus:getShared():on("state_changed", function(data)
    if __go_client_push_notify then
        __go_client_push_notify()
    end
end)

return {
    SYSTEM = SYSTEM,
    system = system,
    CLU = CLU,
    GATE = GATE,
    OBJECT = OBJECT,
    Plugin = Plugin,
    PluginSandbox = PluginSandbox,
    HTTPServer = HTTPServer,
    HTTPListener = HTTPListener
}