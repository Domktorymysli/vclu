# Changelog entries

A PR that users notice adds one file here instead of editing `CHANGELOG.md`, so two PRs never
conflict over it. Name it after the ticket: `0040-exposed-list-api.md` (no ticket: the branch
name). Write the sections as they appear in `CHANGELOG.md`:

```markdown
### Lua runtime

- **`ExposedObjects:list()` is public API** - what users notice, in a sentence or two ([#40](https://github.com/Domktorymysli/vclu/issues/40)).
```

- A file may have several sections (`### Lua runtime`, `### MCP`...); no `## ` headings, the
  release adds the version.
- An entry starts with `- `; following lines, also after a blank line, belong to it.
- A contract change starts its entry with `Contract change:` (see CONTRIBUTING.md).

An entry may be technical: it is written for the people who review the PR. At a release the
section is rewritten for users, in Polish and then English (`.agents/skills/write-release-changelog`).

`go run ./tools/changelog` prints what the next release will say. At a release,
`go run ./tools/changelog release v1.7.0` adds the section to `CHANGELOG.md` and removes the files
(this README stays); sections come in the order the files first use them, so reorder them in the
release commit if needed. `go test ./tools/changelog/` checks that every file here parses.
