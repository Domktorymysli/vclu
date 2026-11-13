package proto

// Pairing follows the OAuth device authorization grant (RFC 8628): vCLU asks
// for a code, shows it to the person, and polls until they confirm it in the
// cloud. JSON over plain HTTPS POST.

// PairStartRequest is sent by vCLU to PathPairStart.
type PairStartRequest struct {
	Name     string `json:"name"`     // shown when confirming, e.g. "vCLU Raspberry Pi 4"
	Version  string `json:"version"`  // vCLU version
	Platform string `json:"platform"` // e.g. "linux/arm64"
}

// PairStartResponse tells vCLU what to show and how to poll.
type PairStartResponse struct {
	DeviceCode      string `json:"device_code"`      // secret, only for polling
	UserCode        string `json:"user_code"`        // shown to the person, "KXQF-7MPA"
	VerifyURL       string `json:"verify_url"`       // where to enter it
	VerifyURLFull   string `json:"verify_url_full"`  // the same with the code filled in (for a QR code)
	Interval        int    `json:"interval"`         // seconds between polls
	ExpiresIn       int    `json:"expires_in"`       // seconds until the codes expire
	ProtocolVersion int    `json:"protocol_version"` // what the cloud speaks
}

// PairPollRequest is sent by vCLU to PathPairPoll every Interval seconds.
type PairPollRequest struct {
	DeviceCode string `json:"device_code"`
}

// PairPollResponse is the answer once the person confirmed (HTTP 200).
type PairPollResponse struct {
	DeviceID    int64  `json:"device_id"`
	DeviceToken string `json:"device_token"` // keep it secret; used as a Bearer token on PathConnect
	HomeID      int64  `json:"home_id"`
	HomeName    string `json:"home_name"`
	ConnectURL  string `json:"connect_url"` // wss://…/ws/device
}

// PairError is the body of a non-200 poll or start answer.
type PairError struct {
	Error string `json:"error"` // one of the Err* codes below
}

// Error codes from RFC 8628, section 3.5.
const (
	ErrAuthorizationPending = "authorization_pending" // keep polling
	ErrSlowDown             = "slow_down"             // poll less often: add 5 s to the interval
	ErrExpiredToken         = "expired_token"         // codes expired: start again
	ErrAccessDenied         = "access_denied"         // the person refused
	ErrInvalidRequest       = "invalid_request"
	ErrRateLimited          = "rate_limited"
)
