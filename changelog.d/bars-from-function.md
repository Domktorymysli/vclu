### Dashboards

- **Bars from a function** - `section:bars(id, label, fn, opts)` takes, in place of a metric path, a function returning the samples itself: `{ { t = unix seconds, v = number }, ... }` (any order, up to 10000). The server aggregates them per hour, day, week or month like a metric's (`sum`, `avg`, `delta`...), as far back as the function keeps them, so no bar is "brak danych" because of `metrics.retentionDays`. `compare` can be a function as well. The page and the apps read the same `/api/dashboard/<id>/bars`, unchanged.

### Plugins

- **Bars on a plugin's dashboard** - a plugin's dashboard can draw `bars`, only from the plugin's own function (a metric path, as the source or as `compare`, is still refused: it reaches the house's metrics). `plugin:has("widget:bars")` says whether this vclu offers it. First user: `@vclu/liczniki`, meter readings typed in by hand with the usage per month.
