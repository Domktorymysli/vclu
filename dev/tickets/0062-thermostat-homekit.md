# #62: An exposed thermostat is a Thermostat in Apple Home

- **Type:** feature
- **Area:** homekit
- **Size:** M
- **Epic:** —
- **Blocked by:** #61 (thermostat in Home Assistant: defines the `climate` expose type and the thermostat object interface)
- **Skill:** `.agents/skills/write-contract-test`
- **Needs hardware:** — (a check with a Grenton CLU thermostat and the Home app is welcome)

> State (who, status) lives in the GitHub issue, not here. To take the ticket, ask in the issue;
> a maintainer assigns it. This file is the specification: change it by PR.

## Goal
A thermostat exposed as `climate` shows in the Home app as a Thermostat accessory with its current
and target temperature; the target set in the Home app reaches it, unless it is read-only.

## Context
- `pkg/homekit/accessory_factory.go` leaves `climate` out on purpose ("a plain switch would let the
  Home app write 0/1"). HomeKit's Thermostat service needs CurrentTemperature, TargetTemperature,
  CurrentHeatingCoolingState, TargetHeatingCoolingState and TemperatureDisplayUnits.
- Use the object interface and the mode mapping defined in #61; keep the two
  integrations in agreement (same target, same off/heat meaning).
- Read-only: HomeKit has no read-only Thermostat. Options: a TemperatureSensor plus nothing to set,
  or a Thermostat whose writes are refused and reverted. Decide, and test the command path
  (`pkg/homekit/lua_command_test.go`).
- TargetTemperature has a range and a step in HomeKit; take them from the expose options
  (`min`, `max`, `step`), within HomeKit's limits (10-38 °C by default).
- HomeKit is used daily on the maintainer's installation: accessory ids must stay stable
  (`aid_store.go`) and a rebuild must not drop other accessories.

## Files
- `pkg/homekit/accessory_factory.go` and its tests
- `docs` for HomeKit (docs.vclu.pl, integrations/homekit)

## Acceptance criteria
- [ ] An exposed `RemoteThermostat` becomes a Thermostat accessory; a test checks its characteristics after a CLU report.
- [ ] A target from the Home app calls `setSetpoint`; a read-only one does not; tests cover both.
- [ ] A change of the target alone (schedule, holiday mode) updates TargetTemperature.
- [ ] `go test ./...` and `go test -race ./...` pass.
- [ ] CHANGELOG entry.

## Contract impact
HomeKit accessory type for `climate`. None beyond the interface the HA ticket defines.
