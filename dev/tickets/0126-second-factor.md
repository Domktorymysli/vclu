# #0126: Confirmation with a second factor in the core: authenticator app codes first, other methods later

- **Type:** feature
- **Area:** access-control
- **Size:** L
- **Epic:** —
- **Blocked by:** —
- **Skill:** —
- **Needs hardware:** —

> State (who, status) lives in the GitHub issue, not here. To take the ticket, ask in the issue;
> a maintainer assigns it. This file is the specification: change it by PR.

## Goal
vCLU can ask a person to confirm an action with a second factor, and the first method works: a code
from an authenticator app (Google Authenticator, Microsoft Authenticator, any RFC 6238 app). The
mechanism is generic: other methods, and other places that ask for a factor, plug into it without
changing its interface.

## Context
Some actions should not happen just because a message or a click arrived: disarming the alarm from
a chat bot is the first case (a module with the Telegram plugin, `allowDisarm` in the Satel
integration). A chat account can be taken over, a phone lent; a code that changes every 30 seconds
stops that, and unlike a PIN it can stay in the chat history.

Today vCLU has one panel user (`Auth.Username`/`PasswordHash` in `.vclu.json`, `pkg/config/auth.go`)
and access profiles for integrations (`pkg/accesscontrol`). Nothing asks for a second factor.

### Design: methods, not vendors
Google and Microsoft Authenticator are the same method (TOTP); "provider" in this ticket means a
**method**. The first method is `totp`; later ones (recovery codes, passkeys/WebAuthn, a push to an
app) must fit the same two-step shape, so the interface is challenge and response, not "check a
6-digit code":

```go
// pkg/auth/factor (name may be refined)
type Method interface {
    ID() string                                                    // "totp"
    BeginEnroll(user string) (Enrollment, error)                   // what to show: otpauth URI for a QR
    FinishEnroll(e Enrollment, response string) (Factor, error)    // first code proves the app has it
    Begin(f Factor, c *Challenge) (Prompt, error)                  // TOTP: "enter the code"; push: send it
    Verify(f Factor, c *Challenge, response string) error          // one attempt
}

type Service interface {
    Factors(user string) []FactorInfo                 // id, method, label, created, last used; never secrets
    Challenge(r Request) (*Challenge, error)          // Request{User, Action, Reason, Channel, TTL}
    Complete(challengeID, response string) (*Grant, error)
    Consume(grantID, user, action string) error        // single use, bound to user and action
}
```

- **A challenge names the action** (`satel.disarm:1`) and a reason a person reads ("Disarm: ground
  floor"). A successful response gives a **grant**: one use, short-lived (about 60 s), bound to that
  user and action. Today a Lua module checks the grant itself; later the core can refuse a protected
  action without a grant (`Consume`), with no change to this interface.
- **Users are keyed by the vCLU user name**, even with one user today. Mapping a channel identity (a
  Telegram chat) to a vCLU user is the consumer's job, not the mechanism's.
- **The channel** (`telegram`, `web`, `lua`, `mcp`) is recorded for the audit, not trusted for
  anything.

### TOTP
- RFC 6238 with the parameters every app supports: SHA-1, 6 digits, 30 s, a 20-byte random secret in
  base32; accept the step before and after; `otpauth://totp/vCLU:<user>?secret=…&issuer=vCLU` for the
  QR, plus the secret as text for typing in.
- **No replay:** a time step that was used once is refused for that factor.
- **Clock:** codes depend on the time; a Raspberry Pi without a battery starts with a wrong clock.
  While `calendar.ClockTrusted` (`pkg/calendar/clock.go`) says no, `totp` refuses with its own error
  instead of failing every code.
- **Limits:** 5 wrong answers per user in a row lock confirmation for 15 minutes, across challenges and
  channels; the lock and its end are in the audit.

### Where the secrets live
- A separate file in the data directory, mode 0600, never in `.vclu.json` (which the panel and tools
  read often); added to the MCP block list next to `.vclu.json` and `keys.json`
  (`mcpAllowedPath`, `pkg/mcp/file_provider.go`) and kept out of the panel's file editor, logs,
  history and every API answer.
- **Threat model, stated in the docs:** this protects against someone who reaches vCLU through a
  channel (a chat, a dashboard, an API key). It does not protect against code that already runs in
  vCLU: Lua modules have `io` and `os` and run as the vCLU process, so a module, `vclu_exec` or an
  agent allowed to promote modules can read the file. Encrypting it with a key in the same place
  would not change that and is not asked for.

### Enrolling and removing
In the panel, logged in (Settings, next to the password): add an authenticator (QR, secret as text,
a label such as "Phone"), confirmed by the first code; a list of factors with the last use; remove a
factor after confirming with the password. Never through a chat or a module: the secret is shown in
the panel only, once.

### Lua API
Small, and the same for any method:

```lua
local ch, err = Auth.challenge({ user = "admin", action = "satel.disarm:1",
    reason = "Rozbrojenie: parter", channel = "telegram" })
-- ch.id, ch.prompt (text to show), ch.method ("totp"), ch.expiresAt; err: "no_factor"
local grant, err = Auth.complete(ch.id, code)
-- err: "invalid", "expired", "locked", "clock_untrusted"
if grant then ... end
```

A challenge expires after 2 minutes. The response passes through Lua (it comes from a chat), the
secret never does. In the sandbox `Auth` answers from fixtures, so a module's flow can be tested
without a real app.

### Audit
History entries for enrol, remove, challenge passed, failed and locked: user, method, action,
channel; never the code or the secret.

### Not in this ticket (follow-ups, each its own ticket)
A second factor at panel login; recovery codes; confirmation for MCP tool calls (an agent asks a
person); refusing protected actions without a grant in the core (Satel disarm); passkeys; the flow in
the Telegram plugin (plugins repository). The interface above is meant to carry all of them; a
decision record fixes it before they start.

## Files
- `pkg/auth/` (new: service, `totp` method, store), `pkg/web/` (settings page and handlers),
  `pkg/lua/` (the `Auth` binding), `pkg/mcp/file_provider.go` (block list), `pkg/history/`
- `dev/decisions/` (a record for the method interface and the Lua API)
- docs for agents (`docs/ai/`, runtime schema) and `changelog.d/`

## Acceptance criteria
- [ ] `totp` passes the RFC 6238 test vectors; a code from the step before and after is accepted, two
      steps away is refused (tests with a fixed clock).
- [ ] The same code is refused the second time; a challenge is refused after it expires; a grant
      works once and only for its user and action (tests).
- [ ] 5 wrong answers lock confirmation for 15 minutes across challenges (test with a fixed clock).
- [ ] With an untrusted clock `Auth.complete` returns `clock_untrusted` and accepts nothing (test).
- [ ] Enrolment in the panel: a QR that Google Authenticator and Microsoft Authenticator both read,
      checked on a phone and noted in the PR; the first code confirms it.
- [ ] The secret is in no API answer, log line, history entry or MCP tool result, and
      `vclu_read_file`/`vclu_grep` refuse the file (tests).
- [ ] A sandbox test of a module that asks for a code and disarms only after a grant.
- [ ] A decision record for the interface; the threat model in the docs.
- [ ] `go test ./...` and `go test -race ./...` pass; a changelog entry.

## Contract impact
New Lua API (`Auth`) and a new file in the data directory: both additive. The method interface is
what later work builds on, hence the decision record. Nothing existing changes behaviour.
