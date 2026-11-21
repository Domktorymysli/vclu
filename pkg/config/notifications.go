package config

// VCLUNotificationsConfig is the "notifications" section of .vclu.json (pkg/notify).
type VCLUNotificationsConfig struct {
	// Disabled lists the sources that add nothing; nil means ["log"]
	Disabled *[]string `json:"disabled,omitempty"`
	// Muted maps a notification key to an RFC 3339 time or "forever"
	Muted map[string]string `json:"muted,omitempty"`
}
