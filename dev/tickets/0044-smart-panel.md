# #0044: vCLU drives a Grenton Smart Panel: buttons and gestures in, screens out

- **Type:** feature
- **Area:** grenton | lua-runtime
- **Size:** L
- **Epic:** —
- **Blocked by:** —
- **Skill:** `.agents/skills/write-contract-test`
- **Needs hardware:** Grenton Smart Panel (SMART_PANEL_FM_4) on a CLU, Object Manager

> State (who, status) lives in the GitHub issue, not here. To take the ticket, ask in the issue;
> a maintainer assigns it. This file is the specification: change it by PR.

## Goal
A Smart Panel on a real CLU is run entirely by vCLU. Object Manager only forwards the panel's
events to vCLU, and a Lua module describes what the panel shows (a menu, pages, a thermostat,
light switches, a temperature view) with a fluent API that new page types can extend.

## Context

### The device
Object Manager defines the module as `module_SMART_PANEL_FM_4_fv03..fv07` (`device-interfaces` folder
of an Object Manager install; these files belong to Grenton, so do not copy them: write down only
the indices used, in your own words). Objects of fv07:

| Object | Class | Count | Used here |
|---|---|---|---|
| PANEL | 29 | 1 | the screen: methods and gesture/display events |
| PANEL_BUTTON | 30 | 16 | 4 physical buttons × 4 built-in pages; events, label, LEDs |
| PANEL_PAGE | 19 | 4 | built-in pages; vCLU does not use them for drawing |
| PANELSENSTEMP | 20 | 1 | temperature, feature 0 Value (°C) |
| PANELSENSLIGHT | 21 | 1 | light, feature 0 Value (%) |

PANEL `execute` methods used for drawing: 0 SwitchOnDisplay, 2 ClearScreen, 3 PrintText(x, y, text,
font 1-3), 20 PrintFloat(x, y, number, precision 0-2, font), 4 DrawLine(x, y, xe, ye, color 0-1),
30 DrawBox(x, y, w, h, color), 5 DrawPoint(x, y, color), 6 DrawIcon(x, y, file), 7 DisplayContent,
21 SetBeep(freq, ms, volume, 0), 22 SetPageNr(1-4). Screen 128 × 64, monochrome; text up to 15
characters, icon file name up to 9 characters (a `.bmp` already on the panel). PANEL events: 0-3
OnGestureUp/Down/Left/Right, 4 OnProximityDetect, 5 OnPageChange, 6 OnDisplayOn, 7 OnDisplayOff.
PANEL_BUTTON events: 3 OnShortPress, 4 OnLongPress, 5 OnHold, 6 OnClick; execute 3 SetLabel, 1
LedSwitchOn, 2 LedSwitchOff. Check the indices against the firmware of the panel you test with.

### What exists
- **CLU → vCLU.** A CLU that calls a function on vCLU sends the call as a Lua script, and vCLU runs it
  as is (`pkg/server/handle_lua_script_request.go`). A global Lua function on vCLU receives its
  string arguments as strings. Test reference: `synchronize()` in `pkg/lua/testdata/e2e/clu/user.lua`.
- **vCLU → CLU.** `RemoteObject:execute` (`pkg/lua/runtime/objects/RemoteObject.lua`) builds
  `obj:execute(i, a, b)` with `tostring` and no quotes, so a string argument (`PrintText`'s text)
  reaches the CLU as a bare name, i.e. nil. `RemoteObject:set` quotes strings but does not escape
  `"`, `\` or newlines. Every call is its own UDP message, sent immediately.
- **The CLU drops bursts** (`pkg/lua/AGENTS.md`, "The CLU drops bursts of UDP messages without a
  word"). One redraw is ClearScreen, several draw calls and DisplayContent; sent as separate
  messages it is exactly the burst that gets lost.
- **OM import.** `pkg/omparser` parses every class but generates objects only for the classes mapped
  in `generator.go` (and registers only those `IsExposable` allows). Classes 19, 20, 21, 29 and 30
  are dropped today, so the panel never reaches `om.lua`.
- **Fluent style to follow:** `Dashboard.new(id):name(...):section(...):toggle(...)`
  (`pkg/lua/runtime/core/Dashboard.lua`).
- An earlier menu library for the panel ran on the CLU itself (menu tree, views with `draw` lists,
  OK/back/down buttons, a timer tick for animations). It was partly a proof that the approach works
  on the panel; take the idea, not the code: here the logic runs on vCLU and the CLU only forwards
  events and draws.

## Design

### 1. Events in: one entry point
In Object Manager, every panel event the user wants calls one function on vCLU with the name of the
panel object and what happened, e.g. `click("PANEL_SALON", "btn1")` for OnClick of button 1, and the
same function for gestures: `click("PANEL_SALON", "swipe:up")`. Inputs: `btn1`..`btn4`,
`swipe:up`, `swipe:down`, `swipe:left`, `swipe:right`, `proximity`, `wake` (OnDisplayOn); long
press and hold as `btn1:long`, `btn1:hold`. The name is the
object name from the OM import, so vCLU finds the panel object without extra configuration.

- Find and document the exact Object Manager syntax for a CLU event to call a function on vCLU
  (with a screenshot), on the docs page and in the module's docs.
- An unknown panel name or input is logged once and ignored; it never raises in the CLU's request.
- The global `click` is a default: Object Manager uploads its own scripts to vCLU, and a vCLU
  script called `click` replaces it. The same entry is `RemotePanel.click(panel, input)`, which such
  a script calls.
- A Smart Panel WiFi is its own CLU (`GATE:new` in om.lua) with the same panel module; its import
  and events work the same way, with the panel's own OM file.
- The handler returns at once: drawing goes out asynchronously, never inside the CLU's request.
- Checked on a Smart Panel WiFi (2026-10-18, Gate firmware 1.0.2, panel module fv05): OnClick
  does not fire after a long press, so OnClick and OnLongPress are enough (OnShortPress is hard to
  aim). Object Manager passed the button's id as the first argument, so `click()` accepts a
  panel or one of its buttons. The Gate sends the call as broadcast and unicast in turn; every
  press arrived. Still open: whether the 4 button objects of page 1 are enough when vCLU never
  changes page; whether gestures switch Grenton's built-in pages (PageControlMode) and must be
  turned off when the panel starts.

### 2. Screen out: short messages, and a lost one is repaired
The CLU handles long messages badly and loses messages without a word. Design for both:
- A frame is a list of draw calls ending with DisplayContent. Pack several calls into one script
  (`p:execute(2) p:execute(3, 0, 0, "Salon", 1) …`) but keep each message under a size limit found
  on the CLU and kept conservative (record it in `pkg/lua/AGENTS.md`). Parts go out spaced
  30-100 ms on the EventBus (non-blocking), as the subscription code does.
- Measured 2026-10-18 on the WiFi panel: the CLU runs one expression per message (two statements
  side by side run nothing), so every message is wrapped in `(function() ... end)()`; 337
  characters went through, the limit is kept at 300. `DrawBox` draws nothing and `PrintText`
  paints a black cell behind every letter, so a frame always redraws the whole screen; there are
  no partial redraws. Fonts: 10, 14 and 28 px high, y is the top.
- A frame is idempotent (it starts from ClearScreen), so a lost part is
  repaired by sending the frame again. The last part (with DisplayContent) is sent with a reply;
  without a reply in time the whole frame is resent, a bounded number of times, then logged.
- Redraws coalesce: if a new frame for the same panel is ready before the last one went out, only
  the newest is sent.
- Small changes do not need a full frame: a moved menu cursor or a changed value redraws only
  its rows (DrawBox in background colour over the row, then the text). Fewer and shorter messages.
- Fix string arguments in `RemoteObject:execute` and `:set` with a Lua string-literal encoder that
  quotes and escapes (`"`, `\`, newline, control characters). Contract test for both. This is a core
  fix and also helps every other remote method that takes text.

### 3. OM import: panel objects
- `pkg/omparser`: classes 29, 30, 19, 20, 21 generate objects in `om.lua`. New Lua classes in
  `pkg/lua/runtime/objects/`: `RemotePanel` (the drawing methods above, `beep`, `displayOn`,
  `setPage`), `RemotePanelButton` (`setLabel`, `ledOn`, `ledOff`). The temperature and light sensors
  reuse `RemoteThermometer` / `RemoteValueSensor` if their features match.
- Panel and button objects are not published to Home Assistant or HomeKit. The temperature and
  light sensors may be, the same way as other sensors.

### 4. The panel library (Lua, outside the core)
A library loaded with `require`, not part of the runtime core (`pkg/lua/AGENTS.md`, "The Lua API is
a contract, keep it small"). Its shape, as a starting point to refine in the PR:

```lua
local Panel = require("lib/panel")   -- location to be decided, see "Contract impact"

Panel.new(PANEL_SALON)
  :buttons{ tl = "btn1", tr = "btn2", bl = "btn3", br = "btn4" }   -- which button is in which corner
  :back("br")                                    -- the same corner is back on every screen
  :home(Panel.menu("Salon")
    :item("Światło", Panel.toggle(LAMPA_SALON))
    :item("Temperatura", Panel.value("Temp", TERMOMETR, "%.1f°C"))
    :item("Ogrzewanie", Panel.thermostat(TERMOSTAT, { step = 0.5 }))
    :item("Sypialnia", Panel.menu("Sypialnia")
      :item("Lampka", Panel.toggle(LAMPKA))))
  :onGesture("swipe:right", function(p) p:open("ogrzewanie") end)   -- gestures: the user's choice
  :sleepAfter(30)                      -- after 30 s without input: the idle screen, or home if none
```

- **The GUI follows the panel's shape.** The panel is a square with four square buttons, one in
  each corner, around a small display in the middle. There are no arrow keys: a page gives each
  corner a meaning (`keys = { tl = {"▲", fn}, tr = {"OK", fn}, bl = {"▼", fn}, br = {"◀", fn} }`),
  and the library draws a short hint for each in the matching corner of the display, so what a
  button does is always shown next to it (edit mode: `+`, `✓`, `-`, `✕`). The content gets the
  area between the hints: plan for about three text rows, short labels, one value per row, and
  check on the panel what fits. The button LEDs (`ledOn`/`ledOff`) mark which corners do
  anything on the current page. Find out on the panel which button object is in which corner,
  and whether the button's own Label/Icon features show anywhere when vCLU draws the screen.
- **One corner is always back.** On every page the library ships, the same corner (default `br`,
  set once per panel with `:back("br")`) is back/exit: it closes the page, cancels an edit (`✕`)
  or a confirmation, and on the home page goes to the idle screen. Pages use the other three
  corners; a page registered by a user gets the back corner from the library too, so all screens
  behave alike.
- **Gestures are left to the user.** Swipes are unreliable on the panel, so no built-in page needs
  them. They are forwarded like the other inputs and a user can bind them with
  `:onGesture(name, fn)` on the panel or handle them in their own page.
- **A page is a table with `draw(screen, panel)`, `keys` and `input(name, panel)`.** Pages name
  the three free corners by role, `up`, `down` and `ok`, and the library places the roles from
  the back corner (the other column is up/down, the rest is ok), so a page works whichever
  corner is back. `input` gets the role (with `:long`/`:hold`). `screen` collects draw
  calls (`screen:text(x, y, s, font):line(...):rule(y):icon(...)`); the library turns it into one
  frame. `Panel.register("name", factory)` adds a page type, so a user module or a later PR adds
  pages (roller, scene list, alarm status) without changing the library.
- **First page types:** `menu` (list with cursor and scrolling, submenus, back), `toggle` (any object
  with `toggle`/`switch`, shows on/off), `value` (one value with a label and format), `thermostat`
  (current and set temperature, +/- on two buttons).
- **A menu changes things in place.** A menu row shows its current value on the right and can be
  edited without leaving the menu: `:toggle(label, obj)` (OK switches), `:number(label, get, set,
  {step, min, max, format})` and `:choice(label, get, set, {options})` (OK enters edit mode, the
  corner keys turn into `+`/`-`, OK sets it, `✕` cancels), `:action(label, fn, {confirm = "..."})`.
  `min`/`max` limit what can be chosen, so a menu row is enough to keep a device in a safe range.
  A value is set once on OK, not on every step. Rows refresh when their objects change.
- **Redraw on change, not on a timer:** a page subscribes to the objects it shows (`onChange`,
  unsubscribed when the page closes) and redraws when they change. No periodic tick is required;
  animations are out of scope.
- **Helpers for pages:** `every = N` on a page redraws it every N seconds, for values without
  `onChange` (plugins that poll over HTTP). `panel:debounce(key, seconds, fn)` runs `fn` once after
  the last call: the value on screen changes at once, the device gets one write (three quick `+`
  are one HTTP request, not three). `panel:notify(text, {beep, seconds})` shows a message over the
  current page (e.g. from an automation), OK or the timeout closes it, and the page comes back.
- **The state lives in the library, on vCLU.** Each panel has its own: open pages (stack), cursor
  and scroll, a value being edited, idle timer, last frame sent. The panel and the CLU keep nothing
  but the picture. It is not persisted: after vCLU starts or reloads, each panel draws its home page.
  The picture can also be lost on the panel side (CLU restart, display back on), so the panel is
  redrawn on OnDisplayOn and when the CLU's session comes back; forward OnDisplayOn as `wake`.
- **Proximity is an input like the others.** `proximity` wakes the panel (redraws the current page,
  resets the idle timer) and calls `:onProximity(fn)` if set, so a user module can use it. Measure
  on the panel how far it reacts and how often the event repeats while someone stands in front of
  it (ProximitySens, ProximityTimeout), and record it: this decides whether it can later serve as
  a presence or motion sensor.
- **An idle screen is built in.** After `sleepAfter` the panel shows `Panel.idle{...}` instead of
  switching off: time and date (vCLU's clock) and a few values, each a label, any object with
  `get()`/`getValue()` (a sensor, a plugin sensor such as a weather plugin's, a VirtualText) and a
  format; optionally an icon chosen by a function of the value (`.bmp` files already on the panel;
  without them, text). Any button opens the home page. The library does not fetch
  weather itself: it shows what an object provides. It redraws once a minute on the minute (only
  the time row) and when a shown value changes.
- **Cleaning mode is built in.** The glass gets dirty, so `Panel.cleaning(seconds)` (default 30) is a
  ready page for any menu, e.g. `:action("Czyszczenie", Panel.cleaning(30))`. While it runs, the
  panel shows a countdown and vCLU ignores every input except `wake`; the buzzer and button LEDs
  are off; at 0 it restores them and returns to the page it came from. The countdown redraws only
  the number, once a second, so a lost message is fixed by the next one. Firmware fv07 has its own
  method (PANEL execute 31 StartCleaningMode, 10-255 s), but it also blocks screen refresh, so it
  cannot show a countdown and older firmware lacks it: vCLU does it itself.
- **Text fits the screen:** the library cuts text to 15 characters and keeps Polish characters
  consistent with what the panel's fonts can print (check on the panel which characters render;
  replace the rest, e.g. `ł` → `l`, in one place).

## Files
- `pkg/omparser/types.go`, `generator.go`, `generator_registry.go` and their tests
- `pkg/lua/runtime/objects/RemoteObject.lua` (string arguments), `RemotePanel.lua`, `RemotePanelButton.lua` (new), `objects/init.lua`, `pkg/lua/embed.go`, `bootstrap_go.lua`
- the panel library (new; location per "Contract impact") and its tests
- the frame sender (single-message frames, splitting, coalescing) near `CLU_REMOTE` / `runtime/grenton/protocol`
- `pkg/lua/AGENTS.md` (panel indices with their source, frame size limit), docs page for the panel (docs.vclu.pl)

## Acceptance criteria
- [ ] An OM import with a Smart Panel generates `RemotePanel` and `RemotePanelButton` objects (and the two sensors) in `om.lua`; Go tests cover the generated lines.
- [ ] `RemoteObject:execute` and `:set` send strings as quoted, escaped Lua literals; a Lua contract test covers quotes, backslash and newline.
- [ ] An OM import of a Smart Panel WiFi generates the same objects under the panel's CLU; a Go test covers it.
- [ ] `click(panel, input)` reaches the panel's current page; unknown panel or input is logged and ignored; tests drive inputs and check the frames sent.
- [ ] No message exceeds the size limit; parts are spaced on the EventBus; quick redraws coalesce to the newest frame; a frame whose last part gets no reply is resent (bounded). Tests check this on a virtual clock, including a dropped part.
- [ ] Menu rows `toggle`, `number`, `choice` and `action` edit in place (OK, `+`/`-`, OK sets once, `✕` cancels, `min`/`max` hold); tests cover each and the frame after every step.
- [ ] Every page shows a hint in each corner whose button does something, and the button LEDs match; changing page or mode updates both. Tested; the PR has a photo of the menu and of edit mode on the panel.
- [ ] The back corner closes every built-in page, cancels edits and confirmations, and leads from home to idle; a user-registered page gets it without code; no built-in page reacts to gestures. Tested.
- [ ] `every`, `panel:debounce` and `panel:notify` work as described; tested on a virtual clock.
- [ ] `menu`, `toggle`, `value` and `thermostat` pages work and are covered by contract tests (input → frame, object change → redraw, closing a page unsubscribes).
- [ ] Two panels keep separate state; after a reload each draws its home page; `wake` and a CLU session coming back redraw the current page. Tests cover all three.
- [ ] A page type registered from a user module (`Panel.register`) works without changes to the library; a test registers one.
- [ ] On a real panel: menu navigation, a light switched from the panel, temperature view and thermostat set point all work; the PR says which firmware, with a photo or video.
- [ ] `Panel.idle` shows after `sleepAfter`, updates the time row on the minute and values on change, and any input opens home; tested on a virtual clock.
- [ ] `Panel.cleaning(n)` counts down from n, ignores inputs, restores buzzer, LEDs and the previous page at 0; tested on a virtual clock and checked on the panel (touching the glass during it does nothing).
- [ ] `proximity` wakes the panel and reaches `:onProximity`; the PR reports the measured range and repeat rate.
- [ ] The Object Manager syntax for forwarding events is documented with a screenshot; indices and the frame size limit are recorded in `pkg/lua/AGENTS.md` with their source.
- [ ] `go test ./...` and `go test -race ./...` pass.
- [ ] CHANGELOG entry in "Unreleased".

## Not in scope
- Grenton's built-in pages (PANEL_PAGE types, DistributedLogic): vCLU draws on its own.
- Emulating a Smart Panel as a vCLU module for Object Manager.
- Animations and free drawing; uploading icons to the panel.
- Publishing proximity as a presence or motion sensor in Home Assistant or HomeKit: a later ticket,
  once the measured range says it is useful.
- The `PANEL2` / `PANEL_PAGE` entries in `runtime/grenton/factories/ObjectFactory.lua` (vCLU's own `om.lua`), which point to classes that do not exist.

## Contract impact
- New public Lua API: `click(panel, input)` as a global called by CLUs, `RemotePanel` /
  `RemotePanelButton`, and the library's `Panel.*` builder and page contract (`draw`/`input`,
  `Panel.register`). Where shipped Lua libraries live and how they are required:
  `dev/decisions/0002-runtime-libraries.md` (`pkg/lua/runtime/lib/`, `require("lib/panel")`).
- For agents writing modules: the library is in `docs/ai/lua-api-reference.md` (generated) and
  `docs/ai/examples.md`, `vclu_runtime_schema` has a `smartPanel` entry, and `vclu_draft_test`
  takes `{"click": panel, "input": "btn1"}` steps, so the frames show in `clu_calls`.
- Changed behaviour: `RemoteObject:execute` now quotes string arguments (previously sent as bare
  names). The only caller that relied on it, `RemoteRGBLight:setRGB`, quoted the colour itself and
  now passes it plain.
