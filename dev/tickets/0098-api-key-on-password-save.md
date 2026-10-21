# #0098: Saving the password keeps the API key

- **Type:** bug
- **Area:** web
- **Size:** S
- **Epic:** —
- **Blocked by:** —
- **Skill:** —
- **Needs hardware:** —

> State (who, status) lives in the GitHub issue, not here. To take the ticket, ask in the issue;
> a maintainer assigns it. This file is the specification: change it by PR.

## Goal
Changing the username or password on Settings → Security leaves the API key as it was. The key
goes away only when it is revoked or authentication is turned off.

## Context
`config.SaveAuth` replaces the whole `auth` section with the username and the password hash, so
`apiKeyHash` is dropped. Every script or integration that calls the API with `X-API-Key` gets
401 after the password is saved, and the page does not say that the key was revoked. The wizard's
security step calls the same function, but there no key exists yet.
Found by the end-to-end suite (vclu-e2e `TestConfigEachSaveWritesOnlyItsSection`); it is the
same on #79.

## Files
- `pkg/config/auth.go` (`SaveAuth`)
- `pkg/web/handlers_forms.go` (`handleSecurityPost`)

## Acceptance criteria
- [ ] After saving the security form with auth on, the API key generated before still works.
- [ ] Turning auth off still clears the `auth` section, the key with it.
- [ ] Revoking the key (`DELETE /api/settings/api-key`) still removes only `apiKeyHash`.
