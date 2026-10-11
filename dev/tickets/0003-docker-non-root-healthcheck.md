# #3: Docker image runs as a non-root user and reports its health

- **Type:** chore
- **Area:** build
- **Size:** M
- **Epic:** —
- **Blocked by:** —
- **Skill:** —
- **Needs hardware:** — (a Linux host with Docker; `--network host` does not behave the same in
  Docker Desktop on macOS and Windows)

> State (who, status) lives in the GitHub issue, not here. To take the ticket, ask in the issue;
> a maintainer assigns it. This file is the specification: change it by PR.

## Goal
`ghcr.io/domktorymysli/vclu` runs vCLU as an unprivileged user, keeps working for people who
already have a data volume, and Docker shows the container as healthy or unhealthy.

## Context
Two Dockerfiles build the same image: `Dockerfile` (from source, used by `docker-compose.yml`) and
`Dockerfile.release` (GoReleaser puts a prebuilt binary in the context; `.goreleaser.yml`,
`dockers:`). Both are Alpine 3.21, run as root, use `/data` as a volume and start
`/opt/vclu/vclu --auto --data-dir /data --web-port 8080`. README runs it with `--network host`.

Things that make "just add `USER`" break:

- **Port 69/udp (TFTP)** is below 1024. A non-root process needs `CAP_NET_BIND_SERVICE`, e.g.
  `setcap cap_net_bind_service=+ep` on the binary in the image (package `libcap`), so users do not
  have to add `--cap-add`.
- **Existing volumes** were written by root. After the switch vCLU cannot write to them. The image
  must either fix ownership on start (an entrypoint that runs as root, `chown`s `/data` once when
  needed and then drops to the user, e.g. with `su-exec`), or the change must be documented as a
  breaking step with the command to fix the volume. Pick one and say why in the PR.
- **GPIO** (`/dev/gpiomem`) needs the user in the right group when the device is passed in; at
  least document it.
- `/api/health` exists, is public and returns `{"status":"ok"}`; Alpine's busybox `wget` can call it.

## Files
- `Dockerfile`, `Dockerfile.release` (keep them in step)
- an entrypoint script if the volume is migrated on start
- `README.md`, `README.en.md` (Docker section), `CHANGELOG.md`

## Acceptance criteria
- [ ] Both images run vCLU as a user with a fixed UID/GID (e.g. 1000), visible in `docker exec <c> id`
  and `ps`.
- [ ] With `--network host` on Linux: the web UI answers on 8080, the UDP command port 1234 and TFTP
  port 69 are open (`ss -lun` in the container or on the host), without `--cap-add`.
- [ ] A volume created by the current root image keeps working after switching to the new image
  (test it: run the old image, create data, switch).
- [ ] `HEALTHCHECK` calls `/api/health`; `docker ps` shows `healthy` after start, and `unhealthy`
  when the web server does not answer.
- [ ] `docker build -f Dockerfile .` works, and the release build passes in a GoReleaser snapshot
  (`goreleaser release --snapshot --clean`) or the `Release rebuild` workflow on a test tag.
- [ ] README Docker section and CHANGELOG describe the change and what existing users must do, if anything.

## Contract impact
None in code. The image's runtime user changes: call it out in the CHANGELOG.
