package tuibridge

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
)

// resolveBinary returns the on-disk path to the Rust TUI binary, extracting
// the embedded bytes into a per-hash cache directory on first use. If
// devBinaryPath is non-empty, it is returned directly without extraction.
func resolveBinary(devBinaryPath string) (string, error) {
	if devBinaryPath != "" {
		if _, err := os.Stat(devBinaryPath); err != nil {
			return "", fmt.Errorf("--tui-dev-binary %q: %w", devBinaryPath, err)
		}
		return devBinaryPath, nil
	}
	data, err := readEmbeddedBinary()
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	hash := hex.EncodeToString(sum[:8])

	cacheRoot, err := os.UserCacheDir()
	if err != nil {
		cacheRoot = os.TempDir()
	}
	dir := filepath.Join(cacheRoot, "opsintelligence", "tui", hash)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("create tui cache dir: %w", err)
	}
	target := filepath.Join(dir, assetName())
	if existing, err := os.ReadFile(target); err == nil && len(existing) == len(data) {
		return target, nil
	}
	tmp, err := os.CreateTemp(dir, ".opsintel-tui-*")
	if err != nil {
		return "", fmt.Errorf("create tmp tui binary: %w", err)
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return "", err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return "", err
	}
	if err := os.Chmod(tmpName, 0o755); err != nil {
		os.Remove(tmpName)
		return "", err
	}
	if err := os.Rename(tmpName, target); err != nil {
		os.Remove(tmpName)
		return "", fmt.Errorf("install tui binary: %w", err)
	}
	return target, nil
}
