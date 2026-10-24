### Dashboards

- **A status rounds its number: `opts.decimals`** - `section:status(..., unit, { decimals = 1 })` shows the number the function returns rounded to that many places (0-6), with the viewer's decimal separator ("21,4 °C" in Polish, "21.4 °C" in English), in a tile and in the header; `21` with `decimals = 1` shows `21,0`. Modules no longer need to format the value into a string to round it, which also fixed the separator to one language. Without the option nothing changes; a value that is not a whole number 0-6 fails at the module's line.
