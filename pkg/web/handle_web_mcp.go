package web

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Domktorymysli/vclu/pkg/config"
	mcppkg "github.com/Domktorymysli/vclu/pkg/mcp"
	"github.com/Domktorymysli/vclu/pkg/workspace"
	"github.com/gin-gonic/gin"
)

// handleMCPDashboard renders the MCP dashboard page.
func (s *Server) handleMCPDashboard(c *gin.Context) {
	masterConfig, _ := config.ReadMasterConfig(s.serverDir)
	mcpEnabled := false
	devModeEnabled := false
	if masterConfig != nil && masterConfig.MCP != nil {
		mcpEnabled = masterConfig.MCP.Enabled
		devModeEnabled = masterConfig.MCP.DevModeEnabled
	}

	s.renderHTML(c, http.StatusOK, "mcp.html", gin.H{
		"currentPage":     "mcp",
		"configured":      s.isConfigured,
		"supportsModules": s.supportsModules,
		"mcpEnabled":      mcpEnabled,
		"devModeEnabled":  devModeEnabled,
		"lang":            s.getLang(c),
		"t":               s.getTranslations(c)["t"],
	})
}

// handleAPIMCPStatus returns MCP server status.
func (s *Server) handleAPIMCPStatus(c *gin.Context) {
	masterConfig, _ := config.ReadMasterConfig(s.serverDir)

	enabled := false
	devModeEnabled := false
	if masterConfig != nil && masterConfig.MCP != nil {
		enabled = masterConfig.MCP.Enabled
		devModeEnabled = masterConfig.MCP.DevModeEnabled
	}

	var activeSessions int64
	var totalRequests int64
	var totalErrors int64
	if s.metrics != nil {
		activeSessions = s.metrics.MCPActiveSessions.Load()
		totalRequests = s.metrics.MCPRequests.Load()
		totalErrors = s.metrics.MCPErrors.Load()
	}

	c.JSON(http.StatusOK, gin.H{
		"enabled":        enabled,
		"devModeEnabled": devModeEnabled,
		"activeSessions": activeSessions,
		"totalRequests":  totalRequests,
		"totalErrors":    totalErrors,
	})
}

// handleAPIMCPSave saves MCP configuration.
func (s *Server) handleAPIMCPSave(c *gin.Context) {
	var req struct {
		Enabled        *bool `json:"enabled"`
		DevModeEnabled *bool `json:"devModeEnabled"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
		return
	}

	masterConfig, err := config.ReadMasterConfig(s.serverDir)
	if err != nil || masterConfig == nil {
		masterConfig = &config.VCLUConfig{}
	}

	if masterConfig.MCP == nil {
		masterConfig.MCP = &config.VCLUMCPConfig{}
	}
	if req.Enabled != nil {
		masterConfig.MCP.Enabled = *req.Enabled
	}
	if req.DevModeEnabled != nil {
		masterConfig.MCP.DevModeEnabled = *req.DevModeEnabled
	}

	if err := config.WriteMasterConfig(s.serverDir, masterConfig); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to save config"})
		return
	}

	// Runtime toggle — sync atomic with config
	if s.mcpServer != nil && req.Enabled != nil {
		s.mcpServer.SetEnabled(*req.Enabled)
	}

	c.JSON(http.StatusOK, gin.H{
		"success":        true,
		"enabled":        masterConfig.MCP.Enabled,
		"devModeEnabled": masterConfig.MCP.DevModeEnabled,
	})
}

// handleAPIMCPStats returns MCP metrics and per-tool breakdown.
func (s *Server) handleAPIMCPStats(c *gin.Context) {
	if s.metrics == nil {
		c.JSON(http.StatusOK, gin.H{})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"requests":       s.metrics.MCPRequests.Load(),
		"errors":         s.metrics.MCPErrors.Load(),
		"activeSessions": s.metrics.MCPActiveSessions.Load(),
		"latencyBuckets": map[string]int64{
			"<10ms":  s.metrics.MCPLatencyBuckets[0].Load(),
			"<50ms":  s.metrics.MCPLatencyBuckets[1].Load(),
			"<100ms": s.metrics.MCPLatencyBuckets[2].Load(),
			"<500ms": s.metrics.MCPLatencyBuckets[3].Load(),
			"<1s":    s.metrics.MCPLatencyBuckets[4].Load(),
			">1s":    s.metrics.MCPLatencyBuckets[5].Load(),
		},
	})
}

// handleAPIMCPActivity returns recent MCP tool calls.
func (s *Server) handleAPIMCPActivity(c *gin.Context) {
	if s.metrics == nil {
		c.JSON(http.StatusOK, gin.H{"activity": []any{}})
		return
	}

	limit := 50
	if l := c.Query("limit"); l != "" {
		if n, err := strconv.Atoi(l); err == nil && n > 0 {
			limit = n
		}
	}

	activity := s.metrics.GetMCPActivity(limit)
	c.JSON(http.StatusOK, gin.H{"activity": activity})
}

// --- MCP Bot CRUD ---

// handleAPIMCPBots lists all MCP bots.
func (s *Server) handleAPIMCPBots(c *gin.Context) {
	masterConfig, _ := config.ReadMasterConfig(s.serverDir)

	var bots []map[string]interface{}
	if masterConfig != nil && masterConfig.MCP != nil {
		for _, b := range masterConfig.MCP.Bots {
			bots = append(bots, map[string]interface{}{
				"id":        b.ID,
				"name":      b.Name,
				"profiles":  b.Profiles,
				"devMode":   b.DevMode,
				"execMode":  b.ExecMode,
				"createdAt": b.CreatedAt,
				"hasKey":    b.APIKeyHash != "",
			})
		}
	}

	if bots == nil {
		bots = []map[string]interface{}{}
	}

	c.JSON(http.StatusOK, gin.H{"bots": bots})
}

// handleAPIMCPBotCreate creates a new MCP bot with a generated API key.
func (s *Server) handleAPIMCPBotCreate(c *gin.Context) {
	var req struct {
		Name     string   `json:"name"`
		Profiles []string `json:"profiles"`
		DevMode  bool     `json:"devMode"`
		ExecMode bool     `json:"execMode"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request"})
		return
	}

	if req.Name == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Bot name is required"})
		return
	}

	// Generate unique ID
	idBytes := make([]byte, 8)
	if _, err := rand.Read(idBytes); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to generate ID"})
		return
	}
	botID := "bot-" + hex.EncodeToString(idBytes)

	// Generate API key
	keyBytes := make([]byte, 32)
	if _, err := rand.Read(keyBytes); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to generate key"})
		return
	}
	plainKey := "vclu_mcp_" + hex.EncodeToString(keyBytes)
	hash := config.HashAPIKey(plainKey)

	// Default profiles if none specified
	profiles := req.Profiles
	if len(profiles) == 0 {
		profiles = []string{"full-access"}
	}

	bot := config.MCPBot{
		ID:         botID,
		Name:       req.Name,
		APIKeyHash: hash,
		Profiles:   profiles,
		DevMode:    req.DevMode,
		ExecMode:   req.ExecMode,
		CreatedAt:  time.Now().UTC().Format(time.RFC3339),
	}

	masterConfig, err := config.ReadMasterConfig(s.serverDir)
	if err != nil || masterConfig == nil {
		masterConfig = &config.VCLUConfig{}
	}
	if masterConfig.MCP == nil {
		masterConfig.MCP = &config.VCLUMCPConfig{}
	}

	masterConfig.MCP.Bots = append(masterConfig.MCP.Bots, bot)

	if err := config.WriteMasterConfig(s.serverDir, masterConfig); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to save config"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"bot": map[string]interface{}{
			"id":        bot.ID,
			"name":      bot.Name,
			"profiles":  bot.Profiles,
			"createdAt": bot.CreatedAt,
		},
		"apiKey":  plainKey,
		"message": "Copy this key now. It will not be shown again.",
	})
}

// handleAPIMCPBotUpdate updates an existing bot (name, profiles, devMode).
func (s *Server) handleAPIMCPBotUpdate(c *gin.Context) {
	botID := c.Param("id")

	var req struct {
		Name     string   `json:"name"`
		Profiles []string `json:"profiles"`
		DevMode  *bool    `json:"devMode"`
		ExecMode *bool    `json:"execMode"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request"})
		return
	}

	masterConfig, err := config.ReadMasterConfig(s.serverDir)
	if err != nil || masterConfig == nil || masterConfig.MCP == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Bot not found"})
		return
	}

	found := false
	for i := range masterConfig.MCP.Bots {
		if masterConfig.MCP.Bots[i].ID == botID {
			if req.Name != "" {
				masterConfig.MCP.Bots[i].Name = req.Name
			}
			if len(req.Profiles) > 0 {
				masterConfig.MCP.Bots[i].Profiles = req.Profiles
			}
			if req.DevMode != nil {
				masterConfig.MCP.Bots[i].DevMode = *req.DevMode
			}
			if req.ExecMode != nil {
				masterConfig.MCP.Bots[i].ExecMode = *req.ExecMode
			}
			found = true
			break
		}
	}

	if !found {
		c.JSON(http.StatusNotFound, gin.H{"error": "Bot not found"})
		return
	}

	if err := config.WriteMasterConfig(s.serverDir, masterConfig); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to save config"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"success": true})
}

// handleAPIMCPBotDelete deletes a bot.
func (s *Server) handleAPIMCPBotDelete(c *gin.Context) {
	botID := c.Param("id")

	masterConfig, err := config.ReadMasterConfig(s.serverDir)
	if err != nil || masterConfig == nil || masterConfig.MCP == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Bot not found"})
		return
	}

	var newBots []config.MCPBot
	found := false
	for _, b := range masterConfig.MCP.Bots {
		if b.ID == botID {
			found = true
			continue
		}
		newBots = append(newBots, b)
	}

	if !found {
		c.JSON(http.StatusNotFound, gin.H{"error": "Bot not found"})
		return
	}

	masterConfig.MCP.Bots = newBots

	if err := config.WriteMasterConfig(s.serverDir, masterConfig); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to save config"})
		return
	}

	// Clean up auto-generated profile for this bot
	if s.accessManager != nil {
		autoProfileName := "_" + botID
		// Unassign first (it might be in MCP assignments)
		_ = s.accessManager.DeleteProfile(autoProfileName)
		_ = s.accessManager.Save()
	}

	c.JSON(http.StatusOK, gin.H{"success": true})
}

// handleAPIMCPBotRegenerate regenerates a bot's API key.
func (s *Server) handleAPIMCPBotRegenerate(c *gin.Context) {
	botID := c.Param("id")

	masterConfig, err := config.ReadMasterConfig(s.serverDir)
	if err != nil || masterConfig == nil || masterConfig.MCP == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Bot not found"})
		return
	}

	keyBytes := make([]byte, 32)
	if _, err := rand.Read(keyBytes); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to generate key"})
		return
	}
	plainKey := "vclu_mcp_" + hex.EncodeToString(keyBytes)
	hash := config.HashAPIKey(plainKey)

	found := false
	for i := range masterConfig.MCP.Bots {
		if masterConfig.MCP.Bots[i].ID == botID {
			masterConfig.MCP.Bots[i].APIKeyHash = hash
			found = true
			break
		}
	}

	if !found {
		c.JSON(http.StatusNotFound, gin.H{"error": "Bot not found"})
		return
	}

	if err := config.WriteMasterConfig(s.serverDir, masterConfig); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to save config"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"apiKey":  plainKey,
		"message": fmt.Sprintf("New key generated for bot %s. Copy it now.", botID),
	})
}

// --- MCP Workspace Dashboard API ---

func (s *Server) getWorkspace() *workspace.Workspace {
	if s.mcpServer == nil {
		return nil
	}
	ws := s.mcpServer.GetWorkspace()
	return ws
}

// handleAPIMCPDrafts lists all draft files.
func (s *Server) handleAPIMCPDrafts(c *gin.Context) {
	ws := s.getWorkspace()
	if ws == nil {
		c.JSON(http.StatusOK, gin.H{"drafts": []any{}, "count": 0})
		return
	}

	drafts, err := ws.ListDrafts()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if drafts == nil {
		drafts = []workspace.DraftInfo{}
	}

	c.JSON(http.StatusOK, gin.H{"drafts": drafts, "count": len(drafts)})
}

// handleAPIMCPDraftGet returns the content of a draft file.
func (s *Server) handleAPIMCPDraftGet(c *gin.Context) {
	ws := s.getWorkspace()
	if ws == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "workspace not configured"})
		return
	}

	path := c.Param("path")
	content, err := ws.GetDraft(path)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"path": path, "content": content})
}

// handleAPIMCPDraftDelete deletes a draft file.
func (s *Server) handleAPIMCPDraftDelete(c *gin.Context) {
	ws := s.getWorkspace()
	if ws == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "workspace not configured"})
		return
	}

	path := c.Param("path")
	if err := ws.DeleteDraft(path); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"success": true, "path": path})
}

// handleAPIMCPDraftDiff returns unified diff between draft and production.
func (s *Server) handleAPIMCPDraftDiff(c *gin.Context) {
	ws := s.getWorkspace()
	if ws == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "workspace not configured"})
		return
	}

	path := c.Param("path")
	draftContent, err := ws.GetDraft(path)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}

	prodContent, _ := ws.GetProductionContent(path)

	// Simple line-based diff for the dashboard
	c.JSON(http.StatusOK, gin.H{
		"path":       path,
		"draft":      draftContent,
		"production": prodContent,
	})
}

// handleAPIMCPDraftPromoteWeb handles promote from the dashboard (human approval).
func (s *Server) handleAPIMCPDraftPromoteWeb(c *gin.Context) {
	ws := s.getWorkspace()
	if ws == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "workspace not configured"})
		return
	}

	var req struct {
		Path  string   `json:"path"`
		Paths []string `json:"paths"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
		return
	}

	paths := req.Paths
	if len(paths) == 0 && req.Path != "" {
		paths = []string{req.Path}
	}
	if len(paths) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "path(s) required"})
		return
	}

	// Promote each file
	var backupNames []string
	var promoted []workspace.Promoted
	for _, p := range paths {
		backupName, err := ws.Promote(p)
		if err != nil {
			// Undo already promoted
			c.JSON(http.StatusInternalServerError, gin.H{
				"error":          fmt.Sprintf("promote failed at %s: %v", p, err),
				"rollbackErrors": ws.Undo(promoted),
			})
			return
		}
		promoted = append(promoted, workspace.Promoted{Path: p, Backup: backupName})
		if backupName != "" {
			backupNames = append(backupNames, backupName)
		}
	}

	// Reload
	if s.luaReloader != nil {
		if err := s.luaReloader.Reload(); err != nil {
			undoErrs := ws.Undo(promoted)
			s.luaReloader.Reload()
			c.JSON(http.StatusInternalServerError, gin.H{
				"error":          fmt.Sprintf("reload failed, rolled back: %v", err),
				"rolledBack":     len(undoErrs) == 0,
				"rollbackErrors": undoErrs,
			})
			return
		}
	}

	// Record audit entry for human-initiated promote
	if s.mcpServer != nil {
		s.mcpServer.AddAudit(mcppkg.AuditEntry{
			Time:    time.Now(),
			Tool:    "promote",
			Paths:   paths,
			Env:     "runtime",
			Success: true,
			Details: fmt.Sprintf("%d file(s) promoted", len(paths)),
			Actor:   "human",
		})
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"paths":   paths,
		"backups": backupNames,
	})
}

// handleAPIMCPBackups lists all backups.
func (s *Server) handleAPIMCPBackups(c *gin.Context) {
	ws := s.getWorkspace()
	if ws == nil {
		c.JSON(http.StatusOK, gin.H{"backups": []any{}, "count": 0})
		return
	}

	backups, err := ws.ListBackups()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if backups == nil {
		backups = []workspace.BackupInfo{}
	}

	c.JSON(http.StatusOK, gin.H{"backups": backups, "count": len(backups)})
}

// handleAPIMCPBackupsClear deletes all backups.
func (s *Server) handleAPIMCPBackupsClear(c *gin.Context) {
	ws := s.getWorkspace()
	if ws == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "workspace not configured"})
		return
	}

	count, err := ws.ClearBackups()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"success": true, "deleted": count})
}

// handleAPIMCPRollback restores a backup file.
func (s *Server) handleAPIMCPRollback(c *gin.Context) {
	ws := s.getWorkspace()
	if ws == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "workspace not configured"})
		return
	}

	var req struct {
		BackupName string `json:"backupName"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || req.BackupName == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "backupName required"})
		return
	}

	if err := ws.Rollback(req.BackupName); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	// Reload
	if s.luaReloader != nil {
		if err := s.luaReloader.Reload(); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("rollback OK but reload failed: %v", err)})
			return
		}
	}

	// Parse original path from backup name for audit
	origPath, _ := workspace.BackupPath(req.BackupName)

	// Record audit entry for human-initiated restore
	if s.mcpServer != nil {
		s.mcpServer.AddAudit(mcppkg.AuditEntry{
			Time:    time.Now(),
			Tool:    "restore",
			Path:    origPath,
			Env:     "runtime",
			Success: true,
			Actor:   "human",
			Details: "Restored from backup: " + req.BackupName,
		})
	}

	c.JSON(http.StatusOK, gin.H{"success": true, "backupName": req.BackupName, "path": origPath})
}

// --- MCP Incidents + Agent Context ---

// handleAPIMCPIncidents lists all incidents.
func (s *Server) handleAPIMCPIncidents(c *gin.Context) {
	if s.mcpServer == nil {
		c.JSON(http.StatusOK, gin.H{"incidents": []any{}, "count": 0})
		return
	}
	store := s.mcpServer.GetIncidents()
	if store == nil {
		c.JSON(http.StatusOK, gin.H{"incidents": []any{}, "count": 0})
		return
	}
	list := store.List()
	c.JSON(http.StatusOK, gin.H{"incidents": list, "count": len(list)})
}

// handleAPIMCPIncidentGet returns a specific incident by ID.
func (s *Server) handleAPIMCPIncidentGet(c *gin.Context) {
	if s.mcpServer == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "not configured"})
		return
	}
	store := s.mcpServer.GetIncidents()
	if store == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "not configured"})
		return
	}
	id := c.Param("id")
	content, err := store.Get(id)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"id": id, "content": content})
}

// handleAPIMCPAgentContext returns the agent context notes.
func (s *Server) handleAPIMCPAgentContext(c *gin.Context) {
	if s.mcpServer == nil {
		c.JSON(http.StatusOK, gin.H{"content": ""})
		return
	}
	ac := s.mcpServer.GetAgentContext()
	if ac == nil {
		c.JSON(http.StatusOK, gin.H{"content": ""})
		return
	}
	content, err := ac.Read()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"content": content})
}

// handleAPIMCPAgentContextClear clears the agent context notes.
func (s *Server) handleAPIMCPAgentContextClear(c *gin.Context) {
	if s.mcpServer == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "not configured"})
		return
	}
	ac := s.mcpServer.GetAgentContext()
	if ac == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "not configured"})
		return
	}
	if err := ac.Clear(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true})
}

// handleAPIMCPAuditLog returns recent audit log entries.
func (s *Server) handleAPIMCPAuditLog(c *gin.Context) {
	if s.mcpServer == nil {
		c.JSON(http.StatusOK, gin.H{"entries": []any{}, "count": 0})
		return
	}

	limit := 50
	if l := c.Query("limit"); l != "" {
		if n, err := strconv.Atoi(l); err == nil && n > 0 {
			limit = n
		}
	}

	entries := s.mcpServer.GetAuditLog().GetRecent(limit)
	c.JSON(http.StatusOK, gin.H{"entries": entries, "count": len(entries)})
}

// --- History & Backup Content ---

// historyEvent is a merged timeline event combining audit log and backups.
type historyEvent struct {
	Time    time.Time `json:"time"`
	Type    string    `json:"type"` // draft_write, draft_test, draft_validate, promote, draft_delete
	Path    string    `json:"path,omitempty"`
	Paths   []string  `json:"paths,omitempty"`
	Actor   string    `json:"actor,omitempty"`
	Success bool      `json:"success"`
	Details string    `json:"details,omitempty"`
	Session string    `json:"session,omitempty"`
	Backup  string    `json:"backup,omitempty"` // backup filename for promote events
}

// handleAPIMCPHistory returns a merged timeline of audit events and backups.
func (s *Server) handleAPIMCPHistory(c *gin.Context) {
	if s.mcpServer == nil {
		c.JSON(http.StatusOK, gin.H{"events": []any{}, "count": 0})
		return
	}

	limit := 200
	if l := c.Query("limit"); l != "" {
		if n, err := strconv.Atoi(l); err == nil && n > 0 {
			limit = n
		}
	}
	if limit > 500 {
		limit = 500
	}

	fileFilter := c.Query("file")

	// 1. Get audit entries from persistent log
	auditEntries := s.mcpServer.GetAuditLog().ReadAll(limit * 2) // read extra, we'll filter

	// Build set of promote times to match with backups
	promoteTimeSet := map[string]bool{}
	var events []historyEvent

	for _, ae := range auditEntries {
		ev := historyEvent{
			Time:    ae.Time,
			Type:    ae.Tool,
			Path:    ae.Path,
			Paths:   ae.Paths,
			Actor:   ae.Actor,
			Success: ae.Success,
			Details: ae.Details,
			Session: ae.Session,
		}

		// For multi-path operations, use first path as primary if Path is empty
		if ev.Path == "" && len(ev.Paths) > 0 {
			ev.Path = ev.Paths[0]
		}

		if ae.Tool == "promote" {
			promoteTimeSet[ae.Time.Format("20060102_150405")] = true
		}

		events = append(events, ev)
	}

	// 2. Get backups and create synthetic promote events for those without audit entries
	ws := s.getWorkspace()
	if ws != nil {
		backups, _ := ws.ListBackups()
		for _, b := range backups {
			// Extract timestamp from backup name
			ts := ""
			if len(b.Name) > 15 && b.Name[8] == '_' {
				ts = b.Name[:15]
			}

			// Check if we already have an audit entry for this promote
			if ts != "" && promoteTimeSet[ts] {
				// Match backup to existing promote event
				for i := range events {
					if events[i].Type == "promote" && events[i].Time.Format("20060102_150405") == ts {
						if events[i].Backup == "" {
							events[i].Backup = b.Name
						}
						break
					}
				}
				continue
			}

			// Synthetic promote event from backup (pre-persistence era)
			events = append(events, historyEvent{
				Time:    b.Created,
				Type:    "promote",
				Path:    b.Path,
				Success: true,
				Details: "backup exists (pre-audit)",
				Backup:  b.Name,
			})
		}
	}

	// 3. Filter by file if requested
	if fileFilter != "" {
		var filtered []historyEvent
		for _, ev := range events {
			if ev.Path == fileFilter || strings.HasPrefix(ev.Path, fileFilter) {
				filtered = append(filtered, ev)
			} else {
				// Check Paths array
				for _, p := range ev.Paths {
					if p == fileFilter || strings.HasPrefix(p, fileFilter) {
						filtered = append(filtered, ev)
						break
					}
				}
			}
		}
		events = filtered
	}

	// 4. Sort by time descending
	sort.Slice(events, func(i, j int) bool {
		return events[i].Time.After(events[j].Time)
	})

	// 5. Apply limit
	if len(events) > limit {
		events = events[:limit]
	}

	if events == nil {
		events = []historyEvent{}
	}

	c.JSON(http.StatusOK, gin.H{"events": events, "count": len(events)})
}

// handleAPIMCPBackupGet returns the content of a specific backup file.
func (s *Server) handleAPIMCPBackupGet(c *gin.Context) {
	ws := s.getWorkspace()
	if ws == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "workspace not configured"})
		return
	}

	name := c.Param("name")
	if name == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "backup name required"})
		return
	}

	content, origPath, err := ws.GetBackup(name)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"name":    name,
		"content": content,
		"path":    origPath,
	})
}

// handleAPIMCPDraftValidateWeb validates a draft file (syntax + blocklist).
func (s *Server) handleAPIMCPDraftValidateWeb(c *gin.Context) {
	var req struct {
		Path string `json:"path"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || req.Path == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "path is required"})
		return
	}

	if s.mcpServer == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "MCP not configured"})
		return
	}

	report, err := s.mcpServer.ValidateDraftFile(req.Path)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, report)
}

// handleAPIMCPDraftTestWeb runs a draft in the sandbox and returns the result.
func (s *Server) handleAPIMCPDraftTestWeb(c *gin.Context) {
	var req struct {
		Path   string               `json:"path"`
		Events []mcppkg.InjectEvent `json:"inject_events,omitempty"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || req.Path == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "path is required"})
		return
	}

	if s.mcpServer == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "MCP not configured"})
		return
	}

	result, err := s.mcpServer.RunSandboxTest([]string{req.Path}, req.Events)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	// Record audit entry for human-initiated test
	s.mcpServer.AddAudit(mcppkg.AuditEntry{
		Time:    time.Now(),
		Tool:    "draft_test",
		Path:    req.Path,
		Env:     "sandbox",
		Success: result.Success,
		Actor:   "human",
		Details: fmt.Sprintf("%d state changes, %d blocked calls, %d errors",
			len(result.StateChanges), len(result.BlockedCalls), len(result.Errors)),
	})

	c.JSON(http.StatusOK, result)
}
