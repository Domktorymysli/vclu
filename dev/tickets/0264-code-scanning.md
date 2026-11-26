# #264: Code scanning: CodeQL, a workflow audit and fuzz tests of the network parsers

- **Type:** test
- **Area:** build
- **Size:** M
- **Epic:** —
- **Blocked by:** —
- **Skill:** —
- **Needs hardware:** —

> State (who, status) lives in the GitHub issue, not here. To take the ticket, ask in the issue;
> a maintainer assigns it. This file is the specification: change it by PR.

## Goal
vclu's own code and its CI are checked automatically, not only its dependencies: static analysis
of the Go code, an audit of the GitHub Actions workflows, and fuzz tests of the code that reads
data from the network.

## Context
The scans so far (`security.yml`, `release.yml`, `.github/*.sh`) look for known advisories in what
vclu uses: govulncheck for Go modules, Trivy for the image, OSV for the vendored web libraries.
They do not look at vclu's own code.

- **CodeQL** (GitHub code scanning, free for public repositories) supports Go and finds patterns
  such as user input reaching a file path, a command or an outgoing request.
- **zizmor** audits GitHub Actions workflows: template injection in `run:`, broad permissions,
  actions not pinned to a commit, credentials kept after checkout. The release workflows hold
  `contents: write` and `packages: write`, so they are what an attacker on the supply chain aims at.
- **Go fuzzing** (`go test -fuzz`, no extra dependency) feeds generated input to a function and
  reports panics and hangs. Only `pkg/omchange` has a fuzz test now. Code that parses what anyone
  on the LAN or a connected device can send is the first target: the Grenton UDP requests and their
  decryption, the Satel frame decoder, CLU reports, TFTP paths, the OM project import.

A crash found by fuzzing or a CodeQL alert can be a vulnerability: it is handled as `SECURITY.md`
says (a draft security advisory), and the input that reproduces it is committed together with the
fix, not before.

## Files
- `.github/workflows/codeql.yml` (or code scanning's default setup, in the repository settings)
- `.github/workflows/security.yml`, `.github/workflows/test.yml`
- `pkg/cipher/`, `pkg/commands/`, `pkg/satel/`, `pkg/server/` (CLU reports), `pkg/tftp/`, `pkg/omproject/`: `*_fuzz_test.go`
- `CONTRIBUTING.md` or `AGENTS.md` (how to run the fuzz tests)

## Acceptance criteria
- [ ] CodeQL analyses the Go code on pull requests to `main`, on pushes to `main` and weekly; results are in the repository's code scanning alerts.
- [ ] Before CodeQL first runs on `main`, its findings on the current code are reviewed (a local run or a private fork); a real problem is fixed or handled as a security advisory first.
- [ ] zizmor (pinned version) audits `.github/workflows/` on pull requests that change them and weekly; a finding at its default severity fails the run, or is fixed, or is ignored with a reason in its configuration.
- [ ] The current workflows pass zizmor.
- [ ] Fuzz tests exist for at least: `cipher` decryption and padding, `commands.ParseLuaScriptRequest`, the Satel frame decoder, `parseClientReportMessage`, the TFTP path check, `omproject.Read`.
- [ ] Each fuzz test has a seed corpus from real or test frames, and checks properties beyond "does not panic" where there are any (e.g. a Satel frame encoded and fed back decodes to itself; a TFTP path never resolves outside its directory).
- [ ] CI runs every fuzz test for a short time (about 30 s each) weekly in `security.yml`; `go test ./...` keeps running the seed corpora.
- [ ] How to run a fuzz test locally and what to do with a crash is written down for contributors.
- [ ] `go test ./...` and `go test -race ./...` pass.

## Contract impact
None.
