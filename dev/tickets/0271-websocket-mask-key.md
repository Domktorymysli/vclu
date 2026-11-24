# #271: gorilla/websocket with the fix for GO-2026-6278

- **Type:** fix
- **Area:** build
- **Size:** XS
- **Epic:** —
- **Blocked by:** —
- **Skill:** —
- **Needs hardware:** —

> State (who, status) lives in the GitHub issue, not here. To take the ticket, ask in the issue;
> a maintainer assigns it. This file is the specification: change it by PR.

## Goal
vclu builds with a `github.com/gorilla/websocket` that is not affected by
[GO-2026-6278](https://pkg.go.dev/vuln/GO-2026-6278), so govulncheck in CI and the release scan
pass again.

## Context
GO-2026-6278 (GHSA-w67g-5rqw-f597): gorilla/websocket v1.5.3 makes the mask key of client
frames with a weak random generator. vclu uses the library in the cloud client (`pkg/cloud`),
which sends masked frames. The mask protects proxies on the way from crafted frames, not the
content, which goes over TLS. govulncheck reports the advisory at symbol level since 23.11, so
`vulncheck` and `release-scan` fail on every PR and would block a release.

The fix is in the library's main branch (commit `d67f41855da4`) and has no tag yet: vclu takes
the pseudo-version `v1.5.4-0.20240701034025-d67f41855da4` and moves to a tag once one is out.

## Files
- `go.mod`, `go.sum`

## Acceptance criteria
- [ ] `go.mod` requires `github.com/gorilla/websocket` at `d67f41855da4` or later.
- [ ] govulncheck reports no finding outside `.github/govulncheck-accepted.txt`.
- [ ] `go test ./...` passes.

## Contract impact
None.
