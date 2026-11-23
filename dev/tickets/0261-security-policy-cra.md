# #261: Security policy, SBOM and a security record for every release (EU CRA)

- **Type:** docs
- **Area:** build
- **Size:** S
- **Epic:** —
- **Blocked by:** —
- **Skill:** —
- **Needs hardware:** —

> State (who, status) lives in the GitHub issue, not here. To take the ticket, ask in the issue;
> a maintainer assigns it. This file is the specification: change it by PR.

## Goal
vclu's security policy and release artifacts follow the EU Cyber Resilience Act (Regulation
2024/2847): a stated support period, response times, a published contact and an SBOM for every
artifact users install, and each release carries a record of how it was checked.

## Context
vclu already has the pieces: `SECURITY.md` with GitHub private vulnerability reporting, a weekly
scan of main and the latest release (`security.yml`, govulncheck and vendored libraries) and
SPDX SBOMs of the Docker images attached to each release (`release.yml`). The CRA applies in full
from 11 December 2027; its reporting duties to ENISA apply from 11 September 2026. Its essential
requirements (Annex I) and the manufacturer's duties (Art. 13-14) ask for a few things the policy
does not say yet, and the SBOM covers the images only, not the binaries and `.deb` packages from
APT.

The vclu cloud and the mobile apps are part of the same product for the CRA; the policy should
name them.

The scans already run at each release and every week, but their results live only in the
Actions logs, which GitHub deletes after 90 days. The CRA's technical documentation is kept
for 10 years, so each release keeps its own security record as release assets: what was built,
with what, how it was scanned and what was found. The weekly scan of a release adds to its
record, so the record also shows when a later advisory was noticed and how it was handled.
Only public information goes there: advisory ids and published GitHub security advisories,
never details of an unfixed vulnerability.

## Files
- `SECURITY.md`
- `.github/workflows/release.yml` (SBOM step)
- `.goreleaser.yaml`, if the SBOM of the binaries is made there
- `.github/workflows/security.yml` (the weekly scan adds to the latest release's record)
- vclu.pl and docs.vclu.pl: `/.well-known/security.txt` (separate repositories, deployed by the maintainer)

## Acceptance criteria
- [ ] `SECURITY.md` states the support period: which releases get security fixes and for how long (at least the latest release; the period for a future LTS line is named or left as "to be announced").
- [ ] `SECURITY.md` states response times: acknowledgement, first assessment, and that an actively exploited vulnerability is handled and reported as the CRA requires.
- [ ] `SECURITY.md` names an e-mail contact (`security@vclu.pl`) next to GitHub private reporting, for reporters without a GitHub account.
- [ ] `SECURITY.md` scope covers the vclu cloud (pairing, tunnel, remote dashboards, voice) and the mobile apps; official plugins (`@vclu/*`) are in scope, community plugins are their authors'.
- [ ] `SECURITY.md` says how fixes are announced: a GitHub security advisory (with a CVE when it applies) and a "Security" line in the release notes.
- [ ] `SECURITY.md` says how users verify what they install: checksums of the release assets, the signed APT repository.
- [ ] Every release attaches an SBOM (SPDX or CycloneDX) of the binaries and `.deb` packages, besides the images; `gh release view vX.Y.Z` lists them.
- [ ] Every release attaches `vclu-X.Y.Z-security.json`: the commit SHA, the tag, the Go version, the names and versions of the scanners (govulncheck, the image scanner, the vendored-library check), the date and result of each scan, and the SBOM file names with their checksums.
- [ ] The weekly scan of the latest release appends its date, scanner versions and result to that release's record; a finding adds its public advisory id and, once fixed, the release and GitHub security advisory that fixed it.
- [ ] The raw scan output of the release is attached next to the record (not only the Actions log).
- [ ] `SECURITY.md` says where the records are.
- [ ] `/.well-known/security.txt` (RFC 9116) on vclu.pl and docs.vclu.pl points to the contact and to `SECURITY.md`, with an `Expires` date.
- [ ] `go test ./...` passes.

## Contract impact
None.
