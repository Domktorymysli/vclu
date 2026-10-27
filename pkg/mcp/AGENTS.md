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

## A tool reads only the arguments in its schema
`addTool` (`tools.go`) refuses an argument the schema does not name and a missing required one,
before the handler runs. A handler that reads a new argument needs it in the schema too
(`tool_args_test.go`).

## Sandbox logs leave out the start-up
`print` output in `load()` before the drafts load is the runtime's and is dropped unless `verbose`
(only `[WARN]`/`[ERROR]` lines stay). A message from the sandbox itself goes through `run.warn` or
`__sandbox_log`, which are always kept (`TestDraftTest_LeavesOutStartupLines`).
