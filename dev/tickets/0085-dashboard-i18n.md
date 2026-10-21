# #85: Dashboards: widgets in the viewer's language (i18n)

- **Type:** feature
- **Area:** web
- **Size:** M
- **Epic:** —
- **Blocked by:** #76 (the calendar widget adds most of the strings)
- **Skill:** —
- **Needs hardware:** —

> State (who, status) lives in the GitHub issue, not here. To take the ticket, ask in the issue;
> a maintainer assigns it. This file is the specification: change it by PR.

## Goal
Every text a dashboard widget draws itself comes from `web/locales/`, in the language vCLU chose
for the viewer, so a dashboard reads in one language.

## Context
- CONTRIBUTING.md: user-facing strings use the i18n system (`web/locales/en.json`, `pl.json`).
  The admin pages do; the public dashboard page `/d/:id` does not: `handleDashboardPage`
  (`pkg/web/handle_web_dashboard.go`) passes no language and no translations to `dashboard.html`.
- `languageMiddleware` (`pkg/web/server.go`) already picks the language for every request,
  `/d/:id` included: `?lang=`, then the cookie, then `Accept-Language`.
- `web/static/js/dashboard.js` hard-codes English (`'Colour'`, `'Brightness'`, `'White'` on `main`)
  and, in the calendar widget (#76), Polish or English by `navigator.language` (Today, Month, Day,
  the show menu, polar day/night, "No connection"). Month and weekday names come from `Intl` and
  stay so, with the page's language instead of the browser's.
- Labels the author writes in Lua (`:calendar('k', 'Kalendarz', ...)`, event titles) are content,
  not translated.

## Files
- `pkg/web/handle_web_dashboard.go`, `web/templates/dashboard.html` (`lang`, a `dashboard` section
  of the translations for the script)
- `web/locales/en.json`, `web/locales/pl.json` (a `dashboard` section)
- `web/static/js/dashboard.js`

## Acceptance criteria
- [ ] `/d/:id?lang=pl` and `?lang=en` draw every widget text the script writes in that language;
  `<html lang>` and `Intl` formats follow it (a test on the rendered page and the served strings).
- [ ] No `navigator.language` and no user-facing string literal left in `dashboard.js` (a test
  greps the file for the known strings, so a new one is noticed).
- [ ] `en.json` and `pl.json` have the same `dashboard` keys (a test).
- [ ] Checked by hand at 390 px and on a desktop in both languages.
- [ ] `go test ./...` and `go test -race ./...` pass.
- [ ] A changelog entry in `changelog.d/0085-dashboard-i18n.md`.

## Contract impact
None: a dashboard's Lua API and JSON do not change.
