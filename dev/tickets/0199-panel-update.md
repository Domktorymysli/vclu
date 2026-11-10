# #199: Update vCLU from the panel

- **Type:** feature
- **Area:** web, build
- **Size:** L
- **Epic:** —
- **Blocked by:** —
- **Skill:** `.agents/skills/write-contract-test`
- **Needs hardware:** a Raspberry Pi with the .deb and one with `install.sh`, to check the restart and the roll back

> State (who, status) lives in the GitHub issue, not here. To take the ticket, ask in the issue;
> a maintainer assigns it. This file is the specification: change it by PR.

## Goal
When the panel shows a new version (#155), an admin can click "Update" and vCLU updates itself to
that release: backup, download, checksum, swap, restart, and a roll back to the old version when
the new one does not start. The panel shows each step and the result.

## Context
Today the update notice tells users to run `update.sh` over ssh. Many Grenton users never use a
terminal, so they stay on old versions. #155 left updating from the panel out of scope for this
ticket.

### vCLU does not replace itself
`deploy/vclu.service` runs with `ProtectSystem=strict` and `ReadWritePaths=<data dir>`: the process
cannot write its binary, the unit file or run apt. Keep that sandbox. vCLU only **asks** for an
update; a separate systemd unit outside the sandbox does it:

- `vclu-update.path` watches `<data dir>/update/request`;
- it starts `vclu-update.service` (oneshot, root, no `ProtectSystem`), which runs the updater;
- the updater writes progress and the result to `<data dir>/update/status.json` and removes the
  request.

The request holds **only a version**, `vX.Y.Z`. The updater rejects anything else. Where to
download it from and how to check it is known only to the updater: GitHub Releases of this repo,
the asset for this machine's arch, verified against `checksums.txt` of the same release (today
`update.sh` does not check it; add that for both paths). A taken over panel can therefore at most
install another official release, never a file or URL of its choosing. A version older than the
running one is refused unless the request says `"downgrade": true`, which only the confirm dialog
sets.

### Install types
vCLU tells which one it runs on and the panel offers only what works there:

- **`install.sh` (tar.gz):** the updater is `update.sh --version <v>` from the installed files
  (not fetched from `main`). `install.sh` and `update.sh` install the two units.
- **.deb / APT** (also the Pi image, #180): the updater runs `apt-get update` and
  `apt-get install -y vclu=<version without v>`; the package ships the two units. The existing
  `preinst` backup stays the backup.
- **Docker:** no updater. The dialog shows the update command (`docker compose pull && docker
  compose up -d`) with a "Copy" button, as today.
- **Anything else** (a binary started by hand, `dev` builds): no button, the dialog as today.

Detection: Docker by `/.dockerenv` or the image's env; .deb by `/var/lib/dpkg/info/vclu.list`;
`install.sh` by the unit in `/etc/systemd/system/vclu.service`; and in each case the updater unit
must be present (`systemctl` cannot be called from the sandbox: the unit writes
`<data dir>/update/available` on install, the panel reads that).

### Start check and roll back
- The updater keeps the backup as `update.sh` does, records the old version and restarts vCLU.
- The new vCLU writes `<data dir>/update/started` with its version once modules are loaded and the
  web server answers.
- No such file for the new version within 120 s: the updater restores the backup (tar.gz: the
  archive; .deb: `apt-get install vclu=<old>` from the APT pool, then the archive for the data),
  starts the old version and writes `"result": "rolled_back"` with the last 200 lines of the
  journal of the failed start.
- This is the only automatic roll back in vCLU; it exists because a user without ssh has no other
  way back.

### Panel
- The update dialog (#155) gets an "Update to X.Y.Z" button for admins, next to the release notes
  and "Before you update". Confirm: "vCLU will restart; lights and automations keep working on the
  CLUs, vCLU automations stop for about a minute."
- `POST /api/update/install` `{ "version": "v1.11.0" }` (admin, CSRF as other POSTs): writes the
  request, `409` when one is in progress, `400` for a version not in the last check's `releases`
  (or older than ours without `downgrade`).
- `GET /api/update/status`: `{ "state": "idle|queued|backup|download|verify|install|restart|done|rolled_back|failed", "from", "to", "started_at", "finished_at", "error", "log" }`
  read from `status.json`.
- While it runs the dialog shows the steps; during the restart it polls `/api/health` and reloads
  the page when vCLU answers. After the reload a banner shows the result: "Updated to 1.11.0" or
  "1.11.0 did not start; 1.10.0 was restored" with the log behind "Details".
- Every request and result goes to the audit log with the user.

### Out of scope
Updating from MCP, from Object Manager (a `VCLU:Update()` method) or automatically on a schedule;
plugins (they have their own update, #112); Docker image updates (Watchtower can be mentioned in
the docs).

## Files
- `deploy/vclu-update.path`, `deploy/vclu-update.service` (new), `deploy/vclu-updater.sh` (new:
  validates the request, picks tar.gz or apt, start check, roll back, `status.json`)
- `deploy/update.sh`, `deploy/install.sh`: checksum check, install the units
- `deploy/deb/*`, `.goreleaser.yml`: ship and enable the units in the package
- `pkg/updatecheck/` or a new `pkg/selfupdate/`: install type, request, status, `started` marker
- `cmd/vclu/main.go`: write `started` after modules load and the web server listens
- `pkg/web/`: `POST /api/update/install`, `GET /api/update/status`
- `web/static/js/update-notice.js`, `web/templates/`, `web/locales/`
- docs: the upgrade page, one paragraph on the button and the roll back
- `changelog.d/199-panel-update.md`

## Acceptance criteria
- [ ] The vCLU process writes nothing outside its data directory; the request file holds only a
  version and an optional downgrade flag; a test.
- [ ] The updater refuses a request that is not `vX.Y.Z`, a version without a release, an asset
  whose checksum does not match `checksums.txt`, and an older version without the flag; a test
  per case (shell test or Go test driving the script with a fake release server).
- [ ] `update.sh` checks `checksums.txt` before it changes anything.
- [ ] `POST /api/update/install` is admin only and answers `409` while an update runs; a test.
- [ ] On a Pi with `install.sh` and on a Pi with the .deb: updating from the panel from the
  previous release to the current one works and the panel shows "Updated to …".
- [ ] A release that exits at start (a test build) is rolled back within about 2 min on both
  installs, the old version answers, and the panel shows the log.
- [ ] In Docker and on a `dev` build there is no "Update" button.
- [ ] `go test ./...` and `go test -race ./...` pass.
- [ ] A changelog entry in `changelog.d/199-panel-update.md`.

## Contract impact
New HTTP endpoints (`/api/update/install`, `/api/update/status`) and new files in the data
directory (`update/`). The install layout changes (two new systemd units); `update.sh` and the
.deb must keep updating installs made before this ticket.
