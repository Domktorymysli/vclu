package web

import (
	"net/http"

	"github.com/Domktorymysli/vclu/pkg/config"
	"github.com/gin-gonic/gin"
)

func (s *Server) handleWizard(c *gin.Context) {
	if config.IsSetupComplete(s.serverDir) {
		c.Redirect(http.StatusFound, "/stats")
		return
	}

	trans := s.getTranslations(c)

	vcluConfig, err := config.ReadMasterConfig(s.serverDir)
	if err != nil {
		vcluConfig = nil
	}

	var wizardState struct {
		DeviceTypeID     string
		DeviceTypeName   string
		Category         string
		DeviceTypeLocked bool
		SupportsModules  bool
		Modules          []config.ModuleInstance
		IOObjects        []config.IOInstance
		PIN              string
		BroadcastEnabled bool
		AuthEnabled      bool
		AuthUsername     string
	}

	if vcluConfig != nil && vcluConfig.Device != nil {
		device := vcluConfig.Device
		wizardState.DeviceTypeID = device.DeviceTypeID
		wizardState.Category = string(device.Category)
		wizardState.DeviceTypeLocked = vcluConfig.SetupCompleted && device.IsDeviceTypeLocked()
		wizardState.SupportsModules = device.SupportsModules()
		wizardState.Modules = device.Modules
		wizardState.IOObjects = device.IOObjects

		if deviceType := config.GetDeviceByID(device.DeviceTypeID); deviceType != nil {
			wizardState.DeviceTypeName = deviceType.Name
		}
	}

	if vcluConfig != nil && vcluConfig.Keys != nil {
		wizardState.PIN = vcluConfig.Keys.PIN
		wizardState.BroadcastEnabled = vcluConfig.Keys.BroadcastEnabled
	}

	if vcluConfig != nil && vcluConfig.Auth != nil && vcluConfig.Auth.Username != "" {
		wizardState.AuthEnabled = true
		wizardState.AuthUsername = vcluConfig.Auth.Username
	}

	if wizardState.Modules == nil {
		wizardState.Modules = []config.ModuleInstance{}
	}
	if wizardState.IOObjects == nil {
		wizardState.IOObjects = []config.IOInstance{}
	}

	s.renderHTML(c, http.StatusOK, "wizard.html", gin.H{
		"title":       "Device Configuration Wizard",
		"deviceTypes": config.PredefinedDevices,
		"moduleTypes": config.PredefinedModules,
		"wizardState": wizardState,
		"lang":        trans["lang"],
		"t":           trans["t"],
		"langs":       trans["langs"],
	})
}
