package web

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"

	"github.com/Domktorymysli/vclu/pkg/config"
	"github.com/gin-gonic/gin"
)

// handleGrenton shows the Grenton integration page with tabs (Device, OM Files, Modules)
func (s *Server) handleGrenton(c *gin.Context) {
	trans := s.getTranslations(c)

	isStandalone := s.deviceConfig != nil && s.deviceConfig.IsStandaloneMode()

	// Determine active tab from query param (default: "device")
	activeTab := c.DefaultQuery("tab", "device")

	// Determine grenton enabled state from master config
	masterConfig, _ := config.ReadMasterConfig(s.serverDir)
	grentonEnabled := false
	if masterConfig != nil {
		grentonEnabled = masterConfig.IsGrentonEnabled()
	}

	// Count modules and IO objects
	moduleCount := 0
	ioObjectCount := 0
	if masterConfig != nil && masterConfig.Device != nil {
		moduleCount = len(masterConfig.Device.Modules)
		ioObjectCount = len(masterConfig.Device.IOObjects)
	}

	data := gin.H{
		"title":           "Grenton",
		"currentPage":     "grenton",
		"configured":      s.isConfigured,
		"supportsModules": s.supportsModules,
		"isStandalone":    isStandalone,
		"grentonEnabled":  grentonEnabled,
		"moduleCount":     moduleCount,
		"ioObjectCount":   ioObjectCount,
		"activeTab":       activeTab,
		"lang":            trans["lang"],
		"t":               trans["t"],
	}

	// Device info
	if s.deviceConfig != nil {
		deviceInfo := gin.H{
			"serialNumber":    fmt.Sprintf("%d", s.deviceConfig.SerialNumber),
			"macAddress":      s.deviceConfig.MACAddress,
			"hardwareType":    s.deviceConfig.HardwareType,
			"hardwareVersion": s.deviceConfig.HardwareVersion,
			"firmwareType":    s.deviceConfig.FirmwareType,
			"firmwareVersion": fmt.Sprintf("0x%08x", s.deviceConfig.FirmwareVersion),
		}

		if masterConfig != nil && masterConfig.Device != nil {
			if dt := config.GetDeviceByID(masterConfig.Device.DeviceTypeID); dt != nil {
				deviceInfo["deviceTypeName"] = dt.Name
				data["hasModules"] = dt.SupportsModules
			}
		}

		data["device"] = deviceInfo

		// Encryption keys
		if s.keys != nil {
			data["hasKeys"] = true
		}

		// Pairing status
		if masterConfig != nil && masterConfig.Paired {
			data["paired"] = true
			data["pairedAt"] = masterConfig.PairedAt
			data["projectName"] = masterConfig.ProjectName
		}
	}

	// OM Files tab data
	data["omFiles"] = config.GetOMFiles(s.serverDir)

	// Modules tab data (only for CLU devices)
	if masterConfig != nil && masterConfig.Device != nil && masterConfig.Device.SupportsModules() {
		modules := masterConfig.Device.Modules
		if modules == nil {
			modules = []config.ModuleInstance{}
		}
		ioObjects := masterConfig.Device.IOObjects
		if ioObjects == nil {
			ioObjects = []config.IOInstance{}
		}

		deviceTypeName := ""
		if dt := config.GetDeviceByID(masterConfig.Device.DeviceTypeID); dt != nil {
			deviceTypeName = dt.Name
		}

		data["deviceTypeID"] = masterConfig.Device.DeviceTypeID
		data["deviceTypeName"] = deviceTypeName
		data["modulesData"] = modules
		data["ioObjects"] = ioObjects
		data["moduleTypes"] = config.PredefinedModules
	}

	s.renderHTML(c, http.StatusOK, "grenton.html", data)
}

// handleAPIGrentonStatus returns Grenton integration status as JSON
//
//	@Summary		Grenton integration status
//	@Description	Whether the Grenton integration is enabled, whether the device runs in standalone mode, pairing state and the configured module/IO object counts. Read from .vclu.json.
//	@Tags			grenton
//	@Produce		json
//	@Success		200	{object}	map[string]interface{}	"enabled, standalone, paired, modules, ioObjects"
//	@Security		ApiKeyAuth
//	@Router			/api/grenton/status [get]
func (s *Server) handleAPIGrentonStatus(c *gin.Context) {
	masterConfig, _ := config.ReadMasterConfig(s.serverDir)

	enabled := false
	standalone := false
	paired := false
	moduleCount := 0
	ioObjectCount := 0

	if masterConfig != nil {
		enabled = masterConfig.IsGrentonEnabled()
		if masterConfig.Device != nil {
			standalone = masterConfig.Device.IsStandalone()
			moduleCount = len(masterConfig.Device.Modules)
			ioObjectCount = len(masterConfig.Device.IOObjects)
		}
		paired = masterConfig.Paired
	}

	c.JSON(http.StatusOK, gin.H{
		"enabled":    enabled,
		"standalone": standalone,
		"paired":     paired,
		"modules":    moduleCount,
		"ioObjects":  ioObjectCount,
	})
}

// handleAPIGrentonSave saves the Grenton enabled toggle
//
//	@Summary		Enable or disable Grenton integration
//	@Description	Persists the Grenton enabled flag to .vclu.json. A restart is required for the change to take effect.
//	@Tags			grenton
//	@Accept			json
//	@Produce		json
//	@Param			toggle	body		object	true	"Toggle: {enabled bool}"
//	@Success		200		{object}	web.APISaveResult
//	@Failure		400		{object}	web.APIError
//	@Failure		500		{object}	web.APIError
//	@Security		ApiKeyAuth
//	@Router			/api/grenton/save [post]
func (s *Server) handleAPIGrentonSave(c *gin.Context) {
	var req struct {
		Enabled bool `json:"enabled"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request"})
		return
	}

	masterConfig, err := config.ReadMasterConfig(s.serverDir)
	if err != nil || masterConfig == nil {
		masterConfig = &config.VCLUConfig{}
	}

	masterConfig.GrentonEnabled = &req.Enabled

	if err := config.WriteMasterConfig(s.serverDir, masterConfig); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to save configuration"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success":         true,
		"restartRequired": true,
	})
}

// --- Sync API handlers ---

// handleAPIGrentonSyncGet returns the sync configuration
//
//	@Summary		Get sync configuration
//	@Description	Returns the remote-object sync configuration from .vclu.json, or the defaults when none is stored.
//	@Tags			grenton
//	@Produce		json
//	@Success		200	{object}	config.VCLUSyncConfig
//	@Failure		500	{object}	web.APIError
//	@Security		ApiKeyAuth
//	@Router			/api/grenton/sync [get]
func (s *Server) handleAPIGrentonSyncGet(c *gin.Context) {
	masterConfig, err := config.ReadMasterConfig(s.serverDir)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to read config"})
		return
	}

	syncCfg := &config.VCLUSyncConfig{}
	if masterConfig != nil && masterConfig.Sync != nil {
		syncCfg = masterConfig.Sync
	} else {
		syncCfg = config.DefaultSyncConfig()
	}

	c.JSON(http.StatusOK, syncCfg)
}

// handleAPIGrentonSyncSave saves sync configuration, cleans stale entries, and pushes to Lua
//
//	@Summary		Save sync configuration
//	@Description	Clamps TTL/batch values to valid ranges, removes object paths no longer present in the Lua registry, persists the result to .vclu.json and pushes it to the Lua runtime immediately (no restart needed).
//	@Tags			grenton
//	@Accept			json
//	@Produce		json
//	@Param			config	body		config.VCLUSyncConfig	true	"Sync configuration"
//	@Success		200		{object}	map[string]interface{}	"success, objects (count kept), staleRemoved (paths dropped)"
//	@Failure		400		{object}	web.APIError
//	@Failure		500		{object}	web.APIError
//	@Security		ApiKeyAuth
//	@Router			/api/grenton/sync [post]
func (s *Server) handleAPIGrentonSyncSave(c *gin.Context) {
	var req config.VCLUSyncConfig
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request"})
		return
	}

	req.Clamp()

	// Clean stale entries: remove paths not in current registry
	var validPaths []string
	var stalePaths []string
	if s.luaExecutor != nil && len(req.Objects) > 0 {
		registryJSON, err := s.luaExecutor.Execute("_registry:toJSON()")
		if err == nil && registryJSON != "" && registryJSON != "nil" {
			// Parse registry to get valid paths
			var registryItems []struct {
				Path string `json:"path"`
			}
			if json.Unmarshal([]byte(registryJSON), &registryItems) == nil {
				registryPaths := make(map[string]bool, len(registryItems))
				for _, item := range registryItems {
					registryPaths[item.Path] = true
				}
				for _, path := range req.Objects {
					if registryPaths[path] {
						validPaths = append(validPaths, path)
					} else {
						stalePaths = append(stalePaths, path)
					}
				}
				req.Objects = validPaths
			}
		}
	}
	if req.Objects == nil {
		req.Objects = []string{}
	}

	// Save to .vclu.json
	masterConfig, err := config.ReadMasterConfig(s.serverDir)
	if err != nil || masterConfig == nil {
		masterConfig = &config.VCLUConfig{}
	}
	masterConfig.Sync = &req

	if err := config.WriteMasterConfig(s.serverDir, masterConfig); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to save configuration"})
		return
	}

	// Push to Lua
	s.syncRemoteConfigToLua(&req)

	c.JSON(http.StatusOK, gin.H{
		"success":      true,
		"objects":      len(req.Objects),
		"staleRemoved": stalePaths,
	})
}

// handleAPIGrentonSyncNow triggers an immediate autoSubscribe
//
//	@Summary		Trigger sync now
//	@Description	Runs LuaAPI.autoSubscribe() in the Lua runtime, immediately re-subscribing to all configured remote CLU objects.
//	@Tags			grenton
//	@Produce		json
//	@Success		200	{object}	map[string]interface{}	"status, result"
//	@Failure		500	{object}	web.APIError
//	@Failure		503	{object}	web.APIError
//	@Security		ApiKeyAuth
//	@Router			/api/grenton/sync/now [post]
func (s *Server) handleAPIGrentonSyncNow(c *gin.Context) {
	if s.luaExecutor == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "Lua executor not available"})
		return
	}

	result, err := s.luaExecutor.Execute("LuaAPI.autoSubscribe()")
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"status": "ok", "result": result})
}

// handleAPIGrentonSyncStatus returns current sync status from Lua
//
//	@Summary		Sync status
//	@Description	Returns the current remote-object sync status as reported by LuaAPI.getSyncStatus(); the JSON is passed through from the Lua runtime verbatim.
//	@Tags			grenton
//	@Produce		json
//	@Success		200	{object}	map[string]interface{}	"subscriptions, objects (shape defined by the Lua runtime)"
//	@Failure		500	{object}	web.APIError
//	@Failure		503	{object}	web.APIError
//	@Security		ApiKeyAuth
//	@Router			/api/grenton/sync/status [get]
func (s *Server) handleAPIGrentonSyncStatus(c *gin.Context) {
	if s.luaExecutor == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "Lua executor not available"})
		return
	}

	result, err := s.luaExecutor.Execute("LuaAPI.getSyncStatus()")
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.Header("Content-Type", "application/json")
	if result == "" || result == "nil" {
		c.String(http.StatusOK, `{"subscriptions":0,"objects":0}`)
	} else {
		c.String(http.StatusOK, result)
	}
}

// syncRemoteConfigToLua pushes sync config to Lua RemoteSubscription
func (s *Server) syncRemoteConfigToLua(syncCfg *config.VCLUSyncConfig) {
	if s.luaExecutor == nil || syncCfg == nil {
		return
	}

	// A config saved by an older version can be outside the current ranges
	clamped := *syncCfg
	clamped.Clamp()
	syncCfg = &clamped

	// Build objects array as Lua literal
	var objectsLua string
	if len(syncCfg.Objects) > 0 {
		parts := make([]string, len(syncCfg.Objects))
		for i, path := range syncCfg.Objects {
			parts[i] = fmt.Sprintf(`"%s"`, path)
		}
		objectsLua = "{" + strings.Join(parts, ", ") + "}"
	} else {
		objectsLua = "{}"
	}

	enabledStr := "false"
	if syncCfg.Enabled {
		enabledStr = "true"
	}

	script := fmt.Sprintf(
		`LuaAPI.syncRemoteConfig({enabled = %s, ttl = %d, batchSize = %d, batchDelay = %d, objects = %s})`,
		enabledStr, syncCfg.TTL, syncCfg.BatchSize, syncCfg.BatchDelay, objectsLua,
	)

	if _, err := s.luaExecutor.Execute(script); err != nil {
		log.Printf("Warning: Failed to sync remote config to Lua: %v", err)
	}
}

// GetSyncConfigSyncFunc returns a function that syncs remote config to Lua on reset
func (s *Server) GetSyncConfigSyncFunc() func() {
	return func() {
		masterConfig, err := config.ReadMasterConfig(s.serverDir)
		if err != nil || masterConfig == nil || masterConfig.Sync == nil {
			return
		}
		s.syncRemoteConfigToLua(masterConfig.Sync)
	}
}
