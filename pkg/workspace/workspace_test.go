package workspace

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func setupTestWorkspace(t *testing.T) (*Workspace, string) {
	t.Helper()
	tmpDir := t.TempDir()
	ws, err := New(tmpDir)
	if err != nil {
		t.Fatalf("failed to create workspace: %v", err)
	}
	return ws, tmpDir
}

func TestValidatePath(t *testing.T) {
	tests := []struct {
		path  string
		valid bool
	}{
		{"modules/init.lua", true},
		{"modules/scenes/movie.lua", true},
		{"modules/automations/motion.lua", true},
		{"modules/helpers/utils.lua", true},
		// Invalid paths
		{"user.lua", false},
		{"om.lua", false},
		{"a/user.lua", false},
		{"../etc/passwd", false},
		{"modules/../../../etc/passwd", false},
		{"modules/init.js", false},
		{"", false},
		{"modules/.lua", false},
	}

	for _, tt := range tests {
		err := ValidatePath(tt.path)
		if (err == nil) != tt.valid {
			t.Errorf("ValidatePath(%q) = err:%v, want valid:%v", tt.path, err, tt.valid)
		}
	}
}

func TestCreateAndGetDraft(t *testing.T) {
	ws, _ := setupTestWorkspace(t)

	content := `-- Test module
local function hello()
    print("hello")
end
`
	// Create
	if err := ws.CreateDraft("modules/init.lua", content); err != nil {
		t.Fatalf("CreateDraft failed: %v", err)
	}

	// Get
	got, err := ws.GetDraft("modules/init.lua")
	if err != nil {
		t.Fatalf("GetDraft failed: %v", err)
	}
	if got != content {
		t.Errorf("GetDraft returned wrong content")
	}
}

func TestListDrafts(t *testing.T) {
	ws, _ := setupTestWorkspace(t)

	ws.CreateDraft("modules/init.lua", "-- init")
	ws.CreateDraft("modules/scenes/movie.lua", "-- movie")

	drafts, err := ws.ListDrafts()
	if err != nil {
		t.Fatalf("ListDrafts failed: %v", err)
	}
	if len(drafts) != 2 {
		t.Fatalf("expected 2 drafts, got %d", len(drafts))
	}
}

func TestDeleteDraft(t *testing.T) {
	ws, _ := setupTestWorkspace(t)

	ws.CreateDraft("modules/init.lua", "-- init")

	if err := ws.DeleteDraft("modules/init.lua"); err != nil {
		t.Fatalf("DeleteDraft failed: %v", err)
	}

	_, err := ws.GetDraft("modules/init.lua")
	if err == nil {
		t.Fatal("expected error after delete")
	}
}

func TestDraftMaxCount(t *testing.T) {
	ws, _ := setupTestWorkspace(t)

	// Create maxDrafts files
	for i := 0; i < maxDrafts; i++ {
		path := "modules/test_" + strings.Repeat("a", 1) + string(rune('a'+i)) + ".lua"
		if i >= 26 {
			path = "modules/scenes/test_" + string(rune('a'+i-26)) + ".lua"
		}
		if err := ws.CreateDraft(path, "-- test"); err != nil {
			t.Fatalf("CreateDraft #%d failed: %v", i, err)
		}
	}

	// 21st should fail
	err := ws.CreateDraft("modules/overflow.lua", "-- too many")
	if err == nil {
		t.Fatal("expected error for too many drafts")
	}
}

func TestDraftMaxSize(t *testing.T) {
	ws, _ := setupTestWorkspace(t)

	bigContent := strings.Repeat("x", maxFileSize+1)
	err := ws.CreateDraft("modules/big.lua", bigContent)
	if err == nil {
		t.Fatal("expected error for oversized file")
	}
}

func TestPromoteAndBackup(t *testing.T) {
	ws, tmpDir := setupTestWorkspace(t)

	// Create a "production" file first
	prodPath := filepath.Join(tmpDir, "modules")
	os.MkdirAll(prodPath, 0755)
	os.WriteFile(filepath.Join(prodPath, "init.lua"), []byte("-- old production"), 0644)

	// Create draft
	ws.CreateDraft("modules/init.lua", "-- new draft")

	// Promote
	backupName, err := ws.Promote("modules/init.lua")
	if err != nil {
		t.Fatalf("Promote failed: %v", err)
	}
	if backupName == "" {
		t.Fatal("expected backup name")
	}

	// Verify production was updated
	prod, err := os.ReadFile(filepath.Join(prodPath, "init.lua"))
	if err != nil {
		t.Fatalf("failed to read production: %v", err)
	}
	if string(prod) != "-- new draft" {
		t.Errorf("production not updated: %s", string(prod))
	}

	// Verify backup exists
	backups, err := ws.ListBackups()
	if err != nil {
		t.Fatalf("ListBackups failed: %v", err)
	}
	if len(backups) != 1 {
		t.Fatalf("expected 1 backup, got %d", len(backups))
	}

	// Rollback
	if err := ws.Rollback(backupName); err != nil {
		t.Fatalf("Rollback failed: %v", err)
	}

	// Verify production was restored
	prod, _ = os.ReadFile(filepath.Join(prodPath, "init.lua"))
	if string(prod) != "-- old production" {
		t.Errorf("rollback failed: %s", string(prod))
	}
}

func TestGetDraftNotFound(t *testing.T) {
	ws, _ := setupTestWorkspace(t)

	_, err := ws.GetDraft("modules/nonexistent.lua")
	if err == nil {
		t.Fatal("expected error for nonexistent draft")
	}
}

func TestUpdateExistingDraft(t *testing.T) {
	ws, _ := setupTestWorkspace(t)

	ws.CreateDraft("modules/init.lua", "-- v1")
	ws.CreateDraft("modules/init.lua", "-- v2")

	content, _ := ws.GetDraft("modules/init.lua")
	if content != "-- v2" {
		t.Errorf("expected v2, got: %s", content)
	}

	// Should still be 1 draft, not 2
	drafts, _ := ws.ListDrafts()
	if len(drafts) != 1 {
		t.Errorf("expected 1 draft after update, got %d", len(drafts))
	}
}
