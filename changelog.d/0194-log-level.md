### Core

- **Log level for stdout** - `.vclu.json` `"log": { "level": "info" }` (`debug`, `info`, `warn`, `error`; default `info`, an unknown value warns and is `info`) sets what goes to stdout, so to journald or the container log. A line's level is its `[DEBUG]` / `[INFO]` / `[WARN]` / `[ERROR]` tag; an untagged line is info, or warning/error when its text says so. The panel's log page and MCP `vclu_logs` keep every line, debug included ([#194](https://github.com/Domktorymysli/vclu/issues/194)).

  Debug lines leave stdout by default: anyone grepping journald for `Logger:debug` output sets `"level": "debug"`.

- **Quieter per-packet lines** - `[CLIENT-REPORT] from ...`, written for every report packet from a CLU, is now debug. The HTTP access log (`[GIN]`) writes only requests answered with 400 or more or slower than 1 s; a WebSocket or an event stream only by its status ([#194](https://github.com/Domktorymysli/vclu/issues/194)).

### Installation

- **Docker log limit** - `docker-compose.yml` caps the container log with `json-file`, `max-size: 10m`, `max-file: 3`; the README's `docker run` has the same `--log-opt` ([#194](https://github.com/Domktorymysli/vclu/issues/194)).
