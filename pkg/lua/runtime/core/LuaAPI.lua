--- LuaAPI - Clean API for Go handlers to call
-- This module provides a clean Lua API for Go handlers, replacing inline Lua scripts in Go code.
-- It offers functions for managing remote CLU subscriptions, access control, MQTT testing,
-- and Home Assistant integration.
-- @module LuaAPI

LuaAPI = {}

--- Auto-subscribe to remote CLUs
-- Automatically subscribes to all remote CLU devices using the RemoteSubscription module.
-- Called from Go handler: handleAPIRegistrySubscribe
-- @return string "OK" on success, or error message if RemoteSubscription is not available
function LuaAPI.autoSubscribe()
    if RemoteSubscription and RemoteSubscription.autoSubscribe then
        RemoteSubscription.autoSubscribe()
        return "OK"
    else
        return "RemoteSubscription not available"
    end
end

--- Sync profiles and assignments from Go
-- Synchronizes profile-based access control data from Go to Lua state.
-- Called from Go function: syncProfilesToLua (during startup and on profile/assignment changes)
-- @param profilesMap table { ["name"] = { default = "full", objects = { ... } }, ... }
-- @param assignmentsMap table { ["mqtt"] = {"profile1"}, ... }
-- @return string Always returns "OK"
function LuaAPI.syncProfiles(profilesMap, assignmentsMap)
    if AccessControl and AccessControl.loadProfiles then
        AccessControl.loadProfiles(profilesMap, assignmentsMap)
    end
    return "OK"
end

--- Sync remote subscription config from Go
-- Pushes whitelist and settings from .vclu.json into RemoteSubscription.config.
-- Starts or stops the renew timer based on enabled flag.
-- Called from Go handler: handleAPISyncSave / syncRemoteConfigToLua
-- @param config table { enabled, ttl, batchSize, batchDelay, objects = {"path1", "path2"} }
-- @return string "OK" on success
function LuaAPI.syncRemoteConfig(config)
    if not RemoteSubscription then
        return "RemoteSubscription not available"
    end
    RemoteSubscription.config.enabled = config.enabled
    RemoteSubscription.config.ttl = config.ttl or 60
    RemoteSubscription.config.batchSize = config.batchSize or 4
    RemoteSubscription.config.batchDelay = config.batchDelay or 50
    -- Convert array to hash map for O(1) lookup
    local wl = {}
    if config.objects then
        for _, path in ipairs(config.objects) do wl[path] = true end
    end
    RemoteSubscription.config.whitelist = wl
    -- Manage renew timer
    if config.enabled then
        RemoteSubscription.startRenewTimer()
    else
        RemoteSubscription.stopRenewTimer()
    end
    return "OK"
end

--- Get remote subscription status
-- Returns sync status JSON from RemoteSubscription.
-- Called from Go handler: handleAPISyncStatus
-- @return string JSON-encoded status
function LuaAPI.getSyncStatus()
    if RemoteSubscription and RemoteSubscription.getStatus then
        return RemoteSubscription.getStatus()
    end
    return '{"subscriptions":0,"objects":0}'
end

--- Test MQTT publish
-- Tests publishing a message to an MQTT topic. Verifies that MQTT is connected and attempts
-- to publish the provided payload to the specified topic.
-- Called from Go handler: handleAPIMQTTTestPublish
-- @param topic string MQTT topic to publish to
-- @param payload string Message payload to publish
-- @return string "OK" on success, or "ERROR: reason" describing the failure
function LuaAPI.testMqttPublish(topic, payload)
    local mqtt = MQTT
    if mqtt and mqtt:isConnected() then
        local ok, err = mqtt:publish(topic, payload)
        if ok then
            return "OK"
        else
            return "ERROR: " .. tostring(err)
        end
    else
        return "ERROR: MQTT not connected"
    end
end

--- Setup Home Assistant integration
-- Initializes the Home Assistant integration by calling HAIntegration.setup with the MQTT instance.
-- This enables Home Assistant discovery and control of CLU objects.
-- Called from Go handler: handleAPIHACommandsEnable
-- @return string "OK" on success, "FAILED" if setup fails, or "FAILED: reason" if dependencies are missing
function LuaAPI.setupHAIntegration()
    if HAIntegration and HAIntegration.setup and MQTT then
        local ok = HAIntegration.setup(MQTT)
        return ok and "OK" or "FAILED"
    else
        return "FAILED: HAIntegration or MQTT not available"
    end
end

return LuaAPI
