package web

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/Domktorymysli/vclu/pkg/accesscontrol"
	"github.com/Domktorymysli/vclu/pkg/config"
	"github.com/gin-gonic/gin"
)

// handleAccess shows the access control page
func (s *Server) handleAccess(c *gin.Context) {
	trans := s.getTranslations(c)

	data := gin.H{
		"title":           "Access Control",
		"currentPage":     "access",
		"configured":      s.isConfigured,
		"supportsModules": s.supportsModules,
		"lang":            trans["lang"],
		"t":               trans["t"],
		"langs":           trans["langs"],
		"integrations":    []string{"mqtt", "homekit", "mcp"},
	}

	s.renderHTML(c, http.StatusOK, "access.html", data)
}

// handleAccessProfiles shows the profiles management page
func (s *Server) handleAccessProfiles(c *gin.Context) {
	trans := s.getTranslations(c)

	data := gin.H{
		"title":           "Access Profiles",
		"currentPage":     "access",
		"configured":      s.isConfigured,
		"supportsModules": s.supportsModules,
		"lang":            trans["lang"],
		"t":               trans["t"],
		"langs":           trans["langs"],
	}

	s.renderHTML(c, http.StatusOK, "access-profiles.html", data)
}

// handleAccessProfileEdit shows the profile editor page with an object matrix.
func (s *Server) handleAccessProfileEdit(c *gin.Context) {
	trans := s.getTranslations(c)

	data := gin.H{
		"title":           "Edit Profile",
		"currentPage":     "access",
		"configured":      s.isConfigured,
		"supportsModules": s.supportsModules,
		"lang":            trans["lang"],
		"t":               trans["t"],
		"langs":           trans["langs"],
		"profileName":     c.Query("name"),
	}

	s.renderHTML(c, http.StatusOK, "access-profile-edit.html", data)
}

// handleAPIAccessList returns all objects with their access levels
//
//	@Summary		List objects with access levels
//	@Description	Returns all exposed objects from the Lua registry together with their per-integration access levels (mqtt, homekit) and the resolved access level for each MCP bot. Returns an empty object list when the Lua engine is unavailable.
//	@Tags			access
//	@Produce		json
//	@Success		200	{object}	map[string]interface{}	"objects [{path, name, type, cluId, access {mqtt, homekit}, botAccess {botId: level}}], integrations []string, bots [{id, name}]"
//	@Security		ApiKeyAuth
//	@Router			/api/access [get]
func (s *Server) handleAPIAccessList(c *gin.Context) {
	// Get objects from Lua registry
	if s.luaExecutor == nil {
		c.JSON(http.StatusOK, gin.H{
			"objects": []interface{}{},
		})
		return
	}

	type accessObject struct {
		Path   string `json:"path"`
		Type   string `json:"type"`
		Name   string `json:"name"`
		CluID  string `json:"cluId"`
		Source string `json:"source"`
	}

	var objects []accessObject
	result, err := s.luaExecutor.Execute("ExposedObjects:accessList()")
	if err == nil && result != "" && result != "nil" && result != "[]" {
		json.Unmarshal([]byte(result), &objects)
	}

	// Load MCP bots for per-bot access columns
	var bots []config.MCPBot
	masterConfig, _ := config.ReadMasterConfig(s.serverDir)
	if masterConfig != nil && masterConfig.MCP != nil {
		bots = masterConfig.MCP.Bots
	}

	// Build response with access levels from manager
	var response []map[string]interface{}
	for _, obj := range objects {
		accessMap := make(map[string]string)

		if s.accessManager != nil {
			// Only include MQTT and HomeKit as integration columns
			accessMap["mqtt"] = string(s.accessManager.GetAccess(accesscontrol.IntegrationMQTT, obj.Path))
			accessMap["homekit"] = string(s.accessManager.GetAccess(accesscontrol.IntegrationHomeKit, obj.Path))
		} else {
			accessMap["mqtt"] = "full"
			accessMap["homekit"] = "full"
		}

		// Per-bot resolved access
		botAccessMap := make(map[string]string)
		if s.accessManager != nil {
			for _, bot := range bots {
				if len(bot.Profiles) > 0 {
					botAccessMap[bot.ID] = string(s.accessManager.ResolveAccess(bot.Profiles, obj.Path))
				} else {
					botAccessMap[bot.ID] = "full"
				}
			}
		}

		displayName := obj.Name
		if obj.Source != "" && obj.Source != "registry" {
			displayName = obj.Name + " (" + obj.Source + ")"
		}

		entry := map[string]interface{}{
			"path":   obj.Path,
			"name":   displayName,
			"type":   obj.Type,
			"cluId":  obj.CluID,
			"access": accessMap,
		}
		if len(botAccessMap) > 0 {
			entry["botAccess"] = botAccessMap
		}

		response = append(response, entry)
	}

	// Bot metadata for column headers
	var botsMeta []map[string]string
	for _, b := range bots {
		botsMeta = append(botsMeta, map[string]string{
			"id":   b.ID,
			"name": b.Name,
		})
	}

	c.JSON(http.StatusOK, gin.H{
		"objects":      response,
		"integrations": []string{"mqtt", "homekit"},
		"bots":         botsMeta,
	})
}

// handleAPIAccessSet sets access level for an object and integration
//
//	@Summary		Set object access level
//	@Description	Sets the access level (full, readonly or hidden) for a single object path and integration (mqtt, homekit or mcp). Persists the change to the access control file and syncs it to the Lua runtime.
//	@Tags			access
//	@Accept			json
//	@Produce		json
//	@Param			request	body		object	true	"Access change: {path, integration, access}"
//	@Success		200		{object}	map[string]interface{}	"success, path, integration, access"
//	@Failure		400		{object}	web.APIError
//	@Failure		500		{object}	web.APIError
//	@Failure		503		{object}	web.APIError
//	@Security		ApiKeyAuth
//	@Router			/api/access [post]
func (s *Server) handleAPIAccessSet(c *gin.Context) {
	var req struct {
		Path        string `json:"path"`
		Integration string `json:"integration"`
		Access      string `json:"access"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request"})
		return
	}

	// Validate integration
	integration := accesscontrol.Integration(req.Integration)
	if integration != accesscontrol.IntegrationMQTT && integration != accesscontrol.IntegrationHomeKit && integration != accesscontrol.IntegrationMCP {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid integration. Must be: mqtt, homekit, or mcp"})
		return
	}

	// Validate access level
	access := accesscontrol.AccessLevel(req.Access)
	if access != accesscontrol.AccessFull && access != accesscontrol.AccessReadonly && access != accesscontrol.AccessHidden {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid access level. Must be: full, readonly, or hidden"})
		return
	}

	if s.accessManager == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "Access manager not available"})
		return
	}

	// Update manager
	s.accessManager.SetAccess(integration, req.Path, access)

	// Save to YAML file
	if err := s.accessManager.Save(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("Failed to save: %v", err)})
		return
	}

	// Sync to Lua
	s.applyAccessChange()

	c.JSON(http.StatusOK, gin.H{
		"success":     true,
		"path":        req.Path,
		"integration": req.Integration,
		"access":      req.Access,
	})
}

// handleAPIAccessIntegrations returns list of available integrations
//
//	@Summary		List available integrations
//	@Description	Returns the static list of integrations that support access control (mqtt, homekit, mcp) with display names and icons.
//	@Tags			access
//	@Produce		json
//	@Success		200	{object}	map[string]interface{}	"integrations [{id, name, icon}]"
//	@Security		ApiKeyAuth
//	@Router			/api/access/integrations [get]
func (s *Server) handleAPIAccessIntegrations(c *gin.Context) {
	integrations := []map[string]string{
		{"id": "mqtt", "name": "MQTT / Home Assistant", "icon": "bi-house-heart"},
		{"id": "homekit", "name": "Apple HomeKit", "icon": "bi-apple"},
		{"id": "mcp", "name": "MCP (AI Bots)", "icon": "bi-robot"},
	}
	c.JSON(http.StatusOK, gin.H{"integrations": integrations})
}

// --- Profile CRUD ---

// handleAPIAccessProfiles lists all profiles with usage info.
//
//	@Summary		List access profiles
//	@Description	Returns all access profiles with their default level, per-object overrides and usage info (which integrations and MCP bots use each profile). The built-in full-access profile is flagged with builtin=true.
//	@Tags			access
//	@Produce		json
//	@Success		200	{object}	map[string]interface{}	"profiles [{name, default, objects, builtin, usedByIntegrations, usedByBots}]"
//	@Failure		503	{object}	web.APIError
//	@Security		ApiKeyAuth
//	@Router			/api/access/profiles [get]
func (s *Server) handleAPIAccessProfiles(c *gin.Context) {
	if s.accessManager == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "Access manager not available"})
		return
	}

	profiles := s.accessManager.ListProfiles()
	assignments := s.accessManager.GetAllAssignments()

	// Build integration usage map: profileName → []integrationName
	intgUsage := make(map[string][]string)
	for intg, names := range assignments {
		for _, name := range names {
			intgUsage[name] = append(intgUsage[name], intg)
		}
	}

	// Build bot usage map: profileName → []botName
	botUsage := make(map[string][]string)
	masterConfig, _ := config.ReadMasterConfig(s.serverDir)
	if masterConfig != nil && masterConfig.MCP != nil {
		for _, bot := range masterConfig.MCP.Bots {
			for _, pn := range bot.Profiles {
				botUsage[pn] = append(botUsage[pn], bot.Name)
			}
		}
	}

	// Convert to a list with names
	var result []map[string]interface{}
	for name, p := range profiles {
		objects := make(map[string]string)
		for k, v := range p.Objects {
			objects[k] = string(v)
		}
		entry := map[string]interface{}{
			"name":    name,
			"default": string(p.Default),
			"objects": objects,
			"builtin": name == accesscontrol.BuiltinProfileFullAccess,
		}
		if intgs, ok := intgUsage[name]; ok {
			entry["usedByIntegrations"] = intgs
		}
		if bots, ok := botUsage[name]; ok {
			entry["usedByBots"] = bots
		}
		result = append(result, entry)
	}

	c.JSON(http.StatusOK, gin.H{"profiles": result})
}

// handleAPIAccessProfileSave creates or updates a profile.
//
//	@Summary		Create or update a profile
//	@Description	Creates or updates a named access profile with a default level (full, readonly or hidden) and per-object overrides. The built-in full-access profile cannot be modified.
//	@Tags			access
//	@Accept			json
//	@Produce		json
//	@Param			profile	body		object	true	"Profile: {name, default, objects {path: level}}"
//	@Success		200		{object}	map[string]interface{}	"success, name"
//	@Failure		400		{object}	web.APIError
//	@Failure		500		{object}	web.APIError
//	@Failure		503		{object}	web.APIError
//	@Security		ApiKeyAuth
//	@Router			/api/access/profiles [post]
func (s *Server) handleAPIAccessProfileSave(c *gin.Context) {
	if s.accessManager == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "Access manager not available"})
		return
	}

	var req struct {
		Name    string            `json:"name"`
		Default string            `json:"default"`
		Objects map[string]string `json:"objects"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request"})
		return
	}

	if req.Name == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Profile name is required"})
		return
	}

	// Validate default level
	defaultLevel := accesscontrol.AccessLevel(req.Default)
	if defaultLevel != accesscontrol.AccessFull && defaultLevel != accesscontrol.AccessReadonly && defaultLevel != accesscontrol.AccessHidden {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid default level"})
		return
	}

	// Convert objects
	objects := make(map[string]accesscontrol.AccessLevel)
	for path, level := range req.Objects {
		al := accesscontrol.AccessLevel(level)
		if al != accesscontrol.AccessFull && al != accesscontrol.AccessReadonly && al != accesscontrol.AccessHidden {
			c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("Invalid access level for %s: %s", path, level)})
			return
		}
		objects[path] = al
	}

	profile := accesscontrol.Profile{
		Default: defaultLevel,
		Objects: objects,
	}

	if err := s.accessManager.SaveProfile(req.Name, profile); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if err := s.accessManager.Save(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("Failed to save: %v", err)})
		return
	}

	// Rules of an assigned profile changed: apply them now, not at the next restart
	s.applyAccessChange()

	c.JSON(http.StatusOK, gin.H{"success": true, "name": req.Name})
}

// handleAPIAccessProfileDelete deletes a profile (auto-unassigns from integrations and bots).
//
//	@Summary		Delete a profile
//	@Description	Deletes an access profile and removes it from all MCP bot assignments (bots left without profiles fall back to full-access). The built-in full-access profile cannot be deleted.
//	@Tags			access
//	@Produce		json
//	@Param			name	path		string	true	"Profile name"
//	@Success		200		{object}	web.APIOK
//	@Failure		400		{object}	web.APIError
//	@Failure		500		{object}	web.APIError
//	@Failure		503		{object}	web.APIError
//	@Security		ApiKeyAuth
//	@Router			/api/access/profiles/{name} [delete]
func (s *Server) handleAPIAccessProfileDelete(c *gin.Context) {
	if s.accessManager == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "Access manager not available"})
		return
	}

	name := c.Param("name")
	if name == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Profile name is required"})
		return
	}

	if err := s.accessManager.DeleteProfile(name); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if err := s.accessManager.Save(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("Failed to save: %v", err)})
		return
	}

	// Also remove from bot profiles in .vclu.json
	_ = updateMasterConfig(s.serverDir, func(masterConfig *config.VCLUConfig) error {
		if masterConfig.MCP == nil {
			return errUnchanged
		}
		changed := false
		for i, bot := range masterConfig.MCP.Bots {
			var filtered []string
			for _, p := range bot.Profiles {
				if p != name {
					filtered = append(filtered, p)
				}
			}
			if len(filtered) != len(bot.Profiles) {
				changed = true
				if len(filtered) == 0 {
					filtered = []string{accesscontrol.BuiltinProfileFullAccess}
				}
				masterConfig.MCP.Bots[i].Profiles = filtered
			}
		}
		if !changed {
			return errUnchanged
		}
		return nil
	})

	s.applyAccessChange()

	c.JSON(http.StatusOK, gin.H{"success": true})
}

// --- Assignments ---

// handleAPIAccessAssignments returns all integration → profile assignments.
//
//	@Summary		List profile assignments
//	@Description	Returns the integration to profile-name assignments for all integrations.
//	@Tags			access
//	@Produce		json
//	@Success		200	{object}	map[string]interface{}	"assignments {integration: []profileName}"
//	@Failure		503	{object}	web.APIError
//	@Security		ApiKeyAuth
//	@Router			/api/access/assignments [get]
func (s *Server) handleAPIAccessAssignments(c *gin.Context) {
	if s.accessManager == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "Access manager not available"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"assignments": s.accessManager.GetAllAssignments()})
}

// handleAPIAccessAssignmentsSave saves all assignments.
//
//	@Summary		Save profile assignments
//	@Description	Replaces the profile assignments for the given integrations. Assigned profiles must exist; the change is persisted and synced to the Lua runtime.
//	@Tags			access
//	@Accept			json
//	@Produce		json
//	@Param			request	body		object	true	"Assignments: {assignments {integration: []profileName}}"
//	@Success		200		{object}	web.APIOK
//	@Failure		400		{object}	web.APIError
//	@Failure		500		{object}	web.APIError
//	@Failure		503		{object}	web.APIError
//	@Security		ApiKeyAuth
//	@Router			/api/access/assignments [post]
func (s *Server) handleAPIAccessAssignmentsSave(c *gin.Context) {
	if s.accessManager == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "Access manager not available"})
		return
	}

	var req struct {
		Assignments map[string][]string `json:"assignments"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request"})
		return
	}

	for intgStr, profiles := range req.Assignments {
		intg := accesscontrol.Integration(intgStr)
		if err := s.accessManager.SetAssignment(intg, profiles); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("Assignment error for %s: %v", intgStr, err)})
			return
		}
	}

	if err := s.accessManager.Save(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("Failed to save: %v", err)})
		return
	}

	// Sync to Lua
	s.applyAccessChange()

	c.JSON(http.StatusOK, gin.H{"success": true})
}

// handleAPIAccessReset resets all profiles and assignments to defaults.
//
//	@Summary		Reset access control
//	@Description	Resets all profiles and assignments to defaults and reassigns every MCP bot to the built-in full-access profile.
//	@Tags			access
//	@Produce		json
//	@Success		200	{object}	web.APIOK
//	@Failure		500	{object}	web.APIError
//	@Failure		503	{object}	web.APIError
//	@Security		ApiKeyAuth
//	@Router			/api/access/reset [post]
func (s *Server) handleAPIAccessReset(c *gin.Context) {
	if s.accessManager == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "Access manager not available"})
		return
	}

	s.accessManager.Reset()

	if err := s.accessManager.Save(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("Failed to save: %v", err)})
		return
	}

	// Also reset bot profiles in config
	_ = updateMasterConfig(s.serverDir, func(masterConfig *config.VCLUConfig) error {
		if masterConfig.MCP == nil {
			return errUnchanged
		}
		for i := range masterConfig.MCP.Bots {
			masterConfig.MCP.Bots[i].Profiles = []string{accesscontrol.BuiltinProfileFullAccess}
		}
		return nil
	})

	s.applyAccessChange()

	c.JSON(http.StatusOK, gin.H{"success": true})
}

// handleAPIAccessBotSet sets access level for a specific bot and object path.
// Auto-creates a _{id} profile and manages bot profile assignments.
//
//	@Summary		Set per-bot access level
//	@Description	Sets the access level (full, readonly or hidden) for one object path of a specific MCP bot. Auto-creates a dedicated "_{botId}" profile when needed, assigns it to the bot (replacing full-access) and drops overrides that match the profile default.
//	@Tags			access
//	@Accept			json
//	@Produce		json
//	@Param			request	body		object	true	"Bot access change: {botId, path, access}"
//	@Success		200		{object}	map[string]interface{}	"success, botId, path, access"
//	@Failure		400		{object}	web.APIError
//	@Failure		404		{object}	web.APIError
//	@Failure		500		{object}	web.APIError
//	@Failure		503		{object}	web.APIError
//	@Security		ApiKeyAuth
//	@Router			/api/access/bot [post]
func (s *Server) handleAPIAccessBotSet(c *gin.Context) {
	var req struct {
		BotID  string `json:"botId"`
		Path   string `json:"path"`
		Access string `json:"access"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request"})
		return
	}

	if req.BotID == "" || req.Path == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "botId and path are required"})
		return
	}

	access := accesscontrol.AccessLevel(req.Access)
	if access != accesscontrol.AccessFull && access != accesscontrol.AccessReadonly && access != accesscontrol.AccessHidden {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid access level. Must be: full, readonly, or hidden"})
		return
	}

	if s.accessManager == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "Access manager not available"})
		return
	}

	// The access file is saved inside fn, so a failure there leaves .vclu.json as it was
	err := config.UpdateMasterConfig(s.serverDir, func(masterConfig *config.VCLUConfig) error {
		// Find the bot
		if masterConfig.MCP == nil {
			return &configError{http.StatusNotFound, "Bot not found"}
		}

		botIdx := -1
		for i, b := range masterConfig.MCP.Bots {
			if b.ID == req.BotID {
				botIdx = i
				break
			}
		}
		if botIdx < 0 {
			return &configError{http.StatusNotFound, "Bot not found"}
		}

		bot := &masterConfig.MCP.Bots[botIdx]
		autoProfileName := "_" + bot.ID

		// Find or create auto profile for this bot
		profile := s.accessManager.GetProfile(autoProfileName)
		if profile == nil {
			// Create auto profile: default full (same as full-access base)
			profile = &accesscontrol.Profile{
				Default: accesscontrol.AccessFull,
				Objects: map[string]accesscontrol.AccessLevel{},
			}
			if err := s.accessManager.SaveProfile(autoProfileName, *profile); err != nil {
				return &configError{http.StatusInternalServerError, fmt.Sprintf("Failed to create bot profile: %v", err)}
			}
		}

		// Set the access level for this object
		if access == profile.Default || (access == accesscontrol.AccessFull && profile.Default == "") {
			// Remove override if it matches default
			delete(profile.Objects, req.Path)
		} else {
			if profile.Objects == nil {
				profile.Objects = map[string]accesscontrol.AccessLevel{}
			}
			profile.Objects[req.Path] = access
		}

		if err := s.accessManager.SaveProfile(autoProfileName, *profile); err != nil {
			return &configError{http.StatusInternalServerError, fmt.Sprintf("Failed to update bot profile: %v", err)}
		}

		// Ensure bot has the auto profile assigned (and remove full-access if present)
		hasAutoProfile := false
		var newProfiles []string
		for _, p := range bot.Profiles {
			if p == autoProfileName {
				hasAutoProfile = true
			}
			if p == accesscontrol.BuiltinProfileFullAccess {
				continue // remove full-access — auto profile replaces it
			}
			newProfiles = append(newProfiles, p)
		}
		if !hasAutoProfile {
			newProfiles = append(newProfiles, autoProfileName)
		}
		bot.Profiles = newProfiles

		// Save both config files
		if err := s.accessManager.Save(); err != nil {
			return &configError{http.StatusInternalServerError, fmt.Sprintf("Failed to save access control: %v", err)}
		}
		return nil
	})
	if ce, ok := asConfigError(err); ok {
		c.JSON(ce.status, gin.H{"error": ce.msg})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("Failed to save bot config: %v", err)})
		return
	}

	// Sync to Lua
	s.applyAccessChange()

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"botId":   req.BotID,
		"path":    req.Path,
		"access":  req.Access,
	})
}
