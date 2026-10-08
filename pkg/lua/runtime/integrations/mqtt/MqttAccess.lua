--
-- MqttAccess - MQTT Access Control wrapper
-- Delegates to unified AccessControl module for MQTT integration
--
-- Access levels:
--   "full"     - publish states + handle commands (default)
--   "readonly" - publish states only, ignore commands
--   "hidden"   - no MQTT interaction at all
--

MqttAccess = {}

-- Integration name for AccessControl
local INTEGRATION = "mqtt"

-- Default access level
MqttAccess.DEFAULT = "full"

--- Get access level for an object path
-- @param objectPath string The object path to query
-- @return string The access level ("full", "readonly", or "hidden")
function MqttAccess.get(objectPath)
    return AccessControl.get(INTEGRATION, objectPath)
end

--- Set access level for an object path
-- @param objectPath string The object path to configure
-- @param accessLevel string The access level to set
function MqttAccess.set(objectPath, accessLevel)
    AccessControl.set(INTEGRATION, objectPath, accessLevel)
end

--- Check if object can be published to MQTT
-- @param objectPath string The object path to check
-- @return boolean True if the object can be published to MQTT
function MqttAccess.canPublish(objectPath)
    return AccessControl.canPublish(INTEGRATION, objectPath)
end

--- Check if object can receive commands from MQTT
-- @param objectPath string The object path to check
-- @return boolean True if the object has "full" access
function MqttAccess.canControl(objectPath)
    return AccessControl.canControl(INTEGRATION, objectPath)
end

--- Check if object is hidden from MQTT
-- @param objectPath string The object path to check
-- @return boolean True if the object has "hidden" access level
function MqttAccess.isHidden(objectPath)
    return AccessControl.isHidden(INTEGRATION, objectPath)
end

--- Check if object is readonly
-- @param objectPath string The object path to check
-- @return boolean True if the object has "readonly" access level
function MqttAccess.isReadonly(objectPath)
    return AccessControl.isReadonly(INTEGRATION, objectPath)
end

--- Get count of non-default access rules
-- @return number The number of objects with MQTT access rules
function MqttAccess.count()
    local count = 0
    local allAccess = AccessControl.getAllForIntegration(INTEGRATION)
    for _ in pairs(allAccess) do
        count = count + 1
    end
    return count
end

--- Get all access levels as table
-- @return table A table mapping object paths to their access levels
function MqttAccess.getAll()
    return AccessControl.getAllForIntegration(INTEGRATION)
end

return MqttAccess
