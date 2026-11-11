# #209: pkg/lua under -race fits go test's default timeout

- **Type:** test
- **Area:** lua-runtime
- **Size:** S
- **Epic:** —
- **Blocked by:** —
- **Skill:** `.agents/skills/fix-flaky-test`
- **Needs hardware:** —

> State (who, status) lives in the GitHub issue, not here. To take the ticket, ask in the issue;
> a maintainer assigns it. This file is the specification: change it by PR.

## Goal
`go test -race ./pkg/lua` runs well inside go test's default 10-minute timeout, so `go test -race ./...`
without `-timeout` passes on a developer's machine and agents get its answer in minutes.

## Context
- On a loaded machine `go test -race ./pkg/lua` took 583 s and once passed 600 s: the run failed with a
  timeout. The goroutine reported then (`TestVirtualThermostatLease_OffEndsRenewals` in
  `newLeaseEngine`) was only the test running at the 10-minute mark; it has no goroutines or channels
  of its own and passes 150 runs under `-race` with `-cpu 1,2,8`.
- The cost is `NewLuaEngine`: about 0.42 s under `-race` (0.05 s without), nearly all of it lexing and
  parsing the ~1 MB runtime through `require`. Almost every one of the ~850 tests creates an engine.
- CI's race job sets `-timeout 20m` (`.github/workflows/test.yml`), so CI did not fail; local runs and
  agents use the default.
- vCLU itself must not change: a process-wide cache of compiled runtime files keeps ~30 MB alive, which
  the MCP sandbox's memory guard counts (`pkg/mcp` `TestSandbox_MemoryGuardBacksOff`).

## Files
- `pkg/lua/lua.go`
- `pkg/lua/runtime_cache.go`
- `pkg/lua/runtime_cache_test.go`
- `pkg/lua/AGENTS.md`

## Acceptance criteria
- [ ] pkg/lua's tests reuse the compiled runtime files between engines; vCLU does not.
- [ ] A changed runtime file is compiled again, files outside the runtime directory are never cached,
      and a missing module is still an error (`runtime_cache_test.go`).
- [ ] `go test -race -count=1 ./pkg/lua` takes a fraction of the 10-minute default (about 80 s here, was 583 s).
- [ ] `TestSandbox_MemoryGuardBacksOff` in `pkg/mcp` passes as before.
- [ ] `go test ./...` and `go test -race ./...` pass.

## Contract impact
None: tests only.
