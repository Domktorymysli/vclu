--[[
    Example Plugin dla vCLU (API v2)

    Ten plik pokazuje minimum potrzebne do stworzenia pluginu
    oraz przykłady użycia nowego API (v2).

    Struktura pluginu:
        plugins/example/
        ├── plugin.json     -- manifest (wymagany)
        └── init.lua        -- kod pluginu (wymagany)
]]

--------------------------------------------------------------------------------
-- PLUGIN REGISTRATION
--------------------------------------------------------------------------------

local plugin = Plugin:new("example", {
    name = "Example Plugin",
    version = "2.1.0",
    description = "Example plugin showcasing vCLU API v2 with expose support"
})

--------------------------------------------------------------------------------
-- STATE (zawsze na początku)
--------------------------------------------------------------------------------

local state = {
    ready = false,
    lastUpdate = 0,
    lastError = nil,
    -- Plugin-specific data
    value = 0,
    status = "unknown"
}

--------------------------------------------------------------------------------
-- INITIALIZATION
--------------------------------------------------------------------------------

plugin:onInit(function(config)
    -- Config validation with coercion helpers
    local greeting = plugin:coerceString(config.greeting, "Hello!")
    local interval = plugin:coerceNumber(config.interval, 60)
    local enabled = plugin:coerceBool(config.enabled, true)

    plugin:log("info", "Plugin loaded! Greeting: " .. greeting)

    -- Create initial registry object using upsertObject
    plugin:upsertObject("data", {
        ready = false,
        value = 0,
        status = "unknown",
        lastUpdate = 0
    })

    ---------------------------------------------------------------------------
    -- SENSORS & CONTROLS (for expose API)
    -- Użytkownik może eksponować te sensory/kontrolki do Home Assistant
    ---------------------------------------------------------------------------

    -- Sensor: tylko do odczytu (getter)
    plugin:sensor("value", function()
        return state.value
    end)

    -- Sensor: status jako binary (0/1)
    plugin:sensor("ready", function()
        return state.ready and 1 or 0
    end)

    -- Control: do odczytu i zapisu (getter + setter)
    plugin:control("target",
        function() return state.value end,           -- getter
        function(v)                                   -- setter
            state.value = v
            plugin:log("info", "Target set to: " .. tostring(v))
            -- Notify sensor about change
            local sensor = plugin:get("value")
            if sensor and sensor._notify then sensor:_notify() end
        end
    )

    -- If enabled, setup polling
    if enabled and config.apiUrl then
        setupPoller(config.apiUrl, interval)
    end
end)

plugin:onCleanup(function()
    plugin:log("info", "Plugin stopping...")
    -- Timery i subskrypcje są czyszczone automatycznie przez sandbox
end)

--------------------------------------------------------------------------------
-- PRZYKŁAD: Poller (zamiast ręcznego setInterval + httpGet)
--------------------------------------------------------------------------------

local poller = nil

function setupPoller(apiUrl, intervalSec)
    poller = plugin:poller("fetch", {
        -- Interval w milisekundach
        interval = intervalSec * 1000,

        -- Czy wykonać natychmiast przy starcie
        immediate = true,

        -- Timeout dla całego tick (włącznie z retry)
        timeout = 30000,

        -- Retry configuration
        retry = {
            maxAttempts = 3,
            backoff = 2000  -- base backoff, doubles each retry
        },

        -- Główna funkcja pollingu
        onTick = function(done)
            -- Użyj plugin:url() do budowania URL z parametrami
            local url = plugin:url(apiUrl, {
                format = "json",
                timestamp = os.time()
            })

            -- Użyj plugin:httpRequest() z parseJson
            plugin:httpRequest({
                url = url,
                timeout = 10000,
                parseJson = "success",  -- automatycznie parsuj JSON przy 2xx
                log = { redact = true }  -- redaguj sekrety w logach (domyślnie true)
            }, function(resp)
                -- resp zawsze jest tabelą: { status, body, headers, json, err, errCode }
                if resp.err then
                    -- Błąd drugim argumentem: done(nil, err). Poller policzy porażkę, wywoła onError i ponowi
                    done(nil, resp.err)
                    return
                end

                -- resp.json jest dostępny gdy parseJson="success" i status 2xx
                if resp.json then
                    -- Aktualizuj state
                    state.ready = true
                    state.lastUpdate = os.time()
                    state.lastError = nil
                    state.value = plugin:coerceNumber(resp.json.value, 0)
                    state.status = plugin:coerceString(resp.json.status, "ok")

                    -- Aktualizuj registry (updateObject - shallow merge, fail if not exists)
                    plugin:updateObject("data", {
                        ready = true,
                        value = state.value,
                        status = state.status,
                        lastUpdate = state.lastUpdate
                    })

                    plugin:log("info", "Data updated: value=" .. state.value)

                    -- Notify sensors for expose API
                    local function notifySensor(id)
                        local sensor = plugin:get(id)
                        if sensor and sensor._notify then sensor:_notify() end
                    end
                    notifySensor("value")
                    notifySensor("ready")

                    -- Emit event z throttlingiem (max 1x na 60s)
                    plugin:emit("example:updated", {
                        value = state.value,
                        status = state.status
                    }, { throttle = 60000 })
                end

                -- Sukces - wywołaj done() bez argumentu
                done()
            end)
        end,

        -- Callback błędu (po wyczerpaniu retry)
        onError = function(err, stats)
            state.lastError = err
            plugin:log("error", "Polling failed: " .. tostring(err))
            plugin:emit("example:error", { error = err })
        end
    })

    -- Start pollera
    poller:start()
end

--------------------------------------------------------------------------------
-- PRZYKŁAD: HTTP z Basic Auth
--------------------------------------------------------------------------------
--[[
plugin:httpRequest({
    url = "https://api.example.com/data",
    headers = {
        ["Authorization"] = plugin:basicAuth("username", "password")
    },
    timeout = 10000,
    parseJson = "success"
}, function(resp)
    if resp.json then
        plugin:log("info", "Got data: " .. tostring(resp.json.value))
    end
end)
]]

--------------------------------------------------------------------------------
-- PRZYKŁAD: HTTP POST z JSON body
--------------------------------------------------------------------------------
--[[
plugin:httpRequest({
    method = "POST",
    url = "https://api.example.com/data",
    json = { key = "value", count = 123 },  -- automatycznie serializuje + Content-Type
    headers = {
        ["Authorization"] = plugin:bearerAuth("my-token")
    },
    timeout = 10000,
    parseJson = "always"  -- parsuj JSON nawet przy błędach (np. API zwraca error w JSON)
}, function(resp)
    if resp.err then
        plugin:log("error", "POST failed: " .. tostring(resp.err))
        return
    end
    plugin:log("info", "POST successful")
end)
]]

--------------------------------------------------------------------------------
-- PRZYKŁAD: HTTP POST z form data
--------------------------------------------------------------------------------
--[[
plugin:httpRequest({
    method = "POST",
    url = "https://api.example.com/login",
    form = { username = "user", password = "pass" },  -- application/x-www-form-urlencoded
    timeout = 10000
}, function(resp)
    if resp.status == 200 then
        plugin:log("info", "Login successful")
    end
end)
]]

--------------------------------------------------------------------------------
-- PRZYKŁAD: Events z throttling/coalesce
--------------------------------------------------------------------------------
--[[
-- Throttle: max 1 event per 60s (leading edge - pierwszy natychmiast)
plugin:emit("example:changed", { value = 42 }, {
    throttle = 60000
})

-- Throttle z trailing: ostatni event w oknie czasowym
plugin:emit("example:position", { x = 10, y = 20 }, {
    throttle = 1000,
    trailing = true  -- emituj ostatnią wartość po upływie throttle
})

-- Coalesce: zbierz wszystkie wartości, emituj jako tablicę
plugin:emit("example:batch", { item = "a" }, {
    throttle = 5000,
    coalesce = true,  -- zbieraj wartości
    key = "item"      -- klucz do tablicy (wynik: { items = ["a", "b", "c"] })
})
]]

--------------------------------------------------------------------------------
-- PRZYKŁAD: Registry - upsertObject vs updateObject
--------------------------------------------------------------------------------
--[[
-- upsertObject: tworzy lub nadpisuje cały obiekt
plugin:upsertObject("sensor", {
    value = 0,
    unit = "°C",
    lastUpdate = os.time()
})

-- updateObject: shallow merge, FAIL jeśli obiekt nie istnieje
local ok, err = plugin:updateObject("sensor", {
    value = 22.5,
    lastUpdate = os.time()
})
if not ok then
    plugin:log("error", "Update failed: " .. err)
end

-- updateObject z fuzzy matching (jeśli pomylisz nazwę pola)
-- plugin:updateObject("sensor", { valuee = 22.5 })
-- -> Error: unknown field 'valuee' (did you mean: 'value'?)
]]

--------------------------------------------------------------------------------
-- PRZYKŁAD: Logging z redakcją sekretów
--------------------------------------------------------------------------------
--[[
-- logSafe automatycznie redaguje sekrety (token, password, apiKey, secret, key)
plugin:logSafe("info", "Request to API", {
    url = "https://api.example.com",
    apiKey = "sk-1234567890"  -- zostanie zredagowane do "sk-12***"
}, true)  -- true = redact (domyślnie)
]]

--------------------------------------------------------------------------------
-- PRZYKŁAD: MQTT (bez zmian - stare API nadal działa)
--------------------------------------------------------------------------------
--[[
-- Publish
plugin:mqttPublish("home/example/status", "online")
plugin:mqttPublish("home/example/data", { temperature = 22.5 })
plugin:mqttPublish("home/example/state", "ON", { retain = true })

-- Subscribe
plugin:mqttSubscribe("home/example/set", function(topic, payload)
    plugin:log("info", topic .. " = " .. payload)
end)
]]

--------------------------------------------------------------------------------
-- PRZYKŁAD: Dostęp do innych pluginów (bez zmian)
--------------------------------------------------------------------------------
--[[
local weather = Plugin.getPlugin("@vclu/weather")
if weather then
    local temp = weather:getTemperature()
    plugin:log("info", "Weather temp: " .. tostring(temp))
end

-- Lista wszystkich pluginów
local all = Plugin.list()
for _, p in ipairs(all) do
    plugin:log("debug", "Loaded: " .. p.id)
end
]]

--------------------------------------------------------------------------------
-- PRZYKŁAD: Expose API (eksponowanie do Home Assistant / HomeKit)
--------------------------------------------------------------------------------
--[[
-- W user.lua użytkownik może eksponować sensory/kontrolki pluginu:

local example = Plugin.getPlugin("@vclu/example")

-- Eksponuj sensor jako number
expose(example:get("value"), "number", {
    name = "Example Value",
    area = "Techniczny",
    min = 0,
    max = 100,
    unit = "units"
})

-- Eksponuj status jako binary sensor
expose(example:get("ready"), "sensor", {
    name = "Example Ready",
    area = "Techniczny"
})

-- Eksponuj kontrolkę (z możliwością zapisu)
expose(example:get("target"), "number", {
    name = "Example Target",
    area = "Techniczny",
    min = 0,
    max = 100,
    step = 1
})

-- Plugin sam definiuje sensory/kontrolki przez:
--   plugin:sensor("id", getter)           -- tylko odczyt
--   plugin:control("id", getter, setter)  -- odczyt + zapis
--
-- Użytkownik eksponuje przez:
--   expose(plugin:get("id"), "type", { options })
]]

--------------------------------------------------------------------------------
-- PUBLIC API
--------------------------------------------------------------------------------

function plugin:isReady()
    return state.ready
end

function plugin:getLastError()
    return state.lastError
end

function plugin:getValue()
    return state.value
end

function plugin:getStatus()
    return state.status
end

function plugin:getData()
    return {
        ready = state.ready,
        value = state.value,
        status = state.status,
        lastUpdate = state.lastUpdate,
        lastError = state.lastError
    }
end

function plugin:refresh()
    if poller then
        poller:poll()
    end
end

function plugin:getStats()
    if poller then
        return poller:stats()
    end
    return {}
end

return plugin
