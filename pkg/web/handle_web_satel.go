package web

import (
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/Domktorymysli/vclu/pkg/config"
	"github.com/Domktorymysli/vclu/pkg/i18n"
	"github.com/Domktorymysli/vclu/pkg/lua"
	"github.com/Domktorymysli/vclu/pkg/satel"
)

// SetSatelManager wires the Satel manager (nil when the integration is
// disabled at startup; the page then offers enabling + restart).
func (s *Server) SetSatelManager(sm *lua.SatelManager) {
	s.satelManager = sm
}

// handleSatel renders the Satel integration page.
func (s *Server) handleSatel(c *gin.Context) {
	trans := s.getTranslations(c)

	cfg := config.GetSatelConfig(s.serverDir)
	if cfg == nil {
		cfg = config.DefaultSatelConfig()
	}

	connected := false
	panelType := ""
	if s.satelManager != nil {
		connected = s.satelManager.Panel().Connected()
		panelType = s.satelManager.Panel().PanelType()
	}

	// Satel translation section as JSON for the external satel.js
	lang, _ := trans["lang"].(string)
	i18nJSON, err := json.Marshal(i18n.GetSection(lang, "satel"))
	if err != nil {
		i18nJSON = []byte("{}")
	}

	data := gin.H{
		"satelI18n":   template.JS(i18nJSON),
		"title":       "Satel",
		"currentPage": "satel",
		"configured":  s.isConfigured,
		"activeTab":   c.DefaultQuery("tab", "connection"),
		"lang":        trans["lang"],
		"t":           trans["t"],

		"satelEnabled": cfg.Enabled,
		"running":      s.satelManager != nil,
		"connected":    connected,
		"panelType":    panelType,

		"host":             cfg.Host,
		"port":             cfg.Port,
		"pollInterval":     cfg.PollInterval,
		"armMode":          cfg.ArmMode,
		"allowDisarm":      cfg.AllowDisarm,
		"allowForceArm":    cfg.AllowForceArm,
		"zones":            cfg.Zones,
		"partitions":       cfg.Partitions,
		"outputs":          cfg.Outputs,
		"tempZones":        cfg.TempZones,
		"hasUserCode":      cfg.UserCode != "",
		"hasEncryptionKey": cfg.EncryptionKey != "",
	}

	s.renderHTML(c, http.StatusOK, "satel.html", data)
}

// handleAPISatelStatus returns live integration status as JSON.
//
//	@Summary		Satel integration status
//	@Description	Whether the integration is enabled, running and connected, plus the detected panel model and monitored object counts.
//	@Tags			satel
//	@Produce		json
//	@Success		200	{object}	web.SatelStatusResponse
//	@Security		ApiKeyAuth
//	@Router			/api/satel/status [get]
func (s *Server) handleAPISatelStatus(c *gin.Context) {
	cfg := config.GetSatelConfig(s.serverDir)
	enabled := cfg != nil && cfg.Enabled

	resp := gin.H{
		"enabled":   enabled,
		"running":   s.satelManager != nil,
		"connected": false,
		"panelType": "",
	}
	if s.satelManager != nil {
		resp["connected"] = s.satelManager.Panel().Connected()
		resp["panelType"] = s.satelManager.Panel().PanelType()
		opts := s.satelManager.Options()
		resp["zones"] = len(opts.Zones)
		resp["partitions"] = len(opts.Partitions)
		resp["outputs"] = len(opts.Outputs)
		resp["scan"] = s.satelManager.Panel().ScanProgress()
	}
	c.JSON(http.StatusOK, resp)
}

// satelSaveRequest mirrors VCLUSatelConfig for the save endpoint.
// Secrets use pointers: nil = keep the stored value, "" = clear it.
type satelSaveRequest struct {
	Enabled       bool    `json:"enabled"`
	Host          string  `json:"host"`
	Port          int     `json:"port"`
	UserCode      *string `json:"userCode"`
	EncryptionKey *string `json:"encryptionKey"`
	PollInterval  int     `json:"pollInterval"`
	Zones         string  `json:"zones"`
	Partitions    string  `json:"partitions"`
	Outputs       string  `json:"outputs"`
	TempZones     string  `json:"tempZones"`
	ArmMode       int     `json:"armMode"`
	AllowDisarm   bool    `json:"allowDisarm"`
	AllowForceArm bool    `json:"allowForceArm"`
}

// handleAPISatelSave validates and stores the Satel configuration.
//
//	@Summary		Save Satel configuration
//	@Description	Validates and stores the Satel section of .vclu.json. Secrets are write-only: send null to keep the stored value, an empty string to clear it. A restart is required for changes to take effect.
//	@Tags			satel
//	@Accept			json
//	@Produce		json
//	@Param			config	body		web.satelSaveRequest	true	"Satel configuration"
//	@Success		200		{object}	web.APISaveResult
//	@Failure		400		{object}	web.APIError
//	@Failure		500		{object}	web.APIError
//	@Security		ApiKeyAuth
//	@Router			/api/satel/config [post]
func (s *Server) handleAPISatelSave(c *gin.Context) {
	var req satelSaveRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request"})
		return
	}

	if req.Enabled && req.Host == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Host is required"})
		return
	}
	for field, spec := range map[string]string{
		"zones": req.Zones, "partitions": req.Partitions,
		"outputs": req.Outputs, "tempZones": req.TempZones,
	} {
		if spec == "" {
			continue
		}
		if _, err := satel.ParseRange(spec); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("Invalid %s range: %v", field, err)})
			return
		}
	}
	if req.ArmMode < 0 || req.ArmMode > 3 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Arm mode must be 0-3"})
		return
	}
	if req.Port < 0 || req.Port > 65535 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid port"})
		return
	}
	if req.PollInterval != 0 && (req.PollInterval < 250 || req.PollInterval > 10000) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Poll interval must be 250-10000 ms"})
		return
	}

	if err := config.UpdateMasterConfig(s.serverDir, func(mc *config.VCLUConfig) error {
		old := mc.Satel
		if old == nil {
			old = config.DefaultSatelConfig()
		}

		cfg := &config.VCLUSatelConfig{
			Enabled:       req.Enabled,
			Host:          req.Host,
			Port:          req.Port,
			UserCode:      old.UserCode,
			EncryptionKey: old.EncryptionKey,
			PollInterval:  req.PollInterval,
			TempPoll:      old.TempPoll,
			Zones:         req.Zones,
			Partitions:    req.Partitions,
			Outputs:       req.Outputs,
			TempZones:     req.TempZones,
			ArmMode:       req.ArmMode,
			AllowDisarm:   req.AllowDisarm,
			AllowForceArm: req.AllowForceArm,
			ReadNames:     old.ReadNames,
		}
		if req.UserCode != nil {
			cfg.UserCode = *req.UserCode
		}
		if req.EncryptionKey != nil {
			cfg.EncryptionKey = *req.EncryptionKey
		}
		mc.Satel = cfg
		return nil
	}); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to save configuration"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success":         true,
		"restartRequired": true,
	})
}

// handleAPISatelTest tests connectivity to an ETHM-1 module with the given
// (or stored) parameters, without touching the running integration.
//
//	@Summary		Test ETHM-1 connection
//	@Description	Dials the ETHM-1 module, reads the panel model/version (cmd 0x7E) and disconnects. Validates host, port and the integration (encryption) key. Send encryptionKey null to test with the stored key.
//	@Tags			satel
//	@Accept			json
//	@Produce		json
//	@Param			target	body		object	true	"Connection target: {host, port, encryptionKey}"
//	@Success		200		{object}	web.SatelTestResponse
//	@Failure		400		{object}	web.APIError
//	@Security		ApiKeyAuth
//	@Router			/api/satel/test [post]
func (s *Server) handleAPISatelTest(c *gin.Context) {
	var req struct {
		Host          string  `json:"host"`
		Port          int     `json:"port"`
		EncryptionKey *string `json:"encryptionKey"` // nil = use stored key
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request"})
		return
	}

	key := ""
	if req.EncryptionKey != nil {
		key = *req.EncryptionKey
	} else if cfg := config.GetSatelConfig(s.serverDir); cfg != nil {
		key = cfg.EncryptionKey
	}

	info, err := satel.TestConnection(req.Host, req.Port, key, 5*time.Second)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "panel": info})
}

// handleAPISatelScan reads names of all zones/partitions/outputs from the
// connected panel (over a minute on ETHM-1 — the frontend shows progress).
//
//	@Summary		Scan panel devices
//	@Description	Reads names of all zones, partitions and outputs from the connected panel (cmd 0xEE). Takes over a minute on ETHM-1 (~300 queries); progress is reported by /api/satel/status. Requires a connected panel. The result is saved and available from GET /api/satel/scan.
//	@Tags			satel
//	@Produce		json
//	@Success		200	{object}	satel.ScanResult
//	@Failure		409	{object}	web.APIError
//	@Failure		500	{object}	web.APIError
//	@Failure		503	{object}	web.APIError
//	@Security		ApiKeyAuth
//	@Router			/api/satel/scan [post]
func (s *Server) handleAPISatelScan(c *gin.Context) {
	if s.satelManager == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "Satel integration is not running (enable it and restart vCLU)"})
		return
	}
	if !s.satelManager.Panel().Connected() {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "Panel is not connected"})
		return
	}

	// ~300 name reads at ~275 ms each on ETHM-1
	result, err := s.satelManager.Panel().Scan(5 * time.Minute)
	if errors.Is(err, satel.ErrScanRunning) {
		c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	// Kept so the selection can be changed later without rescanning
	if err := satel.SaveScan(s.serverDir, result, time.Now()); err != nil {
		log.Printf("[SATEL] saving scan result failed: %v", err)
	}
	c.JSON(http.StatusOK, result)
}

// handleAPISatelLastScan returns the last successful panel scan.
//
//	@Summary		Last panel scan
//	@Description	The last successful scan result saved by POST /api/satel/scan, so devices can be re-selected without rescanning. 404 if no scan was saved yet.
//	@Tags			satel
//	@Produce		json
//	@Success		200	{object}	satel.SavedScan
//	@Failure		404	{object}	web.APIError
//	@Failure		500	{object}	web.APIError
//	@Security		ApiKeyAuth
//	@Router			/api/satel/scan [get]
func (s *Server) handleAPISatelLastScan(c *gin.Context) {
	saved, err := satel.LoadScan(s.serverDir)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if saved == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "No saved scan"})
		return
	}
	c.JSON(http.StatusOK, saved)
}

// handleAPISatelState returns live cached states of all configured objects.
//
//	@Summary		Live panel state
//	@Description	Cached states of all monitored zones, partitions, outputs and temperatures. Served from the Go-side cache — never blocks on panel I/O.
//	@Tags			satel
//	@Produce		json
//	@Success		200	{object}	map[string]interface{}	"connected, panelType, zones []ZoneState, partitions []PartitionState, outputs []OutputState, temps map[zone]°C"
//	@Failure		503	{object}	web.APIError
//	@Security		ApiKeyAuth
//	@Router			/api/satel/state [get]
func (s *Server) handleAPISatelState(c *gin.Context) {
	if s.satelManager == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "Satel integration is not running"})
		return
	}

	panel := s.satelManager.Panel()
	opts := s.satelManager.Options()

	zones := make([]satel.ZoneState, 0, len(opts.Zones))
	for _, n := range opts.Zones {
		zones = append(zones, panel.GetZone(n))
	}
	partitions := make([]satel.PartitionState, 0, len(opts.Partitions))
	for _, n := range opts.Partitions {
		partitions = append(partitions, panel.GetPartition(n))
	}
	outputs := make([]satel.OutputState, 0, len(opts.Outputs))
	for _, n := range opts.Outputs {
		outputs = append(outputs, panel.GetOutput(n))
	}
	temps := map[int]float64{}
	for _, n := range opts.TempZones {
		if t, ok := panel.GetTemperature(n); ok {
			temps[n] = t
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"connected":  panel.Connected(),
		"panelType":  panel.PanelType(),
		"zones":      zones,
		"partitions": partitions,
		"outputs":    outputs,
		"temps":      temps,
	})
}
