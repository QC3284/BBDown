package drm

import (
	"fmt"
	"os"
)

// DrmDecryptor is the entry point for DRM decryption.
type DrmDecryptor struct{}

// GetKeyWidevine retrieves a Widevine content key for DRM-protected content.
// Returns (kid, key) in hex.
func GetKeyWidevine(psshB64, wvdPath string) (*KeyPair, error) {
	if _, err := os.Stat(wvdPath); os.IsNotExist(err) {
		return nil, fmt.Errorf("device.wvd not found: %s", wvdPath)
	}

	keys, err := GetKeys(psshB64, wvdPath)
	if err != nil {
		return nil, fmt.Errorf("widevine key acquisition failed: %w", err)
	}

	if len(keys) == 0 {
		return nil, fmt.Errorf("no keys returned from license server")
	}

	return &keys[0], nil
}
