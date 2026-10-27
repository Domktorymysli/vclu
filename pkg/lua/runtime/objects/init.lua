--- Objects module - loads all object classes.
-- Provides base classes for remote and virtual objects.
-- @module objects

-- Load remote base class first
require("RemoteObject")

-- Load remote derived classes (they depend on RemoteObject being global)
require("RemoteSwitch")
require("RemoteSensor")
require("RemoteLight")
require("RemoteRGBLight")  -- Depends on RemoteLight
require("RemoteLedChannel")  -- Depends on RemoteLight
require("RemoteCover")
require("RemoteNumber")
require("RemoteThermometer")
require("RemoteValueSensor")
require("RemoteThermostat")
require("RemotePanel")
require("RemotePanelButton")

-- Load virtual base class
require("VirtualObject")

-- Load virtual derived classes (they depend on VirtualObject being global)
require("VirtualSwitch")
require("VirtualDimmer")
require("VirtualCover")
require("VirtualSensor")
require("VirtualNumber")
require("VirtualText")
require("VirtualThermostat")

return {
    RemoteObject = RemoteObject,
    RemoteSwitch = RemoteSwitch,
    RemoteSensor = RemoteSensor,
    RemoteLight = RemoteLight,
    RemoteRGBLight = RemoteRGBLight,
    RemoteLedChannel = RemoteLedChannel,
    RemoteCover = RemoteCover,
    RemoteNumber = RemoteNumber,
    RemoteThermometer = RemoteThermometer,
    RemoteValueSensor = RemoteValueSensor,
    RemoteThermostat = RemoteThermostat,
    RemotePanel = RemotePanel,
    RemotePanelButton = RemotePanelButton,
    VirtualObject = VirtualObject,
    VirtualSwitch = VirtualSwitch,
    VirtualDimmer = VirtualDimmer,
    VirtualCover = VirtualCover,
    VirtualSensor = VirtualSensor,
    VirtualNumber = VirtualNumber,
    VirtualText = VirtualText,
    VirtualThermostat = VirtualThermostat,
}
