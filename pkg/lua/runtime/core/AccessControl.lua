--
-- AccessControl - Unified Access Control for all integrations
-- Controls which objects are exposed to various integrations (MQTT, HomeKit, etc.)
--
-- Integrations:
--   "mqtt"    - MQTT/Home Assistant
--   "homekit" - Apple HomeKit
--
-- Access levels:
--   "full"     - publish states + handle commands (default)
--   "readonly" - publish states only, ignore commands
--   "hidden"   - no interaction at all
--

AccessControl = {}

-- Internal storage for access levels
-- Format: { ["CLU.DOU1"] = { mqtt = "full", homekit = "hidden" }, ... }
AccessControl._access = {}

-- Default access level
AccessControl.DEFAULT = "full"

-- Per-integration default overrides (set from Go config, e.g., AccessControl.DEFAULT_MQTT = "readonly")
AccessControl.DEFAULT_MQTT = nil

-- Available integrations
AccessControl.INTEGRATIONS = { "mqtt", "homekit" }

--- Load access levels from Go (called during init/sync)
-- @param accessMap table A nested table: { ["CLU.DOU1"] = { mqtt = "full", homekit = "hidden" }, ... }
function AccessControl.load(accessMap)
    AccessControl._access = accessMap or {}

    local count = 0
    for _ in pairs(AccessControl._access) do
        count = count + 1
    end

    Logger:getShared():info("[AccessControl] Loaded access rules for %d objects", count)
end

--- Get access level for an integration and object path
-- @param integration string The integration to check ("mqtt", "homekit")
-- @param objectPath string The object path to query
-- @return string The access level ("full", "readonly", or "hidden")
function AccessControl.get(integration, objectPath)
    -- 1. Legacy explicit per-object access wins if present (back-compat).
    local obj = AccessControl._access[objectPath]
    if obj and obj[integration] then
        return obj[integration]
    end

    -- 2. Profile-based resolution. The web Access UI stores per-object access as
    --    profiles (e.g. a "custom-mqtt" profile with per-object overrides) and
    --    assigns them to the integration. Without consulting them here, those
    --    settings would have no effect on isHidden()/canControl() and thus on
    --    exposeRegistry()/HA discovery.
    local assigned = AccessControl._assignments[integration]
    if assigned and #assigned > 0 then
        return AccessControl.resolveAccess(assigned, objectPath)
    end

    -- 3. Per-integration default override, then global default.
    if integration == "mqtt" and AccessControl.DEFAULT_MQTT then
        return AccessControl.DEFAULT_MQTT
    end
    return AccessControl.DEFAULT
end

--- Set access level for an integration and object path
-- @param integration string The integration to configure ("mqtt", "homekit")
-- @param objectPath string The object path to configure
-- @param level string The access level to set ("full", "readonly", or "hidden")
function AccessControl.set(integration, objectPath, level)
    if not AccessControl._access[objectPath] then
        AccessControl._access[objectPath] = {}
    end

    if level == AccessControl.DEFAULT or level == "full" then
        AccessControl._access[objectPath][integration] = nil
        -- Clean up empty entries
        local hasAny = false
        for _ in pairs(AccessControl._access[objectPath]) do
            hasAny = true
            break
        end
        if not hasAny then
            AccessControl._access[objectPath] = nil
        end
    else
        AccessControl._access[objectPath][integration] = level
    end

    Logger:getShared():debug("[AccessControl] Set %s.%s = %s", integration, objectPath, level or "full")
end

--- Check if object can be published/visible in integration
-- @param integration string The integration to check
-- @param objectPath string The object path to check
-- @return boolean True if the object can be exposed to the integration
function AccessControl.canPublish(integration, objectPath)
    return AccessControl.get(integration, objectPath) ~= "hidden"
end

--- Check if object can receive commands from integration
-- @param integration string The integration to check
-- @param objectPath string The object path to check
-- @return boolean True if the object has "full" access
function AccessControl.canControl(integration, objectPath)
    return AccessControl.get(integration, objectPath) == "full"
end

--- Check if object is hidden from integration
-- @param integration string The integration to check
-- @param objectPath string The object path to check
-- @return boolean True if the object has "hidden" access level
function AccessControl.isHidden(integration, objectPath)
    return AccessControl.get(integration, objectPath) == "hidden"
end

--- Check if object is readonly in integration
-- @param integration string The integration to check
-- @param objectPath string The object path to check
-- @return boolean True if the object has "readonly" access level
function AccessControl.isReadonly(integration, objectPath)
    return AccessControl.get(integration, objectPath) == "readonly"
end

--- Get all access levels as table (for debugging/export)
-- @return table The full access map
function AccessControl.getAll()
    return AccessControl._access
end

--- Get access levels for a specific integration
-- @param integration string The integration to get
-- @return table A table mapping object paths to their access levels for this integration
function AccessControl.getAllForIntegration(integration)
    local result = {}
    for path, obj in pairs(AccessControl._access) do
        if obj[integration] then
            result[path] = obj[integration]
        end
    end
    return result
end

-- === Profile-based access control ===

-- Internal storage for profiles and assignments
-- Format: { ["profile-name"] = { default = "full", objects = { ["CLU.DOU1"] = "hidden" } } }
AccessControl._profiles = {}

-- Format: { ["mqtt"] = {"full-access"}, ["mcp"] = {"sensors-only", "lights"} }
AccessControl._assignments = {}

-- Level ordering for "most permissive wins"
local _levelOrder = { hidden = 0, readonly = 1, full = 2 }

--- Load profiles from Go (called during init/sync)
-- @param profilesMap table { ["name"] = { default = "full", objects = { ... } }, ... }
-- @param assignmentsMap table { ["mqtt"] = {"profile1", "profile2"}, ... }
function AccessControl.loadProfiles(profilesMap, assignmentsMap)
    AccessControl._profiles = profilesMap or {}
    AccessControl._assignments = assignmentsMap or {}

    local count = 0
    for _ in pairs(AccessControl._profiles) do
        count = count + 1
    end

    Logger:getShared():info("[AccessControl] Loaded %d profile(s)", count)
end

--- Resolve effective access for a list of profile names and an object path
-- Most permissive wins across all profiles.
-- @param profileNames table List of profile names (e.g., {"sensors", "lights"})
-- @param path string Object path
-- @return string The effective access level ("full", "readonly", or "hidden")
function AccessControl.resolveAccess(profileNames, path)
    if not profileNames or #profileNames == 0 then
        return "hidden"
    end

    local best = "hidden"
    local bestOrder = 0

    for _, name in ipairs(profileNames) do
        local p = AccessControl._profiles[name]
        if p then
            local level
            if p.objects and p.objects[path] then
                level = p.objects[path]
            else
                level = p["default"] or "full"
            end

            local ord = _levelOrder[level] or 2
            if ord > bestOrder then
                best = level
                bestOrder = ord
            end

            -- Short-circuit: can't be more permissive than full
            if best == "full" then
                return "full"
            end
        end
    end

    return best
end

--- Get the profiles assigned to an integration
-- @param integration string The integration name
-- @return table List of profile names
function AccessControl.getAssignedProfiles(integration)
    return AccessControl._assignments[integration] or {}
end

return AccessControl
