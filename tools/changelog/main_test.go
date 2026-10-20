package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func write(t *testing.T, path, text string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
}

// The entry files in the repository must parse, or the release fails.
func TestRepoEntries(t *testing.T) {
	if _, _, err := load(filepath.Join("..", "..", dir)); err != nil {
		t.Fatal(err)
	}
}

func TestParse(t *testing.T) {
	got, err := parse("### Lua runtime\n\n- **A** - one.\n- **B** - two,\n  continued.\n\n  Second paragraph.\n\n### MCP\n\n- **C** - three.\n")
	if err != nil {
		t.Fatal(err)
	}
	want := []section{
		{"Lua runtime", []string{"- **A** - one.", "- **B** - two,\n  continued.\n\n  Second paragraph."}},
		{"MCP", []string{"- **C** - three."}},
	}
	if render(got) != render(want) {
		t.Errorf("parse =\n%s\nwant\n%s", render(got), render(want))
	}
}

func TestParseErrors(t *testing.T) {
	for name, text := range map[string]string{
		"empty":           "",
		"text first":      "Some text\n### Lua runtime\n- **A** - a.\n",
		"version heading": "## v1.7.0\n### Lua runtime\n- **A** - a.\n",
		"no entries":      "### Lua runtime\n\n### MCP\n- **A** - a.\n",
		"not an entry":    "### Lua runtime\nplain line\n",
		"empty title":     "### \n- **A** - a.\n",
	} {
		if _, err := parse(text); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
}

// Entries of one section from two files end up together, sections in the order first seen.
func TestReleaseMergesFiles(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, changelog), "# Changelog\n\n## v1.6.0 (2026-10-19)\n\n### Grenton\n\n- **Old** - old.\n")
	write(t, filepath.Join(root, dir, "README.md"), "# not an entry\n")
	write(t, filepath.Join(root, dir, "0040-list.md"), "### Lua runtime\n\n- **List** - list.\n")
	write(t, filepath.Join(root, dir, "0065-tasmota.md"), "### Lua runtime\n\n- **Tasmota** - tasmota.\n\n### MCP\n\n- **MQTT step** - step.\n")
	write(t, filepath.Join(root, dir, "0020-promote.md"), "### MCP\n\n- **Promote** - promote.\n")

	if err := release(root, "v1.7.0", "2026-10-25"); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(root, changelog))
	if err != nil {
		t.Fatal(err)
	}
	want := "# Changelog\n\n## v1.7.0 (2026-10-25)\n\n" +
		"### MCP\n\n- **Promote** - promote.\n- **MQTT step** - step.\n\n" +
		"### Lua runtime\n\n- **List** - list.\n- **Tasmota** - tasmota.\n\n" +
		"## v1.6.0 (2026-10-19)\n\n### Grenton\n\n- **Old** - old.\n"
	if string(data) != want {
		t.Errorf("CHANGELOG.md =\n%s\nwant\n%s", data, want)
	}
	left, _ := filepath.Glob(filepath.Join(root, dir, "*.md"))
	if len(left) != 1 || filepath.Base(left[0]) != "README.md" {
		t.Errorf("left in %s: %v, want README.md only", dir, left)
	}
}

func TestReleaseRefuses(t *testing.T) {
	cases := map[string]struct {
		doc, entry, version, date, want string
	}{
		"no entries":     {"# Changelog\n", "", "v1.7.0", "2026-10-25", "no entries"},
		"same version":   {"# Changelog\n\n## v1.7.0 (2026-10-25)\n", "### MCP\n- **A** - a.\n", "v1.7.0", "2026-10-25", "already has"},
		"unreleased":     {"# Changelog\n\n## Unreleased\n\n### MCP\n\n- **A** - a.\n", "### MCP\n- **B** - b.\n", "v1.7.0", "2026-10-25", "Unreleased"},
		"bad date":       {"# Changelog\n", "### MCP\n- **A** - a.\n", "v1.7.0", "25.10.2026", "YYYY-MM-DD"},
		"broken entries": {"# Changelog\n", "no heading\n", "v1.7.0", "2026-10-25", "0001-a.md"},
	}
	for name, c := range cases {
		root := t.TempDir()
		write(t, filepath.Join(root, changelog), c.doc)
		if c.entry != "" {
			write(t, filepath.Join(root, dir, "0001-a.md"), c.entry)
		}
		err := release(root, c.version, c.date)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: err = %v, want %q", name, err, c.want)
			continue
		}
		if data, _ := os.ReadFile(filepath.Join(root, changelog)); string(data) != c.doc {
			t.Errorf("%s: CHANGELOG.md changed on a refused release", name)
		}
	}
}
