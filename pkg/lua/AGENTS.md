# pkg/lua: Lua runtime

The VM is gopher-lua. Runtime files live in `runtime/`; Go glue and tests in this directory.

## gopher-lua is Lua 5.1, with its own quirks
No `string.pack`, no `bit32`, no `//`. `math.huge` is the largest float, not infinity
(`math.huge * 2 == math.huge` is false). A number is finite when `v - v == 0`; NaN alone is
`v ~= v`. Never compare with `math.huge`. See `runtime/utils/JSON.lua`.

## A caught error can split a closure's shared variable
In gopher-lua, when `pcall` catches an error, the running function's open upvalues are closed too
early: closures created before and after no longer share the variable.
`local n=0; local f=function() n=n+1 end; pcall(function() error("x") end); f(); return n` gives 0.
Keep shared mutable state in a table (`c.n = c.n + 1`), in tests and in modules.

## `_` is the registry, not a throwaway name
`for _, v in ipairs(t)` or a parameter named `_` hides the global registry in that scope:
`_:get(...)` inside it fails with `attempt to index a non-table object`. The plugin sandbox's `_`
proxy had `__index = function(_, key) ... _[key]` and recursed into itself until #82. Name loop
indexes `i` and parameters `self` in code that uses the registry.

## Load order in the bootstrap matters
`runtime/bootstrap_go.lua` loads files in a fixed order; later files use globals set by earlier
ones. A wrong place shows as `attempt to index a nil value` at startup. After adding or moving a
runtime file, start vCLU and read the log, not only the tests.

## State changes go through StateBus
`runtime/utils/StateBus.lua` gives every change its `origin` and `actor` and delivers it to
history, HomeKit, Home Assistant and MQTT. Never publish state to an integration directly; change
the object. Contract: `state_change_test.go`, `state_context_test.go`.

## The Lua API is a contract, keep it small
User scripts and agents depend on every public function in `runtime/`. Build a feature from
existing functions first. Mechanisms belong in the core (watch, persist, history); ready-made
policies belong in libraries. Changing a signature needs a migration and a `Contract change:` note.

## Test harnesses
`newPersistEngine` (`persist_test.go`), `newHistoryEngine` with a step clock (`history_test.go`),
an `httptest` stand-in for httpbin (`fake_httpbin_test.go`). Reuse them.

## runtime/grenton: talking to Grenton CLUs

### The CLU drops bursts of UDP messages without a word
A CLU drops packets when it gets several `clientRegister` calls back to back; roughly the first
4-5 of a burst arrive. A subscription whose renewal was lost expires after its TTL (60 s) and the
CLU stops reporting; nothing tells vCLU. Space every series of messages to a CLU by 30-100 ms
(`batchDelay` in `runtime/grenton/protocol/RemoteSubscription.lua`) without blocking the run loop. A dead
subscription shows as no `[CLIENT-REPORT]` from that session in the log.

### One clientRegister carries many entries
Object Manager registers 28 entries in one `clientRegister` and the CLU answers with all 28 values
(capture in [opengr8on `docs/COMMUNICATION.md`](https://github.com/psobiech/opengr8on/blob/c9c408734493fbb44068ab0a9b026be890782a48/docs/COMMUNICATION.md#L112-L117)),
so `RemoteSubscription.MAX_BATCH_SIZE` is 28. Measured on 2026-10-23: 4 CLUs with 4, 271, 282 and 176
objects at batch size 28 (29 sessions, 11 on the largest CLU) kept every object updating for over
4 hours. No limit on entries or sessions per CLU is known:
[opengr8on](https://github.com/psobiech/opengr8on) and [opengr8on-node](https://github.com/psobiech/opengr8on-node)
only emulate the CLU side and read real CLUs by polling, so they say nothing about it. Fewer,
larger sessions mean fewer renewals to space.

### A lost report is corrected by the renewal, not by a push
A change is reported with one UDP packet and no retry. The CLU answers every renewal
(`clientRegister`, every TTL/2) with the session's current values; `SendCommandNoReply` reads that
answer and `RemoteSubscription.applyRenewAnswer` applies it, except to objects published after the
renewal was sent (the answer is older). Keep renewals going through `refresh`, or a lost report
stays wrong until another object in the session changes. Contract: `pkg/server/renew_reply_test.go`.

### Values and scales
A dimmer's value is 0..1, a digital output is 1/0. HomeKit and Home Assistant count brightness
0..100: convert at the integration edge and test both directions. A cover's position is
0 = closed .. 100 = open everywhere: Grenton, vCLU, Home Assistant and HomeKit.

### Feature and method indexes come from Object Manager
Object Manager ships the definition of every Grenton object as XML: on macOS in
`Object Manager.app/Contents/Eclipse/configuration/com.grenton.om/device-interfaces/`
(`module_*.xml`). Each lists the features (index, get/set, range), methods (`execute` or `set`,
index, parameters) and events of an object class. Take indexes and scales from there, not from
memory; when the English and Polish hints disagree, the feature's own description decides.
`ROLLER_SHUTTER` (class 24): feature 0 State, 7 Position, 8 LamelPosition; methods 0 MoveUp,
1 MoveDown, 3 Stop, 9 SetLamelPosition, 10 SetPosition; feature 13 ReversePosition flips the
scale in the installation. The first firmware (fv01) has no Position, and om.lua does not say
which firmware a module runs, so the OM import only marks class 24 with `_hasPosition = true`.
`ROLLER_SH` (class 11) has no position.
`DIMMER` (class 5): feature 0 Value (0..1, settable), 1 RampTime; execute 0 Hold, 1 Switch,
2 SwitchOn, 3 SwitchOff. It has no SetValue with a ramp.
`LEDRGB` (class 9, LED RGB and LED RGBW 2.0) numbers its methods otherwise: Value is read-only,
execute 0 SetValue, 1 SetHue, 2 SetSaturation, 6 SetRGBvalue, 7 HoldValue, 9 SwitchOn,
10 SwitchOff, 11 Switch, 12 SetWhiteValue (feature 15 WhiteValue, RGBW only). An RGBW module
is told apart in om.lua by its module type (third argument of the module's `OBJECT:new`): 0x18
LED RGBW DIN, 0x1d LED RGBW FM, 0x04 the old LED RGB. `LED_CHANNEL` (class 49, four per RGBW
module): feature 0 Value 0..255, read-only; execute 0 SetValue(0..255, ramp), 1 SwitchOn,
2 SwitchOff, 3 Switch, 4 HoldValue. `RemoteLedChannel` converts to 0..1 in `fromFeature`. A subclass
inherits its parent's indexes: check them (`RemoteLight.METHODS`). Feature 6 RGB is reported
as a quoted string (`"#7c0000"`) and is the colour the module outputs, scaled by Value: a red at
0.49 is `#7c0000`, off is `#000000` (LED RGBW DIN fv01, 2026-10-19). `RemoteRGBLight` subscribes
to the value before the colour and divides by it. The other way too: SetRGBvalue makes the
brightest channel / 255 the value (`#00005a` gave 0.35, `#0000ff` 1), so `setColor` sends the
colour scaled by the brightness; SwitchOn goes to full brightness, the colour stays. `RemoteRGBLight` subscribes
feature 6 RGB (and 15 on `_hasWhite`) next to its value and keeps them aside; Home Assistant
listens for `OnColorChange`.

### A class's traits live in one table
What a Grenton class is for an integration (a dimmer, a light, a cover) is declared once:
`runtime/utils/TypeMap.lua` for Lua, `MQTTType()` in `pkg/omparser/types.go` for Go. Code asks
the table (`TypeMap.getExposeType(t) == "dimmer"`); a new class that needs its name added to an
`if` or a `case` elsewhere is a design error.
A subscription reports one feature per entry: an object that needs more than its feature 0 lists
them in `subscribeFeatures()` and turns each into its value with `fromFeature()` (`RemoteCover`).
What such an object keeps aside from its value changes with an event listed in `CHANGE_EVENTS`
(`OnMovementChange`, `OnSettingsChange`), which `lib/panel` redraws on.
`Thermostat` (class 18, a CLU's own object `THE…`, `object_thermostat_v2.xml`): features
3 PointValue (set by hand, settable with set 3), 6 State (1 active), 8 Mode (0 PointValue,
1 holiday, 2 auto from the schedule, 3 heat-up), 10 Min, 11 Max, 12 TargetTemp (in effect,
with the mode), 14 CurrentTemp; execute 0 Start, 1 Stop. `RemoteThermostat` subscribes to
3, 6, 8, 12 and 14 (five subscription entries); its value is CurrentTemp. Thermostat v1
(CLU firmware 197 and 1f5, FwType 3) has no feature 14.
Which version a CLU runs and whether its thermostats are subscribed or polled is one table,
`RemoteThermostat.FIRMWARE`: rows by om.lua FwType/FwVersion (the OM import writes
`CLU…._omFirmware`) and by the firmware the CLU reports for `CLU…:get(17)` (`clu:firmware()`;
only an answer like `04.08.05` counts). It is an allowlist: a thermostat is subscribed only
when get(17) says 5.x and om.lua does not say FwType 4 (CLU v1.0); 4.x, no answer, another
major are polled with `get` every `pollInterval` s until the hardware test says which features
are safe. A new firmware rule is a row there, not an `if`.
`autoSubscribe` blocks on get(17) once per CLU in a Lua state (`_firmwareAsked`, within
`FIRMWARE_CHECK_SECONDS`); then the poll chain asks without blocking, and a CLU that turns
out safe gets only its polled objects subscribed (`subscribeUpgraded`). The poll and renew
chains wait for each other, so they never interleave on a CLU.
Hardware test on CLU220000592 (04.08.05), 2026-10-24 (#67): no reset from each feature in its own
fresh session, from one session with all five (or eight, batch 8), nor from a full `autoSubscribe`
with both thermostats subscribed and renewed for two minutes. The 2026-10-19 resets did not come
back, so their trigger is still unknown and 4.x stays polled.
Feature 0 is Source (the sensor binding), so a thermostat must never be exposed as a switch:
TypeMap makes `THERMOSTAT` a `climate`, and `ExposedHandle:setValue` on a climate calls
`setSetpoint`, never `set(0)`.

### A thermostat in Home Assistant (`climate`, #61)
An object is a thermostat when it has `getValue` (temperature), `getTarget` and `setSetpoint`;
`getState` and `CHANGE_EVENTS` are optional. Decisions, keep HomeKit (#62) in agreement:
- `exposeRegistry()` and `group:expose()` without a type leave climates out (`explicitOnly` in
  the capabilities): only `expose(th)` shows one, writable unless `readonly`. Exposing subscribes nothing new and sends nothing to the
  CLU (`TestHAClimateExposeSubscribesNothingNew`); subscribing a thermostat can reset an old CLU (#67).
- `hvac_mode` is shown, not set: `off` when `getState()` is 0, otherwise `cool` when the optional
  `getDirection()` says so (VirtualThermostat), else `heat` (ControlDirection, feature 7, is not subscribed). No mode command topic; Grenton's Mode (holiday, auto) is not a preset yet.
- The target shown is `getTarget()` as the CLU reports it, never the value just sent: in holiday
  or auto mode a set point does not change the target. A target from HA is clamped to `min`/`max`
  of the expose options (default 5..35).
- A renewal answer older than a set point can carry the old PointValue and TargetTemp
  (`applyRenewAnswer` skips only objects that published their value). HA keeps showing the
  reported target, so at worst the old one comes back until the next report or renewal (TTL/2).
Contract: `ha_exposed_climate_test.go`, `ha_climate_readonly_test.go`.

### `onChange` is not a reading
`onChange` fires on a change only, and a Remote object's first report (old value nil) fires
nothing. `VirtualThermostat` counts a change as a reading (`staleAfter` is time without one) and
picks the first report up on its tick; a value present when it is created (VirtualNumber's
default 0, a value kept over a reload) does not count. Contract: `virtual_thermostat_test.go`.
Before a Lua state goes away (reload, `RunLoop` stop, `Close` before the loop ran) Go calls
`VirtualThermostat.releaseAll()` (`releaseOutputs` in `history.go`), before MQTT and the other
managers close; a Grenton command opens its own UDP socket, so `Server.Stop` closing
`commandConn` first does not stop it. Contract: `virtual_thermostat_release_test.go`.

### Smart Panel (SMART_PANEL_FM_4)
Indices from the Object Manager definition, fv05 and fv07. PANEL (class 29) execute: 0 SwitchOnDisplay,
2 ClearScreen, 3 PrintText(x, y, text, font 1-3), 4 DrawLine, 5 DrawPoint, 6 DrawIcon(x, y, file),
7 DisplayContent, 20 PrintFloat, 21 SetBeep(freq, ms, volume, 0), 22 SetPageNr, 30 DrawBox (fv05 on),
31 StartCleaningMode (fv07 only); features 13 BuzzerValue, 20 PageControlMode; events
0-3 gestures, 4 OnProximityDetect, 6 OnDisplayOn. PANEL_BUTTON (class 30, 16 = 4 buttons × 4 pages)
execute 1 LedSwitchOn, 2 LedSwitchOff, 3 SetLabel; events 3 OnShortPress, 4 OnLongPress, 5 OnHold,
6 OnClick. Screen 128 × 64, text up to 15 characters. PANEL_PAGE (class 19) is Grenton's own page
and is not imported.

Measured on a Smart Panel WiFi (its own CLU, `GATE:new` in om.lua, Gate firmware 1.0.2, panel
module fv05) on 2026-10-18:
- The CLU runs **one expression per message**: two statements side by side run nothing. `RemotePanel`
  wraps every message in `(function() ... end)()`. Messages of 337 characters went through whole;
  `RemotePanel.MAX_MESSAGE` is 300. A frame of 14 DrawLine calls in two messages 150 ms apart arrived.
- `PrintText`: y is the top of the text; font 1 is 10 px high (capitals), font 2 14 px, font 3 28 px.
  Every letter gets a black cell behind it, so text does not invert and nothing draws on top of it.
  The fonts are proportional and keep leading spaces: a row of 128 px holds 16 digits in font 1 and
  13 in font 2. In font 1 most digits and capitals take 8 px; from a photo of a test screen: 1 and
  F 7, % 9, `.` `[` `]` 4, a space about 6 (`Panel.layout.narrow`, used to right-align values). Place a column by x (the menu cursor `>` is drawn on its own at
  x 0, the label at `Panel.layout.indent`), never by padding with spaces. Text past the right edge is
  cut, and a glyph at y 0 or at the bottom row loses a pixel or two.
- `DrawBox` (30) draws nothing, with (x, y, w, h, color) and with end coordinates alike; it is not in
  fv04 (the bus panels). `DrawLine` and `ClearScreen` work; a frame needs `DisplayContent` at the end.
- Buttons: OnClick does not fire after a long press, so OnClick and OnLongPress are enough.
  The OnClick configured in Object Manager tends to pass the button's id as the first argument of `click()`, so
  `RemotePanel.click` accepts a panel or one of its buttons. The Gate sends the call as broadcast and
  unicast in turn; every press arrived (4 presses in 1.5 s).
- `DrawIcon` (6) draws the panel's own `.bmp` files (64 × 32, arrows 16 × 16) by name without the
  extension.
- Not yet measured: gestures, OnHold, the proximity range, the width of font 3, which button sits in
  which corner.
A frame is several calls in one script, so keep each message under `RemotePanel.MAX_MESSAGE` and
spaced by `SPACING_MS`; the CLU drops bursts.

### Untested on hardware
`RemoteLight` (dimmer), `RemoteLedChannel`, the colour of `RemoteRGBLight` and `RemoteCover` (roller) in
`runtime/objects/` follow the documentation, not a real module. Say so in the PR when you change them; a hardware report is welcome.

## runtime/integrations: Home Assistant, MQTT, HTTP

### Read-only and hidden must hold on commands, not only in discovery
Discovery and command handling are separate paths. An object hidden from an integration must also
refuse a command sent to its topic. Check access where the command executes, with the path as the
object is registered, and add a test that sends the command. Contract: `pkg/lua/access_*_test.go`,
`pkg/lua/ha_*readonly_test.go`.

### A write through the generic path skips an object's own methods
`ExposedHandle:setValue` ends in `obj:set(0, v)` (`runtime/core/ExposedObjects.lua`). Objects whose real
action is a method (a dimmer's `setBrightness`, a cover's `setPosition`, Satel arming) need that
method called. Map the command to the method when the object has one.

### Scales
Home Assistant sends brightness 0..100 (`brightness_scale`); a dimmer's value is 0..1; an on/off
light has no brightness at all. See `runtime/integrations/homeassistant/Commands.lua`.
