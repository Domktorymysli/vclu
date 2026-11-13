// Package proto holds the types of the vCLU ↔ cloud protocol, shared by the
// cloud and by vCLU. It has no dependencies. The cloud keeps the same package;
// change both together.
package proto

// Version is the protocol version this package describes. The cloud accepts
// Version and Version-1.
const Version = 1

// Paths on the cloud.
const (
	PathPairStart = "/api/device/pair/start"
	PathPairPoll  = "/api/device/pair/poll"
	PathUnpair    = "/api/device/unpair" // POST with the device token: the kill switch in vCLU
	PathConnect   = "/ws/device"
)

// Headers the cloud sets on HTTP requests through the tunnel (section 7).
// vCLU trusts them only on the tunnel listener; the cloud removes any a
// viewer sent.
const (
	HeaderActor   = "X-Vclu-Actor"   // "cloud:<user id>"
	HeaderRole    = "X-Vclu-Role"    // "owner", "admin", "member"
	HeaderInstall = "X-Vclu-Install" // the app installation asking for a local token (LocalInstall)
)

// Paths on vCLU's tunnel listener that only the cloud asks, never a viewer
// (the home origins do not pass them on).
const (
	// PathLocalToken issues a token for the app on the LAN (section 6a, "The
	// mobile app at home"): POST as the person, with HeaderInstall; vCLU
	// answers LocalGrant.
	PathLocalToken = "/_cloud/local-token"
)

// LocalGrant is vCLU's answer on PathLocalToken, passed to the app as it is:
// how to reach this vCLU on the LAN and with what. The app pins the
// certificate by Pin, since the LAN has no public name to check.
type LocalGrant struct {
	Token     string   `json:"token"`
	ExpiresAt int64    `json:"expires_at"` // unix seconds
	Addresses []string `json:"addresses"`  // "192.168.1.20:9443", tried in order
	Pin       string   `json:"pin"`        // "sha256/<base64 of the certificate's SubjectPublicKeyInfo hash>"
}
