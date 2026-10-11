# 0001: Tickets, knowledge and coordination for contributors and their agents

- **Status:** proposed
- **Date:** 2026-10-11

## Context

vCLU is built mostly by AI agents under human review. Until now one maintainer ran all of
them, with tickets, notes and pitfalls kept in a private repository. Now vCLU is public and
other people should be able to help, with their own agents.

What has to work:

- an agent of a stranger can find a task, learn everything it needs about it, do it and open
  a PR without asking anyone;
- two contributors do not do the same work, and an abandoned ticket does not stay blocked forever;
- what the project learned (pitfalls, recipes, decisions) reaches every agent, not only the
  maintainer's;
- nothing an outsider writes can instruct an agent to do something a maintainer did not approve.

A system that worked for one writer (tickets as files moved between `open/`, `in-progress/`
and `done/`, locks in a file, IDs reserved through git refs) does not carry over: contributors
cannot push to `main`, and every state change in git becomes a merge conflict between forks.

## Prerequisite

Today the public repository is generated from a private one, and each sync overwrites it. A
merged pull request, including a ticket file, would be lost at the next sync. Before outside
pull requests are accepted, the public repository has to become the source of truth, or merged
pull requests have to be imported into the private one. This is a separate decision.

## Decision

### One source of truth per kind of information

| Information | Lives in | Changed by |
|---|---|---|
| Ticket specification: goal, context, files, acceptance, contract impact | `dev/tickets/NNNN-slug.md` | PR, merged by a maintainer |
| Ticket state: who works on it, status, blockers | the GitHub issue: assignee, labels, relations | maintainers |
| Discussion | issue and PR comments | anyone |
| Plans and decisions | `dev/plans/`, `dev/decisions/` | PR, merged by a maintainer |
| Rules for agents | root `AGENTS.md` | PR, merged by a maintainer |
| Pitfalls of an area | the nearest `AGENTS.md` (`pkg/lua/AGENTS.md`, ...) | PR, merged by a maintainer |
| Recipes for repeatable work | `.agents/skills/<name>/SKILL.md` (Agent Skills format) | PR, merged by a maintainer |

State is never written to the repository, so ticket files are not touched when work starts or
ends: a done ticket's file stays as the record of what was asked. Synchronisation goes one way
only: from a file on `main` to the issue body.

### Creating a ticket

1. A maintainer (or a contributor, by asking in an issue) writes the ticket file. Until it has a
   number it is named `dev/tickets/new-slug.md`.
2. The issue is created as a stub: title, labels, and "specification pending review". Its number
   is the ticket number; GitHub assigns it, so numbers never collide.
3. The file is renamed to `NNNN-slug.md` and merged by PR.
4. Once the file is on `main`, the issue body is generated from it, and a maintainer adds `ready`.

Every ticket has a reviewed file; there is no "issue only" shortcut, because an issue body
written by anyone would then become an agent's instructions.

### Only reviewed text instructs an agent

Issue bodies and comments can be written by anyone. The context an agent gets for a ticket is:

- the ticket file as merged on `main`,
- the `AGENTS.md` files on the path to the ticket's files, which agents load by themselves,
- the linked skill and plan, and the contract tests of the touched area,
- comments by maintainers on the issue, quoted as information.

Other comments are left out. `AGENTS.md` files, `.agents/` and `dev/` instruct every agent, so
`CODEOWNERS` requires a maintainer's review for changes there. Agents take only issues labelled `ready`; only maintainers
set that label.

### Who works on a ticket

A ticket is assigned to one person; their agents work on it under their name. Someone who wants a
`ready` ticket asks in the issue and a maintainer assigns it. A draft PR that references the issue
shows the work is going on. A maintainer takes back a ticket that has had no activity for a while.

Areas where a mistake is expensive (Grenton protocol, sandbox, access control, persistence) carry
the `core` label: maintainers or invited contributors only.

### Epics, dependencies and plans use what GitHub has

- An epic is an issue with sub-issues; a larger theme is a milestone.
- Dependencies are GitHub's "blocked by" relations; a blocked issue does not get `ready`.
- A plan is a file in `dev/plans/`; the ticket links to it. Alternatives are argued in its PR.

### Review gate

A ticket is done when its PR is merged. A PR that changes behaviour is verified by someone other
than its author, human or agent, following the `review-pr` skill: the code runs in a sandbox
without credentials, build and tests run again without cache, and each acceptance criterion is
checked. Contract tests (CONTRIBUTING.md) are the main guard; a PR that changes a contract starts
with `Contract change:` and explains the migration.

## Planned, not built yet

| Piece | Until it exists |
|---|---|
| `CODEOWNERS` for `**/AGENTS.md`, `.agents/**`, `dev/**` and `core` areas | maintainers review every PR |
| Labels `triage`, `ticket`, `ready`, `needs-info`, `core`, `hw` | added with the first tickets |
| `dev/plans/` | created with the first plan |
| A test that keeps knowledge small: an `AGENTS.md` or `SKILL.md` above about 8 kB fails CI | prune by hand when touching a file |

## Standards used

Knowledge uses formats agents already read without project tools: `AGENTS.md`, including nested
files near the code they describe, and Agent Skills (`SKILL.md` with `name` and `description`) in
`.agents/skills/`. Nothing here needs a custom command to work.

## Not carried over

No journal: PR descriptions and git history hold what was done and why, read on demand.
No dashboard: GitHub's issue lists, labels and project views show the state.

## Consequences

- Contributors need a GitHub account; agents need network access to read state.
- Every ticket costs one PR before work starts.

## Alternatives considered

- **Tickets only as issues.** Simple, but specifications change without review and become an
  open channel for instructions to agents; agents also cannot read them offline.
- **Tickets as files with state inside** (the private setup). Claiming needs push access to `main`,
  and every claim conflicts between forks.
- **Two-way sync between files and issues.** Two editable copies of one text drift apart.
