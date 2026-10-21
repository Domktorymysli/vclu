---
name: write-release-changelog
description: "Write the CHANGELOG.md section and the GitHub release description for a vCLU release: assemble changelog.d/, then rewrite it for users, in Polish first and English after, without implementation details."
---

# Write the changelog of a release

**When:** a maintainer releases a version (`vX.Y.Z`). Not in a PR: a PR adds a file to
`changelog.d/` (see its README), and those entries may be technical.

**Read first:** `changelog.d/README.md`; the newest release in `CHANGELOG.md`; the PRs and tickets
the entries link, when an entry does not say what a user notices.

## Who reads it

A person with a Grenton installation who updates vCLU at home. They know Object Manager, CLU,
modules and objects; they do not know vCLU's code. They want to know: what is new for me, what got
fixed, and what I must do when I update. Most of them read Polish.

## Steps

1. `go run ./tools/changelog` prints what the entries say. Read every entry and the PR behind it
   when needed, so you know what the user sees, not only what the code does.
2. `go run ./tools/changelog release vX.Y.Z` puts the raw section into `CHANGELOG.md` and removes
   the files. Replace that section with the text below; the raw entries stay in the PRs.
3. Write the Polish part, then the English part: the same points in the same order, the English a
   translation of the Polish, not a second text.
4. Show the section to the maintainer before the release commit (`CHANGELOG: vX.Y.Z`).
5. The GitHub release description: the same section, with how to update above it (`update.sh
   --version vX.Y.Z`, the Docker image tag) and, when it applies, what was checked on hardware and
   what was not.

## The section

```markdown
## v1.7.0 (2026-10-25)

### Po polsku

**Najważniejsze:** kalendarz domu w modułach Lua i na dashboardzie; ...

#### Przed aktualizacją

- **Harmonogram z wartościami tekstowymi wymaga `values`** - dopisz listę wartości, np.
  `values = { 'dzien', 'noc' }`, inaczej moduł się nie załaduje. Błąd podaje gotową listę.

#### Nowe

- **Kalendarz domu** - wydarzenia (wywóz śmieci, szkoła) i harmonogramy (taryfa, pory dnia)
  opisane w module, z przypomnieniami, świętami i godzinami słońca; widget pokazuje miesiąc i
  dzień ([#76](https://github.com/Domktorymysli/vclu/issues/76)).

#### Poprawki

- **...** - ...

### English

**Highlights:** a house calendar in Lua modules and on dashboards; ...

#### Before you update

- **A schedule of text values needs `values`** - ...

#### New

- **A house calendar** - ...

#### Fixes

- **...** - ...
```

- Leave out a heading with nothing under it. "Przed aktualizacją" / "Before you update" holds every
  `Contract change:` entry, a re-import of OM, a module the user must edit, a setting that moved.
- **Najważniejsze** / **Highlights**: one line, the two or three things worth updating for.
- The area (Grenton, dashboards, Home Assistant, MCP...) goes into the sentence when it helps, not
  into headings.

## How an entry reads

- The bold lead says what the user gets or what stopped going wrong, in their words. The sentence
  after it says when they notice it and what to do, if anything. Two sentences at most.
- Leave out how it was done: file and function names, class and feature numbers, Go and test
  names, protocols, what an earlier version did inside. A Lua name stays only if the user writes it
  in a module (`values`, `:calendar()`, `dom:skip()`).
- A fix says what went wrong for the user, not the cause: "the blind stopped on a second OPEN from
  HomeKit", not "MoveUp was sent instead of SetPosition".
- Small fixes nobody would look for go into one entry: "Drobne poprawki: ..." / "Smaller fixes: ...".
- Each entry links its ticket or PR, so the technical detail is one click away.
- Polish: plain words and the names the vCLU panel and Object Manager use (moduł, obiekt, cecha,
  dashboard); no calques from the English text. Read it aloud once.

Too technical (an entry from v1.6.0):

> The channels of an LED RGBW module are lights - the OM import did not know `LED_CHANNEL` (class
> 49, four per LED RGBW 2.0 module), so a strip driven per channel had nothing in vCLU. Each channel
> is now a `RemoteLedChannel`: ...

For users:

> **Kanały modułu LED RGBW są osobnymi światłami** - każdy z czterech kanałów LED RGBW 2.0 da się
> teraz włączać i ściemniać z modułów i dashboardu. Po aktualizacji zaimportuj OM
> ponownie ([#41](https://github.com/Domktorymysli/vclu/issues/41)).

## Check

- Every entry from `changelog.d/` is in both parts, or merged into a "smaller fixes" entry.
- Nothing a user must do after updating is only in "Poprawki" or "Nowe".
- `go test ./tools/changelog/` passes; `changelog.d/` holds only its README.
