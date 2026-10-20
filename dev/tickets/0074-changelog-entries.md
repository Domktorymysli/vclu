# #0074: Changelog entries in changelog.d/, assembled at a release

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
Two PRs never conflict over the changelog: each adds its own file, the release assembles `CHANGELOG.md`.

## Context
Every PR added its entry at the top of the same "Unreleased" section, so any two open PRs
conflicted. On 2026-10-20, merging one of four open PRs left the other three conflicting.
The format of `CHANGELOG.md` for readers stays the same; release notes are still its section.

## Files
- `tools/changelog/` (assembles and checks the entries)
- `changelog.d/README.md` (the format)
- `CHANGELOG.md` (the "Unreleased" entries move to `changelog.d/`)
- `CONTRIBUTING.md`, `dev/tickets/TEMPLATE.md`, `dev/decisions/0002-runtime-libraries.md`,
  `.agents/skills/review-pr/SKILL.md`

## Acceptance criteria
- [ ] `go run ./tools/changelog` prints the next release's section from `changelog.d/*.md`.
- [ ] `go run ./tools/changelog release v1.7.0` adds `## v1.7.0 (<date>)` above the newest release,
      entries of one section from several files together, and removes the files (README stays).
- [ ] A release is refused, with `CHANGELOG.md` unchanged, when there are no entries, the version
      is there already, an "Unreleased" section is left or an entry file does not parse.
- [ ] `go test ./tools/changelog/` fails on an entry file in the repository that does not parse.
- [ ] `CHANGELOG.md` has no "Unreleased" section; its entries are in `changelog.d/`, and
      `go run ./tools/changelog` prints the same entries.
- [ ] The PR instructions (CONTRIBUTING, ticket template, review skill) point to `changelog.d/`.
- [ ] `go test ./...` and `go test -race ./...` pass.

## Contract impact
None (no runtime behaviour). Contributors write the entry in `changelog.d/` instead of `CHANGELOG.md`.
