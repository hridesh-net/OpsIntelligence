package config

import (
	"strings"
	"testing"

	"github.com/opsintelligence/opsintelligence/internal/dirs"
)

func TestMemPalaceBootstrapConfig_stateDirAndPaths(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	cfg := MemPalaceBootstrapConfig(root)
	layout := dirs.New(root)
	if cfg.StateDir != root {
		t.Fatalf("StateDir: got %q want %q", cfg.StateDir, root)
	}
	if cfg.Memory.EpisodicDBPath != layout.EpisodicDB() {
		t.Fatalf("EpisodicDBPath: got %q want %q", cfg.Memory.EpisodicDBPath, layout.EpisodicDB())
	}
	if !strings.Contains(cfg.Memory.SemanticDBPath, root) {
		t.Fatalf("SemanticDBPath %q should be under state dir", cfg.Memory.SemanticDBPath)
	}
	if cfg.Agent.LocalIntel.CacheDir != layout.LocalIntel {
		t.Fatalf("LocalIntel.CacheDir: got %q want %q", cfg.Agent.LocalIntel.CacheDir, layout.LocalIntel)
	}
	if cfg.Agent.RunTraceFile != layout.AgentRunTrace() {
		t.Fatalf("RunTraceFile: got %q want %q (auto tracing under state_dir)", cfg.Agent.RunTraceFile, layout.AgentRunTrace())
	}
	if cfg.Agent.RunTraceMode != "auto" {
		t.Fatalf("RunTraceMode: got %q want auto", cfg.Agent.RunTraceMode)
	}
}
