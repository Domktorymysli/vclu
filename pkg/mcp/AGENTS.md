# pkg/mcp: MCP server and the draft sandbox

## The sandbox is a contract
`vclu_draft_test` must have no real side effects and run on a virtual clock (`sandbox_*_test.go`).
CLU calls are recorded, not sent. Anything new a draft can reach must be faked here too.

## Timeouts in tests
The sandbox stops a draft after `sandboxTimeout` and above `sandboxMemoryLimit`. Under the race
detector Lua runs several times slower, so `TestMain` raises the timeout for race builds; a test of
the timeout itself sets its own value. Do not assert wall-clock bounds tighter than that.

## The memory guard confirms with a GC
`guardMemory` (`sandbox.go`) confirms a crossing with a forced GC and subtracts what was allocated
during it: a draft that churns garbage under CPU load must not be stopped as a leak. Test changes
here under load (see the `fix-flaky-test` skill).
