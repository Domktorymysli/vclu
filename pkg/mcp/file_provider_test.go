package mcp

import (
	"os"
	"path/filepath"
	"testing"
)

func TestGrep_SearchesSubdirectories(t *testing.T) {
	dir := t.TempDir()
	write := func(rel, body string) {
		p := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("modules/dom.lua", "local NEEDLE = 1\n")
	write("modules/energia/ha.lua", "-- x\nlocal NEEDLE = 2\n")
	write("modules/energia/deep/x.lua", "NEEDLE\n")
	write("modules/notes.txt", "NEEDLE\n")
	write(".vclu.json", "NEEDLE\n")
	write("modules/światło/oświetlenie.lua", "NEEDLE\n")

	files := func(glob string) map[string]bool {
		t.Helper()
		res, err := NewFileProvider(dir).Grep("NEEDLE", glob, 50)
		if err != nil {
			t.Fatal(err)
		}
		got := map[string]bool{}
		for _, r := range res {
			got[filepath.ToSlash(r.File)] = true
		}
		return got
	}

	all := []string{"modules/dom.lua", "modules/energia/ha.lua", "modules/energia/deep/x.lua", "modules/światło/oświetlenie.lua"}
	for _, glob := range []string{"", "*.lua", "**/*.lua", "modules/**/*.lua"} {
		got := files(glob)
		for _, f := range all {
			if !got[f] {
				t.Errorf("glob %q: %s not searched (got %v)", glob, f, got)
			}
		}
		if len(got) != len(all) {
			t.Errorf("glob %q: unexpected files %v", glob, got)
		}
	}

	if got := files("modules/*.lua"); len(got) != 1 || !got["modules/dom.lua"] {
		t.Errorf("modules/*.lua should match only the top level, got %v", got)
	}
	if got := files("modules/energia/*.lua"); len(got) != 1 || !got["modules/energia/ha.lua"] {
		t.Errorf("modules/energia/*.lua: got %v", got)
	}
	if got := files("ha.lua"); len(got) != 1 || !got["modules/energia/ha.lua"] {
		t.Errorf("ha.lua by name: got %v", got)
	}
	if got := files("modules/światło/ośw*.lua"); len(got) != 1 || !got["modules/światło/oświetlenie.lua"] {
		t.Errorf("non-ASCII glob: got %v", got)
	}
}

func TestFileProvider_DoesNotFollowSymlinks(t *testing.T) {
	dir, outside := t.TempDir(), t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "secret.lua"), []byte("NEEDLE SECRET\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "modules"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "modules", "ok.lua"), []byte("NEEDLE\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(outside, "secret.lua"), filepath.Join(dir, "modules", "link.lua")); err != nil {
		t.Skip("symlinks not supported:", err)
	}
	if err := os.Symlink(outside, filepath.Join(dir, "modules", "linkdir")); err != nil {
		t.Fatal(err)
	}
	fp := NewFileProvider(dir)

	for _, p := range []string{"modules/link.lua", "modules/linkdir/secret.lua"} {
		if c, err := fp.ReadFile(p); err == nil {
			t.Errorf("ReadFile followed %s: %q", p, c)
		}
		if err := fp.DeleteFile(p); err == nil {
			t.Errorf("DeleteFile followed %s", p)
		}
	}
	if _, err := os.Stat(filepath.Join(outside, "secret.lua")); err != nil {
		t.Fatal("file outside the data dir was deleted")
	}

	res, err := fp.Grep("NEEDLE", "", 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(res) != 1 || res[0].File != "modules/ok.lua" {
		t.Errorf("Grep: %+v", res)
	}

	for _, pat := range []string{"modules/*.lua", "modules/*/*.lua"} {
		list, err := fp.ListFiles(pat)
		if err != nil {
			t.Fatal(err)
		}
		for _, f := range list {
			if f.Path != "modules/ok.lua" {
				t.Errorf("ListFiles(%q) listed %s", pat, f.Path)
			}
		}
	}
}

func TestDraftTest_RejectsPathsOutsideDrafts(t *testing.T) {
	se, _, _ := newTestSandbox(t)
	for _, p := range []string{"../../../etc/x.lua", "/tmp/x.lua", "a/main.lua"} {
		if _, err := se.RunScenario([]string{p}, SandboxScenario{}); err == nil {
			t.Errorf("RunScenario accepted %q", p)
		}
	}
}
