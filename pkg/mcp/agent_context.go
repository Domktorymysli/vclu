package mcp

import (
	"fmt"
	"os"
	"path/filepath"
	"time"
)

const (
	agentContextFile = "workspace/agent-context.md"
	maxContextSize   = 50 * 1024 // 50KB
)

// AgentContext manages the agent's persistent notes file.
type AgentContext struct {
	baseDir string
}

// NewAgentContext creates a new agent context manager.
func NewAgentContext(baseDir string) *AgentContext {
	// Ensure directory exists
	dir := filepath.Dir(filepath.Join(baseDir, agentContextFile))
	os.MkdirAll(dir, 0755)
	return &AgentContext{baseDir: baseDir}
}

func (ac *AgentContext) path() string {
	return filepath.Join(ac.baseDir, agentContextFile)
}

// Read returns the current agent notes.
func (ac *AgentContext) Read() (string, error) {
	data, err := os.ReadFile(ac.path())
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", err
	}
	return string(data), nil
}

// Append adds notes to the agent context file.
func (ac *AgentContext) Append(notes string) error {
	existing, _ := ac.Read()

	// Add session header
	header := fmt.Sprintf("\n## Session %s\n", time.Now().Format("2006-01-02 15:04"))
	newContent := existing + header + notes + "\n"

	if len(newContent) > maxContextSize {
		return fmt.Errorf("context file would exceed %dKB limit (current: %dKB, adding: %dKB)",
			maxContextSize/1024, len(existing)/1024, len(notes)/1024)
	}

	return os.WriteFile(ac.path(), []byte(newContent), 0644)
}

// Clear removes the agent context file.
func (ac *AgentContext) Clear() error {
	err := os.Remove(ac.path())
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}
