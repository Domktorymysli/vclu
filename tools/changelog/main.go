// Command changelog assembles CHANGELOG.md from the entries in changelog.d/.
//
// Each PR adds its own file changelog.d/<ticket>-<slug>.md instead of editing
// CHANGELOG.md, so two PRs never conflict over it. A file holds one or more
// sections, written as they appear in CHANGELOG.md:
//
//	### Lua runtime
//
//	- **Short summary** - what users notice.
//
// Usage:
//
//	go run ./tools/changelog                          # print what the next release says
//	go run ./tools/changelog release v1.7.0 [date]    # add the release to CHANGELOG.md, remove the files
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const (
	dir       = "changelog.d"
	changelog = "CHANGELOG.md"
)

// section is one "### Title" with its entries, in the order the files list them.
type section struct {
	title   string
	entries []string
}

func main() {
	args := os.Args[1:]
	var err error
	switch {
	case len(args) == 0:
		err = preview(".")
	case args[0] == "release" && (len(args) == 2 || len(args) == 3):
		date := time.Now().Format("2006-01-02")
		if len(args) == 3 {
			date = args[2]
		}
		err = release(".", args[1], date)
	default:
		err = fmt.Errorf("usage: changelog [release <version> [date]]")
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "changelog:", err)
		os.Exit(1)
	}
}

func preview(root string) error {
	sections, _, err := load(filepath.Join(root, dir))
	if err != nil {
		return err
	}
	if len(sections) == 0 {
		fmt.Println("(no entries in " + dir + ")")
		return nil
	}
	fmt.Print(render(sections))
	return nil
}

// release runs in the repository root: CHANGELOG.md and changelog.d/ are relative to it.
func release(root, version, date string) error {
	if _, err := time.Parse("2006-01-02", date); err != nil {
		return fmt.Errorf("date %q: want YYYY-MM-DD", date)
	}
	sections, files, err := load(filepath.Join(root, dir))
	if err != nil {
		return err
	}
	if len(sections) == 0 {
		return fmt.Errorf("no entries in %s", dir)
	}
	path := filepath.Join(root, changelog)
	old, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	out, err := insert(string(old), fmt.Sprintf("## %s (%s)", version, date), render(sections))
	if err != nil {
		return err
	}
	if err := os.WriteFile(path, []byte(out), 0o644); err != nil {
		return err
	}
	for _, f := range files {
		if err := os.Remove(f); err != nil {
			return err
		}
	}
	fmt.Printf("%s: %s with %d entries from %d files\n", changelog, version, count(sections), len(files))
	return nil
}

// load reads every entry file in dir (README.md excepted), sorted by name.
func load(dir string) ([]section, []string, error) {
	names, err := filepath.Glob(filepath.Join(dir, "*.md"))
	if err != nil {
		return nil, nil, err
	}
	sort.Strings(names)
	var files []string
	var sections []section
	for _, name := range names {
		if filepath.Base(name) == "README.md" {
			continue
		}
		data, err := os.ReadFile(name)
		if err != nil {
			return nil, nil, err
		}
		parsed, err := parse(string(data))
		if err != nil {
			return nil, nil, fmt.Errorf("%s: %w", name, err)
		}
		sections = merge(sections, parsed)
		files = append(files, name)
	}
	return sections, files, nil
}

// parse splits one file into sections; an entry starts with "- " and runs to the next one.
func parse(text string) ([]section, error) {
	var out []section
	var cur *section
	for i, line := range strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n") {
		trimmed := strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(line, "### "):
			title := strings.TrimSpace(line[4:])
			if title == "" {
				return nil, fmt.Errorf("line %d: empty section title", i+1)
			}
			out = append(out, section{title: title})
			cur = &out[len(out)-1]
		case trimmed == "":
			if cur != nil && len(cur.entries) > 0 {
				cur.entries[len(cur.entries)-1] += "\n"
			}
		case strings.HasPrefix(line, "#"):
			return nil, fmt.Errorf("line %d: only \"### Section\" headings, the release adds the version", i+1)
		case cur == nil:
			return nil, fmt.Errorf("line %d: text before the first \"### Section\"", i+1)
		case strings.HasPrefix(line, "- "):
			cur.entries = append(cur.entries, line)
		case len(cur.entries) == 0:
			return nil, fmt.Errorf("line %d: an entry starts with \"- \"", i+1)
		default:
			cur.entries[len(cur.entries)-1] += "\n" + line
		}
	}
	for _, s := range out {
		if len(s.entries) == 0 {
			return nil, fmt.Errorf("section %q has no entries", s.title)
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no \"### Section\" with entries")
	}
	for i := range out {
		for j := range out[i].entries {
			out[i].entries[j] = strings.TrimRight(out[i].entries[j], "\n ")
		}
	}
	return out, nil
}

// merge adds the sections of one file; a title seen before keeps its place.
func merge(all, add []section) []section {
	for _, s := range add {
		found := false
		for i := range all {
			if all[i].title == s.title {
				all[i].entries = append(all[i].entries, s.entries...)
				found = true
				break
			}
		}
		if !found {
			all = append(all, section{title: s.title, entries: append([]string(nil), s.entries...)})
		}
	}
	return all
}

func render(sections []section) string {
	var b strings.Builder
	for i, s := range sections {
		if i > 0 {
			b.WriteString("\n")
		}
		b.WriteString("### " + s.title + "\n\n")
		for _, e := range s.entries {
			b.WriteString(e + "\n")
		}
	}
	return b.String()
}

// insert puts the release above the newest "## " heading, below the title.
func insert(doc, heading, body string) (string, error) {
	if strings.Contains(doc, "\n"+heading+"\n") {
		return "", fmt.Errorf("%s already has %q", changelog, heading)
	}
	if strings.Contains(doc, "\n## Unreleased") {
		return "", fmt.Errorf("%s has an \"## Unreleased\" section: move its entries to %s", changelog, dir)
	}
	block := heading + "\n\n" + body + "\n"
	idx := strings.Index(doc, "\n## ")
	if idx < 0 {
		return strings.TrimRight(doc, "\n") + "\n\n" + block, nil
	}
	return doc[:idx+1] + block + doc[idx+1:], nil
}

func count(sections []section) int {
	n := 0
	for _, s := range sections {
		n += len(s.entries)
	}
	return n
}
