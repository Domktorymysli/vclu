# #196: Status page: disk space used by vCLU, growth and a warning before the disk fills

- **Type:** feature
- **Area:** web
- **Size:** M
- **Epic:** —
- **Blocked by:** —
- **Skill:** —
- **Needs hardware:** —

> State (who, status) lives in the GitHub issue, not here. To take the ticket, ask in the issue;
> a maintainer assigns it. This file is the specification: change it by PR.

## Goal
The panel shows what vCLU keeps on disk and how fast it grows, and warns while there is still
time to act, so a full disk is not the first sign.

## Context
- A user's 2 GB VM filled within days: daily history files up to 45 MB (#192) and an unbounded
  log (#194). Nothing in the panel showed it.
- What exists: the history store reports `diskUsageBytes` and the oldest day
  (`pkg/history/store.go`, shown on `/history`); history has `retentionDays` (90) and `maxDiskMB`
  (1024) in `.vclu.json`. Metrics live in `data/clu/metrics`. `deploy/update.sh` keeps the three
  newest `backup-*.tar.gz` next to the binary. Promote backups go to `workspace/backups`.
- Logs: under systemd they go to journald, under Docker to the container log; from inside vCLU
  neither size can be read reliably. What vCLU can measure is what it writes: bytes per hour
  through `pkg/logstream`.

## Behaviour
- A "Storage" card on the status page (`/status`, `web/templates/status.html`):
  - the file system of the data directory: total, free, free in %;
  - vCLU's parts, each with its size: history, metrics, backups (update and promote), plugins,
    the rest of the data directory;
  - growth: history written in the last 24 h (from the day files) and log output per hour (from
    the logstream counter), with "at this rate the disk is full in N days" when under 90 days;
  - a link to the history settings and to the log level setting (#194).
- A warning banner in the panel (all pages, like the update badge) when free space is below
  10% or 500 MB, whichever is larger, and a red one below 3% or 100 MB. It says what takes the
  most space and links to the card.
- The same numbers in `GET /api/storage` (Swagger), as metrics `vclu:disk_free_bytes`,
  `vclu:history_bytes`, `vclu:log_bytes_per_hour` for dashboards and Prometheus, and in MCP
  (`vclu_metrics` lists them) so an agent can answer "why is my disk full".
- Measured at start and then every 15 min; walking the directories must not block the runtime
  (a goroutine, sizes cached).
- History stays within the disk: when free space is below the red line, history rotates the
  oldest day files beyond 7 days even under `retentionDays`, and logs it once (the existing
  `maxDiskMB` still applies).

## Files
- `pkg/web/` (status page card, `GET /api/storage`, banner like `web/static/js/update-notice.js`)
- `pkg/history/` (bytes written per day, rotation on low disk)
- `pkg/logstream/` (byte counter)
- `pkg/metrics/` or where `vclu:*` metrics are registered
- `web/locales/en.json`, `web/locales/pl.json`
- tests in each package; an e2e check of the card in vclu-e2e if the dashboard profile has room

## Acceptance criteria
- [ ] On Linux and macOS the card shows the data directory's file system and the sizes of
      history, metrics, backups and plugins; the numbers match `du` within 1%.
- [ ] With a fake file system at 8% free the banner shows on every page and names the largest
      part; at 2% it is red; above 10% (and 500 MB) there is none.
- [ ] Log bytes per hour change when debug lines are written to stdout and not when they are
      filtered out (#194).
- [ ] "Full in N days" from a growth of 100 MB/day and 1 GB free says 10 days; none at 0 growth.
- [ ] Below the red line history removes day files older than 7 days, never today's, and logs
      it once.
- [ ] `GET /api/storage` and the three metrics are present; the walk runs off the Lua thread.
- [ ] `go test ./...` and `go test -race ./...` pass.
- [ ] A changelog entry in `changelog.d/0196-storage-status.md`.

## Contract impact
HTTP API: a new read-only endpoint, additive. History: rotation on low disk is new behaviour
(oldest data first, only when the disk is nearly full); mention it in the changelog. No decision
record.
