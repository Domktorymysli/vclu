### Dashboards

- **Colour temperature widget** - `section:colorTemp(id, label, fns, opts)` is a slider from warm to cool white over a gradient, the tile says "3000 K · ciepła" (warm, neutral, cool), with a switch and a brightness slider when the light has them. `fns` are `getKelvin`/`setKelvin` (plus `isOn`, `switchOn`, `switchOff`, `getBrightness`, `setBrightness`) or an object with `getColorTemp`/`setColorTemp`; `opts.unit = "mired"` converts for lights that speak mireds. A set outside `min`..`max` (default 2700-6500 K, step 50) is clamped. `section:area` shows it for an exposed light whose object has `getColorTemp`/`setColorTemp` ([#161](https://github.com/Domktorymysli/vclu/issues/161)).

### Lua runtime

- **`lib/cct`: two dimmers as one tunable white light** - `require("lib/cct").mix(warm, cold, { warm = 2700, cold = 6500 })` turns a warm and a cold white channel (Grenton dimmers, LED channels, virtual dimmers) into the functions of `section:colorTemp`: the brightness is their sum, the colour temperature their ratio; changing one keeps the other, switching on brings back the last level and colour temperature (also after a wall switch), a dim light on 0..255 channels keeps its kelvin, and commands go out at least 50 ms apart ([#161](https://github.com/Domktorymysli/vclu/issues/161)).
