# #155: The panel says when a new vCLU version is out

- **Type:** feature
- **Area:** web, core
- **Size:** M
- **Epic:** —
- **Blocked by:** —
- **Skill:** `.agents/skills/write-contract-test`
- **Needs hardware:** —

> State (who, status) lives in the GitHub issue, not here. To take the ticket, ask in the issue;
> a maintainer assigns it. This file is the specification: change it by PR.

## Goal
When a newer release is out, the top right corner of every panel page shows "vCLU X.Y.Z is
available"; a click shows what changed in each skipped release and how to update. The check can be
turned off in Settings and is on by default.

## Context
Users find out about a release only on GitHub, so many run old versions with bugs fixed long ago.
`https://update.vclu.pl` answers whether a version is outdated, from the GitHub Releases of this
repo. What vCLU must do to use it is summed up below.

### Contract (summary)
- `GET https://update.vclu.pl/v1/check?id=<instance id>&version=<version>&os=<GOOS>&arch=<arch>`,
  `User-Agent: vclu/<version>`, no other headers than Go's defaults, no cookies, no auth, HTTPS
  with certificate verification.
- **Instance id:** a random UUID v4 created on the first check, saved in the data directory, kept
  across restarts, `update.sh` and Docker image updates. Never derived from a serial, MAC, CLU id,
  host name or anything about the house. Deleting the file makes a new one.
- **arch:** the release asset's name: `amd64`, `arm64`, `armv6`, `armv7` (GOARCH `arm` + GOARM).
- **When:** once 1-10 min (random) after start, then every 6 h ± 30 min. Timeout 10 s. On any
  error (network, timeout, `429`, `5xx`) wait for the next regular check, no retries. At most one
  check in flight. Never blocks start-up; a failure is a debug log line. A `400` is a vCLU bug:
  log it once.
- **Answer:** `update_available`, `latest` {version, name, published_at, url}, `releases` (every
  release newer than ours, newest first, optional `notes.en` / `notes.pl` markdown and optional
  `summary.en` / `summary.pl` {`highlights`: one line, `before_update`: markdown points}). Unknown
  fields are ignored. The last good answer is kept and shown after a failed check.
- **What is sent:** the instance id, version, os and arch, and nothing else (no house data, no
  object names, no logs); nothing at all when the check is off.

### Setting
- `.vclu.json`: `"updateCheck": { "enabled": true }`; missing means on.
- Environment `VCLU_UPDATE_CHECK=off` turns it off regardless of the file (CI, test rigs, images
  built for demos), so test runs send no checks.
- Settings page: a switch "Check for new versions" ("Sprawdzaj nowe wersje") with one line under
  it (`update_check_desc` in `web/locales/`). Turning it off stops the checks at once and hides the notice;
  turning it on schedules a check within minutes, not at the next 6 h tick. No restart.
- A build whose version has no `X.Y.Z` (`dev`) still checks but never shows a notice; the service answers `update_available: false`.

### Panel
- `GET /api/update` (logged-in users): `{ "enabled", "checked_at", "current", "update_available",
  "latest", "releases" }` from the last good answer; `enabled: false` and nothing else when off.
- Every page with the top bar shows a small badge in the top right corner when
  `update_available`: "vCLU 1.8.0" with an up-arrow icon. A click opens a short dialog, not a wall
  of text. For each of the 3 newest skipped releases: the version, date and a link to `url`; from
  `summary[<panel language>]`, else `summary.en`, the highlights line and the `before_update`
  points, visibly marked "Before you update" ("Przed aktualizacją"); the full
  `notes[<panel language>]`, else `notes.en`, collapsed behind "More" ("Więcej"). Older skipped
  releases: one line each (version, date, link). Under it the update command from the docs page
  and a link to it.
- Summaries and notes are rendered as a small markdown subset (bold, `-` lists, inline code, `[text](https://…)`
  links opening in a new tab) with everything escaped: no raw HTML, only `https://` links.
- "Hide until next version": per browser, remembered for this `latest.version`; a newer one shows
  the badge again.

### Docs
One sentence in the configuration reference, in one place so it is easy to change: the check
sends the instance id, version, os and arch; it is turned off with the switch in Settings or
`VCLU_UPDATE_CHECK=off`.

## Out of scope
Updating from the panel (a separate ticket: download, checksum, swap, restart, roll back on a
failed start). Showing the notice in Home Assistant, MQTT or MCP.

## Files
- `pkg/updatecheck/` (new): instance id, schedule, client, last answer, notes renderer; base URL
  and clock injectable for tests
- `pkg/config/`: `UpdateCheck` section and the env override
- `cmd/vclu/main.go`: start the checker with the version
- `pkg/web/`: `GET /api/update`, `PUT /api/update/settings`, the setting on the Settings page
- `web/templates/settings.html`, `web/templates/_sidebar.html`, a shared script and CSS for the
  badge and dialog, `web/locales/`
- `README.md`, `README.en.md` (the sentence above), `changelog.d/<ticket>-update-check.md`

## Acceptance criteria
- [ ] The id is a UUID v4 created once in the data directory and reused after a restart; a test.
- [ ] The request has exactly `id`, `version`, `os`, `arch` and `User-Agent: vclu/<version>`, no
  other headers than Go's defaults; `arch` is `armv7` for GOARCH `arm` GOARM 7; a test against an
  `httptest` server.
- [ ] First check between 1 and 10 min after start, then 6 h ± 30 min; no retry after an error; one
  check in flight; a test on an injected clock.
- [ ] Off in the file or by `VCLU_UPDATE_CHECK=off`: no request at all; turning off at run time
  stops the next check; turning on schedules one within minutes; a test.
- [ ] A failed check keeps the last good answer; unknown fields are ignored; a test.
- [ ] `GET /api/update` needs a login and returns the shape above; a test.
- [ ] The badge appears only with `update_available`; summaries and notes are escaped (a
  `<script>` and a `javascript:` link stay text); a test of the renderer.
- [ ] No test or CI run talks to `update.vclu.pl`; `tools/demo/run.sh` sets `VCLU_UPDATE_CHECK=off`.
- [ ] The Settings switch has its label and its one-line description; the docs sentence is in the
  configuration reference.
- [ ] `go test ./...` and `go test -race ./...` pass.
- [ ] A changelog entry in `changelog.d/<ticket>-update-check.md`.

## Contract impact
A new outgoing request, on by default and described to the user. A new config section and API
endpoints. Nothing existing changes.
