package main

// mempalace_setup.go — pure-Go helpers for the MemPalace + Local Gemma
// provisioning side-effects, extracted from cmd/opsintelligence/tui/setup.go
// in Phase 5d so the heavy huh-based setup wizard can be deleted without
// losing the bootstrap logic.
//
// None of these touch the UI; they shell out to python3 / venv / pip and
// the embedded opsintelligence binary itself.

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
)

// SetupOptions configures the bootstrap helpers.
type SetupOptions struct {
	StateDir        string
	GGUFPath        string
	BootstrapPython string
}

// SetupResult mirrors the legacy tui.SetupResult.
type SetupResult struct {
	MemPalaceEnabled bool
	GemmaEnabled     bool
	GGUFPath         string
}

func pythonBin() string {
	if runtime.GOOS == "windows" {
		return filepath.Join("Scripts", "python.exe")
	}
	return filepath.Join("bin", "python3")
}

func pythonAvailable(override string) bool {
	py := override
	if py == "" {
		py = "python3"
	}
	_, err := exec.LookPath(py)
	return err == nil
}

func shellRun(ctx context.Context, name string, args ...string) error {
	cmd := exec.CommandContext(ctx, name, args...)
	return cmd.Run()
}

func runMemPalaceSetup(ctx context.Context, opts SetupOptions) error {
	py := opts.BootstrapPython
	if py == "" {
		py = "python3"
	}
	venvRoot := filepath.Join(opts.StateDir, "mempalace", "venv")
	world := filepath.Join(opts.StateDir, "mempalace", "world")

	if err := os.MkdirAll(venvRoot, 0o755); err != nil {
		return err
	}
	if _, err := os.Stat(filepath.Join(venvRoot, pythonBin())); err != nil {
		if err := shellRun(ctx, py, "-m", "venv", venvRoot); err != nil {
			return fmt.Errorf("create venv: %w", err)
		}
	}
	vpy := filepath.Join(venvRoot, pythonBin())
	if err := shellRun(ctx, vpy, "-c", "import mempalace"); err != nil {
		if err := shellRun(ctx, vpy, "-m", "pip", "install", "-q", "-U", "mempalace"); err != nil {
			return fmt.Errorf("pip install: %w", err)
		}
	}
	marker := filepath.Join(opts.StateDir, "mempalace", ".world_initialized")
	if _, err := os.Stat(marker); err != nil {
		if err := os.MkdirAll(world, 0o755); err != nil {
			return err
		}
		cli := filepath.Join(venvRoot, "bin", "mempalace")
		if runtime.GOOS == "windows" {
			cli = filepath.Join(venvRoot, "Scripts", "mempalace.exe")
		}
		initErr := shellRun(ctx, cli, "init", world, "--yes")
		if initErr != nil {
			initErr = shellRun(ctx, vpy, "-m", "mempalace", "init", world, "--yes")
		}
		if initErr != nil {
			return fmt.Errorf("mempalace init: %w", initErr)
		}
		_ = os.WriteFile(marker, []byte("1\n"), 0o644)
	}
	return nil
}

func runGemmaSetup(ctx context.Context, dest string) error {
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return err
	}
	exe, err := os.Executable()
	if err != nil {
		exe = "opsintelligence"
	}
	return shellRun(ctx, exe, "local-intel", "setup", "--gguf-path", dest)
}
