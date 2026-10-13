# #17: Docs: connecting another system to vCLU (MQTT or MCP)

- **Type:** docs
- **Area:** mqtt
- **Size:** S
- **Epic:** —
- **Blocked by:** —
- **Skill:** —
- **Needs hardware:** —

> State (who, status) lives in the GitHub issue, not here. To take the ticket, ask in the issue;
> a maintainer assigns it. This file is the specification: change it by PR.

## Goal
Someone writing an integration for another system (a dashboard, another hub) finds in one page how
to read states and send commands, and which interface to use.

## Context
The first external integration polls `vclu_devices` over MCP every 5 s, because the README presents
MCP as the API and the REST API is read-only for devices. The MQTT interface already gives pushed
states and commands for every exposed object, but the README describes it only as Home Assistant
support.

Topics: discovery `homeassistant/<component>/<id>/config`, availability `vclu/status`, commands
`vclu/exposed/<base>/<component>/<id>[/<subpath>]/set`
(`pkg/lua/runtime/integrations/homeassistant/Commands.lua`, `StatePublisher.lua`). Access profiles
apply to MQTT: readonly objects are published without a command topic (`READONLY_REMAP` in
`StatePublisher.lua`); check what happens to hidden ones before documenting it.

## Files
- `docs/integrating.md` (new, English)
- `README.md`, `README.en.md`: one sentence with a link

## Acceptance criteria
- [ ] The page says when to use MQTT (programs: pushed states, commands) and when MCP (agents,
  writing automations), and that the REST API does not control devices.
- [ ] MQTT: topic layout for state, commands and availability, payloads for switch, light/dimmer
  and cover, and an example with `mosquitto_sub`/`mosquitto_pub`, checked against a running vCLU.
- [ ] Access: how a profile limits what the integration sees and controls, over MQTT and MCP.
- [ ] MCP: endpoint, bot token, the five device and scene tools, a link to the contract test.
- [ ] Both READMEs link to the page.

## Contract impact
None. It documents existing behaviour; if the docs and the code disagree, open a bug.
