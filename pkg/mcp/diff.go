package mcp

import (
	"fmt"
	"strings"
)

// unifiedDiff produces a unified diff between two texts.
// Returns empty string if texts are identical.
func unifiedDiff(oldName, newName, oldText, newText string) string {
	oldLines := splitLines(oldText)
	newLines := splitLines(newText)

	// Simple case: one side empty
	if len(oldLines) == 0 && len(newLines) == 0 {
		return ""
	}

	hunks := computeHunks(oldLines, newLines, 3)
	if len(hunks) == 0 {
		return "" // identical
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("--- %s\n", oldName))
	sb.WriteString(fmt.Sprintf("+++ %s\n", newName))

	for _, h := range hunks {
		sb.WriteString(h)
	}

	return sb.String()
}

// splitLines splits text into lines, preserving empty trailing line behavior.
func splitLines(text string) []string {
	if text == "" {
		return nil
	}
	lines := strings.Split(text, "\n")
	// Remove trailing empty element from final newline
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

// computeHunks computes unified diff hunks with context lines.
func computeHunks(oldLines, newLines []string, contextLines int) []string {
	// Compute edit script using LCS
	edits := diffLines(oldLines, newLines)

	if len(edits) == 0 {
		return nil
	}

	// Check if there are any actual changes
	hasChanges := false
	for _, e := range edits {
		if e.op != opEqual {
			hasChanges = true
			break
		}
	}
	if !hasChanges {
		return nil
	}

	// Group into hunks with context
	var hunks []string
	var currentHunk []editOp
	hunkStart := -1
	lastChangeIdx := -1

	for i, e := range edits {
		if e.op != opEqual {
			// Start new hunk if needed
			if hunkStart == -1 {
				// Add leading context
				start := i - contextLines
				if start < 0 {
					start = 0
				}
				hunkStart = start
				for j := start; j < i; j++ {
					currentHunk = append(currentHunk, edits[j])
				}
			} else if i-lastChangeIdx > 2*contextLines {
				// Gap too large — flush current hunk, start new one
				// Add trailing context to current hunk
				end := lastChangeIdx + contextLines + 1
				if end > len(edits) {
					end = len(edits)
				}
				for j := lastChangeIdx + 1; j < end; j++ {
					currentHunk = append(currentHunk, edits[j])
				}
				hunks = append(hunks, formatHunk(currentHunk, hunkStart, oldLines, newLines))

				// Start new hunk with leading context
				currentHunk = nil
				start := i - contextLines
				if start < 0 {
					start = 0
				}
				hunkStart = start
				for j := start; j < i; j++ {
					currentHunk = append(currentHunk, edits[j])
				}
			} else {
				// Fill gap with equal lines
				for j := lastChangeIdx + 1; j < i; j++ {
					currentHunk = append(currentHunk, edits[j])
				}
			}
			currentHunk = append(currentHunk, e)
			lastChangeIdx = i
		}
	}

	// Flush last hunk
	if len(currentHunk) > 0 {
		end := lastChangeIdx + contextLines + 1
		if end > len(edits) {
			end = len(edits)
		}
		for j := lastChangeIdx + 1; j < end; j++ {
			currentHunk = append(currentHunk, edits[j])
		}
		hunks = append(hunks, formatHunk(currentHunk, hunkStart, oldLines, newLines))
	}

	return hunks
}

type diffOp int

const (
	opEqual  diffOp = iota
	opDelete        // line in old, not in new
	opInsert        // line in new, not in old
)

type editOp struct {
	op      diffOp
	oldLine int // 0-based line number in old (for equal/delete)
	newLine int // 0-based line number in new (for equal/insert)
	text    string
}

// diffLines computes the edit operations between old and new lines using LCS.
func diffLines(oldLines, newLines []string) []editOp {
	m, n := len(oldLines), len(newLines)

	// Build LCS table
	lcs := make([][]int, m+1)
	for i := range lcs {
		lcs[i] = make([]int, n+1)
	}
	for i := m - 1; i >= 0; i-- {
		for j := n - 1; j >= 0; j-- {
			if oldLines[i] == newLines[j] {
				lcs[i][j] = lcs[i+1][j+1] + 1
			} else if lcs[i+1][j] >= lcs[i][j+1] {
				lcs[i][j] = lcs[i+1][j]
			} else {
				lcs[i][j] = lcs[i][j+1]
			}
		}
	}

	// Trace back to build edit ops
	var ops []editOp
	i, j := 0, 0
	for i < m || j < n {
		if i < m && j < n && oldLines[i] == newLines[j] {
			ops = append(ops, editOp{op: opEqual, oldLine: i, newLine: j, text: oldLines[i]})
			i++
			j++
		} else if j < n && (i >= m || lcs[i][j+1] >= lcs[i+1][j]) {
			ops = append(ops, editOp{op: opInsert, newLine: j, text: newLines[j]})
			j++
		} else if i < m {
			ops = append(ops, editOp{op: opDelete, oldLine: i, text: oldLines[i]})
			i++
		}
	}

	return ops
}

// formatHunk formats a single unified diff hunk.
func formatHunk(ops []editOp, startIdx int, oldLines, newLines []string) string {
	if len(ops) == 0 {
		return ""
	}

	// Calculate line numbers
	oldStart, newStart := 0, 0
	oldCount, newCount := 0, 0

	first := true
	for _, op := range ops {
		switch op.op {
		case opEqual:
			if first {
				oldStart = op.oldLine + 1
				newStart = op.newLine + 1
				first = false
			}
			oldCount++
			newCount++
		case opDelete:
			if first {
				oldStart = op.oldLine + 1
				newStart = op.newLine + 1
				if newStart == 0 {
					newStart = 1
				}
				first = false
			}
			oldCount++
		case opInsert:
			if first {
				oldStart = op.oldLine + 1
				if oldStart == 0 {
					oldStart = 1
				}
				newStart = op.newLine + 1
				first = false
			}
			newCount++
		}
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("@@ -%d,%d +%d,%d @@\n", oldStart, oldCount, newStart, newCount))

	for _, op := range ops {
		switch op.op {
		case opEqual:
			sb.WriteString(" " + op.text + "\n")
		case opDelete:
			sb.WriteString("-" + op.text + "\n")
		case opInsert:
			sb.WriteString("+" + op.text + "\n")
		}
	}

	return sb.String()
}
