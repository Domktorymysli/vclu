--
-- Metrics - Lightweight metrics collection for dashboard charts
--
-- Usage:
--   metric("salda:outsideAir", function() return outside:get() end, {unit="°C", interval=10})
--
-- The metric() function registers a getter that Go will call periodically.
-- Results are stored in ring buffers (RAM) and flushed to daily files (disk).
--

local Metrics = {}
Metrics._registered = {}

--- Register a metric for periodic collection.
-- @api public
-- @param path    Unique metric identifier (e.g., "salda:outsideAir")
-- @param getter  Function that returns the current value
-- @param opts    Optional table: {unit="°C", interval=60, decimals=1}
-- @return path
function metric(path, getter, opts)
    opts = opts or {}

    Metrics._registered[path] = {
        getter   = getter,
        unit     = opts.unit or "",
        interval = opts.interval or 60,
        decimals = opts.decimals,
    }

    -- Tell Go to register this metric in the collector
    if __go_metric_register then
        __go_metric_register(path, opts.unit or "", opts.interval or 60)
    end

    return path
end

--- Called by Go runtime. Collects values for the given paths.
-- @param paths  Array of metric path strings (decoded from JSON by Go)
-- @return JSON string: [{"p":"path","v":21.3}, ...]
function Metrics._collect(paths)
    local results = {}
    for _, path in ipairs(paths) do
        local def = Metrics._registered[path]
        if def then
            local ok, val = pcall(def.getter)
            if ok and val ~= nil then
                local v = tonumber(val)
                if v and def.decimals then
                    local mult = 10 ^ def.decimals
                    v = math.floor(v * mult + 0.5) / mult
                end
                if v then
                    results[#results + 1] = {p = path, v = v}
                end
            end
        end
    end
    return JSON:encode(results)
end

--- Collect metrics from JSON-encoded paths string.
--- Called by Go via CallLuaFunction to avoid DoString compilation.
--- @param jsonStr string JSON array of metric paths
--- @return string JSON results
function Metrics._collectJSON(jsonStr)
    local paths = JSON:decode(jsonStr)
    if not paths then return "[]" end
    return Metrics._collect(paths)
end

--- Re-register all Lua metrics with Go collector.
-- Called after SetMetricsCollector when the bridge becomes available late.
function Metrics._syncToGo()
    if not __go_metric_register then return 0 end
    local count = 0
    for path, def in pairs(Metrics._registered) do
        __go_metric_register(path, def.unit, def.interval)
        count = count + 1
    end
    return count
end

--- Called by Go to list all registered metrics.
-- @return JSON string
function Metrics._list()
    local out = {}
    for path, def in pairs(Metrics._registered) do
        out[#out + 1] = {path = path, unit = def.unit, interval = def.interval}
    end
    return JSON:encode(out)
end

return Metrics
