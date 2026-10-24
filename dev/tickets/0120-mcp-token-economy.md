# #0120: MCP tools answer with what the agent needs, not everything

- **Type:** feature
- **Area:** mcp
- **Size:** M
- **Epic:** —
- **Blocked by:** —
- **Skill:** —
- **Needs hardware:** —

> State (who, status) lives in the GitHub issue, not here. To take the ticket, ask in the issue;
> a maintainer assigns it. This file is the specification: change it by PR.

## Goal
An agent working on a house through MCP spends fewer tokens on the same task: it edits a module by
sending the change, not the whole file, and reads short answers unless it asks for more.

## Context
vCLU is built to be programmed by agents, and every byte a tool returns or takes is paid for in
tokens. One evening of dashboard work on a real installation (2026-10-24) showed where they go:

- **Whole files on every edit.** `vclu_draft_write` (`pkg/mcp/tools_sandbox.go`) takes only the full
  content. Adding one `require` line to `modules/init.lua` meant reading the file (~3.5 KB, as
  escaped JSON) and writing it back whole; a 7 KB dashboard module went back and forth several times.
- **Logs full of DEBUG.** `vclu_logs` (`pkg/mcp/tools_dev.go`) returns 100 entries by default with
  every level, mostly MQTT publishes and thermostat polling; `level` and `filter` exist but an agent
  has to know to use them, and `lines` up to 1000 makes a mistake expensive.
- **All dashboards with all widgets.** `vclu_dashboards` (`pkg/mcp/tool_dashboards.go`) has no
  parameters: checking that one dashboard is gone returned ~200 widgets of 13 dashboards.
- **Whole files to read one function.** `vclu_read_file` has no offset or line range.
- **Boilerplate in every sandbox run.** `vclu_draft_test` returns the runtime's start-up lines
  (core loaded, CLUs registered) in `logs` on every run, also when the test passes.

Agents also burn tokens on large review/implementation subagents; that is out of scope here.

## Proposal (the implementer may refine; keep each tool backwards compatible)
1. `vclu_draft_edit(path, old_string, new_string, replace_all?)`: exact replacement in a draft,
   failing when `old_string` is missing or not unique (as Claude Code's Edit). When the draft does not
   exist yet, it starts from the production file of the same path. Returns a short confirmation and
   the size, not the file.
2. `vclu_logs`: default `level` info (DEBUG only when asked) and a smaller default `lines` (e.g. 50);
   say in the description how to get DEBUG. Consider collapsing repeated identical lines
   (`… ×12`).
3. `vclu_dashboards`: without arguments a list (`id`, `name`, `auth`, `widgetCount`); `id` returns
   that dashboard's widgets.
4. `vclu_read_file`: `offset` and `limit` in lines, and the total line count in the answer.
5. `vclu_draft_test`: leave out the runtime's own start-up lines from `logs` (keep the module's lines,
   warnings and errors); a flag `verbose` brings them back.

## Files
- `pkg/mcp/tools_sandbox.go`, `pkg/mcp/tools_dev.go`, `pkg/mcp/tool_dashboards.go`
- `pkg/mcp/*_test.go`; the agent docs that describe these tools (`docs/ai/`, MCP server instructions)

## Acceptance criteria
- [ ] `vclu_draft_edit` replaces a unique string, refuses a missing or repeated one without changing
      the draft, and starts from the production file when there is no draft (tests).
- [ ] `vclu_logs` without arguments has no DEBUG entries and returns at most the new default (test).
- [ ] `vclu_dashboards` without arguments has no widget lists; with `id` it has them (test).
- [ ] `vclu_read_file` with `offset`/`limit` returns those lines and the total (test).
- [ ] `vclu_draft_test` on a passing module returns no runtime start-up lines unless `verbose` (test).
- [ ] A short measurement in the PR: answer sizes in bytes before and after for each tool on a
      realistic workspace (e.g. the sandbox fixtures).
- [ ] `go test ./...` and `go test -race ./...` pass; a changelog entry.

## Contract impact
MCP tool answers are read by agents: changed defaults (`vclu_logs`, `vclu_dashboards`) are a
behaviour change; note it in the changelog. New tool and parameters are additive.
