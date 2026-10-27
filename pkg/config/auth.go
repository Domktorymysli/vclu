package config

import (
	"crypto/rand"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"time"

	"golang.org/x/crypto/bcrypt"
)

// HashPassword creates a bcrypt hash of the password
func HashPassword(password string) (string, error) {
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return "", err
	}
	return string(hash), nil
}

// VerifyPassword checks if the password matches the hash.
// Supports both legacy SHA-1 hashes (40 hex chars) and bcrypt hashes.
func VerifyPassword(password, hash string) bool {
	// Backwards compat: legacy SHA-1 hash (40 hex chars)
	if len(hash) == 40 {
		h := sha1.New()
		h.Write([]byte(password))
		return hex.EncodeToString(h.Sum(nil)) == hash
	}
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) == nil
}

// IsAuthEnabled checks if authentication is configured
func IsAuthEnabled(cluDir string) bool {
	masterConfig, err := ReadMasterConfig(cluDir)
	if err != nil || masterConfig == nil {
		return false
	}
	return masterConfig.Auth != nil && masterConfig.Auth.Username != "" && masterConfig.Auth.PasswordHash != ""
}

// ValidateAuth checks username and password against stored credentials
func ValidateAuth(cluDir string, username, password string) bool {
	masterConfig, err := ReadMasterConfig(cluDir)
	if err != nil || masterConfig == nil || masterConfig.Auth == nil {
		return false
	}
	return masterConfig.Auth.Username == username && VerifyPassword(password, masterConfig.Auth.PasswordHash)
}

// SaveAuth saves authentication credentials to master config, keeping the API key
func SaveAuth(cluDir string, username, password string) error {
	// bcrypt takes a while; hash before taking the lock every save waits on
	hash, err := HashPassword(password)
	if err != nil {
		return err
	}

	masterConfigMu.Lock()
	defer masterConfigMu.Unlock()
	masterConfig, err := ReadMasterConfig(cluDir)
	if err != nil {
		return err
	}
	if masterConfig == nil {
		masterConfig = &VCLUConfig{
			CreatedAt: time.Now(),
		}
	}

	if masterConfig.Auth == nil {
		masterConfig.Auth = &VCLUAuthConfig{}
	}
	masterConfig.Auth.Username = username
	masterConfig.Auth.PasswordHash = hash

	return writeMasterConfig(cluDir, masterConfig)
}

// GenerateAPIKey generates a new API key and returns the plaintext key and its SHA-256 hash.
func GenerateAPIKey() (plaintext, hash string, err error) {
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return "", "", err
	}
	plaintext = hex.EncodeToString(key)
	h := sha256.Sum256([]byte(plaintext))
	hash = hex.EncodeToString(h[:])
	return plaintext, hash, nil
}

// HashAPIKey creates a SHA-256 hash of an API key for storage.
func HashAPIKey(key string) string {
	h := sha256.Sum256([]byte(key))
	return hex.EncodeToString(h[:])
}

// ValidateAPIKey checks if a given API key matches a stored SHA-256 hash.
func ValidateAPIKey(key, storedHash string) bool {
	h := sha256.Sum256([]byte(key))
	computed := hex.EncodeToString(h[:])
	return subtle.ConstantTimeCompare([]byte(computed), []byte(storedHash)) == 1
}
