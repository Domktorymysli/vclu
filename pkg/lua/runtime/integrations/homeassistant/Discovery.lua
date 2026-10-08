--
-- Home Assistant Integration for vCLU
-- Wraps HACommands and HAStatePublisher setup
--

local HAIntegration = {}

HAIntegration.enabled = false

-- Setup full HA integration (commands + state publishing)
-- @param mqttInstance - MQTT client instance
-- @return success, error message
function HAIntegration.setup(mqttInstance)
    if not mqttInstance then
        return false, "MQTT instance required"
    end

    local errors = {}

    -- Setup command handler (HA -> vCLU)
    if HACommands then
        local ok = HACommands.setup(mqttInstance)
        if not ok then
            table.insert(errors, "HACommands setup failed")
        end
    else
        table.insert(errors, "HACommands not loaded")
    end

    -- Setup state publisher (vCLU -> HA)
    if HAStatePublisher then
        local ok = HAStatePublisher.setup(mqttInstance)
        if not ok then
            table.insert(errors, "HAStatePublisher setup failed")
        end
    else
        table.insert(errors, "HAStatePublisher not loaded")
    end

    if #errors == 0 then
        HAIntegration.enabled = true
        if Logger then
            Logger:getShared():info("[HA Integration] Enabled (commands + state publishing)")
        end
        return true, nil
    else
        return false, table.concat(errors, ", ")
    end
end

-- Check if integration is enabled
function HAIntegration.isEnabled()
    return HAIntegration.enabled
end

-- Disable integration
function HAIntegration.disable()
    if HAStatePublisher then
        HAStatePublisher.disable()
    end
    HAIntegration.enabled = false
    if Logger then
        Logger:getShared():info("[HA Integration] Disabled")
    end
end

return HAIntegration
