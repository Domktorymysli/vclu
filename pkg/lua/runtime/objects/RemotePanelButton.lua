--- Remote Smart Panel button (class 30, PANEL_BUTTON).
-- Execute methods from Object Manager device-interfaces (SMART_PANEL_FM_4 fv07):
-- 1 LedSwitchOn, 2 LedSwitchOff, 3 SetLabel. Its events go to vCLU through the
-- panel's click(); see RemotePanel.
-- @module objects.RemotePanelButton
-- @inherits RemoteObject

RemotePanelButton = setmetatable({}, {__index = RemoteObject})
RemotePanelButton.__index = RemotePanelButton

RemotePanelButton.LED_ON = 1
RemotePanelButton.LED_OFF = 2
RemotePanelButton.SET_LABEL = 3

--- Creates a new RemotePanelButton instance.
-- @param clu CLU_REMOTE The CLU the panel is on
-- @param objId string Object id
-- @param name string Optional name from Object Manager
-- @return RemotePanelButton
function RemotePanelButton:new(clu, objId, name)
    local obj = RemoteObject.new(self, clu, objId, name)
    obj._type = "PANEL_BUTTON"
    obj._allowedMethods = {"ledOn", "ledOff", "setLabel"}
    return obj
end

--- The LED call as a script statement, for a panel frame.
-- @param on boolean
-- @return string
function RemotePanelButton:ledOp(on)
    return self._objId .. ":execute(" .. (on and RemotePanelButton.LED_ON or RemotePanelButton.LED_OFF) .. ")"
end

function RemotePanelButton:ledOn() self:execute(RemotePanelButton.LED_ON) end
function RemotePanelButton:ledOff() self:execute(RemotePanelButton.LED_OFF) end

--- Sets the button's label (up to 15 characters).
function RemotePanelButton:setLabel(text) self:execute(RemotePanelButton.SET_LABEL, tostring(text)) end
