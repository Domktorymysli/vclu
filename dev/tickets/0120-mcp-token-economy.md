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
tokens. One evening of dashboard work on a real installation showed where they go:

- **Whole files on every edit.** `vclu_draft_write` (`pkg/mcp/tools_sandbox.go`) takes only the full
  content. Adding one `require` line to `modules/init.lua` meant reading the file (~3.5 KB, as
  escaped JSON) and writing it back whole; a 7 KB dashboard module went back and forth several times.
- **Logs full of DEBUG.** `vclu_logs` (`pkg/mcp/tools_dev.go`) returns 100 entries by default with
  every level, mostly MQTT publishes and thermostat polling; `level` and `filter` exist but an agent
  has to know to use them, and `limit` up to 1000 makes a mistake expensive.
- **All dashboards with all widgets.** `vclu_dashboards` (`pkg/mcp/tool_dashboards.go`) has no
  parameters and returns every widget of every dashboard: on a real installation a dozen dashboards
  come to an answer in the tens of KB, also when the agent only checks that one is gone.
- **Whole files to read one function.** `vclu_read_file` and `vclu_draft_read` have no offset or
  line range.
- **Boilerplate in every sandbox run.** `vclu_draft_test` returns the runtime's start-up lines
  (core loaded, CLUs registered) in `logs` on every run, also when the test passes. They are
  `print` calls during the bootstrap, caught by the sandbox `print` into `result.Logs`
  (`pkg/mcp/sandbox.go`, `load()`), and they count against `maxSandboxLogLines`, so they also push
  out the module's own lines.

Agents also burn tokens on large review/implementation subagents; that is out of scope here.

## Proposal (the implementer may refine; keep each tool backwards compatible)
1. `vclu_draft_edit(path, old_string, new_string, replace_all?)`: exact replacement in a draft,
   failing when `old_string` is missing or not unique (as Claude Code's Edit). When the draft does not
   exist yet, it starts from the production file of the same path (`workspace.GetProductionContent`;
   note it returns an empty string, not an error, for a missing file). The path is checked as in
   `CreateDraft` (`modules/` only). Returns a short confirmation and the size, not the file, and
   writes an audit entry as `draft_write` does.
2. `vclu_logs`: default `level` info (DEBUG only when asked) and a smaller default `limit` (e.g. 50).
   An agent debugging its own module passes `level: "debug"`, because `Logger:debug` is hidden by
   default; the tool description says so. Consider collapsing repeated identical lines (`… ×12`).
3. `vclu_dashboards`: without arguments a list (`id`, `name`, `auth`, `widgetCount`); `id` returns
   that dashboard's widgets. The filtering is done in Go in `pkg/mcp/tool_dashboards.go`;
   `DashboardAPI.list` in `pkg/lua/runtime/core/Dashboard.lua` does not change, because the web
   uses it too (`/dashboards` and the raw JSON of `/api/dashboards` in
   `pkg/web/handle_web_dashboard.go`).
4. `vclu_read_file`: `offset` (1-based line number) and `limit` in lines, and the total line count
   in the answer; a call without them returns the whole file as today.
5. `vclu_draft_test`: leave out the runtime's own start-up lines from `logs`, cut by phase:
   everything printed before the drafts start to load (in each load) is dropped, not matched by
   regex. Keep the module's lines, warnings and errors; a flag `verbose` brings the start-up lines
   back.
6. Every tool refuses an argument its schema does not name, and a missing required one, instead
   of ignoring it: `vclu_logs` got `search`/`lines` (it takes `filter`/`limit`), `vclu_exec` got
   `code` (it takes `script`) and answered as if they were not there. One check where tools are
   registered (`addTool` in `pkg/mcp/tools.go`) compares the names with the schema and answers
   `unknown argument "search" for vclu_logs; did you mean "filter"? Valid: filter, level, limit`,
   with a suggestion for a known alias or a near miss.

## Not in scope
- `offset`/`limit` for `vclu_draft_read`: drafts are the agent's own recent writes; add it only if
  the measurement shows it is needed.

## Files
- `pkg/mcp/tools_sandbox.go`, `pkg/mcp/tools_dev.go`, `pkg/mcp/tool_dashboards.go`, `pkg/mcp/sandbox.go`, `pkg/mcp/tools.go` (argument check)
- `pkg/mcp/server.go`: the server instructions (the "Sandbox pipeline" paragraph gets
  `vclu_draft_edit`; "Live execution" and pitfall 5 say `vclu_dashboards` lists dashboards and
  `id` gives the widgets)
- `pkg/mcp/tools_dev.go`: the descriptions of `vclu_logs` (default level) and `vclu_dashboards`
- `web/templates/mcp.html`: the pipeline `vclu_draft_write → …` in the agent guide, with
  `vclu_draft_edit`
- `pkg/mcp/*_test.go`: `TestHandleDashboards_Success` in `pkg/mcp/tools_phase2_test.go` assumes
  the answer holds widgets and is updated
- `CONTRIBUTING.md`: the "Dashboard room tiles" row
- `changelog.d/0120-mcp-token-economy.md`

## Acceptance criteria
- [ ] `vclu_draft_edit` replaces a unique string, refuses a missing or repeated one without changing
      the draft, starts from the production file when there is no draft, fails when neither the
      draft nor the production file exists, refuses a path outside `modules/` (as `vclu_draft_write`),
      answers without the file content and leaves an audit entry (tests).
- [ ] `vclu_logs` without arguments has no DEBUG entries and returns at most the new default; with
      `level: "debug"` it has them (contract test).
- [ ] `vclu_dashboards` without arguments has no widget lists; with `id` it has that dashboard's
      widgets, a room tile and not its room's widgets; an unknown `id` is an error (contract test).
- [ ] `vclu_read_file` with `offset`/`limit` returns those lines and the total; without them it
      returns the whole file as before (test).
- [ ] `vclu_draft_test` on a passing module returns no runtime start-up lines unless `verbose`, and
      keeps the module's own `print` lines (test).
- [ ] An unknown argument is an error that names the valid ones, with a suggestion for a near
      miss (`search` → `filter`); a missing required argument is an error; valid arguments pass; a
      tool without parameters refuses any argument; every registered tool has its schema (tests).
- [ ] The web `/dashboards` page and `/api/dashboards` answer as before (existing tests pass).
- [ ] A short measurement in the PR: answer sizes in bytes before and after for each tool on a
      realistic workspace (e.g. the sandbox fixtures).
- [ ] `go test ./...` and `go test -race ./...` pass; a changelog entry in
      `changelog.d/0120-mcp-token-economy.md`.

## Contract impact
"Dashboard room tiles" (CONTRIBUTING.md) says the `vclu_dashboards` list shows the tile, not its
room's widgets. With this ticket the list without arguments has no widgets at all; the row is
updated to say the widgets come with `id` and still show the tile, not its room's widgets. The
test of that row (`TestDashboardRoomList` in `pkg/lua/dashboard_room_test.go`) checks
`Dashboard.API.list()`, which does not change, so it stays. The PR starts with `Contract change:`:
an agent that wants widgets passes `id`. No decision record: it is the shape of one tool's answer,
not a design choice other code builds on.

The new defaults of `vclu_logs` and `vclu_dashboards` are a behaviour change agents see: they get
contract tests and a changelog entry. The new tool and parameters are additive. Refusing unknown
arguments is a behaviour change too: a call that passed an argument the tool ignored now fails and
says which name to use.
