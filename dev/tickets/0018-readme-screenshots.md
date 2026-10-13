# #18: README: screenshots and a tests badge that is not hard-coded

- **Type:** docs
- **Area:** web
- **Size:** S
- **Epic:** —
- **Blocked by:** —
- **Skill:** —
- **Needs hardware:** —

> State (who, status) lives in the GitHub issue, not here. To take the ticket, ask in the issue;
> a maintainer assigns it. This file is the specification: change it by PR.

## Goal
The README shows what vCLU looks like, and every badge reflects the repository as it is.

## Context
Both READMEs have the screenshots commented out (`<!-- TODO: screenshots -->`, pointing to
`docs/images/history.png` and `docs/images/editor.png`, which do not exist). The "tests 900+" badge
is a static shields.io image; Build and Codecov are already live.

## Files
- `docs/images/` (new)
- `README.md`, `README.en.md`

## Acceptance criteria
- [ ] Screenshots of History and audit, the code editor and one dashboard, taken on a vCLU with
  demo data: no real names, addresses, IP addresses, serial numbers or tokens.
- [ ] PNG, each under 300 KB, and the same images in both READMEs.
- [ ] The "tests 900+" badge is removed (Build and Codecov already show the state of the tests).

## Contract impact
None.
