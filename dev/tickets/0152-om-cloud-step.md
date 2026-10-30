# #0152: Object Manager 1.13 resets a vCLU after sending a project

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
Sending a project from Object Manager 1.13 ends with a CLU reset, so vCLU loads the new
configuration, also when the project has the CLU's `UseCloud` feature on.

## Context
Reported after #149: OM 1.13.1 writes `MAIN.LUA`, `OM.LUA` and `USER.LUA`, then vCLU logs
```
[TFTP] read file not found: clu/a/CLOUD-PUBLIC.CRT
[TFTP] read file not found: clu/a/CLOUD-PUBLIC.CSR
```
and OM shows "Konfiguracja połączenia CLU do chmury nie powiodła się". The files are on vCLU,
but the configuration is not loaded.

What OM 1.13.1 does after uploading the files (its strings are AES-encrypted, so grep finds none
of them): when the CLU's embedded feature `UseCloud` (index 18) is `1`, it reads
`a:\CLOUD-PUBLIC.CRT`; when that is missing or empty, it reads `a:\CLOUD-PUBLIC.CSR`, has it
signed by the Grenton cloud and uploads the certificate and `CLOUD-ROOT.PEM`. When that fails it
shows the message above and **skips the CLU reset**.

`UseCloud` comes from `CLU:set(18, x)` in `om.lua` when OM imports the CLU; the interface for
vCLU's `clu` profile (hardware type 0x13, firmware 0x1f6) has it on by default, and a project
sent once keeps OM's value. vCLU does not connect to the Grenton cloud, so to OM it should be a
local-only CLU.

## Files
- `pkg/tftp/tftp.go`

## Acceptance criteria
- [ ] A TFTP read of `a:\CLOUD-PUBLIC.CRT` with no such file returns a non-empty placeholder;
  other missing files are still not found; a test that fails without it.
- [ ] Checked with OM 1.13.1 and `UseCloud` on: sending the project shows no cloud error and
  vCLU reloads (say so in the PR).
- [ ] `go test ./...` and `go test -race ./...` pass.
- [ ] A changelog entry in `changelog.d/0152-om-cloud-step.md`.

## Contract impact
None for scripts. Object Manager sees the CLU's Grenton cloud as configured.
