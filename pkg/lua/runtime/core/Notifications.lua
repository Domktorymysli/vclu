--- Notifications - what the panel's bell shows, for a module that passes it on.
--
--   Notifications.on(function(n)
--       if n.resolved or n.level == "info" then return end
--       Cloud.notify{ title = n.title, body = n.body, tag = n.key }
--   end)
--
-- A handler gets a new notification, one whose count reached 10 or 100, and a state that is
-- over (resolved = true: the update was installed, the module loads again). It runs as the
-- system (origin "system"). Errors from the log never come here, nor anything from before a
-- reload. The panel's settings turn sources off and mute keys for the handlers too.
-- @module core.Notifications

local Notifications = {}

local handlers = {}

--- Calls fn(n) for each notification of the panel's bell: n = { id, kind ("state"|"event"),
-- source ("update"|"plugin"|"module"|"login"|"lock"), level ("info"|"warning"|"error"), key,
-- msg, params, title, body, detail, count, first, last (RFC 3339), link, resolved }. msg (e.g.
-- "notify.login.failed") and params (e.g. params.ip) are stable, for code; title and body are
-- English made from them, detail is raw text (a module's error) or nil.
-- @param fn function The handler
-- @return function Call it to stop
-- @api public
function Notifications.on(fn)
    if type(fn) ~= 'function' then error('Notifications.on: expects a function', 2) end
    local h = { fn = fn }
    handlers[#handlers + 1] = h
    return function()
        for i, x in ipairs(handlers) do
            if x == h then table.remove(handlers, i) return end
        end
    end
end

--- Calls the handlers with a notification (called by Go, JSON).
function Notifications._deliver(json)
    local n = JSON.decode(json)
    if type(n) ~= 'table' then return end
    for _, h in ipairs({ unpack(handlers) }) do
        local e = {}
        for k, v in pairs(n) do e[k] = v end
        StateContext.push({ origin = 'system', actor = 'system:vclu' })
        local ok, err = pcall(h.fn, e)
        StateContext.pop()
        if not ok then Logger:getShared():error('[NOTIFY] Notifications.on handler: %s', tostring(err)) end
    end
end

return Notifications
