---
name: write-contract-test
description: "Add or extend a contract test in vCLU when a change adds or alters behaviour that user scripts, plugins or integrations rely on (events, values, errors, origin/actor, access, persistence)."
---

# Write a contract test

**When:** a change adds or alters behaviour that user scripts, plugins or integrations see:
events, values, errors, who-changed-it, access, persistence.

**Read first:** CONTRIBUTING.md, section Contracts (the table says which file holds which contract);
the "Tests" section of the root `AGENTS.md`.

## Steps

1. Find the contract in the CONTRIBUTING.md table. Add to its test file; if the behaviour is new,
   add a row to the table in the same PR.
2. Test from the outside: drive the Lua runtime the way a script does, through a harness such as
   `newPersistEngine` (`pkg/lua/persist_test.go`) or `newHistoryEngine` (`pkg/lua/history_test.go`)
   and `(*LuaEngine).Execute`, and assert what a script or integration observes, e.g. the
   `state_changed` event with its `origin`/`actor`, the MQTT message, the error text.
   Do not assert internals.
3. No hardware, no network: Lua harnesses in `pkg/lua/*_test.go`, the sandbox in `pkg/mcp`
   (virtual clock, recorded CLU calls), `httptest` servers, `t.TempDir()`.
4. Shared counters in Lua go in a table, see `pkg/lua/AGENTS.md`.
5. Prove the test guards something: break the code it covers, see the test fail, put the code back.
   Mention this in the PR.

## Check

- `go test -count=1 -run YourTest ./pkg/x` passes, and fails with the code broken (step 5).
- `go test -race ./pkg/x` passes.
- If an existing contract had to change: the PR description starts with `Contract change:` and
  explains how existing scripts migrate.
