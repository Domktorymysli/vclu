// Package workspace manages draft files and backups for the MCP sandbox pipeline.
// Drafts are written by AI agents and tested in sandbox before promotion to production.
package workspace

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

const (
	maxDrafts   = 20
	maxFileSize = 100 * 1024 // 100KB
	draftsDir   = "workspace/drafts"
	backupsDir  = "workspace/backups"
)

// allowedDraftPath matches paths that agents can write to.
// Only modules/ directory is writable (user.lua and om.lua are OM-generated).
var allowedDraftPath = regexp.MustCompile(`^modules/[a-zA-Z0-9_/]+\.(lua|css)$`)

// DraftInfo describes a draft file.
type DraftInfo struct {
	Path     string    `json:"path"`
	Size     int64     `json:"size"`
	Modified time.Time `json:"modified"`
}

// BackupInfo describes a backup file.
type BackupInfo struct {
	Name    string    `json:"name"`
	Path    string    `json:"path"`
	Size    int64     `json:"size"`
	Created time.Time `json:"created"`
}

// Workspace manages draft and backup files on disk.
type Workspace struct {
	basePath string // root data directory (e.g., data/clu/)
}

// New creates a new Workspace rooted at basePath.
// Creates drafts/ and backups/ directories if they don't exist.
func New(basePath string) (*Workspace, error) {
	w := &Workspace{basePath: basePath}

	// Ensure directories exist
	for _, dir := range []string{w.draftsPath(), w.backupsPath()} {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return nil, fmt.Errorf("failed to create directory %s: %w", dir, err)
		}
	}

	return w, nil
}

func (w *Workspace) draftsPath() string  { return filepath.Join(w.basePath, draftsDir) }
func (w *Workspace) backupsPath() string { return filepath.Join(w.basePath, backupsDir) }

// ValidatePath checks if a path is allowed for drafts.
func ValidatePath(path string) error {
	if path == "" {
		return fmt.Errorf("path is required")
	}
	// Normalize and check for traversal
	clean := filepath.Clean(path)
	if clean != path || strings.Contains(clean, "..") {
		return fmt.Errorf("invalid path: %s", path)
	}
	if !allowedDraftPath.MatchString(clean) {
		return fmt.Errorf("path not allowed (must be modules/*.lua): %s", path)
	}
	return nil
}

// CreateDraft writes content to a draft file.
func (w *Workspace) CreateDraft(path, content string) error {
	if err := ValidatePath(path); err != nil {
		return err
	}
	if len(content) > maxFileSize {
		return fmt.Errorf("file too large: %d bytes (max %d)", len(content), maxFileSize)
	}

	// Check draft count (exclude the file being written)
	drafts, err := w.ListDrafts()
	if err != nil {
		return fmt.Errorf("failed to list drafts: %w", err)
	}
	isUpdate := false
	for _, d := range drafts {
		if d.Path == path {
			isUpdate = true
			break
		}
	}
	if !isUpdate && len(drafts) >= maxDrafts {
		return fmt.Errorf("too many drafts (max %d): delete some first", maxDrafts)
	}

	if err := CheckNoSymlinks(w.draftsPath(), path); err != nil {
		return err
	}
	fullPath := filepath.Join(w.draftsPath(), path)

	// Create parent directories
	if err := os.MkdirAll(filepath.Dir(fullPath), 0755); err != nil {
		return fmt.Errorf("failed to create directory: %w", err)
	}

	return os.WriteFile(fullPath, []byte(content), 0644)
}

// GetDraft reads a draft file.
func (w *Workspace) GetDraft(path string) (string, error) {
	if err := ValidatePath(path); err != nil {
		return "", err
	}

	if err := CheckNoSymlinks(w.draftsPath(), path); err != nil {
		return "", err
	}
	fullPath := filepath.Join(w.draftsPath(), path)
	data, err := os.ReadFile(fullPath)
	if err != nil {
		if os.IsNotExist(err) {
			return "", fmt.Errorf("draft not found: %s", path)
		}
		return "", fmt.Errorf("failed to read draft: %w", err)
	}
	return string(data), nil
}

// ListDrafts returns all draft files.
func (w *Workspace) ListDrafts() ([]DraftInfo, error) {
	var drafts []DraftInfo

	root := w.draftsPath()
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil // skip errors
		}
		if info.IsDir() || info.Mode()&os.ModeSymlink != 0 ||
			(!strings.HasSuffix(path, ".lua") && !strings.HasSuffix(path, ".css")) {
			return nil
		}

		rel, err := filepath.Rel(root, path)
		if err != nil {
			return nil
		}

		drafts = append(drafts, DraftInfo{
			Path:     rel,
			Size:     info.Size(),
			Modified: info.ModTime(),
		})
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("failed to walk drafts: %w", err)
	}

	// Sort by modification time (newest first)
	sort.Slice(drafts, func(i, j int) bool {
		return drafts[i].Modified.After(drafts[j].Modified)
	})

	return drafts, nil
}

// DeleteDraft removes a draft file.
func (w *Workspace) DeleteDraft(path string) error {
	if err := ValidatePath(path); err != nil {
		return err
	}

	if err := CheckNoSymlinks(w.draftsPath(), path); err != nil {
		return err
	}
	fullPath := filepath.Join(w.draftsPath(), path)
	if err := os.Remove(fullPath); err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("draft not found: %s", path)
		}
		return fmt.Errorf("failed to delete draft: %w", err)
	}

	// Clean up empty parent directories
	dir := filepath.Dir(fullPath)
	for dir != w.draftsPath() {
		entries, _ := os.ReadDir(dir)
		if len(entries) > 0 {
			break
		}
		os.Remove(dir)
		dir = filepath.Dir(dir)
	}

	return nil
}

// CreateBackup creates a timestamped backup of a production file before promotion.
// Returns the backup filename.
func (w *Workspace) CreateBackup(path string) (string, error) {
	// Read the production file
	if err := CheckNoSymlinks(w.basePath, path); err != nil {
		return "", err
	}
	prodPath := filepath.Join(w.basePath, path)
	data, err := os.ReadFile(prodPath)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil // No existing file to back up
		}
		return "", fmt.Errorf("failed to read production file: %w", err)
	}

	// Backup filename: 20060102_150405_modules~sub~init.lua; "~" cannot occur in
	// a draft path, so BackupPath can map it back unambiguously
	safeName := strings.ReplaceAll(path, "/", "~")
	var backupName string
	for attempt := 0; ; attempt++ {
		backupName = time.Now().Format("20060102_150405") + "_" + safeName
		f, err := os.OpenFile(filepath.Join(w.backupsPath(), backupName), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0644)
		if os.IsExist(err) && attempt < 3 {
			// same file backed up this second: the name has second resolution
			time.Sleep(time.Until(time.Now().Truncate(time.Second).Add(time.Second)))
			continue
		}
		if err != nil {
			return "", fmt.Errorf("failed to write backup: %w", err)
		}
		_, err = f.Write(data)
		if cerr := f.Close(); err == nil {
			err = cerr
		}
		if err != nil {
			os.Remove(f.Name())
			return "", fmt.Errorf("failed to write backup: %w", err)
		}
		break
	}

	// Auto-cleanup: keep only the newest 50 backups
	w.trimBackups(50)

	return backupName, nil
}

// trimBackups keeps only the newest N backups, deleting the rest.
func (w *Workspace) trimBackups(keep int) {
	dir := w.backupsPath()
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}

	// Filter to files only, sorted by name (timestamp prefix = chronological)
	var files []os.DirEntry
	for _, e := range entries {
		if !e.IsDir() {
			files = append(files, e)
		}
	}
	if len(files) <= keep {
		return
	}

	// Sort ascending by name (oldest first)
	sort.Slice(files, func(i, j int) bool {
		return files[i].Name() < files[j].Name()
	})

	// Delete oldest
	for i := 0; i < len(files)-keep; i++ {
		os.Remove(filepath.Join(dir, files[i].Name()))
	}
}

// ListBackups returns all backup files.
func (w *Workspace) ListBackups() ([]BackupInfo, error) {
	entries, err := os.ReadDir(w.backupsPath())
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to read backups: %w", err)
	}

	var backups []BackupInfo
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}

		name := e.Name()
		origPath, _ := BackupPath(name)

		backups = append(backups, BackupInfo{
			Name:    name,
			Path:    origPath,
			Size:    info.Size(),
			Created: info.ModTime(),
		})
	}

	// Sort by creation time (newest first)
	sort.Slice(backups, func(i, j int) bool {
		return backups[i].Created.After(backups[j].Created)
	})

	return backups, nil
}

// Promote copies a draft to its production path.
// Creates a backup first, then copies the draft.
func (w *Workspace) Promote(path string) (backupName string, err error) {
	if err := ValidatePath(path); err != nil {
		return "", err
	}

	// Read draft
	draftContent, err := w.GetDraft(path)
	if err != nil {
		return "", err
	}

	// Create backup of existing production file
	backupName, err = w.CreateBackup(path)
	if err != nil {
		return "", fmt.Errorf("backup failed: %w", err)
	}

	// Write to production path
	if err := CheckNoSymlinks(w.basePath, path); err != nil {
		return backupName, err
	}
	prodPath := filepath.Join(w.basePath, path)
	if err := os.MkdirAll(filepath.Dir(prodPath), 0755); err != nil {
		return backupName, fmt.Errorf("failed to create production directory: %w", err)
	}
	if err := writeAtomic(prodPath, []byte(draftContent)); err != nil {
		return backupName, fmt.Errorf("failed to write production file: %w", err)
	}

	// Remove the draft after successful promotion
	_ = w.DeleteDraft(path)

	return backupName, nil
}

// BackupPath returns the production path a backup belongs to. Older backups
// used "_" for "/", so for them a "_" in a file name reads as a directory.
func BackupPath(name string) (string, error) {
	if len(name) <= 16 || name[8] != '_' || name[15] != '_' {
		return "", fmt.Errorf("invalid backup name format: %s", name)
	}
	if strings.ContainsAny(name, `/\`) || strings.Contains(name, "..") {
		return "", fmt.Errorf("invalid backup name: %s", name)
	}
	rest := name[16:]
	sep := "~"
	if !strings.Contains(rest, sep) {
		sep = "_"
	}
	path := strings.ReplaceAll(rest, sep, "/")
	if err := ValidatePath(path); err != nil {
		return "", err
	}
	return path, nil
}

// Rollback restores a backup file to its original production path.
func (w *Workspace) Rollback(backupName string) error {
	path, err := BackupPath(backupName)
	if err != nil {
		return err
	}
	return w.restore(backupName, path)
}

// restore writes the content of a backup to path.
func (w *Workspace) restore(backupName, path string) error {
	if err := CheckNoSymlinks(w.backupsPath(), backupName); err != nil {
		return err
	}
	if err := CheckNoSymlinks(w.basePath, path); err != nil {
		return err
	}
	data, err := os.ReadFile(filepath.Join(w.backupsPath(), backupName))
	if err != nil {
		return fmt.Errorf("failed to read backup: %w", err)
	}
	prodPath := filepath.Join(w.basePath, path)
	if err := os.MkdirAll(filepath.Dir(prodPath), 0755); err != nil {
		return fmt.Errorf("failed to create directory: %w", err)
	}
	return writeAtomic(prodPath, data)
}

// writeAtomic replaces path in one step, so a failed write (e.g. a full SD
// card) leaves the previous version in place instead of a truncated file.
func writeAtomic(path string, data []byte) error {
	f, err := os.CreateTemp(filepath.Dir(path), ".promote-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	_, err = f.Write(data)
	if err == nil {
		err = f.Sync()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Chmod(tmp, 0644)
	}
	if err == nil {
		err = os.Rename(tmp, path)
	}
	if err != nil {
		os.Remove(tmp)
	}
	return err
}

// Promoted is one file written by Promote; Backup is empty when the file
// did not exist before.
type Promoted struct {
	Path   string
	Backup string
}

// Undo reverts the given promotions: restores each backup to the path it was
// promoted to, and removes files the promotion created. Returns the failures.
func (w *Workspace) Undo(promoted []Promoted) []string {
	var errs []string
	for _, p := range promoted {
		var err error
		if p.Backup != "" {
			err = w.restore(p.Backup, p.Path)
		} else if err = ValidatePath(p.Path); err == nil {
			if err = CheckNoSymlinks(w.basePath, p.Path); err == nil {
				err = os.Remove(filepath.Join(w.basePath, p.Path))
				if os.IsNotExist(err) {
					err = nil
				}
			}
		}
		if err != nil {
			errs = append(errs, fmt.Sprintf("%s: %v", p.Path, err))
		}
	}
	return errs
}

// CheckDraftPath validates a draft path and refuses symlinks on the way to it.
func (w *Workspace) CheckDraftPath(path string) error {
	if err := ValidatePath(path); err != nil {
		return err
	}
	return CheckNoSymlinks(w.draftsPath(), path)
}

// GetDraftFullPath returns the absolute path to a draft file (for sandbox loading).
func (w *Workspace) GetDraftFullPath(path string) string {
	return filepath.Join(w.draftsPath(), path)
}

// ClearBackups removes all backup files. Returns the number of deleted files.
func (w *Workspace) ClearBackups() (int, error) {
	dir := w.backupsPath()
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, fmt.Errorf("failed to read backups: %w", err)
	}

	count := 0
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if err := os.Remove(filepath.Join(dir, e.Name())); err == nil {
			count++
		}
	}
	return count, nil
}

// GetBackup reads a backup file by name and returns its content and original path.
func (w *Workspace) GetBackup(name string) (content string, origPath string, err error) {
	origPath, err = BackupPath(name)
	if err != nil {
		return "", "", err
	}

	if err := CheckNoSymlinks(w.backupsPath(), name); err != nil {
		return "", "", err
	}
	backupPath := filepath.Join(w.backupsPath(), name)
	data, err := os.ReadFile(backupPath)
	if err != nil {
		if os.IsNotExist(err) {
			return "", "", fmt.Errorf("backup not found: %s", name)
		}
		return "", "", fmt.Errorf("failed to read backup: %w", err)
	}

	return string(data), origPath, nil
}

// GetProductionContent reads a production file (for diff comparison).
func (w *Workspace) GetProductionContent(path string) (string, error) {
	if err := ValidatePath(path); err != nil {
		return "", err
	}
	if err := CheckNoSymlinks(w.basePath, path); err != nil {
		return "", err
	}
	prodPath := filepath.Join(w.basePath, path)
	data, err := os.ReadFile(prodPath)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil // No production file yet
		}
		return "", fmt.Errorf("failed to read production file: %w", err)
	}
	return string(data), nil
}
