--
-- Home Assistant Command Handler for vCLU
-- Handles incoming MQTT commands from HA for objects shared with expose()
--

local HACommands = {}

-- Topic prefix of every vCLU MQTT topic
HACommands.topicPrefix = "vclu"

--- A JSON light command (schema json, RGB light from the OM import):
-- {"state":"ON"|"OFF","brightness":0..100,"color":{"r","g","b"[,"w"]} or {"h","s"}}.
-- Brightness is on the 0..100 scale of brightness_scale; hs saturation is 0..100.
-- @param obj table The light
-- @param payload string JSON
-- @return boolean, string|nil
function HACommands.lightJSON(obj, payload)
    local ok, cmd = pcall(JSON.decode, payload)
    if not ok or type(cmd) ~= "table" then
        return false, "invalid JSON command"
    end
    if cmd.state == "OFF" then
        if obj.switchOff then obj:switchOff() end
        return true
    end
    local changed = false
    local c = cmd.color
    if type(c) == "table" then
        local r, g, b = tonumber(c.r), tonumber(c.g), tonumber(c.b)
        local h, sat = tonumber(c.h), tonumber(c.s)
        local function byte(v) return math.floor(math.max(0, math.min(255, v)) + 0.5) end
        if r and g and b and obj.setColor then
            obj:setColor(byte(r), byte(g), byte(b))
            changed = true
            -- an RGBW light is announced with rgbw: white comes with the colour
            local w = tonumber(c.w)
            if w and obj.setWhite then obj:setWhite(byte(w)) end
        elseif h and sat and obj.setHue and obj.setSaturation then
            obj:setHue(math.max(0, math.min(360, h)))
            obj:setSaturation(math.max(0, math.min(100, sat)) / 100)
            changed = true
        end
    end
    local brightness = tonumber(cmd.brightness)
    if brightness and obj.setBrightness then
        obj:setBrightness(math.max(0, math.min(100, brightness)))
        changed = true
    end
    -- ON alone switches on; so does ON with a colour on a light that is off (HA sends
    -- that when a colour is picked on an off light; SetRGBvalue alone may not light it)
    if cmd.state == "ON" and not brightness and obj.switchOn then
        local v = tonumber(obj.getValue and obj:getValue())
        if not changed or not v or v <= 0 then obj:switchOn() end
    end
    return true
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

    -- expose(..., {mqtt = false}) keeps the object off MQTT: commands too, not only discovery
    if handle.options and handle.options.mqtt == false then
        if Logger then
            Logger:getShared():warn("[HA] Exposed object is not on MQTT: %s", targetPath)
        end
        return false, "object is not on MQTT"
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

    return HACommands.applyExposed(handle, parsed.subpath, payload)
end

--- Carries out a command on an exposed object, after its access was checked.
-- Remote access reuses it (RemoteAccess.lua).
-- @param handle ExposedHandle
-- @param subpath string|nil "brightness", "position", "tilt", "target_temp" or nil
-- @param payload string HA payload ("ON", "OPEN", "42", JSON for a colour light)
-- @return boolean, string|nil
function HACommands.applyExposed(handle, subpath, payload)
    local targetPath = handle.path
    local parsed = { subpath = subpath }

    -- Handle command based on type and payload
    local exposedType = handle.type

    -- target_temp/set is subscribed for a climate only; no other type takes it
    if parsed.subpath == "target_temp" and exposedType ~= "climate" then
        return false, "only a climate takes target_temp"
    end
    if parsed.subpath == "tilt" and exposedType ~= "cover" then
        return false, "only a cover takes tilt"
    end

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
        -- a colour light is announced with schema json: one JSON command
        if type(payload) == "string" and payload:sub(1, 1) == "{" then
            return HACommands.lightJSON(obj, payload)
        end
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
        -- The object's own methods move a roller; setValue would write its feature 0
        local obj = handle.object
        if parsed.subpath == "position" then
            if not HAStatePublisher.coverHasPosition(handle) then
                return false, "cover has no position"
            end
            local position = HAStatePublisher.coverPositionFromHA(payload)
            if not position then
                return false, "invalid position value"
            end
            if obj.setPosition then obj:setPosition(position) else handle:setValue(position) end
            return true
        elseif parsed.subpath == "tilt" then
            if not HAStatePublisher.coverHasTilt(handle) then
                return false, "cover has no tilt"
            end
            local degrees = HAStatePublisher.coverTiltFromHA(payload)
            if not degrees then
                return false, "invalid tilt value"
            end
            obj:setTilt(degrees)
            return true
        elseif payload == "OPEN" then
            if obj.open then obj:open() else handle:setValue(100) end
            return true
        elseif payload == "CLOSE" then
            if obj.close then obj:close() else handle:setValue(0) end
            return true
        elseif payload == "STOP" then
            if obj.stop then obj:stop() elseif obj.execute then obj:execute(3) end
            return true
        end
    elseif exposedType == "climate" then
        -- only a target (setValue clamps it and calls setSetpoint); the mode is not set
        if parsed.subpath ~= "target_temp" then
            return false, "climate takes only target_temp"
        end
        if not tonumber(payload) then
            return false, "invalid temperature value"
        end
        if not handle:setValue(payload) then
            return false, "target not set"
        end
        return true
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

    -- Subscribe to exposed object command topics
    local expPattern = HACommands.topicPrefix .. "/exposed/+/+/+/set"
    local expBrightness = HACommands.topicPrefix .. "/exposed/+/+/+/brightness/set"
    local expPosition = HACommands.topicPrefix .. "/exposed/+/+/+/position/set"
    local expTilt = HACommands.topicPrefix .. "/exposed/+/+/+/tilt/set"
    local expTarget = HACommands.topicPrefix .. "/exposed/+/+/+/target_temp/set"

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

    mqttInstance:subscribe(expTilt, function(topic, payload)
        HACommands.handleExposedMessage(topic, payload)
    end)

    mqttInstance:subscribe(expTarget, function(topic, payload)
        HACommands.handleExposedMessage(topic, payload)
    end)

    if Logger then
        Logger:getShared():info("[HA] Command handler setup complete")
    end

    return true
end

-- Commands on HA topics are Home Assistant acting (the MQTT client wraps
-- every message as service:mqtt; this narrows the actor)
do
    local handle = HACommands.handleExposedMessage
    HACommands.handleExposedMessage = function(topic, payload)
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
