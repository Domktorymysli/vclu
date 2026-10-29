package config

// DefaultCloudURL is the cloud vCLU pairs with unless cloud.url says otherwise.
const DefaultCloudURL = "https://cloud.vclu.pl"

// VCLUCloudConfig is the connection to vclu cloud. Token is a secret: it is
// never logged or returned by the API.
type VCLUCloudConfig struct {
	URL        string `json:"url,omitempty"` // empty = DefaultCloudURL
	DeviceID   int64  `json:"deviceId,omitempty"`
	Token      string `json:"token,omitempty"`
	ConnectURL string `json:"connectUrl,omitempty"`
	HomeID     int64  `json:"homeId,omitempty"`
	HomeName   string `json:"homeName,omitempty"`
	PairedAt   string `json:"pairedAt,omitempty"`
	// LocalPort is where the vCLU app connects on the LAN, with a local token
	// (HTTPS, pinned): 0 for DefaultLocalPort, -1 to turn it off.
	LocalPort int `json:"localPort,omitempty"`
	// Disabled keeps the pairing but no connection: the switch on the cloud
	// page or Cloud.disable() in Lua.
	Disabled bool `json:"disabled,omitempty"`
}

// DefaultLocalPort is the LAN listener for the app unless cloud.localPort says otherwise.
const DefaultLocalPort = 9443

// LocalListenPort returns the port of the LAN listener for the app, 0 when it is off.
func (c *VCLUCloudConfig) LocalListenPort() int {
	switch {
	case c == nil || c.LocalPort == 0:
		return DefaultLocalPort
	case c.LocalPort < 0:
		return 0
	}
	return c.LocalPort
}

// BaseURL returns the cloud address, the default when none is set.
func (c *VCLUCloudConfig) BaseURL() string {
	if c == nil || c.URL == "" {
		return DefaultCloudURL
	}
	return c.URL
}

// IsPaired reports whether vCLU has a device token.
func (c *VCLUCloudConfig) IsPaired() bool {
	return c != nil && c.Token != ""
}
