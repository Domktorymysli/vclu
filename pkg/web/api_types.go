package web

import "github.com/Domktorymysli/vclu/pkg/satel"

// Generic API response envelopes used by the OpenAPI annotations.
// Handlers historically respond with gin.H; these types document the
// stable shapes without changing wire formats.

// APIError is the generic error envelope returned by API endpoints.
type APIError struct {
	Error string `json:"error" example:"Invalid request"`
}

// APIOK is the generic success acknowledgement.
type APIOK struct {
	Success bool `json:"success" example:"true"`
}

// APISaveResult is returned by configuration-saving endpoints.
type APISaveResult struct {
	Success         bool `json:"success" example:"true"`
	RestartRequired bool `json:"restartRequired" example:"true"`
}

// SatelStatusResponse is the live status of the Satel integration.
type SatelStatusResponse struct {
	Enabled    bool   `json:"enabled" example:"true"`
	Running    bool   `json:"running" example:"true"`
	Connected  bool   `json:"connected" example:"true"`
	PanelType  string `json:"panelType" example:"INTEGRA 128"`
	Zones      int    `json:"zones,omitempty" example:"32"`
	Partitions int    `json:"partitions,omitempty" example:"4"`
	Outputs    int    `json:"outputs,omitempty" example:"16"`
	// Progress of a running panel scan (running=false when idle)
	Scan *satel.ScanProgress `json:"scan,omitempty"`
}

// SatelTestResponse is the result of an ETHM-1 connectivity test.
type SatelTestResponse struct {
	Success bool   `json:"success" example:"true"`
	Panel   string `json:"panel,omitempty" example:"INTEGRA 128 v1.23 (2012-05-27)"`
	Error   string `json:"error,omitempty"`
}
