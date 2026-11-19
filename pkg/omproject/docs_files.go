package omproject

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// WriteDocs replaces the agent's files about the project in DocsDir under root. They are
// written next to it first, so a failed import leaves the old ones whole.
func (p *Project) WriteDocs(root string) error {
	dir := filepath.Join(root, filepath.FromSlash(DocsDir))
	parent := filepath.Dir(dir)
	if err := os.MkdirAll(parent, 0755); err != nil {
		return fmt.Errorf("failed to create %s: %v", DocsDir, err)
	}
	// what an import cut short left behind
	if old, _ := filepath.Glob(filepath.Join(parent, ".om-*")); len(old) > 0 {
		for _, o := range old {
			_ = os.RemoveAll(o)
		}
	}
	tmp, err := os.MkdirTemp(parent, ".om-")
	if err != nil {
		return fmt.Errorf("failed to create %s: %v", DocsDir, err)
	}
	defer func() { _ = os.RemoveAll(tmp) }()
	if err := os.Chmod(tmp, 0755); err != nil {
		return fmt.Errorf("failed to create %s: %v", DocsDir, err)
	}
	for name, content := range p.Docs() {
		if err := os.WriteFile(filepath.Join(tmp, name), []byte(content), 0644); err != nil {
			return fmt.Errorf("failed to write %s/%s: %v", DocsDir, name, err)
		}
	}
	if err := RemoveDocs(root); err != nil {
		return err
	}
	if err := os.Rename(tmp, dir); err != nil {
		return fmt.Errorf("failed to replace %s: %v", DocsDir, err)
	}
	return nil
}

// RemoveDocs removes the agent's files about the project under root.
func RemoveDocs(root string) error {
	if err := os.RemoveAll(filepath.Join(root, filepath.FromSlash(DocsDir))); err != nil {
		return fmt.Errorf("failed to remove %s: %v", DocsDir, err)
	}
	return nil
}

// ForgetDocs removes what the docs under root say about a CLU that is deleted: its files,
// and its row in the index, which then says the CLU was removed.
func ForgetDocs(root, id string) error {
	dir := filepath.Join(root, filepath.FromSlash(DocsDir))
	files, _ := filepath.Glob(filepath.Join(dir, id+"*.md"))
	for _, f := range files {
		if name := filepath.Base(f); name == id+".md" || strings.HasPrefix(name, id+"-scripts") {
			if err := os.Remove(f); err != nil {
				return fmt.Errorf("%s/%s: %v", DocsDir, name, err)
			}
		}
	}
	idx := filepath.Join(dir, "index.md")
	b, err := os.ReadFile(idx)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("%s/index.md: %v", DocsDir, err)
	}
	return os.WriteFile(idx, []byte(ForgetCLU(string(b), id)), 0644)
}
