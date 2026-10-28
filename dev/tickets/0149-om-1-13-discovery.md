# #0149: Object Manager 1.13 adds a vCLU found by discovery

- **Type:** bug
- **Area:** grenton
- **Size:** S
- **Epic:** —
- **Blocked by:** —
- **Skill:** —
- **Needs hardware:** Object Manager 1.13 and a vCLU on the same LAN

> State (who, status) lives in the GitHub issue, not here. To take the ticket, ask in the issue;
> a maintainer assigns it. This file is the specification: change it by PR.

## Goal
Fixes for Object Manager 1.13: a vCLU found by discovery is added to a project, gets its project
key and the project; when that cannot work, vCLU's log says why and what to change in OM.

## Context
Reported with OM 1.13.1-262202: adding a new vCLU fails in OM, and vCLU logs
```
[BROADCAST] unknown command from 192.168.50.195: <16 garbage bytes>d2d452b:null:192.168.50.1
```
Reproduced on a Raspberry Pi with OM 1.13.1 (OM 1.8 for comparison). What OM 1.13 does after a
discovery:

- **It assigns the CLU an address.** It takes the address from the project's IP range, skipping
  addresses of other CLUs in the project, and sends `req_set_clu_ip:<serial>:<ip>:<gateway>`
  (unicast `SetIp` first, then broadcast), encrypted with the temporary key from vCLU's latest
  discovery answer. With no free address in the range it sends `null`. A vCLU reset in the web UI
  gets a new serial, so to OM it is a new CLU and the old entry in the project still holds its
  address.
- **vCLU cannot move** but confirmed every address. OM accepts the confirmation (it comes from the
  CLU's old address), puts the CLU at the address it asked for (`192.168.1.2`, or `null`, which
  Java resolves to localhost), and sends SetKey there: "Host is down" or "Receive timed out", and
  the project never reaches vCLU.
- **The garbage in the report** is the first AES block of a message decrypted with the right key
  and another IV. vCLU draws a new temporary IV at every discovery; a request made with an older
  one reads like that. Not reproduced here; the log now says when it happens.

Separately, the wizard's last step POSTs `/api/restart` from the page, but its security step has
just turned the login on: the restart gets 401 and vCLU keeps running without the keys and the
Grenton protocol until restarted by hand.

Not changed: OM logs "Bad padding size" at discovery while it checks whether the CLU already has
the project's key; a vCLU without it is expected to fail that check, and OM then sends SetKey.

## Files
- `pkg/server/handle_set_clu_ip.go`, `pkg/server/handle_set_ip_request.go` (confirm only vCLU's own address)
- `pkg/server/server.go`, `pkg/server/server_discovery.go` (logs)
- `pkg/web/handle_api_wizard.go`, `web/static/js/wizard.js` (restart after finish)

## Acceptance criteria
- [ ] `req_set_clu_ip` / `SetIp` for vCLU's own address is answered as before; for another address
  or `null` it is not answered, and the log names both addresses and what to change in OM; a test
  for broadcast and unicast.
- [ ] The log shows, without keys: the temporary IV of each discovery answer, which key a set-IP
  request came with, and for an unknown command its bytes escaped plus a hint when only the first
  16 bytes are unreadable; a test for the hint.
- [ ] Wizard finish restarts vCLU itself, without a login; a refused finish does not; a test.
- [ ] Checked with OM 1.13.1: discovery, adding the vCLU, SetKey and sending the project succeed
  (say so in the PR).
- [ ] `go test ./...` and `go test -race ./...` pass.
- [ ] A changelog entry in `changelog.d/0149-om-1-13-discovery.md`.

## Contract impact
None for scripts. Object Manager no longer gets a confirmation for an address vCLU does not have.
