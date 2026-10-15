# #39: Remote access has its own access profile, closed by default

- **Type:** feature
- **Area:** access-control
- **Size:** M
- **Epic:** —
- **Blocked by:** —
- **Skill:** `.agents/skills/write-contract-test`
- **Needs hardware:** —

> State (who, status) lives in the GitHub issue, not here. To take the ticket, ask in the issue;
> a maintainer assigns it. This file is the specification: change it by PR.

## Goal
Access from outside the local network (viewing and controlling the house remotely) goes through a
`remote` integration with its own access profile: nothing is visible until the owner allows it,
`readonly` objects can be seen but not controlled, and vCLU itself refuses what the profile forbids.

## Context
Access profiles (`pkg/accesscontrol/manager.go`, `runtime/core/AccessControl.lua`) cover three
integrations: `mqtt`, `homekit` and `mcp`. Each gets `full`, `readonly` or `hidden` per object, and
`full-access` is assigned to every integration by default (`Load`, "Ensure every integration has
at least one assignment").

Remote access is a fourth consumer with a different risk: it reaches the house from the internet.
It differs from the other three in two ways:

- **Closed by default.** A new `remote` integration gets a built-in profile whose default is
  `hidden` (e.g. `no-access`), on a fresh install and when an existing `access_control.yaml`
  is loaded. `full-access` is never assigned to it implicitly.
- **The remote side is not trusted.** What it may see and do is decided in vCLU, where the command
  executes, as for MQTT (`pkg/lua/AGENTS.md`, "Read-only and hidden must hold on commands, not
  only in discovery"). A forged command for a `hidden` or `readonly` object is refused even if the
  remote side never received that object.

What remote access sees is the exposed objects (`expose()`, the expose registry), filtered by the
`remote` profile, the same set Home Assistant and HomeKit work from. Lua dashboards are out of
scope: a widget is a pair of functions, and a profile cannot see which object they touch (a later
ticket can bind widgets to objects).

A command from remote access changes state with `origin = "remote"` and the actor of the person
who sent it (`user:<login>`), so history shows who changed what from outside.

This ticket adds the profile and the enforcement point with tests. It does not add a network
connector; that is a separate ticket that uses this one.

## Files
- `pkg/accesscontrol/manager.go`: `IntegrationRemote`, built-in hidden profile, default assignment.
- `pkg/web/handle_web_access.go`, `web/templates/access*.html`, translations: the integration in
  the Access page and its validation (today a hard-coded list of three).
- `pkg/lua/runtime/core/AccessControl.lua`: `remote` in `INTEGRATIONS`.
- a small Lua module next to the integrations (e.g. `runtime/integrations/remote/`) with two
  functions: the visible exposed objects with their access level, and a command entry that checks
  the profile and calls the object's own method (`pkg/lua/AGENTS.md`, "A write through the generic
  path skips an object's own methods"); reuse the mapping in `homeassistant/Commands.lua` where it fits.
- tests in `pkg/accesscontrol/` and `pkg/lua/` (`access_remote_test.go`).

## Acceptance criteria
- [ ] On a fresh install and on loading an existing `access_control.yaml` without a `remote`
  assignment, `remote` gets the built-in hidden profile, not `full-access`; existing assignments
  of `mqtt`, `homekit` and `mcp` are unchanged.
- [ ] The Access page lists `remote` and lets the owner assign profiles to it; the API accepts it.
- [ ] The object list for remote access contains only exposed objects that are not `hidden` for
  `remote`, each with its level.
- [ ] A command for a `hidden` or `readonly` object is refused and changes nothing; a command for a
  `full` object calls its own method (`switchOn`, `setPosition`, ...) and records
  `origin = "remote"` and the given actor.
- [ ] Changing the profile takes effect without a restart, like the other integrations.
- [ ] Tests fail on the current code; `go test ./...` and `go test -race ./...` pass.
- [ ] CHANGELOG entry in "Unreleased".

## Contract impact
Access control: a new integration and a new `origin` value (`remote`). Existing integrations and
their defaults do not change. Say so in the CHANGELOG.
