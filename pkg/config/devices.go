package config

type DeviceCategory string

const (
	DeviceCategoryGate       DeviceCategory = "GATE"
	DeviceCategoryCLU        DeviceCategory = "CLU"
	DeviceCategoryStandalone DeviceCategory = "STANDALONE"
)

type DeviceType struct {
	ID                 string         `json:"id"`
	Name               string         `json:"name"`
	Category           DeviceCategory `json:"category"`
	Description        string         `json:"description"`
	SerialPrefix       uint32         `json:"serialPrefix"`
	HardwareType       uint8          `json:"hardwareType"`
	HardwareVersion    uint8          `json:"hardwareVersion"`
	FirmwareType       uint8          `json:"firmwareType"`
	FirmwareVersion    uint32         `json:"firmwareVersion"`
	FirmwareVersionStr string         `json:"firmwareVersionStr"`
	SupportsModules    bool           `json:"supportsModules"`
	Icon               string         `json:"icon"`
}

var PredefinedDevices = []DeviceType{
	{
		ID:                 "standalone",
		Name:               "Standalone",
		Category:           DeviceCategoryStandalone,
		Description:        "Independent smart home controller",
		SerialPrefix:       9900,
		HardwareType:       0x00,
		HardwareVersion:    0x01,
		FirmwareType:       0x00,
		FirmwareVersion:    0x000003e8,
		FirmwareVersionStr: "1.0.0-vclu",
		SupportsModules:    false,
		Icon:               "bi-hdd-network",
	},
	{
		ID:                 "gate",
		Name:               "CLU",
		Category:           DeviceCategoryGate,
		Description:        "CLU without I/O modules",
		SerialPrefix:       5200,
		HardwareType:       0x12,
		HardwareVersion:    0x01,
		FirmwareType:       0x03,
		FirmwareVersion:    0x000003e8,
		FirmwareVersionStr: "1.0.0-vclu",
		SupportsModules:    false,
		Icon:               "bi-router",
	},
	{
		ID:                 "clu",
		Name:               "CLU",
		Category:           DeviceCategoryCLU,
		Description:        "Standard CLU with I/O modules",
		SerialPrefix:       2210,
		HardwareType:       0x13,
		HardwareVersion:    0x01,
		FirmwareType:       0x03,
		FirmwareVersion:    0x000001f6,
		FirmwareVersionStr: "1.0.0-vclu",
		SupportsModules:    true,
		Icon:               "bi-cpu",
	},
}

func GetDefaultDevice() DeviceType {
	return PredefinedDevices[0]
}

func GetDeviceByID(id string) *DeviceType {
	for i := range PredefinedDevices {
		if PredefinedDevices[i].ID == id {
			return &PredefinedDevices[i]
		}
	}
	return nil
}
