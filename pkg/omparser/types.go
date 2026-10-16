// Package omparser provides parsing functionality for Grenton om.lua files.
// It extracts object definitions, modules, and event handlers from the generated Lua code.
package omparser

// ObjectClass represents the type of Grenton object
type ObjectClass int

const (
	ClassCLU        ObjectClass = 0  // Main CLU object
	ClassCLURef     ObjectClass = 1  // Reference to another CLU
	ClassModule     ObjectClass = 2  // Physical module
	ClassDIN        ObjectClass = 3  // Digital Input
	ClassDOUT       ObjectClass = 4  // Digital Output
	ClassDIMMER     ObjectClass = 5  // Dimmer
	ClassLEDRGB     ObjectClass = 9  // RGB LED
	ClassROLLER_SH  ObjectClass = 11 // Roller Shutter (basic, no position)
	ClassAnalogIN   ObjectClass = 12 // Analog Input
	ClassAnalogOUT  ObjectClass = 13 // Analog Output
	ClassThermostat ObjectClass = 18 // Thermostat (climate device: CurrentTemp idx14, setpoint, State) - not a plain thermometer
	ClassPanelTemp  ObjectClass = 20 // Smart Panel temperature sensor (Value@idx0)
	ClassPanelLight ObjectClass = 21 // Smart Panel light/illuminance sensor (Value@idx0)
	ClassHumidity   ObjectClass = 22 // Humidity sensor (Value@idx0)
	ClassONE_WIRE   ObjectClass = 23 // 1-Wire temperature sensor (module-based)
	ClassROLLER     ObjectClass = 24 // Roller Shutter (with position)
	ClassLEDChannel ObjectClass = 49 // One channel (R, G, B, W) of an LED RGBW 2.0 module
)

// String returns the human-readable name of the object class
func (c ObjectClass) String() string {
	switch c {
	case ClassCLU:
		return "CLU"
	case ClassCLURef:
		return "CLU_REF"
	case ClassModule:
		return "MODULE"
	case ClassDIN:
		return "DIN"
	case ClassDOUT:
		return "DOUT"
	case ClassDIMMER:
		return "DIMMER"
	case ClassLEDRGB:
		return "LEDRGB"
	case ClassROLLER_SH:
		return "ROLLER_SH"
	case ClassAnalogIN:
		return "AnalogIN"
	case ClassAnalogOUT:
		return "AnalogOUT"
	case ClassThermostat:
		return "THERMOSTAT"
	case ClassPanelTemp:
		return "PANELSENSTEMP"
	case ClassPanelLight:
		return "PANELSENSLIGHT"
	case ClassHumidity:
		return "HUMIDITY_SENSOR"
	case ClassONE_WIRE:
		return "ONE_WIRE"
	case ClassROLLER:
		return "ROLLER_SHUTTER"
	case ClassLEDChannel:
		return "LED_CHANNEL"
	default:
		return "UNKNOWN"
	}
}

// MQTTType returns the Home Assistant component type for this object class
func (c ObjectClass) MQTTType() string {
	switch c {
	case ClassDOUT:
		return "switch"
	case ClassDIN:
		return "binary_sensor"
	case ClassDIMMER:
		return "light"
	case ClassLEDRGB, ClassLEDChannel:
		return "light"
	case ClassROLLER_SH, ClassROLLER:
		return "cover"
	case ClassAnalogIN:
		return "sensor"
	case ClassAnalogOUT:
		return "number"
	case ClassONE_WIRE, ClassPanelTemp, ClassPanelLight, ClassHumidity:
		return "sensor"
	// ClassThermostat (18) is a climate device, not yet exposed — see roadmap.
	default:
		return ""
	}
}

// IsExposable returns true if this object type can be exposed via MQTT
func (c ObjectClass) IsExposable() bool {
	return c.MQTTType() != ""
}

// CLUInfo contains information about the CLU itself
type CLUInfo struct {
	ID        string // e.g., "CLU212345678"
	Name      string // e.g., "DOM20" (from NAME_CLU comment)
	IP        uint32 // IP address as uint32
	FwType    string // Firmware type
	FwVersion string // Firmware version
	HwType    string // Hardware type
	HwVersion string // Hardware version
	IsGate    bool   // true if this is a GATE CLU (virtual CLU or integration hub)
}

// Module represents a physical Grenton module
type Module struct {
	ID         string // e.g., "mm_330000027"
	Serial     string // e.g., "330000027"
	Address    uint32 // Module address
	ModuleType int    // Module type identifier
}

// GrentonObject represents a single I/O object (DOUT, DIN, DIMMER, etc.)
type GrentonObject struct {
	ID       string      // e.g., "DOU5048"
	Name     string      // e.g., "P1_DACHOWE_BOK" (from NAME_IO comment)
	Class    ObjectClass // Object type (3=DIN, 4=DOUT, etc.)
	ModuleID string      // Parent module ID (e.g., "mm_330000027")
	Index    int         // Index on the module (0-7 typically)
}

// CLUReference represents a reference to another CLU
type CLUReference struct {
	ID string // e.g., "CLU_0d1cf150"
	IP uint32 // IP address as uint32
}

// EventHandler represents an event binding
type EventHandler struct {
	ObjectID   string // e.g., "DIN1473"
	EventIndex int    // Event index (1=OnSwitchOn, 2=OnSwitchOff, etc.)
	Handler    string // Function name or inline code
}

// ParsedOM contains all parsed data from an om.lua file
type ParsedOM struct {
	CLU        CLUInfo                  // Main CLU information
	References []CLUReference           // References to other CLUs
	Modules    []Module                 // Physical modules
	Objects    []GrentonObject          // I/O objects (DOUT, DIN, etc.)
	Events     []EventHandler           // Event handlers
	ObjectMap  map[string]GrentonObject // Quick lookup by ID
}

// NewParsedOM creates a new ParsedOM with initialized maps
func NewParsedOM() *ParsedOM {
	return &ParsedOM{
		References: make([]CLUReference, 0),
		Modules:    make([]Module, 0),
		Objects:    make([]GrentonObject, 0),
		Events:     make([]EventHandler, 0),
		ObjectMap:  make(map[string]GrentonObject),
	}
}

// GetObjectsByClass returns all objects of a specific class
func (p *ParsedOM) GetObjectsByClass(class ObjectClass) []GrentonObject {
	result := make([]GrentonObject, 0)
	for _, obj := range p.Objects {
		if obj.Class == class {
			result = append(result, obj)
		}
	}
	return result
}

// Module type ids (the third argument of a module's OBJECT:new in om.lua) of the LED RGBW
// 2.0 modules, DIN and FM; the older LED RGB module (0x04) has no white channel.
var rgbwModuleTypes = map[int]bool{0x18: true, 0x1d: true}

// HasWhite tells whether an LEDRGB object sits on an RGBW module.
func (p *ParsedOM) HasWhite(obj GrentonObject) bool {
	if obj.Class != ClassLEDRGB {
		return false
	}
	for _, m := range p.Modules {
		if m.ID == obj.ModuleID {
			return rgbwModuleTypes[m.ModuleType]
		}
	}
	return false
}

// GetExposableObjects returns all objects that can be exposed via MQTT
func (p *ParsedOM) GetExposableObjects() []GrentonObject {
	result := make([]GrentonObject, 0)
	for _, obj := range p.Objects {
		if obj.Class.IsExposable() {
			result = append(result, obj)
		}
	}
	return result
}
