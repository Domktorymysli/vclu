---
name: write-release-changelog
description: "Write the short release notes of a vCLU release: one CHANGELOG.md section and one plain-text post for groups, English first and Polish second, the same points in the same order, every release in the same shape. The detail stays in the PRs and commits."
---

# Write the release notes

**When:** a maintainer releases a version (`vX.Y.Z`). Not in a PR: a PR adds a file to
`changelog.d/` (see its README), and those entries may be long and technical. The release notes
are short; whoever wants the detail follows the link to the PR, where the commits say everything.

**Read first:** `changelog.d/README.md`; the newest release in `CHANGELOG.md` (copy its shape);
`go run ./tools/changelog` (what the entries say); `git log --merges --format='%s' vPREV..HEAD`
(the PRs of this release).

## Who reads it

A person with a Grenton installation who updates vCLU at home, in a Facebook or Discord group or on
GitHub. They know Object Manager, CLU, modules and objects, not vCLU's code. They want three
answers in a minute: is it worth updating, must I do something when I update, what changed.

## What you produce

Two texts with the same content:

1. **The `CHANGELOG.md` section** (Markdown), also the GitHub release description with how to
   update above it.
2. **The post for groups** (plain text): no `**`, no `[text](url)`, no headings with `#` — Facebook
   shows them as they are. Bare URLs, `•` bullets, an empty line between parts.

Each has two parts: **English** first, then **Po polsku**. The Polish part is the same points in the
same order, written for a Polish reader (not a word-for-word calque); not a second, different text.

## Shape (every release looks like this)

```markdown
## v1.7.0 (2026-10-25)

### English

**Highlights:** a house calendar; new dashboard tiles (rooms, thermostat, forecast, player);
plugin updates from the panel that actually update.

**Before you update**
- Text schedules need `values = { ... }`, or the module does not load ([#76](https://github.com/Domktorymysli/vclu/issues/76)).

**New**
- House calendar: events, reminders, holidays and schedules in modules and on dashboards ([#76](https://github.com/Domktorymysli/vclu/issues/76)).
- Thermostat tile: set the target with + and -, read-only or locked when you want ([#64](https://github.com/Domktorymysli/vclu/issues/64)).
- ...

**Fixes**
- Picking a colour on a dashboard works again ([#116](https://github.com/Domktorymysli/vclu/pull/116)).
- Smaller fixes: ...

Full list: https://github.com/Domktorymysli/vclu/compare/v1.6.0...v1.7.0

### Po polsku

**Najważniejsze:** kalendarz domu; nowe kafle dashboardów (pokoje, termostat, prognoza, odtwarzacz);
aktualizacja pluginu z panelu naprawdę go aktualizuje.

**Przed aktualizacją**
- Harmonogram z wartościami tekstowymi wymaga `values = { ... }`, inaczej moduł się nie załaduje ([#76](...)).

**Nowe**
- Kalendarz domu: wydarzenia, przypomnienia, święta i harmonogramy w modułach i na dashboardzie ([#76](...)).
- ...

**Poprawki**
- Wybór koloru na dashboardzie znowu działa ([#116](...)).
- Drobne poprawki: ...

Pełna lista: https://github.com/Domktorymysli/vclu/compare/v1.6.0...v1.7.0
```

The same as a post:

```text
vCLU v1.7.0 — https://github.com/Domktorymysli/vclu/releases/tag/v1.7.0

ENGLISH
Highlights: a house calendar; new dashboard tiles (rooms, thermostat, forecast, player); ...

Before you update
• Text schedules need values = { ... }, or the module does not load.

New
• House calendar: events, reminders, holidays and schedules in modules and on dashboards.
• ...

Fixes
• Picking a colour on a dashboard works again.

POLSKI
Najważniejsze: ...
...
```

## Rules

- **Length:** one language part fits on a phone screen and a half: at most ~10 points under
  "New", ~6 under "Fixes"; each point one line (about 20 words), no second sentence unless it is
  what the user must do. The whole section is under ~3000 characters per language.
- **Highlights / Najważniejsze:** one line, the two or three things worth updating for.
- **Before you update / Przed aktualizacją:** every `Contract change:` entry, a re-import of OM, a
  module the user must edit, a setting that moved. Leave the heading out when empty. Nothing a user
  must do may hide under "New" or "Fixes".
- **Group, then cut:** several entries about one feature become one point (the epic or the main PR
  link); internal work (tests, CI, refactors, Go version, agent/MCP details a user does not see)
  goes into one "Smaller fixes: ..." / "Drobne poprawki: ..." point or is left out. MCP changes
  stay only when an agent user notices them (e.g. "promote limit 60/h").
- **Words of the user:** what they get or what stopped going wrong, not how. No file, function,
  class or feature numbers, protocols, Go or test names. A Lua name stays only if the user writes
  it in a module (`values`, `:thermostat()`, `optional()`).
- **Links:** each point links its ticket or PR in the Markdown section; the post has no per-point
  links, only the release URL at the top (and the compare URL at the end).
- **Hardware:** say what was checked on real hardware and what was not, only in the GitHub release
  description (one line), not in the post.
- Polish: plain words and the names of the vCLU panel and Object Manager (moduł, obiekt, cecha,
  dashboard). Read it aloud once.

## Steps

1. `go run ./tools/changelog` and the merged PRs: list every change, then group and cut as above.
2. `go run ./tools/changelog release vX.Y.Z` puts the raw entries into `CHANGELOG.md` and removes
   the files from `changelog.d/`. Replace that raw section with the short section; the raw text
   stays in the PRs and commits.
3. Write the English part, then the Polish part. Then the post from the same text.
4. Show both to the maintainer before the release commit (`CHANGELOG: vX.Y.Z`); save the post in
   the PR description or give it in the reply, ready to paste.
5. The GitHub release description: how to update (`update.sh --version vX.Y.Z`, the Docker image
   tag), one line on hardware checks, then the section.

## Check

- Every `changelog.d/` entry is in a point, in "smaller fixes", or deliberately left out as internal.
- English and Polish have the same points in the same order; the post matches the section.
- Under the length limits; no point longer than two lines on a phone.
- `go test ./tools/changelog/` passes; `changelog.d/` holds only its README.
