package satel

// ArmMode is the Integra arm mode (0-3).
type ArmMode int

// ZoneState is the cached state of a single alarm zone.
type ZoneState struct {
	Number   int    `json:"number"`
	Violated bool   `json:"violated"`
	Tamper   bool   `json:"tamper"`
	Alarm    bool   `json:"alarm"`
	Bypass   bool   `json:"bypass"`
	Name     string `json:"name,omitempty"`
}

// PartitionState is the cached state of a partition.
type PartitionState struct {
	Number    int    `json:"number"`
	Armed     bool   `json:"armed"`
	Alarm     bool   `json:"alarm"`
	FireAlarm bool   `json:"fireAlarm"`
	EntryTime bool   `json:"entryTime"`
	ExitTime  bool   `json:"exitTime"`
	Name      string `json:"name,omitempty"`
}

// OutputState is the cached state of an output.
type OutputState struct {
	Number int    `json:"number"`
	Active bool   `json:"active"`
	Name   string `json:"name,omitempty"`
}

// Event types delivered on Panel.Events().
const (
	EventConnection  = "connection"  // Connected + PanelType (+ Message on failure)
	EventZone        = "zone"        // Number + Zone
	EventPartition   = "partition"   // Number + Partition
	EventOutput      = "output"      // Number + Output
	EventTemperature = "temperature" // Number + Temp
	EventName        = "name"        // Kind + Number + Message (the name)
	EventResult      = "result"      // Op + Number + OK + Code + Message
)

// Event is a state change or async operation result emitted by the Panel.
type Event struct {
	Type      string          `json:"type"`
	Number    int             `json:"number,omitempty"`
	Kind      string          `json:"kind,omitempty"` // for name events: zone|partition|output
	Connected bool            `json:"connected,omitempty"`
	PanelType string          `json:"panelType,omitempty"`
	Zone      *ZoneState      `json:"zone,omitempty"`
	Partition *PartitionState `json:"partition,omitempty"`
	Output    *OutputState    `json:"output,omitempty"`
	Temp      *float64        `json:"temp,omitempty"`
	Op        string          `json:"op,omitempty"`
	OK        bool            `json:"ok,omitempty"`
	Code      byte            `json:"code,omitempty"`
	Message   string          `json:"message,omitempty"`
}

// panelModel describes an Integra panel type as reported by cmd 0x7E.
type panelModel struct {
	Name     string
	Zones    int
	Extended bool // INTEGRA 256 Plus: 32-byte zone/output masks via +0x00 reads
}

// panelModels maps 0x7E type codes to models (openHAB IntegraType table).
var panelModels = map[byte]panelModel{
	0:   {"INTEGRA 24", 24, false},
	1:   {"INTEGRA 32", 32, false},
	2:   {"INTEGRA 64", 64, false},
	3:   {"INTEGRA 128", 128, false},
	4:   {"INTEGRA 128-WRL SIM300", 128, false},
	66:  {"INTEGRA 64 Plus", 64, false},
	67:  {"INTEGRA 128 Plus", 128, false},
	72:  {"INTEGRA 256 Plus", 256, true},
	132: {"INTEGRA 128-WRL LEON", 128, false},
}
