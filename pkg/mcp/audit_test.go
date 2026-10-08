package mcp

import (
	"testing"
	"time"
)

func TestAuditLog_AddAndGet(t *testing.T) {
	a := NewAuditLog("")

	a.Add(AuditEntry{Tool: "draft_write", Path: "modules/init.lua", Env: "workspace", Success: true})
	a.Add(AuditEntry{Tool: "draft_test", Path: "modules/init.lua", Env: "sandbox", Success: true})

	entries := a.GetAll()
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(entries))
	}

	// Newest first
	if entries[0].Tool != "draft_test" {
		t.Errorf("expected newest first, got %s", entries[0].Tool)
	}
}

func TestAuditLog_GetRecent(t *testing.T) {
	a := NewAuditLog("")

	for i := 0; i < 10; i++ {
		a.Add(AuditEntry{Tool: "test", Success: true})
	}

	recent := a.GetRecent(3)
	if len(recent) != 3 {
		t.Fatalf("expected 3 entries, got %d", len(recent))
	}
}

func TestAuditLog_RingBuffer(t *testing.T) {
	a := NewAuditLog("")

	for i := 0; i < auditMaxEntries+50; i++ {
		a.Add(AuditEntry{Tool: "test", Success: true})
	}

	entries := a.GetAll()
	if len(entries) != auditMaxEntries {
		t.Fatalf("expected %d entries (ring buffer), got %d", auditMaxEntries, len(entries))
	}
}

func TestAuditLog_CountRecentErrors(t *testing.T) {
	a := NewAuditLog("")

	a.Add(AuditEntry{Tool: "promote", Success: false, Time: time.Now()})
	a.Add(AuditEntry{Tool: "draft_test", Success: true, Time: time.Now()})
	a.Add(AuditEntry{Tool: "promote", Success: false, Time: time.Now()})

	count := a.CountRecentErrors(time.Minute)
	if count != 2 {
		t.Fatalf("expected 2 recent errors, got %d", count)
	}
}
