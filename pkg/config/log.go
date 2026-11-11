package config

// VCLULogConfig is the "log" section of .vclu.json, read at start.
type VCLULogConfig struct {
	// Level is the lowest level written to stdout: debug, info, warn or error; empty is info
	Level string `json:"level,omitempty"`
}

// LogLevel is the "log.level" setting, "" when not set. A nil config gives "".
func (c *VCLUConfig) LogLevel() string {
	if c == nil || c.Log == nil {
		return ""
	}
	return c.Log.Level
}
