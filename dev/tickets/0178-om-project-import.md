# #0178: Import the Object Manager project right after discovery

- **Type:** feature
- **Area:** grenton
- **Size:** M
- **Epic:** —
- **Blocked by:** —
- **Skill:** —
- **Needs hardware:** — (project files from several OM versions, anonymised, as fixtures)

> State (who, status) lives in the GitHub issue, not here. To take the ticket, ask in the issue;
> a maintainer assigns it. This file is the specification: change it by PR.

## Goal
After CLU discovery the user can upload the OM project (`.omp`) instead of a diagnostic pack, and the
agent learns the house from it: objects with names, event bindings and the CLU scripts.

## Context
- The diagnostic pack import stays the reference path: `om.lua` from the CLU changes less between
  OM versions than the project format. The project adds what the pack lacks and must never be required.
- `.omp` is a ZIP: `system.xml` (XStream object stream: CLUs, modules, objects, `Event` →
  `commands` as Lua lines), `scripts/<CLU>/*.lua` (plain Lua; the `*.lua.vd` next to them is a
  serialized block diagram, ignore it), `diagnostics/`, `properties.xml` (OM version and project
  secrets), `measurements.db`.
- The diagnostic pack already carries a nested project zip; `cluNamesFromProject` in
  `pkg/web/handle_web_om.go` reads CLU names from it. One reader serves both sources.
- OM versions differ in `system.xml`. Read only the elements needed, tolerate unknown and missing
  ones, and report what was skipped instead of failing the import.
- vCLU reads only the version from `properties.xml`; it never stores or logs anything else from it,
  and the uploaded file is not kept on disk after parsing.

## Files
- `pkg/web/handle_web_om.go`
- `pkg/web/handle_web_om_test.go`
- `pkg/web/testdata/` (project fixtures)

## Acceptance criteria
- [ ] Uploading a `.omp` registers the same CLUs and objects as the diagnostic pack built from that project.
- [ ] Event bindings and the CLU scripts become readable by the agent, from a `.omp` or from the
      project inside a diagnostic pack (where exactly: decide in the PR, e.g. a generated section of `ai/house.md`).
- [ ] Fixtures from at least two OM versions pass; an unknown element or a missing section yields a warning, not an error.
- [ ] The discovery flow in the web UI offers the project upload next to the diagnostic pack.
- [ ] A test asserts nothing from `properties.xml` other than the version is kept, and no upload copy stays on disk.
- [ ] `go test ./...` and `go test -race ./...` pass.
- [ ] A changelog entry in `changelog.d/0178-om-project-import.md`.

## Contract impact
None (adds an accepted upload type).
