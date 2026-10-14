# pkg/lua: Lua runtime

The VM is gopher-lua. Runtime files live in `runtime/`; Go glue and tests in this directory.

## gopher-lua is Lua 5.1, with its own quirks
No `string.pack`, no `bit32`, no `//`. `math.huge` is the largest float, not infinity
(`math.huge * 2 == math.huge` is false). A number is finite when `v - v == 0`; NaN alone is
`v ~= v`. Never compare with `math.huge`. See `runtime/utils/JSON.lua`.

## A caught error can split a closure's shared variable
In gopher-lua, when `pcall` catches an error, the running function's open upvalues are closed too
early: closures created before and after no longer share the variable.
`local n=0; local f=function() n=n+1 end; pcall(function() error("x") end); f(); return n` gives 0.
Keep shared mutable state in a table (`c.n = c.n + 1`), in tests and in modules.

## Load order in the bootstrap matters
`runtime/bootstrap_go.lua` loads files in a fixed order; later files use globals set by earlier
ones. A wrong place shows as `attempt to index a nil value` at startup. After adding or moving a
runtime file, start vCLU and read the log, not only the tests.

## State changes go through StateBus
`runtime/utils/StateBus.lua` gives every change its `origin` and `actor` and delivers it to
history, HomeKit, Home Assistant and MQTT. Never publish state to an integration directly; change
the object. Contract: `state_change_test.go`, `state_context_test.go`.

## The Lua API is a contract, keep it small
User scripts and agents depend on every public function in `runtime/`. Build a feature from
existing functions first. Mechanisms belong in the core (watch, persist, history); ready-made
policies belong in libraries. Changing a signature needs a migration and a `Contract change:` note.

## Test harnesses
`newPersistEngine` (`persist_test.go`), `newHistoryEngine` with a step clock (`history_test.go`),
an `httptest` stand-in for httpbin (`fake_httpbin_test.go`). Reuse them.

## runtime/grenton: talking to Grenton CLUs

### The CLU drops bursts of UDP messages without a word
A CLU drops packets when it gets several `clientRegister` calls back to back; roughly the first
4-5 of a burst arrive. A subscription whose renewal was lost expires after its TTL (60 s) and the
CLU stops reporting; nothing tells vCLU. Space every series of messages to a CLU by 30-100 ms
(`batchDelay` in `runtime/grenton/protocol/RemoteSubscription.lua`) without blocking the run loop. A dead
subscription shows as no `[CLIENT-REPORT]` from that session in the log.

### A lost report is corrected by the renewal, not by a push
A change is reported with one UDP packet and no retry. The CLU answers every renewal
(`clientRegister`, every TTL/2) with the session's current values; `SendCommandNoReply` applies that
answer like a report. Keep renewals going through it, or a lost report stays wrong until another
object in the session changes. Contract: `pkg/server/renew_reply_test.go`.

### Values and scales
A dimmer's value is 0..1, a digital output is 1/0. HomeKit and Home Assistant count brightness
0..100, HomeKit counts 100 = open while vCLU counts 0 = open. Convert at the integration edge and
test both directions.

### Untested on hardware
`RemoteLight` (dimmer) and `RemoteCover` (roller) in `runtime/objects/` follow the documentation, not a
real module. Say so in the PR when you change them; a hardware report is welcome.

## runtime/integrations: Home Assistant, MQTT, HTTP

### Read-only and hidden must hold on commands, not only in discovery
Discovery and command handling are separate paths. An object hidden from an integration must also
refuse a command sent to its topic. Check access where the command executes, with the path as the
object is registered, and add a test that sends the command. Contract: `pkg/lua/access_*_test.go`,
`pkg/lua/ha_*readonly_test.go`.

### A write through the generic path skips an object's own methods
`ExposedHandle:setValue` ends in `obj:set(0, v)` (`runtime/core/ExposedObjects.lua`). Objects whose real
action is a method (a dimmer's `setBrightness`, a cover's `setPosition`, Satel arming) need that
method called. Map the command to the method when the object has one.

### Scales
Home Assistant sends brightness 0..100 (`brightness_scale`); a dimmer's value is 0..1; an on/off
light has no brightness at all. See `runtime/integrations/homeassistant/Commands.lua`.
