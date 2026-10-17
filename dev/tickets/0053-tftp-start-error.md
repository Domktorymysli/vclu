# #53: Object Manager is told when the TFTP server cannot start

- **Type:** bug
- **Area:** grenton
- **Size:** S
- **Epic:** —
- **Blocked by:** —
- **Skill:** `.agents/skills/write-contract-test`
- **Needs hardware:** Object Manager sending a project to vCLU (to check the success path is unchanged)

> State (who, status) lives in the GitHub issue, not here. To take the ticket, ask in the issue;
> a maintainer assigns it. This file is the specification: change it by PR.

## Goal
When vCLU cannot open the TFTP port, Object Manager gets no OK, and the next request tries again.

## Context
Object Manager sends a project to a CLU over TFTP. It asks the CLU to start its TFTP server first
(`req_start_ftp`, `pkg/server/handle_start_ftp.go`, and `StartTFTPd`,
`pkg/server/handle_start_tftp_request.go`). vCLU answers OK when `tftpServer.Start()` returns no error.

`Start()` (`pkg/tftp/tftp.go`) runs `ListenAndServe` in a goroutine and returns nil at once, so a
failed bind on port 69 (port taken by another TFTP server or dnsmasq, no `CAP_NET_BIND_SERVICE`,
Docker with `--security-opt no-new-privileges`, found in the review of #3) only shows as
`[TFTP] server error` in the log. Then:

- vCLU answers OK, Object Manager sends the files into nothing and the send hangs;
- `running` stays true, so every later request is answered OK too, until vCLU restarts.

`github.com/pin/tftp/v3` has `Serve(conn)` for exactly this: open the UDP socket in `Start()`,
return its error, and serve the open socket in the goroutine.

This is the path every project upload from Object Manager takes, and `handle_reset.go` stops the
server after an upload. Change only how the start reports a failure: the success path, the
timeouts, the root dir and the reset handling stay as they are. `running` is read and written
from the UDP handlers and the reset without a lock; check it with `-race` while you are there.

## Files
- `pkg/tftp/tftp.go`
- `pkg/server/handle_start_ftp.go`, `pkg/server/handle_start_tftp_request.go` (only if their error path needs to change)
- tests in `pkg/tftp/` and `pkg/server/`

## Acceptance criteria
- [ ] With port 69 (or the test port) already bound by another socket, `Start()` returns an error, `IsRunning()` is false, and a later `Start()` once the port is free succeeds. A test shows it.
- [ ] `req_start_ftp` and `StartTFTPd` get no OK when the start fails, and OK as before when it succeeds; tests send both requests.
- [ ] A file written over TFTP after a successful start lands in the root dir as before; `Stop()` after it, and `Stop()` after a failed start, do not block or panic. Tested.
- [ ] Checked by hand: Object Manager sends a project to vCLU as before (say so in the PR).
- [ ] `go test ./...` and `go test -race ./...` pass.
- [ ] CHANGELOG entry in "Unreleased".

## Contract impact
None: the protocol and the replies on success are unchanged.
