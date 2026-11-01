### Dashboards

- **A gauge tile** - `section:gauge(id, label, getValue, opts)` shows a number between `min` and `max` on a 240° arc (the value in the middle) or, with `style = 'bar'`, on a horizontal bar. `thresholds = { {8, 'warning'}, {11, 'danger'} }` colour the fill and the tile from each value on, `target` (a number or a function) draws a marker such as the contracted power limit. A value outside the range is drawn at the end of the scale and shown as it is; nil shows `—`, an error the error mark. Wrong options fail at build ([#158](https://github.com/Domktorymysli/vclu/issues/158)).
