package mcp

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestIncidentStore_SaveAndGet(t *testing.T) {
	dir := t.TempDir()
	store := NewIncidentStore(dir)

	snap := IncidentSnapshot{
		Timestamp: time.Now(),
		Trigger:   "rollback",
		ErrorMsg:  "Reinitialize failed: attempt to index nil",
		Paths:     []string{"modules/init.lua"},
		RecentLogs: []string{
			"14:30:00 [ERROR] Reinitialize failed",
		},
		AuditTrail: []AuditEntry{
			{Time: time.Now(), Tool: "draft_write", Path: "modules/init.lua", Success: true},
		},
	}

	id, err := store.Save(snap)
	if err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	if id == "" {
		t.Fatal("expected non-empty ID")
	}

	// Get by ID
	content, err := store.Get(id)
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}
	if !strings.Contains(content, "rollback") {
		t.Error("expected 'rollback' in content")
	}
	if !strings.Contains(content, "modules/init.lua") {
		t.Error("expected file path in content")
	}

	// Get latest
	latest, err := store.GetLatest()
	if err != nil {
		t.Fatalf("GetLatest failed: %v", err)
	}
	if latest != content {
		t.Error("expected latest to match saved incident")
	}
}

func TestIncidentStore_List(t *testing.T) {
	dir := t.TempDir()
	store := NewIncidentStore(dir)

	// Save 3 incidents
	for i := 0; i < 3; i++ {
		snap := IncidentSnapshot{
			Timestamp: time.Now().Add(time.Duration(i) * time.Second),
			Trigger:   "watchdog",
			ErrorMsg:  "test error",
		}
		if _, err := store.Save(snap); err != nil {
			t.Fatalf("Save %d failed: %v", i, err)
		}
		time.Sleep(time.Second) // Ensure different timestamps
	}

	list := store.List()
	if len(list) != 3 {
		t.Fatalf("expected 3 incidents, got %d", len(list))
	}

	// Newest first
	if list[0].Timestamp.Before(list[1].Timestamp) {
		t.Error("expected newest first")
	}
}

func TestIncidentStore_CountRecent(t *testing.T) {
	dir := t.TempDir()
	store := NewIncidentStore(dir)

	snap := IncidentSnapshot{
		Timestamp: time.Now(),
		Trigger:   "rollback",
		ErrorMsg:  "test",
	}
	store.Save(snap)

	count := store.CountRecent(7 * 24 * time.Hour)
	if count != 1 {
		t.Fatalf("expected 1 recent incident, got %d", count)
	}
}

func TestIncidentStore_GetInvalidID(t *testing.T) {
	dir := t.TempDir()
	store := NewIncidentStore(dir)

	_, err := store.Get("../../../etc/passwd")
	if err == nil {
		t.Fatal("expected error for path traversal")
	}
}

func TestIncidentStore_Cleanup(t *testing.T) {
	dir := t.TempDir()
	store := NewIncidentStore(dir)

	// Create more than maxIncidents
	incDir := filepath.Join(dir, incidentsDir)
	for i := 0; i < maxIncidents+5; i++ {
		ts := time.Now().Add(-time.Duration(i) * time.Hour)
		name := ts.Format("2006-01-02T15-04-05") + ".md"
		os.WriteFile(filepath.Join(incDir, name), []byte("# test"), 0644)
	}

	// Trigger cleanup via save
	store.Save(IncidentSnapshot{
		Timestamp: time.Now(),
		Trigger:   "test",
		ErrorMsg:  "cleanup test",
	})

	list := store.List()
	if len(list) > maxIncidents {
		t.Errorf("expected at most %d incidents after cleanup, got %d", maxIncidents, len(list))
	}
}
