package web

import (
	"archive/zip"
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/Domktorymysli/vclu/pkg/config"
	"github.com/gin-gonic/gin"
)

// BackupInfo represents information about a backup file
type BackupInfo struct {
	Filename  string `json:"filename"`
	Size      int64  `json:"size"`
	SizeHuman string `json:"sizeHuman"`
	Created   string `json:"created"`
	CreatedAt int64  `json:"createdAt"`
}

// backupSchedulerState holds the cancel func and mutex for the auto-backup scheduler
type backupSchedulerState struct {
	cancel context.CancelFunc
	mu     sync.Mutex
}

// handleBackup shows the backup page
func (s *Server) handleBackup(c *gin.Context) {
	trans := s.getTranslations(c)
	s.renderHTML(c, http.StatusOK, "backup.html", gin.H{
		"title":           "Backup",
		"currentPage":     "backup",
		"configured":      s.isConfigured,
		"supportsModules": s.supportsModules,
		"lang":            trans["lang"],
		"t":               trans["t"],
		"langs":           trans["langs"],
	})
}

// handleAPIBackupCreate creates a new backup
//
//	@Summary		Create a backup
//	@Description	Creates a timestamped ZIP archive of the data directory in the sibling backup directory. When a backup with the same minute timestamp already exists, seconds are appended to keep the filename unique.
//	@Tags			backup
//	@Produce		json
//	@Success		200	{object}	map[string]interface{}	"success, filename, size, sizeHuman"
//	@Failure		500	{object}	web.APIError
//	@Security		ApiKeyAuth
//	@Router			/api/backup/create [post]
func (s *Server) handleAPIBackupCreate(c *gin.Context) {
	// Create backup directory if it doesn't exist
	backupDir := filepath.Join(filepath.Dir(s.serverDir), "backup")
	if err := os.MkdirAll(backupDir, 0755); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("Failed to create backup directory: %v", err)})
		return
	}

	// Generate filename with timestamp (YYYY-MM-DD_HH-MM)
	timestamp := time.Now().Format("2006-01-02_15-04")
	filename := fmt.Sprintf("backup_%s.zip", timestamp)
	zipPath := filepath.Join(backupDir, filename)

	// Check if file already exists (backup in same minute)
	if _, err := os.Stat(zipPath); err == nil {
		// Add seconds to make unique
		timestamp = time.Now().Format("2006-01-02_15-04-05")
		filename = fmt.Sprintf("backup_%s.zip", timestamp)
		zipPath = filepath.Join(backupDir, filename)
	}

	// Create ZIP file
	if err := createBackupZip(zipPath, s.serverDir); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("Failed to create backup: %v", err)})
		return
	}

	// Get file info
	info, err := os.Stat(zipPath)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Backup created but failed to get info"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success":   true,
		"filename":  filename,
		"size":      info.Size(),
		"sizeHuman": humanizeBytes(info.Size()),
	})
}

// handleAPIBackupList returns list of available backups
//
//	@Summary		List backups
//	@Description	Returns all ZIP backups in the backup directory with size and creation time, sorted newest first. Returns an empty list when the directory does not exist yet.
//	@Tags			backup
//	@Produce		json
//	@Success		200	{object}	map[string]interface{}	"backups []web.BackupInfo {filename, size, sizeHuman, created, createdAt}"
//	@Failure		500	{object}	web.APIError
//	@Security		ApiKeyAuth
//	@Router			/api/backup/list [get]
func (s *Server) handleAPIBackupList(c *gin.Context) {
	backupDir := filepath.Join(filepath.Dir(s.serverDir), "backup")

	// Check if backup directory exists
	if _, err := os.Stat(backupDir); os.IsNotExist(err) {
		c.JSON(http.StatusOK, gin.H{"backups": []BackupInfo{}})
		return
	}

	// Read directory
	entries, err := os.ReadDir(backupDir)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("Failed to read backup directory: %v", err)})
		return
	}

	var backups []BackupInfo
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".zip") {
			continue
		}

		info, err := entry.Info()
		if err != nil {
			continue
		}

		backups = append(backups, BackupInfo{
			Filename:  entry.Name(),
			Size:      info.Size(),
			SizeHuman: humanizeBytes(info.Size()),
			Created:   info.ModTime().Format("2006-01-02 15:04:05"),
			CreatedAt: info.ModTime().Unix(),
		})
	}

	// Sort by creation time (newest first)
	sort.Slice(backups, func(i, j int) bool {
		return backups[i].CreatedAt > backups[j].CreatedAt
	})

	c.JSON(http.StatusOK, gin.H{"backups": backups})
}

// handleAPIBackupDownload downloads a backup file
//
//	@Summary		Download a backup
//	@Description	Streams a backup ZIP file as an attachment. The filename must be a plain .zip name without path separators.
//	@Tags			backup
//	@Produce		octet-stream
//	@Param			filename	path	string	true	"Backup filename (.zip)"
//	@Success		200			{file}	file
//	@Failure		400			{object}	web.APIError
//	@Failure		404			{object}	web.APIError
//	@Security		ApiKeyAuth
//	@Router			/api/backup/download/{filename} [get]
func (s *Server) handleAPIBackupDownload(c *gin.Context) {
	filename := c.Param("filename")

	// Validate filename (security check)
	if strings.Contains(filename, "..") || strings.Contains(filename, "/") || strings.Contains(filename, "\\") {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid filename"})
		return
	}

	if !strings.HasSuffix(filename, ".zip") {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid file type"})
		return
	}

	backupDir := filepath.Join(filepath.Dir(s.serverDir), "backup")
	filePath := filepath.Join(backupDir, filename)

	// Check if file exists
	if _, err := os.Stat(filePath); os.IsNotExist(err) {
		c.JSON(http.StatusNotFound, gin.H{"error": "Backup not found"})
		return
	}

	c.Header("Content-Description", "File Transfer")
	c.Header("Content-Disposition", fmt.Sprintf("attachment; filename=%s", filename))
	c.Header("Content-Type", "application/zip")
	c.File(filePath)
}

// handleAPIBackupDelete deletes a backup file
//
//	@Summary		Delete a backup
//	@Description	Deletes a backup ZIP file from the backup directory. The filename must be a plain .zip name without path separators.
//	@Tags			backup
//	@Produce		json
//	@Param			filename	path		string	true	"Backup filename (.zip)"
//	@Success		200			{object}	web.APIOK
//	@Failure		400			{object}	web.APIError
//	@Failure		404			{object}	web.APIError
//	@Failure		500			{object}	web.APIError
//	@Security		ApiKeyAuth
//	@Router			/api/backup/{filename} [delete]
func (s *Server) handleAPIBackupDelete(c *gin.Context) {
	filename := c.Param("filename")

	// Validate filename (security check)
	if strings.Contains(filename, "..") || strings.Contains(filename, "/") || strings.Contains(filename, "\\") {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid filename"})
		return
	}

	if !strings.HasSuffix(filename, ".zip") {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid file type"})
		return
	}

	backupDir := filepath.Join(filepath.Dir(s.serverDir), "backup")
	filePath := filepath.Join(backupDir, filename)

	// Check if file exists
	if _, err := os.Stat(filePath); os.IsNotExist(err) {
		c.JSON(http.StatusNotFound, gin.H{"error": "Backup not found"})
		return
	}

	// Delete file
	if err := os.Remove(filePath); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("Failed to delete backup: %v", err)})
		return
	}

	c.JSON(http.StatusOK, gin.H{"success": true})
}

// handleAPIBackupRestore restores a backup ZIP to the data directory
//
//	@Summary		Restore a backup
//	@Description	Restores a backup ZIP into the data directory. A safety backup (pre_restore_*.zip) of the current state is created first, then the archive is extracted and the Lua runtime is reloaded to pick up the restored files.
//	@Tags			backup
//	@Accept			json
//	@Produce		json
//	@Param			request	body		object	true	"Restore request: {filename}"
//	@Success		200		{object}	map[string]interface{}	"success, restoredFrom, safetyBackup"
//	@Failure		400		{object}	web.APIError
//	@Failure		404		{object}	web.APIError
//	@Failure		500		{object}	web.APIError
//	@Security		ApiKeyAuth
//	@Router			/api/backup/restore [post]
func (s *Server) handleAPIBackupRestore(c *gin.Context) {
	var req struct {
		Filename string `json:"filename"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request body"})
		return
	}

	// Validate filename (same checks as delete)
	if strings.Contains(req.Filename, "..") || strings.Contains(req.Filename, "/") || strings.Contains(req.Filename, "\\") {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid filename"})
		return
	}
	if !strings.HasSuffix(req.Filename, ".zip") {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid file type"})
		return
	}

	backupDir := filepath.Join(filepath.Dir(s.serverDir), "backup")
	zipPath := filepath.Join(backupDir, req.Filename)

	// Verify ZIP exists
	if _, err := os.Stat(zipPath); os.IsNotExist(err) {
		c.JSON(http.StatusNotFound, gin.H{"error": "Backup not found"})
		return
	}

	// Create safety backup before overwriting
	if err := os.MkdirAll(backupDir, 0755); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("Failed to create backup directory: %v", err)})
		return
	}
	timestamp := time.Now().Format("2006-01-02_15-04-05")
	safetyFilename := fmt.Sprintf("pre_restore_%s.zip", timestamp)
	safetyPath := filepath.Join(backupDir, safetyFilename)
	if err := createBackupZip(safetyPath, s.serverDir); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("Failed to create safety backup: %v", err)})
		return
	}

	// Extract ZIP to serverDir
	if err := extractBackupZip(zipPath, s.serverDir); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("Failed to restore backup: %v", err)})
		return
	}

	// Reload Lua runtime
	if s.luaReloader != nil {
		if err := s.luaReloader.Reload(); err != nil {
			log.Printf("Warning: Failed to reload Lua after restore: %v", err)
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"success":      true,
		"restoredFrom": req.Filename,
		"safetyBackup": safetyFilename,
	})
}

// extractBackupZip extracts a ZIP archive to the data directory
func extractBackupZip(zipPath string, destDir string) error {
	reader, err := zip.OpenReader(zipPath)
	if err != nil {
		return fmt.Errorf("failed to open zip: %w", err)
	}
	defer reader.Close()

	// The backup stores files relative to parent of dataDir (e.g., "a/om.lua")
	destDirClean := filepath.Clean(destDir) + string(os.PathSeparator)
	parentDir := filepath.Dir(destDir)

	for _, f := range reader.File {
		// Zip slip protection
		destPath := filepath.Join(parentDir, f.Name)
		cleanDest := filepath.Clean(destPath)
		if cleanDest != filepath.Clean(destDir) &&
			!strings.HasPrefix(cleanDest+string(os.PathSeparator), destDirClean) &&
			!strings.HasPrefix(cleanDest, destDirClean) {
			log.Printf("Skipping zip entry outside target dir: %s", f.Name)
			continue
		}

		// Skip entries > 10MB
		if f.UncompressedSize64 > 10*1024*1024 {
			log.Printf("Skipping zip entry exceeding 10MB limit: %s (%d bytes)", f.Name, f.UncompressedSize64)
			continue
		}

		if f.FileInfo().IsDir() {
			os.MkdirAll(destPath, 0755)
			continue
		}

		// Ensure parent directory exists
		if err := os.MkdirAll(filepath.Dir(destPath), 0755); err != nil {
			return fmt.Errorf("failed to create directory for %s: %w", f.Name, err)
		}

		// Extract file
		rc, err := f.Open()
		if err != nil {
			return fmt.Errorf("failed to open zip entry %s: %w", f.Name, err)
		}

		outFile, err := os.Create(destPath)
		if err != nil {
			rc.Close()
			return fmt.Errorf("failed to create file %s: %w", destPath, err)
		}

		_, err = io.Copy(outFile, io.LimitReader(rc, 10*1024*1024))
		outFile.Close()
		rc.Close()
		if err != nil {
			return fmt.Errorf("failed to extract %s: %w", f.Name, err)
		}
	}

	return nil
}

// handleAPIBackupSettings returns current backup scheduler settings
//
//	@Summary		Backup scheduler settings
//	@Description	Returns the current auto-backup scheduler settings from .vclu.json, or the defaults when none are stored.
//	@Tags			backup
//	@Produce		json
//	@Success		200	{object}	config.VCLUBackupConfig
//	@Security		ApiKeyAuth
//	@Router			/api/backup/settings [get]
func (s *Server) handleAPIBackupSettings(c *gin.Context) {
	cfg := s.getBackupConfig()
	c.JSON(http.StatusOK, cfg)
}

// handleAPIBackupSettingsSave saves backup scheduler settings
//
//	@Summary		Save backup scheduler settings
//	@Description	Validates and stores the auto-backup scheduler settings (interval clamped to 1-168 hours, maxBackups to >= 0) in .vclu.json, then restarts the scheduler with the new settings. No vCLU restart is required.
//	@Tags			backup
//	@Accept			json
//	@Produce		json
//	@Param			settings	body		config.VCLUBackupConfig	true	"Backup scheduler settings"
//	@Success		200			{object}	web.APIOK
//	@Failure		400			{object}	web.APIError
//	@Failure		500			{object}	web.APIError
//	@Security		ApiKeyAuth
//	@Router			/api/backup/settings [post]
func (s *Server) handleAPIBackupSettingsSave(c *gin.Context) {
	var req config.VCLUBackupConfig
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request body"})
		return
	}

	// Validate
	if req.IntervalH < 1 {
		req.IntervalH = 1
	}
	if req.IntervalH > 168 { // max 1 week
		req.IntervalH = 168
	}
	if req.MaxBackups < 0 {
		req.MaxBackups = 0
	}

	// Save to .vclu.json
	if err := config.UpdateMasterConfig(s.serverDir, func(mc *config.VCLUConfig) error {
		mc.Backup = &req
		return nil
	}); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("Failed to save config: %v", err)})
		return
	}

	// Restart scheduler with new settings
	s.restartBackupScheduler()

	c.JSON(http.StatusOK, gin.H{"success": true})
}

// getBackupConfig returns the current backup config from .vclu.json
func (s *Server) getBackupConfig() *config.VCLUBackupConfig {
	masterConfig, err := config.ReadMasterConfig(s.serverDir)
	if err != nil || masterConfig == nil || masterConfig.Backup == nil {
		return config.DefaultBackupConfig()
	}
	return masterConfig.Backup
}

// startBackupScheduler starts the auto-backup scheduler goroutine
func (s *Server) startBackupScheduler() {
	cfg := s.getBackupConfig()
	if !cfg.AutoEnabled || cfg.IntervalH <= 0 {
		return
	}

	ctx, cancel := context.WithCancel(context.Background())
	s.backupState.mu.Lock()
	s.backupState.cancel = cancel
	s.backupState.mu.Unlock()

	ticker := time.NewTicker(time.Duration(cfg.IntervalH) * time.Hour)
	go func() {
		for {
			select {
			case <-ctx.Done():
				ticker.Stop()
				return
			case <-ticker.C:
				s.runAutoBackup(cfg)
			}
		}
	}()

	log.Printf("Auto-backup scheduler started: every %dh, keep %d backups", cfg.IntervalH, cfg.MaxBackups)
}

// restartBackupScheduler cancels old scheduler and starts new one
func (s *Server) restartBackupScheduler() {
	s.backupState.mu.Lock()
	if s.backupState.cancel != nil {
		s.backupState.cancel()
		s.backupState.cancel = nil
	}
	s.backupState.mu.Unlock()

	s.startBackupScheduler()
}

// stopBackupScheduler stops the auto-backup scheduler
func (s *Server) stopBackupScheduler() {
	s.backupState.mu.Lock()
	defer s.backupState.mu.Unlock()
	if s.backupState.cancel != nil {
		s.backupState.cancel()
		s.backupState.cancel = nil
	}
}

// runAutoBackup creates an auto-backup and enforces retention
func (s *Server) runAutoBackup(cfg *config.VCLUBackupConfig) {
	backupDir := filepath.Join(filepath.Dir(s.serverDir), "backup")
	if err := os.MkdirAll(backupDir, 0755); err != nil {
		log.Printf("Auto-backup: failed to create backup dir: %v", err)
		return
	}

	timestamp := time.Now().Format("2006-01-02_15-04-05")
	filename := fmt.Sprintf("auto_backup_%s.zip", timestamp)
	zipPath := filepath.Join(backupDir, filename)

	if err := createBackupZip(zipPath, s.serverDir); err != nil {
		log.Printf("Auto-backup: failed to create backup: %v", err)
		return
	}

	log.Printf("Auto-backup: created %s", filename)

	// Enforce retention — only delete auto_backup_* files
	if cfg.MaxBackups > 0 {
		s.enforceAutoBackupRetention(backupDir, cfg.MaxBackups)
	}
}

// enforceAutoBackupRetention keeps at most maxBackups auto_backup_* files
func (s *Server) enforceAutoBackupRetention(backupDir string, maxBackups int) {
	entries, err := os.ReadDir(backupDir)
	if err != nil {
		log.Printf("Auto-backup retention: failed to read dir: %v", err)
		return
	}

	type backupEntry struct {
		name    string
		modTime time.Time
	}
	var autoBackups []backupEntry

	for _, entry := range entries {
		if entry.IsDir() || !strings.HasPrefix(entry.Name(), "auto_backup_") || !strings.HasSuffix(entry.Name(), ".zip") {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		autoBackups = append(autoBackups, backupEntry{name: entry.Name(), modTime: info.ModTime()})
	}

	if len(autoBackups) <= maxBackups {
		return
	}

	// Sort oldest first
	sort.Slice(autoBackups, func(i, j int) bool {
		return autoBackups[i].modTime.Before(autoBackups[j].modTime)
	})

	// Delete oldest
	toDelete := len(autoBackups) - maxBackups
	for i := 0; i < toDelete; i++ {
		path := filepath.Join(backupDir, autoBackups[i].name)
		if err := os.Remove(path); err != nil {
			log.Printf("Auto-backup retention: failed to delete %s: %v", autoBackups[i].name, err)
		} else {
			log.Printf("Auto-backup retention: deleted old backup %s", autoBackups[i].name)
		}
	}
}

// createBackupZip creates a ZIP archive of the data directory
func createBackupZip(zipPath string, dataDir string) error {
	// Create ZIP file
	zipFile, err := os.Create(zipPath)
	if err != nil {
		return fmt.Errorf("failed to create zip file: %w", err)
	}
	defer zipFile.Close()

	zipWriter := zip.NewWriter(zipFile)
	defer zipWriter.Close()

	// Walk through data directory
	err = filepath.Walk(dataDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		// Skip the backup directory itself
		if strings.Contains(path, "backup") {
			return nil
		}

		// Get relative path
		relPath, err := filepath.Rel(filepath.Dir(dataDir), path)
		if err != nil {
			return err
		}

		// Skip directories (they will be created implicitly)
		if info.IsDir() {
			return nil
		}

		// Create file header
		header, err := zip.FileInfoHeader(info)
		if err != nil {
			return err
		}
		header.Name = relPath
		header.Method = zip.Deflate

		// Create writer for this file
		writer, err := zipWriter.CreateHeader(header)
		if err != nil {
			return err
		}

		// Open and copy file content
		file, err := os.Open(path)
		if err != nil {
			return err
		}
		defer file.Close()

		_, err = io.Copy(writer, file)
		return err
	})

	return err
}

// humanizeBytes converts bytes to human readable format
func humanizeBytes(bytes int64) string {
	const unit = 1024
	if bytes < unit {
		return fmt.Sprintf("%d B", bytes)
	}
	div, exp := int64(unit), 0
	for n := bytes / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(bytes)/float64(div), "KMGTPE"[exp])
}
