### Dashboards

- **A slat slider on the single cover tile** - `section:cover(...)` takes `getTilt` and
  `setTilt` in its `fns` and shows a 0-90° slat slider while `getTilt` returns a number, as the
  cover group tile does; tiles from `section:area` pass a Grenton roller's slats on by themselves.
  The widget JSON of a `cover` carries `tilt` and accepts the `tilt` action, clamped to 0-90
  ([#227](https://github.com/Domktorymysli/vclu/issues/227)).
