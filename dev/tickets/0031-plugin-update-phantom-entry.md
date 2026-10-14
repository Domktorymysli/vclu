# #31: After "Update", the plugins page shows the plugin with no version and no configuration

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
After installing or updating a plugin, the plugins page shows that plugin once, under its full id,
with the new version and its saved configuration, without a page reload.

## Context
Installed plugins are keyed by their full id (`@vclu/salda-recuperator`) in the page
(`web/templates/plugins.html`, `installedPlugins['{{.ID}}']`). The Install/Update button on an
available plugin passes its short id (`salda-recuperator`). After `POST /api/plugins/install`,
`PluginsManager.install` (`web/static/js/plugins.js`) stores
`installedPlugins[<short id>] = { version: data.version || '?', config: {} }` and switches to the
Installed tab. That tab then shows a second entry for the plugin, with version `?` and "not
configured", and its config form is empty. Seen in production: the configuration in `.vclu.json`
is intact and the plugin keeps running on it; after a reload the extra entry is gone. Saving the
empty form posts to `/api/plugins/<short id>/config`, which the server refuses ("Plugin not
found"), so no data is lost, but the user sees an error.

Related:
- `handlePluginsAPIInstall` (`pkg/web/handle_web_plugins.go`) answers with `success`, `action`
  and `fullId`, but no `version`, hence `?`.
- `_renderInstalledPlugins` matches installed plugins to the catalog by `p.id`, which is the full
  id for installed plugins and the short id in the catalog, so the card can miss the description
  and the update indicator. The page also asks for the schema under both ids.

## Files
- `web/static/js/plugins.js`
- `pkg/web/handle_web_plugins.go`

## Acceptance criteria
- [ ] Install and update answer with `fullId` and `version`.
- [ ] After an update, the front updates the entry under `fullId`, keeping its `config` and
  `enabled`; no entry under the short id appears.
- [ ] After a first install, the new entry is under `fullId` with the version from the answer.
- [ ] The Installed tab matches installed plugins to the catalog by full id (description, update
  indicator).
- [ ] A Go test of `POST /api/plugins/install` for an installed plugin: the answer has `fullId`
  and `version`, and `.vclu.json` keeps the plugin's `config` and `enabled`.
- [ ] Checked in a browser on the local demo (`tools/demo/run.sh`): update a plugin, the Installed
  tab shows it once with its configuration.
- [ ] `go test ./...` passes.
- [ ] CHANGELOG entry: after updating a plugin, the plugins page no longer shows it as unconfigured.

## Contract impact
None.
