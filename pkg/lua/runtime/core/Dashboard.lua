--- Dashboard - Lua-defined dashboards for tablet/wall-mounted control panels
-- Builder pattern API for defining dashboards entirely in Lua.
-- Each dashboard is a public page at /d/:id with per-dashboard auth.
-- @module Dashboard

-- Global registry (cleared on module reload, rebuilt by build() calls)
_dashboards = _dashboards or {}
_dashboardOrder = _dashboardOrder or {}

-- ==========================================================================
-- SectionHandle - proxy for adding widgets to a specific section
-- ==========================================================================

local SectionHandle = {}
SectionHandle.__index = SectionHandle

local CALENDAR_WIDGET_KEYS = { view = true, tags = true, schedules = true, holidays = true, namedays = true, sun = true, span = true }

-- "a", "b": the sorted keys of a set, or "none".
local function quoted(set)
    local out = {}
    for k in pairs(set) do out[#out + 1] = '"' .. tostring(k) .. '"' end
    table.sort(out)
    return #out > 0 and table.concat(out, ', ') or 'none'
end

-- A calendar widget keeps the calendar's id, so a dashboard may be built before the calendar.
local function calendarWidget(id, label, cal, opts)
    local calId = type(cal) == 'table' and cal.id or cal
    if type(calId) ~= 'string' then
        error('calendar widget "' .. tostring(id) .. '": expected a calendar or its id', 3)
    end
    opts = opts or {}
    local where = 'calendar widget "' .. tostring(id) .. '"'
    for k in pairs(opts) do
        if not CALENDAR_WIDGET_KEYS[k] then
            error(where .. ': unknown option "' .. tostring(k) .. '" (view, tags, schedules, holidays, namedays, sun, span)', 3)
        end
    end
    if opts.view ~= nil and opts.view ~= 'month' and opts.view ~= 'day' then
        error(where .. ': view must be "month" or "day"', 3)
    end
    for _, k in ipairs({ 'holidays', 'namedays', 'sun' }) do
        if opts[k] ~= nil and type(opts[k]) ~= 'boolean' then error(where .. ': ' .. k .. ' must be true or false', 3) end
    end
    if opts.schedules ~= nil and opts.schedules ~= true and type(opts.schedules) ~= 'table' then
        error(where .. ': schedules expects a list of schedule ids, or true for all', 3)
    end
    -- a calendar built already (the usual order: its module, then the dashboard) is checked now
    local cal = Calendar and Calendar.get(calId)
    if cal then
        local info = cal:info()
        local known = {}
        for _, sc in ipairs(info.schedules) do known[sc.id] = true end
        if type(opts.schedules) == 'table' then
            for _, sid in ipairs(opts.schedules) do
                if not known[sid] then
                    error(where .. ': calendar "' .. calId .. '" has no schedule "' .. tostring(sid) .. '" (schedules: ' .. quoted(known) .. ')', 3)
                end
            end
        end
        local tags = {}
        for _, ev in ipairs(info.events) do for _, t in ipairs(ev.tags or {}) do tags[t] = true end end
        for _, t in ipairs(type(opts.tags) == 'table' and opts.tags or { opts.tags }) do
            if not tags[t] then
                error(where .. ': no event of calendar "' .. calId .. '" has the tag "' .. tostring(t) .. '" (tags: ' .. quoted(tags) .. ')', 3)
            end
        end
    end
    return { id = id, type = 'calendar', label = label, calendar = calId, opts = opts }
end

-- A colour widget given a light object (RemoteRGBLight) instead of fns. Reads come from what
-- the light keeps (a get() would ask the CLU on every poll); brightness and white are 0..100.
local function lightColorFns(light)
    local function pct(v) v = tonumber(v); return v and math.floor(v * 100 + 0.5) end
    local fns = {
        getColor = function()
            local r, g, b = light:getColor()
            if r then return { r = r, g = g, b = b } end
        end,
        setColor = function(r, g, b) light:setColor(r, g, b) end,
        get = function() return pct(light:getValue()) end,
        set = function(p) light:setValue(p / 100) end,
        switchOn = function() light:switchOn() end,
        switchOff = function() light:switchOff() end,
    }
    if light._hasWhite then
        fns.getWhite = function() return light._white and pct(light._white / 255) end
        fns.setWhite = function(p) light:setWhite(p * 255 / 100) end
    end
    return fns
end

local function colorFns(fns)
    if type(fns) == 'table' and fns._objId and type(fns.setColor) == 'function' then
        return lightColorFns(fns)
    end
    return fns
end

-- ==========================================================================
-- Rooms from the house model (section:area, dash:floor)
-- ==========================================================================

-- Kinds that have a widget; a thermostat (climate) has none yet
local AREA_KINDS = { 'light', 'switch', 'dimmer', 'cover', 'temperature', 'humidity', 'lock' }
local AREA_DEFAULT_KINDS = { 'light', 'dimmer', 'cover', 'temperature', 'humidity' }
local AREA_KEYS = { 'kinds', 'skip' }
local FLOOR_KEYS = { 'kinds', 'order', 'skip', 'titles' }
local AREA_ID = 'area:'

local function isList(t, kind)
    if type(t) ~= 'table' then return false end
    for k, v in pairs(t) do
        if type(k) ~= 'number' or type(v) ~= kind then return false end
    end
    return true
end

local function has(list, v)
    for _, x in ipairs(list) do if x == v then return true end end
    return false
end

-- Checked options of section:area / dash:floor; errors name the module's line (level)
local function areaSpec(where, opts, keys, level)
    opts = opts or {}
    if type(opts) ~= 'table' then error(where .. ': options must be a table', level) end
    for k in pairs(opts) do
        if not has(keys, k) then
            error(where .. ': unknown option "' .. tostring(k) .. '" (' .. table.concat(keys, ', ') .. ')', level)
        end
    end
    if opts.kinds ~= nil then
        if not isList(opts.kinds, 'string') then error(where .. ': kinds expects a list of kinds', level) end
        for _, k in ipairs(opts.kinds) do
            if not has(AREA_KINDS, k) then
                error(where .. ': kind "' .. k .. '" has no widget (' .. table.concat(AREA_KINDS, ', ') .. ')', level)
            end
        end
    end
    if opts.skip ~= nil and not isList(opts.skip, 'string') then error(where .. ': skip expects a list of paths', level) end
    if opts.order ~= nil and not isList(opts.order, 'string') then error(where .. ': order expects a list of areas', level) end
    if opts.titles ~= nil and type(opts.titles) ~= 'table' then error(where .. ': titles expects { [area] = title }', level) end
    return { kinds = opts.kinds or AREA_DEFAULT_KINDS, skip = opts.skip, order = opts.order, titles = opts.titles or {} }
end

local function isOn(v)
    local n = tonumber(v)
    return v == true or (n ~= nil and n > 0)
end

-- Paths already warned about: dashboard = true on a kind without a widget
local _noWidgetWarned = {}

-- A widget for an exposed object, or nil for a kind without one
local function areaWidget(h, id)
    local o, kind = h.object, h.type
    if type(o) ~= 'table' then return nil end
    local function m(name)
        if type(o[name]) == 'function' then return function(...) return o[name](o, ...) end end
    end
    local w = { id = id, label = h.options.name or h.path,
        opts = { disabled = function() return h.options.readonly == true end } }
    local rgb = o._objId and type(o.setColor) == 'function' and type(o.getColor) == 'function'
    local dims = type(o.getBrightness) == 'function' and type(o.setBrightness) == 'function'
    if (kind == 'light' or kind == 'dimmer') and rgb then
        w.type, w.icon, w.fns = 'color', 'palette', lightColorFns(o)
    elseif kind == 'dimmer' or (kind == 'light' and dims) then
        w.type, w.icon = 'dimmer', 'lightbulb'
        w.fns = {
            get = function()
                if dims then return o:getBrightness() end
                local v = tonumber(h:getValue())
                return v and math.floor(v * 100 + 0.5)
            end,
            set = function(p) if dims then o:setBrightness(p) else o:setValue(p / 100) end end,
            switchOn = m('switchOn'), switchOff = m('switchOff'),
        }
    elseif kind == 'light' or kind == 'switch' or kind == 'lock' then
        w.type = 'toggle'
        w.icon = (kind == 'lock' and 'lock') or (kind == 'switch' and 'power') or 'lightbulb'
        w.getValue = function() return isOn(h:getValue()) end
        w.onToggle = function(v)
            if v and o.switchOn then o:switchOn()
            elseif not v and o.switchOff then o:switchOff()
            elseif o.setValue then o:setValue(v and 1 or 0)
            elseif not h:setValue(v and 1 or 0) then error('cannot switch ' .. h.path) end
        end
    elseif kind == 'cover' then
        w.type, w.icon = 'cover', 'window'
        -- a Grenton roller keeps its movement aside; without a position the state is the movement
        w.fns = { getPosition = m('getPosition'), getState = o._hasPosition and m('getState') or nil,
            open = m('open'), close = m('close'), stop = m('stop'), set = m('setPosition') }
    elseif kind == 'temperature' or kind == 'humidity' then
        w.type = 'status'
        w.icon = kind == 'temperature' and 'thermometer-half' or 'droplet'
        w.unit = kind == 'temperature' and '°C' or '%'
        w.getValue = function() return h:getValue() end
    else
        if h.options.dashboard == true and not _noWidgetWarned[h.path] then
            _noWidgetWarned[h.path] = true
            Logger:getShared():warn("[DASHBOARD] %s: dashboard = true, but a %s has no widget (%s)",
                h.path, tostring(kind), table.concat(AREA_KINDS, ', '))
        end
        return nil
    end
    return w
end

-- A path in an id: other bytes as %XX, so two paths never give one id
local function idOf(path)
    return AREA_ID .. path:gsub('[^%w%._:%-]', function(c) return string.format('%%%02X', c:byte()) end)
end

-- Widgets of the devices in areas (on floor, when given); ids from the path, the same on every
-- read; "#n" only for the same device shown again
local function areaWidgets(spec, areas, used, floor)
    local E = ExposedObjects
    if not (E and E._inArea) then return {} end
    local out = {}
    for _, f in ipairs(E:_inArea(areas, { kinds = spec.kinds, skip = spec.skip, floor = floor, flag = 'dashboard' })) do
        local base = idOf(f.path)
        local id, n = base, 1
        while used[id] do n = n + 1; id = base .. '#' .. n end
        local w = areaWidget(f.handle, id)
        if w then
            used[id] = true
            out[#out + 1] = w
        end
    end
    return out
end

-- The dashboard as the API reads it: area sections filled from what is exposed, a floor one
-- section per area; reused until something is exposed, unexposed or its options change
-- (widgets read the objects live)
local function expandAreas(dash)
    if not dash or not dash._hasAreas then return dash end
    local E = ExposedObjects
    local gen = E and E._generation or 0
    if dash._areaView and dash._areaGen == gen then return dash._areaView end
    local used, sections = {}, {}
    local function add(sec, widgets)
        local c = {}
        for k, v in pairs(sec) do c[k] = v end
        c.widgets = widgets
        sections[#sections + 1] = c
    end
    for _, sec in ipairs(dash._sections) do
        local spec = sec._floor
        if spec then
            for _, a in ipairs(E and E._areasOn and E:_areasOn(spec.floor, spec.order) or {}) do
                local widgets = areaWidgets(spec, { a }, used, spec.floor)
                if #widgets > 0 then add({ name = spec.titles[a] or a, icon = sec.icon }, widgets) end
            end
        elseif sec._area then
            local widgets = areaWidgets(sec._area, sec._area.areas, used)
            for _, w in ipairs(sec.widgets) do widgets[#widgets + 1] = w end
            add(sec, widgets)
        else
            sections[#sections + 1] = sec
        end
    end
    local view = setmetatable({ _sections = sections }, { __index = dash })
    dash._areaView, dash._areaGen = view, gen
    return view
end

function SectionHandle:new(builder, sectionIndex)
    local h = { _builder = builder, _sectionIndex = sectionIndex }
    setmetatable(h, self)
    return h
end

--- Add a button widget to this section.
-- @param id string Unique widget identifier
-- @param label string Display label
-- @param icon string Bootstrap icon name (e.g., "power", "door-open")
-- @param onTap function Callback fired on button press: function()
-- @param opts table Optional: {disabled=function()->bool, confirm=string}
-- @api public
function SectionHandle:button(id, label, icon, onTap, opts)
    self._builder:_addWidget(self._sectionIndex, {
        id = id, type = 'button', label = label, icon = icon,
        onTap = onTap, opts = opts or {}
    })
    return self
end

--- Add a toggle widget to this section.
-- @param id string Unique widget identifier
-- @param label string Display label
-- @param icon string Bootstrap icon name (e.g., "lightbulb", "lamp")
-- @param getValue function Returns current state: function() -> boolean|number (truthy=on)
-- @param onToggle function Called with desired state: function(bool) — use switchOn/switchOff, NOT toggle()
-- @param opts table Optional: {disabled=function()->bool}
-- @api public
function SectionHandle:toggle(id, label, icon, getValue, onToggle, opts)
    self._builder:_addWidget(self._sectionIndex, {
        id = id, type = 'toggle', label = label, icon = icon,
        getValue = getValue, onToggle = onToggle, opts = opts or {}
    })
    return self
end

--- Add a slider widget to this section.
-- @param id string Unique widget identifier
-- @param label string Display label
-- @param min number Minimum slider value
-- @param max number Maximum slider value
-- @param getValue function Returns current value: function() -> number
-- @param onSet function Called with new value: function(number)
-- @param opts table Optional: {step=number, unit=string, icon=string, disabled=function()->bool}
-- @api public
function SectionHandle:slider(id, label, min, max, getValue, onSet, opts)
    self._builder:_addWidget(self._sectionIndex, {
        id = id, type = 'slider', label = label,
        min = min, max = max,
        getValue = getValue, onSet = onSet, opts = opts or {}
    })
    return self
end

--- Add a status widget to this section.
-- @param id string Unique widget identifier
-- @param label string Display label
-- @param icon string Bootstrap icon name (e.g., "thermometer-half", "droplet")
-- @param getValue function Returns current value: function() -> number|string
-- @param unit string Unit suffix displayed after value (e.g., "°C", "%", "lx")
-- @param opts table Optional: {disabled=function()->bool, color=string|function()->string|nil}
-- @api public
function SectionHandle:status(id, label, icon, getValue, unit, opts)
    self._builder:_addWidget(self._sectionIndex, {
        id = id, type = 'status', label = label, icon = icon,
        getValue = getValue, unit = unit or '', opts = opts or {}
    })
    return self
end

--- Add a link widget to this section.
-- @param id string Unique widget identifier
-- @param label string Display label
-- @param icon string Bootstrap icon name
-- @param url string|function URL string or function() -> string
-- @param opts table Optional: {disabled=function()->bool}
-- @api public
function SectionHandle:link(id, label, icon, url, opts)
    self._builder:_addWidget(self._sectionIndex, {
        id = id, type = 'link', label = label, icon = icon,
        url = url, opts = opts or {}
    })
    return self
end

--- Add a chart widget to this section.
-- @param id string Unique widget identifier
-- @param label string Display label (e.g., "Temperatura 24h")
-- @param metricPath string Metric path registered via metric() (e.g., "temp:salon", "salda:outsideAir")
-- @param opts table Optional: {range=string ("1h","6h","24h","7d"), unit=string}
-- @api public
function SectionHandle:chart(id, label, metricPath, opts)
    self._builder:_addWidget(self._sectionIndex, {
        id = id, type = 'chart', label = label,
        metricPath = metricPath, opts = opts or {}
    })
    return self
end

--- Add a calendar widget to this section: a month grid and the day of a tapped date.
-- @param id string Unique widget identifier
-- @param label string Display label
-- @param cal table Calendar from Calendar.new(...):build(), or its id
-- @param opts table Optional: {view="month"|"day", tags={...}, schedules={ids drawn as 24 h bars in the day view} or true for all of the calendar's,
--   holidays=true (holiday names, on by default), namedays=false (name days in the day view),
--   sun=true (sunrise and sunset in the day view, with :location()), span=n}; what is off is not sent to the browser
-- @api public
function SectionHandle:calendar(id, label, cal, opts)
    self._builder:_addWidget(self._sectionIndex, calendarWidget(id, label, cal, opts))
    return self
end

--- Add a select widget to this section.
-- @param id string Unique widget identifier
-- @param label string Display label
-- @param icon string Bootstrap icon name
-- @param options table Array of {value=any, label=string} or function() -> same
-- @param getValue function Returns current selected value: function() -> any
-- @param onSelect function Called with selected value: function(any)
-- @param opts table Optional: {disabled=function()->bool}
-- @api public
function SectionHandle:select(id, label, icon, options, getValue, onSelect, opts)
    self._builder:_addWidget(self._sectionIndex, {
        id = id, type = 'select', label = label, icon = icon,
        options = options, getValue = getValue, onSelect = onSelect, opts = opts or {}
    })
    return self
end

--- Add an image widget to this section.
-- @param id string Unique widget identifier
-- @param label string Display label
-- @param url string|function Image URL string or function() -> string
-- @param opts table Optional: {refreshInterval=number (seconds)}
-- @api public
function SectionHandle:image(id, label, url, opts)
    self._builder:_addWidget(self._sectionIndex, {
        id = id, type = 'image', label = label,
        url = url, opts = opts or {}
    })
    return self
end

--- Add a group label widget to this section.
-- @param label string Group heading text
-- @param icon string Bootstrap icon name (optional)
-- @api public
function SectionHandle:group(label, icon)
    self._builder:_addWidget(self._sectionIndex, {
        id = '_group_' .. tostring(#self._builder._sections[self._sectionIndex].widgets),
        type = 'group', label = label, icon = icon, opts = {}
    })
    return self
end

--- Add a cover widget to this section.
-- @param id string Unique widget identifier
-- @param label string Display label
-- @param icon string Bootstrap icon name (e.g., "arrows-expand")
-- @param fns table {getPosition=function()->number|nil, getState=function()->number|nil, open=function(), close=function(), stop=function(), set=function(position)}
--   Buttons up/stop/down call open/stop/close. With set, a 0-100% slider is shown under them.
--   Position 0 = closed, 100 = open, as in Grenton; nil (not reported yet) is shown as --.
--   getState lights the button of the movement: 0 = stopped, 1 = up, 2 = down.
-- @param opts table Optional: {disabled=function()->bool}
-- @api public
function SectionHandle:cover(id, label, icon, fns, opts)
    self._builder:_addWidget(self._sectionIndex, {
        id = id, type = 'cover', label = label, icon = icon,
        fns = fns, opts = opts or {}
    })
    return self
end

--- Add a dimmer widget to this section.
-- @param id string Unique widget identifier
-- @param label string Display label
-- @param icon string Bootstrap icon name (e.g., "brightness-high")
-- @param fns table {getValue=function()->number(0-1), setValue=function(number), switchOn=function(), switchOff=function()}
-- @param opts table Optional: {disabled=function()->bool}
-- @api public
function SectionHandle:dimmer(id, label, icon, fns, opts)
    self._builder:_addWidget(self._sectionIndex, {
        id = id, type = 'dimmer', label = label, icon = icon,
        fns = fns, opts = opts or {}
    })
    return self
end

--- Add a color picker widget to this section.
-- @param id string Unique widget identifier
-- @param label string Display label
-- @param icon string Bootstrap icon name (e.g., "palette")
-- @param fns table {getColor=function()->{r,g,b}, setColor=function(r,g,b), get?/set? brightness 0-100,
--   switchOn?, switchOff?, isOn?, getWhite?/setWhite? white 0-100}, or a RemoteRGBLight (white when it has one)
-- @param opts table Optional: {disabled=function()->bool}
-- @api public
function SectionHandle:color(id, label, icon, fns, opts)
    self._builder:_addWidget(self._sectionIndex, {
        id = id, type = 'color', label = label, icon = icon,
        fns = colorFns(fns), opts = opts or {}
    })
    return self
end

--- Fill this section with a widget for every device expose() put in an area, picked by kind:
-- a light, switch or lock a toggle, a dimmer a dimmer, an RGB light the colour widget, a cover
-- a cover, temperature and humidity a status; lights first, then covers, then the rest by name.
-- Filled when something is exposed or unexposed, so devices a plugin exposes later show up;
-- widgets added by hand come after them; a read-only device's widget is disabled. Like the
-- panel, expose(obj, type, { dashboard = false }) keeps an object off dashboards and
-- dashboard = true puts one of those kinds on them even when kinds leaves it out (other kinds
-- have no widget and log a warning). Widget ids are "area:" .. path, other bytes as %XX.
-- Anyone who can open the dashboard controls what is exposed in the area later: use
-- :auth('session') or 'pin'; access profiles of MQTT, HomeKit and MCP do not apply here.
-- @param area string Area name, or a list of names (one room under two names)
-- @param opts table Optional: {kinds={"light","dimmer","cover","temperature","humidity"} (also "switch", "lock"), skip={path, ...}}
-- @api public
function SectionHandle:area(area, opts)
    local sec = self._builder._sections[self._sectionIndex]
    local areas = type(area) == 'table' and area or { area }
    if (type(area) ~= 'string' and type(area) ~= 'table') or #areas == 0 or not isList(areas, 'string') then
        error('section:area: expected an area name or a list of them, got ' .. type(area), 2)
    end
    if sec._area then error('section:area: this section already shows an area; pass a list for several', 2) end
    sec._area = areaSpec('section:area', opts, AREA_KEYS, 3)
    sec._area.areas = areas
    self._builder._hasAreas = true
    self._builder._areaView = nil
    return self
end

--- Add one section per area on a floor (delegates to DashboardBuilder:floor).
-- @api public
function SectionHandle:floor(floor, opts)
    local b = self._builder:_floor(floor, opts) -- no tail call: errors count the levels
    return b
end

--- Conditional block: only execute fn if current time matches any of the given periods.
-- @param periods table Array of period names ("morning","afternoon","evening","night")
-- @param fn function Receives self (SectionHandle) as argument
-- @api public
function SectionHandle:when(periods, fn)
    self._builder:when(periods, fn)
    return self
end

--- Start a new section (delegates to DashboardBuilder:section).
-- @api public
function SectionHandle:section(name, icon, opts)
    return self._builder:section(name, icon, opts)
end

--- Start a header section (delegates to DashboardBuilder:header).
-- @api public
function SectionHandle:header()
    return self._builder:header()
end

--- Finalize and register the dashboard (delegates to DashboardBuilder:build).
-- @api public
function SectionHandle:build()
    return self._builder:build()
end

--- Set dashboard name (delegates to DashboardBuilder:name).
-- @api public
function SectionHandle:name(s)  self._builder:name(s);  return self end
--- Set dashboard theme (delegates to DashboardBuilder:theme).
-- @api public
function SectionHandle:theme(t) self._builder:theme(t); return self end
--- Set dashboard poll interval (delegates to DashboardBuilder:poll).
-- @api public
function SectionHandle:poll(n)  self._builder:poll(n);  return self end
--- Add dashboard to a tab group (delegates to DashboardBuilder:tabGroup).
-- @api public
function SectionHandle:tabGroup(g) self._builder:tabGroup(g); return self end

-- ==========================================================================
-- DashboardBuilder
-- ==========================================================================

local DashboardBuilder = {}
DashboardBuilder.__index = DashboardBuilder

function DashboardBuilder:new(id)
    if type(id) ~= 'string' or id == '' then
        error('Dashboard.new() requires a non-empty string ID')
    end
    local b = {
        _id = id,
        _name = id,
        _icon = 'grid',
        _columns = 3,
        _auth = 'public',
        _pin = nil,
        _poll = 2,
        _theme = nil,
        _sections = {},    -- { {name, icon, widgets={}} }
        _currentSection = nil, -- index into _sections
        _groups = {},
        _groupSet = {},    -- dedup lookup
    }
    setmetatable(b, self)
    return b
end

--- Set the display name for this dashboard.
-- @param s string Display name shown in navigation
-- @api public
function DashboardBuilder:name(s)
    self._name = s
    return self
end

--- Set the icon for this dashboard.
-- @param s string Bootstrap icon name (e.g., "house", "thermometer", "camera-video")
-- @api public
function DashboardBuilder:icon(s)
    self._icon = s
    return self
end

--- Set the number of grid columns.
-- @param n number Number of columns (default 3)
-- @api public
function DashboardBuilder:columns(n)
    self._columns = n
    return self
end

--- Set the authentication mode ("public", "pin").
-- @param s string Auth mode: "public" (no auth) or "pin" (requires PIN)
-- @api public
function DashboardBuilder:auth(s)
    self._auth = s
    return self
end

--- Set the PIN code for pin-authenticated dashboards.
-- @param s string PIN code (used when auth="pin")
-- @api public
function DashboardBuilder:pin(s)
    self._pin = s
    return self
end

--- Set the poll interval in seconds.
-- @param n number Poll interval in seconds (default 2)
-- @api public
function DashboardBuilder:poll(n)
    self._poll = n
    return self
end

--- Set the dashboard theme.
-- @param t string Theme name (e.g., "dark", "light")
-- @api public
function DashboardBuilder:theme(t)
    self._theme = t
    return self
end

--- Add this dashboard to a named tab group for navigation.
-- @param name string Tab group name (dashboards with same group show as tabs)
-- @api public
function DashboardBuilder:tabGroup(name)
    if not self._groupSet[name] then
        self._groupSet[name] = true
        table.insert(self._groups, name)
    end
    return self
end

--- Start a header section (must be called before any section).
-- @api public
function DashboardBuilder:header()
    if #self._sections > 0 then
        error('header() must be called before any section()')
    end
    local sec = { name = '', icon = '', widgets = {}, _isHeader = true }
    table.insert(self._sections, sec)
    self._currentSection = #self._sections
    return SectionHandle:new(self, self._currentSection)
end

--- Start a new named section for grouping widgets.
-- @param name string Section title
-- @param icon string Bootstrap icon name
-- @param opts table Optional: {columns=number}
-- @return SectionHandle Handle for adding widgets to this section
-- @api public
function DashboardBuilder:section(name, icon, opts)
    opts = opts or {}
    local sec = { name = name, icon = icon or '', widgets = {}, _columns = opts.columns }
    table.insert(self._sections, sec)
    self._areaView = nil
    self._currentSection = #self._sections
    return SectionHandle:new(self, self._currentSection)
end

--- Add one section per area on a floor (expose(..., { area = "Salon", floor = "Parter" })),
-- each filled as section:area; areas with nothing to show are left out. Read each time the
-- dashboard is, so new areas show up. Start a section() to add widgets by hand after it.
-- @param floor string Floor name
-- @param opts table Optional: {order={area, ...} (first, in this order; the rest by name), titles={[area]=title}, kinds, skip}
-- @api public
function DashboardBuilder:floor(floor, opts)
    local b = self:_floor(floor, opts) -- no tail call: errors count the levels
    return b
end

-- Errors name the module that called floor() on the builder or a section
function DashboardBuilder:_floor(floor, opts)
    if type(floor) ~= 'string' or floor == '' then
        error('dash:floor: expected a floor name, got ' .. type(floor), 3)
    end
    local spec = areaSpec('dash:floor', opts, FLOOR_KEYS, 4)
    spec.floor = floor
    table.insert(self._sections, { name = floor, icon = '', widgets = {}, _floor = spec })
    self._areaView = nil
    self._currentSection = nil
    self._hasAreas = true
    return self
end

-- Internal: add widget to a section
function DashboardBuilder:_addWidget(sectionIndex, widget)
    local sec = self._sections[sectionIndex]
    if not sec then
        error('No section at index ' .. tostring(sectionIndex))
    end
    -- level 3: the module that called a widget method on the builder or a section
    if type(widget.id) == 'string' and widget.id:sub(1, #AREA_ID) == AREA_ID then
        error('widget id "' .. widget.id .. '": ids starting with "' .. AREA_ID .. '" are kept for section:area', 3)
    end
    table.insert(sec.widgets, widget)
    self._areaView = nil
end

--- Add a button widget to the current section.
-- @param id string Unique widget identifier
-- @param label string Display label
-- @param icon string Bootstrap icon name (e.g., "power", "door-open")
-- @param onTap function Callback fired on button press: function()
-- @param opts table Optional: {disabled=function()->bool, confirm=string}
-- @api public
function DashboardBuilder:button(id, label, icon, onTap, opts)
    if not self._currentSection then
        error('Call :section() before adding widgets (dash:floor() ends the current section)', 2)
    end
    self:_addWidget(self._currentSection, {
        id = id, type = 'button', label = label, icon = icon,
        onTap = onTap, opts = opts or {}
    })
    return self
end

--- Add a toggle widget to the current section.
-- @param id string Unique widget identifier
-- @param label string Display label
-- @param icon string Bootstrap icon name (e.g., "lightbulb", "lamp")
-- @param getValue function Returns current state: function() -> boolean|number (truthy=on)
-- @param onToggle function Called with desired state: function(bool) — use switchOn/switchOff, NOT toggle()
-- @param opts table Optional: {disabled=function()->bool}
-- @api public
function DashboardBuilder:toggle(id, label, icon, getValue, onToggle, opts)
    if not self._currentSection then
        error('Call :section() before adding widgets (dash:floor() ends the current section)', 2)
    end
    self:_addWidget(self._currentSection, {
        id = id, type = 'toggle', label = label, icon = icon,
        getValue = getValue, onToggle = onToggle, opts = opts or {}
    })
    return self
end

--- Add a slider widget to the current section.
-- @param id string Unique widget identifier
-- @param label string Display label
-- @param min number Minimum slider value
-- @param max number Maximum slider value
-- @param getValue function Returns current value: function() -> number
-- @param onSet function Called with new value: function(number)
-- @param opts table Optional: {step=number, unit=string, icon=string, disabled=function()->bool}
-- @api public
function DashboardBuilder:slider(id, label, min, max, getValue, onSet, opts)
    if not self._currentSection then
        error('Call :section() before adding widgets (dash:floor() ends the current section)', 2)
    end
    self:_addWidget(self._currentSection, {
        id = id, type = 'slider', label = label,
        min = min, max = max,
        getValue = getValue, onSet = onSet, opts = opts or {}
    })
    return self
end

--- Add a status widget to the current section.
-- @param id string Unique widget identifier
-- @param label string Display label
-- @param icon string Bootstrap icon name (e.g., "thermometer-half", "droplet")
-- @param getValue function Returns current value: function() -> number|string
-- @param unit string Unit suffix displayed after value (e.g., "°C", "%", "lx")
-- @param opts table Optional: {disabled=function()->bool, color=string|function()->string|nil}
-- @api public
function DashboardBuilder:status(id, label, icon, getValue, unit, opts)
    if not self._currentSection then
        error('Call :section() before adding widgets (dash:floor() ends the current section)', 2)
    end
    self:_addWidget(self._currentSection, {
        id = id, type = 'status', label = label, icon = icon,
        getValue = getValue, unit = unit or '', opts = opts or {}
    })
    return self
end

--- Add a link widget to the current section.
-- @param id string Unique widget identifier
-- @param label string Display label
-- @param icon string Bootstrap icon name
-- @param url string|function URL string or function() -> string
-- @param opts table Optional: {disabled=function()->bool}
-- @api public
function DashboardBuilder:link(id, label, icon, url, opts)
    if not self._currentSection then
        error('Call :section() before adding widgets (dash:floor() ends the current section)', 2)
    end
    self:_addWidget(self._currentSection, {
        id = id, type = 'link', label = label, icon = icon,
        url = url, opts = opts or {}
    })
    return self
end

--- Add a chart widget to the current section.
-- @param id string Unique widget identifier
-- @param label string Display label (e.g., "Temperatura 24h")
-- @param metricPath string Metric path registered via metric() (e.g., "temp:salon", "salda:outsideAir")
-- @param opts table Optional: {range=string ("1h","6h","24h","7d"), unit=string}
-- @api public
function DashboardBuilder:chart(id, label, metricPath, opts)
    if not self._currentSection then
        error('Call :section() before adding widgets (dash:floor() ends the current section)', 2)
    end
    self:_addWidget(self._currentSection, {
        id = id, type = 'chart', label = label,
        metricPath = metricPath, opts = opts or {}
    })
    return self
end

--- Add a calendar widget to the current section: a month grid and the day of a tapped date.
-- @param id string Unique widget identifier
-- @param label string Display label
-- @param cal table Calendar from Calendar.new(...):build(), or its id
-- @param opts table Optional: {view="month"|"day", tags={...}, schedules={ids drawn as 24 h bars in the day view} or true for all of the calendar's,
--   holidays=true (holiday names, on by default), namedays=false (name days in the day view),
--   sun=true (sunrise and sunset in the day view, with :location()), span=n}; what is off is not sent to the browser
-- @api public
function DashboardBuilder:calendar(id, label, cal, opts)
    if not self._currentSection then
        error('Call :section() before adding widgets (dash:floor() ends the current section)', 2)
    end
    self:_addWidget(self._currentSection, calendarWidget(id, label, cal, opts))
    return self
end

--- Add a select widget to the current section.
-- @param id string Unique widget identifier
-- @param label string Display label
-- @param icon string Bootstrap icon name
-- @param options table Array of {value=any, label=string} or function() -> same
-- @param getValue function Returns current selected value: function() -> any
-- @param onSelect function Called with selected value: function(any)
-- @param opts table Optional: {disabled=function()->bool}
-- @api public
function DashboardBuilder:select(id, label, icon, options, getValue, onSelect, opts)
    if not self._currentSection then
        error('Call :section() before adding widgets (dash:floor() ends the current section)', 2)
    end
    self:_addWidget(self._currentSection, {
        id = id, type = 'select', label = label, icon = icon,
        options = options, getValue = getValue, onSelect = onSelect, opts = opts or {}
    })
    return self
end

--- Add an image widget to the current section.
-- @param id string Unique widget identifier
-- @param label string Display label
-- @param url string|function Image URL string or function() -> string
-- @param opts table Optional: {refreshInterval=number (seconds)}
-- @api public
function DashboardBuilder:image(id, label, url, opts)
    if not self._currentSection then
        error('Call :section() before adding widgets (dash:floor() ends the current section)', 2)
    end
    self:_addWidget(self._currentSection, {
        id = id, type = 'image', label = label,
        url = url, opts = opts or {}
    })
    return self
end

--- Add a group label widget to the current section.
-- @param label string Group heading text
-- @param icon string Bootstrap icon name (optional)
-- @api public
function DashboardBuilder:group(label, icon)
    if not self._currentSection then
        error('Call :section() before adding widgets (dash:floor() ends the current section)', 2)
    end
    self:_addWidget(self._currentSection, {
        id = '_group_' .. tostring(#self._sections[self._currentSection].widgets),
        type = 'group', label = label, icon = icon, opts = {}
    })
    return self
end

--- Add a cover widget to the current section.
-- @param id string Unique widget identifier
-- @param label string Display label
-- @param icon string Bootstrap icon name (e.g., "arrows-expand")
-- @param fns table {getPosition=function()->number|nil, getState=function()->number|nil, open=function(), close=function(), stop=function(), set=function(position)}
--   Buttons up/stop/down call open/stop/close. With set, a 0-100% slider is shown under them.
--   Position 0 = closed, 100 = open, as in Grenton; nil (not reported yet) is shown as --.
--   getState lights the button of the movement: 0 = stopped, 1 = up, 2 = down.
-- @param opts table Optional: {disabled=function()->bool}
-- @api public
function DashboardBuilder:cover(id, label, icon, fns, opts)
    if not self._currentSection then
        error('Call :section() before adding widgets (dash:floor() ends the current section)', 2)
    end
    self:_addWidget(self._currentSection, {
        id = id, type = 'cover', label = label, icon = icon,
        fns = fns, opts = opts or {}
    })
    return self
end

--- Add a dimmer widget to the current section.
-- @param id string Unique widget identifier
-- @param label string Display label
-- @param icon string Bootstrap icon name (e.g., "brightness-high")
-- @param fns table {getValue=function()->number(0-1), setValue=function(number), switchOn=function(), switchOff=function()}
-- @param opts table Optional: {disabled=function()->bool}
-- @api public
function DashboardBuilder:dimmer(id, label, icon, fns, opts)
    if not self._currentSection then
        error('Call :section() before adding widgets (dash:floor() ends the current section)', 2)
    end
    self:_addWidget(self._currentSection, {
        id = id, type = 'dimmer', label = label, icon = icon,
        fns = fns, opts = opts or {}
    })
    return self
end

--- Add a color picker widget to the current section.
-- @param id string Unique widget identifier
-- @param label string Display label
-- @param icon string Bootstrap icon name (e.g., "palette")
-- @param fns table {getColor=function()->{r,g,b}, setColor=function(r,g,b), get?/set? brightness 0-100,
--   switchOn?, switchOff?, isOn?, getWhite?/setWhite? white 0-100}, or a RemoteRGBLight (white when it has one)
-- @param opts table Optional: {disabled=function()->bool}
-- @api public
function DashboardBuilder:color(id, label, icon, fns, opts)
    if not self._currentSection then
        error('Call :section() before adding widgets (dash:floor() ends the current section)', 2)
    end
    self:_addWidget(self._currentSection, {
        id = id, type = 'color', label = label, icon = icon,
        fns = colorFns(fns), opts = opts or {}
    })
    return self
end

--- Conditional block: only execute fn if current time matches any of the given periods.
-- @param periods table|string Period name(s): "morning","afternoon","evening","night"
-- @param fn function Receives self (DashboardBuilder) as argument
-- @api public
function DashboardBuilder:when(periods, fn)
    if type(periods) == 'string' then periods = {periods} end
    local current = Dashboard.timeOfDay()
    for _, p in ipairs(periods) do
        if p == current then
            fn(self)
            return self
        end
    end
    return self
end

--- Finalize and register the dashboard in the global registry.
-- @api public
function DashboardBuilder:build()
    -- Validate unique widget IDs (skip auto-generated group labels)
    local seen = {}
    for _, sec in ipairs(self._sections) do
        for _, w in ipairs(sec.widgets) do
            if w.type ~= 'group' then
                if seen[w.id] then
                    error('Duplicate widget ID "' .. w.id .. '" in dashboard "' .. self._id .. '"')
                end
                seen[w.id] = true
            end
        end
    end

    -- Store in global registry (idempotent — replaces previous)
    _dashboards[self._id] = self

    -- Track insertion order (avoid duplicates on rebuild)
    local found = false
    for _, oid in ipairs(_dashboardOrder) do
        if oid == self._id then found = true; break end
    end
    if not found then
        table.insert(_dashboardOrder, self._id)
    end

    return self
end

-- ==========================================================================
-- DashboardAPI — called from Go via LuaEngine.Execute()
-- ==========================================================================

local DashboardAPI = {}

-- Safe value getter: wraps getValue in pcall
local function safeGetValue(fn)
    if type(fn) ~= 'function' then return nil end
    local ok, val = pcall(fn)
    if ok then
        -- Remote CLU calls return strings via __go_send_command;
        -- convert numeric strings to numbers so JSON encodes them unquoted
        if type(val) == 'string' then
            local num = tonumber(val)
            if num then return num, nil end
        end
        return val, nil
    else
        return nil, tostring(val)
    end
end

-- Colour errors already logged, by "dashboard/widget"
local _colorErrorLogged = {}

-- Safe colour: a string as is, a status tile's function's current string result, nil on error
local function safeColor(dashId, w)
    local c = w.opts.color
    if type(c) ~= 'function' then return c end
    if w.type ~= 'status' then return nil end
    local ok, val = pcall(c)
    if ok then
        if type(val) == 'string' then return val end
        return nil
    end
    local key = tostring(dashId) .. '/' .. tostring(w.id)
    if not _colorErrorLogged[key] then
        _colorErrorLogged[key] = true
        Logger:getShared():warn("[DASHBOARD] Color error for '%s': %s", key, tostring(val))
    end
    return nil
end

-- Safe disabled check
local function safeDisabled(opts)
    if not opts or type(opts.disabled) ~= 'function' then return false end
    local ok, val = pcall(opts.disabled)
    if ok then return val == true end
    return false
end

--- Internal: resolve a dashboard entry. For dynamic dashboards, rebuilds fresh.
-- bare: without filling area sections (auth and PIN need no widgets)
local function resolveDashboard(id, bare)
    local entry = _dashboards[id]
    if not entry then return nil end
    if not entry._isDynamic then return bare and entry or expandAreas(entry) end

    -- Dynamic: create fresh builder, call user function, auto-build
    local builder = DashboardBuilder:new(id)
    local ok, err = pcall(entry._builderFn, builder)
    if not ok then
        Logger:getShared():warn("[DASHBOARD] Dynamic build error for '" .. id .. "': " .. tostring(err))
        return nil
    end
    -- Auto-build if the function didn't call build()
    if not _dashboards[id] or _dashboards[id]._isDynamic then
        builder:build()
    end
    local result = _dashboards[id]
    -- Restore the dynamic sentinel so next call rebuilds again
    _dashboards[id] = entry
    return bare and result or expandAreas(result)
end

--- Get auth type for a dashboard (lightweight, no getValue calls)
-- @param id string Dashboard ID
-- @return string JSON with auth type
function DashboardAPI.getAuth(id)
    local dash = resolveDashboard(id, true)
    if not dash then
        return JSON.encode({ error = 'Dashboard not found' })
    end
    return JSON.encode({ auth = dash._auth })
end

--- Whether the dashboard draws this metric (chart or status sparkline)
-- @return string JSON {used=bool, ranges=[...]} or {error=...}
function DashboardAPI.usesMetric(id, path)
    local dash = resolveDashboard(id)
    if not dash then
        return JSON.encode({ error = 'Dashboard not found' })
    end
    local ranges = {}
    for _, sec in ipairs(dash._sections) do
        for _, w in ipairs(sec.widgets) do
            if w.type == 'chart' and w.metricPath == path then
                table.insert(ranges, w.opts.range or '2h')
            elseif w.type == 'status' and w.opts and w.opts.spark == path then
                table.insert(ranges, w.opts.sparkRange or '2h')
            end
        end
    end
    if #ranges == 0 then return JSON.encode({ used = false }) end
    return JSON.encode({ used = true, ranges = ranges })
end

local CALENDAR_CHIPS = 3      -- a month cell shows 3 events and "+n"
local CALENDAR_DAY_MAX = 300  -- events in one day view

--- Occurrences of a calendar widget's calendar for the days from..to ('YYYY-MM-DD', at most 42)
-- @return string JSON {today, now, ready, days=[{date, weekday, workday, holiday}], items=[...]},
--   or {error, status} with the HTTP status of a bad request
function DashboardAPI.calendarRange(id, widgetId, from, to)
    local function fail(status, msg) return JSON.encode({ error = msg, status = status }) end
    local dash = resolveDashboard(id)
    if not dash then return fail(404, 'Dashboard not found') end
    local w
    for _, sec in ipairs(dash._sections) do
        for _, x in ipairs(sec.widgets) do
            if x.id == widgetId and x.type == 'calendar' then w = x end
        end
    end
    if not w then return fail(403, 'calendar widget not on this dashboard') end
    local cal = Calendar.get(w.calendar)
    if not cal then return fail(404, 'calendar "' .. w.calendar .. '" not built') end
    local okA, a = pcall(cal.day, cal, from)
    local okB, b = pcall(cal.day, cal, to)
    if not okA or not okB or b.starts < a.starts then return fail(400, 'from and to expected as YYYY-MM-DD') end
    if a.date < '2000-01-01' or b.date > '2100-12-31' then return fail(400, 'dates from 2000 to 2100') end
    local today = cal:day()

    -- what the author switched off is not sent at all (a public page would show the house's location)
    local showHol, showNames, showSun = w.opts.holidays ~= false, w.opts.namedays == true, w.opts.sun ~= false
    local function minute(ts) return ts and math.floor(ts / 60 + 0.5) * 60 end
    local days = {}
    local d = a
    while d.starts <= b.starts do
        if #days == 42 then return fail(400, 'at most 42 days') end
        local one = a.date == b.date
        local sun = one and showSun and cal:sun(d.date) or nil
        days[#days + 1] = { date = d.date, weekday = d.weekday, workday = d.workday,
            holiday = showHol and d.holiday or false, holidayName = showHol and d.holidayName or nil,
            namedays = one and showNames and d.namedays or nil,
            sunrise = sun and minute(sun.sunrise), sunset = sun and minute(sun.sunset), polar = sun and sun.polar }
        d = cal:day(d.ends)
    end
    local items = {}
    local ok, list = pcall(cal.between, cal, a.date, b.date, { tags = w.opts.tags })
    if not ok then
        Logger:getShared():error('[DASHBOARD] %s calendar %s: %s', tostring(id), tostring(widgetId), tostring(list))
        return fail(500, 'calendar unavailable')
    end
    -- a range sends what its cells draw and each day's count: a busy calendar stays small
    -- (and under gopher-lua's limit on the values table.concat can join)
    local byDate, shown = {}, {}
    for _, day in ipairs(days) do byDate[day.date] = day; day.count = 0; shown[day.date] = 0 end
    local cap = a.date == b.date and CALENDAR_DAY_MAX or CALENDAR_CHIPS
    for _, e in ipairs(list) do
        local keep = false
        for _, day in ipairs(days) do
            if day.date >= e.date and day.date <= (e.last or e.date) then
                day.count = day.count + 1
                if shown[day.date] < cap then shown[day.date] = shown[day.date] + 1; keep = true end
            end
        end
        if keep then
            items[#items + 1] = { id = e.id, title = e.title, icon = e.icon, at = e.at, ends = e.ends,
                allDay = e.allDay, date = e.date, last = e.last, paused = e.paused, source = e.source }
        end
    end
    -- schedules as 24 h bars: only for a single day (the day view)
    local schedules
    if w.opts.schedules and a.date == b.date and cal:isReady() then
        local titles, ids = {}, w.opts.schedules
        if ids == true then ids = {} end
        for _, sc in ipairs(cal:info().schedules) do
            titles[sc.id] = sc.title
            if w.opts.schedules == true then ids[#ids + 1] = sc.id end
        end
        schedules = {}
        for _, sid in ipairs(ids) do
            local okS, segs = pcall(cal.segments, cal, sid, a.date)
            if okS then
                for _, sg in ipairs(segs) do sg.value = tostring(sg.value) end
                schedules[#schedules + 1] = { id = sid, title = titles[sid], segments = segs }
            end
        end
    end
    return JSON.encode({ today = today.date, now = math.floor(__go_time_ms() / 1000), zone = datetime.timezone(),
        ready = cal:isReady(), days = days, items = items, schedules = schedules })
end

--- List all dashboards with widget details
-- @return string JSON array of dashboard summaries including widgets array
function DashboardAPI.list()
    local result = {}
    -- Collect IDs first to avoid iterator issues with dynamic resolution
    local ids = {}
    for id in pairs(_dashboards) do
        table.insert(ids, id)
    end
    table.sort(ids)

    for _, id in ipairs(ids) do
        local dash = resolveDashboard(id)
        if dash then
            local widgetCount = 0
            local widgets = {}
            for _, sec in ipairs(dash._sections) do
                widgetCount = widgetCount + #sec.widgets
                for _, w in ipairs(sec.widgets) do
                    table.insert(widgets, {
                        id = w.id,
                        type = w.type,
                        label = w.label,
                        icon = w.icon,
                    })
                end
            end
            table.insert(result, {
                id = id,
                name = dash._name,
                icon = dash._icon,
                auth = dash._auth,
                widgetCount = widgetCount,
                widgets = widgets,
            })
        end
    end
    return JSON.encode(result)
end

--- Get full dashboard definition with current values
-- @param id string Dashboard ID
-- @return string JSON definition or error
function DashboardAPI.getDefinition(id)
    local dash = resolveDashboard(id)
    if not dash then
        return JSON.encode({ error = 'Dashboard not found' })
    end

    local sections = {}
    local widgets = {}

    for si, sec in ipairs(dash._sections) do
        local secDef = { name = sec.name, icon = sec.icon }
        if sec._isHeader then secDef.isHeader = true end
        if sec._columns then secDef.columns = sec._columns end
        table.insert(sections, secDef)

        for _, w in ipairs(sec.widgets) do
            local widget = {
                id = w.id,
                type = w.type,
                label = w.label,
                icon = w.icon,
                section = si - 1, -- 0-indexed for JS
                span = w.opts.span,
                size = w.opts.size,
                color = safeColor(id, w),
            }

            -- Get current value
            if w.type == 'toggle' or w.type == 'slider' or w.type == 'status' or w.type == 'select' then
                local val, err = safeGetValue(w.getValue)
                if err then
                    widget.value = JSON.null
                    widget.error = err
                else
                    widget.value = val
                end
            end

            -- Slider-specific fields
            if w.type == 'slider' then
                widget.min = w.min
                widget.max = w.max
            end

            -- Status unit + sparkline
            if w.type == 'status' then
                widget.unit = w.unit
                if w.opts.spark then
                    widget.spark = w.opts.spark
                    widget.sparkRange = w.opts.sparkRange or '2h'
                end
            end

            -- Link URL
            if w.type == 'link' then
                widget.url = w.url
            end

            -- Chart fields (no getValue — frontend fetches directly)
            if w.type == 'chart' then
                widget.metricPath = w.metricPath
                widget.range = w.opts.range or '2h'
                widget.refresh = w.opts.refresh
                widget.unit = w.opts.unit or ''
                widget.min = w.opts.min
                widget.max = w.opts.max
                widget.fill = w.opts.fill
            end

            if w.type == 'calendar' then
                widget.calendar = w.calendar
                widget.view = w.opts.view or 'month'
                -- defaults; a viewer can switch them on the widget
                widget.holidays = w.opts.holidays ~= false
                widget.namedays = w.opts.namedays == true
                widget.sun = w.opts.sun ~= false
            end

            -- Select widget: options list
            if w.type == 'select' then
                -- Normalize options to {value, label} format
                local opts_list = {}
                for _, opt in ipairs(w.options or {}) do
                    if type(opt) == 'string' then
                        table.insert(opts_list, { value = opt, label = opt })
                    elseif type(opt) == 'table' then
                        table.insert(opts_list, { value = opt.value, label = opt.label or opt.value })
                    end
                end
                widget.options = opts_list
            end

            -- Image widget: URL (static string or dynamic function)
            if w.type == 'image' then
                if type(w.url) == 'function' then
                    local ok, u = pcall(w.url)
                    widget.url = ok and u or ''
                else
                    widget.url = w.url or ''
                end
                widget.refresh = w.opts.refresh
                widget.height = w.opts.height
                widget.align = w.opts.align
            end

            -- Group label: spans full width, no interaction
            if w.type == 'group' then
                -- label and icon already set above
            end

            -- Cover widget
            if w.type == 'cover' then
                local fns = w.fns or {}
                widget.position = safeGetValue(fns.getPosition)
                local state = safeGetValue(fns.getState)
                widget.state = (state ~= nil) and state or 0
                widget.capabilities = {
                    set = type(fns.set) == 'function'
                }
            end

            -- Dimmer widget
            if w.type == 'dimmer' then
                local fns = w.fns or {}
                widget.value = safeGetValue(fns.get)
                local isOn = safeGetValue(fns.isOn)
                if isOn ~= nil then
                    widget.on = (isOn == true)
                elseif widget.value ~= nil then
                    widget.on = (widget.value > 0)
                end
                widget.capabilities = {
                    toggle = type(fns.switchOn) == 'function' and type(fns.switchOff) == 'function'
                }
            end

            -- Color picker widget
            if w.type == 'color' then
                local fns = w.fns or {}
                local clr = safeGetValue(fns.getColor)
                if type(clr) == 'table' then
                    widget.rgb = {
                        r = clr.r or clr[1] or 0,
                        g = clr.g or clr[2] or 0,
                        b = clr.b or clr[3] or 0
                    }
                end
                widget.brightness = safeGetValue(fns.get)
                widget.white = safeGetValue(fns.getWhite)
                local isOn = safeGetValue(fns.isOn)
                if isOn ~= nil then
                    widget.on = (isOn == true)
                elseif widget.brightness ~= nil then
                    widget.on = (widget.brightness > 0)
                end
                local presets = (w.opts and w.opts.presets) or {
                    {label='Warm White', r=255, g=180, b=100},
                    {label='Cool White', r=224, g=232, b=255},
                    {label='Red', r=255, g=68, b=68},
                    {label='Green', r=68, g=255, b=68},
                    {label='Blue', r=68, g=68, b=255},
                    {label='Purple', r=170, g=68, b=255},
                    {label='Orange', r=255, g=140, b=0},
                    {label='Cyan', r=0, g=206, b=209},
                }
                widget.presets = presets
                widget.capabilities = {
                    brightness = type(fns.get) == 'function' and type(fns.set) == 'function',
                    white = type(fns.getWhite) == 'function' and type(fns.setWhite) == 'function',
                    toggle = type(fns.switchOn) == 'function' and type(fns.switchOff) == 'function'
                }
            end

            -- Confirm dialog text (for buttons)
            if w.opts.confirm then
                widget.confirm = w.opts.confirm
            end

            -- Disabled state
            widget.disabled = safeDisabled(w.opts)

            table.insert(widgets, widget)
        end
    end

    local def = {
        id = dash._id,
        name = dash._name,
        icon = dash._icon,
        columns = dash._columns,
        auth = dash._auth,
        poll = dash._poll,
        theme = dash._theme,
        period = Dashboard.timeOfDay(),
        sections = sections,
        widgets = widgets,
    }

    -- Compute group tabs for navigation
    if dash._groups and #dash._groups > 0 then
        local groupTabs = {}
        for _, grp in ipairs(dash._groups) do
            local tabs = {}
            for _, otherId in ipairs(_dashboardOrder) do
                local otherDb = _dashboards[otherId]
                if otherDb then
                    local otherGroups = otherDb._groups or {}
                    -- For dynamic dashboards, resolve to read _groups
                    if otherDb._isDynamic then
                        local resolved = resolveDashboard(otherId, true)
                        if resolved then
                            otherGroups = resolved._groups or {}
                            -- Also read name/icon from resolved
                            for _, otherGrp in ipairs(otherGroups) do
                                if otherGrp == grp then
                                    tabs[#tabs+1] = {
                                        id = otherId,
                                        name = resolved._name or otherId,
                                        icon = resolved._icon or 'grid',
                                        active = (otherId == id)
                                    }
                                    break
                                end
                            end
                        end
                    else
                        for _, otherGrp in ipairs(otherGroups) do
                            if otherGrp == grp then
                                tabs[#tabs+1] = {
                                    id = otherId,
                                    name = otherDb._name or otherId,
                                    icon = otherDb._icon or 'grid',
                                    active = (otherId == id)
                                }
                                break
                            end
                        end
                    end
                end
            end
            if #tabs >= 2 then
                groupTabs[#groupTabs+1] = { name = grp, tabs = tabs }
            end
        end
        if #groupTabs > 0 then
            def.groupTabs = groupTabs
        end
    end

    return JSON.encode(def)
end

--- Get widget values for polling
-- @param id string Dashboard ID
-- @return string JSON map of widgetId -> {value, disabled}
function DashboardAPI.getValues(id)
    local dash = resolveDashboard(id)
    if not dash then
        return JSON.encode({ error = 'Dashboard not found' })
    end

    local result = {}
    for _, sec in ipairs(dash._sections) do
        for _, w in ipairs(sec.widgets) do
            if w.getValue then
                local entry = {}
                local val, err = safeGetValue(w.getValue)
                if err then
                    entry.value = JSON.null
                    entry.error = err
                else
                    entry.value = val
                end
                entry.disabled = safeDisabled(w.opts)
                -- false: the function gave no colour, the page drops it
                if w.type == 'status' and type(w.opts.color) == 'function' then
                    entry.color = safeColor(id, w) or false
                end
                result[w.id] = entry
            elseif w.type == 'cover' then
                local fns = w.fns or {}
                local entry = { disabled = safeDisabled(w.opts) }
                entry.position = safeGetValue(fns.getPosition)
                local state = safeGetValue(fns.getState)
                entry.state = (state ~= nil) and state or 0
                result[w.id] = entry
            elseif w.type == 'dimmer' then
                local fns = w.fns or {}
                local entry = { disabled = safeDisabled(w.opts) }
                entry.value = safeGetValue(fns.get)
                local isOn = safeGetValue(fns.isOn)
                if isOn ~= nil then
                    entry.on = (isOn == true)
                elseif entry.value ~= nil then
                    entry.on = (entry.value > 0)
                end
                result[w.id] = entry
            elseif w.type == 'color' then
                local fns = w.fns or {}
                local entry = { disabled = safeDisabled(w.opts) }
                local clr = safeGetValue(fns.getColor)
                if type(clr) == 'table' then
                    entry.rgb = {
                        r = clr.r or clr[1] or 0,
                        g = clr.g or clr[2] or 0,
                        b = clr.b or clr[3] or 0
                    }
                end
                entry.brightness = safeGetValue(fns.get)
                entry.white = safeGetValue(fns.getWhite)
                local isOn = safeGetValue(fns.isOn)
                if isOn ~= nil then
                    entry.on = (isOn == true)
                elseif entry.brightness ~= nil then
                    entry.on = (entry.brightness > 0)
                end
                result[w.id] = entry
            elseif w.opts and type(w.opts.disabled) == 'function' then
                -- Button with disabled callback
                result[w.id] = { disabled = safeDisabled(w.opts) }
            end
        end
    end

    return JSON.encode(result)
end

--- Execute a widget action
-- @param id string Dashboard ID
-- @param widgetId string Widget ID
-- @param action string Action type (tap, toggle, set)
-- @param value any Action value (for toggle: bool, for set: number)
-- @return string JSON result
function DashboardAPI.executeAction(id, widgetId, action, value)
    local dash = resolveDashboard(id)
    if not dash then
        return JSON.encode({ error = 'Dashboard not found' })
    end

    -- Find widget by ID
    local widget = nil
    for _, sec in ipairs(dash._sections) do
        for _, w in ipairs(sec.widgets) do
            if w.id == widgetId then
                widget = w
                break
            end
        end
        if widget then break end
    end

    if not widget then
        return JSON.encode({ error = 'Widget not found' })
    end

    -- Check disabled
    if safeDisabled(widget.opts) then
        return JSON.encode({ error = 'Widget is disabled' })
    end

    -- Validate action matches widget type
    if widget.type == 'button' then
        if action ~= 'tap' then
            return JSON.encode({ error = 'Invalid action for button' })
        end
        local ok, ret = pcall(widget.onTap)
        if not ok then
            return JSON.encode({ error = tostring(ret) })
        end
        -- onTap can return a string message for toast notification
        local result = { success = true }
        if type(ret) == 'string' then result.message = ret end
        return JSON.encode(result)

    elseif widget.type == 'toggle' then
        if action ~= 'toggle' then
            return JSON.encode({ error = 'Invalid action for toggle' })
        end
        local boolVal = value == true or value == 'true'
        local ok, ret = pcall(widget.onToggle, boolVal)
        if not ok then
            return JSON.encode({ error = tostring(ret) })
        end
        local result = { success = true, value = boolVal }
        if type(ret) == 'string' then result.message = ret end
        return JSON.encode(result)

    elseif widget.type == 'slider' then
        if action ~= 'set' then
            return JSON.encode({ error = 'Invalid action for slider' })
        end
        local numVal = tonumber(value)
        if not numVal then
            return JSON.encode({ error = 'Value must be a number' })
        end
        if numVal < widget.min or numVal > widget.max then
            return JSON.encode({ error = 'Value out of range [' .. widget.min .. ', ' .. widget.max .. ']' })
        end
        local ok, ret = pcall(widget.onSet, numVal)
        if not ok then
            return JSON.encode({ error = tostring(ret) })
        end
        local result = { success = true, value = numVal }
        if type(ret) == 'string' then result.message = ret end
        return JSON.encode(result)

    elseif widget.type == 'select' then
        if action ~= 'select' then
            return JSON.encode({ error = 'Invalid action for select' })
        end
        local ok, ret = pcall(widget.onSelect, value)
        if not ok then
            return JSON.encode({ error = tostring(ret) })
        end
        local result = { success = true, value = value }
        if type(ret) == 'string' then result.message = ret end
        return JSON.encode(result)

    elseif widget.type == 'status' then
        return JSON.encode({ error = 'Read-only widget' })

    elseif widget.type == 'link' then
        return JSON.encode({ error = 'Link widget has no server action' })

    elseif widget.type == 'image' then
        return JSON.encode({ error = 'Image widget has no server action' })

    elseif widget.type == 'group' then
        return JSON.encode({ error = 'Group label has no server action' })

    elseif widget.type == 'cover' then
        local fns = widget.fns or {}
        if action == 'open' then
            if type(fns.open) ~= 'function' then return JSON.encode({ error = 'No open function' }) end
            local ok, ret = pcall(fns.open)
            if not ok then return JSON.encode({ error = tostring(ret) }) end
            return JSON.encode({ success = true })
        elseif action == 'close' then
            if type(fns.close) ~= 'function' then return JSON.encode({ error = 'No close function' }) end
            local ok, ret = pcall(fns.close)
            if not ok then return JSON.encode({ error = tostring(ret) }) end
            return JSON.encode({ success = true })
        elseif action == 'stop' then
            if type(fns.stop) ~= 'function' then return JSON.encode({ error = 'No stop function' }) end
            local ok, ret = pcall(fns.stop)
            if not ok then return JSON.encode({ error = tostring(ret) }) end
            return JSON.encode({ success = true })
        elseif action == 'set' then
            if type(fns.set) ~= 'function' then return JSON.encode({ error = 'No set function' }) end
            local numVal = tonumber(value)
            if not numVal then return JSON.encode({ error = 'Value must be a number' }) end
            if numVal < 0 or numVal > 100 then return JSON.encode({ error = 'Value out of range [0, 100]' }) end
            local ok, ret = pcall(fns.set, numVal)
            if not ok then return JSON.encode({ error = tostring(ret) }) end
            return JSON.encode({ success = true, value = numVal })
        else
            return JSON.encode({ error = 'Invalid action for cover' })
        end

    elseif widget.type == 'dimmer' then
        local fns = widget.fns or {}
        if action == 'set' then
            if type(fns.set) ~= 'function' then return JSON.encode({ error = 'No set function' }) end
            local numVal = tonumber(value)
            if not numVal then return JSON.encode({ error = 'Value must be a number' }) end
            if numVal < 0 or numVal > 100 then return JSON.encode({ error = 'Value out of range [0, 100]' }) end
            local ok, ret = pcall(fns.set, numVal)
            if not ok then return JSON.encode({ error = tostring(ret) }) end
            return JSON.encode({ success = true, value = numVal })
        elseif action == 'switchOn' then
            if type(fns.switchOn) ~= 'function' then return JSON.encode({ error = 'No switchOn function' }) end
            local ok, ret = pcall(fns.switchOn)
            if not ok then return JSON.encode({ error = tostring(ret) }) end
            return JSON.encode({ success = true })
        elseif action == 'switchOff' then
            if type(fns.switchOff) ~= 'function' then return JSON.encode({ error = 'No switchOff function' }) end
            local ok, ret = pcall(fns.switchOff)
            if not ok then return JSON.encode({ error = tostring(ret) }) end
            return JSON.encode({ success = true })
        else
            return JSON.encode({ error = 'Invalid action for dimmer' })
        end

    elseif widget.type == 'color' then
        local fns = widget.fns or {}
        if action == 'setColor' then
            if type(fns.setColor) ~= 'function' then return JSON.encode({ error = 'No setColor function' }) end
            if type(value) ~= 'table' then return JSON.encode({ error = 'Value must be {r, g, b}' }) end
            local function channel(v) return math.floor(math.max(0, math.min(255, tonumber(v) or 0)) + 0.5) end
            local ok, ret = pcall(fns.setColor, channel(value.r), channel(value.g), channel(value.b))
            if not ok then return JSON.encode({ error = tostring(ret) }) end
            return JSON.encode({ success = true })
        elseif action == 'set' then
            if type(fns.set) ~= 'function' then return JSON.encode({ error = 'No set function' }) end
            local numVal = tonumber(value)
            if not numVal then return JSON.encode({ error = 'Value must be a number' }) end
            if numVal < 0 or numVal > 100 then return JSON.encode({ error = 'Value out of range [0, 100]' }) end
            local ok, ret = pcall(fns.set, numVal)
            if not ok then return JSON.encode({ error = tostring(ret) }) end
            return JSON.encode({ success = true, value = numVal })
        elseif action == 'setWhite' then
            if type(fns.setWhite) ~= 'function' then return JSON.encode({ error = 'No setWhite function' }) end
            local numVal = tonumber(value)
            if not numVal then return JSON.encode({ error = 'Value must be a number' }) end
            if numVal < 0 or numVal > 100 then return JSON.encode({ error = 'Value out of range [0, 100]' }) end
            local ok, ret = pcall(fns.setWhite, numVal)
            if not ok then return JSON.encode({ error = tostring(ret) }) end
            return JSON.encode({ success = true, value = numVal })
        elseif action == 'switchOn' then
            if type(fns.switchOn) ~= 'function' then return JSON.encode({ error = 'No switchOn function' }) end
            local ok, ret = pcall(fns.switchOn)
            if not ok then return JSON.encode({ error = tostring(ret) }) end
            return JSON.encode({ success = true })
        elseif action == 'switchOff' then
            if type(fns.switchOff) ~= 'function' then return JSON.encode({ error = 'No switchOff function' }) end
            local ok, ret = pcall(fns.switchOff)
            if not ok then return JSON.encode({ error = tostring(ret) }) end
            return JSON.encode({ success = true })
        else
            return JSON.encode({ error = 'Invalid action for color' })
        end
    end

    return JSON.encode({ error = 'Unknown widget type' })
end

--- Verify PIN for a dashboard
-- @param id string Dashboard ID
-- @param pin string PIN to verify
-- @return string JSON result
function DashboardAPI.verifyPin(id, pin)
    local dash = resolveDashboard(id, true)
    if not dash then
        return JSON.encode({ valid = false })
    end
    if dash._auth ~= 'pin' or not dash._pin then
        return JSON.encode({ valid = false })
    end
    -- :pin(1234) is a number, the typed PIN a string
    return JSON.encode({ valid = (tostring(pin) == tostring(dash._pin)) })
end

--- PIN of a 'pin' dashboard, for checking cookies server-side (never sent to pages)
-- @return string JSON {pin=...} or {}
function DashboardAPI.getPin(id)
    local dash = resolveDashboard(id, true)
    if not dash or dash._auth ~= 'pin' or not dash._pin then
        return JSON.encode({})
    end
    return JSON.encode({ pin = tostring(dash._pin) })
end

-- ==========================================================================
-- Module interface
-- ==========================================================================

local Dashboard = {}

--- Create a new DashboardBuilder for the given ID.
-- @api public
function Dashboard.new(id)
    return DashboardBuilder:new(id)
end

--- Register a dynamic dashboard that rebuilds on every definition request.
-- The builder function receives a fresh DashboardBuilder and should configure it.
-- build() is called automatically if the function doesn't call it.
-- @param id string Dashboard ID
-- @param builderFn function(DashboardBuilder) Builder function
-- @api public
function Dashboard.dynamic(id, builderFn)
    if type(id) ~= 'string' or id == '' then
        error('Dashboard.dynamic() requires a non-empty string ID')
    end
    if type(builderFn) ~= 'function' then
        error('Dashboard.dynamic() requires a builder function')
    end
    -- Store a sentinel with the builder function
    _dashboards[id] = { _isDynamic = true, _builderFn = builderFn, _id = id }

    -- Track insertion order
    local found = false
    for _, oid in ipairs(_dashboardOrder) do
        if oid == id then found = true; break end
    end
    if not found then
        table.insert(_dashboardOrder, id)
    end
end

--- Return current time-of-day period name.
-- @return string "morning"|"afternoon"|"evening"|"night"
-- @api public
function Dashboard.timeOfDay()
    local h = tonumber(os.date("%H"))
    if h >= 6 and h < 12 then return "morning"
    elseif h >= 12 and h < 17 then return "afternoon"
    elseif h >= 17 and h < 22 then return "evening"
    else return "night" end
end

--- Return human-friendly label for a period.
-- @api public
function Dashboard.periodLabel(period)
    local labels = {
        morning = "Rano", afternoon = "Popołudnie",
        evening = "Wieczór", night = "Noc",
    }
    return labels[period] or period
end

-- Attach API for Go calls
Dashboard.API = DashboardAPI

return Dashboard
