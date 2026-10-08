package workspace

import (
	"os"
	"path/filepath"
	"testing"
)

func prodFile(t *testing.T, root, rel string) (string, bool) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, rel))
	if os.IsNotExist(err) {
		return "", false
	}
	if err != nil {
		t.Fatal(err)
	}
	return string(data), true
}

func TestRollback_UnderscoresInNames(t *testing.T) {
	root := t.TempDir()
	w, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"modules/foo_bar.lua", "modules/my_dir/sub_mod.lua"} {
		if err := os.MkdirAll(filepath.Dir(filepath.Join(root, p)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, p), []byte("OLD"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := w.CreateDraft(p, "NEW"); err != nil {
			t.Fatal(err)
		}
		backup, err := w.Promote(p)
		if err != nil {
			t.Fatal(err)
		}
		if got, err := BackupPath(backup); err != nil || got != p {
			t.Errorf("BackupPath(%s) = %q, %v; want %s", backup, got, err, p)
		}
		if err := w.Rollback(backup); err != nil {
			t.Fatal(err)
		}
		if c, _ := prodFile(t, root, p); c != "OLD" {
			t.Errorf("%s after rollback: %q", p, c)
		}
	}
	if _, err := os.Stat(filepath.Join(root, "modules", "foo")); err == nil {
		t.Error("rollback created modules/foo/")
	}
}

func TestBackupPath_Formats(t *testing.T) {
	for name, want := range map[string]string{
		"20260101_120000_modules_init.lua":       "modules/init.lua", // legacy
		"20260101_120000_modules_energia_ha.lua": "modules/energia/ha.lua",
		"20260101_120000_modules~foo_bar.lua":    "modules/foo_bar.lua",
	} {
		if got, err := BackupPath(name); err != nil || got != want {
			t.Errorf("BackupPath(%s) = %q, %v; want %s", name, got, err, want)
		}
	}
}

func TestUndo_RestoresAndRemovesCreated(t *testing.T) {
	root := t.TempDir()
	w, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "modules"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "modules", "old_one.lua"), []byte("OLD"), 0o644); err != nil {
		t.Fatal(err)
	}
	var promoted []Promoted
	for _, p := range []string{"modules/old_one.lua", "modules/brand_new.lua"} {
		if err := w.CreateDraft(p, "NEW"); err != nil {
			t.Fatal(err)
		}
		backup, err := w.Promote(p)
		if err != nil {
			t.Fatal(err)
		}
		promoted = append(promoted, Promoted{Path: p, Backup: backup})
	}
	if promoted[1].Backup != "" {
		t.Fatalf("new file got a backup: %s", promoted[1].Backup)
	}
	if errs := w.Undo(promoted); len(errs) > 0 {
		t.Fatal(errs)
	}
	if c, _ := prodFile(t, root, "modules/old_one.lua"); c != "OLD" {
		t.Errorf("old_one.lua after undo: %q", c)
	}
	if _, ok := prodFile(t, root, "modules/brand_new.lua"); ok {
		t.Error("brand_new.lua created by the promote is still there")
	}

	// a failure is reported, not swallowed
	if errs := w.Undo([]Promoted{{Path: "modules/x.lua", Backup: "20260101_120000_modules~x.lua"}}); len(errs) != 1 {
		t.Errorf("missing backup: errs = %v", errs)
	}
}

func TestCreateBackup_SameSecondKeepsBoth(t *testing.T) {
	root := t.TempDir()
	w, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	p := "modules/a.lua"
	if err := os.MkdirAll(filepath.Join(root, "modules"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, p), []byte("V0"), 0o644); err != nil {
		t.Fatal(err)
	}
	var backups []string
	for _, v := range []string{"V1", "V2"} {
		if err := w.CreateDraft(p, v); err != nil {
			t.Fatal(err)
		}
		b, err := w.Promote(p)
		if err != nil {
			t.Fatal(err)
		}
		backups = append(backups, b)
	}
	if backups[0] == backups[1] {
		t.Fatalf("both promotes got backup %s", backups[0])
	}
	if err := w.Rollback(backups[0]); err != nil {
		t.Fatal(err)
	}
	if c, _ := prodFile(t, root, p); c != "V0" {
		t.Errorf("rollback of the first promote restored %q, want V0", c)
	}
}

func TestPromote_FailedWriteKeepsOldVersion(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory permissions")
	}
	root := t.TempDir()
	w, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(root, "modules")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "a.lua"), []byte("OLD"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := w.CreateDraft("modules/a.lua", "NEW"); err != nil {
		t.Fatal(err)
	}
	// no new files in modules/: the temp file cannot be created
	if err := os.Chmod(dir, 0o555); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(dir, 0o755)
	if _, err := w.Promote("modules/a.lua"); err == nil {
		t.Fatal("promote should fail")
	}
	if c, _ := prodFile(t, root, "modules/a.lua"); c != "OLD" {
		t.Errorf("production after a failed promote: %q", c)
	}
}
