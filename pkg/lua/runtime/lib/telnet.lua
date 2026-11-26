--- Telnet over a TCP connection: the device's option requests refused, its text as lines
-- and prompts.
--
-- In a plugin with the tcp permission it is the global Telnet:
--
--   local c = plugin:tcpConnect({ host = "192.168.1.20", port = 23 })
--   local t = Telnet.wrap(c)
--   t:onPrompt("login: $", function() t:send("admin") end)
--   t:onLine(function(line) Logger:info(line) end)
--
-- wrap takes the connection's onReceive. Every DO is answered WONT and every WILL DONT, so
-- the device stays in plain line mode; subnegotiation (SB ... SE) and other commands are
-- dropped, IAC IAC is the byte 255. A command or a line split across two reads is joined.
-- A prompt is text without a line end that matches a pattern (end it with $); it is
-- consumed when matched, a line is never a prompt.
-- @module lib.telnet

local M = {}

local IAC, DONT, DO, WONT, WILL, SB, SE = 255, 254, 253, 252, 251, 250, 240

--- Telnet on conn, anything with send(bytes), onReceive(fn(bytes)) and close()
-- @param conn table a connection from plugin:tcpConnect
-- @param opts table|nil {newline = "\r\n"}: what send puts after a line
-- @return table t:send(line), t:sendRaw(bytes), t:onLine(fn(line)), t:onPrompt(pattern, fn(text)), t:close()
function M.wrap(conn, opts)
    opts = type(opts) == "table" and opts or {}
    local newline = opts.newline or "\r\n"
    local pending = ""  -- the start of a command the next read completes
    local inSB = false
    local text = ""     -- text after the last line end
    local lineFn
    local prompts = {}

    local function reply(cmd, option)
        conn:send(string.char(IAC, cmd, option))
    end

    -- the text in raw, commands answered and taken out
    local function strip(raw)
        local s = pending .. raw
        pending = ""
        local out = {}
        local i, n = 1, #s
        while i <= n do
            local b = s:byte(i)
            if b == IAC then
                if i == n then
                    pending = s:sub(i)
                    break
                end
                local c = s:byte(i + 1)
                if inSB then
                    if c == SE then inSB = false end
                    i = i + 2
                elseif c == IAC then
                    out[#out + 1] = "\255"
                    i = i + 2
                elseif c == DO or c == DONT or c == WILL or c == WONT then
                    if i + 2 > n then
                        pending = s:sub(i)
                        break
                    end
                    local option = s:byte(i + 2)
                    if c == DO then
                        reply(WONT, option)
                    elseif c == WILL then
                        reply(DONT, option)
                    end
                    i = i + 3
                elseif c == SB then
                    inSB = true
                    i = i + 2
                else
                    i = i + 2 -- NOP, GA and the like
                end
            else
                local j = s:find("\255", i, true) or n + 1
                if not inSB then out[#out + 1] = s:sub(i, j - 1) end
                i = j
            end
        end
        return table.concat(out)
    end

    conn:onReceive(function(bytes)
        text = text .. strip(bytes):gsub("%z", "")
        while true do
            local at = text:find("\n", 1, true)
            if not at then break end
            local line = text:sub(1, at - 1):gsub("\r$", "")
            text = text:sub(at + 1)
            if lineFn then lineFn(line) end
        end
        if text == "" then return end
        for _, p in ipairs(prompts) do
            if text:find(p.pattern) then
                local shown = text
                text = ""
                p.fn(shown)
                return
            end
        end
    end)

    local t = {}
    function t:send(line) return conn:send(tostring(line):gsub("\255", "\255\255") .. newline) end
    function t:sendRaw(bytes) return conn:send(tostring(bytes)) end
    function t:onLine(fn) lineFn = fn end
    function t:onPrompt(pattern, fn)
        prompts[#prompts + 1] = { pattern = tostring(pattern), fn = fn }
    end
    function t:close() return conn:close() end
    return t
end

return M
