# #2: govulncheck in CI

- **Type:** chore
- **Area:** build
- **Size:** S
- **Epic:** —
- **Blocked by:** —
- **Skill:** —
- **Needs hardware:** —

> State (who, status) lives in the GitHub issue, not here. To take the ticket, ask in the issue;
> a maintainer assigns it. This file is the specification: change it by PR.

## Goal
A pull request or push that brings in a known, reachable vulnerability in Go or a dependency fails
CI, and a known vulnerability we have reviewed and accepted does not.

## Context
`.github/workflows/test.yml` runs `test`, `race` and `lint`; nothing checks vulnerabilities. In
October 2026 a manual run of govulncheck found 22 reachable issues, fixed by the Go 1.26 upgrade.

Today `go run golang.org/x/vuln/cmd/govulncheck@latest ./...` exits with 3 for one finding:
**GO-2026-4316**, an open redirect in the `RedirectSlashes` middleware of `github.com/go-chi/chi`
v1.5.4, pulled in by the HomeKit library (`github.com/brutella/hap`). It has no fixed version. The
traces govulncheck shows go through chi's response writer wrappers, not `RedirectSlashes`, which
vCLU does not use. A plain govulncheck step would therefore fail on every run.

govulncheck has no built-in ignore list. Its JSON output (`-format json`) lists findings with their
OSV id.

## Files
- `.github/workflows/test.yml`: a `vulncheck` job.
- A file listing accepted findings, e.g. `.github/govulncheck-accepted.txt`: one id per line with
  a reason and the date of the review.
- A small script or inline step that runs govulncheck with `-format json`, collects ids of findings
  at symbol level (the ones govulncheck counts as affecting the code) and fails if any is not in
  the accepted list.

## Acceptance criteria
- [ ] The `vulncheck` job passes on `main` today with GO-2026-4316 in the accepted list, its reason
  and review date.
- [ ] Removing GO-2026-4316 from the list makes the job fail and print the finding (show this in the PR).
- [ ] A finding that is only in a required module but not called does not fail the job.
- [ ] The job uses the same Go version as the other jobs (`go-version: "1.26"`) and runs on pull
  requests and pushes to `main`.
- [ ] CONTRIBUTING.md says in two sentences what to do when the job fails: upgrade the module, or,
  if there is no fix and the code is not affected, add the id with a reason.

## Contract impact
None.
