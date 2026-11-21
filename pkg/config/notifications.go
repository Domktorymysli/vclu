package config

// VCLUNotificationsConfig is the "notifications" section of .vclu.json (pkg/notify).
type VCLUNotificationsConfig struct {
	// Sources turned on or off by the user; a source not listed keeps its default
	Sources map[string]bool `json:"sources,omitempty"`
	// Muted maps a notification key to how long it stays muted
	Muted map[string]VCLUNotificationMute `json:"muted,omitempty"`
}

// VCLUNotificationMute is one muted key: Until is an RFC 3339 time or "forever".
type VCLUNotificationMute struct {
	Until  string            `json:"until"`
	Title  string            `json:"title,omitempty"` // the entry's title when muted, for the settings
	Msg    string            `json:"msg,omitempty"`   // and its message, for the settings in the panel's language
	Params map[string]string `json:"params,omitempty"`
}
