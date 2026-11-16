# #NNNN: HTTP Digest authentication in the Lua HTTP client

- **Type:** feature
- **Area:** lua-runtime, plugins
- **Size:** S
- **Epic:** —
- **Blocked by:** —
- **Skill:** `.agents/skills/write-contract-test`
- **Needs hardware:** — (a device that accepts only Digest, e.g. a Hikvision intercom, for a final check)

> State (who, status) lives in the GitHub issue, not here. To take the ticket, ask in the issue;
> a maintainer assigns it. This file is the specification: change it by PR.

## Goal
A module sends any HTTP request (GET, PUT, POST…, with a body) to a device that accepts only
Digest, e.g. `HTTP:new("bramka"):setDigestAuth("admin", "secret"):put(url, xml)`.

## Context
Many devices (Hikvision ISAPI: intercoms, cameras, NVRs) answer Basic with 401 and accept only
Digest. A user asked how to open a gate from myGrenton through a Hikvision DS-KV6124 with a PUT;
Grenton's GATE HTTP cannot do it without a separate server.

`HTTP` (`HttpClient.lua`) has `setBasicAuth` and `setAuthorization`, which only set a header. Digest
cannot be done in Lua: it needs the server's challenge (`WWW-Authenticate: Digest realm, nonce,
qop, opaque`) and MD5, which the runtime does not have.

Digest is already computed in Go for camera snapshots (`pkg/camera/manager.go`, `fetchDigest`,
GET only, `qop=auth`, `opaque`). That code is the starting point: move it to a shared helper and
use it in `HTTPManager` (`pkg/lua/http.go`): send the request, on a 401 with a Digest challenge
compute the answer and send it again with the same body. The password goes from Lua to Go as a
parameter, not as a header the user could read back.

## Files
- `pkg/lua/http.go`
- `pkg/lua/runtime/integrations/http/HttpClient.lua`
- `pkg/camera/manager.go` (uses the shared helper)
- `docs` repo: the HTTP client page

## Acceptance criteria
- [ ] `HTTP:setDigestAuth(user, password)` makes sync and async requests of any method answer a Digest challenge; the body is sent again unchanged.
- [ ] `qop=auth`, no `qop`, `opaque` and `algorithm=MD5` work; `algorithm=SHA-256` works or gives a clear error.
- [ ] The URI in the answer is the request's path with its query, as the server checks it.
- [ ] A second 401 after the answer returns 401 to Lua, with no loop.
- [ ] A server that sends no Digest challenge gets the first request only (no extra request, no credentials sent).
- [ ] Camera snapshots with `auth = "digest"` work as before (tests on the shared helper).
- [ ] Tests against an `httptest` server that checks the Digest answer (RFC 7616).
- [ ] `go test ./...` and `go test -race ./...` pass.
- [ ] A changelog entry in `changelog.d/NNNN-http-digest-auth.md`.

## Contract impact
Lua API: a new method `HTTP:setDigestAuth`; nothing existing changes. No decision record needed.
