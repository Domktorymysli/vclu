package config

import (
	"crypto/rand"
	"time"
)

// IsSetupComplete checks if setup has been completed
func IsSetupComplete(cluDir string) bool {
	config, err := ReadMasterConfig(cluDir)
	if err != nil || config == nil {
		return false
	}
	return config.SetupCompleted
}

// SetPaired marks CLU as paired and stores project key
func SetPaired(cluDir string, projectName, projectID, projectKey, projectIV string) error {
	masterConfigMu.Lock()
	defer masterConfigMu.Unlock()
	masterConfig, err := ReadMasterConfig(cluDir)
	if err != nil || masterConfig == nil {
		masterConfig = &VCLUConfig{}
	}

	masterConfig.Paired = true
	masterConfig.PairedAt = time.Now().Format(time.RFC3339)
	masterConfig.ProjectName = projectName
	masterConfig.ProjectID = projectID
	masterConfig.ProjectKey = projectKey
	masterConfig.ProjectIV = projectIV

	return writeMasterConfig(cluDir, masterConfig)
}

// GenerateGrentonLikeSerial generates a serial number similar to real Grenton devices
// Format: PREFIX + 5 random digits (e.g., 5200XXXXX for Gate, 2210XXXXX for CLU)
// If serialPrefix is 0, uses the default device's prefix.
func GenerateGrentonLikeSerial(serialPrefix ...uint32) uint64 {
	var prefix uint64
	if len(serialPrefix) > 0 && serialPrefix[0] > 0 {
		prefix = uint64(serialPrefix[0])
	} else {
		device := GetDefaultDevice()
		prefix = uint64(device.SerialPrefix)
	}

	// Generate random number 0-99999 (5 digits)
	randomBytes := make([]byte, 3)
	if _, err := rand.Read(randomBytes); err != nil {
		// Fallback to time-based if random fails
		return prefix*100000 + uint64(time.Now().UnixNano()%100000)
	}

	random := uint64(randomBytes[0])<<16 | uint64(randomBytes[1])<<8 | uint64(randomBytes[2])
	random = random % 100000 // 0-99999

	return prefix*100000 + random
}
