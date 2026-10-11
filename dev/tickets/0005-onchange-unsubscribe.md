# #5: expose() warns when onChange() returns no unsubscribe function

- **Type:** bug
- **Area:** lua-runtime
- **Size:** S
- **Epic:** —
- **Blocked by:** —
- **Skill:** `.agents/skills/write-contract-test`
- **Needs hardware:** —

> State (who, status) lives in the GitHub issue, not here. To take the ticket, ask in the issue;
> a maintainer assigns it. This file is the specification: change it by PR.

## Goal
An object whose `onChange()` returns nothing is noticed when it is exposed, instead of leaking its
callback after `unexpose()`.

## Context
`pkg/lua/runtime/core/ExposedObjects.lua` (in `expose`): `obj:onChange(cb)` is called and its
result kept as `handle._unsubscribe` only if it is a function; `unexpose` calls it. Core objects
return one (`RemoteObject`, `VirtualObject`, `DOUT`, `DIN`). Objects written in plugins or
modules may not; then `unexpose` leaves the callback registered and it keeps running.

## Files
- `pkg/lua/runtime/core/ExposedObjects.lua`
- the object API reference (onChange must return an unsubscribe function)
- a test in `pkg/lua/`

## Acceptance criteria
- [ ] Exposing an object whose `onChange` returns a non-function logs one warning with the path,
  once per object.
- [ ] Core objects produce no warning (test exposes each kind).
- [ ] The requirement is in the API reference for custom objects.

## Contract impact
None changed; a warning only.
