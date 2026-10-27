--- StateContext - who and what caused the state changes being made now.
--
-- A stack of {origin, actor, correlationId} on the single Lua loop. Entry
-- points (MCP, web, HomeKit, HA commands, module loading) push a context for
-- the duration of their work; StateChange.publish reads the top. Timers keep
-- a copy of the context they were created in and restore it only for their
-- own callback, so a cause is propagated explicitly, never leaked.
--
--   origin  channel the cause came in through: grenton, mqtt, homekit, web, remote,
--           api, mcp, lua, physical, system, unknown
--   actor   stable id of the initiator: user:<login>, agent:<bot>,
--           automation:<id>, service:home-assistant, device:<id>, system:vclu
--
-- @module core.StateContext

StateContext = {}

local stack = {}

local function copy(ctx)
    return { origin = ctx.origin, actor = ctx.actor, correlationId = ctx.correlationId }
end

--- The active context, or nil.
-- @return table|nil
function StateContext.current()
    return stack[#stack]
end

--- Pushes a context; pair with pop (Go entry points pop in a defer).
-- @param ctx table {origin, actor, correlationId}
function StateContext.push(ctx)
    stack[#stack + 1] = copy(ctx)
end

--- Removes the top context.
function StateContext.pop()
    stack[#stack] = nil
end

--- Empties the stack (start of a work item or lifecycle operation).
function StateContext.reset()
    stack = {}
end

local corr = 0

--- A new correlation id for an entry point implemented in Lua.
-- @return string
function StateContext.newCorrelationId()
    if __go_state_change_id then return "c-" .. __go_state_change_id() end
    corr = corr + 1
    return "c-lua-" .. corr
end

--- Stack depth, to restore with truncate() after a Go entry point.
function StateContext.depth()
    return #stack
end

--- Drops contexts above depth n (also ones a failed script left behind).
function StateContext.truncate(n)
    for i = #stack, n + 1, -1 do stack[i] = nil end
end

--- Runs fn(...) with ctx on top; the context is removed also when fn fails.
-- @param ctx table {origin, actor, correlationId}
-- @param fn function
-- @return any Results of fn
function StateContext.with(ctx, fn, ...)
    StateContext.push(ctx)
    local depth = #stack
    local res = { pcall(fn, ...) }
    -- fn may have left extra contexts behind on error
    for i = #stack, depth, -1 do stack[i] = nil end
    if not res[1] then error(res[2], 0) end
    return unpack(res, 2, table.maxn(res))
end

--- Binds fn to the current context (nil: fn unchanged), for later callbacks.
-- @param fn function
-- @return function
function StateContext.bind(fn)
    local ctx = stack[#stack]
    if not ctx or type(fn) ~= "function" then return fn end
    ctx = copy(ctx)
    return function(...) return StateContext.with(ctx, fn, ...) end
end

-- Every timer (EventBus, setTimeout/after/every, plugin sandbox) goes through
-- these globals: a callback runs in the context its timer was created in.
for _, name in ipairs({ "__go_setTimeout", "__go_setInterval" }) do
    local raw = _G[name]
    if type(raw) == "function" then
        _G[name] = function(fn, ms) return raw(StateContext.bind(fn), ms) end
    end
end

return StateContext
