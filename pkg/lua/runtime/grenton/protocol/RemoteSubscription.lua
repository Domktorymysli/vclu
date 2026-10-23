--- RemoteSubscription - Subscribe to state updates from real CLUs.
-- This module provides functionality to subscribe to object state changes on real
-- CLUs using the SYSTEM:clientRegister/clientDestroy mechanism.
-- It handles batching subscriptions, parsing incoming reports, and updating the
-- object registry with new values.
--
-- The subscription mechanism uses UDP communication to register with remote CLUs
-- and receive push notifications when object states change. Subscriptions have a
-- configurable TTL and are periodically re-registered to maintain the connection.
--
-- @module RemoteSubscription

RemoteSubscription = {}

--- Configuration for remote subscriptions
-- @table config
-- @field ttl number Default TTL (time-to-live) in seconds for subscriptions (default: 60)
-- @field batchSize number Entries per subscription (default: 4, MIN_BATCH_SIZE..MAX_BATCH_SIZE)
-- @field objectTypes table List of object types to subscribe to (default: {"DOUT", "DIN", "DIMM"})
-- @field enabled boolean Whether auto-subscription is enabled (default: true)
RemoteSubscription.config = {
    ttl = 60,                        -- Default TTL in seconds
    batchSize = 4,                   -- Entries per subscription
    batchDelay = 50,                 -- ms delay between batches (30-100)
    objectTypes = {"DOUT", "DIN", "DIMM"},  -- Types to subscribe (legacy)
    enabled = false,                 -- Disabled by default; user must configure via UI
    whitelist = nil,                 -- Hash map of whitelisted paths (nil = nothing syncs)
}

--- Minimum spacing (ms) between renew packets; below this the CLU drops them
RemoteSubscription.RENEW_MIN_SPACING_MS = 50

--- Range of config.batchSize; Object Manager itself registers 28 entries in one clientRegister
RemoteSubscription.MIN_BATCH_SIZE = 2
RemoteSubscription.MAX_BATCH_SIZE = 28

--- config.batchSize brought into MIN_BATCH_SIZE..MAX_BATCH_SIZE
function RemoteSubscription.batchSize()
    local n = tonumber(RemoteSubscription.config.batchSize) or 4
    return math.max(RemoteSubscription.MIN_BATCH_SIZE, math.min(RemoteSubscription.MAX_BATCH_SIZE, math.floor(n)))
end

--- autoSubscribe time budget per batch (s); a LAN round-trip plus batchDelay is ~0.1s
RemoteSubscription.SECONDS_PER_BATCH = 0.25

--- Active subscriptions table
-- Key format: "cluIP:sessionID", value contains subscription metadata
-- @table activeSubscriptions
RemoteSubscription.activeSubscriptions = {}

--- Timestamp of last completed autoSubscribe run
RemoteSubscription._lastSync = nil

--- Renew timer handle
RemoteSubscription._renewTimer = nil

--- Running renew chain {startedAt = os.time()} (nil = none); see refreshAll
RemoteSubscription._renewRun = nil

--- A renew chain running longer than this (s) is treated as lost
RemoteSubscription.RENEW_STALE_SECONDS = 60

--- Session ID counter for generating unique session identifiers
-- @field nextSessionID number
RemoteSubscription.nextSessionID = 1

--- Generate a new unique session ID
-- Increments the internal session counter and returns the new ID
-- @return number The newly generated session ID
function RemoteSubscription.generateSessionID()
    local id = RemoteSubscription.nextSessionID
    RemoteSubscription.nextSessionID = id + 1
    return id
end

--- Convert numeric IP (uint32) to dotted string format
-- If input is already a string, returns it unchanged
-- @param numIP number|string IP address as uint32 or dotted string
-- @return string IP address in dotted decimal notation (e.g., "192.168.1.1")
local function numericIPToString(numIP)
    if type(numIP) == "string" then
        return numIP
    end
    local b1 = math.floor(numIP / 16777216) % 256
    local b2 = math.floor(numIP / 65536) % 256
    local b3 = math.floor(numIP / 256) % 256
    local b4 = numIP % 256
    return string.format("%d.%d.%d.%d", b1, b2, b3, b4)
end

--- Serialize a table of subscriptions for Lua command
-- Converts subscription table to format: {{objectId, featureIndex}, {objectId, featureIndex}, ...}
-- @param subs table Array of subscription objects with objId and featureIndex fields
-- @return string Serialized subscription string suitable for SYSTEM:clientRegister
local function serializeSubscriptions(subs)
    local parts = {}
    for _, sub in ipairs(subs) do
        table.insert(parts, string.format("{%s, %d}", sub.objId, sub.featureIndex))
    end
    return "{" .. table.concat(parts, ", ") .. "}"
end

--- Parse values string from clientReport
-- Handles both simple values like {1,0,0.5,23.4} and nested structures like {0,0,{0,0,0,0},5}
-- @param valuesStr string Raw values string from clientReport with outer braces
-- @return table Array of parsed values (numbers or strings for nested structures)
local function parseValues(valuesStr)
    local values = {}
    -- Remove outer braces if present
    local clean = valuesStr:gsub("^{", ""):gsub("}$", "")

    Logger:getShared():debug("[parseValues] input: '%s' -> clean: '%s'", valuesStr, clean)

    -- Parse respecting nested braces (using table.concat to avoid O(N^2) string allocs)
    local buf = {}
    local depth = 0

    for i = 1, #clean do
        local char = clean:sub(i, i)
        if char == "{" then
            depth = depth + 1
            buf[#buf + 1] = char
        elseif char == "}" then
            depth = depth - 1
            buf[#buf + 1] = char
        elseif char == "," and depth == 0 then
            -- End of value at top level
            local trimmed = table.concat(buf):match("^%s*(.-)%s*$")
            if trimmed and trimmed ~= "" then
                local num = tonumber(trimmed)
                table.insert(values, num or trimmed)
            end
            buf = {}
        else
            buf[#buf + 1] = char
        end
    end

    -- Don't forget the last value
    local trimmed = table.concat(buf):match("^%s*(.-)%s*$")
    if trimmed and trimmed ~= "" then
        local num = tonumber(trimmed)
        table.insert(values, num or trimmed)
    end

    return values
end

--- Subscribe to objects on remote CLU
-- Sends a clientRegister command to the target CLU and stores subscription metadata.
-- Returns initial values for all subscribed objects.
-- @param cluIP string|number IP address of the CLU (dotted string or uint32)
-- @param objects table Array of subscription objects with objId and featureIndex fields (max 4)
-- @param cluSerial string CLU serial number for registry lookup (e.g., "CLU212345678")
-- @return number|nil sessionID Session ID for this subscription, or nil on error
-- @return table|string initialValues Array of initial values, or error message on failure
function RemoteSubscription.subscribe(cluIP, objects, cluSerial)
    -- Validate batch size
    if #objects > RemoteSubscription.batchSize() then
        return nil, "max " .. RemoteSubscription.batchSize() .. " entries per subscription"
    end

    -- Convert IP if numeric
    local targetIP = numericIPToString(cluIP)

    -- Generate session ID
    local sessionID = RemoteSubscription.generateSessionID()

    -- Get vCLU IP (from global or fallback)
    local vcluIP = __vclu_ip or "0.0.0.0"

    -- Build subscriptions table for Lua command
    local subsStr = serializeSubscriptions(objects)

    -- Build the clientRegister script
    local script = string.format(
        "SYSTEM:clientRegister('%s', %d, %d, %s)",
        vcluIP, 4344, sessionID, subsStr
    )

    -- debug, not info: a reload fires one of these per batch (dozens in a row).
    -- NOTE: Logger:debug is an unconditional print() — there is no level gating,
    -- so this does NOT reduce how many lines reach the log ring. What it does is
    -- tag them [DEBUG] so vclu_logs(level:"info") can filter them out. The ring
    -- still evicts at the same rate; fixing that needs a real level gate.
    -- The per-reload summary is still logged at info by the caller.
    Logger:getShared():debug("[RemoteSub] Subscribing to %s: session=%d, objects=%d",
        targetIP, sessionID, #objects)
    Logger:getShared():debug("[RemoteSub] Script: %s", script)

    -- Send to real CLU via encrypted UDP
    if not __go_send_command then
        return nil, "__go_send_command not available"
    end

    local delay = math.max(30, math.min(100, RemoteSubscription.config.batchDelay or 50))
    local result, err = __go_send_command(targetIP, script, delay)
    if err then
        Logger:getShared():error("[RemoteSub] Subscribe failed: %s", tostring(err))
        return nil, err
    end

    Logger:getShared():debug("[RemoteSub] Response: %s", tostring(result))

    -- Parse response: clientReport:sessionID:{values}
    local reportSid, valuesStr = result:match("clientReport:(%d+):(.+)")
    if not reportSid then
        Logger:getShared():error("[RemoteSub] Invalid response format: %s", tostring(result))
        return nil, "invalid response format"
    end

    -- Store subscription metadata
    local key = targetIP .. ":" .. sessionID
    RemoteSubscription.activeSubscriptions[key] = {
        cluIP = targetIP,
        cluSerial = cluSerial,
        sessionID = sessionID,
        objects = objects,
        registeredAt = os.time(),
        expiresAt = os.time() + RemoteSubscription.config.ttl,
    }

    -- Parse and return initial values
    local initialValues = parseValues(valuesStr)

    -- debug for the same reason as the "Subscribing" line above: one per batch.
    -- Diagnostic note: this was the only place reporting how many values a CLU
    -- actually returned. The info-level summary counts REQUESTED objects, so a CLU
    -- that accepts a subscription and answers with 0 values is now only visible
    -- at debug level.
    Logger:getShared():debug("[RemoteSub] Subscribed to %s: session=%d, got %d values",
        targetIP, sessionID, #initialValues)

    -- Update registry with initial values
    RemoteSubscription.updateRegistry(targetIP, sessionID, initialValues)

    return sessionID, initialValues
end

--- Re-register subscription before TTL expires
-- Sends clientRegister (not clientRefresh) so the CLU keeps the session alive. A live
-- session gets no push; an expired one is recreated and pushes current states.
-- Fire-and-forget via noreply: the CLU answers with the session's current values and Go hands
-- them to applyRenewAnswer, which corrects a state whose report was lost.
-- @param cluIP string|number IP address of the CLU (dotted string or uint32)
-- @param sessionID number Session ID to re-register
-- @return true|nil success True on send, nil on error
-- @return string|nil error Error message if send failed
function RemoteSubscription.refresh(cluIP, sessionID)
    local targetIP = numericIPToString(cluIP)
    local vcluIP = __vclu_ip or "0.0.0.0"

    -- Look up subscription to get objects list
    local key = targetIP .. ":" .. sessionID
    local sub = RemoteSubscription.activeSubscriptions[key]
    if not sub then
        return nil, "subscription not found: " .. key
    end

    local subsStr = serializeSubscriptions(sub.objects)
    local script = string.format(
        "SYSTEM:clientRegister('%s', %d, %d, %s)",
        vcluIP, 4344, sessionID, subsStr
    )

    Logger:getShared():debug("[RemoteSub] Re-registering %s: session=%d, objects=%d",
        targetIP, sessionID, #sub.objects)

    if not __go_send_command_noreply then
        return nil, "no command sender available"
    end

    -- What was published before sending; applyRenewAnswer keeps anything newer
    local marks = {}
    for i, objInfo in ipairs(sub.objects) do
        local obj = _registry and _registry[tostring(sub.cluSerial) .. "." .. objInfo.objId]
        marks[i] = obj and StateChange.mark(obj) or 0
    end
    sub.renewMarks = marks

    local ok, err = __go_send_command_noreply(targetIP, script)
    if not ok then
        sub.renewMarks = nil
        Logger:getShared():error("[RemoteSub] Re-register failed: %s", tostring(err))
        return nil, err
    end

    -- Update expiry time
    sub.expiresAt = os.time() + RemoteSubscription.config.ttl

    return true
end

--- Destroy subscription (fire-and-forget, best-effort)
-- @param cluIP string|number IP address of the CLU (dotted string or uint32)
-- @param sessionID number Session ID to destroy
-- @return true
function RemoteSubscription.destroy(cluIP, sessionID)
    local targetIP = numericIPToString(cluIP)
    local vcluIP = __vclu_ip or "0.0.0.0"

    local script = string.format(
        "SYSTEM:clientDestroy('%s', %d, %d)",
        vcluIP, 4344, sessionID
    )

    Logger:getShared():info("[RemoteSub] Destroying subscription %s: session=%d", targetIP, sessionID)

    if __go_send_command_noreply then
        __go_send_command_noreply(targetIP, script)
    end

    -- Remove from active subscriptions
    local key = targetIP .. ":" .. sessionID
    RemoteSubscription.activeSubscriptions[key] = nil

    return true
end

--- Handle incoming clientReport from Go
-- Called when port 4344 receives a push notification from the remote CLU.
-- Parses the values and updates the object registry.
-- @param cluIP string IP address of the CLU sending the report
-- @param sessionID number Session ID associated with this report
-- @param valuesStr string Raw values string from the clientReport
function RemoteSubscription.handleReport(cluIP, sessionID, valuesStr)
    Logger:getShared():debug("[RemoteSub] ClientReport from %s: session=%d, values=%s",
        cluIP, sessionID, valuesStr)

    -- Parse values
    local values = parseValues(valuesStr)

    -- Update registry
    RemoteSubscription.updateRegistry(cluIP, sessionID, values)
end

--- Update registry with received values
-- Looks up the subscription metadata and updates each subscribed object in the registry.
-- Publishes state changes to StateBus/MQTT if the value changed.
-- @param cluIP string IP address of the CLU
-- @param sessionID number Session ID for the subscription
-- @param values table Array of new values corresponding to subscribed objects
-- @param skip table|nil skip[i] = true leaves the i-th object as it is
function RemoteSubscription.updateRegistry(cluIP, sessionID, values, skip)
    local key = cluIP .. ":" .. sessionID
    local sub = RemoteSubscription.activeSubscriptions[key]

    if not sub then
        Logger:getShared():warn("[RemoteSub] Unknown subscription: %s:%d", cluIP, sessionID)
        return
    end

    -- A clientReport is the CLU telling us its state
    local ctx = { origin = "grenton", actor = "device:" .. tostring(sub.cluSerial) }

    -- Update each object in registry
    for i, objInfo in ipairs(sub.objects) do
        local newValue = values[i]
        if newValue == nil then
            break
        end

        -- Find object in registry
        local registryKey = sub.cluSerial .. "." .. objInfo.objId
        local obj = _registry and _registry[registryKey]

        -- A skipped entry is left as it is, its movement included
        local skipped = skip and skip[i]

        -- A roller reports its position and its movement; only one is the value
        if obj and obj.fromFeature and not skipped then
            newValue = obj:fromFeature(objInfo.featureIndex or 0, newValue)
        end

        if obj and newValue ~= nil and not skipped then
            local oldValue = obj._lastState

            -- Dead-band for analog sensors: skip noise < 0.1
            if type(newValue) == "number" and type(oldValue) == "number"
               and objInfo.objId:sub(1, 3) == "ANA"
               and math.abs(newValue - oldValue) < 0.1 then
                goto continue
            end

            -- Use updateState() to handle validation and events
            local mark = StateChange.mark(obj)
            local objCtx = ctx
            if oldValue ~= nil and oldValue ~= newValue then objCtx = StateChange.reportContext(obj, newValue, ctx) end
            if obj.updateState then
                StateChange.applyFrom(obj, objCtx, obj.updateState, obj, newValue)
            else
                obj._lastState = newValue
            end

            -- An exposed object's onChange callback may have published already
            if obj._lastState ~= oldValue then
                obj._lastEvent = os.time()
                if not StateChange.publishedSince(obj, mark) then
                    StateChange.publish(registryKey, obj, oldValue, obj._lastState, objCtx)
                end
            end
        end

        ::continue::
    end
end

--- Apply the CLU's answer to a renewal sent by refresh(): the session's current values.
-- Corrects a state whose report was lost. An object published after the renewal was sent
-- (a local write or a push) keeps its state: the answer is older.
-- @param cluIP string IP address the renewal was sent to
-- @param sessionID number Session ID from the answer
-- @param values table Array of values, as in a report
function RemoteSubscription.applyRenewAnswer(cluIP, sessionID, values)
    local sub = RemoteSubscription.activeSubscriptions[cluIP .. ":" .. sessionID]
    local marks = sub and sub.renewMarks
    if not marks or #values ~= #sub.objects then return end
    sub.renewMarks = nil

    local skip = {}
    for i, objInfo in ipairs(sub.objects) do
        local obj = _registry and _registry[tostring(sub.cluSerial) .. "." .. objInfo.objId]
        skip[i] = obj ~= nil and StateChange.publishedSince(obj, marks[i])
    end
    RemoteSubscription.updateRegistry(cluIP, sessionID, values, skip)
end

--- Auto-subscribe to whitelisted remote objects
-- Uses whitelist from config to determine which objects to subscribe to.
-- If no whitelist is configured, nothing syncs.
function RemoteSubscription.autoSubscribe()
    if not RemoteSubscription.config.enabled then
        Logger:getShared():debug("[RemoteSub] Sync disabled, skipping")
        return
    end

    local wl = RemoteSubscription.config.whitelist
    if not wl or not next(wl) then
        Logger:getShared():info("[RemoteSub] No objects in whitelist, skipping")
        return
    end

    if not _registry then
        Logger:getShared():warn("[RemoteSub] Registry not available")
        return
    end

    Logger:getShared():info("[RemoteSub] Starting auto-subscribe (whitelist mode)...")

    -- Save old subscriptions, clear table AFTER new ones are registered
    -- This prevents "Unknown subscription" warnings during the transition window
    local oldSubs = RemoteSubscription.activeSubscriptions
    local oldCount = 0
    for _ in pairs(oldSubs) do oldCount = oldCount + 1 end
    RemoteSubscription.activeSubscriptions = {}

    -- Sorted so batches (and session contents) are the same on every run
    local paths = {}
    for path, _ in pairs(wl) do paths[#paths + 1] = path end
    table.sort(paths)

    -- Group whitelisted objects by CLU
    local cluObjects = {}  -- key = cluIP, value = {cluSerial, objects}
    local cluOrder = {}

    for _, path in ipairs(paths) do
        local cluId = path:match("^(.-)%.(.+)$")
        local obj = _registry and _registry[path]

        if obj and obj._clu and obj._clu.address then
            local cluIP = numericIPToString(obj._clu.address)

            if not cluObjects[cluIP] then
                cluObjects[cluIP] = {
                    cluSerial = cluId,
                    objects = {}
                }
                cluOrder[#cluOrder + 1] = cluIP
            end

            local features = obj.subscribeFeatures and obj:subscribeFeatures() or {0}
            for _, featureIndex in ipairs(features) do
                table.insert(cluObjects[cluIP].objects, {
                    objId = obj._objId,
                    featureIndex = featureIndex
                })
            end
        end
    end

    -- Subscribe to each CLU in batches
    local totalSubscriptions = 0
    local totalObjects = 0
    local skippedCLUs = 0
    local startTime = os.time()
    local batchSize = RemoteSubscription.batchSize()

    -- Each batch is a blocking round-trip plus batchDelay, so the budget grows with the work
    local totalBatches = 0
    for _, cluData in pairs(cluObjects) do
        totalBatches = totalBatches + math.ceil(#cluData.objects / batchSize)
    end
    local maxTotalSeconds = math.max(30, math.ceil(totalBatches * RemoteSubscription.SECONDS_PER_BATCH))

    for idx, cluIP in ipairs(cluOrder) do
        local cluData = cluObjects[cluIP]
        if os.time() - startTime > maxTotalSeconds then
            local remaining = #cluOrder - idx + 1
            skippedCLUs = skippedCLUs + remaining
            Logger:getShared():warn("[RemoteSub] Total timeout (%ds) exceeded, skipping %d remaining CLUs",
                maxTotalSeconds, remaining)
            break
        end

        local objects = cluData.objects
        local cluSerial = cluData.cluSerial or "UNKNOWN"

        Logger:getShared():info("[RemoteSub] CLU %s (%s): %d objects to subscribe", cluSerial, cluIP, #objects)

        for i = 1, #objects, batchSize do
            local batch = {}
            for j = i, math.min(i + batchSize - 1, #objects) do
                table.insert(batch, objects[j])
            end

            local sessionID, err = RemoteSubscription.subscribe(cluIP, batch, cluSerial)
            if sessionID then
                totalSubscriptions = totalSubscriptions + 1
                totalObjects = totalObjects + #batch
            else
                Logger:getShared():error("[RemoteSub] Failed to subscribe to %s: %s — skipping remaining batches",
                    cluIP, tostring(err))
                skippedCLUs = skippedCLUs + 1
                break
            end
        end
    end

    -- Now destroy old subscriptions (best-effort, fire-and-forget after new ones are active)
    if oldCount > 0 and __go_send_command_noreply then
        local destroyed = 0
        for key, sub in pairs(oldSubs) do
            local vcluIP = __vclu_ip or "0.0.0.0"
            local script = string.format(
                "SYSTEM:clientDestroy('%s', %d, %d)",
                vcluIP, 4344, sub.sessionID
            )
            __go_send_command_noreply(sub.cluIP, script)
            destroyed = destroyed + 1
        end
        Logger:getShared():info("[RemoteSub] Cleaned up %d old subscriptions", destroyed)
    end

    RemoteSubscription._lastSync = os.time()

    Logger:getShared():info("[RemoteSub] Auto-subscribe complete: %d subscriptions, %d objects, %d CLUs skipped (%.0fs)",
        totalSubscriptions, totalObjects, skippedCLUs, os.difftime(os.time(), startTime))
end

--- Get sync status for API
-- Returns JSON string with subscription count, object count, last sync time, and last events.
-- @return string JSON-encoded status object
function RemoteSubscription.getStatus()
    local count, objects = 0, 0
    for _, sub in pairs(RemoteSubscription.activeSubscriptions) do
        count = count + 1
        objects = objects + #sub.objects
    end
    -- Collect states and last event timestamps for whitelisted objects
    local lastEvents = {}
    local states = {}
    local wl = RemoteSubscription.config.whitelist
    if _registry and wl then
        for path, _ in pairs(wl) do
            local obj = _registry[path]
            if obj then
                if obj._lastEvent then
                    lastEvents[path] = obj._lastEvent
                end
                if obj._lastState ~= nil then
                    states[path] = obj._lastState
                end
            end
        end
    end
    return JSON.encode({
        subscriptions = count,
        objects = objects,
        lastSync = RemoteSubscription._lastSync,
        lastEvents = lastEvents,
        states = states,
    })
end

--- Re-register all active subscriptions (periodic renew).
-- Sends clientRegister for each active subscription so the CLU keeps it alive.
-- Falls back to autoSubscribe only on failure.
-- Sends are chained with a delay (gap survives RunLoop stalls): the CLU drops
-- back-to-back packets, and a session whose renew is lost expires silently.
function RemoteSubscription.refreshAll()
    if not RemoteSubscription.config.enabled then return end

    -- One chain at a time: two interleaved chains send back-to-back again
    local current = RemoteSubscription._renewRun
    if current then
        if os.time() - current.startedAt < RemoteSubscription.RENEW_STALE_SECONDS then
            Logger:getShared():warn("[RemoteSub] Previous renew still running, skipping this one")
            return
        end
        Logger:getShared():warn("[RemoteSub] Previous renew never finished, starting a new one")
    end

    local subs = {}
    for key, sub in pairs(RemoteSubscription.activeSubscriptions) do
        subs[#subs + 1] = { key = key, sub = sub }
    end
    if #subs == 0 then return end

    local delay = math.max(RemoteSubscription.RENEW_MIN_SPACING_MS,
        math.min(100, RemoteSubscription.config.batchDelay or 50))
    local count, failed = 0, 0
    local run = { startedAt = os.time() }
    RemoteSubscription._renewRun = run

    local function finish()
        RemoteSubscription._renewRun = nil
        if failed > 0 then
            Logger:getShared():info("[RemoteSub] %d re-registered, %d failed — running full re-subscribe", count, failed)
            RemoteSubscription.autoSubscribe()
        elseif count > 0 then
            RemoteSubscription._lastSync = os.time()
            Logger:getShared():debug("[RemoteSub] Re-registered %d subscriptions", count)
        end
    end

    local function step(i)
        if RemoteSubscription._renewRun ~= run then return end -- abandoned as stale
        local entry = subs[i]
        -- Skip sessions replaced/destroyed meanwhile (not a failure)
        if RemoteSubscription.config.enabled
           and RemoteSubscription.activeSubscriptions[entry.key] == entry.sub then
            -- pcall: an error here would silently end the chain for all later sessions
            local ok, result, err = pcall(RemoteSubscription.refresh, entry.sub.cluIP, entry.sub.sessionID)
            if ok and result then
                count = count + 1
            else
                failed = failed + 1
                Logger:getShared():warn("[RemoteSub] Refresh failed for %s: %s", entry.key, tostring(ok and err or result))
                RemoteSubscription.activeSubscriptions[entry.key] = nil
            end
        end

        if i == #subs then return finish() end
        local timerId = EventBus:getShared():setTimeout(function() step(i + 1) end, delay)
        if not timerId or timerId < 0 then
            -- No timer slot: the rest would expire unrenewed, so re-subscribe instead
            Logger:getShared():warn("[RemoteSub] Renew timer rejected, %d sessions not renewed", #subs - i)
            failed = failed + (#subs - i)
            finish()
        end
    end

    step(1)
end

--- Start the renew timer that periodically re-registers subscriptions.
-- Timer interval is TTL/2 (minimum 10 seconds).
-- Uses refreshAll() for periodic re-registration; autoSubscribe() only on first run or failure.
function RemoteSubscription.startRenewTimer()
    RemoteSubscription.stopRenewTimer()
    local interval = math.max((RemoteSubscription.config.ttl or 60) / 2, 10) * 1000  -- ms, min 10s
    RemoteSubscription._renewTimer = EventBus:getShared():setInterval(function()
        Logger:getShared():debug("[RemoteSub] Renew timer fired")
        if next(RemoteSubscription.activeSubscriptions) then
            RemoteSubscription.refreshAll()
        else
            RemoteSubscription.autoSubscribe()
        end
    end, interval)
    Logger:getShared():info("[RemoteSub] Renew timer started, interval=%dms", interval)
end

--- Stop the renew timer
function RemoteSubscription.stopRenewTimer()
    if RemoteSubscription._renewTimer then
        EventBus:getShared():clearInterval(RemoteSubscription._renewTimer)
        RemoteSubscription._renewTimer = nil
    end
end

--- Destroy all active subscriptions (cleanup)
-- Iterates through all active subscriptions and destroys them. Used during shutdown.
function RemoteSubscription.destroyAll()
    RemoteSubscription.stopRenewTimer()
    Logger:getShared():info("[RemoteSub] Destroying all subscriptions...")

    for key, sub in pairs(RemoteSubscription.activeSubscriptions) do
        RemoteSubscription.destroy(sub.cluIP, sub.sessionID)
    end

    RemoteSubscription.activeSubscriptions = {}
end

return RemoteSubscription
