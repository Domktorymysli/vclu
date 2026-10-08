package workspace

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// outside is a directory beyond the workspace root that links point to.
func symlinkSetup(t *testing.T) (root, outside string) {
	t.Helper()
	root, outside = t.TempDir(), t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "secret.lua"), []byte("SECRET"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "modules"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(outside, "secret.lua"), filepath.Join(root, "modules", "link.lua")); err != nil {
		t.Skip("symlinks not supported:", err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "modules", "linkdir")); err != nil {
		t.Fatal(err)
	}
	return root, outside
}

func TestCheckNoSymlinks(t *testing.T) {
	root, _ := symlinkSetup(t)
	for rel, bad := range map[string]bool{
		"modules/link.lua":           true,
		"modules/linkdir/secret.lua": true,
		"modules/linkdir/new.lua":    true,
		"modules/plain.lua":          false, // missing is fine
		"modules/new/dir/x.lua":      false,
	} {
		err := CheckNoSymlinks(root, rel)
		if (err != nil) != bad {
			t.Errorf("%s: err=%v, want error=%v", rel, err, bad)
		}
	}
}

func TestWorkspace_DoesNotFollowSymlinks(t *testing.T) {
	root, outside := symlinkSetup(t)
	w, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	unchanged := func() {
		t.Helper()
		data, _ := os.ReadFile(filepath.Join(outside, "secret.lua"))
		if string(data) != "SECRET" {
			t.Fatalf("file outside the workspace was changed: %q", data)
		}
		if _, err := os.Stat(filepath.Join(outside, "new.lua")); err == nil {
			t.Fatal("file created outside the workspace")
		}
	}

	for _, p := range []string{"modules/link.lua", "modules/linkdir/secret.lua"} {
		if c, err := w.GetProductionContent(p); err == nil || strings.Contains(c, "SECRET") {
			t.Errorf("diff read %s through a symlink: %q, %v", p, c, err)
		}
		if err := w.CreateDraft(p, "PWNED"); err != nil {
			t.Fatal(err)
		}
		if _, err := w.Promote(p); err == nil {
			t.Errorf("promote wrote %s through a symlink", p)
		}
		unchanged()
	}
	if err := w.CreateDraft("modules/linkdir/new.lua", "PWNED"); err != nil {
		t.Fatal(err)
	}
	if _, err := w.Promote("modules/linkdir/new.lua"); err == nil {
		t.Error("promote created a file through a symlinked directory")
	}
	unchanged()

	// a draft directory that is a link
	if err := os.Symlink(outside, filepath.Join(w.draftsPath(), "modules", "evil")); err != nil {
		t.Fatal(err)
	}
	if err := w.CreateDraft("modules/evil/new.lua", "PWNED"); err == nil {
		t.Error("draft written through a symlinked directory")
	}
	if _, err := w.GetDraft("modules/evil/secret.lua"); err == nil {
		t.Error("draft read through a symlinked directory")
	}
	unchanged()

	// rollback into a linked file
	name := "20260101_000000_modules_link.lua"
	if err := os.WriteFile(filepath.Join(w.backupsPath(), name), []byte("PWNED"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := w.Rollback(name); err == nil {
		t.Error("rollback wrote through a symlink")
	}
	unchanged()
}

func TestRollback_RejectsTraversal(t *testing.T) {
	w, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{
		"20260101_000000_.._.._etc_passwd",
		"20260101_000000_../../x.lua",
		"20260101_000000_a_main.lua",
	} {
		if err := w.Rollback(name); err == nil {
			t.Errorf("rollback accepted %q", name)
		}
	}
}

func TestDrafts_SymlinkAndTraversal(t *testing.T) {
	root, outside := symlinkSetup(t)
	w, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(w.draftsPath(), "modules"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(outside, "secret.lua"), filepath.Join(w.draftsPath(), "modules", "l.lua")); err != nil {
		t.Fatal(err)
	}
	drafts, err := w.ListDrafts()
	if err != nil {
		t.Fatal(err)
	}
	if len(drafts) != 0 {
		t.Errorf("symlinked draft listed: %+v", drafts)
	}
	for _, p := range []string{"modules/l.lua", "../../x.lua", "/etc/x.lua", "modules/../a/main.lua"} {
		if err := w.CheckDraftPath(p); err == nil {
			t.Errorf("CheckDraftPath accepted %q", p)
		}
	}
	if err := w.CheckDraftPath("modules/ok.lua"); err != nil {
		t.Errorf("CheckDraftPath rejected a plain draft: %v", err)
	}
}
