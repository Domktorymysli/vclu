# vCLU Code Examples — Cookbook

Code patterns for the AI agent. One pattern per category.

## Scena (scene)

Manual trigger, exposed as a scene. The user runs it from the dashboard, HomeKit, MQTT or MCP.

```lua
--- Scena: Tryb Film
local function movieMode()
    _registry:get("custom:living_main"):set(0, 20)
    _registry:get("custom:living_cover"):set(0, 0)
end

local scene = { execute = function(self) movieMode() end }
expose(scene, "scene", {
    name = "Film", path = "scene:movie", area = "Salon", group = "Sceny",
}):description("Light 20%, close the blinds. Undo: scene:normal")
```

## Automatyzacja (event-driven)

Reaction to an event (motion sensor, state change). StateBus + setTimeout.

```lua
--- Automation: kitchen motion sensor -> counter light ON for 5 min
-- state_changed goes through StateBus: one event per change, fields path/old/value
StateBus:getShared():on("state_changed", function(data)
    if data.path == "s:kitchen_motion" and data.value == 1 then
        _registry:get("custom:kitchen_counter"):set(0, 80)
        setTimeout(function()
            _registry:get("custom:kitchen_counter"):set(0, 0)
        end, 300000)
    end
end)
```

## Harmonogram (schedule)

A fixed time or a time window: use `Calendar`, not a `setInterval` that polls the clock.
It plans from the wall clock in the `datetime` zone (DST included), waits until NTP has set
the clock, and does not fire moments skipped by a clock jump.

```lua
--- Schedule: the office light at 17:00 on workdays (public holidays excluded)
-- File: modules/kalendarz/init.lua (require("kalendarz") in modules/init.lua)
local dom = Calendar.new('dom'):holidays('pl')
    :event('biuro', { every = 'workday', time = '17:00' })
    :build()
dom:on('start', 'biuro', function() _registry:get("custom:office_light"):set(0, 1) end)
return dom
```

## Calendar: events and schedules of values

One calendar per house, built in one module; other modules take it with `Calendar.get('dom')` (or
`require('kalendarz')`) and add `:on()` handlers. Building the same id again replaces the calendar,
and the handlers of the earlier one stop. Rules: `every` = `'day'`, `'workday'`,
`'weekend'`, `'holiday'`, a weekday (`'mo'`..`'su'`) or a list of them, `'month'` (with `day = 10`,
`day = -1` the last, or `on = 'first mo'` / `'last fr'` / `'last workday'`), `'year'` (with
`date = 'MM-DD'`, or `month = 10, on = 'last su'`); `dates = { 'YYYY-MM-DD', ... }` for a schedule published as a list of days (waste collection); `weeks`/`months`/`years = n` every n-th (needs `from`); `from`, `to`, `times`
bound the series; a one-off has `date = 'YYYY-MM-DD'` or a range `'a..b'` (all day). No `time`
means all day. With `:location(lat, lon)` a time can be the sun: `time = 'sunset-30m'`, `'sunrise+15m'`,
`'dawn'`/`'dusk'` (civil twilight, better for lights than sunset), a window `hours = { 'sunset', 'sunrise' }`
(ends the next morning), and the hours the sun stands on a facade: `sun = { azimuth = { 135, 225 }, elevation = 15 }`
(degrees from north, 180 south; the lowest elevation or `{ low, high }`). `dom:sun(date)` gives sunrise, sunset, dawn,
dusk; `dom:sunPosition()` the azimuth and elevation now. Use these instead of the sun-position plugin's events.

```lua
-- shade the south rooms while the sun is on that side and high enough to heat them
:schedule('rolety-poludnie', { values = { 'otwarte', 'zaslon' }, default = 'otwarte',
    { every = 'day', sun = { azimuth = { 135, 225 }, elevation = 15 }, value = 'zaslon' } })
```

`except` drops dates or ranges, `moves = { ['2026-11-10'] = '2026-11-12' }` moves one.

```lua
-- File: modules/kalendarz/init.lua, the whole calendar in one place
local dom = Calendar.new('dom'):name('Dom'):holidays('pl'):location(52.23, 21.01)
    :event('ogrod', { every = 'day', time = 'sunset-30m' })              -- garden lights before dark
    :notify(function(e) Logger:getShared():info('%s', e.text) end)   -- or an object with :send(e)
    :event('bio', { title = 'Bio', icon = 'trash', every = 'tu', weeks = 2, from = '2026-10-27',
        time = '06:30', remind = '-12h' })
    :event('szkola', { title = 'Szkoła', every = 'workday', time = '08:00', duration = '6h',
        tags = { 'dzieci' }, catchup = true, except = { '2026-12-22..2027-01-06' } })
    :event('rolety', { every = 'day', time = '21:00', jitter = '15m' })   -- presence simulation
    :schedule('taryfa', { default = 'droga', title = 'Taryfa',            -- last window wins
        values = { 'droga', 'tania' },                                     -- a value not listed is a build() error
        colors = { tania = 'success', droga = 'danger' },                  -- dashboard bars and tiles
        { every = 'workday', hours = '13:00-15:00', value = 'tania' },
        { every = 'day', hours = '22:00-06:00', value = 'tania' },
        { every = 'weekend', value = 'tania' } })
    :build()

dom:on('start', 'szkola', function(e) end)   -- e: id, title, at, ends, date, tags, catchup
dom:on('end', 'szkola', function(e) end)
dom:on('reminder', function(e) end)          -- e.offset = '-12h', e.text
dom:on('change', 'taryfa', function(v, old) end)   -- old == nil: the first value after start

dom:value('taryfa'); dom:isActive('szkola'); dom:next('bio'); dom:today(); dom:upcoming(7)
dom:nextChange('taryfa')   -- { value = 'droga', at = ts } within 8 days; same-value windows are not a change
dom:color('taryfa')        -- colour of the value now
dom:segments('taryfa', '2026-10-20')       -- one day: { {from, to, value, color}, ... }; or (id, from, to)
dom:between('2026-11-01', '2026-11-30')    -- occurrences in a range (dates or timestamps), each with date..last
dom:day('2026-11-11')                      -- { date, weekday, workday, holiday, holidayName, namedays, starts, ends }
dom:add({ title = 'Goście', date = '2026-11-14..2026-11-16' })   -- saved, survives a restart
dom:skip('bio', '2026-11-10'); dom:pause('2026-12-24..2027-01-02', { tags = { 'dzieci' } })
```

- `catchup = true`: after a start or a clock jump during an occurrence, `start` fires with `e.catchup`.
- `skip`, `pause` or `remove` of an occurrence going on fires its `end` at once (`e.cancelled`), so what its start switched on goes off. At one moment `end` fires before `start`: a light handed from one event to the next stays on.
- `build()` refuses what would otherwise pass silently: a schedule without `default`, a schedule of text without `values` (the error lists the values to copy) or a window value or `default` not in it, `colors` for a value the schedule does not have, `remind` without a sign (`'-12h'` before, `'+1h'` after), `every = 'workday'`/`'holiday'` without `:holidays('pl')`, `except`/`moves` on a day that is not one of the series, `duration` without `time`, `on('change', <event>)`. The widget refuses unknown options and schedules or tags its calendar does not have.
- `day = 31` skips months without a 31st; the last day of every month is `day = -1`.
- Until the clock is trusted `value()`/`isActive()` return nil; `:clock({ wait = false })` skips the wait.
- Test with `vclu_draft_test` and `advance`: the virtual clock drives the calendar.

## Expose z pluginu

Wrapping a plugin object. The plugin loads after the modules, so use setTimeout.

```lua
--- Expose a plugin object (delayed, because the plugin loads after the modules)
EventBus:getShared():setTimeout(function()
    local plugin = Plugin.getPlugin("@vclu/my-plugin")
    if not plugin then
        Logger:getShared():warn("[EXPOSE] Plugin not loaded")
        return
    end
    expose(plugin:get("sensor1"), "temperature", {
        name = "Czujnik temp", path = "plugin:sensor1",
        area = "Kotlownia",
    })
end, 5000)
```

## Noisy sensor: reporting threshold

A 1-Wire thermometer jumps 22.1 <-> 22.2. `filter` passes to history,
HA/MQTT and `onChange` only changes of at least `delta` from the last
reported value; `heartbeat` (s) reports a smaller change if that much time
has passed since the last report. `getValue()` returns the raw reading.

```lua
expose(_:get("CLU220000592.ONE3544"), "temperature", {
    name = "Temperatura", area = "Pokój gościnny",
    filter = { delta = 0.2, heartbeat = 600 },
})
```

## Object without history

Computed or technical values you do not want in history. Events
still go to HA, MQTT and `watch`; `lastChange()` returns nil.

```lua
expose(debugCounter, "number", { name = "Licznik debug", readonly = true, history = false })
```

## Expose with metadata

Full metadata for the AI agent: area, group, tags, description.

```lua
expose(pump, "switch", {
    name = "Pompa cyrkulacyjna",
    path = "hw:pump_circ",
    area = "Kotlownia",
    group = "Ogrzewanie",
}):tags({"critical", "dependsOn:hw:boiler", "noAutoOff"})
  :description("Domestic hot water. 3x a day for 5 min. Do not turn on when the boiler is OFF.")
```

## Walk the exposed objects

What `expose()` describes once (type, name, area, tags) can be read back instead of keeping a
second list. `ExposedObjects:list()` is the live map path -> handle: read it, never change it.

```lua
local function temperatures()
    local out = {}
    for path, h in pairs(ExposedObjects:list()) do
        if h.type == "temperature" then
            out[path] = { name = h.options.name, area = h.options.area, value = h:getValue() }
        end
    end
    return out
end
```

## MQTT Tasmota

A Tasmota device is one line with `lib/tasmota`; the topic is the device's Topic (Tasmota:
Configuration → MQTT). The library keeps the state from the device (`stat/<topic>/POWER`,
`RESULT`, `tele/<topic>/STATE`), sends commands, never sends a state from the device back, and
asks for the state on start and when the device comes back online. `expose` stays in the module.
A sensor is read-only (expose it with `readonly = true`). The device keeps Tasmota's default
Full Topic (`%prefix%/%topic%/`). The library subscribes `stat/<topic>/+` and `tele/<topic>/+`; MQTT keeps one callback per
pattern, so do not subscribe those patterns in the module (a single topic such as
`tele/<topic>/SENSOR` is fine).

So far only a Sonoff Basic (one relay, Tasmota 13.3.0) is used with vCLU, and the tests replay
its messages. Relays 2 and up, the dimmer and sensors follow Tasmota's documented messages but no
such device has been tried; colour lights, shutters and button events are not supported.

```lua
--- modules/tasmota/init.lua
local tasmota = require("lib/tasmota")

local lamp = tasmota.switch("tasmota_75088A")                    -- { relay = 2 } for POWER2
expose(lamp, "light", { path = "tasmota.biurko", name = "Lampka biurko", area = "Biuro" })

local plug = tasmota.sensor("tasmota_gniazdko", "ENERGY.Power", { unit = "W" })
expose(plug, "number", { path = "tasmota.gniazdko_moc", name = "Gniazdko moc", area = "Biuro",
    readonly = true, unit = "W", device_class = "power", state_class = "measurement" })

-- tasmota.dimmer(topic) for a dimmer, tasmota.online(topic) from LWT,
-- tasmota.devices() lists what Tasmota's own discovery announced (nothing is created from it)
```

For any other MQTT device, `MQTT` is the default client, set up in the web UI (Integrations →
MQTT → Client); a client for another broker is `MQTT:new()` with `:setHost(...)`, `:connect()`.
Subscribe callback: `function(topic, payload, qos, retain)`. In `vclu_draft_test`, the step
`{"mqtt": "stat/tasmota_75088A/POWER", "payload": "ON"}` delivers a message to the module.

### Virtual Objects

Virtual objects (`VirtualSwitch`, `VirtualDimmer`, `VirtualCover`, `VirtualSensor`, `VirtualNumber`)
provide ready-made state management, `onChange` with unsubscribe, events and integration with `expose()`.
Use them instead of hand-building tables.

| Class | _type | Methods |
|-------|-------|--------|
| `VirtualSwitch` | DOUT | `switchOn`, `switchOff`, `toggle`, `on`, `off`, `getValue`, `setValue` |
| `VirtualDimmer` | DIMMER | `switchOn`, `switchOff`, `toggle`, `getBrightness`, `setBrightness`, `getValue`, `setValue` |
| `VirtualCover` | ROLLER | `open`, `close`, `stop`, `up`, `down`, `getPosition`, `setPosition` |
| `VirtualSensor` | DIN | `getValue`, `isActive` (read-only, use `updateState()` from callback) |
| `VirtualNumber` | AnalogOUT | `getValue`, `setValue`, `getMeta()` — constructor: `VirtualNumber:new(name, {min, max, step, unit, initial, persist})` |
| `VirtualText` | TEXT | `getValue`, `setValue` — constructor: `VirtualText:new(name, {initial, maxLength, persist})` |

Every `Virtual*:new(name, opts)` takes `initial` and `persist`: with `persist = true` (or a key string) the value survives reloads and restarts, e.g. `VirtualSwitch:new("tryb_nocny", { initial = 0, persist = true })`. State of an automation itself: `local st = persist("name", { pauseUntil = 0 })`, then `st.pauseUntil = ...` is saved. Avoid persist for values changing every second (each save rewrites a KV file).

```lua
--- Dimmer: Zigbee lamp via MQTT
local lamp = VirtualDimmer:new("Lampa salon")

MQTT:subscribe("zigbee2mqtt/lampa_salon", function(topic, payload)
    local data = JSON.decode(payload)
    if data and data.brightness then
        lamp:updateState(data.brightness / 254)  -- Zigbee 0-254 -> 0.0-1.0
    end
end)

lamp:onEvent("OnChange", function(val)
    MQTT:publish("zigbee2mqtt/lampa_salon/set", JSON.encode({
        brightness = math.floor(val * 254)
    }))
end)

expose(lamp, "dimmer", { name = "Lampa salon", path = "z2m:lampa_salon", area = "Salon" })
```

### VirtualNumber with a range

Numeric value with min/max/step validation. `setValue()` clamps automatically.
`getMeta()` returns {min, max, step, unit} — useful in dashboards.

```lua
--- Target temperature with range 16-30, step 0.5
local target = VirtualNumber:new("TARGET_TEMP", {min = 16, max = 30, step = 0.5, unit = "°C"})
target:expose()

target:onChange(function(val, old)
    Logger:getShared():info("[TEMP] changed " .. old .. " -> " .. val)
    MQTT:publish("heating/target", tostring(val))
end)

-- On the dashboard: slider with a range
:slider('target', 'Docelowa', 16, 30,
    function() return target:getValue() end,
    function(v) target:setValue(v) end)
```

### VirtualText

Text value. Useful as a status flag, label, or any string.
`setValue()` converts to a string. Optional `maxLength` truncates.

```lua
--- Alarm status
local alarm_status = VirtualText:new("ALARM_STATUS", {initial = "OK"})
alarm_status:expose()

alarm_status:onChange(function(val, old)
    Logger:getShared():info("[ALARM] " .. old .. " -> " .. val)
end)

-- Setting from MQTT, an automation, a scene:
alarm_status:setValue("ARMED")
alarm_status:setValue("ALARM!")

-- On the dashboard: status widget
:status('alarm', 'Alarm', 'shield-check',
    function() return alarm_status:getValue() end)
```

## Grenton objects without a proxy: call a script on the CLU

vCLU generates proxies only for some Grenton classes (DOUT, DIN, DIMMER, LEDRGB, ROLLER, analog I/O, 1-Wire, panel sensors and buttons, thermostats). A thermostat (`THE…`) is a `RemoteThermostat`: `getValue()` is the temperature at its sensor, `getTarget()` the target in effect, `getSetpoint()`/`setSetpoint(v)` the set point by hand, `getState()`, `getMode()` (add it to Grenton → Sync for values; it is not exposed to Home Assistant or HomeKit). For anything else (a thermostat's schedule, a module without a proxy) do not invent methods: write a script in Object Manager on the CLU and call it from vCLU. Each imported CLU is a global `CLU<serial>` (a `CLU_REMOTE`, from `imports/proxy_<serial>.lua`).

```lua
--- An OM script on the CLU does the work (e.g. a thermostat's holiday mode)
-- execute(_, script) sends the script text to the CLU and returns its reply (string) or nil, err.
local reply, err = CLU220000592:execute(0, "SetThermostat(21)")
if err then Logger:getShared():warn("thermostat: " .. err) end

-- Read a feature the same way; the reply is a string (check the id and index in OM)
local hyst = tonumber(CLU220000592:execute(0, "THE0855:get(5)"))

-- No reply needed: does not wait for the CLU
CLU220000592:executeNoReply(0, "SetThermostat(19)")
```

- The first argument is ignored; the script is plain Lua run on the CLU.
- `execute` blocks the vCLU event loop until the CLU replies, up to 5 s when the CLU is offline or the reply is lost (the CLU does drop UDP packets). Do not call it in a loop or from frequent timers; prefer `executeNoReply` for commands.
- Changes the script makes to proxied objects come back as CLU reports, so history records them with actor `device:<CLU>`, not your automation. Objects without a proxy do not appear in history at all.
- In `vclu_draft_test` the script is recorded in `clu_calls` instead of being sent; `execute` returns no reply there.
- Check the script name and arguments in OM first; a typo only shows up as an error reply from the CLU.

## Dashboard (Lua-defined)

Public dashboard for a tablet/wall. Defined in Lua, available at `/d/:id`.
Widget types: button, toggle, slider, status, link, select, chart, image, group, cover, dimmer, color, calendar.
`:calendar(id, label, cal, { view = 'month' | 'day', tags = {...}, schedules = { 'taryfa' } })` shows a calendar
built with `Calendar`: a month grid and the day of a tapped date (full width); `schedules` adds a 24 h bar per schedule (`schedules = true`: all of the calendar's)
to the day view; `holidays` (holiday names, default on), `namedays` (name days in the day view, default off) and
`sun` (sunrise and sunset in the day view, default on) set what the widget may show: what is off is not sent to the
browser (a public page would give away the house's location), and a viewer can hide the rest on the widget (kept per device). A schedule as a tile: `:status('taryfa', 'Taryfa', 'lightning', function() local n = dom:nextChange('taryfa')
return (dom:value('taryfa') or '--') .. (n and (' · do ' .. datetime.format(datetime.fromTimestamp(n.at), '15:04')) or '') end, nil,
{ color = function() return dom:color('taryfa') end })`.

**IMPORTANT**: After creating a dashboard file you MUST add a `require()` in `modules/init.lua`.
Without it the dashboard will not be loaded!

```lua
--- Dashboard: example
-- File: modules/dashboards/home.lua

local r = _registry

Dashboard.new('home')
  :name('Dom')
  :icon('house-door')
  :columns(3)
  :auth('public')   -- 'public' | 'session' | 'pin'
  :poll(2)          -- polling interval in seconds
  :theme({
    accent = '#27C2A8',
    background = '#1a1a2e',
    cardBg = 'rgba(22, 33, 62, 0.85)', -- transparency looks good with a wallpaper
    text = '#eee',
    wallpaper = '/static/img/wallpaper.jpg', -- optional wallpaper (URL)
  })
  -- Header bar — compact bar with values (before the sections!)
  :header()
    :status('temp_out', 'Zewn', 'thermometer-half',
      function() return r:get("CLU212345678.ANA4201"):getValue() end, '°C')
    :status('hum_out', 'Wilg', 'droplet',
      function() return r:get("CLU212345678.ANA4202"):getValue() end, '%')
  -- Salon — 3 columns (default)
  :section('Salon', 'lamp')
    :toggle('salon_kanapa', 'Kanapa', 'lamp',
      function() return r:get("CLU220000592.DOU0789"):getValue() end,
      function(v)
        local obj = r:get("CLU220000592.DOU0789")
        if v then obj:switchOn() else obj:switchOff() end
      end)
    :toggle('salon_sufit', 'Sufit', 'lightbulb',
      function() return r:get("CLU220000592.DOU0790"):getValue() end,
      function(v)
        local obj = r:get("CLU220000592.DOU0790")
        if v then obj:switchOn() else obj:switchOff() end
      end)
    :button('movie', 'Movie', 'film', function()
      r:get("scene:salon_movie"):execute()
    end)
  -- Temperatures — 4 columns (compact statuses)
  :section('Temperatury', 'thermometer-half', { columns = 4 })
    :status('temp_salon', 'Salon', 'thermometer-half',
      function() return r:get("CLU212345678.ANA4201"):getValue() end, '°C')
    :status('temp_kuchnia', 'Kuchnia', 'thermometer-half',
      function() return r:get("CLU212345678.ANA4203"):getValue() end, '°C')
    :status('temp_sypialnia', 'Sypialnia', 'thermometer-half',
      function() return r:get("CLU212345678.ANA4204"):getValue() end, '°C')
    :status('temp_biuro', 'Biuro', 'thermometer-half',
      function() return r:get("CLU212345678.ANA4205"):getValue() end, '°C')
  -- Tools — 2 columns (wide links)
  :section('Narzedzia', 'tools', { columns = 2 })
    :link('grafana', 'Grafana', 'graph-up', 'http://192.168.1.50:3000')
    :link('cam1', 'Kamera front', 'camera-video', 'http://192.168.1.100/stream', { color = 'info' })
  :build()

-- Widget options (optional last argument):
-- { span = 2 }           -- widget takes 2 columns
-- { size = 'large' }     -- larger widget
-- { color = 'warning' }  -- color: accent/success/warning/danger or hex
-- { disabled = function() return true end }  -- conditionally disabled

-- Section options (3rd argument of :section()):
-- { columns = 4 }        -- override the column count for this section
```

### Cover widget (blind)

Buttons up / stop / down, and a 0-100% position slider when `set` is given.

```lua
local roleta = r:get("CLU220000592.ROL0001")

-- inside a Dashboard.new(...):section(...) chain
:cover('roleta_salon', 'Roleta salon', 'window', {
    getPosition = function() return roleta:getPosition() end, -- slider and "NN%" label
    getState    = function() return roleta:getState() end,    -- lights the moving button
    open  = function() roleta:open() end,
    close = function() roleta:close() end,
    stop  = function() roleta:stop() end,
    set   = function(p) roleta:setPosition(p) end,            -- without it: buttons only
})

-- A fixed position on one tap: a plain button
:button('roleta_pol', 'Pół', 'window', function() roleta:setPosition(50) end)
```

- **Scale:** 0 = closed .. 100 = open, the same as `getPosition`/`setPosition`, the Grenton app,
  Home Assistant and HomeKit.
- **No position:** a Grenton roller without position (class 11, firmware fv01) returns nil from
  `getPosition()`; the slider then shows `--`. Leave out `getPosition` and `set` for such a roller
  and keep the buttons.
- **getState:** 0 = stopped, 1 = up, 2 = down. Without it the stop button stays lit.

### Select widget (dropdown)

Dropdown for choosing from a list of options. Two option formats: plain strings or {value, label}.

```lua
-- Plain strings (value = label)
:select('mode', 'Tryb', 'gear', {'Auto', 'Low', 'Medium', 'High'},
    function() return currentMode end,
    function(v) setMode(v); return "Ustawiono: " .. v end)

-- {value, label} objects
:select('fan', 'Wentylator', 'fan',
    {{value='off', label='Wylaczony'}, {value='low', label='Niski'}, {value='high', label='Wysoki'}},
    function() return 'low' end,
    function(v) setFanSpeed(v) end,
    {span = 2})
```

### Image widget

Displays an image from a URL. Optional auto-refresh (e.g. an IP camera).

```lua
:image('cam', 'Kamera front', 'http://192.168.1.100/snapshot.jpg',
    {refresh = 10, height = 200})  -- refresh every 10s, height 200px

-- The URL can also be a function (dynamic):
:image('weather', 'Mapa pogody', function() return getWeatherMapUrl() end)
```

### Group label (label/separator)

Visual separator in a section — spans the full width of the grid.

```lua
:section('Sterowanie', 'sliders')
    :group('Oswietlenie', 'lamp')
    :toggle('main', 'Glowne', 'lightbulb', ...)
    :toggle('led', 'LED', 'lamp', ...)
    :group('Niebezpieczne')
    :button('reset', 'Reset', 'power', fn, {confirm = "Czy na pewno?", color = 'danger'})
```

### Confirm na przycisku

The `confirm` option on a button shows a confirmation dialog before executing.

```lua
:button('reset', 'Resetuj system', 'power',
    function() doReset(); return "Zresetowano" end,
    {confirm = "Czy na pewno chcesz zresetowac?", color = 'danger'})
```

### Toast (notification)

Callbacks (onTap, onToggle, onSet, onSelect) can return a string.
It will be shown as a short notification (toast) on the dashboard.

```lua
:button('movie', 'Tryb Film', 'film', function()
    activateMovieMode()
    return "Movie mode enabled"   -- shows a toast
end)
```

### Chart widget

The `chart` widget displays a chart of data from the Metrics system. The data is fetched
directly by the browser from `/api/metrics/data`, NOT through Lua polling.

```lua
:chart('outside', 'Temp. zewnetrzna', 'salda:outsideAir',
    {unit = '°C', range = '2h', span = 2, color = 'info', fill = true})
-- range: "30m", "2h", "24h", "7d"
-- refresh: seconds (default auto by range: 2h->60s, 24h->300s)
-- fill: true = fill under the line
-- min, max: fixed Y axis limits
```

### Sparkline on a status widget

Small chart under the status value. Takes data from Metrics.

```lua
:status('salon', 'Salon', 'thermometer-half',
    function() return r["CLU212345678.ANA4201"]:getValue() end, '°C',
    {spark = 'temp:salon', sparkRange = '2h', color = '#FF6B6B'})
```

### Colour that follows the value (status)

`color` on a status widget (section or header) can be a function. It is called on every poll;
the tile's icon and value take the returned colour (in the header, the icon), and `nil` means
no colour. Other widget types take only a string colour. Keep the
threshold in your own Lua. An error in the function leaves the value and drops the colour
(logged once).

```lua
:status('sms', 'SMS', 'chat-dots', function() return smsLeft end, '',
    {color = function()
        if smsLeft < 20 then return 'danger' end  -- red below 20
    end})
```

### Dashboard dynamiczny (Dashboard.dynamic)

A dashboard that changes depending on the Lua runtime state — time of day,
scene state, global variable values. The builder function is called
on EVERY API request (every ~30s), so the dashboard reacts to changes
without reloading the module.

**Difference vs Dashboard.new()**: `Dashboard.new():build()` builds the dashboard once
when the module loads and is static. `Dashboard.dynamic()` rebuilds the
dashboard live.

```lua
--- Dynamic dashboard: changes by time of day
-- File: modules/dashboards/home.lua

local r = _registry

-- Themes by time of day
local themes = {
    morning   = { accent = '#FF9F43', background = 'linear-gradient(135deg, #1a1a2e, #2d1b3d, #1a1a2e)' },
    afternoon = { accent = '#4ECDC4', background = 'linear-gradient(135deg, #0f2027, #203a43, #2c5364)' },
    evening   = { accent = '#E17055', background = 'linear-gradient(135deg, #1a1a2e, #3d1b1b, #1a1a2e)' },
    night     = { accent = '#6C5CE7', background = 'linear-gradient(135deg, #0a0a1a, #151530, #0a0a1a)' },
}

Dashboard.dynamic('home', function(d)
    local period = Dashboard.timeOfDay()  -- "morning"/"afternoon"/"evening"/"night"

    d:name(period == 'evening' and 'Dobry wieczor' or 'Dzien dobry')
     :icon(period == 'night' and 'moon-stars' or 'sun')
     :columns(3)
     :auth('public')
     :theme(themes[period])

    -- Always visible: temperatures
    d:section('Temperatury', 'thermometer-half')
        :status('salon', 'Salon', 'thermometer-half',
            function() return r["CLU212345678.ANA4201"]:getValue() end, '°C',
            {spark = 'temp:salon', sparkRange = '2h'})

    -- Afternoon/evening: show the office lights
    if period == 'afternoon' or period == 'evening' then
        d:section('Biuro', 'lightbulb')
            :toggle('biuro_led', 'LED', 'lamp',
                function() return r["CLU212345678.DOU3328"]:getValue() == 1 end,
                function(v)
                    if v then r["CLU212345678.DOU3328"]:switchOn()
                    else r["CLU212345678.DOU3328"]:switchOff() end
                end)
    end

    -- Morning: show the overnight temperature chart
    if period == 'morning' then
        d:section('Temperatura w nocy', 'graph-up')
            :chart('night', 'Salon — 24h', 'temp:salon',
                {unit = '°C', range = '24h', span = 3, fill = true})
    end
end)
```

#### Helper functions

```lua
Dashboard.timeOfDay()
-- Returns: "morning" (6-12), "afternoon" (12-17), "evening" (17-22), "night" (22-6)

Dashboard.periodLabel("morning")
-- Returns: "Rano" (in Polish: Rano/Popoludnie/Wieczor/Noc)
```

#### Scene-reactive dashboard

A dashboard can react to any Lua state — e.g. a global variable
set by a scene:

```lua
-- In the scene (modules/scenes/movie.lua):
_G.scene_state = _G.scene_state or {}
_G.scene_state.movie = true

-- In the dashboard:
Dashboard.dynamic('salon', function(d)
    local isMovie = (_G.scene_state or {}).movie == true

    if isMovie then
        d:name('Kino domowe'):icon('film')
         :theme({ accent = '#E17055', background = '#0a0a1a' })
        d:section('Kino', 'film')
            :button('stop', 'Zakoncz film', 'stop-circle', function()
                _G.scene_state.movie = false
            end)
    else
        d:name('Salon'):icon('house')
        d:section('Sterowanie', 'lamp')
            :toggle('light', 'Swiatlo', 'lightbulb', ...)
            :button('movie', 'Film', 'film', function()
                _G.scene_state.movie = true
                -- ... run the scene ...
            end)
    end
end)
```

### Custom CSS (modules/custom.css)

The `modules/custom.css` file lets you override dashboard styles.
Editable through the Lua editor or the MCP draft pipeline.

```css
/* modules/custom.css */
:root {
    --dash-accent: #FF6B6B;
    --dash-bg: #0a0a2e;
}
.dash-widget { border-radius: 20px; }
```

```lua
-- modules/init.lua — ADD THE REQUIRE:
require("dashboards/home")
```

### Convenience methods (use instead of execute/get)

| Module | Methods |
|-------|--------|
| DOUT (switch) | `switchOn(time)`, `switchOff(time)`, `toggle(time)`, `getValue()`, `isOn()` |
| DIN (sensor) | `getValue()`, `isActive()` |
| ANALOG_OUT | `setValue(val)`, `getValue()`, `getPercent()` |
| ANALOG_IN | `getValue()`, `getPercent()` |
| Timer | `start()`, `stop()`, `pause()`, `isRunning()` |

### Theme options

| Key | Description | Example |
|-------|------|---------|
| `accent` | Accent color (buttons, sliders, active toggles) | `'#27C2A8'` |
| `background` | Background color | `'#1a1a2e'` |
| `cardBg` | Card/widget color (use rgba for transparency with a wallpaper) | `'rgba(22,33,62,0.85)'` |
| `text` | Text color | `'#eee'` |
| `wallpaper` | Background wallpaper URL (full screen, cover) | `'/static/img/bg.jpg'` |

### Available icons (Bootstrap Icons)

Dashboards use Bootstrap Icons. The icon name is what follows `bi-`.
Full list: https://icons.getbootstrap.com/

**Smart home — most commonly used:**

| Icon | Name | Use |
|-------|-------|--------|
| house | `house`, `house-door`, `house-fill` | Main dashboard |
| lamp | `lamp`, `lamp-fill`, `lightbulb`, `lightbulb-fill` | Lights |
| sun | `sun`, `sun-fill`, `brightness-high` | Outdoor lights |
| moon | `moon`, `moon-fill`, `moon-stars` | Bedroom, night mode |
| thermometer | `thermometer-half`, `thermometer-high`, `thermometer-low` | Temperature |
| water | `droplet`, `droplet-fill`, `moisture` | Watering, humidity |
| fan | `fan`, `wind` | Ventilation |
| heating | `fire`, `thermometer-sun` | Furnace, heating |
| switch | `toggle-on`, `toggle-off`, `power` | Switches |
| door | `door-open`, `door-closed` | Doors, entrance |
| gate | `box-arrow-up`, `box-arrow-down`, `arrow-up`, `arrow-down` | Blinds, gates |
| garage | `car-front`, `truck` | Garage |
| camera | `camera-video`, `eye`, `shield-check` | Security |
| alarm | `bell`, `bell-fill`, `exclamation-triangle` | Alarm |
| lock | `lock`, `unlock`, `key` | Locks |
| socket | `plug`, `outlet` | Sockets |
| kitchen | `cup-hot`, `cup-hot-fill` | Kitchen |
| music | `music-note`, `volume-up`, `speaker` | Audio |
| film | `film`, `tv`, `display` | Media, home cinema |
| office | `pc-display`, `laptop` | Office |
| garden | `tree`, `flower1`, `flower2` | Garden |
| floors | `layers`, `stack` | Floors, groups |
| clock | `clock`, `alarm`, `stopwatch` | Timers |
| wifi | `wifi`, `broadcast`, `router` | Network |
| robot | `robot`, `cpu` | Automation |
| chart | `graph-up`, `bar-chart`, `activity` | Charts, statistics |
| info | `info-circle`, `question-circle` | Information |
| grid | `grid`, `grid-3x3` | Dashboard |

## Dashboard — tab navigation (tabGroup)

Dashboards in the same group show a tab bar at the top.
`:tabGroup(name)` — adds a dashboard to a group. Can be called multiple times.
Groups with 1 dashboard are hidden. Do NOT confuse with `:group()` (a visual label in a section).

```lua
--- 3 dashboards in one group "dom"
Dashboard.new('home')
    :name('Dom'):icon('house'):tabGroup('dom'):build()

Dashboard.new('cameras')
    :name('Kamery'):icon('camera-video'):tabGroup('dom'):build()

Dashboard.new('temperatures')
    :name('Temperatury'):icon('thermometer-half'):tabGroup('dom'):build()

-- Result: each of these dashboards shows at the top:
--   [Dom]  [Kamery]  [Temperatury]
-- Clicking switches to the chosen dashboard.

-- Dashboard in multiple groups:
Dashboard.new('settings')
    :name('Ustawienia'):icon('gear')
    :tabGroup('dom')     -- visible in group "dom"
    :tabGroup('admin')   -- and in group "admin"
    :build()
```

## Grenton Smart Panel (screen drawn by vCLU)

A Smart Panel (on the TF-Bus of a CLU, or a Smart Panel WiFi, which is its own CLU) shows
what a module draws with `require("lib/panel")`. The panel sends its presses (and gestures, proximity,
wake) through its CLU: every button's OnClick is set in Object Manager to call the script `click`
on vCLU with `("<panel or button id>", "btn1")`, and OnLongPress with `"btn1:long"`. The OM import
creates the panel as `RemotePanel` (class 29) with its buttons attached.

```lua
-- modules/panel_biuro/init.lua — one panel = one module
local Panel = require("lib/panel")
local temperatury = require("temperatury")       -- read values from domain modules

Panel.lang("pl")                                 -- the library's own texts in Polish

local zadana = VirtualNumber:new("zadana_biuro", { initial = 21, persist = true })

if not Panel.find("PAN1085") then return nil end -- no OM import of the panel yet

-- a room from expose(..., { area = "Biuro" }) in the house model: lights, the dimmer
-- and the temperature, named as in HomeKit, each with its widget; refilled each time it
-- opens, rows added by hand come after the devices
local biuro = Panel.area("Biuro")
    :number("Zadana", function() return zadana:getValue() end,
        function(v) zadana:setValue(v) end, { step = 0.5, min = 19, max = 22, format = "%.1f" })

-- rows by hand: add() picks the widget by what the object is (switch, dimmer, cover,
-- sensor); a path missing from the registry adds no row, an empty menu is left out
local bramy = Panel.menu("Bramy")
    :gate("Brama", "CLU220000592.DIN0095", { open = 0 }) -- an input wired to "closed"
    :add("Roleta", "CLU220000592.ROL1234")              -- up opens, down closes, OK stops

-- a floor from expose(..., { area = "Salon", floor = "Parter" }): its areas, in this order
-- first; expose(..., { panel = false }) keeps an object off the panel, panel = true puts
-- on one that is left out by default (switches such as the intercom)
local parter = Panel.floor("Parter", { order = { "Wiatrołap", "Hol", "Salon" } })

-- the panel from the OM import by id; this also puts back click(), which the
-- user.lua from Object Manager hides with an empty one
Panel.new("PAN1085")
    :buttons{ tl = "btn1", tr = "btn2", bl = "btn3", br = "btn4" }
    :back("tr")                                   -- back top right, OK below it (the default)
    :home(Panel.menu("Dom", { biuro, bramy, parter })
        :action("Czyszczenie", Panel.cleaning(30)))
    :idle(Panel.idle{ { "Biuro", temperatury.all[1].read, "%.1f" } })
    :sleepAfter(30)                               -- idle screen after 30 s without input
```

The corners mean the same on every page: up and down on the left (`+` and `-` while editing
a value), back top right (`<`) and OK bottom right (`>`). Keep the default so every
panel in a house works alike; the LEDs light the corners that do something. A page type of your own: `Panel.register(name, factory)`, where
a page is `{ draw = function(screen, panel) ... end, keys = { up = { "+" } }, input = function(role, panel) ... end }`
and `screen` has `text(x, y, s, font)`, `textRight`, `textCenter`, `line`, `rule(y)`, `icon(x, y, "temp")`.

What the panel can do (measured on a Smart Panel WiFi): 128 × 64 monochrome; fonts 1-3 are
10, 14 and 28 px high, y is the top; 15 characters per text; Polish letters are replaced
(`ł` → `l`); boxes are not filled, so text never inverts; icons are the `.bmp` files on the
panel's card (`temp`, `fan`, `sun`, `lamp1on`... 64 × 32; `arrups`, `arrdowns` 16 × 16).
Draw only through `lib/panel`: it sends each frame in short spaced messages, one expression
each, which is what the panel's CLU accepts. Do not loop `PANEL:execute(...)` yourself.

Test a panel module in `vclu_draft_test` with `click` steps; the frames are in `clu_calls`:

```json
[{"advance": 1}, {"click": "PAN1085", "input": "btn3"}, {"advance": 1}]
```

## Directory convention

Put code in modules, not loose files in `modules/`. Read `modules/init.lua`
first and follow the layout that is already there.

```
modules/init.lua           -- entry point, requires everything below
modules/<domain>/init.lua  -- one domain (plugin, device, room): readings,
                           -- metrics, exports, automations; returns a table
modules/<domain>/*.lua     -- more files of the same domain: require("<domain>/file")
modules/dashboards/        -- dashboards (Lua-defined UI), one per domain
modules/scenes/            -- scenes (manual triggers)
modules/helpers/           -- helper functions shared by domains
```

A dashboard only draws. It reads from its domain module, not from the plugin
directly, so the dashboard and every export (InfluxDB, Home Assistant) show the
same values:

```lua
-- modules/pogoda/init.lua
local weather = Plugin.getPlugin("@vclu/weather")
if not weather then return nil end

local M = {}
function M.temp()
    local d = weather:getData()
    return d.ready and d.temp or nil
end
metric("pogoda:temp", M.temp, { unit = "°C", interval = 600 })
return M
```

```lua
-- modules/dashboards/pogoda.lua
local pogoda = require("pogoda")
if type(pogoda) ~= "table" then return end   -- module skipped: require gives true

Dashboard.new("pogoda"):name("Pogoda"):icon("cloud-sun")
    :header()
    :status("temp", "Temperatura", "thermometer-half", pogoda.temp, "°C")
-- ... sections, then build()
```

Each file = one module. Name = snake_case.
user.lua and om.lua are OM files — NEVER modify them, they will be overwritten.

**IMPORTANT**: Flat `modules/*.lua` files load by themselves, alphabetically after
`init.lua`; keep them for short standalone scripts. A `require()` of such a file in `init.lua`
loads it earlier, and it still runs once; what the file returns is what `require()` gets.
Everything in a subdirectory loads only through a `require()` in `modules/init.lua`, and
`require("<domain>")` loads `modules/<domain>/init.lua`. Never put `modules/<name>.lua` next to
`modules/<name>/`: `require("<name>")` finds the flat file first. Do not name a file like a
runtime module (`Timer`, `TypeMap`, `string`...): it runs, but `require()` of that name keeps
the runtime module.

```lua
-- modules/init.lua (example)
require("pogoda")              -- modules/pogoda/init.lua
require("scenes/salon_movie")
require("automations/motion_kitchen")
require("dashboards/pogoda")
require("dashboards/home")
```
