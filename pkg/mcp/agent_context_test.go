package mcp

import (
	"strings"
	"testing"
)

func TestAgentContext_ReadEmpty(t *testing.T) {
	dir := t.TempDir()
	ac := NewAgentContext(dir)

	content, err := ac.Read()
	if err != nil {
		t.Fatalf("Read failed: %v", err)
	}
	if content != "" {
		t.Errorf("expected empty string, got %q", content)
	}
}

func TestAgentContext_AppendAndRead(t *testing.T) {
	dir := t.TempDir()
	ac := NewAgentContext(dir)

	err := ac.Append("- Found bug in timer logic\n- TODO: add cleanup")
	if err != nil {
		t.Fatalf("Append failed: %v", err)
	}

	content, err := ac.Read()
	if err != nil {
		t.Fatalf("Read failed: %v", err)
	}
	if !strings.Contains(content, "Found bug") {
		t.Error("expected notes content")
	}
	if !strings.Contains(content, "## Session") {
		t.Error("expected session header")
	}
}

func TestAgentContext_AppendMultiple(t *testing.T) {
	dir := t.TempDir()
	ac := NewAgentContext(dir)

	ac.Append("Session 1 notes")
	ac.Append("Session 2 notes")

	content, err := ac.Read()
	if err != nil {
		t.Fatalf("Read failed: %v", err)
	}
	if !strings.Contains(content, "Session 1") {
		t.Error("expected session 1 notes")
	}
	if !strings.Contains(content, "Session 2") {
		t.Error("expected session 2 notes")
	}
}

func TestAgentContext_SizeLimit(t *testing.T) {
	dir := t.TempDir()
	ac := NewAgentContext(dir)

	// Write a large initial block
	big := strings.Repeat("x", maxContextSize-100)
	ac.Append(big)

	// Try to append more — should fail
	err := ac.Append(strings.Repeat("y", 200))
	if err == nil {
		t.Fatal("expected error for size limit exceeded")
	}
}

func TestAgentContext_Clear(t *testing.T) {
	dir := t.TempDir()
	ac := NewAgentContext(dir)

	ac.Append("some notes")
	ac.Clear()

	content, err := ac.Read()
	if err != nil {
		t.Fatalf("Read failed: %v", err)
	}
	if content != "" {
		t.Errorf("expected empty after clear, got %q", content)
	}
}
