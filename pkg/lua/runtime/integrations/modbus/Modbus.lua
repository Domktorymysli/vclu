---
--- Modbus - Modbus TCP client for RS485 gateways (Waveshare RS485-to-ETH, USR, Elfin...)
--- Wraps the Go __go_modbus_* functions: requests run off the Lua thread and
--- complete through a shared poller, so a slow bus never stalls the CLU.
---
--- Every call is asynchronous and reports through `callback(values, err)`.
--- Exactly one of the two is set: on success `err` is nil, on failure `values` is nil.
---
--- ## Request options
---
---     host     string  address of the *gateway* (required)
---     port     number  gateway TCP port, default 502 (Waveshare ships on 4196)
---     unit     number  Modbus slave address on the RS485 bus, 1-247, default 1.
---                      This is the device behind the gateway, never an IP address.
---     addr     number  zero-based Modbus register or bit offset, default 0
---     qty      number  registers (FC03/FC04) or bits (FC01/FC02) to read, default 1
---     value    number  single register value, FC06 only
---     values   table   register values, FC16 only
---     timeout  number  milliseconds for one transaction, default 2000
---     fc       number  function code, only needed with `Modbus.request`
---
--- Option tables are never modified; each helper works on its own copy.
---
--- ## What `values` holds, per function code
---
---     FC01 readCoils       array of 0/1, one entry per requested bit
---     FC02 readDiscrete    array of 0/1, one entry per requested bit
---     FC03 readHolding     array of 16-bit registers, 1-indexed
---     FC04 readInput       array of 16-bit registers, 1-indexed
---     FC06 writeSingle     echo { address, value }
---     FC16 writeMultiple   echo { address, count }
---
--- Registers arrive raw. Use the decoders below to build 32-bit values.
---
--- ## Concurrency
---
--- Requests to the same `host:port` are serialised, because the RS485 bus behind
--- a gateway carries one transaction at a time. Issuing ten at once queues them
--- rather than speeding anything up. Different gateways do run in parallel.
---
-- @module Modbus
---

local Modbus = {}

Modbus.READ_COILS      = 1
Modbus.READ_DISCRETE   = 2
Modbus.READ_HOLDING    = 3
Modbus.READ_INPUT      = 4
Modbus.WRITE_SINGLE    = 6
Modbus.WRITE_MULTIPLE  = 16

local DEFAULT_PORT    = 502
local DEFAULT_TIMEOUT = 2000
local POLL_INTERVAL   = 20

-- requestId -> {callback, expiresAt}
local pending = {}
local pendingCount = 0
local pollTimer = nil
local seq = 0

local function available()
    return type(__go_modbus_request) == "function"
end

local function nowMs()
    if type(__go_time_ms) == "function" then return __go_time_ms() end
    return os.time() * 1000
end

local function nextRequestId()
    seq = seq + 1
    return "mb_" .. tostring(nowMs()) .. "_" .. tostring(seq)
end

local function finish(requestId, values, err)
    local entry = pending[requestId]
    if not entry then return end
    pending[requestId] = nil
    pendingCount = pendingCount - 1
    local ok, callErr = pcall(entry.callback, values, err)
    if not ok and Logger then
        Logger:error("[Modbus] callback error: " .. tostring(callErr))
    end
end

local function poll()
    while true do
        local id = __go_modbus_poll_async()
        if not id or id == "" then break end
        -- Always collect the response, even when nothing waits for it any more:
        -- leaving it uncollected keeps it in the Go-side map forever.
        local valuesJson, err = __go_modbus_get_response(id)
        if pending[id] then
            if err then
                finish(id, nil, err)
            else
                local decoded = JSON and JSON.decode(valuesJson) or nil
                finish(id, decoded or {}, nil)
            end
        end
    end

    -- Expire stale requests in two passes: finish() runs a callback that may
    -- start a new request, and inserting into a table mid-pairs() is undefined.
    local now = nowMs()
    local expired = nil
    for id, entry in pairs(pending) do
        if now > entry.expiresAt then
            expired = expired or {}
            expired[#expired + 1] = { id = id, timeout = entry.timeout }
        end
    end
    if expired then
        for _, item in ipairs(expired) do
            finish(item.id, nil, "timeout after " .. tostring(item.timeout) .. "ms")
        end
    end

    if pendingCount == 0 and pollTimer then
        clearInterval(pollTimer)
        pollTimer = nil
    end
end

local function ensurePoller()
    if pollTimer then return end
    pollTimer = setInterval(poll, POLL_INTERVAL)
end

-- Helpers set `fc` and friends, so they work on a copy: the caller's table is
-- often a config fragment that must survive the call unchanged.
local function withFc(opts, fc)
    local copy = {}
    if opts then
        for k, v in pairs(opts) do copy[k] = v end
    end
    copy.fc = fc
    return copy
end

--- Run one Modbus transaction with an explicit function code.
-- Prefer the named helpers below; this is the escape hatch for function codes
-- that have none. See the module header for the option and result shapes.
-- @param opts table host, port, unit, fc, addr, qty, value, values, timeout
-- @param callback function(values, err)
function Modbus.request(opts, callback)
    opts = opts or {}
    callback = callback or function() end

    if not available() then
        callback(nil, "modbus backend not available")
        return
    end
    if not opts.host or opts.host == "" then
        callback(nil, "host is required")
        return
    end

    local fc      = opts.fc or Modbus.READ_HOLDING
    local timeout = opts.timeout or DEFAULT_TIMEOUT
    local values  = opts.values
    local valuesJson = ""
    if values then
        if type(values) ~= "table" then values = { values } end
        valuesJson = JSON and JSON.encode(values) or ""
    end

    local requestId = nextRequestId()
    pending[requestId] = {
        callback = callback,
        timeout = timeout,
        -- Grace period on top of the Go-side deadline, which covers connect+retry.
        expiresAt = nowMs() + timeout * 2 + 1000
    }
    pendingCount = pendingCount + 1
    ensurePoller()

    local ok, err = __go_modbus_request(
        requestId,
        opts.host,
        opts.port or DEFAULT_PORT,
        opts.unit or 1,
        fc,
        opts.addr or 0,
        opts.qty or 1,
        valuesJson,
        timeout
    )
    if not ok then
        finish(requestId, nil, err or "request rejected")
    end
end

--- Read coils (FC01). Yields one 0/1 entry per requested bit.
-- @param opts table host, port, unit, addr, qty, timeout
-- @param callback function(values, err)
function Modbus.readCoils(opts, callback)
    Modbus.request(withFc(opts, Modbus.READ_COILS), callback)
end

--- Read discrete inputs (FC02). Yields one 0/1 entry per requested bit.
-- @param opts table host, port, unit, addr, qty, timeout
-- @param callback function(values, err)
function Modbus.readDiscrete(opts, callback)
    Modbus.request(withFc(opts, Modbus.READ_DISCRETE), callback)
end

--- Read holding registers (FC03). Yields 16-bit registers, 1-indexed.
-- @param opts table host, port, unit, addr, qty, timeout
-- @param callback function(values, err)
function Modbus.readHolding(opts, callback)
    Modbus.request(withFc(opts, Modbus.READ_HOLDING), callback)
end

--- Read input registers (FC04). Yields 16-bit registers, 1-indexed.
-- The SDM120/Eastron family exposes everything here.
-- @param opts table host, port, unit, addr, qty, timeout
-- @param callback function(values, err)
function Modbus.readInput(opts, callback)
    Modbus.request(withFc(opts, Modbus.READ_INPUT), callback)
end

--- Write one holding register (FC06). Yields the echo { address, value }.
-- @param opts table host, port, unit, addr, value, timeout
-- @param callback function(values, err)
function Modbus.writeSingle(opts, callback)
    local copy = withFc(opts, Modbus.WRITE_SINGLE)
    copy.values = { copy.value or 0 }
    Modbus.request(copy, callback)
end

--- Write consecutive holding registers (FC16). Yields the echo { address, count }.
-- @param opts table host, port, unit, addr, values, timeout
-- @param callback function(values, err)
function Modbus.writeMultiple(opts, callback)
    Modbus.request(withFc(opts, Modbus.WRITE_MULTIPLE), callback)
end

--------------------------------------------------------------------------------
-- DECODERS
--------------------------------------------------------------------------------

--- IEEE-754 single precision from two big-endian registers.
-- @param hi number first register, @param lo number second register
-- @return number|nil decoded value (nil for NaN/inf)
function Modbus.toFloat32(hi, lo)
    if not hi or not lo then return nil end
    local sign = (hi >= 0x8000) and -1 or 1
    local exponent = math.floor(hi / 128) % 256
    local mantissa = (hi % 128) * 65536 + lo

    if exponent == 255 then return nil end       -- NaN / infinity
    if exponent == 0 then
        if mantissa == 0 then return sign * 0.0 end
        return sign * mantissa * 2 ^ -149        -- subnormal
    end
    return sign * (1 + mantissa / 8388608) * 2 ^ (exponent - 127)
end

--- Signed 32-bit integer from two big-endian registers.
function Modbus.toInt32(hi, lo)
    if not hi or not lo then return nil end
    local v = hi * 65536 + lo
    if v >= 2147483648 then v = v - 4294967296 end
    return v
end

--- Unsigned 32-bit integer from two big-endian registers.
function Modbus.toUint32(hi, lo)
    if not hi or not lo then return nil end
    return hi * 65536 + lo
end

--- Signed 16-bit integer from one register.
function Modbus.toInt16(v)
    if not v then return nil end
    if v >= 32768 then return v - 65536 end
    return v
end

--- Decode a float at register offset `offset` inside a block read from `base`.
-- Registers arrive 1-indexed in Lua, hence the +1.
function Modbus.floatAt(registers, base, offset)
    if not registers then return nil end
    local i = offset - base + 1
    return Modbus.toFloat32(registers[i], registers[i + 1])
end

--------------------------------------------------------------------------------
-- DIAGNOSTICS
--------------------------------------------------------------------------------

--- Counters for diagnostics.
-- `pending` counts requests this Lua runtime still waits on; `uncollected`
-- counts finished responses sitting in the Go-side map, which should return to
-- zero once the poller sweeps them. A persistently non-zero `uncollected` means
-- responses are being stranded.
-- @return table { requests, errors, targets, open, pending, uncollected }
function Modbus.stats()
    if type(__go_modbus_stats) ~= "function" then return {} end
    local raw = __go_modbus_stats()
    local decoded = JSON and JSON.decode(raw) or {}
    decoded.pending = pendingCount
    return decoded
end

--- Whether the Go-side Modbus backend is present in this build.
function Modbus.isAvailable()
    return available()
end

return Modbus
