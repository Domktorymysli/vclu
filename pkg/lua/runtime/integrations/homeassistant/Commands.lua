--
-- Home Assistant Command Handler for vCLU
-- Handles incoming MQTT commands from HA and routes them to CLU devices
--

local HACommands = {}

-- Topic prefix (must match generator_ha.go)
HACommands.topicPrefix = "vclu"

-- Command handlers by component type
HACommands.handlers = {
    -- Switch: ON/OFF
    -- DOUT methods: execute(0)=Switch, execute(1,Time)=SwitchOn, execute(2,Time)=SwitchOff
    switch = function(obj, payload, subpath)
        if not obj then return false, "object not found" end

        if payload == "ON" then
            if obj.SwitchOn then
                obj:SwitchOn()
            elseif obj.execute then
                obj:execute(1, 0) -- SwitchOn(Time=0 unlimited)
            end
            return true
        elseif payload == "OFF" then
            if obj.SwitchOff then
                obj:SwitchOff()
            elseif obj.execute then
                obj:execute(2, 0) -- SwitchOff(Time=0 unlimited)
            end
            return true
        end
        return false, "unknown payload: " .. tostring(payload)
    end,

    -- Light (dimmer): ON/OFF or brightness
    -- DIMMER methods: execute(1,Time)=Switch, execute(2,Time)=SwitchOn, execute(3,Time)=SwitchOff
    light = function(obj, payload, subpath)
        if not obj then return false, "object not found" end

        -- Check if it's brightness command
        if subpath == "brightness/set" then
            local brightness = tonumber(payload)
            if brightness then
                if obj.SetValue then
                    obj:SetValue(brightness)
                elseif obj.set then
                    obj:set(0, brightness) -- Value property
                end
                return true
            end
            return false, "invalid brightness value"
        end

        -- ON/OFF command
        if payload == "ON" then
            if obj.SwitchOn then
                obj:SwitchOn()
            elseif obj.execute then
                obj:execute(2, 0) -- SwitchOn(Time=0)
            end
            return true
        elseif payload == "OFF" then
            if obj.SwitchOff then
                obj:SwitchOff()
            elseif obj.execute then
                obj:execute(3, 0) -- SwitchOff(Time=0)
            end
            return true
        end
        return false, "unknown payload: " .. tostring(payload)
    end,

    -- Cover (roller): OPEN/CLOSE/STOP or position
    -- ROLLER_SH methods: execute(0,Time)=Up, execute(1,Time)=Down, execute(2,Time)=Start, execute(3)=Stop
    cover = function(obj, payload, subpath)
        if not obj then return false, "object not found" end

        -- Check if it's position command
        if subpath == "position/set" then
            local position = tonumber(payload)
            if position then
                if obj.SetPosition then
                    obj:SetPosition(position)
                elseif obj.set then
                    obj:set(0, position)
                end
                return true
            end
            return false, "invalid position value"
        end

        -- OPEN/CLOSE/STOP command
        if payload == "OPEN" then
            if obj.Open then
                obj:Open()
            elseif obj.execute then
                obj:execute(0, 0) -- Up(Time=0 uses MaxTime)
            end
            return true
        elseif payload == "CLOSE" then
            if obj.Close then
                obj:Close()
            elseif obj.execute then
                obj:execute(1, 0) -- Down(Time=0 uses MaxTime)
            end
            return true
        elseif payload == "STOP" then
            if obj.Stop then
                obj:Stop()
            elseif obj.execute then
                obj:execute(3) -- Stop (no params)
            end
            return true
        end
        return false, "unknown payload: " .. tostring(payload)
    end,

    -- Number (analog out): numeric value
    number = function(obj, payload, subpath)
        if not obj then return false, "object not found" end

        local value = tonumber(payload)
        if value then
            if obj.SetValue then
                obj:SetValue(value)
            elseif obj.set then
                obj:set(0, value)
            end
            return true
        end
        return false, "invalid number value"
    end,
}

-- Parse topic and extract components
-- Topic format: vclu/{clu_id}/{component}/{object_id}/set
-- or: vclu/{clu_id}/{component}/{object_id}/{subpath}/set
function HACommands.parseTopic(topic)
    local prefix, cluId, component, objectId, rest = topic:match("^([^/]+)/([^/]+)/([^/]+)/([^/]+)/?(.*)")

    if not prefix or prefix ~= HACommands.topicPrefix then
        return nil
    end

    -- Check if rest ends with /set
    local subpath = nil
    if rest == "set" then
        -- Simple: vclu/clu.../switch/dou.../set
        subpath = nil
    elseif rest:match("/set$") then
        -- Complex: vclu/clu.../light/dim.../brightness/set
        subpath = rest:gsub("/set$", "")
    elseif rest ~= "" then
        -- Not a command topic
        return nil
    end

    return {
        cluId = cluId,
        component = component,
        objectId = objectId,
        subpath = subpath,
    }
end

-- Find object in registry.
--
-- Returns (object, matchedPath). matchedPath is the registry key the object was
-- actually found under, and it is what authorization MUST be checked against —
-- never a path rebuilt from the topic. The topic's cluId segment plays no part in
-- three of the four lookups, so a rebuilt path can name an object that was never
-- consulted, miss the access table, and fall through to the profile default.
--
-- The Lua global table is deliberately NOT searched. om.lua defines every object
-- as a global (DOU1234._type = ...) as well as registering it, so a global lookup
-- resolves real devices under an identity the access rules do not know. An object
-- that cannot be named cannot be authorized.
function HACommands.findObject(cluId, objectId)
    local paths = {
        string.upper(cluId) .. "." .. string.upper(objectId),
        cluId .. "." .. objectId,
        string.upper(objectId),
        objectId,
    }

    for _, path in ipairs(paths) do
        if _registry and _registry[path] then
            return _registry[path], path
        end
    end

    return nil, nil
end

-- Handle incoming MQTT message
function HACommands.handleMessage(topic, payload)
    local parsed = HACommands.parseTopic(topic)
    if not parsed then
        return false, "not a command topic"
    end

    -- Find handler for component type
    local handler = HACommands.handlers[parsed.component]
    if not handler then
        if Logger then
            Logger:warn("[HA] Unknown component type: %s", parsed.component)
        end
        return false, "unknown component: " .. parsed.component
    end

    -- Find object
    local obj, objPath = HACommands.findObject(parsed.cluId, parsed.objectId)
    if not obj then
        if Logger then
            Logger:warn("[HA] Object not found: %s.%s", parsed.cluId, parsed.objectId)
        end
        return false, "object not found: " .. parsed.cluId .. "." .. parsed.objectId
    end

    -- Check MQTT access - only allow commands for "full" access objects.
    --
    -- objPath comes from findObject, i.e. the key the object was really found
    -- under. It must never be rebuilt from the topic: the cluId segment is
    -- attacker-controlled and is ignored by most of the lookups, so a rebuilt path
    -- could authorize a path nobody uses while operating a different device.
    if not objPath then
        if Logger then
            Logger:getShared():warn("[HA] Command blocked (object has no registry path): %s.%s",
                parsed.cluId, parsed.objectId)
        end
        return false, "access denied: object has no registry path"
    end

    if MqttAccess and MqttAccess.canControl and not MqttAccess.canControl(objPath) then
        if Logger then
            Logger:getShared():warn("[HA] Command blocked (access denied): %s", objPath)
        end
        return false, "access denied: " .. objPath
    end

    -- Execute handler
    local ok, err = handler(obj, payload, parsed.subpath)

    if ok then
        if Logger then
            Logger:info("[HA] Command executed: %s/%s -> %s", parsed.component, parsed.objectId, payload)
        end
    else
        if Logger then
            Logger:error("[HA] Command failed: %s", tostring(err))
        end
    end

    return ok, err
end

-- Parse exposed object topic
-- Topic format: vclu/exposed/{base}/{component}/{id}/set
-- or: vclu/exposed/{base}/{component}/{id}/{subpath}/set
function HACommands.parseExposedTopic(topic)
    local prefix, exposed, base, component, objectId, rest = topic:match("^([^/]+)/([^/]+)/([^/]+)/([^/]+)/([^/]+)/?(.*)")

    if not prefix or prefix ~= HACommands.topicPrefix or exposed ~= "exposed" then
        return nil
    end

    -- Check if rest ends with /set
    local subpath = nil
    if rest == "set" then
        subpath = nil
    elseif rest:match("/set$") then
        subpath = rest:gsub("/set$", "")
    elseif rest ~= "" then
        return nil
    end

    return {
        base = base,
        component = component,
        objectId = objectId,
        subpath = subpath,
    }
end

-- Handle exposed object command
function HACommands.handleExposedMessage(topic, payload)
    local parsed = HACommands.parseExposedTopic(topic)
    if not parsed then
        return false, "not an exposed command topic"
    end

    if Logger then
        Logger:getShared():debug("[HA] Exposed command: %s -> %s", topic, payload)
    end

    -- Find ExposedObjects handle by searching all exposed objects
    if not ExposedObjects then
        return false, "ExposedObjects not available"
    end

    local handle = nil
    local targetPath = nil

    -- Search for matching exposed object
    for path, h in pairs(ExposedObjects:list()) do
        -- Extract base and id from path
        local pathBase, pathId = path:match("^([^:]+):(.+)$")
        if not pathBase then
            pathBase = "vclu"
            pathId = path
        end

        -- Normalize for comparison
        local normalizedBase = string.lower(pathBase):gsub("[^%w]", "_")
        local normalizedId = string.lower(pathId):gsub("[^%w]", "_")

        if normalizedBase == parsed.base and normalizedId == parsed.objectId then
            handle = h
            targetPath = path
            break
        end
    end

    if not handle then
        if Logger then
            Logger:getShared():warn("[HA] Exposed object not found: %s/%s", parsed.base, parsed.objectId)
        end
        return false, "exposed object not found"
    end

    -- Check if readonly.
    --
    -- TWO independent sources of "readonly" and BOTH must be honoured:
    --   1. options.readonly — set in Lua via expose(..., {readonly=true}) / :readonly()
    --   2. AccessControl   — the "Readonly" level set per object in the dashboard,
    --                        read here through MqttAccess.canControl()
    -- Checking only (1) meant an object marked Readonly in the UI stayed fully
    -- controllable from Home Assistant.
    --
    -- This gate lives on the command path deliberately. Discovery merely advertises
    -- what HA should show; it cannot protect anything, because a retained discovery
    -- message, a stale HA entity, or anyone publishing straight to the topic all
    -- bypass it. Refusing here is what actually stops the write.
    if handle.options and handle.options.readonly then
        if Logger then
            Logger:getShared():warn("[HA] Exposed object is readonly: %s", targetPath)
        end
        return false, "object is readonly"
    end

    if MqttAccess and MqttAccess.canControl and not MqttAccess.canControl(targetPath) then
        if Logger then
            Logger:getShared():warn("[HA] Command blocked (access denied): %s", targetPath)
        end
        return false, "access denied: " .. targetPath
    end

    -- Handle command based on type and payload
    local exposedType = handle.type

    if exposedType == "switch" then
        if payload == "ON" then
            handle:setValue(1)
            return true
        elseif payload == "OFF" then
            handle:setValue(0)
            return true
        end
    elseif (exposedType == "light" or exposedType == "dimmer") and not ExposedObjects.hasBrightness(handle) then
        -- on/off light (DOUT): 1/0, a brightness write is refused
        if parsed.subpath == "brightness" then
            return false, "light has no brightness"
        elseif payload == "ON" then
            handle:setValue(1)
            return true
        elseif payload == "OFF" then
            handle:setValue(0)
            return true
        end
    elseif exposedType == "light" or exposedType == "dimmer" then
        -- value is 0..1, HA sends brightness as 0..100 (brightness_scale)
        local obj = handle.object
        if parsed.subpath == "brightness" then
            local pct = tonumber(payload)
            if pct then
                pct = math.max(0, math.min(100, pct))
                if obj.setBrightness then obj:setBrightness(pct) else handle:setValue(pct / 100) end
                handle._haBrightnessAt = os.time()
                return true
            end
        elseif payload == "ON" then
            -- HA sends ON right after a brightness change: keep that brightness
            local at = handle._haBrightnessAt
            handle._haBrightnessAt = nil
            if at and os.time() - at <= 2 then return true end
            if obj.switchOn then obj:switchOn() else handle:setValue(1) end
            return true
        elseif payload == "OFF" then
            handle._haBrightnessAt = nil
            if obj.switchOff then obj:switchOff() else handle:setValue(0) end
            return true
        end
    elseif exposedType == "cover" then
        if parsed.subpath == "position" then
            local position = tonumber(payload)
            if position then
                handle:setValue(position)
                return true
            end
        elseif payload == "OPEN" then
            handle:setValue(100)
            return true
        elseif payload == "CLOSE" then
            handle:setValue(0)
            return true
        elseif payload == "STOP" then
            -- For stop, try execute if available
            if handle.object and handle.object.execute then
                handle.object:execute(3)
            end
            return true
        end
    elseif exposedType == "number" then
        local value = tonumber(payload)
        if value then
            handle:setValue(value)
            return true
        end
    elseif exposedType == "scene" then
        if payload == "ON" then
            handle:executeScene()
            return true
        end
    end

    if Logger then
        Logger:getShared():info("[HA] Exposed command executed: %s -> %s", targetPath, payload)
    end

    return true
end

-- Setup HA command handler on MQTT instance
function HACommands.setup(mqttInstance)
    if not mqttInstance then
        if Logger then
            Logger:error("[HA] No MQTT instance provided")
        end
        return false
    end

    -- Subscribe to legacy registry command topics
    local pattern = HACommands.topicPrefix .. "/+/+/+/set"
    local patternBrightness = HACommands.topicPrefix .. "/+/+/+/brightness/set"
    local patternPosition = HACommands.topicPrefix .. "/+/+/+/position/set"

    if Logger then
        Logger:getShared():info("[HA] Subscribing to command topics: %s", pattern)
    end

    -- Subscribe with callback (legacy registry objects)
    mqttInstance:subscribe(pattern, function(topic, payload)
        HACommands.handleMessage(topic, payload)
    end)

    mqttInstance:subscribe(patternBrightness, function(topic, payload)
        HACommands.handleMessage(topic, payload)
    end)

    mqttInstance:subscribe(patternPosition, function(topic, payload)
        HACommands.handleMessage(topic, payload)
    end)

    -- Subscribe to exposed object command topics
    local expPattern = HACommands.topicPrefix .. "/exposed/+/+/+/set"
    local expBrightness = HACommands.topicPrefix .. "/exposed/+/+/+/brightness/set"
    local expPosition = HACommands.topicPrefix .. "/exposed/+/+/+/position/set"

    if Logger then
        Logger:getShared():info("[HA] Subscribing to exposed command topics: %s", expPattern)
    end

    mqttInstance:subscribe(expPattern, function(topic, payload)
        HACommands.handleExposedMessage(topic, payload)
    end)

    mqttInstance:subscribe(expBrightness, function(topic, payload)
        HACommands.handleExposedMessage(topic, payload)
    end)

    mqttInstance:subscribe(expPosition, function(topic, payload)
        HACommands.handleExposedMessage(topic, payload)
    end)

    if Logger then
        Logger:getShared():info("[HA] Command handler setup complete")
    end

    return true
end

-- Commands on HA topics are Home Assistant acting (the MQTT client wraps
-- every message as service:mqtt; this narrows the actor)
for _, name in ipairs({ "handleMessage", "handleExposedMessage" }) do
    local handle = HACommands[name]
    HACommands[name] = function(topic, payload)
        if not StateContext then return handle(topic, payload) end
        local outer = StateContext.current()
        return StateContext.with({
            origin = "mqtt",
            actor = "service:home-assistant",
            correlationId = (outer and outer.correlationId) or StateContext.newCorrelationId(),
        }, handle, topic, payload)
    end
end

return HACommands
