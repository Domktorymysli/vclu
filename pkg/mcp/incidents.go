package mcp

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const (
	incidentsDir      = "docs/ai/incidents"
	maxIncidents      = 50
	incidentRetention = 30 * 24 * time.Hour // 30 days
)

// IncidentSnapshot captures the system state at the time of an incident.
type IncidentSnapshot struct {
	Timestamp    time.Time      `json:"timestamp"`
	Trigger      string         `json:"trigger"` // "promote" (modules failed to load); older files: "rollback", "watchdog"
	ErrorMsg     string         `json:"error_msg"`
	CodeDiff     string         `json:"code_diff,omitempty"`
	Paths        []string       `json:"paths,omitempty"`
	RecentLogs   []string       `json:"recent_logs"`
	AuditTrail   []AuditEntry   `json:"audit_trail"`
	DeviceStates map[string]any `json:"device_states,omitempty"`
	BlockedCalls []BlockedCall  `json:"blocked_calls,omitempty"`
}

// IncidentInfo describes an incident file (for listing).
type IncidentInfo struct {
	ID        string    `json:"id"`
	Trigger   string    `json:"trigger"`
	ErrorMsg  string    `json:"error_msg"`
	Timestamp time.Time `json:"timestamp"`
	Size      int64     `json:"size"`
}

// IncidentStore manages incident files on disk.
type IncidentStore struct {
	baseDir string // e.g., data/clu/
}

// NewIncidentStore creates a new incident store.
func NewIncidentStore(baseDir string) *IncidentStore {
	dir := filepath.Join(baseDir, incidentsDir)
	os.MkdirAll(dir, 0755)
	return &IncidentStore{baseDir: baseDir}
}

func (is *IncidentStore) dir() string {
	return filepath.Join(is.baseDir, incidentsDir)
}

// Save writes an incident snapshot to disk as markdown.
func (is *IncidentStore) Save(snap IncidentSnapshot) (string, error) {
	id := snap.Timestamp.Format("2006-01-02T15-04-05")

	md := is.renderMarkdown(snap)
	path := filepath.Join(is.dir(), id+".md")
	if err := os.WriteFile(path, []byte(md), 0644); err != nil {
		return "", fmt.Errorf("failed to write incident: %w", err)
	}

	// Update latest.md symlink (or copy for portability)
	latestPath := filepath.Join(is.dir(), "latest.md")
	os.Remove(latestPath)
	os.WriteFile(latestPath, []byte(md), 0644)

	// Enforce retention
	is.cleanup()

	log.Printf("[MCP] Incident saved: %s (%s)", id, snap.Trigger)
	return id, nil
}

// GetLatest reads the latest incident file.
func (is *IncidentStore) GetLatest() (string, error) {
	data, err := os.ReadFile(filepath.Join(is.dir(), "latest.md"))
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", err
	}
	return string(data), nil
}

// Get reads an incident file by ID.
func (is *IncidentStore) Get(id string) (string, error) {
	// Sanitize: only allow timestamp-like IDs
	if strings.Contains(id, "/") || strings.Contains(id, "..") {
		return "", fmt.Errorf("invalid incident ID")
	}
	name := id
	if !strings.HasSuffix(name, ".md") {
		name += ".md"
	}
	data, err := os.ReadFile(filepath.Join(is.dir(), name))
	if err != nil {
		return "", fmt.Errorf("incident not found: %s", id)
	}
	return string(data), nil
}

// List returns all incidents (newest first).
func (is *IncidentStore) List() []IncidentInfo {
	entries, err := os.ReadDir(is.dir())
	if err != nil {
		return nil
	}

	var incidents []IncidentInfo
	for _, e := range entries {
		if e.IsDir() || e.Name() == "latest.md" || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}

		id := strings.TrimSuffix(e.Name(), ".md")
		ts, _ := time.Parse("2006-01-02T15-04-05", id)

		// Read first few lines to get trigger/error
		trigger, errorMsg := is.parseHeader(filepath.Join(is.dir(), e.Name()))

		incidents = append(incidents, IncidentInfo{
			ID:        id,
			Trigger:   trigger,
			ErrorMsg:  errorMsg,
			Timestamp: ts,
			Size:      info.Size(),
		})
	}

	sort.Slice(incidents, func(i, j int) bool {
		return incidents[i].Timestamp.After(incidents[j].Timestamp)
	})

	return incidents
}

// CountRecent returns how many incidents happened in the given window.
func (is *IncidentStore) CountRecent(window time.Duration) int {
	cutoff := time.Now().Add(-window)
	count := 0
	for _, inc := range is.List() {
		if inc.Timestamp.After(cutoff) {
			count++
		}
	}
	return count
}

// renderMarkdown converts an IncidentSnapshot to a readable markdown file.
func (is *IncidentStore) renderMarkdown(snap IncidentSnapshot) string {
	var b strings.Builder

	b.WriteString(fmt.Sprintf("# Incident: %s @ %s\n\n", snap.Trigger, snap.Timestamp.Format("2006-01-02 15:04:05")))

	b.WriteString("## Trigger\n")
	b.WriteString(snap.ErrorMsg + "\n\n")

	if len(snap.Paths) > 0 {
		b.WriteString("## Files\n")
		for _, p := range snap.Paths {
			b.WriteString("- " + p + "\n")
		}
		b.WriteString("\n")
	}

	if snap.CodeDiff != "" {
		b.WriteString("## Code Diff\n```diff\n")
		b.WriteString(snap.CodeDiff)
		b.WriteString("\n```\n\n")
	}

	if len(snap.DeviceStates) > 0 {
		b.WriteString("## Device States at Incident\n")
		for path, state := range snap.DeviceStates {
			stateJSON, _ := json.Marshal(state)
			b.WriteString(fmt.Sprintf("- %s = %s\n", path, string(stateJSON)))
		}
		b.WriteString("\n")
	}

	if len(snap.RecentLogs) > 0 {
		b.WriteString(fmt.Sprintf("## Recent Logs (last %d)\n", len(snap.RecentLogs)))
		for _, l := range snap.RecentLogs {
			b.WriteString("- " + l + "\n")
		}
		b.WriteString("\n")
	}

	if len(snap.BlockedCalls) > 0 {
		b.WriteString("## Blocked Calls\n")
		for _, bc := range snap.BlockedCalls {
			argsJSON, _ := json.Marshal(bc.Args)
			b.WriteString(fmt.Sprintf("- %s: %s\n", bc.Type, string(argsJSON)))
		}
		b.WriteString("\n")
	}

	if len(snap.AuditTrail) > 0 {
		b.WriteString("## Audit Trail\n")
		for _, a := range snap.AuditTrail {
			status := "OK"
			if !a.Success {
				status = "FAIL"
			}
			path := a.Path
			if len(a.Paths) > 0 {
				path = strings.Join(a.Paths, ", ")
			}
			b.WriteString(fmt.Sprintf("- %s %s %s [%s] %s\n",
				a.Time.Format("15:04:05"), a.Tool, path, status, a.Details))
		}
		b.WriteString("\n")
	}

	return b.String()
}

// parseHeader reads first lines of an incident file to extract trigger and error.
func (is *IncidentStore) parseHeader(path string) (trigger, errorMsg string) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", ""
	}
	lines := strings.SplitN(string(data), "\n", 10)
	for _, line := range lines {
		if strings.HasPrefix(line, "# Incident: ") {
			parts := strings.SplitN(strings.TrimPrefix(line, "# Incident: "), " @ ", 2)
			if len(parts) >= 1 {
				trigger = parts[0]
			}
		}
		if strings.HasPrefix(line, "## Trigger") {
			continue
		}
		if trigger != "" && errorMsg == "" && line != "" && !strings.HasPrefix(line, "#") {
			errorMsg = line
		}
	}
	return
}

// cleanup removes old incidents beyond retention and max count.
func (is *IncidentStore) cleanup() {
	incidents := is.List()
	cutoff := time.Now().Add(-incidentRetention)

	for i, inc := range incidents {
		remove := false
		if i >= maxIncidents {
			remove = true
		}
		if !inc.Timestamp.IsZero() && inc.Timestamp.Before(cutoff) {
			remove = true
		}
		if remove {
			os.Remove(filepath.Join(is.dir(), inc.ID+".md"))
		}
	}
}
