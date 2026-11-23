# #266: OpenSSF Scorecard: measured project practices and signed releases

- **Type:** chore
- **Area:** build
- **Size:** S
- **Epic:** —
- **Blocked by:** #261
- **Skill:** —
- **Needs hardware:** —

> State (who, status) lives in the GitHub issue, not here. To take the ticket, ask in the issue;
> a maintainer assigns it. This file is the specification: change it by PR.

## Goal
vclu's development practices are measured by OpenSSF Scorecard and shown with a badge in the
README, and every release asset can be verified to come from vclu's release workflow.

## Context
[OpenSSF Scorecard](https://github.com/ossf/scorecard) checks a repository against a list of
practices (branch protection, code review, pinned dependencies, token permissions, SAST, fuzzing,
security policy, signed releases, dependency updates, known vulnerabilities) and scores each 0-10.
The `ossf/scorecard-action` runs it in GitHub Actions and publishes the result for the badge.
Installers, partners and customers who ask how vclu is built (also with the EU Cyber Resilience
Act in mind) can check the score themselves.

Some checks follow from other tickets: SAST and fuzzing from the code scanning ticket, the
security policy from #261. Signed releases are new: GitHub artifact attestations
(`actions/attest-build-provenance`) record which workflow and commit built each archive, `.deb`
and image, and users verify them with `gh attestation verify`. Some checks cannot reach 10 for a
project with one maintainer (code review of every change by a second person); the score says so
and the reason is written down instead.

## Files
- `.github/workflows/scorecard.yml`
- `.github/workflows/*.yml` (permissions, pinned actions)
- `.github/workflows/release.yml` (attestations)
- `.github/dependabot.yml`
- `README.md`, `README.en.md` (badge)
- `SECURITY.md` (verifying attestations)

## Acceptance criteria
- [ ] Scorecard runs weekly and on pushes to `main`, publishes its results (`publish_results: true`) and uploads them to code scanning.
- [ ] `README.md` and `README.en.md` show the Scorecard badge linking to the published result.
- [ ] Every workflow sets `permissions` at the top to read-only and grants write per job, only where needed.
- [ ] Third-party actions are pinned to a full commit SHA with the version in a comment; Dependabot (or Renovate) keeps the actions and Go modules up to date.
- [ ] Every release attests the provenance of its archives, `.deb` packages and images; `gh attestation verify <file> -R Domktorymysli/vclu` succeeds for a downloaded asset; `SECURITY.md` says how to verify.
- [ ] The PR lists the score of each check before and after, and for each check below 10 either what was done or why it stays (e.g. one maintainer, no second reviewer).
- [ ] `go test ./...` passes.

## Contract impact
None.
