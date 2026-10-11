# #7: EventBus warns when callbacks for one event pile up

- **Type:** chore
- **Area:** lua-runtime
- **Size:** S
- **Epic:** —
- **Blocked by:** —
- **Skill:** —
- **Needs hardware:** —

> State (who, status) lives in the GitHub issue, not here. To take the ticket, ask in the issue;
> a maintainer assigns it. This file is the specification: change it by PR.

## Goal
Code that registers a callback in a loop or on every reload without removing it is visible in the
log before it slows the house down.

## Context
`pkg/lua/runtime/utils/EventBusGo.lua`: `EventBus:on` appends to `self.callbacks[eventName]`
with no limit; `emit` runs all of them. A plugin or module that calls `on` in a timer, or on each
re-create, accumulates callbacks for ever.

## Files
- `pkg/lua/runtime/utils/EventBusGo.lua`; a test in `pkg/lua/`

## Acceptance criteria
- [ ] Above a threshold (e.g. 100 callbacks for one event name) `on` logs one warning with the
  event name and the count; again only when the count doubles.
- [ ] Callbacks are still added (no behaviour change besides the log).
- [ ] The threshold is a constant in one place; a real installation's busiest event stays well below it
  (state it in the PR).

## Contract impact
None.
