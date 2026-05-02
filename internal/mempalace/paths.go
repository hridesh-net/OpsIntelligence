package mempalace

import (
	"os"
	"path/filepath"
	"runtime"

	"github.com/opsintelligence/opsintelligence/internal/dirs"
)

// ManagedBaseDir returns the MemPalace directory under stateDir.
func ManagedBaseDir(stateDir string) string {
	return dirs.New(stateDir).MemPalace
}

// ManagedVenvRoot returns the path to the dedicated Python venv for MemPalace.
func ManagedVenvRoot(stateDir string) string {
	return dirs.New(stateDir).MemPalaceVenv()
}

// ManagedWorldDir is the default "world" path passed to `mempalace init`.
func ManagedWorldDir(stateDir string) string {
	return dirs.New(stateDir).MemPalaceWorld()
}

// WorldInitMarker is written after a successful `mempalace init`.
func WorldInitMarker(stateDir string) string {
	return dirs.New(stateDir).MemPalaceInitMarker()
}

// VenvInterpreter returns the python executable inside venvRoot.
// venvRoot is the path returned by ManagedVenvRoot.
func VenvInterpreter(venvRoot string) string {
	if runtime.GOOS == "windows" {
		return filepath.Join(venvRoot, "Scripts", "python.exe")
	}
	p := filepath.Join(venvRoot, "bin", "python3")
	if _, err := os.Stat(p); err == nil {
		return p
	}
	return filepath.Join(venvRoot, "bin", "python")
}

// VenvMempalaceCLI returns the mempalace console script path inside the venv.
func VenvMempalaceCLI(venvRoot string) string {
	if runtime.GOOS == "windows" {
		return filepath.Join(venvRoot, "Scripts", "mempalace.exe")
	}
	return filepath.Join(venvRoot, "bin", "mempalace")
}
