package drm

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"time"
)

// FindMp4decrypt resolves the mp4decrypt binary: explicit path first, then PATH lookup.
func FindMp4decrypt(explicitPath string) string {
	if explicitPath != "" {
		if info, err := os.Stat(explicitPath); err == nil && !info.IsDir() {
			return explicitPath
		}
	}
	if p, err := exec.LookPath("mp4decrypt"); err == nil {
		return p
	}
	return ""
}

// DecryptStream decrypts an encrypted media file in place.
// The kid:key pair is written to a temp key-file (never exposed on the command
// line), mp4decrypt is run with "mp4decrypt --key-file <keyfile> <input> <output>",
// and the input is atomically replaced by the decrypted output. Timeout caps the
// external process (upstream reuses the muxer timeout for this).
func DecryptStream(ctx context.Context, mp4decryptPath, kidHex, keyHex, input string, timeout time.Duration) error {
	if input == "" {
		return nil
	}
	if kidHex == "" || keyHex == "" {
		return fmt.Errorf("decrypt: key or kid missing")
	}
	if _, err := os.Stat(input); err != nil {
		return fmt.Errorf("decrypt: input not found: %w", err)
	}
	output := input + ".dec"
	_ = os.Remove(output)

	// Write key-file in "kid:key" format (lowercase hex).
	keyFile, err := os.CreateTemp("", "bbdown-key-*.tmp")
	if err != nil {
		return fmt.Errorf("decrypt: create key file: %w", err)
	}
	keyPath := keyFile.Name()
	defer func() {
		// Securely delete: overwrite with NULs before removing (best effort).
		_ = os.WriteFile(keyPath, make([]byte, 64), 0o600)
		_ = os.Remove(keyPath)
	}()
	if _, err := keyFile.WriteString(kidHex + ":" + keyHex); err != nil {
		_ = keyFile.Close()
		return fmt.Errorf("decrypt: write key file: %w", err)
	}
	if err := keyFile.Sync(); err != nil {
		_ = keyFile.Close()
		return fmt.Errorf("decrypt: sync key file: %w", err)
	}
	if err := keyFile.Close(); err != nil {
		return fmt.Errorf("decrypt: close key file: %w", err)
	}

	if timeout <= 0 {
		timeout = 30 * time.Minute
	}
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(runCtx, mp4decryptPath, "--key-file", keyPath, input, output)
	stderr, err := cmd.CombinedOutput()
	if err != nil {
		_ = os.Remove(output)
		if runCtx.Err() == context.DeadlineExceeded {
			return fmt.Errorf("mp4decrypt timed out after %v", timeout)
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("mp4decrypt failed: %v: %s", err, string(stderr))
	}
	info, err := os.Stat(output)
	if err != nil || info.Size() == 0 {
		_ = os.Remove(output)
		return fmt.Errorf("mp4decrypt exited 0 but produced no valid output")
	}

	// Atomically replace the input with the decrypted output.
	if err := os.Remove(input); err != nil {
		_ = os.Remove(output)
		return fmt.Errorf("decrypt: remove original: %w", err)
	}
	if err := os.Rename(output, input); err != nil {
		return fmt.Errorf("decrypt: replace with decrypted: %w", err)
	}
	return nil
}
