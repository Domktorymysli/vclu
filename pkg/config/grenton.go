package config

// VCLUGrentonConfig is the "grenton" section of .vclu.json: settings for the Grenton CLUs
// vCLU talks to.
type VCLUGrentonConfig struct {
	// GateAlarm applies to every GATE Alarm imported from Object Manager
	GateAlarm *VCLUGateAlarmConfig `json:"gateAlarm,omitempty"`
}

// VCLUGateAlarmConfig holds what vCLU may do with a GATE Alarm's alarm objects.
type VCLUGateAlarmConfig struct {
	// AllowDisarm lets vCLU disarm a GATE Alarm's Satel zones (partitions) and Jablotron
	// sections; arming always works. Default false, as the Satel integration's allowDisarm.
	AllowDisarm bool `json:"allowDisarm,omitempty"`
}

// GateAlarmAllowDisarm is grenton.gateAlarm.allowDisarm: false when unset or on a nil config.
func (c *VCLUConfig) GateAlarmAllowDisarm() bool {
	return c != nil && c.Grenton != nil && c.Grenton.GateAlarm != nil && c.Grenton.GateAlarm.AllowDisarm
}
