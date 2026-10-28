package web

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"time"

	"github.com/Domktorymysli/vclu/pkg/config"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

type WizardStateResponse struct {
	DeviceTypeID         string                  `json:"deviceTypeId"`
	DeviceTypeName       string                  `json:"deviceTypeName"`
	Category             config.DeviceCategory   `json:"category"`
	DeviceTypeLocked     bool                    `json:"deviceTypeLocked"`
	SupportsModules      bool                    `json:"supportsModules"`
	AvailableDeviceTypes []config.DeviceType     `json:"availableDeviceTypes"`
	AvailableModuleTypes []config.ModuleType     `json:"availableModuleTypes"`
	Modules              []config.ModuleInstance `json:"modules"`
	IOObjects            []config.IOInstance     `json:"ioObjects"`
	PIN                  string                  `json:"pin"`
	BroadcastEnabled     bool                    `json:"broadcastEnabled"`
	AuthEnabled          bool                    `json:"authEnabled"`
	AuthUsername         string                  `json:"authUsername"`
}

type SetDeviceTypeRequest struct {
	DeviceTypeID string `json:"deviceTypeId" binding:"required"`
}

type AddModuleRequest struct {
	ModuleTypeID string `json:"moduleTypeId" binding:"required"`
	Name         string `json:"name"`
}

type AddModuleResponse struct {
	Module    config.ModuleInstance `json:"module"`
	IOObjects []config.IOInstance   `json:"ioObjects"`
}

func (s *Server) handleAPIWizardState(c *gin.Context) {
	vcluConfig, err := config.ReadMasterConfig(s.serverDir)
	if err != nil || vcluConfig == nil || vcluConfig.Device == nil {
		c.JSON(http.StatusOK, WizardStateResponse{
			DeviceTypeLocked:     false,
			SupportsModules:      false,
			AvailableDeviceTypes: config.PredefinedDevices,
			AvailableModuleTypes: config.PredefinedModules,
			Modules:              []config.ModuleInstance{},
			IOObjects:            []config.IOInstance{},
		})
		return
	}

	device := vcluConfig.Device
	deviceType := config.GetDeviceByID(device.DeviceTypeID)

	response := WizardStateResponse{
		DeviceTypeID:         device.DeviceTypeID,
		Category:             device.Category,
		DeviceTypeLocked:     device.IsDeviceTypeLocked(),
		SupportsModules:      device.SupportsModules(),
		AvailableDeviceTypes: config.PredefinedDevices,
		AvailableModuleTypes: config.PredefinedModules,
		Modules:              device.Modules,
		IOObjects:            device.IOObjects,
	}

	if deviceType != nil {
		response.DeviceTypeName = deviceType.Name
	}

	if response.Modules == nil {
		response.Modules = []config.ModuleInstance{}
	}
	if response.IOObjects == nil {
		response.IOObjects = []config.IOInstance{}
	}

	// Populate keys info
	if vcluConfig.Keys != nil {
		response.PIN = vcluConfig.Keys.PIN
		response.BroadcastEnabled = vcluConfig.Keys.BroadcastEnabled
	}

	// Populate auth info
	if vcluConfig.Auth != nil && vcluConfig.Auth.Username != "" {
		response.AuthEnabled = true
		response.AuthUsername = vcluConfig.Auth.Username
	}

	c.JSON(http.StatusOK, response)
}

func (s *Server) handleAPIWizardSetDeviceType(c *gin.Context) {
	var req SetDeviceTypeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request: " + err.Error()})
		return
	}

	deviceType := config.GetDeviceByID(req.DeviceTypeID)
	if deviceType == nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Unknown device type: " + req.DeviceTypeID})
		return
	}

	if err := config.UpdateMasterConfig(s.serverDir, func(vcluConfig *config.VCLUConfig) error {
		// under the lock, so a finish that lands first wins
		if vcluConfig.SetupCompleted {
			return &configError{http.StatusForbidden, "Setup already completed. Device type cannot be changed."}
		}
		if vcluConfig.Device == nil {
			vcluConfig.Device = &config.VCLUDeviceConfig{}
		}

		vcluConfig.Device.DeviceTypeID = deviceType.ID
		vcluConfig.Device.Category = deviceType.Category
		vcluConfig.Device.HardwareType = deviceType.HardwareType
		vcluConfig.Device.HardwareVersion = deviceType.HardwareVersion
		vcluConfig.Device.FirmwareType = deviceType.FirmwareType
		vcluConfig.Device.FirmwareVersion = deviceType.FirmwareVersion
		vcluConfig.Device.FirmwareVersionStr = deviceType.FirmwareVersionStr

		if vcluConfig.Device.SerialNumber == 0 {
			vcluConfig.Device.SerialNumber = config.GenerateGrentonLikeSerial(deviceType.SerialPrefix)
		}

		if !deviceType.SupportsModules {
			vcluConfig.Device.Modules = nil
			vcluConfig.Device.IOObjects = nil
		}
		return nil
	}); err != nil {
		if ce, ok := asConfigError(err); ok {
			c.JSON(ce.status, gin.H{"error": ce.msg})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to save configuration: " + err.Error()})
		return
	}

	log.Printf("Wizard: Device type set to %s (%s)", deviceType.Name, deviceType.Category)

	c.JSON(http.StatusOK, gin.H{
		"success":    true,
		"deviceType": deviceType,
	})
}

func (s *Server) handleAPIWizardAddModule(c *gin.Context) {
	var req AddModuleRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request: " + err.Error()})
		return
	}

	moduleType := config.GetModuleByID(req.ModuleTypeID)
	if moduleType == nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Unknown module type: " + req.ModuleTypeID})
		return
	}

	var moduleInstance config.ModuleInstance
	var ioObjects []config.IOInstance
	err := config.UpdateMasterConfig(s.serverDir, func(vcluConfig *config.VCLUConfig) error {
		if vcluConfig.Device == nil {
			return &configError{http.StatusBadRequest, "Device not configured. Please select device type first."}
		}

		if !vcluConfig.Device.SupportsModules() {
			return &configError{http.StatusForbidden, "This device type does not support modules"}
		}

		moduleInstance = config.ModuleInstance{
			ID:           uuid.New().String(),
			TypeID:       moduleType.ID,
			Serial:       config.GenerateModuleSerial(moduleType),
			Name:         req.Name,
			HardwareType: moduleType.HardwareType,
		}

		if moduleInstance.Name == "" {
			moduleInstance.Name = fmt.Sprintf("%s_%d", moduleType.Name, len(vcluConfig.Device.Modules)+1)
		}

		ioObjects = config.GenerateIOObjectsForModule(&moduleInstance, moduleType)

		vcluConfig.Device.Modules = append(vcluConfig.Device.Modules, moduleInstance)
		vcluConfig.Device.IOObjects = append(vcluConfig.Device.IOObjects, ioObjects...)
		return nil
	})
	if ce, ok := asConfigError(err); ok {
		c.JSON(ce.status, gin.H{"error": ce.msg})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to save configuration: " + err.Error()})
		return
	}

	log.Printf("Wizard: Added module %s (%s) with %d I/O objects", moduleInstance.Name, moduleInstance.Serial, len(ioObjects))

	c.JSON(http.StatusOK, AddModuleResponse{
		Module:    moduleInstance,
		IOObjects: ioObjects,
	})
}

func (s *Server) handleAPIWizardRemoveModule(c *gin.Context) {
	moduleID := c.Param("id")
	if moduleID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Module ID required"})
		return
	}

	err := config.UpdateMasterConfig(s.serverDir, func(vcluConfig *config.VCLUConfig) error {
		if vcluConfig.Device == nil {
			return &configError{http.StatusBadRequest, "Device not configured"}
		}

		found := false
		newModules := make([]config.ModuleInstance, 0, len(vcluConfig.Device.Modules))
		for _, m := range vcluConfig.Device.Modules {
			if m.ID == moduleID {
				found = true
				log.Printf("Wizard: Removing module %s (%s)", m.Name, m.Serial)
			} else {
				newModules = append(newModules, m)
			}
		}

		if !found {
			return &configError{http.StatusNotFound, "Module not found"}
		}

		newIOObjects := make([]config.IOInstance, 0, len(vcluConfig.Device.IOObjects))
		for _, io := range vcluConfig.Device.IOObjects {
			if io.ModuleID != moduleID {
				newIOObjects = append(newIOObjects, io)
			}
		}

		vcluConfig.Device.Modules = newModules
		vcluConfig.Device.IOObjects = newIOObjects
		return nil
	})
	if ce, ok := asConfigError(err); ok {
		c.JSON(ce.status, gin.H{"error": ce.msg})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to save configuration: " + err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"success": true})
}

type SetKeysRequest struct {
	PIN              string `json:"pin"`
	BroadcastEnabled bool   `json:"broadcastEnabled"`
	BroadcastKey     string `json:"broadcastKey"`
	DefaultIV        string `json:"defaultIV"`
	BroadcastIV      string `json:"broadcastIV"`
}

type SetSecurityRequest struct {
	Username        string `json:"username"`
	Password        string `json:"password"`
	PasswordConfirm string `json:"passwordConfirm"`
}

func (s *Server) handleAPIWizardSetKeys(c *gin.Context) {
	var req SetKeysRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request: " + err.Error()})
		return
	}

	// Validate PIN
	if len(req.PIN) != 8 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "PIN must be exactly 8 characters"})
		return
	}

	// Validate broadcast keys if enabled
	if req.BroadcastEnabled {
		bk, err := base64.StdEncoding.DecodeString(req.BroadcastKey)
		if err != nil || len(bk) != 16 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Default Key must be valid Base64 encoding of 16 bytes"})
			return
		}
		di, err := base64.StdEncoding.DecodeString(req.DefaultIV)
		if err != nil || len(di) != 16 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Default IV must be valid Base64 encoding of 16 bytes"})
			return
		}
		bi, err := base64.StdEncoding.DecodeString(req.BroadcastIV)
		if err != nil || len(bi) != 16 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Default Broadcast IV must be valid Base64 encoding of 16 bytes"})
			return
		}
	}

	err := config.UpdateMasterConfig(s.serverDir, func(vcluConfig *config.VCLUConfig) error {
		// Auto-generate Key/IV if not set
		if vcluConfig.Keys == nil {
			key := make([]byte, 16)
			iv := make([]byte, 16)
			if _, err := rand.Read(key); err != nil {
				return &configError{http.StatusInternalServerError, "Failed to generate keys"}
			}
			if _, err := rand.Read(iv); err != nil {
				return &configError{http.StatusInternalServerError, "Failed to generate keys"}
			}
			vcluConfig.Keys = &config.VCLUKeysConfig{
				Key:       base64.StdEncoding.EncodeToString(key),
				IV:        base64.StdEncoding.EncodeToString(iv),
				DefaultIV: base64.StdEncoding.EncodeToString(iv),
			}
		}

		vcluConfig.Keys.PIN = req.PIN
		vcluConfig.Keys.BroadcastEnabled = req.BroadcastEnabled
		if req.BroadcastEnabled {
			vcluConfig.Keys.BroadcastKey = req.BroadcastKey
			vcluConfig.Keys.DefaultIV = req.DefaultIV
			vcluConfig.Keys.BroadcastIV = req.BroadcastIV
		} else {
			vcluConfig.Keys.BroadcastKey = ""
			vcluConfig.Keys.BroadcastIV = ""
			// Keep DefaultIV — it's auto-generated and needed for discovery handshake
			// independently of broadcast keys
		}
		return nil
	})
	if ce, ok := asConfigError(err); ok {
		c.JSON(ce.status, gin.H{"error": ce.msg})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to save keys: " + err.Error()})
		return
	}

	log.Printf("Wizard: Keys saved (PIN set, broadcast=%v)", req.BroadcastEnabled)
	c.JSON(http.StatusOK, gin.H{"success": true})
}

func (s *Server) handleAPIWizardSetSecurity(c *gin.Context) {
	var req SetSecurityRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request: " + err.Error()})
		return
	}

	if len(req.Username) < 3 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Username must be at least 3 characters"})
		return
	}
	if len(req.Password) < 8 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Password must be at least 8 characters"})
		return
	}
	if req.Password != req.PasswordConfirm {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Passwords do not match"})
		return
	}

	if err := config.SaveAuth(s.serverDir, req.Username, req.Password); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to save auth: " + err.Error()})
		return
	}
	log.Printf("Wizard: Auth enabled for user %q", req.Username)

	c.JSON(http.StatusOK, gin.H{"success": true})
}

func (s *Server) handleAPIWizardFinish(c *gin.Context) {
	var vcluConfig *config.VCLUConfig
	err := config.UpdateMasterConfig(s.serverDir, func(mc *config.VCLUConfig) error {
		if mc.Device == nil {
			return &configError{http.StatusBadRequest, "Device not configured"}
		}

		if mc.Device.DeviceTypeID == "" {
			return &configError{http.StatusBadRequest, "Please select a device type first"}
		}

		if mc.Auth == nil || mc.Auth.Username == "" || mc.Auth.PasswordHash == "" {
			return &configError{http.StatusBadRequest, "Password is required. Please go back to the Security step."}
		}

		mc.SetupCompleted = true

		// Ensure keys are generated if not set during wizard
		if mc.Keys == nil {
			key := make([]byte, 16)
			iv := make([]byte, 16)
			if _, err := rand.Read(key); err != nil {
				return &configError{http.StatusInternalServerError, "Failed to generate keys"}
			}
			if _, err := rand.Read(iv); err != nil {
				return &configError{http.StatusInternalServerError, "Failed to generate keys"}
			}
			mc.Keys = &config.VCLUKeysConfig{
				Key:       base64.StdEncoding.EncodeToString(key),
				IV:        base64.StdEncoding.EncodeToString(iv),
				DefaultIV: base64.StdEncoding.EncodeToString(iv),
				PIN:       "00000000",
			}
		}
		vcluConfig = mc
		return nil
	})
	if ce, ok := asConfigError(err); ok {
		c.JSON(ce.status, gin.H{"error": ce.msg})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to save configuration: " + err.Error()})
		return
	}

	localIPStr := s.getLocalIP()
	localIP := config.IPToUint32(localIPStr)

	generator := config.NewOMGenerator(vcluConfig.Device, localIP)
	omContent, err := generator.Generate()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to generate om.lua: " + err.Error()})
		return
	}

	if err := config.WriteOMLua(s.serverDir, omContent); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to write om.lua: " + err.Error()})
		return
	}

	// Re-read config and propagate with fresh keys from .vclu.json
	// (not stale s.keys/s.deviceConfig which are from server startup)
	localNetIP := net.ParseIP(localIPStr)
	macStr := s.getMACAddress()
	macAddr, _ := net.ParseMAC(macStr)
	if _, _, err := config.LoadAndPropagateConfig(s.serverDir, localNetIP, macAddr); err != nil {
		log.Printf("Warning: Failed to propagate config: %v", err)
	}

	log.Printf("Wizard: Configuration finalized. Device type: %s, Modules: %d, I/O Objects: %d",
		vcluConfig.Device.DeviceTypeID,
		len(vcluConfig.Device.Modules),
		len(vcluConfig.Device.IOObjects))

	// The server restarts itself: the wizard's security step has just turned the login on, so a
	// POST /api/restart from the page, which has no session yet, would get 401 and vCLU would keep
	// running without the keys and the Grenton protocol until restarted by hand.
	s.scheduleRestart()

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "Configuration saved. Virtual CLU restarts to apply it.",
		"device": gin.H{
			"type":      vcluConfig.Device.DeviceTypeID,
			"category":  vcluConfig.Device.Category,
			"modules":   len(vcluConfig.Device.Modules),
			"ioObjects": len(vcluConfig.Device.IOObjects),
		},
	})
}

func (s *Server) handleAPIWizardGenerateOMPreview(c *gin.Context) {
	vcluConfig, err := config.ReadMasterConfig(s.serverDir)
	if err != nil || vcluConfig == nil || vcluConfig.Device == nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Device not configured"})
		return
	}

	localIPStr := s.getLocalIP()
	localIP := config.IPToUint32(localIPStr)

	generator := config.NewOMGenerator(vcluConfig.Device, localIP)
	omContent, err := generator.Generate()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to generate om.lua: " + err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"content": omContent,
	})
}

// handleAPIHealth is a liveness probe.
//
//	@Summary		Health check
//	@Description	Liveness probe. Always returns {"status": "ok"} when the web server is up. Public — no authentication required.
//	@Tags			system
//	@Produce		json
//	@Success		200	{object}	map[string]interface{}	"status: \"ok\""
//	@Router			/api/health [get]
func (s *Server) handleAPIHealth(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

// handleAPIRestart restarts the vCLU process.
//
//	@Summary		Restart vCLU
//	@Description	Acknowledges the request, then exits the process after 500 ms so the service manager (e.g. systemd) restarts vCLU. All in-memory state is lost; configuration on disk is untouched.
//	@Tags			system
//	@Produce		json
//	@Success		200	{object}	map[string]interface{}	"message: \"Restarting...\""
//	@Security		ApiKeyAuth
//	@Router			/api/restart [post]
func (s *Server) handleAPIRestart(c *gin.Context) {
	log.Printf("CLU restart requested")
	c.JSON(http.StatusOK, gin.H{"message": "Restarting..."})
	s.scheduleRestart()
}

// scheduleRestart exits the process after 500 ms, time for the answer to reach the browser, so
// the service manager starts vCLU again. Tests set s.restart instead.
func (s *Server) scheduleRestart() {
	if s.restart != nil {
		s.restart()
		return
	}
	go func() {
		time.Sleep(500 * time.Millisecond)
		log.Printf("Restarting vCLU...")
		os.Exit(0)
	}()
}
