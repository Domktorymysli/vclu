# #160: A bars widget: a metric per hour or per day of the house's zone

- **Type:** feature
- **Area:** web
- **Size:** M
- **Epic:** —
- **Blocked by:** —
- **Skill:** `.agents/skills/run-local-vclu`
- **Needs hardware:** —

> State (who, status) lives in the GitHub issue, not here. To take the ticket, ask in the issue;
> a maintainer assigns it. This file is the specification: change it by PR.

## Goal
`section:bars(id, label, metricPath, opts)` draws one bar per hour or day of the house's zone from
`metric()` samples: daily kWh from a counting meter, PV against consumption, the warmest hour.

## Context
Metrics (`pkg/metrics`) keep raw samples: a ring buffer in RAM and one file a day on disk, no
downsampling; files older than `retainDays` (7 days) are removed, so a 30-day range has no data
unless the retention is raised. The chart widget reads raw samples through `/api/dashboard/:id/metrics`, gated by
`Dashboard.API.usesMetric`. The house's zone is `datetime.setTimezone(...)` or the server's,
named for the browser by `__go_calendar_zone()` (`pkg/lua/calendar.go`, #129).

- Aggregation in Go (`pkg/metrics/buckets.go`): buckets from local midnight (a DST day is 23 or
  25 h) or on the hour (a fall-back night has 02:00 twice), `sum`, `avg` (plain mean of the
  samples), `max`, `min`, `delta`. `delta` sums a counter's rises over its high mark: a dip (one
  bad reading, a 0 from a Modbus timeout) adds nothing; a value lower for 3 samples or an hour is
  a reset and the count goes on from it. A rise across a gap goes to the later bucket when it is
  at most a bucket long, else it is left out. A bucket without samples is `null`, not 0.
- `GET /api/dashboard/:id/bars?widget=<id>`: the widget names its metrics, range and aggregation
  (`Dashboard.API.barsSpec`), so the page cannot ask for another metric or a longer range; the
  raw metric stays unreadable through `/metrics`. Dashboard auth as for charts; 30 s cache;
  concurrent misses of one widget load once. Buckets before the retention are marked `old`.
- Retention is `metrics.retentionDays` in `.vclu.json` (1-366, default 7). Disk: about 25 KB a
  day per metric sampled every 60 s, about 200 KB every 10 s; backups include `metrics/` (not
  changed here). `/api/metrics/data` and `vclu_metrics` reach back at most the retention and
  answer with at most 2000 points (averaged, same shape).
- Files keep 10 significant digits (a meter at 123456.78 kWh lost its decimals with 6); a last
  line without its newline (still being written) is not read.

## Files
- `pkg/lua/runtime/core/Dashboard.lua` (`bars` on both builders, `barsSpec`, the definition)
- `pkg/metrics/buckets.go`, `storage.go`, `collector.go`; `pkg/config/config.go` (`retentionDays`), `cmd/vclu/main.go`
- `pkg/web/handle_api_metrics.go`, `pkg/mcp/tool_metrics.go` (range and point caps)
- `pkg/web/handle_dashboard_bars.go`, `handle_web_dashboard.go` (cache), `server.go` (route)
- `web/static/js/dashboard.js`, `web/static/css/dashboard.css`
- `pkg/mcp/tools_dev.go`, `docs/ai/examples.md`, `docs/ai/lua-api-reference.md`

## Acceptance criteria
- [ ] Daily buckets start at the house's midnight; on 29.03 and 25.10 (Europe/Warsaw) a day holds 23 and 25 hourly rises (`pkg/web/dashboard_bars_test.go`, `pkg/metrics/buckets_test.go`).
- [ ] `delta` over a reset is never negative and counts the rises after it; one or two bad readings add nothing; a missing hour or day is a gap (`TestAggregateDelta`, `TestAggregateDeltaDips`, `TestAggregate`).
- [ ] Concurrent misses of one widget aggregate once (`TestDashboardBarsMergesConcurrentLoads`); days before the retention are `old` (`TestDashboardBarsOldBuckets`).
- [ ] Raw queries stop at the retention and at 2000 points (`TestQueryRangeCapsRetentionAndPoints`, `TestMetricsToolCapsRangeAndPoints`); a half-written line is skipped (`TestReadSingleFileSkipsHalfWrittenLine`).
- [ ] A public dashboard serves its own bars without login; another widget, another dashboard's widget and the raw metric are refused; session and PIN dashboards keep their gates (`TestDashboardBarsFollowDashboardAuth`).
- [ ] Wrong options fail at build with the module's line (`TestDashboardBarsRefused`).
- [ ] The page draws the bars as SVG built with DOM calls, day names (pl/en) or hours on the axis, a value on hover or tap, the newest bar marked as filling, in light and dark themes, at phone width, without the grow animation under reduced motion.
- [ ] `go test ./...` and `go test -race ./...` pass.
- [ ] A changelog entry in `changelog.d/0160-dashboard-bars.md`.

## Contract impact
Lua API: new `section:bars` / `dash:bars` (additive). HTTP: new `/api/dashboard/:id/bars`.
Config: new `metrics.retentionDays` (default unchanged). `/api/metrics/data` and `vclu_metrics`
return at most 2000 points and no samples older than the retention (same shape).
