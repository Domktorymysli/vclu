# #157: A dashboard shows a table of rows from Lua

- **Type:** feature
- **Area:** web
- **Size:** M
- **Epic:** —
- **Blocked by:** —
- **Skill:** `.agents/skills/run-local-vclu`
- **Needs hardware:** —

> State (who, status) lives in the GitHub issue, not here. To take the ticket, ask in the issue;
> a maintainer assigns it. This file is the specification: change it by PR.

## Goal
`section:table(id, label, rows, opts)` shows rows from a Lua function in named columns, sortable on
the page and readable on a phone.

## Context
Several values side by side (rooms with temperature, humidity and a window; meters; zones) take a
status tile each today. The table is also the base for a history and an alarm zone list later.
The widget follows `section:forecast` in `pkg/lua/runtime/core/Dashboard.lua`: a constructor that
checks the arguments at build, a state function read by `getDefinition` and `getValues`, and
`dashboard.js` that redraws only when the rows changed.

## Files
- `pkg/lua/runtime/core/Dashboard.lua` (`tableWidget`, `tableState`, `SectionHandle:table`, `DashboardBuilder:table`)
- `pkg/lua/dashboard_table_test.go`
- `web/static/js/dashboard.js`, `web/static/css/dashboard.css`
- `pkg/mcp/tools_dev.go` (runtime schema), `docs/ai/examples.md`, `docs/ai/lua-api-reference.md`, `CONTRIBUTING.md`

## Acceptance criteria
- [ ] `opts.columns` is required: `{ key, label?, unit?, decimals? (0-6), align? ('left'|'right') }`; an unknown option or column key, a repeated key, a `sort` that is not a column or a `limit` outside 1-200 fail at the module's line (`TestDashboardTableArgumentErrors`).
- [ ] `rows()` is read on every poll; cells are numbers (NaN empty), text (a number in a column with `decimals`), booleans or nil; anything else, an error or a result that is not a list of tables is the tile's error (`TestDashboardTableErrors`).
- [ ] `sort` is ascending and stable with empty cells last, applied before `limit`; `desc = true` (a boolean, needs `sort`) sorts descending with empty cells still last, so `limit` keeps the highest rows; the page's header arrow starts in that direction; at most 200 rows are sent with the `total` (`TestDashboardTableSortLimitColor`, `TestDashboardTableSortDesc`, `TestDashboardTableSortMixed`, `TestDashboardTableEmptyAndCap`).
- [ ] `color(row, key)` colours a cell with a colour name, `#hex`, `rgb()` or `hsl()`, anything else is dropped; an error leaves the cell plain and is logged once (`TestDashboardTableColorError`).
- [ ] On the page: a header tap sorts (again: descending), numbers rounded with the unit and the viewer's decimal separator, ✓ / — for booleans, "brak danych" / "no data" without rows, cells set with `textContent`; at 600 px and below each row is a card.
- [ ] `go test ./...` and `go test -race ./...` pass.
- [ ] A changelog entry in `changelog.d/0157-dashboard-table.md`.

## Contract impact
A new public method on the dashboard builder (`section:table`), added to the contract table in
CONTRIBUTING.md. No existing behaviour changes.
