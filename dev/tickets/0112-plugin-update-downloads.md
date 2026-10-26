# #0112: Updating a plugin from the panel downloads the new version

- **Type:** bug
- **Area:** plugins
- **Size:** S
- **Epic:** —
- **Blocked by:** —
- **Skill:** —
- **Needs hardware:** —

> State (who, status) lives in the GitHub issue, not here. To take the ticket, ask in the issue;
> a maintainer assigns it. This file is the specification: change it by PR.

## Goal
After "Update" on the Plugins page and a reload, the runtime runs the version the page shows.

## Context
`handlePluginsAPIInstall` (`pkg/web/handle_web_plugins.go`) only writes the new `version` of an
installed plugin to `.vclu.json`. `Loader.LoadAllEnabled` (`pkg/plugins/loader.go`) downloads a
plugin only when `resolvePluginDir` finds no `plugin.json`, and it finds the old version through
`plugins.lock.json` or by scanning `plugins/<id>/`. Seen on a real installation: weather 1.1.0 in
the config, 1.0.2 on disk and running. Installations where someone already pressed Update are in
that state now, so the loader has to fix it too, not only the page.

## Files
- `pkg/web/handle_web_plugins.go` (`handlePluginsAPIInstall`)
- `pkg/plugins/loader.go` (`LoadAllEnabled`, `DownloadPlugin`)

## Acceptance criteria
- [ ] Install and Update download the plugin before writing `.vclu.json`; a failed download answers
      with an error and leaves the config as it was.
- [ ] `LoadAllEnabled` downloads a plugin whose files on disk are not the version in `.vclu.json`;
      when that fails (no network) it logs and loads the files it has.
- [ ] Tests cover both, and an installed plugin at the configured version is not downloaded again.
- [ ] `go test ./...` and `go test -race ./...` pass.
- [ ] A changelog entry in `changelog.d/0112-plugin-update-downloads.md`.

## Contract impact
None.
