# #58: Diagnostic pack import names the CLU that failed

- **Type:** bug
- **Area:** grenton
- **Size:** S
- **Epic:** —
- **Blocked by:** —
- **Skill:** —
- **Needs hardware:** —

> State (who, status) lives in the GitHub issue, not here. To take the ticket, ask in the issue;
> a maintainer assigns it. This file is the specification: change it by PR.

## Goal
An import error names the CLU the way the imported ones are named, and says what went wrong.

## Context
OM files can be imported as a diagnostic pack from Object Manager (`handleZipUpload`,
`pkg/web/handle_web_om.go`). The summary in the panel (`web/static/js/om-files.js`) lists the
imported CLUs by name, but an error shows the path inside the zip and, for an empty file, a wrong
reason:

```
Dom1 (127 obj, 24 mod)
Errors:
CLU511000000/om.lua: could not extract serial number from CLU ID
```

The name of a failed CLU is not in its `om.lua`. The pack carries the Object Manager project (a
nested zip with `system.xml`), where each CLU is `<name>…</name>`, `<ipAddress>…</ipAddress>`,
`<nameOnCLU>CLU…</nameOnCLU>`.

Object Manager writes empty files for a CLU it could not read (seen with a Gate Alarm that answers
on the network). `omparser.Parse` accepts an empty file and the serial check then fails.

The summary also puts CLU names and errors into HTML unescaped; both come from the uploaded file.

## Files
- `pkg/web/handle_web_om.go`
- `web/static/js/om-files.js`
- tests in `pkg/web/`

## Acceptance criteria
- [ ] An error and the skipped vCLU read `Alarm (CLU511000000): …` when the pack has a project naming that CLU, and `CLU511000000: …` when it has none. A test shows both.
- [ ] An empty `om.lua`, in a pack or uploaded alone, is reported as empty, not as a bad serial number.
- [ ] The response keeps its fields and types (`errors` and `skippedList` are lists of strings); CLUs that import, import as before.
- [ ] The summary escapes CLU names, the skipped list and errors.
- [ ] `go test ./...` and `go test -race ./...` pass.
- [ ] CHANGELOG entry in "Unreleased".

## Contract impact
None: `/api/om/upload` keeps its fields; only the text of errors and skipped entries changes.
