# #288: Plugin wizard: sign in with a QR code or a device code (plugin:login)

- **Type:** feature
- **Area:** plugins, web
- **Size:** M
- **Epic:** #247
- **Blocked by:** —
- **Skill:** `.agents/skills/write-contract-test`
- **Needs hardware:** — (a Tuya account linked to Smart Life for a final check, once vclu has its own Tuya client id)

> State (who, status) lives in the GitHub issue, not here. To take the ticket, ask in the issue;
> a maintainer assigns it. This file is the specification: change it by PR.

## Goal
A plugin signs the user in to its cloud from the wizard: the panel shows a QR code or a code with a
link, waits while the user confirms in a phone app, and the tokens the plugin gets are saved as its
secrets, e.g. `p:login(function(cfg, show, done) ... show({qr = text}) ... done({token = t}) end)`.

## Context
Many clouds sign in without a password typed into vclu: the user scans a QR code in the vendor's
app (Tuya/Smart Life, as in Home Assistant's integration) or opens a link and types a short code
(the OAuth 2.0 device authorization grant, RFC 8628: Google, Microsoft, Spotify, many others).
The plugin can already do the HTTP part (`p:httpRequest`, `permissions.internet`, `Crypto`), but
it cannot show anything to the user: the wizard (#247) has form fields (`string`, `number`,
`boolean`, `enum`, `secret`, `host`, `object`) and `discover`, no step that shows a code and waits.

The pieces exist:
- `plugin:discover(fn)` (`pkg/lua/runtime/core/PluginAPI.lua`) and `PluginSandbox:discover` /
  `takeDiscovered` (`PluginSandbox.lua`), driven by `pkg/plugins/wizard.go` and
  `pkg/web/handle_web_plugins_wizard.go`: a plugin call with a `done` callback the panel waits on.
  `login` follows the same shape, with one more callback, `show`, that can be called several times.
- Secrets: `pkg/plugins/secrets.go` (`SetPluginSecrets`), kept apart from `.vclu.json` and plugin
  storage, masked in the plugin's logs, never shown in the panel.
- QR rendering: `rsc.io/qr` is already a dependency (`pkg/web/clients_panel.go` makes a PNG data
  URI for client pairing). The QR image is made in Go from the text the plugin gives; the plugin
  sends text only.

Tuya's QR sign-in needs a client id Tuya registers for vclu; Home Assistant's must not be used.
This ticket is the generic step; it does not depend on that.

## API
- `p:login(fn)` registers the sign-in; `fn(cfg, show, done)`:
  - `show{qr = text}` shows a QR code of `text`; `show{code = "ABCD-1234", url = "https://..."}`
    shows a code and a link (the link opens in a new tab); `hint` is an optional line of text. A later
    `show` replaces the earlier one (a code that expired and was renewed).
  - `done(secrets)` ends the sign-in: `secrets` is a table of string fields saved with
    `SetPluginSecrets` and given to the plugin as its other secrets; `done(nil, err)` shows `err`.
- The panel: a "Sign in" button in the wizard when the plugin has `login`; the step shows what
  `show` gave, a spinner, "Cancel". At most one sign-in per plugin at a time; it ends after 5 minutes
  without `done`, or on "Cancel", and the plugin's callbacks after that do nothing.
- `text` is at most 1024 bytes; `url` is `https://` only; `code` and `hint` are short plain text.
- Not in this ticket: a QR widget on dashboards, QR from modules.

## Files
- `pkg/lua/runtime/core/PluginAPI.lua` (`Plugin:login`)
- `pkg/lua/runtime/core/PluginSandbox.lua` (start, `show` state, `done`, cancel, timeout)
- `pkg/plugins/wizard.go`, `pkg/plugins/secrets.go`
- `pkg/web/handle_web_plugins_wizard.go` (start, poll with the QR as a PNG data URI, cancel)
- `web/static/js/plugin-wizard.js`, `web/locales/en.json`, `web/locales/pl.json`
- vclu-plugins `DEVELOPMENT.md`; `docs` repo: the plugin wizard page

## Acceptance criteria
- [ ] A plugin with `login` gets a "Sign in" button; one without does not.
- [ ] `show{qr}` makes the panel show a QR that decodes to the same text; `show{code, url}` shows both; a second `show` replaces the first.
- [ ] `done(secrets)` saves them as the plugin's secrets: the plugin reads them, the panel and the API never return them, the plugin's logs mask them.
- [ ] `done(nil, err)` shows the error; a raised error in `fn` does the same.
- [ ] Cancel and the 5-minute timeout end the sign-in; a later `show` or `done` from the plugin changes nothing.
- [ ] A second "Sign in" while one is running is refused, not doubled.
- [ ] `text` over 1024 bytes, a `url` that is not `https://`, and `secrets` with non-string fields are refused with a clear error.
- [ ] A test plugin does an RFC 8628 device-code flow against an `httptest` server end to end.
- [ ] `go test ./...` and `go test -race ./...` pass.
- [ ] A changelog entry in `changelog.d/0288-plugin-login.md`.

## Contract impact
Plugin API: a new method `plugin:login`; wizard HTTP API: start/poll/cancel of a sign-in. Nothing
existing changes. No decision record needed.
