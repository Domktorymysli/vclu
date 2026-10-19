# #66: Smart Panel checked on the panel: thermostat, proximity, photos

- **Type:** test
- **Area:** grenton
- **Size:** S
- **Epic:** —
- **Blocked by:** —
- **Skill:** —
- **Needs hardware:** a Smart Panel (WiFi or on a CLU), Object Manager, a CLU with a Thermostat object

> State (who, status) lives in the GitHub issue, not here. To take the ticket, ask in the issue;
> a maintainer assigns it. This file is the specification: change it by PR.

## Goal
The parts of #44 that need a person at the panel are checked and written down.

## Context
#44 (PR #45, released in v1.6.0) runs on a Smart Panel WiFi (panel module fv05) since 2026-10-18:
menu, edits, corners, charts, history, idle, cleaning. Left from its acceptance criteria:
- A Grenton thermostat (`RemoteThermostat`, class 18) on the panel: `menu:thermostat`, read-only
  and in manual mode with `+`/`-`. Needs the OM project imported again (the import creates the
  thermostats) and the thermostat on the Sync list. Check that the set point reaches the CLU and
  the target follows, and that holiday/auto mode leaves the page read-only.
- Proximity: wire OnProximityDetect as `proximity`, measure from how far the panel reacts and how
  often the event repeats while someone stands in front of it (ProximitySens, ProximityTimeout).
  Record it in `pkg/lua/AGENTS.md` (Smart Panel section).
- Photos for the docs: the menu and edit mode on the panel; a screenshot of the Object Manager
  event (OnClick calling the vCLU script `click`) for docs.vclu.pl (integrations/smart-panel).
- Whether the per-minute redraw of a clock (`Panel.corners{ clock = true }`) wakes a display the
  panel blanked after ProximityTimeout.

## Files
- `pkg/lua/AGENTS.md`, docs.vclu.pl `content/docs/integrations/smart-panel.md` and the Smart Panel tutorial

## Acceptance criteria
- [ ] Thermostat checked on a real CLU (read-only, manual with `+`/`-`, auto/holiday read-only); findings in the issue.
- [ ] Proximity range and repeat rate recorded in `pkg/lua/AGENTS.md`.
- [ ] Photos and the OM screenshot in the docs.
- [ ] The answer about the clock waking the display recorded (and a ticket if it does).

## Contract impact
None.
