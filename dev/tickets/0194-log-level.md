# #194: Log level for stdout, quieter per-packet lines, Docker log limit

- **Type:** feature
- **Area:** build
- **Size:** S
- **Epic:** —
- **Blocked by:** —
- **Skill:** —
- **Needs hardware:** —

> State (who, status) lives in the GitHub issue, not here. To take the ticket, ask in the issue;
> a maintainer assigns it. This file is the specification: change it by PR.

## Goal
By default vCLU writes info and above to stdout, and the Docker setup caps its log, so journald or
the container log stays small on a Pi or a small VM.

## Context
- Every `log.*` line goes through `pkg/logstream` (`cmd/vclu/main.go`: `log.SetOutput(logStream)`),
  which writes it to stdout unfiltered and keeps the last 1000 in a ring buffer for the panel and
  MCP `vclu_logs`. `parseLine` already reads the level from the `[DEBUG]` / `[WARN]` / `[ERROR]` tag.
- Lua's `Logger:debug/info/warn/error` (`pkg/lua/runtime/utils/LoggerGo.lua`) is `print("[DEBUG] ...")`
  and the like; `print` is `log.Println` (`pkg/lua/lua.go`). There is no level anywhere: no
  `.vclu.json` key, no env var, no flag. MCP `vclu_logs` hides debug by its own filter
  (`pkg/mcp/tools_dev.go`), which is why agents do not see the volume.
- Measured on one installation over an hour: 34 000 lines, 4 MB; `[DEBUG]` 58%. The loudest:
  `MQTT[MQTT]:publish(...)` on every publish (`integrations/mqtt/MqttClient.lua`),
  `CLU_REMOTE:executeAsync -> ...` on every call (`grenton/modules/CLU_REMOTE.lua`),
  `[CLIENT-REPORT] from ... values=...` on every packet from a CLU (Go,
  `pkg/server/client_subscription.go`, printed as info), and the GIN access log for every HTTP
  request, polling included (`pkg/web/server.go`: `gin.Default()`, written straight to stdout,
  not through `log`).
- `docker-compose.yml` has no `logging:` section, so Docker's json-file log grows without limit
  unless the daemon sets one. `deploy/vclu.service` logs to journald, which caps itself
  (`SystemMaxUse`, by default 10% of the file system).

## Behaviour
- `.vclu.json` `"log": { "level": "info" }`, one of `debug`, `info`, `warn`, `error`; default
  `info`. Read at start; an unknown value logs a warning and uses `info`.
- Lines below the level are not written to stdout. The ring buffer keeps every line, so
  `vclu_logs` with `level: "debug"` and the panel's log page show debug as before.
- A line without a level tag (plain `print()` from a module, Go `log.Printf` without a tag) counts
  as info.
- `[CLIENT-REPORT] from ...` is logged at debug.
- The GIN access log writes only requests with a status of 400 or more, or slower than 1 s.
- `docker-compose.yml` gets `logging: { driver: json-file, options: { max-size: "10m", max-file: "3" } }`;
  the Docker part of the install docs shows the same as `docker run --log-opt`.

## Files
- `pkg/logstream/logstream.go` (level filter for the original writer)
- `cmd/vclu/main.go`, `pkg/config/config.go` (the `log` section)
- `pkg/server/client_subscription.go` (`[CLIENT-REPORT]` at debug)
- `pkg/web/server.go` (GIN logger with a skip for fast successful requests)
- `docker-compose.yml`, `docs/` install pages if they show `docker run`
- tests in `pkg/logstream`, `pkg/web`

## Acceptance criteria
- [ ] With the default config a `[DEBUG]` line is not on stdout but is in the ring buffer and in
      `vclu_logs` with `level: "debug"`; `[INFO]`, `[WARN]`, `[ERROR]` and an untagged line are on
      stdout.
- [ ] `"log": { "level": "debug" }` writes debug to stdout; `"warn"` drops info; an unknown value
      warns and behaves as info.
- [ ] A CLU report packet produces no stdout line at the default level.
- [ ] A `GET` answered 200 in under 1 s produces no GIN line; a 404, a 500 and a slow request do.
- [ ] `docker compose config` shows the log options.
- [ ] `go test ./...` and `go test -race ./...` pass.
- [ ] A changelog entry in `changelog.d/0194-log-level.md`.

## Contract impact
Configuration: a new optional `.vclu.json` section, additive. Logs: debug lines and the access log
leave stdout by default; anyone grepping journald for them sets `"level": "debug"`. Mention it in
the changelog; no decision record.
