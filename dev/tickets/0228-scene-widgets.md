# #228: Plugins draw their own dashboard widgets (`plugin:widget` and scenes)

- **Type:** feature
- **Area:** plugins, lua-runtime, web
- **Size:** L (in stages, one PR each)
- **Epic:** —
- **Blocked by:** —
- **Skill:** —
- **Needs hardware:** no

> State (who, status) lives in the GitHub issue, not here. To take the ticket, ask in the issue;
> a maintainer assigns it. This file is the specification: change it by PR.

## Goal
A plugin author defines a new dashboard widget in Lua alone (an aquarium, a heating diagram, a
switchboard), and every client of the dashboard definition draws it, including clients that render
the definition natively, without a new release of that client.

## Context
- Every widget type today has its own renderer in `web/static/js/dashboard.js`, and a native
  client has its own code per type; a type it does not know is a placeholder tile. A widget from a
  plugin would need a release of every client.
- Instead the definition gets **one** new type, `scene`: a description of a drawing made of a
  small, fixed set of shapes, behaviours and bindings. Clients implement that set once. A plugin
  only produces scenes, so a new widget is new data, never new client code.
- The Lua runs on vclu; a client may be far away on a weak network. Lua therefore never draws
  frame by frame: it describes the scene once, the client animates it, and only variables travel
  with the values (polling `/values` or the dashboard WebSocket).
- The scene is data, not code: nothing a plugin writes runs on the client.
- The mock-up that started this (an aquarium: tank proportions, water and light, sensors, plants,
  stones and fish swimming in shoals) is the first test of the vocabulary: if it can be described
  as a scene, most custom widgets can.

### The plugin's side (proposal)

```lua
local plugin = Plugin:new("aquarium", { name = "Aquarium", version = "1.0.0" })

plugin:widget("aquarium", {
    -- what the user passes; checked by the core, errors at the user's line
    options = {
        size  = { type = "numbers", count = 3, required = true },
        water = { type = "color", default = "#2f8f86" },
        temp  = { type = "value" },    -- a function or an object with :value()
        light = { type = "object" },
        fish  = { type = "list" },
    },

    -- builds the scene once per definition (Lua reload, period change), not per frame
    draw = function(s, o)
        local w, h = o.size[1], o.size[2]
        s:rect{ w = w, h = h, fill = Draw.linear(o.water, Draw.darken(o.water, .3)) }
        for _, f in ipairs(o.fish or {}) do
            for _ = 1, f.count do
                s:group{ behave = Draw.wander{ bounds = { 0, 0, w, h * .9 },
                                               speed = Draw.bind("temp", { 22, 30 }, { .5, 2 }) } }
                 :ellipse{ rx = 3, ry = 1, fill = f.color }
            end
        end
        s:rect{ w = w, h = h, fill = "#000", opacity = Draw.bind("light", { off = .6, on = 0 }) }
        s:tap{ action = "toggle_light" }
    end,

    -- the variables the scene binds to, read on every poll
    values = function(o)
        return { temp = o.temp:value(), light = o.light:value() == 1 }
    end,

    actions = {
        toggle_light = function(o) o.light:switch() end,
    },
})
```

### The user's side

```lua
d:widget("aquarium", "akwa", "Akwarium", {
    size  = { 100, 50, 40 },
    temp  = _:get("CLU1->T_AKWA"),
    light = _:get("CLU1->DOU_LAMPA"),
    fish  = { { color = "#3fa9f5", count = 8 } },
})
```

The names (`plugin:widget`, `d:widget`, `Draw`) are proposals; settle them in stage 1.

### The vocabulary (version 1, to be settled in stage 1)

- **Shapes:** `rect` (with radius), `ellipse`, `path` (SVG path data), `text` (one line),
  `group` (translate, scale, rotate, opacity, `z`).
- **Fills:** a colour, a linear or radial gradient.
- **Depth:** `z` from 0 (front) to 1 (back) scales a group and mixes its colours towards a
  background colour, so a scene can have perspective without 3D.
- **Behaviours** (the client animates them, seeded so two screens show the same):
  `sway` (plants), `wander` (random movement inside bounds), `school` (`wander` for a group that
  keeps together), `emit` (bubbles, flakes), `tween` (a smooth change when a bound value changes).
- **Bindings:** a property follows a variable through an explicit mapping (a range to a range, a
  boolean to two values, a range to a colour), never an expression language.
- **Taps:** a region runs a plugin action or opens a layer of ordinary widgets (as `room` does).

Anything a client does not know (a newer shape or behaviour) is skipped for that node, not the whole
tile. The scene carries its own version (`draw: 1`); a client that knows a lower one shows today's
placeholder tile.

## Stages
Each stage is its own PR against this ticket; the ticket stays open until the last.

1. **Scene contract and `Draw` in Lua.** The JSON of a scene, the version 1 vocabulary, limits,
   validation with errors at the module's line; a `scene` widget a dashboard can add directly
   (`section:scene(...)`); fixtures of scenes next to the dashboard definition tests. A decision
   record in `dev/decisions/` for the vocabulary and how it grows.
2. **Renderer in `dashboard.js`** (canvas): shapes, fills, depth, bindings, taps; paused off screen
   and in a background tab; `prefers-reduced-motion` slows or stops the behaviours.
3. **Behaviours** in `dashboard.js`: `sway`, `wander`, `school`, `emit`, `tween`.
4. **`plugin:widget`**: options checked by the core, `draw` / `values` / `actions`, the widget's
   type namespaced by its plugin (`<plugin>.<name>`) so plugins cannot take over core types or each
   other's; actions go through the usual dashboard action path (access, `readonly`, `confirm`,
   remote roles).
5. **The aquarium plugin** in `vclu-plugins` as the proof, and a page in the plugin docs on writing
   a widget.

## Files
- `pkg/lua/runtime/core/Dashboard.lua` (the `scene` widget, `d:widget`)
- `pkg/lua/runtime/core/Draw.lua` (new: the scene builder)
- `pkg/lua/runtime/core/PluginAPI.lua` (`plugin:widget`), `PluginSandbox.lua` (what a plugin may call)
- `web/static/js/dashboard.js` or a new `web/static/js/scene.js`
- `pkg/lua/dashboard_scene_test.go`, `pkg/lua/plugin_widget_test.go` (new)
- `dev/decisions/NNNN-scene-vocabulary.md` (new)

## Acceptance criteria
- [ ] A scene with every version 1 shape, fill, behaviour, binding and tap is in the definition as
      `type = "scene"`, frozen by a fixture; unknown keys, wrong types and scenes over the limits
      fail at the module's line, naming the valid keys.
- [ ] Only the variables travel in the values; a scene's definition changes only on reload or a
      period change (its ETag stays the same between polls).
- [ ] `dashboard.js` draws every fixture; a node of an unknown kind is skipped and the rest of the
      tile is drawn; nothing animates off screen or in a hidden tab.
- [ ] A plugin's widget is usable as `d:widget("<plugin>.<name>", ...)`; a plugin cannot register
      a core type or another plugin's name; its actions are refused like other widgets' for
      `readonly`, `confirm` without confirmation and roles that may not act.
- [ ] The aquarium plugin from stage 5 works in the panel with fish, plants, light and a sensor
      bound to the scene.
- [ ] A contract row in `CONTRIBUTING.md` for scenes and plugin widgets.
- [ ] `go test ./...` and `go test -race ./...` pass; a changelog entry in `changelog.d/` per stage
      users notice.

## Contract impact
New: the `scene` widget type in the dashboard definition, with its own version, and the
`plugin:widget` API for plugins. Both become contracts (a decision record and a row in
`CONTRIBUTING.md`); existing widget types do not change.

## Open questions
- Drag in taps (a custom knob or slider) in version 1, or later.
- Images from the plugin (data URIs, size limit) in version 1, or later.
- Text beyond one line.
- Limits: nodes per scene, animated nodes (the mock-up keeps fish to 40), definition size.
