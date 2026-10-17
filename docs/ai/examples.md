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

Periodic condition checks. setInterval.

```lua
--- Schedule: turn on the light every day at 17:00
local fired_today = ""
EventBus:getShared():setInterval(function()
    local t = os.date("*t")
    local today = os.date("%Y-%m-%d")
    if t.hour == 17 and t.min == 0 and fired_today ~= today then
        fired_today = today
        _registry:get("custom:office_light"):set(0, 1)
    end
end, 30000)
```

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

## MQTT Tasmota

Controlling a Tasmota device over MQTT. `MQTT` is the default client, set up in the web UI (Integrations → MQTT → Client); a client for another broker is `MQTT:new()` with `:setHost(...)`, `:connect()`.
Subscribe callback: `function(topic, payload, qos, retain)`.
Use `VirtualSwitch` instead of hand-building a table with `_cbs`, `get()`, `set()`.

```lua
--- Tasmota: desk lamp (switch with state)
local lamp = VirtualSwitch:new("Lampka biurko")

-- State feedback from Tasmota
MQTT:subscribe("stat/tasmota_desk/POWER", function(topic, payload)
    lamp:updateState(payload == "ON" and 1 or 0)
end)

-- Commands to Tasmota
lamp:onSwitchOn(function() MQTT:publish("cmnd/tasmota_desk/Power", "ON") end)
lamp:onSwitchOff(function() MQTT:publish("cmnd/tasmota_desk/Power", "OFF") end)

expose(lamp, "switch", { name = "Lampka biurko", path = "tasmota:desk", area = "Biuro" })
```

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

vCLU generates proxies only for some Grenton classes (DOUT, DIN, DIMMER, LEDRGB, ROLLER, analog I/O, 1-Wire, panel sensors). For anything else (a thermostat, a module without a proxy) do not invent methods: write a script in Object Manager on the CLU and call it from vCLU. Each imported CLU is a global `CLU<serial>` (a `CLU_REMOTE`, from `imports/proxy_<serial>.lua`).

```lua
--- Thermostat on the CLU: OM script SetThermostat(temp) does the work
-- execute(_, script) sends the script text to the CLU and returns its reply (string) or nil, err.
local reply, err = CLU220000592:execute(0, "SetThermostat(21)")
if err then Logger:getShared():warn("thermostat: " .. err) end

-- Read a feature the same way; the reply is a string
-- (THE0855 = thermostat id, 14 = current temperature; check both in OM)
local temp = tonumber(CLU220000592:execute(0, "THE0855:get(14)"))

-- No reply needed: does not wait for the CLU
CLU220000592:executeNoReply(0, "SetThermostat(19)")
```

- The first argument is ignored; the script is plain Lua run on the CLU.
- `execute` blocks the vCLU event loop until the CLU replies, up to 5 s when the CLU is offline or the reply is lost (the CLU does drop UDP packets). Do not call it in a loop or from frequent timers; prefer `executeNoReply` for commands.
- Changes the script makes to proxied objects come back as CLU reports, so history records them with actor `device:<CLU>`, not your automation. Objects without a proxy (the thermostat itself) do not appear in history at all.
- In `vclu_draft_test` the script is recorded in `clu_calls` instead of being sent; `execute` returns no reply there.
- Check the script name and arguments in OM first; a typo only shows up as an error reply from the CLU.

## Dashboard (Lua-defined)

Public dashboard for a tablet/wall. Defined in Lua, available at `/d/:id`.
Widget types: button, toggle, slider, status, link, select, chart, image, group, cover, dimmer, color.

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
what a module draws with `require("lib/panel")`. Object Manager only forwards the panel's
events: on every button's OnClick it calls the script `click` on vCLU with
`("<panel or button id>", "btn1")`, and on OnLongPress with `"btn1:long"`. The OM import
creates the panel as `RemotePanel` (class 29) with its buttons attached.

```lua
-- modules/panel_biuro/init.lua — one panel = one module
local Panel = require("lib/panel")
local temperatury = require("temperatury")       -- read values from domain modules

local PANEL = _:get("CLU470000100.PAN1085")      -- the PANEL object from the OM import
if not PANEL then return nil end

-- OM uploads an empty click(panel, input) to user.lua; modules load after it,
-- so put the runtime entry back
click = RemotePanel.click

local lampa = _:get("CLU221000290.DOU4157")
local zadana = VirtualNumber:new("zadana_biuro", { initial = 21, persist = true })

Panel.new(PANEL)
    :buttons{ tl = "btn1", tr = "btn2", bl = "btn3", br = "btn4" }
    :back("br")                                   -- this corner is back on every page
    :home(Panel.menu("Biuro")
        :toggle("Swiatlo", lampa)                 -- OK switches it, the row follows the object
        :number("Zadana", function() return zadana:getValue() end,
            function(v) zadana:setValue(v) end, { step = 0.5, min = 19, max = 22, format = "%.1f" })
        :submenu("Temperatury", Panel.menu("Temperatury")
            :value("Biuro", temperatury.all[1].read, "%.1f"))
        :action("Czyszczenie", Panel.cleaning(30)))
    :idle(Panel.idle{ { "Biuro", temperatury.all[1].read, "%.1f" } })
    :sleepAfter(30)                               -- idle screen after 30 s without input
```

The corners get their meaning from the page (up, down, ok, back) and show a hint; their LEDs
light when they do something. A page type of your own: `Panel.register(name, factory)`, where
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
