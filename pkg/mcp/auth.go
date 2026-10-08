package mcp

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"net/http"
	"strings"

	"github.com/Domktorymysli/vclu/pkg/config"
)

// authenticateRequest checks the Authorization header and returns the matching bot (or nil).
// Always reads from config — zero in-memory cache.
func authenticateRequest(r *http.Request, cfg ConfigProvider) *config.MCPBot {
	token := extractBearerToken(r)
	if token == "" {
		return nil
	}

	bots := cfg.GetMCPBots()
	for i := range bots {
		if verifyTokenHash(token, bots[i].APIKeyHash) {
			return &bots[i]
		}
	}

	return nil
}

// validateBearerToken checks the Authorization header against stored hashes.
// Kept for backwards compatibility — returns true if any bot or legacy key matches.
func validateBearerToken(r *http.Request, cfg ConfigProvider) bool {
	return authenticateRequest(r, cfg) != nil
}

// extractBearerToken gets the token from Authorization: Bearer <token> header.
func extractBearerToken(r *http.Request) string {
	auth := r.Header.Get("Authorization")
	if auth == "" {
		return ""
	}
	const prefix = "Bearer "
	if !strings.HasPrefix(auth, prefix) {
		return ""
	}
	return strings.TrimPrefix(auth, prefix)
}

// verifyTokenHash compares a plaintext token against a SHA-256 hash.
func verifyTokenHash(token, storedHash string) bool {
	h := sha256.Sum256([]byte(token))
	tokenHash := hex.EncodeToString(h[:])
	return subtle.ConstantTimeCompare([]byte(tokenHash), []byte(storedHash)) == 1
}
