package mcp

import (
	"strings"
	"testing"
)

func TestUnifiedDiff_Identical(t *testing.T) {
	text := "line1\nline2\nline3\n"
	diff := unifiedDiff("a/file", "b/file", text, text)
	if diff != "" {
		t.Errorf("expected empty diff for identical files, got: %s", diff)
	}
}

func TestUnifiedDiff_AllNew(t *testing.T) {
	diff := unifiedDiff("a/file", "b/file", "", "line1\nline2\n")
	if diff == "" {
		t.Fatal("expected non-empty diff")
	}
	if !strings.Contains(diff, "+line1") {
		t.Errorf("expected +line1 in diff: %s", diff)
	}
	if !strings.Contains(diff, "+line2") {
		t.Errorf("expected +line2 in diff: %s", diff)
	}
}

func TestUnifiedDiff_AllRemoved(t *testing.T) {
	diff := unifiedDiff("a/file", "b/file", "line1\nline2\n", "")
	if diff == "" {
		t.Fatal("expected non-empty diff")
	}
	if !strings.Contains(diff, "-line1") {
		t.Errorf("expected -line1 in diff: %s", diff)
	}
}

func TestUnifiedDiff_Modification(t *testing.T) {
	old := "line1\nline2\nline3\n"
	new := "line1\nmodified\nline3\n"
	diff := unifiedDiff("a/file", "b/file", old, new)
	if diff == "" {
		t.Fatal("expected non-empty diff")
	}
	if !strings.Contains(diff, "-line2") {
		t.Errorf("expected -line2 in diff: %s", diff)
	}
	if !strings.Contains(diff, "+modified") {
		t.Errorf("expected +modified in diff: %s", diff)
	}
	// Context line should be present
	if !strings.Contains(diff, " line1") {
		t.Errorf("expected context line1 in diff: %s", diff)
	}
}

func TestUnifiedDiff_Addition(t *testing.T) {
	old := "line1\nline2\n"
	new := "line1\nline2\nline3\nline4\n"
	diff := unifiedDiff("a/file", "b/file", old, new)
	if !strings.Contains(diff, "+line3") {
		t.Errorf("expected +line3: %s", diff)
	}
	if !strings.Contains(diff, "+line4") {
		t.Errorf("expected +line4: %s", diff)
	}
}

func TestUnifiedDiff_Headers(t *testing.T) {
	diff := unifiedDiff("a/modules/init.lua (production)", "b/modules/init.lua (draft)", "old\n", "new\n")
	if !strings.HasPrefix(diff, "--- a/modules/init.lua (production)") {
		t.Errorf("expected --- header: %s", diff)
	}
	if !strings.Contains(diff, "+++ b/modules/init.lua (draft)") {
		t.Errorf("expected +++ header: %s", diff)
	}
}
