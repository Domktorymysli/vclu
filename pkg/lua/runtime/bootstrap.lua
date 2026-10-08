--
-- Virtual CLU - Lua Runtime Bootstrap
-- Loads all necessary modules for CLU emulation
--

-- Set up package path for our modules
local runtime_path = _VCLU_RUNTIME_PATH or "./pkg/lua/runtime"
package.path = runtime_path .. "/?.lua;" ..
               runtime_path .. "/utils/?.lua;" ..
               runtime_path .. "/modules/?.lua;" ..
               runtime_path .. "/modules/?/?.lua;" ..
               package.path

-- Load utilities first
Logger = require("Logger")
EventBus = require("EventBus")
EventBusRecorder = require("EventBusRecorder")
ModuleData = require("ModuleData")
Module = require("Module")

-- Load module implementations
CLU = require("modules.CLU.CLU")
Timer = require("modules.Timer.Timer")
DIN = require("modules.DIN.DIN")
DOUT = require("modules.DOUT.DOUT")
BUTTON = require("modules.BUTTON.BUTTON")
PANEL_BUTTON = require("modules.PANEL_BUTTON.PANEL_BUTTON")
HTTPRequest = require("modules.HTTP_REQUEST.HTTPRequest")
UNDEFINED = require("modules.UNDEFINED")

-- Load factories
OBJECT = require("OBJECT")
GATE = require("GATE")

-- Load system
SYSTEM = require("SYSTEM")
system = SYSTEM:getShared()

-- Global checkAlive function (used by Object Manager)
function checkAlive()
    return "true"
end

return {
    SYSTEM = SYSTEM,
    system = system,
    CLU = CLU,
    GATE = GATE,
    OBJECT = OBJECT
}
