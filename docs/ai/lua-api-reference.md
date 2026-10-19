# vCLU Lua API Reference

Auto-generated from `@api public` tags in Lua sources.
Only public API is listed — internal/private functions are omitted.

## Table of Contents

- [utils.Helpers](#utilshelpers) (20 functions)
- [objects.VirtualSwitch](#objectsvirtualswitch) (12 functions)
- [objects.VirtualDimmer](#objectsvirtualdimmer) (14 functions)
- [objects.VirtualNumber](#objectsvirtualnumber) (4 functions)
- [objects.VirtualSensor](#objectsvirtualsensor) (6 functions)
- [objects.VirtualText](#objectsvirtualtext) (3 functions)
- [objects.VirtualCover](#objectsvirtualcover) (10 functions)
- [objects.VirtualObject](#objectsvirtualobject) (12 functions)
- [ExposedObjects](#exposedobjects) (21 functions)
- [core.Registry](#coreregistry) (9 functions)
- [core.Metrics](#coremetrics) (1 functions)
- [Dashboard](#dashboard) (48 functions)
- [lib.panel](#libpanel) (55 functions)
- [objects.RemotePanel](#objectsremotepanel) (6 functions)
- [GPIO_DOUT](#gpio_dout) (1 functions)
- [GPIO_DIN](#gpio_din) (1 functions)
- [integrations.mqtt.MqttClient](#integrationsmqttmqttclient) (30 functions)

---

## utils.Helpers

### `clamp(value, min, max) → number Clamped value`

Clamp value to min-max range

- `value` (number) — Value to clamp
- `min` (number) — Minimum value
- `max` (number) — Maximum value

**Returns:** number Clamped value

### `lerp(a, b, t) → number Interpolated value`

Linear interpolation between two values

- `a` (number) — Start value
- `b` (number) — End value
- `t` (number) — Progress (0-1)

**Returns:** number Interpolated value

### `map(value, inMin, inMax, outMin, outMax) → number Mapped value`

Map value from one range to another

- `value` (number) — Input value
- `inMin` (number) — Input range minimum
- `inMax` (number) — Input range maximum
- `outMin` (number) — Output range minimum
- `outMax` (number) — Output range maximum

**Returns:** number Mapped value

### `round(value, decimals) → number Rounded value`

Round number to given precision

- `value` (number) — Value to round
- `decimals` (number) — Number of decimal places (default 0)

**Returns:** number Rounded value

### `after(ms, callback) → number Timer ID`

Execute function after delay (with slow callback detection)

- `ms` (number) — Delay in milliseconds
- `callback` (function) — Function to execute

**Returns:** number Timer ID

### `every(ms, callback) → number Timer ID`

Execute function repeatedly (with overlap warning)

- `ms` (number) — Interval in milliseconds
- `callback` (function) — Function to execute

**Returns:** number Timer ID

### `cancel(id)`

Cancel timer

- `id` (number) — Timer ID

### `on(eventName, callback) → function Unsubscribe function`

Listen for an event on the shared EventBus

- `eventName` (string) — Event name (e.g. "weather:changed")
- `callback` (function) — Handler function

**Returns:** function Unsubscribe function

```lua
on("weather:changed", function(data) print(data.temp) end)
```

### `emit(eventName)`

Emit an event on the shared EventBus

- `eventName` (string) — Event name

```lua
emit("weather:changed", { temp = 22.5 })
```

### `setTimeout(callback, ms) → number Timer ID`

One-shot timer (JS-style: setTimeout(fn, ms))

- `callback` (function) — Function to execute
- `ms` (number) — Delay in milliseconds

**Returns:** number Timer ID

### `setInterval(callback, ms) → number Timer ID`

Repeating timer (JS-style: setInterval(fn, ms))

- `callback` (function) — Function to execute
- `ms` (number) — Interval in milliseconds

**Returns:** number Timer ID

### `clearTimeout(id)`

Clear any timer by ID

- `id` (number) — Timer ID from setTimeout or setInterval

### `scene(name, fn)`

Define a scene

- `name` (string) — Scene name
- `fn` (function) — Scene function

### `getScene(name) → table Scene object`

Get scene by name

- `name` (string) — Scene name

**Returns:** table Scene object

### `runScene(name)`

Run scene by name

- `name` (string) — Scene name

### `getAllScenes() → table Array of scene names`

Get all scene names

**Returns:** table Array of scene names

### `sequence() → table Sequence builder`

Create a sequence of actions with delays. Returns a builder with methods: seq:add(fn), seq:wait(ms), seq:run(onComplete), seq:stop()

**Returns:** table Sequence builder

### `cascade(group, delayMs, fn)`

Execute function on each object with delay between

- `group` (table) — RemoteGroup
- `delayMs` (number) — Delay between objects in ms
- `fn` (function) — Function to call on each object

### `inspect(value, depth) → string Human-readable representation`

Inspect any value and return human-readable string Recognizes: Plugin, RemoteObject, RemoteGroup, Registry objects, tables, primitives

- `value` (any) — Value to inspect
- `depth` (number) — Max recursion depth (default 2)

**Returns:** string Human-readable representation

### `dump(value, label)`

Print inspect result (convenience wrapper)

- `value` (any) — Value to inspect
- `label` (string) — Optional label

---

## objects.VirtualSwitch

### `VirtualSwitch:new(name, opts) → VirtualSwitch The new virtual switch instance`

Creates a new VirtualSwitch instance.

- `name` (string) — Optional name for the switch
- `opts` (table) — Optional: initial (default 0), persist (true or a key: keep the value across reloads and restarts)

**Returns:** VirtualSwitch The new virtual switch instance

### `VirtualSwitch:getValue() → number Current state (0 = off, 1 = on)`

Gets the current value of the switch.

**Returns:** number Current state (0 = off, 1 = on)

### `VirtualSwitch:setValue(value)`

Sets the value of the switch.

- `value` (number) — The state to set (0 = off, 1 = on)

### `VirtualSwitch:switchOn()`

Turns the switch on.

### `VirtualSwitch:switchOff()`

Turns the switch off.

### `VirtualSwitch:on()`

Shorthand to turn the switch on.

### `VirtualSwitch:off()`

Shorthand to turn the switch off.

### `VirtualSwitch:toggle()`

Toggles the switch state.

### `VirtualSwitch:isOn() → boolean True if state == 1`

Checks if the switch is on.

**Returns:** boolean True if state == 1

### `VirtualSwitch:updateState(newValue)`

Updates the switch state and triggers appropriate events.

- `newValue` (number) — The new state value (0 or 1)

### `VirtualSwitch:onSwitchOn(callback) → VirtualSwitch Self for method chaining`

Registers a callback for when the switch turns on.

- `callback` (function) — Function called when switch turns on

**Returns:** VirtualSwitch Self for method chaining

### `VirtualSwitch:onSwitchOff(callback) → VirtualSwitch Self for method chaining`

Registers a callback for when the switch turns off.

- `callback` (function) — Function called when switch turns off

**Returns:** VirtualSwitch Self for method chaining

---

## objects.VirtualDimmer

### `VirtualDimmer:new(name, opts) → VirtualDimmer The new virtual dimmer instance`

Creates a new VirtualDimmer instance.

- `name` (string) — Optional name for the dimmer
- `opts` (table) — Optional: initial (default 0), persist (true or a key: keep the value across reloads and restarts)

**Returns:** VirtualDimmer The new virtual dimmer instance

### `VirtualDimmer:getValue() → number Current brightness`

Gets the current brightness value (0.0 to 1.0).

**Returns:** number Current brightness

### `VirtualDimmer:setValue(value)`

Sets the brightness value (0.0 to 1.0).

- `value` (number) — Brightness value

### `VirtualDimmer:getBrightness() → number Brightness percentage (0 to 100)`

Gets the current brightness as a percentage.

**Returns:** number Brightness percentage (0 to 100)

### `VirtualDimmer:setBrightness(percent)`

Sets the brightness as a percentage.

- `percent` (number) — Brightness percentage (0 to 100)

### `VirtualDimmer:switchOn()`

Turns the dimmer on, restoring last brightness.

### `VirtualDimmer:switchOff()`

Turns the dimmer off, saving current brightness for restore.

### `VirtualDimmer:on()`

Shorthand to turn the dimmer on.

### `VirtualDimmer:off()`

Shorthand to turn the dimmer off.

### `VirtualDimmer:toggle()`

Toggles the dimmer state.

### `VirtualDimmer:isOn() → boolean True if brightness > 0`

Checks if the dimmer is on (brightness > 0).

**Returns:** boolean True if brightness > 0

### `VirtualDimmer:updateState(newValue)`

Updates the dimmer state and triggers appropriate events.

- `newValue` (number) — The new brightness value (0.0 to 1.0)

### `VirtualDimmer:onSwitchOn(callback) → VirtualDimmer Self for method chaining`

Registers a callback for when the dimmer turns on.

- `callback` (function) — Function called when dimmer turns on

**Returns:** VirtualDimmer Self for method chaining

### `VirtualDimmer:onSwitchOff(callback) → VirtualDimmer Self for method chaining`

Registers a callback for when the dimmer turns off.

- `callback` (function) — Function called when dimmer turns off

**Returns:** VirtualDimmer Self for method chaining

---

## objects.VirtualNumber

### `VirtualNumber:new(name, opts) → VirtualNumber The new virtual number instance`

Creates a new VirtualNumber instance.

- `name` (string) — Optional name for the number object
- `opts` (table) — Optional config: min, max, step, unit, initial, persist

**Returns:** VirtualNumber The new virtual number instance

### `VirtualNumber:getValue() → number Current value`

Gets the current numeric value.

**Returns:** number Current value

### `VirtualNumber:setValue(value)`

Sets the numeric value (clamped to configured range).

- `value` (number) — The value to set

### `VirtualNumber:getMeta() → table {min, max, step, unit}`

Returns configured metadata (useful for dashboard widgets).

**Returns:** table {min, max, step, unit}

---

## objects.VirtualSensor

### `VirtualSensor:new(name, opts) → VirtualSensor The new virtual sensor instance`

Creates a new VirtualSensor instance.

- `name` (string) — Optional name for the sensor
- `opts` (table) — Optional: initial (default 0), persist (true or a key: keep the value across reloads and restarts)

**Returns:** VirtualSensor The new virtual sensor instance

### `VirtualSensor:getValue() → number Current state (0 = inactive, 1 = active)`

Gets the current value of the sensor.

**Returns:** number Current state (0 = inactive, 1 = active)

### `VirtualSensor:isActive() → boolean True if state == 1`

Checks if the sensor is active.

**Returns:** boolean True if state == 1

### `VirtualSensor:updateState(newValue)`

Updates the sensor state and triggers appropriate events.

- `newValue` (number) — The new state value (0 or 1)

### `VirtualSensor:onSwitchOn(callback) → VirtualSensor Self for method chaining`

Registers a callback for when the sensor activates.

- `callback` (function) — Function called when sensor turns on

**Returns:** VirtualSensor Self for method chaining

### `VirtualSensor:onSwitchOff(callback) → VirtualSensor Self for method chaining`

Registers a callback for when the sensor deactivates.

- `callback` (function) — Function called when sensor turns off

**Returns:** VirtualSensor Self for method chaining

---

## objects.VirtualText

### `VirtualText:new(name, opts) → VirtualText The new virtual text instance`

Creates a new VirtualText instance.

- `name` (string) — Optional name for the text object
- `opts` (table) — Optional config: initial (default ""), maxLength, persist

**Returns:** VirtualText The new virtual text instance

### `VirtualText:getValue() → string Current value`

Gets the current text value.

**Returns:** string Current value

### `VirtualText:setValue(value)`

Sets the text value.

- `value` (string) — The value to set

---

## objects.VirtualCover

### `VirtualCover:new(name, opts) → VirtualCover The new virtual cover instance`

Creates a new VirtualCover instance.

- `name` (string) — Optional name for the cover
- `opts` (table) — Optional: initial (default 0), persist (true or a key: keep the value across reloads and restarts)

**Returns:** VirtualCover The new virtual cover instance

### `VirtualCover:getPosition() → number Position (0 = closed, 100 = open)`

Gets the current position.

**Returns:** number Position (0 = closed, 100 = open)

### `VirtualCover:setPosition(position)`

Sets the cover to a specific position.

- `position` (number) — Target position (0 = closed, 100 = open)

### `VirtualCover:open()`

Opens the cover (position = 100).

### `VirtualCover:close()`

Closes the cover (position = 0).

### `VirtualCover:stop()`

Stops the cover movement. Override for actual stop command.

### `VirtualCover:up()`

Moves the cover up (alias for open).

### `VirtualCover:down()`

Moves the cover down (alias for close).

### `VirtualCover:updateState(newValue)`

Updates the cover state and triggers appropriate events.

- `newValue` (number) — The new position value

### `VirtualCover:onPositionChange(callback) → VirtualCover Self for method chaining`

Registers a callback for when the cover position changes.

- `callback` (function) — Function called with (newPos, oldPos)

**Returns:** VirtualCover Self for method chaining

---

## objects.VirtualObject

### `VirtualObject:new(name) → VirtualObject The new virtual object instance`

Creates a new VirtualObject instance.

- `name` (string) — Optional name for the object

**Returns:** VirtualObject The new virtual object instance

### `VirtualObject:getValue() → any Current state`

Gets the current state value.

**Returns:** any Current state

### `VirtualObject:onChange(cb) → function Unsubscribe function`

Registers a callback for state changes.

- `cb` (function) — Called with (newValue, oldValue)

**Returns:** function Unsubscribe function

### `VirtualObject:onEvent(eventName, callback) → VirtualObject Self for method chaining`

Registers an event handler for a named event.

- `eventName` (string) — The name of the event
- `callback` (function) — The callback function

**Returns:** VirtualObject Self for method chaining

### `VirtualObject:emit(eventName)`

Emits an event, invoking all registered handlers.

- `eventName` (string) — The name of the event

### `VirtualObject:addTag(...) → VirtualObject Self for method chaining`

Adds one or more tags to this object.

**Returns:** VirtualObject Self for method chaining

### `VirtualObject:removeTag(tag) → VirtualObject Self for method chaining`

Removes a tag from this object.

- `tag` (string) — The tag name to remove

**Returns:** VirtualObject Self for method chaining

### `VirtualObject:hasTag(tag) → boolean True if the object has the tag`

Checks if this object has a specific tag.

- `tag` (string) — The tag name to check

**Returns:** boolean True if the object has the tag

### `VirtualObject:getTags() → table Array of tag names`

Gets all tags assigned to this object.

**Returns:** table Array of tag names

### `VirtualObject:expose(options) → ExposedHandle Handle for configuring the exposure`

Exposes this object to MQTT and HomeKit. Returns an ExposedHandle for fluent configuration.

- `options` (table) — Optional settings

**Returns:** ExposedHandle Handle for configuring the exposure

### `VirtualObject:unexpose() → boolean True if was exposed and now unexposed`

Unexposes this object.

**Returns:** boolean True if was exposed and now unexposed

### `VirtualObject:isExposed() → boolean True if exposed`

Checks if this object is currently exposed.

**Returns:** boolean True if exposed

---

## ExposedObjects

### `ExposedHandle:name(n) → ExposedHandle self for chaining`

Set the display name (fluent)

- `n` (string) — Display name

**Returns:** ExposedHandle self for chaining

### `ExposedHandle:readonly() → ExposedHandle self for chaining`

Mark as read-only (fluent)

**Returns:** ExposedHandle self for chaining

### `ExposedHandle:mqttOnly() → ExposedHandle self for chaining`

Expose only to MQTT (fluent)

**Returns:** ExposedHandle self for chaining

### `ExposedHandle:homekitOnly() → ExposedHandle self for chaining`

Expose only to HomeKit (fluent)

**Returns:** ExposedHandle self for chaining

### `ExposedHandle:group(g) → ExposedHandle self for chaining`

Set group (fluent)

- `g` (string) — Group name

**Returns:** ExposedHandle self for chaining

### `ExposedHandle:area(a) → ExposedHandle self for chaining`

Set physical area/room (fluent)

- `a` (string) — Area name (e.g., "Kuchnia", "Salon")

**Returns:** ExposedHandle self for chaining

### `ExposedHandle:tags(t) → ExposedHandle self for chaining`

Set tags (fluent)

- `t` (table) — Array of tag strings (e.g., {"critical", "noAutoOff"})

**Returns:** ExposedHandle self for chaining

### `ExposedHandle:description(d) → ExposedHandle self for chaining`

Set description for AI agents (fluent)

- `d` (string) — Free-text description with rules and constraints

**Returns:** ExposedHandle self for chaining

### `ExposedHandle:update(opts) → boolean Success, false if handle is inactive`

Update options at runtime

- `opts` (table) — Options to merge (filter = false removes a filter)

**Returns:** boolean Success, false if handle is inactive

### `ExposedHandle:rename(newName) → boolean Success, false if handle is inactive`

Rename the exposed object

- `newName` (string) — New display name

**Returns:** boolean Success, false if handle is inactive

### `ExposedHandle:unexpose()`

Remove from exposed objects (idempotent - safe to call multiple times)

### `ExposedHandle:getPath() → string path`

Get the path identifier

**Returns:** string path

### `ExposedHandle:isExposed() → boolean`

Check if still exposed

**Returns:** boolean

### `ExposedHandle:executeScene() → boolean Success, false if handle is inactive or in cooldown`

Execute scene with debounce protection Prevents double-execution from HomeKit echo

**Returns:** boolean Success, false if handle is inactive or in cooldown

### `ExposedHandle:getLastChanged() → number|nil Epoch seconds when state last changed, or nil if never`

Get last changed timestamp (epoch seconds)

**Returns:** number|nil Epoch seconds when state last changed, or nil if never

### `ExposedHandle:getValue() → any Current value, or nil if handle is inactive`

Get current value from object

**Returns:** any Current value, or nil if handle is inactive

### `ExposedHandle:setValue(value) → boolean Success, false if handle is inactive or readonly`

Set value on object

- `value` (any) — Value to set

**Returns:** boolean Success, false if handle is inactive or readonly

### `ExposedObjects:get(path) → ExposedHandle or nil when nothing is exposed at that path`

Get an exposed handle by path (fields as in list())

- `path` (string) — Exposed path

**Returns:** ExposedHandle or nil when nothing is exposed at that path

### `ExposedObjects:list() → table path -> handle; stable fields: type, options.name, options.area, options.tags, getValue()`

All exposed objects: the live map, read it and never change it

**Returns:** table path -> handle; stable fields: type, options.name, options.area, options.tags, getValue()

### `expose(obj, objType, options) → ExposedHandle`

Global function to expose an object. A custom object's onChange(cb) must return a function that removes cb: unexpose() calls it, otherwise the callback stays registered and expose() logs a warning.

- `obj` (table) — Object to expose
- `objType` (string) — Type (switch, light, sensor, etc.)
- `options` (table) — Optional settings

**Returns:** ExposedHandle

### `exposeRegistry(options) → number Count of exposed objects`

Global function to auto-expose all registry objects Call this after om.lua is loaded to expose all objects to MQTT/HomeKit. Objects can then be modified via their handles.

- `options` (table) — Optional default options

**Returns:** number Count of exposed objects

---

## core.Registry

### `_registry:sync(pathOrName, value)`

Synchronize object state from external source (e.g., real CLU scripts). Updates the state via updateState() and publishes one state_changed. Used by real CLU om.lua scripts: VCLU:execute(0, "_:sync('CLU.DOUT1', value)")

- `pathOrName` (string) — Object path or name
- `value` (any) — New state value

### `_registry:event(pathOrName)`

Emit an event on an object. Used by real CLU scripts to forward events: _:event("CLU.DOUT_01.OnSwitchOn")

- `pathOrName` (string) — Full event path (e.g., "CLU.DOUT_01.OnSwitchOn")

### `_registry:get(pathOrName) → table|nil The object if found`

Get an object by name or path.

- `pathOrName` (string) — Object path or name

**Returns:** table|nil The object if found

### `_registry:byTag(tag) → RemoteGroup A group containing all matching objects`

Get all objects with a specific tag.

- `tag` (string) — The tag to search for

**Returns:** RemoteGroup A group containing all matching objects

### `_registry:byTags(...) → RemoteGroup A group containing all matching objects`

Get objects matching any of the specified tags.

**Returns:** RemoteGroup A group containing all matching objects

### `_registry:tags() → table Array of tag strings`

Get all available tags in the registry.

**Returns:** table Array of tag strings

### `_registry:list() → table Array of tables with path, type, and name`

List all registered objects.

**Returns:** table Array of tables with path, type, and name

### `_registry:find(pattern) → table Array of tables with path and obj fields`

Find objects matching a pattern.

- `pattern` (string) — Lua pattern to match against paths

**Returns:** table Array of tables with path and obj fields

### `registerObject(path, obj)`

Register an object in the registry.

- `path` (string) — Full object path (e.g., "CLU.DOUT_01")
- `obj` (table) — The object instance to register

---

## core.Metrics

### `metric(path, getter, opts) → path`

Register a metric for periodic collection.

- `path` (Unique) — metric identifier (e.g., "salda:outsideAir")
- `getter` (Function) — that returns the current value
- `opts` (Optional) — table: {unit="°C", interval=60, decimals=1}

**Returns:** path

---

## Dashboard

### `SectionHandle:button(id, label, icon, onTap, opts)`

Add a button widget to this section.

- `id` (string) — Unique widget identifier
- `label` (string) — Display label
- `icon` (string) — Bootstrap icon name (e.g., "power", "door-open")
- `onTap` (function) — Callback fired on button press: function()
- `opts` (table) — Optional: {disabled=function()->bool, confirm=string}

### `SectionHandle:toggle(id, label, icon, getValue, onToggle, opts)`

Add a toggle widget to this section.

- `id` (string) — Unique widget identifier
- `label` (string) — Display label
- `icon` (string) — Bootstrap icon name (e.g., "lightbulb", "lamp")
- `getValue` (function) — Returns current state: function() -> boolean|number (truthy=on)
- `onToggle` (function) — Called with desired state: function(bool) — use switchOn/switchOff, NOT toggle()
- `opts` (table) — Optional: {disabled=function()->bool}

### `SectionHandle:slider(id, label, min, max, getValue, onSet, opts)`

Add a slider widget to this section.

- `id` (string) — Unique widget identifier
- `label` (string) — Display label
- `min` (number) — Minimum slider value
- `max` (number) — Maximum slider value
- `getValue` (function) — Returns current value: function() -> number
- `onSet` (function) — Called with new value: function(number)
- `opts` (table) — Optional: {step=number, unit=string, icon=string, disabled=function()->bool}

### `SectionHandle:status(id, label, icon, getValue, unit, opts)`

Add a status widget to this section.

- `id` (string) — Unique widget identifier
- `label` (string) — Display label
- `icon` (string) — Bootstrap icon name (e.g., "thermometer-half", "droplet")
- `getValue` (function) — Returns current value: function() -> number|string
- `unit` (string) — Unit suffix displayed after value (e.g., "°C", "%", "lx")
- `opts` (table) — Optional: {disabled=function()->bool, color=string|function()->string|nil}

### `SectionHandle:link(id, label, icon, url, opts)`

Add a link widget to this section.

- `id` (string) — Unique widget identifier
- `label` (string) — Display label
- `icon` (string) — Bootstrap icon name
- `url` (string) — |function URL string or function() -> string
- `opts` (table) — Optional: {disabled=function()->bool}

### `SectionHandle:chart(id, label, metricPath, opts)`

Add a chart widget to this section.

- `id` (string) — Unique widget identifier
- `label` (string) — Display label (e.g., "Temperatura 24h")
- `metricPath` (string) — Metric path registered via metric() (e.g., "temp:salon", "salda:outsideAir")
- `opts` (table) — Optional: {range=string ("1h","6h","24h","7d"), unit=string}

### `SectionHandle:select(id, label, icon, options, getValue, onSelect, opts)`

Add a select widget to this section.

- `id` (string) — Unique widget identifier
- `label` (string) — Display label
- `icon` (string) — Bootstrap icon name
- `options` (table) — Array of {value=any, label=string} or function() -> same
- `getValue` (function) — Returns current selected value: function() -> any
- `onSelect` (function) — Called with selected value: function(any)
- `opts` (table) — Optional: {disabled=function()->bool}

### `SectionHandle:image(id, label, url, opts)`

Add an image widget to this section.

- `id` (string) — Unique widget identifier
- `label` (string) — Display label
- `url` (string) — |function Image URL string or function() -> string
- `opts` (table) — Optional: {refreshInterval=number (seconds)}

### `SectionHandle:group(label, icon)`

Add a group label widget to this section.

- `label` (string) — Group heading text
- `icon` (string) — Bootstrap icon name (optional)

### `SectionHandle:cover(id, label, icon, fns, opts)`

Add a cover widget to this section. Buttons up/stop/down call open/stop/close. With set, a 0-100% slider is shown under them. Position 0 = closed, 100 = open, as in Grenton; nil (not reported yet) is shown as --. getState lights the button of the movement: 0 = stopped, 1 = up, 2 = down.

- `id` (string) — Unique widget identifier
- `label` (string) — Display label
- `icon` (string) — Bootstrap icon name (e.g., "arrows-expand")
- `fns` (table) — {getPosition=function()->number|nil, getState=function()->number|nil, open=function(), close=function(), stop=function(), set=function(position)}
- `opts` (table) — Optional: {disabled=function()->bool}

### `SectionHandle:dimmer(id, label, icon, fns, opts)`

Add a dimmer widget to this section.

- `id` (string) — Unique widget identifier
- `label` (string) — Display label
- `icon` (string) — Bootstrap icon name (e.g., "brightness-high")
- `fns` (table) — {getValue=function()->number(0-1), setValue=function(number), switchOn=function(), switchOff=function()}
- `opts` (table) — Optional: {disabled=function()->bool}

### `SectionHandle:color(id, label, icon, fns, opts)`

Add a color picker widget to this section. switchOn?, switchOff?, isOn?, getWhite?/setWhite? white 0-100}, or a RemoteRGBLight (white when it has one)

- `id` (string) — Unique widget identifier
- `label` (string) — Display label
- `icon` (string) — Bootstrap icon name (e.g., "palette")
- `fns` (table) — {getColor=function()->{r,g,b}, setColor=function(r,g,b), get?/set? brightness 0-100,
- `opts` (table) — Optional: {disabled=function()->bool}

### `SectionHandle:when(periods, fn)`

Conditional block: only execute fn if current time matches any of the given periods.

- `periods` (table) — Array of period names ("morning","afternoon","evening","night")
- `fn` (function) — Receives self (SectionHandle) as argument

### `SectionHandle:section(name, icon, opts)`

Start a new section (delegates to DashboardBuilder:section).

### `SectionHandle:header()`

Start a header section (delegates to DashboardBuilder:header).

### `SectionHandle:build()`

Finalize and register the dashboard (delegates to DashboardBuilder:build).

### `SectionHandle:name(s)`

Set dashboard name (delegates to DashboardBuilder:name).

### `SectionHandle:theme(t)`

Set dashboard name (delegates to DashboardBuilder:name). Set dashboard theme (delegates to DashboardBuilder:theme).

### `SectionHandle:poll(n)`

Set dashboard name (delegates to DashboardBuilder:name). Set dashboard theme (delegates to DashboardBuilder:theme). Set dashboard poll interval (delegates to DashboardBuilder:poll).

### `SectionHandle:tabGroup(g)`

Set dashboard name (delegates to DashboardBuilder:name). Set dashboard theme (delegates to DashboardBuilder:theme). Set dashboard poll interval (delegates to DashboardBuilder:poll). Add dashboard to a tab group (delegates to DashboardBuilder:tabGroup).

### `DashboardBuilder:name(s)`

Set the display name for this dashboard.

- `s` (string) — Display name shown in navigation

### `DashboardBuilder:icon(s)`

Set the icon for this dashboard.

- `s` (string) — Bootstrap icon name (e.g., "house", "thermometer", "camera-video")

### `DashboardBuilder:columns(n)`

Set the number of grid columns.

- `n` (number) — Number of columns (default 3)

### `DashboardBuilder:auth(s)`

Set the authentication mode ("public", "pin").

- `s` (string) — Auth mode: "public" (no auth) or "pin" (requires PIN)

### `DashboardBuilder:pin(s)`

Set the PIN code for pin-authenticated dashboards.

- `s` (string) — PIN code (used when auth="pin")

### `DashboardBuilder:poll(n)`

Set the poll interval in seconds.

- `n` (number) — Poll interval in seconds (default 2)

### `DashboardBuilder:theme(t)`

Set the dashboard theme.

- `t` (string) — Theme name (e.g., "dark", "light")

### `DashboardBuilder:tabGroup(name)`

Add this dashboard to a named tab group for navigation.

- `name` (string) — Tab group name (dashboards with same group show as tabs)

### `DashboardBuilder:header()`

Start a header section (must be called before any section).

### `DashboardBuilder:section(name, icon, opts) → SectionHandle Handle for adding widgets to this section`

Start a new named section for grouping widgets.

- `name` (string) — Section title
- `icon` (string) — Bootstrap icon name
- `opts` (table) — Optional: {columns=number}

**Returns:** SectionHandle Handle for adding widgets to this section

### `DashboardBuilder:button(id, label, icon, onTap, opts)`

Add a button widget to the current section.

- `id` (string) — Unique widget identifier
- `label` (string) — Display label
- `icon` (string) — Bootstrap icon name (e.g., "power", "door-open")
- `onTap` (function) — Callback fired on button press: function()
- `opts` (table) — Optional: {disabled=function()->bool, confirm=string}

### `DashboardBuilder:toggle(id, label, icon, getValue, onToggle, opts)`

Add a toggle widget to the current section.

- `id` (string) — Unique widget identifier
- `label` (string) — Display label
- `icon` (string) — Bootstrap icon name (e.g., "lightbulb", "lamp")
- `getValue` (function) — Returns current state: function() -> boolean|number (truthy=on)
- `onToggle` (function) — Called with desired state: function(bool) — use switchOn/switchOff, NOT toggle()
- `opts` (table) — Optional: {disabled=function()->bool}

### `DashboardBuilder:slider(id, label, min, max, getValue, onSet, opts)`

Add a slider widget to the current section.

- `id` (string) — Unique widget identifier
- `label` (string) — Display label
- `min` (number) — Minimum slider value
- `max` (number) — Maximum slider value
- `getValue` (function) — Returns current value: function() -> number
- `onSet` (function) — Called with new value: function(number)
- `opts` (table) — Optional: {step=number, unit=string, icon=string, disabled=function()->bool}

### `DashboardBuilder:status(id, label, icon, getValue, unit, opts)`

Add a status widget to the current section.

- `id` (string) — Unique widget identifier
- `label` (string) — Display label
- `icon` (string) — Bootstrap icon name (e.g., "thermometer-half", "droplet")
- `getValue` (function) — Returns current value: function() -> number|string
- `unit` (string) — Unit suffix displayed after value (e.g., "°C", "%", "lx")
- `opts` (table) — Optional: {disabled=function()->bool, color=string|function()->string|nil}

### `DashboardBuilder:link(id, label, icon, url, opts)`

Add a link widget to the current section.

- `id` (string) — Unique widget identifier
- `label` (string) — Display label
- `icon` (string) — Bootstrap icon name
- `url` (string) — |function URL string or function() -> string
- `opts` (table) — Optional: {disabled=function()->bool}

### `DashboardBuilder:chart(id, label, metricPath, opts)`

Add a chart widget to the current section.

- `id` (string) — Unique widget identifier
- `label` (string) — Display label (e.g., "Temperatura 24h")
- `metricPath` (string) — Metric path registered via metric() (e.g., "temp:salon", "salda:outsideAir")
- `opts` (table) — Optional: {range=string ("1h","6h","24h","7d"), unit=string}

### `DashboardBuilder:select(id, label, icon, options, getValue, onSelect, opts)`

Add a select widget to the current section.

- `id` (string) — Unique widget identifier
- `label` (string) — Display label
- `icon` (string) — Bootstrap icon name
- `options` (table) — Array of {value=any, label=string} or function() -> same
- `getValue` (function) — Returns current selected value: function() -> any
- `onSelect` (function) — Called with selected value: function(any)
- `opts` (table) — Optional: {disabled=function()->bool}

### `DashboardBuilder:image(id, label, url, opts)`

Add an image widget to the current section.

- `id` (string) — Unique widget identifier
- `label` (string) — Display label
- `url` (string) — |function Image URL string or function() -> string
- `opts` (table) — Optional: {refreshInterval=number (seconds)}

### `DashboardBuilder:group(label, icon)`

Add a group label widget to the current section.

- `label` (string) — Group heading text
- `icon` (string) — Bootstrap icon name (optional)

### `DashboardBuilder:cover(id, label, icon, fns, opts)`

Add a cover widget to the current section. Buttons up/stop/down call open/stop/close. With set, a 0-100% slider is shown under them. Position 0 = closed, 100 = open, as in Grenton; nil (not reported yet) is shown as --. getState lights the button of the movement: 0 = stopped, 1 = up, 2 = down.

- `id` (string) — Unique widget identifier
- `label` (string) — Display label
- `icon` (string) — Bootstrap icon name (e.g., "arrows-expand")
- `fns` (table) — {getPosition=function()->number|nil, getState=function()->number|nil, open=function(), close=function(), stop=function(), set=function(position)}
- `opts` (table) — Optional: {disabled=function()->bool}

### `DashboardBuilder:dimmer(id, label, icon, fns, opts)`

Add a dimmer widget to the current section.

- `id` (string) — Unique widget identifier
- `label` (string) — Display label
- `icon` (string) — Bootstrap icon name (e.g., "brightness-high")
- `fns` (table) — {getValue=function()->number(0-1), setValue=function(number), switchOn=function(), switchOff=function()}
- `opts` (table) — Optional: {disabled=function()->bool}

### `DashboardBuilder:color(id, label, icon, fns, opts)`

Add a color picker widget to the current section. switchOn?, switchOff?, isOn?, getWhite?/setWhite? white 0-100}, or a RemoteRGBLight (white when it has one)

- `id` (string) — Unique widget identifier
- `label` (string) — Display label
- `icon` (string) — Bootstrap icon name (e.g., "palette")
- `fns` (table) — {getColor=function()->{r,g,b}, setColor=function(r,g,b), get?/set? brightness 0-100,
- `opts` (table) — Optional: {disabled=function()->bool}

### `DashboardBuilder:when(periods, fn)`

Conditional block: only execute fn if current time matches any of the given periods.

- `periods` (table) — |string Period name(s): "morning","afternoon","evening","night"
- `fn` (function) — Receives self (DashboardBuilder) as argument

### `DashboardBuilder:build()`

Finalize and register the dashboard in the global registry.

### `Dashboard.new(id)`

Create a new DashboardBuilder for the given ID.

### `Dashboard.dynamic(id, builderFn)`

Register a dynamic dashboard that rebuilds on every definition request. The builder function receives a fresh DashboardBuilder and should configure it. build() is called automatically if the function doesn't call it.

- `id` (string) — Dashboard ID
- `builderFn` (function) — (DashboardBuilder) Builder function

### `Dashboard.timeOfDay() → string "morning"|"afternoon"|"evening"|"night"`

Return current time-of-day period name.

**Returns:** string "morning"|"afternoon"|"evening"|"night"

### `Dashboard.periodLabel(period)`

Return human-friendly label for a period.

---

## lib.panel

### `Panel.lang(code)`

Sets Panel.strings to a language ("pl"); English is the default.

- `code` (string) — Language code from Panel.languages

### `Panel.map(s)`

Text with the letters the panel lacks replaced.

### `Panel.text(s)`

Text as the panel can print it: letters mapped, cut to MAX_TEXT.

### `Screen:text(x, y, s, font)`

Text at x, y (y is the top), font 1-3; cut to 15 characters.

### `Screen:textRight(y, s, font, right)`

Text aligned to the right edge (or to x as its right end).

### `Screen:textCenter(y, s, font, x0, x1)`

Text centred between x0 and x1 (default: the whole width).

### `Screen:float(x, y, n, precision, font)`

A number printed by the panel itself, with precision 0-2.

### `Screen:line(x, y, xe, ye, color)`

A line from x, y to xe, ye (color 1 white, 0 black).

### `Screen:rule(y)`

A horizontal rule across the screen.

### `Screen:point(x, y, color)`

One pixel.

### `Screen:icon(x, y, file)`

An icon from the panel's card: file name without .bmp (most are 64 x 32, arrows 16 x 16).

### `Panel.new(obj) → Panel`

Takes over a panel.

- `obj` (RemotePanel) — The panel from the OM import

**Returns:** Panel

### `Panel.find(id)`

A panel from the OM import by its object id or name ("PAN1085"), or nil.

### `Panel:buttons(map)`

Which physical button is in which corner.

- `map` (table) — { tl = "btn1", tr = "btn2", bl = "btn3", br = "btn4" }

### `Panel:back(corner)`

The corner that is back on every screen (default "tr"; ok is below or above it).

### `Panel:home(page)`

The home page (a menu); without an argument, goes home.

### `Panel:sleepAfter(seconds)`

After this many seconds without input: the idle screen, or home.

### `Panel:onGesture(name, fn)`

A gesture handler: name "swipe:up" / "swipe:down" / "swipe:left" / "swipe:right".

### `Panel:onProximity(fn)`

Called when someone comes close to the panel.

### `Panel:open(page)`

Opens a page on top of the current one.

### `Panel:goBack()`

Closes the current page; on home, goes to the idle screen.

### `Panel:goHome()`

Closes every page and shows home.

### `Panel:goIdle()`

Closes every page and shows the idle screen (or home).

### `Panel:name(name, page)`

Names a page so panel:open("name") and gestures can open it.

### `Panel:debounce(key, seconds, fn)`

Runs fn once, seconds after the last call with this key.

### `Panel:notify(text, opts)`

Shows a message over the current page; OK, back or the timeout closes it.

- `text` (string)
- `opts` (table) — |nil { beep = true, seconds = 30, title = "..." }

### `Panel:redraw(full)`

Asks for a redraw; changes within REDRAW_MS make one frame.

- `full` (boolean) — Kept for callers; every frame redraws the whole screen

### `Panel.register(name, factory)`

Adds a page type: Panel.<name>(...) calls factory(...) and returns the page.

### `Panel.menu(name, submenus)`

A list of rows; up/down move, OK opens or edits, back leaves (or cancels an edit).

- `name` (string) — Title
- `submenus` (table) — |nil Menus to add as submenus (empty ones are left out)

### `Menu:item(label, page)`

A row that opens a page or a submenu (also Menu:submenu).

### `Menu:submenu(label, menu)`

A row that opens a menu; a menu with no rows adds nothing. Without a label: the menu's name.

### `Menu:toggle(label, obj)`

A row with an on/off object (or its registry path); OK switches it. No object: no row.

### `Menu:value(label, src, format, page)`

A read-only value; with a page (e.g. Panel.chart), OK opens it.

### `Menu:number(label, get, set, opts)`

A number edited in place: OK, then +/-, OK sets it once, back cancels.

- `label` (string) — Row label
- `get` (function) — Returns the current value
- `set` (function) — Gets the new value once, on OK
- `opts` (table) — { step = 1, min, max, format = "%.1f", watch = obj }

### `Menu:dimmer(obj, opts)`

A dimmer's brightness edited in place, 0-100 by 10; 0 shows OFF. No object: no row. without them the row switches it on and off

- `obj` (table) — |string Object with getBrightness/setBrightness (percent), or its path;
- `opts` (table) — |nil { step = 10, format }

### `Menu:choice(label, get, set, opts)`

One of a list of options edited in place.

- `label` (string) — Row label
- `get` (function) — Returns the current option
- `set` (function) — Gets the chosen option once, on OK
- `opts` (table) — { options = { "A", "B" }, watch = obj }

### `Menu:action(label, fn, opts)`

A row that runs fn(panel) (or opens a page); opts.confirm asks first.

### `Panel.kind(obj) → string|nil "switch", "light", "dimmer", "cover", "temperature", "sensor", ...`

What device an object is: its expose() type, else the one of its Grenton type (TypeMap).

**Returns:** string|nil "switch", "light", "dimmer", "cover", "temperature", "sensor", ...

### `Panel.press(obj)`

One press on a device: a cover opens (closes when open), anything else switches.

### `Menu:add(label, obj, opts)`

A row for a device, by what it is (Panel.kind): a switch or light switches, a dimmer edits its brightness, a cover opens its page, a sensor's value opens its chart (a binary sensor's its history). No label: the name from expose(). readonly: the state only, nothing switches (expose(..., { readonly = true }) sets it)

- `label` (string) — |nil Row label
- `obj` (table) — |string Object or registry path; missing: no row
- `opts` (table) — |nil { kind, format, chart = false, history = false, readonly = true }

### `Menu:gate(obj, opts)`

A gate (or a door, a window): open or closed, OK shows when it changed. "closed" gives 0 when open)

- `obj` (table) — |string A binary input or its path; missing: no row
- `opts` (table) — |nil { open = 1 } the value that means open (a Grenton input wired to

### `Menu:thermostat(label, obj, opts)`

A Grenton thermostat: the temperature at its sensor; OK opens its page with the target. + and - write the set point by hand, so only in manual mode (in holiday or auto mode the thermostat follows its own value or the schedule, and the page only shows). min, max (what + and - can reach), format = "%.1f" }

- `label` (string) — Row label and page title
- `obj` (table) — |string A RemoteThermostat or its path; missing: no row
- `opts` (table) — |nil { readonly = true (the page shows, nothing changes), step = 0.5,

### `Menu:cover(label, obj)`

A cover: its position (^ or v while it moves); OK opens its page.

### `Panel.cover(name, obj)`

A cover on its own page: up opens, down closes, OK stops; position and movement follow.

- `name` (string) — Title
- `obj` (table) — |string Cover (open, close, stop, getPosition) or its path

### `Panel.area(area, opts)`

A menu of the devices expose() put in an area, each with its row (Menu:add), named as in Home Assistant and HomeKit; filled each time it opens, so devices a plugin exposes later show up. Rows added by hand come after the devices. Lights, dimmers and covers first, then temperature and humidity; switches (pumps, gates, door openers) and sensors only when asked for in kinds. Like homekit and mqtt, expose(obj, type, { panel = false }) keeps an object off the panels and panel = true puts it on whatever its kind (e.g. the intercom).

- `area` (string) — |table Area name, or several (one room under two names)
- `opts` (table) — |nil { title, kinds = { "light", "dimmer", "cover", "temperature", "humidity" }, skip = { path } }

### `Panel.floor(floor, opts)`

A menu of the areas on a floor (expose(..., { area = "Salon", floor = "Parter" })), each a Panel.area; areas with nothing for the panel are left out. Filled each time it opens. name), titles = { [area] = title }, kinds, skip }

- `floor` (string) — Floor name
- `opts` (table) — |nil { title, order = { area, ... } (first, in this order; the rest by

### `Panel.confirm(text, fn, heading)`

Asks before running fn(panel): OK runs it and closes, back closes.

### `Panel.toggle(obj, name)`

One on/off object on its own page; OK switches.

### `Panel.value(name, src, format)`

One value on its own page.

### `Panel.chart(name, src, opts) → table|nil The page; nil when src has no history (so a menu row gets no OK)`

Hourly bars of a value's history: up and down pick an hour, its time and value show on the right. Each bar is the time-weighted mean of that hour; the last is the current one. returning changes newest first ({ value, atMs })

- `name` (string) — Title
- `src` (table) — |function An object with history (obj:history(n)) or a function
- `opts` (table) — { hours = 12, format = "%.1f" }

**Returns:** table|nil The page; nil when src has no history (so a menu row gets no OK)

### `Panel.thermostat(o)`

Current and set temperature; +/- change the set point, written 1 s after the last press. Without set (or while canSet() is false) the page only shows them.

- `o` (table) — { name, current = obj|fn, get = fn, set = fn|nil, canSet = fn|nil, step = 0.5, min, max, format = "%.1f" }

### `Panel.history(name, src, opts) → table|nil The page; nil when src has no history`

The last changes of an object, newest first, as a list: when and what.

- `name` (string) — Title
- `src` (table) — |function Object with :history(n), or a function returning { {value, atMs}, ... }
- `opts` (table) — |nil { count = 10, format = "%s" | fn(value) | { [value] = text } }

**Returns:** table|nil The page; nil when src has no history

### `Panel.corners(opts)`

A page where each corner is one button, e.g. home with the room's lights; no back. draw = fn(screen, panel), every, watch } A corner with a device does what its row would (a light or dimmer switches, a cover opens or closes), with a function calls fn(panel); a corner with only a label shows it and leaves its LED off; a path not in the registry keeps the label only. clock draws the time in the middle (refreshed every minute), draw anything else there.

- `opts` (table) — { tl = { "Swiatlo", obj|path|fn }, tr, bl, br, clock = true,

### `Panel.idle(rows)`

The idle screen: time, date and a few values; any button opens home.

- `rows` (table) — { { "Salon", obj|fn, "%.1f" }, ... }

### `Panel.cleaning(seconds)`

Locks the panel for cleaning: a countdown, inputs ignored, buzzer and LEDs off.

---

## objects.RemotePanel

### `RemotePanel:addButton(index, button)`

Attaches a PANEL_BUTTON of the same module (the OM import does this).

- `index` (number) — Button index on the module (0-15; 0-3 are page 1)
- `button` (RemotePanelButton)

### `RemotePanel:getButton(index) → RemotePanelButton|nil`

Gets an attached button.

- `index` (number) — Button index on the module

**Returns:** RemotePanelButton|nil

### `RemotePanel:displayOn()`

Switches the display on.

### `RemotePanel:beep(freq, ms, volume)`

Beeps.

- `freq` (number) — Frequency 30-5700 Hz (default 2000)
- `ms` (number) — Duration 1-2000 ms (default 100)
- `volume` (number) — Volume 0-16 (default 8)

### `RemotePanel:setPage(nr)`

Shows one of Grenton's built-in pages (1-4).

### `RemotePanel.click(name, input)`

Entry point for the CLU: every panel event in Object Manager calls click("<panel name>", "<input>"). Never raises into the CLU's request.

- `name` (string) — Panel object name or id
- `input` (string) — Input (btn1..btn4, btn1:long, swipe:up, proximity, wake, ...)

---

## GPIO_DOUT

### `GPIO_DOUT:new(name, pin, opts) → GPIO_DOUT instance`

Create a new GPIO_DOUT object

- `name` (string) — Global name (e.g., "RELAY1")
- `pin` (number) — BCM pin number
- `opts` (table) — Optional: {activeLow = bool}

**Returns:** GPIO_DOUT instance

---

## GPIO_DIN

### `GPIO_DIN:new(name, pin, pull) → GPIO_DIN instance`

Create a new GPIO_DIN object

- `name` (string) — Global name (e.g., "BTN1")
- `pin` (number) — BCM pin number
- `pull` (string) — "up", "down", or "off" (default: "up")

**Returns:** GPIO_DIN instance

---

## integrations.mqtt.MqttClient

### `MQTT:new(id)`

Create new MQTT instance (an id already in use destroys the old instance, with a warning).

### `MQTT:getId()`

Get instance ID.

### `MQTT:setUrl(url) → self for chaining`

Set broker URL

- `url` (string) — Broker URL (e.g., "tcp://192.168.1.100" or "ssl://broker.example.com")

**Returns:** self for chaining

### `MQTT:setHost(host) → self for chaining`

Set broker host (alias for setUrl with tcp:// prefix)

- `host` (string) — Broker host (e.g., "192.168.1.100" or "broker.example.com")

**Returns:** self for chaining

### `MQTT:setPort(port) → self for chaining`

Set broker port

- `port` (number) — Broker port (default 1883, or 8883 for SSL)

**Returns:** self for chaining

### `MQTT:setClientId(clientId) → self for chaining`

Set client ID

- `clientId` (string) — Unique client identifier

**Returns:** self for chaining

### `MQTT:setCredentials(username, password) → self for chaining`

Set authentication credentials

- `username` (string) — Username
- `password` (string) — Password

**Returns:** self for chaining

### `MQTT:setTopicPrefix(prefix) → self for chaining`

Set topic prefix for all publish/subscribe operations

- `prefix` (string) — Prefix (e.g., "grenton", "home/grenton")

**Returns:** self for chaining

### `MQTT:setQoS(qos) → self for chaining`

Set default QoS level

- `qos` (number) — QoS level (0, 1, or 2)

**Returns:** self for chaining

### `MQTT:setKeepAlive(seconds) → self for chaining`

Set keep alive interval

- `seconds` (number) — Keep alive interval in seconds

**Returns:** self for chaining

### `MQTT:setAutoReconnect(enabled, intervalMs) → self for chaining`

Enable/disable auto reconnect

- `enabled` (boolean) — Auto reconnect flag
- `intervalMs` (number) — Reconnect interval in milliseconds (optional)

**Returns:** self for chaining

### `MQTT:isConnected() → boolean True if connected`

Get connection status (synced with Go backend)

**Returns:** boolean True if connected

### `MQTT:getUrl() → string Full broker URL`

Get broker URL (full with port)

**Returns:** string Full broker URL

### `MQTT:getTopicPrefix() → string Topic prefix`

Get topic prefix

**Returns:** string Topic prefix

### `MQTT:connect() → boolean Success`

Connect to broker

**Returns:** boolean Success

### `MQTT:disconnect(removeInstance) → boolean Success`

Disconnect from broker

- `removeInstance` (boolean) — Remove from instance registry (default: true)

**Returns:** boolean Success

### `MQTT:destroy() → boolean Success`

Destroy client: unsubscribe on the broker, disconnect, drop subscriptions, handlers and the registry entry

**Returns:** boolean Success

### `MQTT:publish(topic, payload, qos, retain) → boolean Success`

Publish message to topic

- `topic` (string) — Topic (prefix will be added if set)
- `payload` (string) — |table Message payload (table will be JSON encoded)
- `qos` (number) — QoS level (optional, uses default)
- `retain` (boolean) — Retain flag (optional, uses default)

**Returns:** boolean Success

### `MQTT:subscribe(topic, callback) → boolean Success`

Subscribe to topic

- `topic` (string) — Topic pattern (prefix will be added if set)
- `callback` (function) — Callback function(topic, payload, qos, retain)

**Returns:** boolean Success

### `MQTT:unsubscribe(topic) → boolean Success`

Unsubscribe from topic

- `topic` (string) — Topic pattern

**Returns:** boolean Success

### `MQTT:onConnect(callback) → self for chaining`

Register OnConnect handler

- `callback` (function) — Callback function()

**Returns:** self for chaining

### `MQTT:onDisconnect(callback) → self for chaining`

Register OnDisconnect handler

- `callback` (function) — Callback function()

**Returns:** self for chaining

### `MQTT:onMessage(callback) → self for chaining`

Register OnMessage handler (global, for all subscriptions)

- `callback` (function) — Callback function(topic, payload, qos, retain)

**Returns:** self for chaining

### `MQTT:onError(callback) → self for chaining`

Register OnError handler

- `callback` (function) — Callback function(error)

**Returns:** self for chaining

### `MQTT:loadDiscoveryMessages(messages) → self`

Load discovery messages generated by Go (omparser.GenerateHADiscovery)

- `messages` (table) — Array of {topic, payload, retain} from Go

**Returns:** self

### `MQTT:addDiscoveryMessage(topic, payload) → self`

Add single discovery message

- `topic` (string) — Full topic (e.g., "homeassistant/switch/.../config")
- `payload` (string) — |table JSON payload

**Returns:** self

### `MQTT:configureDiscovery(config) → self`

Configure discovery settings

- `config` (table) — {autoPublish}

**Returns:** self

### `MQTT:publishAllDiscovery() → number Number of messages published`

Publish all loaded discovery messages

**Returns:** number Number of messages published

### `MQTT:removeAllDiscovery() → number Number of messages published`

Remove all discovery (publish empty payloads)

**Returns:** number Number of messages published

### `MQTT:publishState(topic, state) → boolean Success`

Publish state update for object

- `topic` (string) — State topic (from discovery message)
- `state` (any) — State value

**Returns:** boolean Success

---

